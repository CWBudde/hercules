package leaves

import (
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
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
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
func (ta *TemporalActivityAnalysis) Configure(facts map[string]any) error {
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
func (*TemporalActivityAnalysis) ConfigureUpstream(facts map[string]any) error {
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
func (ta *TemporalActivityAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if !ta.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	reader := factReader{facts: deps}
	commit := readFact[*object.Commit](&reader, core.DependencyCommit)
	author := readFact[int](&reader, identity.DependencyAuthor)
	tick := readFact[int](&reader, items.DependencyTick)
	lineStats := readFact[map[object.ChangeEntry]items.LineStats](&reader, items.DependencyLineStats)

	if reader.err != nil {
		return nil, reader.err
	}

	weekday, hour, month, weekIndex := temporalIndices(commit.Author.When)
	activity := ta.activityFor(author)

	// Calculate line changes
	totalLines := 0
	for _, stats := range lineStats {
		totalLines += stats.Added + stats.Removed
	}

	incrementTemporalDimension(&activity.Weekdays, weekday, totalLines)
	incrementTemporalDimension(&activity.Hours, hour, totalLines)
	incrementTemporalDimension(&activity.Months, month, totalLines)
	incrementTemporalDimension(&activity.Weeks, weekIndex, totalLines)

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

	return noDependencies(), nil
}

func temporalIndices(commitTime time.Time) (int, int, int, int) {
	weekday := int(commitTime.Weekday())
	hour := commitTime.Hour()
	month := int(commitTime.Month()) - 1
	_, isoWeek := commitTime.ISOWeek()
	week := min(max(isoWeek-1, 0), 52)

	return weekday, hour, month, week
}

func incrementTemporalDimension(dimension *TemporalDimension, index, lines int) {
	dimension.Commits[index]++
	dimension.Lines[index] += lines
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (ta *TemporalActivityAnalysis) Finalize() any {
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
func (ta *TemporalActivityAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	temporalResult, err := requiredResult[TemporalActivityResult](result)
	if err != nil {
		return err
	}

	if binary {
		return ta.serializeBinary(&temporalResult, writer)
	}

	ta.serializeText(&temporalResult, writer)

	return nil
}

// Deserialize loads the result from Protocol Buffers blob.
func (ta *TemporalActivityAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.TemporalActivityResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal temporal activity result: %w", err)
	}

	activities := map[int]*DeveloperTemporalActivity{}

	for devID, pbActivity := range message.GetActivities() {
		// Handle AuthorMissing special case
		dev := int(devID)
		if devID == -1 {
			dev = core.AuthorMissing
		}

		activity := &DeveloperTemporalActivity{
			Weekdays: dimensionFromProto(pbActivity.GetWeekdays(), 7),
			Hours:    dimensionFromProto(pbActivity.GetHours(), 24),
			Months:   dimensionFromProto(pbActivity.GetMonths(), 12),
			Weeks:    dimensionFromProto(pbActivity.GetWeeks(), 53),
		}

		activities[dev] = activity
	}

	// Deserialize ticks
	ticks := map[int]map[int]*TemporalActivityTick{}

	for tickID, pbTickDevs := range message.GetTicks() {
		tickDevs := map[int]*TemporalActivityTick{}

		for devID, pbTick := range pbTickDevs.GetDevs() {
			dev := int(devID)
			if devID == -1 {
				dev = core.AuthorMissing
			}

			tickDevs[dev] = &TemporalActivityTick{
				Commits: int(pbTick.GetCommits()),
				Lines:   int(pbTick.GetLines()),
				Weekday: int(pbTick.GetWeekday()),
				Hour:    int(pbTick.GetHour()),
				Month:   int(pbTick.GetMonth()),
				Week:    int(pbTick.GetWeek()),
			}
		}

		ticks[int(tickID)] = tickDevs
	}

	result := TemporalActivityResult{
		Activities:         activities,
		Ticks:              ticks,
		reversedPeopleDict: message.GetDevIndex(),
		tickSize:           time.Duration(message.GetTickSize()),
	}

	return result, nil
}

func dimensionFromProto(source *pb.TemporalDimension, size int) TemporalDimension {
	dimension := newTemporalDimension(size)
	if source == nil {
		return dimension
	}

	for i, count := range source.GetCommits()[:min(size, len(source.GetCommits()))] {
		dimension.Commits[i] = int(count)
	}

	for i, count := range source.GetLines()[:min(size, len(source.GetLines()))] {
		dimension.Lines[i] = int(count)
	}

	return dimension
}

func writeTemporalDimension(writer io.Writer, name string, dimension TemporalDimension) {
	writeTemporalValues(writer, name+"_commits", dimension.Commits)
	writeTemporalValues(writer, name+"_lines", dimension.Lines)
}

func writeTemporalValues(writer io.Writer, name string, values []int) {
	_, _ = fmt.Fprintf(writer, "        %s: [", name)

	for i, value := range values {
		if i > 0 {
			_, _ = fmt.Fprint(writer, ", ")
		}

		_, _ = fmt.Fprintf(writer, "%d", value)
	}

	_, _ = fmt.Fprintln(writer, "]")
}

func protobufAuthorID(author int) int32 {
	if author == core.AuthorMissing {
		return -1
	}

	authorID, err := intToProtoInt32(author, "author ID")
	if err != nil {
		panic(err)
	}

	return authorID
}

func temporalActivityToProto(activity *DeveloperTemporalActivity) (*pb.DeveloperTemporalActivity, error) {
	weekdays, err := temporalDimensionToProto(activity.Weekdays)
	if err != nil {
		return nil, err
	}

	hours, err := temporalDimensionToProto(activity.Hours)
	if err != nil {
		return nil, err
	}

	months, err := temporalDimensionToProto(activity.Months)
	if err != nil {
		return nil, err
	}

	weeks, err := temporalDimensionToProto(activity.Weeks)
	if err != nil {
		return nil, err
	}

	return &pb.DeveloperTemporalActivity{
		Weekdays: weekdays, Hours: hours, Months: months, Weeks: weeks,
	}, nil
}

func temporalDimensionToProto(dimension TemporalDimension) (*pb.TemporalDimension, error) {
	result := &pb.TemporalDimension{
		Commits: make([]int32, len(dimension.Commits)),
		Lines:   make([]int32, len(dimension.Lines)),
	}
	for index, count := range dimension.Commits {
		pbCount, err := intToProtoInt32(count, "temporal commit count")
		if err != nil {
			return nil, err
		}

		result.Commits[index] = pbCount
	}

	for index, count := range dimension.Lines {
		pbCount, err := intToProtoInt32(count, "temporal line count")
		if err != nil {
			return nil, err
		}

		result.Lines[index] = pbCount
	}

	return result, nil
}

func temporalTicksToProto(
	ticks map[int]map[int]*TemporalActivityTick,
) (map[int32]*pb.TemporalActivityTickDevs, error) {
	result := make(map[int32]*pb.TemporalActivityTickDevs, len(ticks))
	for tick, developers := range ticks {
		protoDevelopers, err := temporalDevelopersToProto(developers)
		if err != nil {
			return nil, err
		}

		tickID, err := intToProtoInt32(tick, "temporal tick")
		if err != nil {
			return nil, err
		}

		result[tickID] = &pb.TemporalActivityTickDevs{Devs: protoDevelopers}
	}

	return result, nil
}

func temporalDevelopersToProto(
	developers map[int]*TemporalActivityTick,
) (map[int32]*pb.TemporalActivityTick, error) {
	result := make(map[int32]*pb.TemporalActivityTick, len(developers))
	for developer, activity := range developers {
		developerID, err := intToProtoInt32(developer, "temporal author")
		if err != nil {
			return nil, err
		}

		if developer == core.AuthorMissing {
			developerID = -1
		}

		protoActivity, err := temporalTickToProto(activity)
		if err != nil {
			return nil, err
		}

		result[developerID] = protoActivity
	}

	return result, nil
}

func temporalTickToProto(activity *TemporalActivityTick) (*pb.TemporalActivityTick, error) {
	commits, err := intToProtoInt32(activity.Commits, "temporal tick commit count")
	if err != nil {
		return nil, err
	}

	lines, err := intToProtoInt32(activity.Lines, "temporal tick line count")
	if err != nil {
		return nil, err
	}

	weekday, err := intToProtoInt32(activity.Weekday, "temporal weekday")
	if err != nil {
		return nil, err
	}

	hour, err := intToProtoInt32(activity.Hour, "temporal hour")
	if err != nil {
		return nil, err
	}

	month, err := intToProtoInt32(activity.Month, "temporal month")
	if err != nil {
		return nil, err
	}

	week, err := intToProtoInt32(activity.Week, "temporal ISO week")
	if err != nil {
		return nil, err
	}

	return &pb.TemporalActivityTick{
		Commits: commits, Lines: lines,
		Weekday: weekday, Hour: hour, Month: month, Week: week,
	}, nil
}

var _ = core.RegisterPipelineItem(&TemporalActivityAnalysis{})

// MergeResults combines two TemporalActivityResult-s together.
func (ta *TemporalActivityAnalysis) MergeResults(
	firstResult, secondResult any, _, _ *core.CommonAnalysisResult,
) any {
	tar1, err := requiredResult[TemporalActivityResult](firstResult)
	if err != nil {
		return err
	}

	tar2, err := requiredResult[TemporalActivityResult](secondResult)
	if err != nil {
		return err
	}

	merged := TemporalActivityResult{
		Activities:         make(map[int]*DeveloperTemporalActivity),
		Ticks:              make(map[int]map[int]*TemporalActivityTick),
		reversedPeopleDict: tar1.reversedPeopleDict, // Use first dict, should be same
		tickSize:           tar1.tickSize,
	}

	mergeTemporalActivities(merged.Activities, tar1.Activities)
	mergeTemporalActivities(merged.Activities, tar2.Activities)
	mergeTemporalTicks(merged.Ticks, tar1.Ticks)
	mergeTemporalTicks(merged.Ticks, tar2.Ticks)

	return merged
}

func (ta *TemporalActivityAnalysis) activityFor(author int) *DeveloperTemporalActivity {
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

	return activity
}

func (ta *TemporalActivityAnalysis) serializeText(result *TemporalActivityResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  temporal_activity:")
	_, _ = fmt.Fprintln(writer, "    activities:")

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

		_, _ = fmt.Fprintf(writer, "      %d:\n", devID)

		writeTemporalDimension(writer, "weekdays", activity.Weekdays)
		writeTemporalDimension(writer, "hours", activity.Hours)
		writeTemporalDimension(writer, "months", activity.Months)
		writeTemporalDimension(writer, "weeks", activity.Weeks)
	}

	// Output people dictionary
	_, _ = fmt.Fprintln(writer, "    people:")

	for _, person := range result.reversedPeopleDict {
		_, _ = fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}
}

func (ta *TemporalActivityAnalysis) serializeBinary(result *TemporalActivityResult, writer io.Writer) error {
	message := pb.TemporalActivityResults{}
	message.DevIndex = result.reversedPeopleDict
	message.Activities = make(map[int32]*pb.DeveloperTemporalActivity)

	for dev, activity := range result.Activities {
		developerID, err := intToProtoInt32(dev, "temporal author")
		if err != nil {
			return err
		}

		if dev == core.AuthorMissing {
			developerID = -1
		}

		pbActivity, err := temporalActivityToProto(activity)
		if err != nil {
			return err
		}

		message.Activities[developerID] = pbActivity
	}

	var err error

	message.Ticks, err = temporalTicksToProto(result.Ticks)
	if err != nil {
		return err
	}

	message.TickSize = int64(result.tickSize)

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal temporal activity result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write temporal activity result: %w", err)
	}

	return nil
}

func mergeTemporalActivities(
	target, source map[int]*DeveloperTemporalActivity,
) {
	for developer, sourceActivity := range source {
		activity := target[developer]
		if activity == nil {
			activity = &DeveloperTemporalActivity{
				Weekdays: newTemporalDimension(7), Hours: newTemporalDimension(24),
				Months: newTemporalDimension(12), Weeks: newTemporalDimension(53),
			}
			target[developer] = activity
		}

		addTemporalDimension(&activity.Weekdays, sourceActivity.Weekdays)
		addTemporalDimension(&activity.Hours, sourceActivity.Hours)
		addTemporalDimension(&activity.Months, sourceActivity.Months)
		addTemporalDimension(&activity.Weeks, sourceActivity.Weeks)
	}
}

func addTemporalDimension(target *TemporalDimension, source TemporalDimension) {
	for i := range target.Commits {
		target.Commits[i] += source.Commits[i]
		target.Lines[i] += source.Lines[i]
	}
}

func mergeTemporalTicks(
	target, source map[int]map[int]*TemporalActivityTick,
) {
	for tick, developers := range source {
		if target[tick] == nil {
			target[tick] = make(map[int]*TemporalActivityTick)
		}

		for developer, activity := range developers {
			existing := target[tick][developer]
			if existing == nil {
				activityCopy := *activity
				target[tick][developer] = &activityCopy
			} else {
				existing.Commits += activity.Commits
				existing.Lines += activity.Lines
			}
		}
	}
}
