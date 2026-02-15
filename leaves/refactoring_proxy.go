package leaves

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
)

const (
	// ConfigRefactoringThreshold is the name of the configuration option
	ConfigRefactoringThreshold = "RefactoringProxy.Threshold"
)

// tickChangeMetrics tracks changes for one tick
type tickChangeMetrics struct {
	TotalChanges int // All additions, modifications, deletions, renames
	Renames      int // Changes where From.Name != To.Name (both non-empty)
}

// RefactoringProxyResult is returned by RefactoringProxy.Finalize()
type RefactoringProxyResult struct {
	// Time series data (parallel arrays, same length)
	Ticks        []int       // Tick indices
	RenameRatios []float64   // Rename ratio per tick (0.0 to 1.0)
	IsRefactoring []bool      // true if ratio > threshold
	TotalChanges []int       // Total changes per tick (for context)

	// Configuration metadata
	Threshold float64       // The threshold used for classification
	tickSize  time.Duration
}

// RefactoringProxy measures rename/move rate to identify refactoring phases
type RefactoringProxy struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// RefactoringThreshold is the rename ratio threshold (0.0-1.0)
	RefactoringThreshold float64

	// tickMetrics maps tick -> metrics
	tickMetrics map[int]*tickChangeMetrics
	tickSize    time.Duration

	l core.Logger
}

// Name of this PipelineItem
func (rp *RefactoringProxy) Name() string {
	return "RefactoringProxy"
}

// Provides returns entities produced
func (rp *RefactoringProxy) Provides() []string {
	return []string{}
}

// Requires returns entities needed
func (rp *RefactoringProxy) Requires() []string {
	return []string{
		items.DependencyTreeChanges,
		items.DependencyTick,
	}
}

// Flag for command line switch
func (rp *RefactoringProxy) Flag() string {
	return "refactoring-proxy"
}

// Description explains what the analysis does
func (rp *RefactoringProxy) Description() string {
	return "Tracks rename/move rate over time to distinguish refactoring phases from feature work."
}

// ListConfigurationOptions returns changeable properties
func (rp *RefactoringProxy) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigRefactoringThreshold,
			Description: "Rename ratio threshold to classify a tick as refactoring-heavy (0.0-1.0).",
			Flag:        "refactoring-threshold",
			Type:        core.FloatConfigurationOption,
			Default:     float32(0.5),
		},
	}
	return options[:]
}

// Configure sets properties
func (rp *RefactoringProxy) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		rp.l = l
	}
	if val, exists := facts[ConfigRefactoringThreshold].(float32); exists {
		rp.RefactoringThreshold = float64(val)
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		rp.tickSize = val
	}
	return nil
}

// ConfigureUpstream configures upstream dependencies
func (*RefactoringProxy) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Initialize resets caches
func (rp *RefactoringProxy) Initialize(repository *git.Repository) error {
	rp.l = core.NewLogger()
	rp.tickMetrics = map[int]*tickChangeMetrics{}
	rp.OneShotMergeProcessor.Initialize()

	if rp.RefactoringThreshold == 0 {
		rp.RefactoringThreshold = 0.5
	}

	return nil
}

// isRename checks if a change represents a file rename
func isRename(change *object.Change) bool {
	hasFrom := change.From.Name != ""
	hasTo := change.To.Name != ""
	differentNames := change.From.Name != change.To.Name

	return hasFrom && hasTo && differentNames
}

// getOrCreateTickMetrics retrieves or creates tick metrics
func (rp *RefactoringProxy) getOrCreateTickMetrics(tick int) *tickChangeMetrics {
	metrics, exists := rp.tickMetrics[tick]
	if !exists {
		metrics = &tickChangeMetrics{}
		rp.tickMetrics[tick] = metrics
	}
	return metrics
}

// Consume runs on next commit data
func (rp *RefactoringProxy) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !rp.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	tick := deps[items.DependencyTick].(int)
	treeChanges := deps[items.DependencyTreeChanges].(object.Changes)

	if len(treeChanges) == 0 {
		return nil, nil
	}

	metrics := rp.getOrCreateTickMetrics(tick)

	for _, change := range treeChanges {
		metrics.TotalChanges++

		if isRename(change) {
			metrics.Renames++
		}
	}

	return nil, nil
}

