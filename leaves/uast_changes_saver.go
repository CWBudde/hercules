package leaves

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
)

const (
	// ConfigUASTChangesSaverOutputPath controls where --dump-uast-changes writes artifacts.
	ConfigUASTChangesSaverOutputPath = "ChangesSaver.OutputPath"
)

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
func (saver *UASTChangesSaver) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		saver.l = l
	}
	if val, exists := facts[ConfigUASTChangesSaverOutputPath].(string); exists {
		saver.OutputPath = val
	}
	return nil
}

func (*UASTChangesSaver) ConfigureUpstream(facts map[string]interface{}) error {
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
	return os.MkdirAll(saver.OutputPath, 0o755)
}

// Consume runs this PipelineItem on the next commit data.
func (saver *UASTChangesSaver) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !saver.ShouldConsumeCommit(deps) {
		return nil, nil
	}
	commit := deps[core.DependencyCommit].(*object.Commit)
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	for i, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		if action != merkletrie.Modify {
			continue
		}
		fromBlob := cache[change.From.TreeEntry.Hash]
		toBlob := cache[change.To.TreeEntry.Hash]
		if fromBlob == nil || toBlob == nil {
			continue
		}
		if _, err = fromBlob.CountLines(); err == items.ErrorBinary {
			continue
		} else if err != nil {
			return nil, err
		}
		if _, err = toBlob.CountLines(); err == items.ErrorBinary {
			continue
		} else if err != nil {
			return nil, err
		}

		beforeNodes, err := ast_items.ExtractNamedNodes(change.From.Name, fromBlob.Data)
		if err != nil {
			saver.l.Warnf("UASTChangesSaver: commit %s file %s before-parse failed: %v",
				commit.Hash.String(), change.From.Name, err)
			continue
		}
		afterNodes, err := ast_items.ExtractNamedNodes(change.To.Name, toBlob.Data)
		if err != nil {
			saver.l.Warnf("UASTChangesSaver: commit %s file %s after-parse failed: %v",
				commit.Hash.String(), change.To.Name, err)
			continue
		}
		if len(beforeNodes) == 0 && len(afterNodes) == 0 {
			continue
		}

		record, err := saver.dumpChangeFiles(
			commit.Hash, i, change.To.Name,
			change.From.TreeEntry.Hash, fromBlob.Data, beforeNodes,
			change.To.TreeEntry.Hash, toBlob.Data, afterNodes,
		)
		if err != nil {
			return nil, err
		}
		saver.result = append(saver.result, record)
	}
	return nil, nil
}

func shortHash(hash plumbing.Hash) string {
	raw := hash.String()
	if len(raw) > 12 {
		return raw[:12]
	}
	return raw
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

	if err := os.WriteFile(srcBeforePath, srcBefore, 0o644); err != nil {
		return UASTChangeRecord{}, err
	}
	if err := os.WriteFile(srcAfterPath, srcAfter, 0o644); err != nil {
		return UASTChangeRecord{}, err
	}
	beforeJSON, err := json.MarshalIndent(nodesBefore, "", "  ")
	if err != nil {
		return UASTChangeRecord{}, err
	}
	afterJSON, err := json.MarshalIndent(nodesAfter, "", "  ")
	if err != nil {
		return UASTChangeRecord{}, err
	}
	if err := os.WriteFile(uastBeforePath, beforeJSON, 0o644); err != nil {
		return UASTChangeRecord{}, err
	}
	if err := os.WriteFile(uastAfterPath, afterJSON, 0o644); err != nil {
		return UASTChangeRecord{}, err
	}
	return UASTChangeRecord{
		FileName:   fileName,
		SrcBefore:  srcBeforePath,
		SrcAfter:   srcAfterPath,
		UASTBefore: uastBeforePath,
		UASTAfter:  uastAfterPath,
	}, nil
}

// Fork clones this PipelineItem.
func (saver *UASTChangesSaver) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(saver, n)
}

// Finalize returns the result of the analysis.
func (saver *UASTChangesSaver) Finalize() interface{} {
	return saver.result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (saver *UASTChangesSaver) Serialize(result interface{}, binary bool, writer io.Writer) error {
	records, ok := result.([]UASTChangeRecord)
	if !ok {
		return fmt.Errorf("result is not []UASTChangeRecord: %T", result)
	}
	if binary {
		payload := map[string]interface{}{"changes": records}
		return json.NewEncoder(writer).Encode(payload)
	}
	for _, sc := range records {
		fmt.Fprintf(writer, "  - {file: %s, src0: %s, src1: %s, uast0: %s, uast1: %s}\n",
			sc.FileName, sc.SrcBefore, sc.SrcAfter, sc.UASTBefore, sc.UASTAfter)
	}
	return nil
}

func init() {
	core.Registry.Register(&UASTChangesSaver{})
}
