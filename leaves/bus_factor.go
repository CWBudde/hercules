package leaves

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

var (
	// errBusFactorMismatchingTickSizes rejects merges of results produced with different tick
	// sizes: their tick axes cannot be rebased onto one another.
	errBusFactorMismatchingTickSizes = errors.New("mismatching tick sizes")
	// errBusFactorMismatchingThresholds rejects merges of results computed against different
	// ownership thresholds, which are not comparable.
	errBusFactorMismatchingThresholds = errors.New("mismatching bus factor thresholds")
)

// BusFactorAnalysis computes the bus factor of a repository over time.
// The bus factor is the smallest number k of developers whose combined line
// ownership covers at least a configurable threshold (default 80%) of all
// living lines. A low bus factor indicates high risk: few people understand
// the codebase.
//
// It consumes LineHistoryChanges to track per-file, per-author alive-line
// counts and snapshots the bus factor at each tick.
type BusFactorAnalysis struct {
	core.NoopMerger

	// Threshold is the ownership fraction that must be covered (default 0.8 = 80%).
	Threshold float32

	// ownership references the shared incremental alive-line ownership state.
	ownership *ownershipSnapshotAccumulator
	// peopleResolver resolves author IDs to names.
	peopleResolver core.IdentityResolver
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize references TicksSinceStart.TickSize.
	tickSize time.Duration
	// snapshots stores per-tick bus factor snapshots.
	snapshots map[int]*BusFactorSnapshot
	l         core.Logger
}

const (
	// ConfigBusFactorThreshold is the name of the option to configure the ownership threshold.
	ConfigBusFactorThreshold = "BusFactor.Threshold"
)

// BusFactorSnapshot stores the bus factor and ownership distribution at a single tick.
type BusFactorSnapshot struct {
	// BusFactor is the smallest k where the top-k owners cover >= threshold of lines.
	BusFactor int
	// TotalLines is the total number of alive lines at this tick.
	TotalLines int64
	// AuthorLines maps author index to their alive line count.
	AuthorLines map[int]int64
}

