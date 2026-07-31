package leaves

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

var (
	errUnexpectedRefactoringProxyResult = errors.New("result is not a RefactoringProxyResult")
	// errRefactoringProxyMismatchingTickSizes rejects merges of results produced with different
	// tick granularities: their tick indices do not describe the same time axis.
	errRefactoringProxyMismatchingTickSizes = errors.New("mismatching tick sizes")
	// errRefactoringProxyMismatchingThresholds rejects merges of results classified with different
	// thresholds. Unlike descriptive metadata the threshold *is* the classifier, and the renderer
	// draws it as a reference line, so silently picking one would produce a wrong picture.
	errRefactoringProxyMismatchingThresholds = errors.New("mismatching thresholds")
)

const (
	// ConfigRefactoringThreshold is the name of the configuration option.
	ConfigRefactoringThreshold = "RefactoringProxy.Threshold"
)

// tickChangeMetrics tracks changes for one tick.
type tickChangeMetrics struct {
	TotalChanges int // All additions, modifications, deletions, renames
	Renames      int // Changes where From.Name != To.Name (both non-empty)
}

// RefactoringProxyResult is returned by RefactoringProxy.Finalize().
type RefactoringProxyResult struct {
	// Time series data (parallel arrays, same length)
	Ticks         []int     // Tick indices
	RenameRatios  []float64 // Rename ratio per tick (0.0 to 1.0)
	IsRefactoring []bool    // true if ratio > threshold
	TotalChanges  []int     // Total changes per tick (for context)

	// Configuration metadata
	Threshold float64 // The threshold used for classification
	tickSize  time.Duration
}

// RefactoringProxy measures rename/move rate to identify refactoring phases.
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

// Name of this PipelineItem.
func (rp *RefactoringProxy) Name() string {
	return "RefactoringProxy"
}

// Provides returns entities produced.
func (rp *RefactoringProxy) Provides() []string {
	return []string{}
}

// Requires returns entities needed.
func (rp *RefactoringProxy) Requires() []string {
	return []string{
		items.DependencyTreeChanges,
		items.DependencyTick,
	}
}

// Flag for command line switch.
func (rp *RefactoringProxy) Flag() string {
	return "refactoring-proxy"
}

// Description explains what the analysis does.
func (rp *RefactoringProxy) Description() string {
	return "Tracks rename/move rate over time to distinguish refactoring phases from feature work."
}

// ListConfigurationOptions returns changeable properties.
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

// Configure sets properties.
func (rp *RefactoringProxy) Configure(facts map[string]any) error {
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

// ConfigureUpstream configures upstream dependencies.
func (*RefactoringProxy) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets caches.
func (rp *RefactoringProxy) Initialize(repository *git.Repository) error {
	rp.l = core.NewLogger()
	rp.tickMetrics = map[int]*tickChangeMetrics{}
	rp.OneShotMergeProcessor.Initialize()

	if rp.RefactoringThreshold == 0 {
		rp.RefactoringThreshold = 0.5
	}

	return nil
}

// isRename checks if a change represents a file rename.
func isRename(change *object.Change) bool {
	hasFrom := change.From.Name != ""
	hasTo := change.To.Name != ""
	differentNames := change.From.Name != change.To.Name

	return hasFrom && hasTo && differentNames
}

// Consume runs on next commit data.
func (rp *RefactoringProxy) Consume(deps map[string]any) (map[string]any, error) {
	if !rp.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	reader := factReader{facts: deps}
	tick := readFact[int](&reader, items.DependencyTick)
	treeChanges := readFact[object.Changes](&reader, items.DependencyTreeChanges)

	if reader.err != nil {
		return nil, reader.err
	}

	if len(treeChanges) == 0 {
		return noDependencies(), nil
	}

	metrics := rp.getOrCreateTickMetrics(tick)

	for _, change := range treeChanges {
		metrics.TotalChanges++

		if isRename(change) {
			metrics.Renames++
		}
	}

	return noDependencies(), nil
}

// Finalize returns the analysis result.

func (rp *RefactoringProxy) Finalize() any {
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

	for tickIndex, tick := range ticks {
		metrics := rp.tickMetrics[tick]

		result.Ticks[tickIndex] = tick
		result.TotalChanges[tickIndex] = metrics.TotalChanges

		if metrics.TotalChanges > 0 {
			ratio := float64(metrics.Renames) / float64(metrics.TotalChanges)
			result.RenameRatios[tickIndex] = ratio
			result.IsRefactoring[tickIndex] = ratio > rp.RefactoringThreshold
		} else {
			result.RenameRatios[tickIndex] = 0.0
			result.IsRefactoring[tickIndex] = false
		}
	}

	return result
}

// Fork clones this pipeline item.
func (rp *RefactoringProxy) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(rp, n)
}

