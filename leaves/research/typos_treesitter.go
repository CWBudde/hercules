//go:build !babelfish
// +build !babelfish

package research

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/levenshtein"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/meko-christian/hercules/internal/yaml"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// TyposDatasetBuilder collects pairs of typo-fix in source code identifiers.
type TyposDatasetBuilder struct {
	core.NoopMerger

	// MaximumAllowedDistance is the maximum Levenshtein distance between two identifiers
	// to consider them a typo-fix pair.
	MaximumAllowedDistance int

	// typos stores the found typo-fix pairs.
	typos []Typo
	// lcontext is the Context for measuring Levenshtein distance between lines.
	lcontext *levenshtein.Context
	// remote carries the repository remote URL (for debugging)
	remote string

	extractor *ast_items.TreeSitterExtractor
	l         core.Logger
}

// TyposResult is returned by TyposDatasetBuilder.Finalize() and carries the found typo-fix
// pairs of identifiers.
type TyposResult struct {
	Typos []Typo
}

// Typo carries the information about a typo-fix pair.
type Typo struct {
	Wrong   string
	Correct string
	Commit  plumbing.Hash
	File    string
	Line    int
}

const (
	// DefaultMaximumAllowedTypoDistance is the default value of the maximum Levenshtein distance
	// between two identifiers to consider them a typo-fix pair.
	DefaultMaximumAllowedTypoDistance = 4
	// ConfigTyposDatasetMaximumAllowedDistance is the name of the configuration option
	// (`TyposDatasetBuilder.Configure()`) which sets the maximum Levenshtein distance between
	// two identifiers to consider them a typo-fix pair.
	ConfigTyposDatasetMaximumAllowedDistance = "TyposDatasetBuilder.MaximumAllowedDistance"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (tdb *TyposDatasetBuilder) Name() string {
	return "TyposDataset"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (tdb *TyposDatasetBuilder) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (tdb *TyposDatasetBuilder) Requires() []string {
	return []string{
		items.DependencyTreeChanges, items.DependencyFileDiff, items.DependencyBlobCache,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (tdb *TyposDatasetBuilder) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name: ConfigTyposDatasetMaximumAllowedDistance,
			Description: "Maximum Levenshtein distance between two identifiers to consider them " +
				"a typo-fix pair.",
			Flag:    "typos-max-distance",
			Type:    core.IntConfigurationOption,
			Default: DefaultMaximumAllowedTypoDistance,
		},
	}
	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (tdb *TyposDatasetBuilder) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		tdb.l = l
	}
	if val, exists := facts[ConfigTyposDatasetMaximumAllowedDistance].(int); exists {
		tdb.MaximumAllowedDistance = val
	}
	return nil
}

func (*TyposDatasetBuilder) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (tdb *TyposDatasetBuilder) Flag() string {
	return "typos-dataset"
}