// BusFactorResult is returned by BusFactorAnalysis.Finalize().
type BusFactorResult struct {
	// Snapshots maps tick index to the bus factor snapshot at that tick.
	Snapshots map[int]*BusFactorSnapshot
	// SubsystemBusFactor maps directory prefix to bus factor for the final tick.
	SubsystemBusFactor map[string]int
	// Threshold used for the computation.
	Threshold float32
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize is the duration of each tick.
	tickSize time.Duration
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (bf *BusFactorAnalysis) Name() string {
	return "BusFactor"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (bf *BusFactorAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (bf *BusFactorAnalysis) Requires() []string {
	return []string{
		dependencyOwnershipSnapshot,
		identity.DependencyAuthor,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (bf *BusFactorAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{{
		Name:        ConfigBusFactorThreshold,
		Description: "Ownership threshold for bus factor computation (0.0-1.0).",
		Flag:        "bus-factor-threshold",
		Type:        core.FloatConfigurationOption,
		Default:     float32(0.8),
	}}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (bf *BusFactorAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		bf.l = l
	}

	if _, exists := facts[ConfigBusFactorThreshold]; exists {
		threshold, err := requiredFact[float32](facts, ConfigBusFactorThreshold)
		if err != nil {
			return err
		}

		bf.Threshold = threshold
	}

	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		bf.reversedPeopleDict = val
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		bf.tickSize = val
	}

	if val, ok := facts[core.FactIdentityResolver].(core.IdentityResolver); ok {
		bf.peopleResolver = val
	}

	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*BusFactorAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (bf *BusFactorAnalysis) Flag() string {
	return "bus-factor"
}

// Description returns the text which explains what the analysis is doing.
func (bf *BusFactorAnalysis) Description() string {
	return "Computes the bus factor (smallest k developers owning >= threshold of lines) over time."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (bf *BusFactorAnalysis) Initialize(repository *git.Repository) error {
	bf.l = core.NewLogger()
	bf.snapshots = map[int]*BusFactorSnapshot{}
	bf.ownership = nil

	if bf.Threshold <= 0 || bf.Threshold > 1 {
		bf.Threshold = 0.8
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// It closes the previous tick before applying the first commit from a later tick.
func (bf *BusFactorAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	reader := factReader{facts: deps}
	update := readFact[ownershipSnapshotUpdate](&reader, dependencyOwnershipSnapshot)

	if reader.err != nil {
		return nil, reader.err
	}

	bf.ownership = update.State

	if update.ClosedTotals != nil {
		bf.takeSnapshot(update.ClosedTick, *update.ClosedTotals)
	}

	return noDependencies(), nil
}

// computeBusFactor returns the smallest k such that the top-k authors own >= threshold of totalLines.
// Returns 0 if totalLines is 0.

func computeBusFactor(authorLines map[int]int64, totalLines int64, threshold float32) int {
	if totalLines == 0 {
		return 0
	}

	// Sort author line counts in descending order
	counts := make([]int64, 0, len(authorLines))
	for _, lines := range authorLines {
		counts = append(counts, lines)
	}

	sort.Slice(counts, func(i, j int) bool {
		return counts[i] > counts[j]
	})

	target := busFactorTargetLines(totalLines, threshold)
	if target == 0 {
		return 0
	}

	var cumulative int64
	for k, c := range counts {
		cumulative += c
		if cumulative >= target {
			return k + 1
		}
	}

	return len(counts)
}

// busFactorTargetLines computes ceil(totalLines * threshold) using the shortest decimal
// representation of the configured float32. This treats a configured 0.8 as exactly four fifths
// instead of inheriting its binary floating-point approximation.
func busFactorTargetLines(totalLines int64, threshold float32) int64 {
	if totalLines <= 0 || threshold <= 0 {
		return 0
	}

	decimal := strconv.FormatFloat(float64(threshold), 'g', -1, 32)

	ratio, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return totalLines
	}

	numerator := new(big.Int).Mul(ratio.Num(), big.NewInt(totalLines))

	target, remainder := new(big.Int).QuoRem(
		numerator,
		ratio.Denom(),
		new(big.Int),
	)
	if remainder.Sign() > 0 {
		target.Add(target, big.NewInt(1))
	}

	return target.Int64()
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (bf *BusFactorAnalysis) Finalize() any {
	if bf.ownership != nil {
		if tick, totals := bf.ownership.finalSnapshot(); totals != nil {
			bf.takeSnapshot(tick, *totals)
		}
	}

	return BusFactorResult{
		Snapshots:          bf.snapshots,
		SubsystemBusFactor: bf.computeSubsystemBusFactor(),
		Threshold:          bf.Threshold,
		reversedPeopleDict: bf.reversedPeopleDict,
		tickSize:           bf.tickSize,
	}
}

// Fork clones this pipeline item.
func (bf *BusFactorAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(bf, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (bf *BusFactorAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	bfResult, err := requiredResult[BusFactorResult](result)
	if err != nil {
		return fmt.Errorf("serialize bus factor: %w", err)
	}

	if binary {
		return bf.serializeBinary(&bfResult, writer)
	}

	bf.serializeText(&bfResult, writer)

	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (bf *BusFactorAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.BusFactorAnalysisResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal bus factor result: %w", err)
	}

	snapshots := make(map[int]*BusFactorSnapshot, len(message.GetSnapshots()))
	for tick, pbSnapshot := range message.GetSnapshots() {
		authorLines := make(map[int]int64, len(pbSnapshot.GetAuthorLines()))
		for authorID, lines := range pbSnapshot.GetAuthorLines() {
			dev := int(authorID)
			if authorID == -1 {
				dev = core.AuthorMissing
			}

			authorLines[dev] = lines
		}

		snapshots[int(tick)] = &BusFactorSnapshot{
			BusFactor:   int(pbSnapshot.GetBusFactor()),
			TotalLines:  pbSnapshot.GetTotalLines(),
			AuthorLines: authorLines,
		}
	}

	subsystemBF := make(map[string]int, len(message.GetSubsystemBusFactor()))
	for dir, bf := range message.GetSubsystemBusFactor() {
		subsystemBF[dir] = int(bf)
	}

	result := BusFactorResult{
		Snapshots:          snapshots,
		SubsystemBusFactor: subsystemBF,
		Threshold:          message.GetThreshold(),
		reversedPeopleDict: message.GetDevIndex(),
		tickSize:           time.Duration(message.GetTickSize()),
	}

	return result, nil
}

// QualifyPaths prefixes every subsystem - a directory prefix - with the repository it was observed
// in. Without it the worst-case max below compares "cmd" in one repository against "cmd" in another
// and reports a single bus factor for both.
func (bf *BusFactorAnalysis) QualifyPaths(result any, repository string) any {
	busFactorResult, err := requiredResult[BusFactorResult](result)
	if err != nil {
		return fmt.Errorf("qualify bus factor paths: %w", err)
	}

	subsystems := make(map[string]int, len(busFactorResult.SubsystemBusFactor))
	for directory, factor := range busFactorResult.SubsystemBusFactor {
		subsystems[core.QualifyRepositoryPath(repository, directory)] = factor
	}

	busFactorResult.SubsystemBusFactor = subsystems

	return busFactorResult
}

// MergeResults combines two BusFactorResult-s together.
//
// Ownership is additive across repositories, so the per-tick distributions are summed per identity
// on a common tick axis and the bus factor is recomputed from each sum. It is a threshold over the
// combined distribution and cannot be merged from the per-repository factors.
func (bf *BusFactorAnalysis) MergeResults(
	result1, result2 any, common1, common2 *core.CommonAnalysisResult,
) any {
	bfr1, err := requiredResult[BusFactorResult](result1)
	if err != nil {
		return fmt.Errorf("merge bus factor first result: %w", err)
	}

	bfr2, err := requiredResult[BusFactorResult](result2)
	if err != nil {
		return fmt.Errorf("merge bus factor second result: %w", err)
	}

	if bfr1.tickSize != bfr2.tickSize {
		return fmt.Errorf("%w (r1: %d, r2: %d) received",
			errBusFactorMismatchingTickSizes, bfr1.tickSize, bfr2.tickSize)
	}

	if bfr1.Threshold != bfr2.Threshold {
		return fmt.Errorf("%w (r1: %f, r2: %f) received",
			errBusFactorMismatchingThresholds, bfr1.Threshold, bfr2.Threshold)
	}

	people, mergedPeopleDict := join.PeopleIdentities(bfr1.reversedPeopleDict, bfr2.reversedPeopleDict)
	offset1, offset2 := mergedTickOffsets(common1, common2, bfr1.tickSize)

	merged := BusFactorResult{
		Snapshots:          make(map[int]*BusFactorSnapshot),
		SubsystemBusFactor: make(map[string]int),
		Threshold:          bfr1.Threshold,
		reversedPeopleDict: mergedPeopleDict,
		tickSize:           bfr1.tickSize,
	}

	summed := mergeOwnershipTicks(
		busFactorOwnership(bfr1.Snapshots), busFactorOwnership(bfr2.Snapshots),
		offset1, offset2, people,
	)
	for tick, lines := range summed {
		merged.Snapshots[tick] = &BusFactorSnapshot{
			BusFactor:   computeBusFactor(lines.AuthorLines, lines.TotalLines, merged.Threshold),
			TotalLines:  lines.TotalLines,
			AuthorLines: lines.AuthorLines,
		}
	}

	// Merge subsystem bus factors: take the max (worst case). The per-directory distributions
	// are not part of the result, so they cannot be summed the way the per-tick ones are.
	maps.Copy(merged.SubsystemBusFactor, bfr1.SubsystemBusFactor)

	for dir, bf := range bfr2.SubsystemBusFactor {
		if existing, ok := merged.SubsystemBusFactor[dir]; !ok || bf > existing {
			merged.SubsystemBusFactor[dir] = bf
		}
	}

	return merged
}

// busFactorOwnership views the per-tick snapshots as bare ownership distributions.
func busFactorOwnership(snapshots map[int]*BusFactorSnapshot) map[int]ownershipTickLines {
	lines := make(map[int]ownershipTickLines, len(snapshots))
	for tick, snapshot := range snapshots {
		lines[tick] = ownershipTickLines{
			TotalLines:  snapshot.TotalLines,
			AuthorLines: snapshot.AuthorLines,
		}
	}

	return lines
}

func (bf *BusFactorAnalysis) takeSnapshot(tick int, totals ownershipTotals) {
	bf.snapshots[tick] = &BusFactorSnapshot{
		BusFactor:   computeBusFactor(totals.AuthorLines, totals.TotalLines, bf.Threshold),
		TotalLines:  totals.TotalLines,
		AuthorLines: totals.AuthorLines,
	}
}

// computeSubsystemBusFactor computes bus factor per directory prefix at the final tick.
func (bf *BusFactorAnalysis) computeSubsystemBusFactor() map[string]int {
	if bf.ownership == nil {
		return nil
	}

	subsystems := bf.ownership.subsystemOwnership()
	if subsystems == nil {
		return nil
	}

	result := make(map[string]int, len(subsystems))
	for dir, authorLines := range subsystems {
		var totalLines int64
		for _, lines := range authorLines {
			totalLines += lines
		}

		result[dir] = computeBusFactor(authorLines, totalLines, bf.Threshold)
	}

	return result
}

func (bf *BusFactorAnalysis) serializeText(result *BusFactorResult, writer io.Writer) {
	// Serialize's legacy text path has no error channel.
	_, _ = fmt.Fprintln(writer, "  bus_factor:")
	_, _ = fmt.Fprintf(writer, "    threshold: %.2f\n", result.Threshold)

	// Sort ticks for deterministic output
	ticks := make([]int, 0, len(result.Snapshots))
	for tick := range result.Snapshots {
		ticks = append(ticks, tick)
	}

	sort.Ints(ticks)

	_, _ = fmt.Fprintln(writer, "    per_tick:")

	for _, tick := range ticks {
		snapshot := result.Snapshots[tick]
		_, _ = fmt.Fprintf(writer, "      %d: {bus_factor: %d, total_lines: %d}\n",
			tick, snapshot.BusFactor, snapshot.TotalLines)
	}

	if len(result.SubsystemBusFactor) > 0 {
		_, _ = fmt.Fprintln(writer, "    per_subsystem:")

		dirs := make([]string, 0, len(result.SubsystemBusFactor))
		for dir := range result.SubsystemBusFactor {
			dirs = append(dirs, dir)
		}

		sort.Strings(dirs)

		for _, dir := range dirs {
			_, _ = fmt.Fprintf(writer, "      %s: %d\n", yaml.SafeString(dir), result.SubsystemBusFactor[dir])
		}
	}

	_, _ = fmt.Fprintln(writer, "    people:")

	for _, person := range result.reversedPeopleDict {
		_, _ = fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}

	_, _ = fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

func (bf *BusFactorAnalysis) serializeBinary(result *BusFactorResult, writer io.Writer) error {
	message := pb.BusFactorAnalysisResults{
		DevIndex:  result.reversedPeopleDict,
		TickSize:  int64(result.tickSize),
		Threshold: result.Threshold,
	}

	message.Snapshots = make(map[int32]*pb.BusFactorTickSnapshot, len(result.Snapshots))
	for tick, snapshot := range result.Snapshots {
		busFactor, err := intToProtoInt32(snapshot.BusFactor, "bus factor")
		if err != nil {
			return err
		}

		pbSnapshot := &pb.BusFactorTickSnapshot{
			BusFactor:   busFactor,
			TotalLines:  snapshot.TotalLines,
			AuthorLines: make(map[int32]int64, len(snapshot.AuthorLines)),
		}
		for author, lines := range snapshot.AuthorLines {
			authorID, err := intToProtoInt32(author, "bus factor author")
			if err != nil {
				return err
			}

			pbSnapshot.AuthorLines[authorID] = lines
		}

		tickID, err := intToProtoInt32(tick, "bus factor tick")
		if err != nil {
			return err
		}

		message.Snapshots[tickID] = pbSnapshot
	}

	message.SubsystemBusFactor = make(map[string]int32, len(result.SubsystemBusFactor))
	for dir, factor := range result.SubsystemBusFactor {
		pbFactor, err := intToProtoInt32(factor, "subsystem bus factor")
		if err != nil {
			return err
		}

		message.SubsystemBusFactor[dir] = pbFactor
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal bus factor result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write bus factor result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&BusFactorAnalysis{})
