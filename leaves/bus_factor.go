package leaves

import (
	"fmt"
	"io"
	"path"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/linehistory"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
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

	// fileResolver is used to scan files for current ownership state.
	fileResolver core.FileIdResolver
	// peopleResolver resolves author IDs to names.
	peopleResolver core.IdentityResolver
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize references TicksSinceStart.TickSize.
	tickSize time.Duration
	// snapshots stores per-tick bus factor snapshots.
	snapshots map[int]*BusFactorSnapshot
	// lastTick tracks the most recent tick seen.
	lastTick int

	l core.Logger
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
		linehistory.DependencyLineHistory,
		identity.DependencyAuthor,
		items.DependencyTick,
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
func (bf *BusFactorAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		bf.l = l
	}
	if val, exists := facts[ConfigBusFactorThreshold]; exists {
		bf.Threshold = val.(float32)
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
func (*BusFactorAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
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
	bf.lastTick = -1
	if bf.Threshold <= 0 || bf.Threshold > 1 {
		bf.Threshold = 0.8
	}
	return nil
}

// Consume runs this PipelineItem on the next commit data.
// It captures the file resolver from LineHistoryChanges and records the current tick.
// The actual ownership scanning happens at each new tick boundary.
func (bf *BusFactorAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	changes := deps[linehistory.DependencyLineHistory].(core.LineHistoryChanges)
	tick := deps[items.DependencyTick].(int)
	bf.fileResolver = changes.Resolver

	// Take a snapshot when we move to a new tick
	if tick > bf.lastTick {
		if bf.lastTick >= 0 {
			bf.takeSnapshot(bf.lastTick)
		}
		bf.lastTick = tick
	}

	return nil, nil
}

// takeSnapshot scans all files and computes the bus factor for the given tick.
func (bf *BusFactorAnalysis) takeSnapshot(tick int) {
	if bf.fileResolver == nil {
		return
	}

	authorLines := map[int]int64{}
	bf.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		previousLine := 0
		previousAuthor := int(core.AuthorMissing)

		bf.fileResolver.ScanFile(fileId,
			func(line int, _ core.TickNumber, author core.AuthorId) {
				length := line - previousLine
				if length > 0 && previousAuthor != int(core.AuthorMissing) {
					authorLines[previousAuthor] += int64(length)
				}
				previousLine = line
				if author >= core.AuthorMissing {
					previousAuthor = int(core.AuthorMissing)
				} else {
					previousAuthor = int(author)
				}
			})
	})

	var totalLines int64
	for _, lines := range authorLines {
		totalLines += lines
	}

	busFactor := computeBusFactor(authorLines, totalLines, bf.Threshold)

	// Copy the map for the snapshot
	snapshotLines := make(map[int]int64, len(authorLines))
	for k, v := range authorLines {
		snapshotLines[k] = v
	}

	bf.snapshots[tick] = &BusFactorSnapshot{
		BusFactor:   busFactor,
		TotalLines:  totalLines,
		AuthorLines: snapshotLines,
	}
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

	target := int64(float64(threshold) * float64(totalLines))
	var cumulative int64
	for k, c := range counts {
		cumulative += c
		if cumulative >= target {
			return k + 1
		}
	}
	return len(counts)
}

// computeSubsystemBusFactor computes bus factor per directory prefix at the final tick.
func (bf *BusFactorAnalysis) computeSubsystemBusFactor() map[string]int {
	if bf.fileResolver == nil {
		return nil
	}

	// Accumulate per-directory, per-author line counts
	subsystems := map[string]map[int]int64{} // dir -> author -> lines
	bf.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		dir := path.Dir(fileName)
		if dir == "." {
			dir = "/"
		}

		previousLine := 0
		previousAuthor := int(core.AuthorMissing)

		bf.fileResolver.ScanFile(fileId,
			func(line int, _ core.TickNumber, author core.AuthorId) {
				length := line - previousLine
				if length > 0 && previousAuthor != int(core.AuthorMissing) {
					dirAuthors := subsystems[dir]
					if dirAuthors == nil {
						dirAuthors = map[int]int64{}
						subsystems[dir] = dirAuthors
					}
					dirAuthors[previousAuthor] += int64(length)
				}
				previousLine = line
				if author >= core.AuthorMissing {
					previousAuthor = int(core.AuthorMissing)
				} else {
					previousAuthor = int(author)
				}
			})
	})

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

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (bf *BusFactorAnalysis) Finalize() interface{} {
	// Take the final snapshot for the last tick
	if bf.lastTick >= 0 {
		bf.takeSnapshot(bf.lastTick)
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
func (bf *BusFactorAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	bfResult := result.(BusFactorResult)
	if binary {
		return bf.serializeBinary(&bfResult, writer)
	}
	bf.serializeText(&bfResult, writer)
	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (bf *BusFactorAnalysis) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.BusFactorAnalysisResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]*BusFactorSnapshot, len(message.Snapshots))
	for tick, pbSnapshot := range message.Snapshots {
		authorLines := make(map[int]int64, len(pbSnapshot.AuthorLines))
		for authorID, lines := range pbSnapshot.AuthorLines {
			dev := int(authorID)
			if authorID == -1 {
				dev = core.AuthorMissing
			}
			authorLines[dev] = lines
		}
		snapshots[int(tick)] = &BusFactorSnapshot{
			BusFactor:   int(pbSnapshot.BusFactor),
			TotalLines:  pbSnapshot.TotalLines,
			AuthorLines: authorLines,
		}
	}

	subsystemBF := make(map[string]int, len(message.SubsystemBusFactor))
	for dir, bf := range message.SubsystemBusFactor {
		subsystemBF[dir] = int(bf)
	}

	result := BusFactorResult{
		Snapshots:          snapshots,
		SubsystemBusFactor: subsystemBF,
		Threshold:          message.Threshold,
		reversedPeopleDict: message.DevIndex,
		tickSize:           time.Duration(message.TickSize),
	}
	return result, nil
}

