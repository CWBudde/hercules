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
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
)

// TemporalActivityAnalysis calculates both commit and line change activity across temporal dimensions.
// It tracks when developers work by extracting weekday, hour, month, and ISO week from commits.
// This complements DevsAnalysis which tracks activity over project lifetime.
//
// Use cases:
//   - Understand when developers typically work
//   - Identify time zone distribution of team
//   - Detect work pattern changes over time
//   - Assess work-life balance indicators
//
// The analysis tracks both commit counts and line changes simultaneously,
// providing richer insights into developer activity patterns.
//
// Data is stored both as aggregated totals (for backward compatibility) and
// as per-tick data (allowing date range filtering in post-processing).
type TemporalActivityAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// activities maps developer index to their temporal activity (aggregated totals)
	activities map[int]*DeveloperTemporalActivity
	// ticks maps tick index to developer index to temporal activity for that tick
	ticks map[int]map[int]*TemporalActivityTick
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
	// tickSize references TicksSinceStart.TickSize
	tickSize time.Duration

	l core.Logger
}

// TemporalDimension stores both commit counts and line change counts for a temporal dimension.
type TemporalDimension struct {
	Commits []int // Number of commits
	Lines   []int // Number of lines changed (added + removed)
}

// DeveloperTemporalActivity stores activity counts across temporal dimensions for one developer.
type DeveloperTemporalActivity struct {
	Weekdays TemporalDimension // Sunday=0 to Saturday=6 (length 7)
	Hours    TemporalDimension // 0-23 (length 24)
	Months   TemporalDimension // January=0 to December=11 (length 12)
	Weeks    TemporalDimension // ISO week 1-53 stored at index 0-52 (week N stored at index N-1, length 53)
}

// TemporalActivityTick stores temporal activity for a single tick (day).
type TemporalActivityTick struct {
	Commits int // Number of commits in this tick
	Lines   int // Number of lines changed in this tick
	Weekday int // Day of week (0-6, Sunday=0)
	Hour    int // Hour of day (0-23) - hour of first/primary commit
	Month   int // Month (0-11, January=0)
	Week    int // ISO week (0-52, week 1-53 stored as 0-52)
}

