//go:build !babelfish
// +build !babelfish

package uast

import (
	"errors"
	"io"

	"github.com/go-git/go-git/v5"
	"github.com/meko-christian/hercules/internal/core"
)

const (
	// FeatureUast is kept for compatibility; Babelfish support requires -tags babelfish.
	FeatureUast = "uast"
	// DependencyUasts is the historical dependency name for extracted UAST blobs.
	DependencyUasts = "uasts"
	// DependencyUastChanges is the historical dependency name for changed UAST blobs.
	DependencyUastChanges = "changed_uasts"
	// ConfigUASTChangesSaverOutputPath is preserved for CLI compatibility.
	ConfigUASTChangesSaverOutputPath = "ChangesSaver.OutputPath"
)

var errBabelfishRequired = errors.New("this analysis requires Babelfish support; rebuild with -tags babelfish")

// ChangesSaver is a non-babelfish placeholder for --dump-uast-changes.
type ChangesSaver struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	OutputPath string
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (saver *ChangesSaver) Name() string {
	return "UASTChangesSaver"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (saver *ChangesSaver) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (saver *ChangesSaver) Requires() []string {
	return []string{}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (saver *ChangesSaver) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigUASTChangesSaverOutputPath,
			Description: "The target directory where to store the changed UAST files.",
			Flag:        "changed-uast-dir",
			Type:        core.PathConfigurationOption,
			Default:     ".",
		},
	}
	return options[:]
}

// Flag for the command line switch which enables this analysis.
func (saver *ChangesSaver) Flag() string {
	return "dump-uast-changes"
}

// Description returns the text which explains what the analysis is doing.
func (saver *ChangesSaver) Description() string {
	return "Unavailable in this build. Rebuild with -tags babelfish to enable UAST dumps."
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (saver *ChangesSaver) Configure(facts map[string]interface{}) error {
	if val, exists := facts[ConfigUASTChangesSaverOutputPath]; exists {
		saver.OutputPath = val.(string)
	}
	return nil
}

func (*ChangesSaver) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (saver *ChangesSaver) Initialize(repository *git.Repository) error {
	return errBabelfishRequired
}

// Consume runs this PipelineItem on the next commit data.
func (saver *ChangesSaver) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	return nil, errBabelfishRequired
}

// Fork clones this PipelineItem.
func (saver *ChangesSaver) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(saver, n)
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (saver *ChangesSaver) Finalize() interface{} {
	return nil
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (saver *ChangesSaver) Serialize(result interface{}, binary bool, writer io.Writer) error {
	return errBabelfishRequired
}

func init() {
	core.Registry.Register(&ChangesSaver{})
}
