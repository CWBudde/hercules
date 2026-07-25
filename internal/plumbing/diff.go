package plumbing

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	ast_items "github.com/cwbudde/hercules/internal/plumbing/ast"
)

// refineCoverageThreshold is the fraction of new-file lines above which the
// range-limited tree-sitter parse is skipped in favor of a full-file parse.
// Below the threshold, range-limited parsing reduces per-call allocation; above
// it, the parser setup and range bookkeeping outweigh the savings.
const refineCoverageThreshold = 0.5

// refineRangePadding is how many lines of context are added on each side of a
// computed range before the included-ranges parse. Tree-sitter's grammar
// resolution is mildly context-sensitive, so giving the parser a window of
// surrounding tokens can either improve or worsen agreement with the full
// parse depending on what falls into the padded window. Empirically a small
// non-zero pad (a few lines) is helpful; very large pads can amplify drift by
// giving the parser enough partial structure to make confidently-different
// decisions.
const refineRangePadding = 4

// refineRangeMinLines is the minimum new-file line count for which the
// range-limited path is considered in auto mode. Below this threshold the
// full-file parse is already cheap enough that the bookkeeping overhead is
// not worth introducing any potential heuristic drift.
const refineRangeMinLines = 1000

// Refine modes for ConfigFileDiffRefineMode.
const (
	// RefineModeAuto picks between full-file and range-limited parsing using
	// the file size, coverage threshold, and padding heuristics. Default.
	RefineModeAuto = "auto"
	// RefineModeFull always parses the entire file. Restores the pre-Phase-2
	// behavior bit-for-bit; useful when reproducibility outweighs throughput.
	RefineModeFull = "full"
	// RefineModeRange always uses included-ranges parsing when at least one
	// range is computed (still falls back to full when the range list is empty).
	// Maximizes throughput at the cost of any small heuristic drift.
	RefineModeRange = "range"
)

