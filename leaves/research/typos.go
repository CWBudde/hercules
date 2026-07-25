package research

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/levenshtein"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	ast_items "github.com/cwbudde/hercules/internal/plumbing/ast"
	"github.com/cwbudde/hercules/internal/yaml"
)

var (
	errInvalidCommitDependency      = errors.New("invalid commit dependency")
	errInvalidBlobCacheDependency   = errors.New("invalid blob cache dependency")
	errInvalidFileDiffDependency    = errors.New("invalid file diff dependency")
	errInvalidTreeChangesDependency = errors.New("invalid tree changes dependency")
	errInvalidTyposResult           = errors.New("invalid typos result")
	errTypoLineRange                = errors.New("typo line exceeds the protobuf int32 range")
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
func (tdb *TyposDatasetBuilder) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		tdb.l = l
	}

	if val, exists := facts[ConfigTyposDatasetMaximumAllowedDistance].(int); exists {
		tdb.MaximumAllowedDistance = val
	}

	return nil
}

func (*TyposDatasetBuilder) ConfigureUpstream(facts map[string]any) error {
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
func (tdb *TyposDatasetBuilder) Consume(deps map[string]any) (map[string]any, error) {
	// Skip merge commits
	if isMerge, exists := deps[core.DependencyIsMerge].(bool); exists && isMerge {
		return noDependencies(), nil
	}

	commitObject, ok := deps[core.DependencyCommit].(*object.Commit)
	if !ok {
		return nil, errInvalidCommitDependency
	}

	commit := commitObject.Hash

	cache, ok := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	if !ok {
		return nil, errInvalidBlobCacheDependency
	}

	diffs, ok := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)
	if !ok {
		return nil, errInvalidFileDiffDependency
	}

	changes, ok := deps[items.DependencyTreeChanges].(object.Changes)
	if !ok {
		return nil, errInvalidTreeChangesDependency
	}

	for _, change := range changes {
		typos, err := tdb.typosFromChange(commit, change, cache, diffs)
		if err != nil {
			return nil, err
		}

		tdb.typos = append(tdb.typos, typos...)
	}

	return noDependencies(), nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.

func (tdb *TyposDatasetBuilder) Finalize() any {
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
func (tdb *TyposDatasetBuilder) Serialize(result any, binary bool, writer io.Writer) error {
	commitsResult, ok := result.(TyposResult)
	if !ok {
		return errInvalidTyposResult
	}

	if binary {
		return tdb.serializeBinary(&commitsResult, writer)
	}

	tdb.serializeText(&commitsResult, writer)

	return nil
}

func (tdb *TyposDatasetBuilder) typosFromChange(
	commit plumbing.Hash,
	change *object.Change,
	cache map[plumbing.Hash]*items.CachedBlob,
	diffs map[string]items.FileDiffData,
) ([]Typo, error) {
	action, err := change.Action()
	if err != nil {
		return nil, fmt.Errorf("determine change action: %w", err)
	}

	if action != merkletrie.Modify {
		return nil, nil
	}

	before := cache[change.From.TreeEntry.Hash]
	after := cache[change.To.TreeEntry.Hash]

	diff, exists := diffs[change.To.Name]
	if before == nil || after == nil || !exists {
		return nil, nil
	}

	candidates, focusedBefore, focusedAfter := tdb.typoCandidates(before.Data, after.Data, diff)
	if len(candidates) == 0 {
		return nil, nil
	}

	beforeIDs, err := tdb.identifiersForTypo(commit, change.From.Name, "before", before.Data, focusedBefore)
	if err != nil {
		return nil, fmt.Errorf("extract identifiers before change: %w", err)
	}

	afterIDs, err := tdb.identifiersForTypo(commit, change.To.Name, "after", after.Data, focusedAfter)
	if err != nil {
		return nil, fmt.Errorf("extract identifiers after change: %w", err)
	}
	var typos []Typo

	for _, candidate := range candidates {
		removed, added := beforeIDs[candidate.Before], afterIDs[candidate.After]
		if len(removed) == 1 && len(added) == 1 && removed[0] != added[0] {
			typos = append(typos, Typo{
				Wrong: removed[0], Correct: added[0], Commit: commit,
				File: change.To.Name, Line: candidate.After,
			})
		}
	}

	return typos, nil
}

func (tdb *TyposDatasetBuilder) typoCandidates(
	before, after []byte, diff items.FileDiffData,
) ([]candidate, map[int]bool, map[int]bool) {
	beforeLines, afterLines := bytes.Split(before, []byte{'\n'}), bytes.Split(after, []byte{'\n'})
	var candidates []candidate
	focusedBefore, focusedAfter := map[int]bool{}, map[int]bool{}
	var beforeLine, afterLine, removedSize int

	for _, edit := range diff.Diffs {
		size := utf8.RuneCountInString(edit.Text)
		switch edit.Type {
		case diffmatchpatch.DiffDelete:
			beforeLine += size
			removedSize = size
		case diffmatchpatch.DiffInsert:
			if size == removedSize {
				for i := range size {
					left, right := beforeLine-size+i, afterLine+i
					if left >= 0 && left < len(beforeLines) && right >= 0 && right < len(afterLines) &&
						tdb.lcontext.Distance(string(beforeLines[left]), string(afterLines[right])) <=
							tdb.MaximumAllowedDistance {
						candidates = append(candidates, candidate{left, right})
						focusedBefore[left], focusedAfter[right] = true, true
					}
				}
			}

			afterLine += size
			removedSize = 0
		case diffmatchpatch.DiffEqual:
			beforeLine, afterLine, removedSize = beforeLine+size, afterLine+size, 0
		}
	}

	return candidates, focusedBefore, focusedAfter
}

func (tdb *TyposDatasetBuilder) identifiersForTypo(
	commit plumbing.Hash, file, phase string, data []byte, focused map[int]bool,
) (map[int][]string, error) {
	nodes, err := tdb.extractor.ExtractIdentifiers(file, data)
	if err != nil {
		tdb.l.Warnf(
			"repo %s commit %s file %s failed to parse %s AST: %v",
			tdb.remote, commit.String(), file, phase, err,
		)

		return nil, fmt.Errorf("extract identifiers: %w", err)
	}

	return collectIdentifiersByLine(nodes, focused), nil
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
	for typoIndex, typo := range result.Typos {
		if typo.Line < math.MinInt32 || typo.Line > math.MaxInt32 {
			return fmt.Errorf("%w: %d", errTypoLineRange, typo.Line)
		}

		message.Typos[typoIndex] = &pb.Typo{
			Wrong:   typo.Wrong,
			Correct: typo.Correct,
			Commit:  typo.Commit.String(),
			File:    typo.File,
			Line:    int32(typo.Line),
		}
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal typos result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write typos result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&TyposDatasetBuilder{})