// TemporalActivityResult is returned by TemporalActivityAnalysis.Finalize().
type TemporalActivityResult struct {
	// Activities maps developer index to temporal activity (aggregated totals)
	Activities map[int]*DeveloperTemporalActivity
	// Ticks maps tick index to developer index to temporal activity for that tick
	Ticks map[int]map[int]*TemporalActivityTick
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
	// tickSize is the duration of each tick
	tickSize time.Duration
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (ta *TemporalActivityAnalysis) Name() string {
	return "TemporalActivity"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (ta *TemporalActivityAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (ta *TemporalActivityAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor,
		items.DependencyLineStats,
		items.DependencyTick,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (ta *TemporalActivityAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{}
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (ta *TemporalActivityAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		ta.l = l
	}
	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		ta.reversedPeopleDict = val
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		ta.tickSize = val
	}
	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*TemporalActivityAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (ta *TemporalActivityAnalysis) Flag() string {
	return "temporal-activity"
}

// Description returns the text which explains what the analysis is doing.
func (ta *TemporalActivityAnalysis) Description() string {
	return "Calculates commit and line change activity by weekday, hour, month, and ISO week."
}

// newTemporalDimension creates a new TemporalDimension with the specified size.
func newTemporalDimension(size int) TemporalDimension {
	return TemporalDimension{
		Commits: make([]int, size),
		Lines:   make([]int, size),
	}
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (ta *TemporalActivityAnalysis) Initialize(repository *git.Repository) error {
	ta.l = core.NewLogger()
	ta.activities = map[int]*DeveloperTemporalActivity{}
	ta.ticks = map[int]map[int]*TemporalActivityTick{}
	ta.OneShotMergeProcessor.Initialize()
	return nil
}

// Consume runs this PipelineItem on the next commit data.
func (ta *TemporalActivityAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !ta.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	commit := deps[core.DependencyCommit].(*object.Commit)
	author := deps[identity.DependencyAuthor].(int)
	tick := deps[items.DependencyTick].(int)

	// Extract temporal components from commit timestamp
	commitTime := commit.Author.When
	weekday := int(commitTime.Weekday()) // Sunday=0, Monday=1, ..., Saturday=6
	hour := commitTime.Hour()            // 0-23
	month := int(commitTime.Month()) - 1 // January=0, ..., December=11
	_, week := commitTime.ISOWeek()      // ISO week number 1-53

	// Normalize week to 0-based index (week 1-53 -> index 0-52)
	weekIndex := week - 1
	if weekIndex < 0 {
		weekIndex = 0
	} else if weekIndex > 52 {
		weekIndex = 52
	}

	// Get or create activity tracker for this developer (aggregated totals)
	activity := ta.activities[author]
	if activity == nil {
		activity = &DeveloperTemporalActivity{
			Weekdays: newTemporalDimension(7),
			Hours:    newTemporalDimension(24),
			Months:   newTemporalDimension(12),
			Weeks:    newTemporalDimension(53),
		}
		ta.activities[author] = activity
	}

	// Calculate line changes
	lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)
	totalLines := 0
	for _, stats := range lineStats {
		totalLines += stats.Added + stats.Removed
	}

	// Update aggregated temporal counters with both commits and lines
	activity.Weekdays.Commits[weekday] += 1
	activity.Weekdays.Lines[weekday] += totalLines

	activity.Hours.Commits[hour] += 1
	activity.Hours.Lines[hour] += totalLines

	activity.Months.Commits[month] += 1
	activity.Months.Lines[month] += totalLines

	activity.Weeks.Commits[weekIndex] += 1
	activity.Weeks.Lines[weekIndex] += totalLines

	// Store per-tick data for date range filtering
	tickDevs := ta.ticks[tick]
	if tickDevs == nil {
		tickDevs = map[int]*TemporalActivityTick{}
		ta.ticks[tick] = tickDevs
	}

	tickActivity := tickDevs[author]
	if tickActivity == nil {
		tickActivity = &TemporalActivityTick{
			Weekday: weekday,
			Hour:    hour,
			Month:   month,
			Week:    weekIndex,
		}
		tickDevs[author] = tickActivity
	}
	tickActivity.Commits += 1
	tickActivity.Lines += totalLines

	return nil, nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (ta *TemporalActivityAnalysis) Finalize() interface{} {
	return TemporalActivityResult{
		Activities:         ta.activities,
		Ticks:              ta.ticks,
		reversedPeopleDict: ta.reversedPeopleDict,
		tickSize:           ta.tickSize,
	}
}

// Fork clones this pipeline item.
func (ta *TemporalActivityAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(ta, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (ta *TemporalActivityAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	temporalResult := result.(TemporalActivityResult)
	if binary {
		return ta.serializeBinary(&temporalResult, writer)
	}
	ta.serializeText(&temporalResult, writer)
	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (ta *TemporalActivityAnalysis) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.TemporalActivityResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	activities := map[int]*DeveloperTemporalActivity{}
	for devID, pbActivity := range message.Activities {
		// Handle AuthorMissing special case
		dev := int(devID)
		if devID == -1 {
			dev = core.AuthorMissing
		}

		// Create native DeveloperTemporalActivity struct
		activity := &DeveloperTemporalActivity{
			Weekdays: newTemporalDimension(7),
			Hours:    newTemporalDimension(24),
			Months:   newTemporalDimension(12),
			Weeks:    newTemporalDimension(53),
		}

		// Copy weekdays
		if pbActivity.Weekdays != nil {
			for i := 0; i < 7 && i < len(pbActivity.Weekdays.Commits); i++ {
				activity.Weekdays.Commits[i] = int(pbActivity.Weekdays.Commits[i])
			}
			for i := 0; i < 7 && i < len(pbActivity.Weekdays.Lines); i++ {
				activity.Weekdays.Lines[i] = int(pbActivity.Weekdays.Lines[i])
			}
		}

		// Copy hours
		if pbActivity.Hours != nil {
			for i := 0; i < 24 && i < len(pbActivity.Hours.Commits); i++ {
				activity.Hours.Commits[i] = int(pbActivity.Hours.Commits[i])
			}
			for i := 0; i < 24 && i < len(pbActivity.Hours.Lines); i++ {
				activity.Hours.Lines[i] = int(pbActivity.Hours.Lines[i])
			}
		}

		// Copy months
		if pbActivity.Months != nil {
			for i := 0; i < 12 && i < len(pbActivity.Months.Commits); i++ {
				activity.Months.Commits[i] = int(pbActivity.Months.Commits[i])
			}
			for i := 0; i < 12 && i < len(pbActivity.Months.Lines); i++ {
				activity.Months.Lines[i] = int(pbActivity.Months.Lines[i])
			}
		}

		// Copy weeks
		if pbActivity.Weeks != nil {
			for i := 0; i < 53 && i < len(pbActivity.Weeks.Commits); i++ {
				activity.Weeks.Commits[i] = int(pbActivity.Weeks.Commits[i])
			}
			for i := 0; i < 53 && i < len(pbActivity.Weeks.Lines); i++ {
				activity.Weeks.Lines[i] = int(pbActivity.Weeks.Lines[i])
			}
		}

		activities[dev] = activity
	}

	// Deserialize ticks
	ticks := map[int]map[int]*TemporalActivityTick{}
	for tickID, pbTickDevs := range message.Ticks {
		tickDevs := map[int]*TemporalActivityTick{}
		for devID, pbTick := range pbTickDevs.Devs {
			dev := int(devID)
			if devID == -1 {
				dev = core.AuthorMissing
			}
			tickDevs[dev] = &TemporalActivityTick{
				Commits: int(pbTick.Commits),
				Lines:   int(pbTick.Lines),
				Weekday: int(pbTick.Weekday),
				Hour:    int(pbTick.Hour),
				Month:   int(pbTick.Month),
				Week:    int(pbTick.Week),
			}
		}
		ticks[int(tickID)] = tickDevs
	}

	result := TemporalActivityResult{
		Activities:         activities,
		Ticks:              ticks,
		reversedPeopleDict: message.DevIndex,
		tickSize:           time.Duration(message.TickSize),
	}
	return result, nil
}

func (ta *TemporalActivityAnalysis) serializeText(result *TemporalActivityResult, writer io.Writer) {
	fmt.Fprintln(writer, "  temporal_activity:")
	fmt.Fprintln(writer, "    activities:")

	// Sort developers for consistent output
	devs := make([]int, 0, len(result.Activities))
	for dev := range result.Activities {
		devs = append(devs, dev)
	}
	sort.Ints(devs)

	for _, dev := range devs {
		activity := result.Activities[dev]
		devID := dev
		if dev == core.AuthorMissing {
			devID = -1
		}

		fmt.Fprintf(writer, "      %d:\n", devID)

		// Weekdays
		fmt.Fprintf(writer, "        weekdays_commits: [")
		for i, count := range activity.Weekdays.Commits {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
		fmt.Fprintf(writer, "        weekdays_lines: [")
		for i, count := range activity.Weekdays.Lines {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Hours
		fmt.Fprintf(writer, "        hours_commits: [")
		for i, count := range activity.Hours.Commits {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
		fmt.Fprintf(writer, "        hours_lines: [")
		for i, count := range activity.Hours.Lines {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Months
		fmt.Fprintf(writer, "        months_commits: [")
		for i, count := range activity.Months.Commits {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
		fmt.Fprintf(writer, "        months_lines: [")
		for i, count := range activity.Months.Lines {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")

		// Weeks
		fmt.Fprintf(writer, "        weeks_commits: [")
		for i, count := range activity.Weeks.Commits {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
		fmt.Fprintf(writer, "        weeks_lines: [")
		for i, count := range activity.Weeks.Lines {
			if i > 0 {
				fmt.Fprint(writer, ", ")
			}
			fmt.Fprintf(writer, "%d", count)
		}
		fmt.Fprintln(writer, "]")
	}

	// Output people dictionary
	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
}

func (ta *TemporalActivityAnalysis) serializeBinary(result *TemporalActivityResult, writer io.Writer) error {
	message := pb.TemporalActivityResults{}
	message.DevIndex = result.reversedPeopleDict
	message.Activities = make(map[int32]*pb.DeveloperTemporalActivity)

	for dev, activity := range result.Activities {
		devID := int32(dev)
		if dev == core.AuthorMissing {
			devID = -1
		}

		pbActivity := &pb.DeveloperTemporalActivity{
			Weekdays: &pb.TemporalDimension{
				Commits: make([]int32, len(activity.Weekdays.Commits)),
				Lines:   make([]int32, len(activity.Weekdays.Lines)),
			},
			Hours: &pb.TemporalDimension{
				Commits: make([]int32, len(activity.Hours.Commits)),
				Lines:   make([]int32, len(activity.Hours.Lines)),
			},
			Months: &pb.TemporalDimension{
				Commits: make([]int32, len(activity.Months.Commits)),
				Lines:   make([]int32, len(activity.Months.Lines)),
			},
			Weeks: &pb.TemporalDimension{
				Commits: make([]int32, len(activity.Weeks.Commits)),
				Lines:   make([]int32, len(activity.Weeks.Lines)),
			},
		}

		// Copy weekdays
		for i, count := range activity.Weekdays.Commits {
			pbActivity.Weekdays.Commits[i] = int32(count)
		}
		for i, count := range activity.Weekdays.Lines {
			pbActivity.Weekdays.Lines[i] = int32(count)
		}

		// Copy hours
		for i, count := range activity.Hours.Commits {
			pbActivity.Hours.Commits[i] = int32(count)
		}
		for i, count := range activity.Hours.Lines {
			pbActivity.Hours.Lines[i] = int32(count)
		}

		// Copy months
		for i, count := range activity.Months.Commits {
			pbActivity.Months.Commits[i] = int32(count)
		}
		for i, count := range activity.Months.Lines {
			pbActivity.Months.Lines[i] = int32(count)
		}

		// Copy weeks
		for i, count := range activity.Weeks.Commits {
			pbActivity.Weeks.Commits[i] = int32(count)
		}
		for i, count := range activity.Weeks.Lines {
			pbActivity.Weeks.Lines[i] = int32(count)
		}

		message.Activities[devID] = pbActivity
	}

	// Serialize ticks
	message.Ticks = make(map[int32]*pb.TemporalActivityTickDevs)
	message.TickSize = int64(result.tickSize)
	for tick, tickDevs := range result.Ticks {
		pbTickDevs := &pb.TemporalActivityTickDevs{
			Devs: make(map[int32]*pb.TemporalActivityTick),
		}
		for dev, tickActivity := range tickDevs {
			devID := int32(dev)
			if dev == core.AuthorMissing {
				devID = -1
			}
			pbTickDevs.Devs[devID] = &pb.TemporalActivityTick{
				Commits: int32(tickActivity.Commits),
				Lines:   int32(tickActivity.Lines),
				Weekday: int32(tickActivity.Weekday),
				Hour:    int32(tickActivity.Hour),
				Month:   int32(tickActivity.Month),
				Week:    int32(tickActivity.Week),
			}
		}
		message.Ticks[int32(tick)] = pbTickDevs
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

func init() {
	core.Registry.Register(&TemporalActivityAnalysis{})
}

// MergeResults combines two TemporalActivityResult-s together.
func (ta *TemporalActivityAnalysis) MergeResults(
	r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult,
) interface{} {
	tar1 := r1.(TemporalActivityResult)
	tar2 := r2.(TemporalActivityResult)

	merged := TemporalActivityResult{
		Activities:         make(map[int]*DeveloperTemporalActivity),
		Ticks:              make(map[int]map[int]*TemporalActivityTick),
		reversedPeopleDict: tar1.reversedPeopleDict, // Use first dict, should be same
		tickSize:           tar1.tickSize,
	}

	// Merge activities from both results
	allDevs := make(map[int]bool)
	for dev := range tar1.Activities {
		allDevs[dev] = true
	}
	for dev := range tar2.Activities {
		allDevs[dev] = true
	}

	for dev := range allDevs {
		mergedActivity := &DeveloperTemporalActivity{
			Weekdays: newTemporalDimension(7),
			Hours:    newTemporalDimension(24),
			Months:   newTemporalDimension(12),
			Weeks:    newTemporalDimension(53),
		}

		// Add activities from r1
		if activity1, exists := tar1.Activities[dev]; exists {
			for i := range mergedActivity.Weekdays.Commits {
				mergedActivity.Weekdays.Commits[i] += activity1.Weekdays.Commits[i]
				mergedActivity.Weekdays.Lines[i] += activity1.Weekdays.Lines[i]
			}
			for i := range mergedActivity.Hours.Commits {
				mergedActivity.Hours.Commits[i] += activity1.Hours.Commits[i]
				mergedActivity.Hours.Lines[i] += activity1.Hours.Lines[i]
			}
			for i := range mergedActivity.Months.Commits {
				mergedActivity.Months.Commits[i] += activity1.Months.Commits[i]
				mergedActivity.Months.Lines[i] += activity1.Months.Lines[i]
			}
			for i := range mergedActivity.Weeks.Commits {
				mergedActivity.Weeks.Commits[i] += activity1.Weeks.Commits[i]
				mergedActivity.Weeks.Lines[i] += activity1.Weeks.Lines[i]
			}
		}

		// Add activities from r2
		if activity2, exists := tar2.Activities[dev]; exists {
			for i := range mergedActivity.Weekdays.Commits {
				mergedActivity.Weekdays.Commits[i] += activity2.Weekdays.Commits[i]
				mergedActivity.Weekdays.Lines[i] += activity2.Weekdays.Lines[i]
			}
			for i := range mergedActivity.Hours.Commits {
				mergedActivity.Hours.Commits[i] += activity2.Hours.Commits[i]
				mergedActivity.Hours.Lines[i] += activity2.Hours.Lines[i]
			}
			for i := range mergedActivity.Months.Commits {
				mergedActivity.Months.Commits[i] += activity2.Months.Commits[i]
				mergedActivity.Months.Lines[i] += activity2.Months.Lines[i]
			}
			for i := range mergedActivity.Weeks.Commits {
				mergedActivity.Weeks.Commits[i] += activity2.Weeks.Commits[i]
				mergedActivity.Weeks.Lines[i] += activity2.Weeks.Lines[i]
			}
		}

		merged.Activities[dev] = mergedActivity
	}

	// Merge ticks from both results
	for tick, tickDevs := range tar1.Ticks {
		merged.Ticks[tick] = make(map[int]*TemporalActivityTick)
		for dev, tickActivity := range tickDevs {
			merged.Ticks[tick][dev] = &TemporalActivityTick{
				Commits: tickActivity.Commits,
				Lines:   tickActivity.Lines,
				Weekday: tickActivity.Weekday,
				Hour:    tickActivity.Hour,
				Month:   tickActivity.Month,
				Week:    tickActivity.Week,
			}
		}
	}
	for tick, tickDevs := range tar2.Ticks {
		if merged.Ticks[tick] == nil {
			merged.Ticks[tick] = make(map[int]*TemporalActivityTick)
		}
		for dev, tickActivity := range tickDevs {
			if existing, exists := merged.Ticks[tick][dev]; exists {
				existing.Commits += tickActivity.Commits
				existing.Lines += tickActivity.Lines
			} else {
				merged.Ticks[tick][dev] = &TemporalActivityTick{
					Commits: tickActivity.Commits,
					Lines:   tickActivity.Lines,
					Weekday: tickActivity.Weekday,
					Hour:    tickActivity.Hour,
					Month:   tickActivity.Month,
					Week:    tickActivity.Week,
				}
			}
		}
	}

	return merged
}