// FileDiff calculates the difference of files which were modified.
// It is a PipelineItem.
type FileDiff struct {
	core.NoopMerger

	CleanupDisabled   bool
	WhitespaceIgnore  bool
	RefineDisabled    bool
	RefineMaxFileSize int
	RefineMaxLines    int
	RefineMode        string
	Timeout           time.Duration

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
	// to suppress whitespace changes which can pollute the core diff of the files.
	ConfigFileWhitespaceIgnore = "FileDiff.WhitespaceIgnore"

	// ConfigFileDiffTimeout is the number of milliseconds a single diff calculation may elapse.
	// We need this timeout to avoid spending too much time comparing big or "bad" files.
	ConfigFileDiffTimeout = "FileDiff.Timeout"

	// ConfigFileDiffDisableRefine disables tree-sitter-based post-processing
	// which tweaks ambiguous insert/equal boundaries for better structural alignment.
	ConfigFileDiffDisableRefine = "FileDiff.NoRefine"

	// ConfigFileDiffRefineMaxFileSize is the maximum file size in bytes for which
	// tree-sitter refinement is applied. Files larger than this threshold are skipped
	// to bound memory usage. 0 means unlimited.
	ConfigFileDiffRefineMaxFileSize = "FileDiff.RefineMaxFileSize"

	// DefaultFileDiffRefineMaxFileSize is 200 KB — large generated files typically
	// exceed this and cause multi-GB memory spikes in the tree-sitter AST cache.
	DefaultFileDiffRefineMaxFileSize = 200 * 1024

	// ConfigFileDiffRefineMaxLines is the maximum line count for which tree-sitter
	// refinement is applied. Files with more lines than this threshold are skipped.
	// 0 means unlimited. Complements ConfigFileDiffRefineMaxFileSize for files that
	// are small in bytes but large in line count (e.g. minified or generated files).
	ConfigFileDiffRefineMaxLines = "FileDiff.RefineMaxLines"

	// DefaultFileDiffRefineMaxLines is 5000 lines.
	DefaultFileDiffRefineMaxLines = 5000

	// ConfigFileDiffRefineMode selects the refinement parsing strategy. See the
	// RefineMode* constants for accepted values.
	ConfigFileDiffRefineMode = "FileDiff.RefineMode"
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
		{
			Name:        ConfigFileDiffRefineMaxFileSize,
			Description: "Maximum file size in bytes for tree-sitter diff refinement. Files larger than this are skipped to bound memory usage. 0 means unlimited.",
			Flag:        "diff-refine-max-file-size",
			Type:        core.IntConfigurationOption,
			Default:     DefaultFileDiffRefineMaxFileSize,
		},
		{
			Name:        ConfigFileDiffRefineMaxLines,
			Description: "Maximum line count for tree-sitter diff refinement. Files with more lines than this are skipped to bound memory usage. 0 means unlimited.",
			Flag:        "diff-refine-max-lines",
			Type:        core.IntConfigurationOption,
			Default:     DefaultFileDiffRefineMaxLines,
		},
		{
			Name:        ConfigFileDiffRefineMode,
			Description: "Strategy for tree-sitter refinement parsing: \"auto\" picks per-file (default, fast on large files), \"full\" always parses the entire file (bit-identical pre-Phase-2 output), \"range\" always uses included-ranges parsing.",
			Flag:        "diff-refine-mode",
			Type:        core.StringConfigurationOption,
			Default:     RefineModeAuto,
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (diff *FileDiff) Configure(facts map[string]any) error {
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

	if val, exists := facts[ConfigFileDiffRefineMaxFileSize].(int); exists {
		diff.RefineMaxFileSize = val
	} else {
		diff.RefineMaxFileSize = DefaultFileDiffRefineMaxFileSize
	}

	if val, exists := facts[ConfigFileDiffRefineMaxLines].(int); exists {
		diff.RefineMaxLines = val
	} else {
		diff.RefineMaxLines = DefaultFileDiffRefineMaxLines
	}

	diff.RefineMode = RefineModeAuto

	if val, exists := facts[ConfigFileDiffRefineMode].(string); exists && val != "" {
		switch val {
		case RefineModeAuto, RefineModeFull, RefineModeRange:
			diff.RefineMode = val
		default:
			diff.l.Warnf("invalid diff-refine-mode %q; using %q", val, RefineModeAuto)
		}
	}

	return nil
}

func (*FileDiff) ConfigureUpstream(facts map[string]any) error {
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
		response := strings.ReplaceAll(str, " ", "")
		return response
	}

	return str
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (diff *FileDiff) Consume(deps map[string]any) (map[string]any, error) {
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
			_, err = blobFrom.CountLines()
			if errors.Is(err, ErrBinary) {
				continue
			}
			if err != nil {
				return nil, err
			}

			_, err = blobTo.CountLines()
			if errors.Is(err, ErrBinary) {
				continue
			}
			if err != nil {
				return nil, err
			}

			// we are not validating UTF-8 here because for example
			// git/git 4f7770c87ce3c302e1639a7737a6d2531fe4b160 fetch-pack.c is invalid UTF-8
			strFrom, strTo := string(blobFrom.Data), string(blobTo.Data)
			dmp := diffmatchpatch.New()
			dmp.DiffTimeout = diff.Timeout
			src, dst, _ := dmp.DiffLinesToRunes(
				stripWhitespace(strFrom, diff.WhitespaceIgnore),
				stripWhitespace(strTo, diff.WhitespaceIgnore),
			)

			diffs := dmp.DiffMainRunes(src, dst, false)
			if !diff.CleanupDisabled {
				diffs = dmp.DiffCleanupMerge(dmp.DiffCleanupSemanticLossless(diffs))
			}

			fileDiffData := FileDiffData{
				OldLinesOfCode: len(src),
				NewLinesOfCode: len(dst),
				Diffs:          diffs,
			}
			if !diff.RefineDisabled &&
				(diff.RefineMaxFileSize == 0 || len(blobTo.Data) <= diff.RefineMaxFileSize) &&
				(diff.RefineMaxLines == 0 || fileDiffData.NewLinesOfCode <= diff.RefineMaxLines) {
				fileDiffData = diff.refineWithTreeSitter(change.To.Name, blobTo.Data, fileDiffData)
			}

			result[change.To.Name] = fileDiffData
		default:
			continue
		}
	}

	return map[string]any{DependencyFileDiff: result}, nil
}

