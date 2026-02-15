package plumbing

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/meko-christian/hercules/internal/core"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// FileDiff calculates the difference of files which were modified.
// It is a PipelineItem.
type FileDiff struct {
	core.NoopMerger
	CleanupDisabled  bool
	WhitespaceIgnore bool
	RefineDisabled   bool
	Timeout          time.Duration

	l core.Logger
}

const (
	// ConfigFileDiffDisableCleanup is the name of the configuration option (FileDiff.Configure())
	// to suppress diffmatchpatch.DiffCleanupSemanticLossless() which is supposed to improve
	// the human interpretability of diffs.
	ConfigFileDiffDisableCleanup = "FileDiff.NoCleanup"

	// DependencyFileDiff is the name of the dependency provided by FileDiff.
	DependencyFileDiff = "file_diff"

	// ConfigFileWhitespaceIgnore is the name of the configuration option (FileDiff.Configure())
	// to suppress whitespace changes which can pollute the core diff of the files
	ConfigFileWhitespaceIgnore = "FileDiff.WhitespaceIgnore"

	// ConfigFileDiffTimeout is the number of milliseconds a single diff calculation may elapse.
	// We need this timeout to avoid spending too much time comparing big or "bad" files.
	ConfigFileDiffTimeout = "FileDiff.Timeout"

	// ConfigFileDiffDisableRefine disables tree-sitter-based post-processing
	// which tweaks ambiguous insert/equal boundaries for better structural alignment.
	ConfigFileDiffDisableRefine = "FileDiff.NoRefine"
)