// Finalize returns the analysis result
func (rp *RefactoringProxy) Finalize() interface{} {
	ticks := make([]int, 0, len(rp.tickMetrics))
	for tick := range rp.tickMetrics {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)

	result := RefactoringProxyResult{
		Ticks:         make([]int, len(ticks)),
		RenameRatios:  make([]float64, len(ticks)),
		IsRefactoring: make([]bool, len(ticks)),
		TotalChanges:  make([]int, len(ticks)),
		Threshold:     rp.RefactoringThreshold,
		tickSize:      rp.tickSize,
	}

	for i, tick := range ticks {
		metrics := rp.tickMetrics[tick]

		result.Ticks[i] = tick
		result.TotalChanges[i] = metrics.TotalChanges

		if metrics.TotalChanges > 0 {
			ratio := float64(metrics.Renames) / float64(metrics.TotalChanges)
			result.RenameRatios[i] = ratio
			result.IsRefactoring[i] = ratio > rp.RefactoringThreshold
		} else {
			result.RenameRatios[i] = 0.0
			result.IsRefactoring[i] = false
		}
	}

	return result
}

// Fork clones this pipeline item
func (rp *RefactoringProxy) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(rp, n)
}

// serializeText outputs YAML format
func (rp *RefactoringProxy) serializeText(result *RefactoringProxyResult, writer io.Writer) {
	fmt.Fprintln(writer, "  refactoring_proxy:")
	fmt.Fprintf(writer, "    threshold: %.2f\n", result.Threshold)
	fmt.Fprintf(writer, "    tick_size: %d\n", int(result.tickSize.Seconds()))

	// Ticks array
	fmt.Fprint(writer, "    ticks: [")
	for i, tick := range result.Ticks {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%d", tick)
	}
	fmt.Fprintln(writer, "]")

	// Rename ratios array
	fmt.Fprint(writer, "    rename_ratios: [")
	for i, ratio := range result.RenameRatios {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%.4f", ratio)
	}
	fmt.Fprintln(writer, "]")

	// Is refactoring array
	fmt.Fprint(writer, "    is_refactoring: [")
	for i, isRef := range result.IsRefactoring {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%t", isRef)
	}
	fmt.Fprintln(writer, "]")

	// Total changes array
	fmt.Fprint(writer, "    total_changes: [")
	for i, total := range result.TotalChanges {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%d", total)
	}
	fmt.Fprintln(writer, "]")
}

// serializeBinary outputs Protocol Buffers format
func (rp *RefactoringProxy) serializeBinary(result *RefactoringProxyResult, writer io.Writer) error {
	message := pb.RefactoringProxyResults{
		Ticks:         make([]int32, len(result.Ticks)),
		RenameRatios:  make([]float32, len(result.RenameRatios)),
		IsRefactoring: result.IsRefactoring,
		TotalChanges:  make([]int32, len(result.TotalChanges)),
		Threshold:     float32(result.Threshold), // Convert float64 to float32 for protobuf
		TickSize:      int64(result.tickSize),
	}

	for i := range result.Ticks {
		message.Ticks[i] = int32(result.Ticks[i])
		message.RenameRatios[i] = float32(result.RenameRatios[i])
		message.TotalChanges[i] = int32(result.TotalChanges[i])
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// Serialize converts analysis result to text or bytes
func (rp *RefactoringProxy) Serialize(result interface{}, binary bool, writer io.Writer) error {
	refactoringResult, ok := result.(RefactoringProxyResult)
	if !ok {
		return fmt.Errorf("result is not a RefactoringProxyResult: '%v'", result)
	}
	if binary {
		return rp.serializeBinary(&refactoringResult, writer)
	}
	rp.serializeText(&refactoringResult, writer)
	return nil
}

// Deserialize converts protobuf bytes to RefactoringProxyResult
func (rp *RefactoringProxy) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.RefactoringProxyResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	result := RefactoringProxyResult{
		Ticks:         make([]int, len(message.Ticks)),
		RenameRatios:  make([]float64, len(message.RenameRatios)),
		IsRefactoring: message.IsRefactoring,
		TotalChanges:  make([]int, len(message.TotalChanges)),
		Threshold:     float64(message.Threshold), // Convert float32 from protobuf to float64
		tickSize:      time.Duration(message.TickSize),
	}

	for i := range message.Ticks {
		result.Ticks[i] = int(message.Ticks[i])
		result.RenameRatios[i] = float64(message.RenameRatios[i])
		result.TotalChanges[i] = int(message.TotalChanges[i])
	}

	return result, nil
}

func init() {
	core.Registry.Register(&RefactoringProxy{})
}
