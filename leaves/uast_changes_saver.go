package leaves

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	ast_items "github.com/cwbudde/hercules/internal/plumbing/ast"
)

const (
	// ConfigUASTChangesSaverOutputPath controls where --dump-uast-changes writes artifacts.
	ConfigUASTChangesSaverOutputPath = "ChangesSaver.OutputPath"
)

var errUnexpectedUASTChangesResult = errors.New("result is not []UASTChangeRecord")

// UASTChangeRecord points to files written for a changed source file.
// Field names are intentionally preserved for compatibility with historical output keys.
type UASTChangeRecord struct {
	FileName   string `json:"file"`
	SrcBefore  string `json:"src0"`
	SrcAfter   string `json:"src1"`
	UASTBefore string `json:"uast0"`
	UASTAfter  string `json:"uast1"`
}

// UASTChangesSaver dumps modified file sources and corresponding tree-sitter AST node lists.
// It replaces the old Babelfish-backed --dump-uast-changes flow.
type UASTChangesSaver struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	OutputPath string
	result     []UASTChangeRecord

	l core.Logger
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (saver *UASTChangesSaver) Name() string {
	return "UASTChangesSaver"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (saver *UASTChangesSaver) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (saver *UASTChangesSaver) Requires() []string {
	return []string{items.DependencyTreeChanges, items.DependencyBlobCache}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (saver *UASTChangesSaver) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigUASTChangesSaverOutputPath,
			Description: "The target directory where to store changed source and AST files.",
			Flag:        "changed-uast-dir",
			Type:        core.PathConfigurationOption,
			Default:     ".",
		},
	}
}

// Flag for the command line switch which enables this analysis.
func (saver *UASTChangesSaver) Flag() string {
	return "dump-uast-changes"
}

// Description returns the text which explains what the analysis is doing.
func (saver *UASTChangesSaver) Description() string {
	return "Saves tree-sitter ASTs and file contents on disk for each modified file."
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (saver *UASTChangesSaver) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		saver.l = l
	}

	if val, exists := facts[ConfigUASTChangesSaverOutputPath].(string); exists {
		saver.OutputPath = val
	}

	return nil
}

func (*UASTChangesSaver) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume() calls.
func (saver *UASTChangesSaver) Initialize(repository *git.Repository) error {
	saver.l = core.NewLogger()
	saver.result = nil
	saver.OneShotMergeProcessor.Initialize()

	if saver.OutputPath == "" {
		saver.OutputPath = "."
	}

	err := os.MkdirAll(saver.OutputPath, 0o755)
	if err != nil {
		return fmt.Errorf("create UAST changes output directory %q: %w", saver.OutputPath, err)
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
func (saver *UASTChangesSaver) Consume(deps map[string]any) (map[string]any, error) {
	if !saver.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	commit := deps[core.DependencyCommit].(*object.Commit)
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)

	for i, change := range changes {
		record, keep, err := saver.processChange(commit, i, change, cache)
		if err != nil {
			return nil, err
		}

		if keep {
			saver.result = append(saver.result, record)
		}
	}

	return nil, nil
}

func eitherBlobIsBinary(blobs ...*items.CachedBlob) (bool, error) {
	for _, blob := range blobs {
		_, err := blob.CountLines()
		if errors.Is(err, items.ErrBinary) {
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("count blob lines: %w", err)
		}
	}

	return false, nil
}

func shortHash(hash plumbing.Hash) string {
	raw := hash.String()
	if len(raw) > 12 {
		return raw[:12]
	}

	return raw
}

// Fork clones this PipelineItem.
func (saver *UASTChangesSaver) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(saver, n)
}

