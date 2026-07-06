// Minimal Hercules plugin used by the plugin compatibility smoke test
// (test/plugin_smoke). It registers a leaf analysis which counts the consumed
// commits — just enough to prove that a plugin built with
// `go build -buildmode=plugin` loads into hercules via --plugin, registers its
// command line flag, runs and serializes a result.
//
// The directory lives under testdata/ so that the ordinary `go build ./...` /
// `go test ./...` walks skip it; the smoke test builds it by explicit path.
package main

import (
	"fmt"
	"io"

	hercules "github.com/cwbudde/hercules"
	"github.com/go-git/go-git/v5"
)

// MinimalPluginTest counts the number of consumed commits.
type MinimalPluginTest struct {
	// No special branch merge logic is required
	hercules.NoopMerger
	// Process each merge commit only once
	hercules.OneShotMergeProcessor

	commits int
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (m *MinimalPluginTest) Name() string {
	return "MinimalPluginTest"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (m *MinimalPluginTest) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (m *MinimalPluginTest) Requires() []string {
	return []string{}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (m *MinimalPluginTest) ListConfigurationOptions() []hercules.ConfigurationOption {
	return nil
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (m *MinimalPluginTest) Configure(facts map[string]interface{}) error {
	return nil
}

// ConfigureUpstream is called on each item in the reverse dependency order.
func (m *MinimalPluginTest) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag returns the command line switch which activates the analysis.
func (m *MinimalPluginTest) Flag() string {
	return "minimal-plugin-test"
}

// Description returns the text which explains what the analysis is doing.
func (m *MinimalPluginTest) Description() string {
	return "Counts the analysed commits; exists solely to smoke-test the plugin system."
}

// Initialize resets the internal temporary data structures and prepares the object for Consume().
func (m *MinimalPluginTest) Initialize(repository *git.Repository) error {
	m.OneShotMergeProcessor.Initialize()
	m.commits = 0
	return nil
}

// Consume is called for every commit in the sequence.
func (m *MinimalPluginTest) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if m.ShouldConsumeCommit(deps) {
		m.commits++
	}
	return map[string]interface{}{}, nil
}

// Fork clones the same item several times on branches.
func (m *MinimalPluginTest) Fork(n int) []hercules.PipelineItem {
	return hercules.ForkSamePipelineItem(m, n)
}

// Finalize produces the result of the analysis. No more Consume() calls are expected afterwards.
func (m *MinimalPluginTest) Finalize() interface{} {
	return m.commits
}

// Serialize converts the result from Finalize() to YAML. Protocol Buffers output
// is deliberately not supported — the smoke test only exercises the text path.
func (m *MinimalPluginTest) Serialize(result interface{}, binary bool, writer io.Writer) error {
	if binary {
		return fmt.Errorf("MinimalPluginTest does not support binary serialization")
	}
	_, err := fmt.Fprintf(writer, "  consumed_commits: %d\n", result.(int))
	return err
}

func init() {
	hercules.Registry.Register(&MinimalPluginTest{})
}