func (bf *BusFactorAnalysis) serializeText(result *BusFactorResult, writer io.Writer) {
	fmt.Fprintln(writer, "  bus_factor:")
	fmt.Fprintf(writer, "    threshold: %.2f\n", result.Threshold)

	// Sort ticks for deterministic output
	ticks := make([]int, 0, len(result.Snapshots))
	for tick := range result.Snapshots {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)

	fmt.Fprintln(writer, "    per_tick:")
	for _, tick := range ticks {
		snapshot := result.Snapshots[tick]
		fmt.Fprintf(writer, "      %d: {bus_factor: %d, total_lines: %d}\n",
			tick, snapshot.BusFactor, snapshot.TotalLines)
	}

	if len(result.SubsystemBusFactor) > 0 {
		fmt.Fprintln(writer, "    per_subsystem:")
		dirs := make([]string, 0, len(result.SubsystemBusFactor))
		for dir := range result.SubsystemBusFactor {
			dirs = append(dirs, dir)
		}
		sort.Strings(dirs)
		for _, dir := range dirs {
			fmt.Fprintf(writer, "      %s: %d\n", yaml.SafeString(dir), result.SubsystemBusFactor[dir])
		}
	}

	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
	fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

func (bf *BusFactorAnalysis) serializeBinary(result *BusFactorResult, writer io.Writer) error {
	message := pb.BusFactorAnalysisResults{
		DevIndex:  result.reversedPeopleDict,
		TickSize:  int64(result.tickSize),
		Threshold: result.Threshold,
	}

	message.Snapshots = make(map[int32]*pb.BusFactorTickSnapshot, len(result.Snapshots))
	for tick, snapshot := range result.Snapshots {
		pbSnapshot := &pb.BusFactorTickSnapshot{
			BusFactor:   int32(snapshot.BusFactor),
			TotalLines:  snapshot.TotalLines,
			AuthorLines: make(map[int32]int64, len(snapshot.AuthorLines)),
		}
		for author, lines := range snapshot.AuthorLines {
			authorID := int32(author)
			if author == core.AuthorMissing {
				authorID = -1
			}
			pbSnapshot.AuthorLines[authorID] = lines
		}
		message.Snapshots[int32(tick)] = pbSnapshot
	}

	message.SubsystemBusFactor = make(map[string]int32, len(result.SubsystemBusFactor))
	for dir, bf := range result.SubsystemBusFactor {
		message.SubsystemBusFactor[dir] = int32(bf)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// MergeResults combines two BusFactorResult-s together.
func (bf *BusFactorAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	bfr1 := r1.(BusFactorResult)
	bfr2 := r2.(BusFactorResult)

	merged := BusFactorResult{
		Snapshots:          make(map[int]*BusFactorSnapshot),
		SubsystemBusFactor: make(map[string]int),
		Threshold:          bfr1.Threshold,
		reversedPeopleDict: bfr1.reversedPeopleDict,
		tickSize:           bfr1.tickSize,
	}

	// Merge snapshots: take the snapshot with the larger total lines for overlapping ticks
	for tick, snapshot := range bfr1.Snapshots {
		merged.Snapshots[tick] = snapshot
	}
	for tick, snapshot := range bfr2.Snapshots {
		if existing, ok := merged.Snapshots[tick]; !ok || snapshot.TotalLines > existing.TotalLines {
			merged.Snapshots[tick] = snapshot
		}
	}

	// Merge subsystem bus factors: take the max (worst case)
	for dir, bf := range bfr1.SubsystemBusFactor {
		merged.SubsystemBusFactor[dir] = bf
	}
	for dir, bf := range bfr2.SubsystemBusFactor {
		if existing, ok := merged.SubsystemBusFactor[dir]; !ok || bf > existing {
			merged.SubsystemBusFactor[dir] = bf
		}
	}

	return merged
}

func init() {
	core.Registry.Register(&BusFactorAnalysis{})
}