func (diff *FileDiff) refineWithTreeSitter(path string, source []byte, original FileDiffData) FileDiffData {
	if original.NewLinesOfCode <= 0 || len(original.Diffs) < 2 {
		return original
	}

	boundaries := collectSuspiciousBoundaries(original.Diffs)
	if len(boundaries) == 0 {
		return original
	}

	ranges := boundariesToRanges(boundaries, original.NewLinesOfCode)

	nodes, err := extractNodesForRefinement(path, source, ranges, original.NewLinesOfCode, diff.RefineMode)
	if err != nil {
		diff.l.Warnf("FileDiff: failed to refine %s: %v", path, err)
		return original
	}

	if len(nodes) == 0 {
		return original
	}

	line2node := buildLineToNodeIndex(nodes, original.NewLinesOfCode)
	refined := refineDiffByNodeDensityWithBoundaries(original, line2node, boundaries)
	refined.Diffs = normalizeDiffs(refined.Diffs)

	return refined
}

// extractNodesForRefinement chooses between full-file and range-limited
// tree-sitter parsing per the configured mode and the size/coverage of the
// suspicious boundaries.
//
// In auto mode the decision is:
//   - empty range list  → full parse (heuristic touches no specific lines).
//   - newLOC < refineRangeMinLines → full parse (small files don't benefit).
//   - padded coverage ≥ refineCoverageThreshold → full parse (savings would be
//     marginal once range bookkeeping is paid).
//   - otherwise → range-limited with refineRangePadding lines of context.
//
// Padding (refineRangePadding) gives tree-sitter enough surrounding tokens to
// resolve grammar-context-sensitive constructs the same way it would in a
// full-file parse, eliminating the rare single-line drift observed without it.
func extractNodesForRefinement(
	path string,
	source []byte,
	ranges []ast_items.LineRange,
	newLOC int,
	mode string,
) ([]ast_items.Node, error) {
	switch mode {
	case RefineModeFull:
		return ast_items.ExtractNamedNodes(path, source)
	case RefineModeRange:
		if len(ranges) == 0 {
			return ast_items.ExtractNamedNodes(path, source)
		}

		return ast_items.ExtractNamedNodesInRanges(path, source, padRanges(ranges, newLOC, refineRangePadding))
	default: // RefineModeAuto and anything unrecognized
		if len(ranges) == 0 || newLOC < refineRangeMinLines {
			return ast_items.ExtractNamedNodes(path, source)
		}

		padded := padRanges(ranges, newLOC, refineRangePadding)
		if coverageRatio(padded, newLOC) >= refineCoverageThreshold {
			return ast_items.ExtractNamedNodes(path, source)
		}

		return ast_items.ExtractNamedNodesInRanges(path, source, padded)
	}
}

// padRanges extends each LineRange by `pad` lines on either side, clamping to
// [1, newLOC]. The result is unsorted and may overlap — buildSitterRanges
// (called inside ExtractNamedNodesInRanges) merges and sorts before passing the
// list to tree-sitter. A non-positive `pad` returns the input untouched.
func padRanges(ranges []ast_items.LineRange, newLOC, pad int) []ast_items.LineRange {
	if pad <= 0 || len(ranges) == 0 {
		return ranges
	}

	out := make([]ast_items.LineRange, 0, len(ranges))
	for _, r := range ranges {
		start := max(r.Start-pad, 1)

		end := min(r.End+pad, newLOC)

		if start > end {
			continue
		}

		out = append(out, ast_items.LineRange{Start: start, End: end})
	}

	return out
}

// buildLineToNodeIndex turns a flat node list into a per-line lookup table the
// node-density heuristic can read. Lines outside any node remain nil; lines
// outside the [1, newLOC] window are silently dropped.
func buildLineToNodeIndex(nodes []ast_items.Node, newLOC int) [][]ast_items.Node {
	line2node := make([][]ast_items.Node, newLOC)

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

	return line2node
}

func normalizeDiffs(diffs []diffmatchpatch.Diff) []diffmatchpatch.Diff {
	// Refinement can legally shift boundaries but must still return a normalized
	// sequence without adjacent operations of the same type.
	return diffmatchpatch.New().DiffCleanupMerge(diffs)
}