// Finalize returns the result of the analysis.
func (saver *UASTChangesSaver) Finalize() any {
	return saver.result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (saver *UASTChangesSaver) Serialize(result any, binary bool, writer io.Writer) error {
	records, ok := result.([]UASTChangeRecord)
	if !ok {
		return fmt.Errorf("%w: %T", errUnexpectedUASTChangesResult, result)
	}

	if binary {
		payload := map[string]any{"changes": records}

		err := json.NewEncoder(writer).Encode(payload)
		if err != nil {
			return fmt.Errorf("encode UAST changes JSON: %w", err)
		}

		return nil
	}

	for _, sc := range records {
		_, err := fmt.Fprintf(writer, "  - {file: %s, src0: %s, src1: %s, uast0: %s, uast1: %s}\n",
			sc.FileName, sc.SrcBefore, sc.SrcAfter, sc.UASTBefore, sc.UASTAfter)
		if err != nil {
			return fmt.Errorf("write UAST changes text: %w", err)
		}
	}

	return nil
}

func (saver *UASTChangesSaver) processChange(
	commit *object.Commit,
	index int,
	change *object.Change,
	cache map[plumbing.Hash]*items.CachedBlob,
) (UASTChangeRecord, bool, error) {
	action, err := change.Action()
	if err != nil {
		return UASTChangeRecord{}, false, fmt.Errorf("determine action for tree change: %w", err)
	}

	if action != merkletrie.Modify {
		return UASTChangeRecord{}, false, nil
	}

	fromBlob := cache[change.From.TreeEntry.Hash]

	toBlob := cache[change.To.TreeEntry.Hash]
	if fromBlob == nil || toBlob == nil {
		return UASTChangeRecord{}, false, nil
	}

	binary, err := eitherBlobIsBinary(fromBlob, toBlob)
	if err != nil || binary {
		return UASTChangeRecord{}, false, err
	}

	beforeNodes, ok := saver.extractChangeNodes(commit, change.From.Name, "before", fromBlob.Data)
	if !ok {
		return UASTChangeRecord{}, false, nil
	}

	afterNodes, ok := saver.extractChangeNodes(commit, change.To.Name, "after", toBlob.Data)
	if !ok || len(beforeNodes) == 0 && len(afterNodes) == 0 {
		return UASTChangeRecord{}, false, nil
	}

	record, err := saver.dumpChangeFiles(
		commit.Hash, index, change.To.Name,
		change.From.TreeEntry.Hash, fromBlob.Data, beforeNodes,
		change.To.TreeEntry.Hash, toBlob.Data, afterNodes,
	)

	return record, err == nil, err
}

func (saver *UASTChangesSaver) extractChangeNodes(
	commit *object.Commit, name, phase string, data []byte,
) ([]ast_items.Node, bool) {
	nodes, err := ast_items.ExtractNamedNodes(name, data)
	if err != nil {
		saver.l.Warnf(
			"UASTChangesSaver: commit %s file %s %s-parse failed: %v",
			commit.Hash.String(), name, phase, err,
		)

		return nil, false
	}

	return nodes, true
}

func (saver *UASTChangesSaver) dumpChangeFiles(
	commitHash plumbing.Hash,
	changeIndex int,
	fileName string,
	fromHash plumbing.Hash,
	srcBefore []byte,
	nodesBefore []ast_items.Node,
	toHash plumbing.Hash,
	srcAfter []byte,
	nodesAfter []ast_items.Node,
) (UASTChangeRecord, error) {
	prefix := fmt.Sprintf("%s_%03d", shortHash(commitHash), changeIndex)
	srcBeforePath := filepath.Join(saver.OutputPath, fmt.Sprintf("%s_before_%s.src", prefix, shortHash(fromHash)))
	srcAfterPath := filepath.Join(saver.OutputPath, fmt.Sprintf("%s_after_%s.src", prefix, shortHash(toHash)))
	uastBeforePath := filepath.Join(saver.OutputPath, fmt.Sprintf("%s_before_%s.ast.json", prefix, shortHash(fromHash)))
	uastAfterPath := filepath.Join(saver.OutputPath, fmt.Sprintf("%s_after_%s.ast.json", prefix, shortHash(toHash)))

	err := os.WriteFile(srcBeforePath, srcBefore, 0o600)
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("write source before change to %q: %w", srcBeforePath, err)
	}

	err = os.WriteFile(srcAfterPath, srcAfter, 0o600)
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("write source after change to %q: %w", srcAfterPath, err)
	}

	beforeJSON, err := json.MarshalIndent(nodesBefore, "", "  ")
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("marshal AST before change for %q: %w", fileName, err)
	}

	afterJSON, err := json.MarshalIndent(nodesAfter, "", "  ")
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("marshal AST after change for %q: %w", fileName, err)
	}

	err = os.WriteFile(uastBeforePath, beforeJSON, 0o600)
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("write AST before change to %q: %w", uastBeforePath, err)
	}

	err = os.WriteFile(uastAfterPath, afterJSON, 0o600)
	if err != nil {
		return UASTChangeRecord{}, fmt.Errorf("write AST after change to %q: %w", uastAfterPath, err)
	}

	return UASTChangeRecord{
		FileName:   fileName,
		SrcBefore:  srcBeforePath,
		SrcAfter:   srcAfterPath,
		UASTBefore: uastBeforePath,
		UASTAfter:  uastAfterPath,
	}, nil
}

var _ = core.RegisterPipelineItem(&UASTChangesSaver{})