// FileDiffData is the type of the dependency provided by FileDiff.
type FileDiffData struct {
	OldLinesOfCode int
	NewLinesOfCode int
	Diffs          []diffmatchpatch.Diff
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (diff *FileDiff) Name() string {
	return "FileDiff"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (diff *FileDiff) Provides() []string {
	return []string{DependencyFileDiff}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (diff *FileDiff) Requires() []string {
	return []string{DependencyTreeChanges, DependencyBlobCache}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (diff *FileDiff) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigFileDiffDisableCleanup,
			Description: "Do not apply additional heuristics to improve diffs.",
			Flag:        "no-diff-cleanup",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		},
		{
			Name:        ConfigFileWhitespaceIgnore,
			Description: "Ignore whitespace when computing diffs.",
			Flag:        "no-diff-whitespace",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		},
		{
			Name:        ConfigFileDiffTimeout,
			Description: "Maximum time in milliseconds a single diff calculation may elapse.",
			Flag:        "diff-timeout",
			Type:        core.IntConfigurationOption,
			Default:     1000,
		},
		{
			Name:        ConfigFileDiffDisableRefine,
			Description: "Disable tree-sitter-based refinement of ambiguous diff boundaries.",
			Flag:        "no-diff-refine",
			Type:        core.BoolConfigurationOption,
			Default:     false,
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (diff *FileDiff) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		diff.l = l
	}
	if val, exists := facts[ConfigFileDiffDisableCleanup].(bool); exists {
		diff.CleanupDisabled = val
	}
	if val, exists := facts[ConfigFileWhitespaceIgnore].(bool); exists {
		diff.WhitespaceIgnore = val
	}
	if val, exists := facts[ConfigFileDiffTimeout].(int); exists {
		if val <= 0 {
			diff.l.Warnf("invalid timeout value: %d", val)
		}
		diff.Timeout = time.Duration(val) * time.Millisecond
	}
	if val, exists := facts[ConfigFileDiffDisableRefine].(bool); exists {
		diff.RefineDisabled = val
	}
	return nil
}

func (*FileDiff) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (diff *FileDiff) Initialize(repository *git.Repository) error {
	diff.l = core.NewLogger()
	return nil
}

func stripWhitespace(str string, ignoreWhitespace bool) string {
	if ignoreWhitespace {
		response := strings.Replace(str, " ", "", -1)
		return response
	}
	return str
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (diff *FileDiff) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	result := map[string]FileDiffData{}
	cache := deps[DependencyBlobCache].(map[plumbing.Hash]*CachedBlob)
	treeDiff := deps[DependencyTreeChanges].(object.Changes)
	for _, change := range treeDiff {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		switch action {
		case merkletrie.Modify:
			blobFrom := cache[change.From.TreeEntry.Hash]
			blobTo := cache[change.To.TreeEntry.Hash]

			// Skip binary files; diffmatchpatch treats them as text and would produce noisy line counts.
			if _, err := blobFrom.CountLines(); err == ErrorBinary {
				continue
			} else if err != nil {
				return nil, err
			}
			if _, err := blobTo.CountLines(); err == ErrorBinary {
				continue
			} else if err != nil {
				return nil, err
			}

			// we are not validating UTF-8 here because for example
			// git/git 4f7770c87ce3c302e1639a7737a6d2531fe4b160 fetch-pack.c is invalid UTF-8
			strFrom, strTo := string(blobFrom.Data), string(blobTo.Data)
			dmp := diffmatchpatch.New()
			dmp.DiffTimeout = diff.Timeout
			src, dst, _ := dmp.DiffLinesToRunes(stripWhitespace(strFrom, diff.WhitespaceIgnore), stripWhitespace(strTo, diff.WhitespaceIgnore))
			diffs := dmp.DiffMainRunes(src, dst, false)
			if !diff.CleanupDisabled {
				diffs = dmp.DiffCleanupMerge(dmp.DiffCleanupSemanticLossless(diffs))
			}
			fileDiffData := FileDiffData{
				OldLinesOfCode: len(src),
				NewLinesOfCode: len(dst),
				Diffs:          diffs,
			}
			if !diff.RefineDisabled {
				fileDiffData = diff.refineWithTreeSitter(change.To.Name, blobTo.Data, fileDiffData)
			}
			result[change.To.Name] = fileDiffData
		default:
			continue
		}
	}
	return map[string]interface{}{DependencyFileDiff: result}, nil
}

func (diff *FileDiff) refineWithTreeSitter(path string, source []byte, original FileDiffData) FileDiffData {
	if original.NewLinesOfCode <= 0 || len(original.Diffs) < 2 {
		return original
	}
	nodes, err := ast_items.ExtractNamedNodes(path, source)
	if err != nil {
		diff.l.Warnf("FileDiff: failed to refine %s: %v", path, err)
		return original
	}
	if len(nodes) == 0 {
		return original
	}
	line2node := make([][]ast_items.Node, original.NewLinesOfCode)
	for _, node := range nodes {
		startLine := node.StartLine
		endLine := node.EndLine
		if startLine < 1 {
			startLine = 1
		}
		if endLine < startLine {
			endLine = startLine
		}
		if startLine > len(line2node) {
			continue
		}
		if endLine > len(line2node) {
			endLine = len(line2node)
		}
		for line := startLine; line <= endLine; line++ {
			line2node[line-1] = append(line2node[line-1], node)
		}
	}
	return refineDiffByNodeDensity(original, line2node)
}

func refineDiffByNodeDensity(original FileDiffData, line2node [][]ast_items.Node) FileDiffData {
	suspicious := map[int][2]int{}
	line := 0
	for i, edit := range original.Diffs {
		if i == len(original.Diffs)-1 {
			break
		}
		if edit.Type == diffmatchpatch.DiffInsert &&
			original.Diffs[i+1].Type == diffmatchpatch.DiffEqual {
			matched := commonPrefixRunes(edit.Text, original.Diffs[i+1].Text)
			if matched > 0 {
				suspicious[i] = [2]int{line, matched}
			}
		}
		if edit.Type != diffmatchpatch.DiffDelete {
			line += utf8.RuneCountInString(edit.Text)
		}
	}
	if len(suspicious) == 0 {
		return original
	}

	refined := FileDiffData{
		OldLinesOfCode: original.OldLinesOfCode,
		NewLinesOfCode: original.NewLinesOfCode,
		Diffs:          make([]diffmatchpatch.Diff, 0, len(original.Diffs)+len(suspicious)),
	}
	skipNext := false
	for i, edit := range original.Diffs {
		if skipNext {
			skipNext = false
			continue
		}
		info, ok := suspicious[i]
		if !ok {
			refined.Diffs = append(refined.Diffs, edit)
			continue
		}
		baseLine := info[0]
		matched := info[1]
		size := utf8.RuneCountInString(edit.Text)
		n1 := countNodesInInterval(line2node, baseLine, baseLine+size)
		n2 := countNodesInInterval(line2node, baseLine+matched, baseLine+size+matched)
		if n1 <= n2 {
			refined.Diffs = append(refined.Diffs, edit)
			continue
		}

		skipNext = true
		runes := []rune(edit.Text)
		refined.Diffs = append(refined.Diffs, diffmatchpatch.Diff{
			Type: diffmatchpatch.DiffEqual,
			Text: string(runes[:matched]),
		})
		refined.Diffs = append(refined.Diffs, diffmatchpatch.Diff{
			Type: diffmatchpatch.DiffInsert,
			Text: string(runes[matched:]) + string(runes[:matched]),
		})
		nextEqual := []rune(original.Diffs[i+1].Text)
		if len(nextEqual) > matched {
			refined.Diffs = append(refined.Diffs, diffmatchpatch.Diff{
				Type: diffmatchpatch.DiffEqual,
				Text: string(nextEqual[matched:]),
			})
		}
	}
	return refined
}

func commonPrefixRunes(left, right string) int {
	lr := []rune(left)
	rr := []rune(right)
	matched := 0
	for matched < len(lr) && matched < len(rr) && lr[matched] == rr[matched] {
		matched++
	}
	return matched
}

func countNodesInInterval(line2node [][]ast_items.Node, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(line2node) {
		end = len(line2node)
	}
	if start >= end {
		return 0
	}
	seen := map[string]struct{}{}
	for i := start; i < end; i++ {
		for _, node := range line2node[i] {
			seen[node.ID] = struct{}{}
		}
	}
	return len(seen)
}

// Fork clones this PipelineItem.
func (diff *FileDiff) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(diff, n)
}

func init() {
	core.Registry.Register(&FileDiff{})
}