// suspiciousBoundary marks a DiffInsert→DiffEqual transition where the insert
// shares a non-empty prefix with the following equal segment. The refinement
// heuristic may reorder these boundaries based on node density.
//
// `line` is 0-indexed against the new file. `size` is the insert's rune length
// (number of new lines, since the diff was produced via DiffLinesToRunes).
// `matched` is the size of the common prefix shared with the following equal.
type suspiciousBoundary struct {
	diffIndex int
	line      int
	size      int
	matched   int
}

// collectSuspiciousBoundaries walks a diff and emits one entry per
// DiffInsert→DiffEqual boundary that has a non-empty common-prefix overlap.
// `line` is 0-indexed against the new file, matching the convention used by
// refineDiffByNodeDensityWithBoundaries.
func collectSuspiciousBoundaries(diffs []diffmatchpatch.Diff) []suspiciousBoundary {
	if len(diffs) < 2 {
		return nil
	}
	var out []suspiciousBoundary
	line := 0

	for i, edit := range diffs {
		if i == len(diffs)-1 {
			break
		}

		size := utf8.RuneCountInString(edit.Text)
		if edit.Type == diffmatchpatch.DiffInsert &&
			diffs[i+1].Type == diffmatchpatch.DiffEqual {
			matched := commonPrefixRunes(edit.Text, diffs[i+1].Text)
			if matched > 0 {
				out = append(out, suspiciousBoundary{
					diffIndex: i,
					line:      line,
					size:      size,
					matched:   matched,
				})
			}
		}

		if edit.Type != diffmatchpatch.DiffDelete {
			line += size
		}
	}

	return out
}

// boundariesToRanges converts each suspicious boundary into the line interval
// the node-density heuristic actually inspects:
// [baseLine+1, baseLine+size+matched] (1-indexed, inclusive).
// Result is unsorted and may overlap; ast_items.ExtractNamedNodesInRanges
// handles normalization internally.
func boundariesToRanges(boundaries []suspiciousBoundary, newLOC int) []ast_items.LineRange {
	if len(boundaries) == 0 || newLOC <= 0 {
		return nil
	}

	out := make([]ast_items.LineRange, 0, len(boundaries))
	for _, b := range boundaries {
		// The heuristic reads countNodesInInterval(line2node, b.line, b.line+size)
		// and (b.line+matched, b.line+size+matched). Their union, expressed in
		// 1-indexed inclusive line numbers, is [b.line+1, b.line+size+matched].
		start := b.line + 1

		end := min(max(b.line+b.size+b.matched, start), newLOC)

		if start > newLOC {
			continue
		}

		out = append(out, ast_items.LineRange{Start: start, End: end})
	}

	return out
}

// coverageRatio reports the fraction of new-file lines covered by the union of
// `ranges` after merging overlaps. Result is in [0, 1].
func coverageRatio(ranges []ast_items.LineRange, newLOC int) float64 {
	if newLOC <= 0 || len(ranges) == 0 {
		return 0
	}

	sorted := make([]ast_items.LineRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	total := 0

	curStart, curEnd := sorted[0].Start, sorted[0].End
	for _, r := range sorted[1:] {
		if r.Start <= curEnd+1 {
			if r.End > curEnd {
				curEnd = r.End
			}

			continue
		}

		total += curEnd - curStart + 1
		curStart, curEnd = r.Start, r.End
	}

	total += curEnd - curStart + 1
	if total > newLOC {
		total = newLOC
	}

	return float64(total) / float64(newLOC)
}

func refineDiffByNodeDensity(original FileDiffData, line2node [][]ast_items.Node) FileDiffData {
	return refineDiffByNodeDensityWithBoundaries(original, line2node, collectSuspiciousBoundaries(original.Diffs))
}

func refineDiffByNodeDensityWithBoundaries(
	original FileDiffData,
	line2node [][]ast_items.Node,
	boundaries []suspiciousBoundary,
) FileDiffData {
	if len(boundaries) == 0 {
		return original
	}

	suspicious := make(map[int]suspiciousBoundary, len(boundaries))
	for _, b := range boundaries {
		suspicious[b.diffIndex] = b
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

		baseLine := info.line
		matched := info.matched
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