// Serialize converts analysis result to text or bytes.
func (rp *RefactoringProxy) Serialize(result any, binary bool, writer io.Writer) error {
	refactoringResult, ok := result.(RefactoringProxyResult)
	if !ok {
		return fmt.Errorf("%w: '%v'", errUnexpectedRefactoringProxyResult, result)
	}

	if binary {
		return rp.serializeBinary(&refactoringResult, writer)
	}

	rp.serializeText(&refactoringResult, writer)

	return nil
}

// Deserialize converts protobuf bytes to RefactoringProxyResult.
func (rp *RefactoringProxy) Deserialize(pbmessage []byte) (any, error) {
	message := pb.RefactoringProxyResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal refactoring proxy result: %w", err)
	}

	result := RefactoringProxyResult{
		Ticks:         make([]int, len(message.GetTicks())),
		RenameRatios:  make([]float64, len(message.GetRenameRatios())),
		IsRefactoring: message.GetIsRefactoring(),
		TotalChanges:  make([]int, len(message.GetTotalChanges())),
		Threshold:     float64(message.GetThreshold()), // Convert float32 from protobuf to float64
		tickSize:      time.Duration(message.GetTickSize()),
	}

	for i := range message.GetTicks() {
		result.Ticks[i] = int(message.GetTicks()[i])
		result.RenameRatios[i] = float64(message.GetRenameRatios()[i])
		result.TotalChanges[i] = int(message.GetTotalChanges()[i])
	}

	return result, nil
}

// refactoringTickAccumulator collects the merge inputs for a single tick of the merged axis.
type refactoringTickAccumulator struct {
	// renames is the count-weighted rename mass, ratio*total summed over the sources. It stays a
	// float64 because Finalize() already dropped the raw integer count; the weighted mean never
	// needs it back as an integer.
	renames      float64
	totalChanges int
	sources      int
	// ratio is the verbatim ratio of the last source, used when this tick has exactly one source.
	ratio float64
}

// MergeResults combines two RefactoringProxyResult-s together.
func (rp *RefactoringProxy) MergeResults(result1, result2 any,
	commonResult1, commonResult2 *core.CommonAnalysisResult,
) any {
	cr1, err := requiredResult[RefactoringProxyResult](result1)
	if err != nil {
		return fmt.Errorf("merge refactoring proxy first result: %w", err)
	}

	cr2, err := requiredResult[RefactoringProxyResult](result2)
	if err != nil {
		return fmt.Errorf("merge refactoring proxy second result: %w", err)
	}

	if cr1.tickSize != cr2.tickSize {
		return fmt.Errorf("%w (r1: %d, r2: %d) received",
			errRefactoringProxyMismatchingTickSizes, cr1.tickSize, cr2.tickSize)
	}

	if cr1.Threshold != cr2.Threshold {
		return fmt.Errorf("%w (r1: %f, r2: %f) received",
			errRefactoringProxyMismatchingThresholds, cr1.Threshold, cr2.Threshold)
	}

	offset1, offset2 := refactoringProxyTickOffsets(commonResult1, commonResult2, cr1.tickSize)

	accumulated := map[int]*refactoringTickAccumulator{}
	accumulateRefactoringTicks(accumulated, &cr1, offset1)
	accumulateRefactoringTicks(accumulated, &cr2, offset2)

	return buildMergedRefactoringResult(accumulated, cr1.Threshold, cr1.tickSize)
}

// refactoringProxyTickOffsets rebases both tick axes onto the earlier repository start, which is
// exactly what CommonAnalysisResult.Merge() records as the merged BeginTime.
func refactoringProxyTickOffsets(
	commonResult1, commonResult2 *core.CommonAnalysisResult, tickSize time.Duration,
) (int, int) {
	if tickSize <= 0 || commonResult1 == nil || commonResult2 == nil {
		return 0, 0
	}

	firstStart := items.FloorTime(commonResult1.BeginTimeAsTime(), tickSize)
	secondStart := items.FloorTime(commonResult2.BeginTimeAsTime(), tickSize)

	startTime := firstStart
	if secondStart.Before(startTime) {
		startTime = secondStart
	}

	offset1 := int(firstStart.Sub(startTime) / tickSize)
	offset2 := int(secondStart.Sub(startTime) / tickSize)

	return offset1, offset2
}

// accumulateRefactoringTicks folds one source into the shifted, merged tick axis.
func accumulateRefactoringTicks(
	target map[int]*refactoringTickAccumulator, source *RefactoringProxyResult, offset int,
) {
	for tickIndex, tick := range source.Ticks {
		totalChanges := 0
		if tickIndex < len(source.TotalChanges) {
			totalChanges = source.TotalChanges[tickIndex]
		}

		ratio := 0.0
		if tickIndex < len(source.RenameRatios) {
			ratio = source.RenameRatios[tickIndex]
		}

		targetTick := tick + offset

		accumulator := target[targetTick]
		if accumulator == nil {
			accumulator = &refactoringTickAccumulator{}
			target[targetTick] = accumulator
		}

		accumulator.renames += ratio * float64(totalChanges)
		accumulator.totalChanges += totalChanges
		accumulator.sources++
		accumulator.ratio = ratio
	}
}

