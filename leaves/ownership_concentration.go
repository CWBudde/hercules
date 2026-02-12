package leaves

import (
	"fmt"
	"io"
	"math"
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

// OwnershipConcentrationAnalysis computes the Gini coefficient and
// Herfindahl-Hirschman Index (HHI) of code ownership over time.
// Gini = 0 means perfectly equal ownership, Gini = 1 means one person owns
// everything. HHI ranges from 1/n (equal) to 1.0 (single author).
//
// It consumes LineHistoryChanges to track per-file, per-author alive-line
// counts and snapshots concentration metrics at each tick.
type OwnershipConcentrationAnalysis struct {
	core.NoopMerger

	// fileResolver is used to scan files for current ownership state.
	fileResolver core.FileIdResolver
	// peopleResolver resolves author IDs to names.
	peopleResolver core.IdentityResolver
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize references TicksSinceStart.TickSize.
	tickSize time.Duration
	// snapshots stores per-tick concentration snapshots.
	snapshots map[int]*OwnershipConcentrationSnapshot
	// lastTick tracks the most recent tick seen.
	lastTick int

	l core.Logger
}

// OwnershipConcentrationSnapshot stores concentration metrics at a single tick.
type OwnershipConcentrationSnapshot struct {
	// Gini coefficient (0 = perfectly equal, 1 = one person owns everything).
	Gini float64
	// HHI (Herfindahl-Hirschman Index), ranges from 1/n to 1.0.
	HHI float64
	// TotalLines is the total number of alive lines at this tick.
	TotalLines int64
	// AuthorLines maps author index to their alive line count.
	AuthorLines map[int]int64
}

// SubsystemConcentration stores per-directory concentration at the final tick.
type SubsystemConcentration struct {
	Gini float64
	HHI  float64
}

// OwnershipConcentrationResult is returned by OwnershipConcentrationAnalysis.Finalize().
type OwnershipConcentrationResult struct {
	// Snapshots maps tick index to the concentration snapshot at that tick.
	Snapshots map[int]*OwnershipConcentrationSnapshot
	// SubsystemConcentration maps directory prefix to concentration metrics at the final tick.
	SubsystemConcentration map[string]*SubsystemConcentration
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict.
	reversedPeopleDict []string
	// tickSize is the duration of each tick.
	tickSize time.Duration
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (oc *OwnershipConcentrationAnalysis) Name() string {
	return "OwnershipConcentration"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (oc *OwnershipConcentrationAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (oc *OwnershipConcentrationAnalysis) Requires() []string {
	return []string{
		linehistory.DependencyLineHistory,
		identity.DependencyAuthor,
		items.DependencyTick,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (oc *OwnershipConcentrationAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return nil
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (oc *OwnershipConcentrationAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		oc.l = l
	}
	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		oc.reversedPeopleDict = val
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		oc.tickSize = val
	}
	if val, ok := facts[core.FactIdentityResolver].(core.IdentityResolver); ok {
		oc.peopleResolver = val
	}
	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*OwnershipConcentrationAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (oc *OwnershipConcentrationAnalysis) Flag() string {
	return "ownership-concentration"
}

// Description returns the text which explains what the analysis is doing.
func (oc *OwnershipConcentrationAnalysis) Description() string {
	return "Computes Gini coefficient and HHI of code ownership concentration over time."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (oc *OwnershipConcentrationAnalysis) Initialize(repository *git.Repository) error {
	oc.l = core.NewLogger()
	oc.snapshots = map[int]*OwnershipConcentrationSnapshot{}
	oc.lastTick = -1
	return nil
}

// Consume runs this PipelineItem on the next commit data.
func (oc *OwnershipConcentrationAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	changes := deps[linehistory.DependencyLineHistory].(core.LineHistoryChanges)
	tick := deps[items.DependencyTick].(int)
	oc.fileResolver = changes.Resolver

	if tick > oc.lastTick {
		if oc.lastTick >= 0 {
			oc.takeSnapshot(oc.lastTick)
		}
		oc.lastTick = tick
	}

	return nil, nil
}

// takeSnapshot scans all files and computes concentration metrics for the given tick.
func (oc *OwnershipConcentrationAnalysis) takeSnapshot(tick int) {
	if oc.fileResolver == nil {
		return
	}

	authorLines := map[int]int64{}
	oc.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		previousLine := 0
		previousAuthor := int(core.AuthorMissing)

		oc.fileResolver.ScanFile(fileId,
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

	gini := computeGini(authorLines, totalLines)
	hhi := computeHHI(authorLines, totalLines)

	snapshotLines := make(map[int]int64, len(authorLines))
	for k, v := range authorLines {
		snapshotLines[k] = v
	}

	oc.snapshots[tick] = &OwnershipConcentrationSnapshot{
		Gini:        gini,
		HHI:         hhi,
		TotalLines:  totalLines,
		AuthorLines: snapshotLines,
	}
}

// computeGini computes the Gini coefficient from author line counts.
// Uses the standard formula: G = (2 * Sum(i * x_i)) / (n * S) - (n+1)/n
// where x_i are sorted ascending and S is the total.
// Returns 0 for 0 or 1 authors.
func computeGini(authorLines map[int]int64, totalLines int64) float64 {
	n := len(authorLines)
	if n <= 1 || totalLines == 0 {
		return 0
	}

	// Sort line counts ascending
	counts := make([]int64, 0, n)
	for _, lines := range authorLines {
		counts = append(counts, lines)
	}
	sort.Slice(counts, func(i, j int) bool {
		return counts[i] < counts[j]
	})

	// G = (2 * Sum_{i=1}^{n} i * x_i) / (n * S) - (n+1)/n
	var weightedSum float64
	for i, c := range counts {
		weightedSum += float64(i+1) * float64(c)
	}
	nf := float64(n)
	gini := (2.0*weightedSum)/(nf*float64(totalLines)) - (nf+1.0)/nf
	// Clamp to [0,1] to handle floating point imprecision
	return math.Max(0, math.Min(1, gini))
}

// computeHHI computes the Herfindahl-Hirschman Index from author line counts.
// Returns 0 for 0 authors, 1 for 1 author.
func computeHHI(authorLines map[int]int64, totalLines int64) float64 {
	if totalLines == 0 || len(authorLines) == 0 {
		return 0
	}

	var hhi float64
	for _, lines := range authorLines {
		share := float64(lines) / float64(totalLines)
		hhi += share * share
	}
	return hhi
}

// computeSubsystemConcentration computes Gini and HHI per directory prefix at the final tick.
func (oc *OwnershipConcentrationAnalysis) computeSubsystemConcentration() map[string]*SubsystemConcentration {
	if oc.fileResolver == nil {
		return nil
	}

	subsystems := map[string]map[int]int64{} // dir -> author -> lines
	oc.fileResolver.ForEachFile(func(fileId core.FileId, fileName string) {
		dir := path.Dir(fileName)
		if dir == "." {
			dir = "/"
		}

		previousLine := 0
		previousAuthor := int(core.AuthorMissing)

		oc.fileResolver.ScanFile(fileId,
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

	result := make(map[string]*SubsystemConcentration, len(subsystems))
	for dir, authorLines := range subsystems {
		var totalLines int64
		for _, lines := range authorLines {
			totalLines += lines
		}
		result[dir] = &SubsystemConcentration{
			Gini: computeGini(authorLines, totalLines),
			HHI:  computeHHI(authorLines, totalLines),
		}
	}
	return result
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (oc *OwnershipConcentrationAnalysis) Finalize() interface{} {
	if oc.lastTick >= 0 {
		oc.takeSnapshot(oc.lastTick)
	}

	return OwnershipConcentrationResult{
		Snapshots:              oc.snapshots,
		SubsystemConcentration: oc.computeSubsystemConcentration(),
		reversedPeopleDict:     oc.reversedPeopleDict,
		tickSize:               oc.tickSize,
	}
}

// Fork clones this pipeline item.
func (oc *OwnershipConcentrationAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(oc, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (oc *OwnershipConcentrationAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	ocResult := result.(OwnershipConcentrationResult)
	if binary {
		return oc.serializeBinary(&ocResult, writer)
	}
	oc.serializeText(&ocResult, writer)
	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (oc *OwnershipConcentrationAnalysis) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.OwnershipConcentrationResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]*OwnershipConcentrationSnapshot, len(message.Snapshots))
	for tick, pbSnapshot := range message.Snapshots {
		authorLines := make(map[int]int64, len(pbSnapshot.AuthorLines))
		for authorID, lines := range pbSnapshot.AuthorLines {
			dev := int(authorID)
			if authorID == -1 {
				dev = core.AuthorMissing
			}
			authorLines[dev] = lines
		}
		snapshots[int(tick)] = &OwnershipConcentrationSnapshot{
			Gini:        pbSnapshot.Gini,
			HHI:         pbSnapshot.Hhi,
			TotalLines:  pbSnapshot.TotalLines,
			AuthorLines: authorLines,
		}
	}

	subsystemConc := make(map[string]*SubsystemConcentration)
	for dir := range message.SubsystemGini {
		subsystemConc[dir] = &SubsystemConcentration{
			Gini: message.SubsystemGini[dir],
			HHI:  message.SubsystemHhi[dir],
		}
	}

	result := OwnershipConcentrationResult{
		Snapshots:              snapshots,
		SubsystemConcentration: subsystemConc,
		reversedPeopleDict:     message.DevIndex,
		tickSize:               time.Duration(message.TickSize),
	}
	return result, nil
}

func (oc *OwnershipConcentrationAnalysis) serializeText(result *OwnershipConcentrationResult, writer io.Writer) {
	fmt.Fprintln(writer, "  ownership_concentration:")

	ticks := make([]int, 0, len(result.Snapshots))
	for tick := range result.Snapshots {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)

	fmt.Fprintln(writer, "    per_tick:")
	for _, tick := range ticks {
		snapshot := result.Snapshots[tick]
		fmt.Fprintf(writer, "      %d: {gini: %.4f, hhi: %.4f, total_lines: %d}\n",
			tick, snapshot.Gini, snapshot.HHI, snapshot.TotalLines)
	}

	if len(result.SubsystemConcentration) > 0 {
		fmt.Fprintln(writer, "    per_subsystem:")
		dirs := make([]string, 0, len(result.SubsystemConcentration))
		for dir := range result.SubsystemConcentration {
			dirs = append(dirs, dir)
		}
		sort.Strings(dirs)
		for _, dir := range dirs {
			sc := result.SubsystemConcentration[dir]
			fmt.Fprintf(writer, "      %s: {gini: %.4f, hhi: %.4f}\n", yaml.SafeString(dir), sc.Gini, sc.HHI)
		}
	}

	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
	fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

func (oc *OwnershipConcentrationAnalysis) serializeBinary(result *OwnershipConcentrationResult, writer io.Writer) error {
	message := pb.OwnershipConcentrationResults{
		DevIndex: result.reversedPeopleDict,
		TickSize: int64(result.tickSize),
	}

	message.Snapshots = make(map[int32]*pb.OwnershipConcentrationTickSnapshot, len(result.Snapshots))
	for tick, snapshot := range result.Snapshots {
		pbSnapshot := &pb.OwnershipConcentrationTickSnapshot{
			Gini:        snapshot.Gini,
			Hhi:         snapshot.HHI,
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

	message.SubsystemGini = make(map[string]float64, len(result.SubsystemConcentration))
	message.SubsystemHhi = make(map[string]float64, len(result.SubsystemConcentration))
	for dir, sc := range result.SubsystemConcentration {
		message.SubsystemGini[dir] = sc.Gini
		message.SubsystemHhi[dir] = sc.HHI
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// MergeResults combines two OwnershipConcentrationResult-s together.
func (oc *OwnershipConcentrationAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	ocr1 := r1.(OwnershipConcentrationResult)
	ocr2 := r2.(OwnershipConcentrationResult)

	merged := OwnershipConcentrationResult{
		Snapshots:              make(map[int]*OwnershipConcentrationSnapshot),
		SubsystemConcentration: make(map[string]*SubsystemConcentration),
		reversedPeopleDict:     ocr1.reversedPeopleDict,
		tickSize:               ocr1.tickSize,
	}

	// Merge snapshots: take the snapshot with the larger total lines for overlapping ticks
	for tick, snapshot := range ocr1.Snapshots {
		merged.Snapshots[tick] = snapshot
	}
	for tick, snapshot := range ocr2.Snapshots {
		if existing, ok := merged.Snapshots[tick]; !ok || snapshot.TotalLines > existing.TotalLines {
			merged.Snapshots[tick] = snapshot
		}
	}

	// Merge subsystem concentration: take from the result with more data (higher Gini as tiebreaker)
	for dir, sc := range ocr1.SubsystemConcentration {
		merged.SubsystemConcentration[dir] = sc
	}
	for dir, sc := range ocr2.SubsystemConcentration {
		if _, ok := merged.SubsystemConcentration[dir]; !ok {
			merged.SubsystemConcentration[dir] = sc
		}
	}

	return merged
}

func init() {
	core.Registry.Register(&OwnershipConcentrationAnalysis{})
}