// Description returns the text which explains what the analysis is doing.
func (tdb *TyposDatasetBuilder) Description() string {
	return "Extracts typo-fix identifier pairs from source code in commit diffs."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (tdb *TyposDatasetBuilder) Initialize(repository *git.Repository) error {
	tdb.l = core.NewLogger()
	if tdb.MaximumAllowedDistance <= 0 {
		tdb.MaximumAllowedDistance = DefaultMaximumAllowedTypoDistance
	}
	tdb.lcontext = &levenshtein.Context{}
	tdb.remote = core.GetSensibleRemote(repository)
	tdb.extractor = ast_items.NewTreeSitterExtractor()
	return nil
}

type candidate struct {
	Before int
	After  int
}

func collectIdentifiersByLine(nodes []ast_items.Node, focused map[int]bool) map[int][]string {
	result := map[int][]string{}
	for _, node := range nodes {
		if node.Name == "" {
			continue
		}
		line := node.StartLine - 1
		if focused[line] {
			result[line] = append(result[line], node.Name)
		}
	}
	return result
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (tdb *TyposDatasetBuilder) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	// Skip merge commits
	if isMerge, exists := deps[core.DependencyIsMerge].(bool); exists && isMerge {
		return nil, nil
	}
	commit := deps[core.DependencyCommit].(*object.Commit).Hash
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	diffs := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		if action != merkletrie.Modify {
			continue
		}
		before := cache[change.From.TreeEntry.Hash]
		after := cache[change.To.TreeEntry.Hash]
		if before == nil || after == nil {
			continue
		}
		diff, exists := diffs[change.To.Name]
		if !exists {
			continue
		}
		linesBefore := bytes.Split(before.Data, []byte{'\n'})
		linesAfter := bytes.Split(after.Data, []byte{'\n'})
		var lineNumBefore, lineNumAfter int
		var candidates []candidate
		focusedLinesBefore := map[int]bool{}
		focusedLinesAfter := map[int]bool{}
		removedSize := 0
		for _, edit := range diff.Diffs {
			size := utf8.RuneCountInString(edit.Text)
			switch edit.Type {
			case diffmatchpatch.DiffDelete:
				lineNumBefore += size
				removedSize = size
			case diffmatchpatch.DiffInsert:
				if size == removedSize {
					for i := 0; i < size; i++ {
						lb := lineNumBefore - size + i
						la := lineNumAfter + i
						if lb < 0 || lb >= len(linesBefore) || la < 0 || la >= len(linesAfter) {
							continue
						}
						dist := tdb.lcontext.Distance(string(linesBefore[lb]), string(linesAfter[la]))
						if dist <= tdb.MaximumAllowedDistance {
							candidates = append(candidates, candidate{lb, la})
							focusedLinesBefore[lb] = true
							focusedLinesAfter[la] = true
						}
					}
				}
				lineNumAfter += size
				removedSize = 0
			case diffmatchpatch.DiffEqual:
				lineNumBefore += size
				lineNumAfter += size
				removedSize = 0
			}
		}
		if len(candidates) == 0 {
			continue
		}

		beforeIdentifiers, err := tdb.extractor.ExtractIdentifiers(change.From.Name, before.Data)
		if err != nil {
			tdb.l.Warnf("repo %s commit %s file %s failed to parse before AST: %v",
				tdb.remote, commit.String(), change.From.Name, err)
			continue
		}
		afterIdentifiers, err := tdb.extractor.ExtractIdentifiers(change.To.Name, after.Data)
		if err != nil {
			tdb.l.Warnf("repo %s commit %s file %s failed to parse after AST: %v",
				tdb.remote, commit.String(), change.To.Name, err)
			continue
		}
		removedIdentifiers := collectIdentifiersByLine(beforeIdentifiers, focusedLinesBefore)
		addedIdentifiers := collectIdentifiersByLine(afterIdentifiers, focusedLinesAfter)
		for _, c := range candidates {
			idsBefore := removedIdentifiers[c.Before]
			idsAfter := addedIdentifiers[c.After]
			if len(idsBefore) == 1 && len(idsAfter) == 1 && idsBefore[0] != idsAfter[0] {
				tdb.typos = append(tdb.typos, Typo{
					Wrong:   idsBefore[0],
					Correct: idsAfter[0],
					Commit:  commit,
					File:    change.To.Name,
					Line:    c.After,
				})
			}
		}
	}
	return nil, nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (tdb *TyposDatasetBuilder) Finalize() interface{} {
	// deduplicate
	typos := make([]Typo, 0, len(tdb.typos))
	pairs := map[string]bool{}
	for _, t := range tdb.typos {
		id := t.Wrong + "|" + t.Correct
		if _, exists := pairs[id]; !exists {
			pairs[id] = true
			typos = append(typos, t)
		}
	}
	return TyposResult{Typos: typos}
}

// Fork clones this pipeline item.
func (tdb *TyposDatasetBuilder) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(tdb, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (tdb *TyposDatasetBuilder) Serialize(result interface{}, binary bool, writer io.Writer) error {
	commitsResult := result.(TyposResult)
	if binary {
		return tdb.serializeBinary(&commitsResult, writer)
	}
	tdb.serializeText(&commitsResult, writer)
	return nil
}

func (tdb *TyposDatasetBuilder) serializeText(result *TyposResult, writer io.Writer) {
	for _, t := range result.Typos {
		fmt.Fprintf(writer, "  - wrong: %s\n", yaml.SafeString(t.Wrong))
		fmt.Fprintf(writer, "    correct: %s\n", yaml.SafeString(t.Correct))
		fmt.Fprintf(writer, "    commit: %s\n", t.Commit.String())
		fmt.Fprintf(writer, "    file: %s\n", yaml.SafeString(t.File))
		fmt.Fprintf(writer, "    line: %d\n", t.Line)
	}
}

func (tdb *TyposDatasetBuilder) serializeBinary(result *TyposResult, writer io.Writer) error {
	message := pb.TyposDataset{}
	message.Typos = make([]*pb.Typo, len(result.Typos))
	for i, t := range result.Typos {
		message.Typos[i] = &pb.Typo{
			Wrong:   t.Wrong,
			Correct: t.Correct,
			Commit:  t.Commit.String(),
			File:    t.File,
			Line:    int32(t.Line),
		}
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

func init() {
	core.Registry.Register(&TyposDatasetBuilder{})
}