// buildMergedRefactoringResult emits the sorted, re-classified merged result.
func buildMergedRefactoringResult(
	accumulated map[int]*refactoringTickAccumulator, threshold float64, tickSize time.Duration,
) RefactoringProxyResult {
	ticks := make([]int, 0, len(accumulated))
	for tick := range accumulated {
		ticks = append(ticks, tick)
	}

	// serializeText/serializeBinary do not sort, so map iteration order would leak into the output.
	sort.Ints(ticks)

	merged := RefactoringProxyResult{
		Ticks:         make([]int, len(ticks)),
		RenameRatios:  make([]float64, len(ticks)),
		IsRefactoring: make([]bool, len(ticks)),
		TotalChanges:  make([]int, len(ticks)),
		Threshold:     threshold,
		tickSize:      tickSize,
	}

	for tickIndex, tick := range ticks {
		accumulator := accumulated[tick]

		ratio := 0.0

		switch {
		case accumulator.sources == 1:
			// (r*t)/t can differ from r by an ulp, so keep a lone source bit-for-bit.
			ratio = accumulator.ratio
		case accumulator.totalChanges > 0:
			ratio = accumulator.renames / float64(accumulator.totalChanges)
		}

		merged.Ticks[tickIndex] = tick
		merged.RenameRatios[tickIndex] = ratio
		merged.IsRefactoring[tickIndex] = ratio > threshold
		merged.TotalChanges[tickIndex] = accumulator.totalChanges
	}

	return merged
}

// getOrCreateTickMetrics retrieves or creates tick metrics.
func (rp *RefactoringProxy) getOrCreateTickMetrics(tick int) *tickChangeMetrics {
	metrics, exists := rp.tickMetrics[tick]
	if !exists {
		metrics = &tickChangeMetrics{}
		rp.tickMetrics[tick] = metrics
	}

	return metrics
}

// serializeText outputs YAML format.
func (rp *RefactoringProxy) serializeText(result *RefactoringProxyResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  refactoring_proxy:")
	_, _ = fmt.Fprintf(writer, "    threshold: %.2f\n", result.Threshold)
	_, _ = fmt.Fprintf(writer, "    tick_size: %d\n", int(result.tickSize.Seconds()))

	// Ticks array
	// Serialize's legacy text path has no error channel.
	_, _ = fmt.Fprint(writer, "    ticks: [")

	for i, tick := range result.Ticks {
		if i > 0 {
			_, _ = fmt.Fprint(writer, ", ")
		}

		_, _ = fmt.Fprintf(writer, "%d", tick)
	}

	_, _ = fmt.Fprintln(writer, "]")

	// Rename ratios array
	_, _ = fmt.Fprint(writer, "    rename_ratios: [")

	for i, ratio := range result.RenameRatios {
		if i > 0 {
			_, _ = fmt.Fprint(writer, ", ")
		}

		_, _ = fmt.Fprintf(writer, "%.4f", ratio)
	}

	_, _ = fmt.Fprintln(writer, "]")

	// Is refactoring array
	_, _ = fmt.Fprint(writer, "    is_refactoring: [")

	for i, isRef := range result.IsRefactoring {
		if i > 0 {
			_, _ = fmt.Fprint(writer, ", ")
		}

		_, _ = fmt.Fprintf(writer, "%t", isRef)
	}

	_, _ = fmt.Fprintln(writer, "]")

	// Total changes array
	_, _ = fmt.Fprint(writer, "    total_changes: [")

	for i, total := range result.TotalChanges {
		if i > 0 {
			_, _ = fmt.Fprint(writer, ", ")
		}

		_, _ = fmt.Fprintf(writer, "%d", total)
	}

	_, _ = fmt.Fprintln(writer, "]")
}

// serializeBinary outputs Protocol Buffers format.
func (rp *RefactoringProxy) serializeBinary(result *RefactoringProxyResult, writer io.Writer) error {
	message := pb.RefactoringProxyResults{
		Ticks:         make([]int32, len(result.Ticks)),
		RenameRatios:  make([]float32, len(result.RenameRatios)),
		IsRefactoring: result.IsRefactoring,
		TotalChanges:  make([]int32, len(result.TotalChanges)),
		Threshold:     float32(result.Threshold), // Convert float64 to float32 for protobuf
		TickSize:      int64(result.tickSize),
	}

	for tickIndex := range result.Ticks {
		tick, err := intToProtoInt32(result.Ticks[tickIndex], "refactoring-proxy tick")
		if err != nil {
			return err
		}

		totalChanges, err := intToProtoInt32(
			result.TotalChanges[tickIndex], "refactoring-proxy total changes",
		)
		if err != nil {
			return err
		}

		message.Ticks[tickIndex] = tick
		message.RenameRatios[tickIndex] = float32(result.RenameRatios[tickIndex])
		message.TotalChanges[tickIndex] = totalChanges
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal refactoring proxy result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write refactoring proxy result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&RefactoringProxy{})
