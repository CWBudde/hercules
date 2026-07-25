package leaves

import (
	"fmt"
	"io"
	"maps"
	"sort"
	"strconv"
	"strings"
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

// onboardingTickMetrics tracks activity for one author at one tick.
type onboardingTickMetrics struct {
	Commits      int
	Files        map[string]bool // set of unique files
	LinesAdded   int
	LinesRemoved int
	LinesChanged int

	// Filtered metrics (meaningful commits only)
	MeaningfulCommits      int
	MeaningfulFiles        map[string]bool
	MeaningfulLinesAdded   int
	MeaningfulLinesRemoved int
	MeaningfulLinesChanged int
}

// OnboardingSnapshot captures metrics at a specific milestone.
type OnboardingSnapshot struct {
	DaysSinceJoin int

	// All commits
	TotalCommits int
	TotalFiles   int
	TotalLines   int

	// Meaningful commits only
	MeaningfulCommits int
	MeaningfulFiles   int
	MeaningfulLines   int
}

// AuthorOnboardingData contains onboarding progression for one author.
type AuthorOnboardingData struct {
	FirstCommitTick int
	JoinCohort      string // "YYYY-MM"

	// Indexed by window days (e.g., 7, 30, 90)
	Snapshots map[int]*OnboardingSnapshot
}

// CohortStats contains aggregated statistics for a cohort.
type CohortStats struct {
	Cohort      string // "YYYY-MM"
	AuthorCount int

	// Averaged across all authors in cohort
	AverageSnapshots map[int]*OnboardingSnapshot
}

// OnboardingResult is returned by OnboardingAnalysis.Finalize().
type OnboardingResult struct {
	Authors             map[int]*AuthorOnboardingData
	Cohorts             map[string]*CohortStats
	WindowDays          []int
	MeaningfulThreshold int
	reversedPeopleDict  []string
	tickSize            time.Duration
}

// OnboardingAnalysis measures how quickly new contributors ramp up.
type OnboardingAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// WindowDays specifies milestone days for snapshots (e.g., [7, 30, 90])
	WindowDays []int
	// MeaningfulThreshold is minimum lines for "meaningful" commit
	MeaningfulThreshold int

	// author -> tick -> metrics
	authorTimeline     map[int]map[int]*onboardingTickMetrics
	reversedPeopleDict []string
	tickSize           time.Duration

	l core.Logger
}

const (
	// ConfigOnboardingWindows is the name of the option to set OnboardingAnalysis.WindowDays.
	ConfigOnboardingWindows = "Onboarding.Windows"
	// ConfigOnboardingMeaningfulThreshold is the name of the option to set OnboardingAnalysis.MeaningfulThreshold.
	ConfigOnboardingMeaningfulThreshold = "Onboarding.MeaningfulThreshold"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (oa *OnboardingAnalysis) Name() string {
	return "Onboarding"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (oa *OnboardingAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (oa *OnboardingAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor,
		items.DependencyTick,
		items.DependencyLineStats,
		items.DependencyTreeChanges,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (oa *OnboardingAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigOnboardingWindows,
			Description: "Comma-separated list of days for snapshot milestones (e.g., '7,30,90').",
			Flag:        "onboarding-windows",
			Type:        core.StringConfigurationOption,
			Default:     "7,30,90",
		},
		{
			Name:        ConfigOnboardingMeaningfulThreshold,
			Description: "Minimum lines changed for a commit to count as 'meaningful'.",
			Flag:        "onboarding-meaningful-threshold",
			Type:        core.IntConfigurationOption,
			Default:     10,
		},
	}

	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (oa *OnboardingAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		oa.l = l
	}

	if val, exists := facts[ConfigOnboardingWindows].(string); exists {
		// Parse comma-separated window days
		parts := strings.Split(val, ",")

		oa.WindowDays = make([]int, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			days, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("invalid window days value '%s': %w", part, err)
			}

			oa.WindowDays = append(oa.WindowDays, days)
		}

		sort.Ints(oa.WindowDays)
	}

	if val, exists := facts[ConfigOnboardingMeaningfulThreshold].(int); exists {
		oa.MeaningfulThreshold = val
	}

	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		oa.reversedPeopleDict = val
	}

	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		oa.tickSize = val
	}

	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*OnboardingAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (oa *OnboardingAnalysis) Flag() string {
	return "onboarding"
}

// Description returns the text which explains what the analysis is doing.
func (oa *OnboardingAnalysis) Description() string {
	return "Measures how quickly new contributors ramp up: time-to-first-change, " +
		"breadth-of-files in first N days, convergence to stable contribution patterns."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume() calls.
func (oa *OnboardingAnalysis) Initialize(repository *git.Repository) error {
	oa.l = core.NewLogger()
	oa.authorTimeline = map[int]map[int]*onboardingTickMetrics{}
	oa.OneShotMergeProcessor.Initialize()

	// Set defaults if not configured
	if len(oa.WindowDays) == 0 {
		oa.WindowDays = []int{7, 30, 90}
	}

	if oa.MeaningfulThreshold == 0 {
		oa.MeaningfulThreshold = 10
	}

	return nil
}

// Consume runs this PipelineItem on the next commit data.
func (oa *OnboardingAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if !oa.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	reader := factReader{facts: deps}
	author := readFact[int](&reader, identity.DependencyAuthor)
	tick := readFact[int](&reader, items.DependencyTick)
	treeChanges := readFact[object.Changes](&reader, items.DependencyTreeChanges)
	lineStats := readFact[map[object.ChangeEntry]items.LineStats](&reader, items.DependencyLineStats)

	if reader.err != nil {
		return nil, reader.err
	}

	if len(treeChanges) == 0 {
		return noDependencies(), nil
	}

	metrics := oa.getOrCreateTickMetrics(author, tick)

	// Track files and accumulate line stats
	commitTotalLines := 0

	for changeEntry, stats := range lineStats {
		fileName := changeEntry.Name
		metrics.Files[fileName] = true

		linesChanged := stats.Added + stats.Removed + stats.Changed
		metrics.LinesAdded += stats.Added
		metrics.LinesRemoved += stats.Removed
		metrics.LinesChanged += stats.Changed
		commitTotalLines += linesChanged

		// Track meaningful metrics if threshold exceeded
		if linesChanged >= oa.MeaningfulThreshold {
			metrics.MeaningfulFiles[fileName] = true
			metrics.MeaningfulLinesAdded += stats.Added
			metrics.MeaningfulLinesRemoved += stats.Removed
			metrics.MeaningfulLinesChanged += stats.Changed
		}
	}

	// Increment commit counters
	metrics.Commits++
	if commitTotalLines >= oa.MeaningfulThreshold {
		metrics.MeaningfulCommits++
	}

	return noDependencies(), nil
}

// cumulativeMetrics represents running totals across an author's timeline.

type cumulativeMetrics struct {
	commits           int
	files             map[string]bool
	lines             int
	meaningfulCommits int
	meaningfulFiles   map[string]bool
	meaningfulLines   int
}

// newCumulativeMetrics creates an empty cumulative metrics tracker.
func newCumulativeMetrics() *cumulativeMetrics {
	return &cumulativeMetrics{
		files:           map[string]bool{},
		meaningfulFiles: map[string]bool{},
	}
}

// accumulate adds tick metrics to cumulative totals.
func (cm *cumulativeMetrics) accumulate(tickMetrics *onboardingTickMetrics) {
	cm.commits += tickMetrics.Commits
	for file := range tickMetrics.Files {
		cm.files[file] = true
	}

	cm.lines += tickMetrics.LinesAdded + tickMetrics.LinesRemoved + tickMetrics.LinesChanged

	cm.meaningfulCommits += tickMetrics.MeaningfulCommits
	for file := range tickMetrics.MeaningfulFiles {
		cm.meaningfulFiles[file] = true
	}

	cm.meaningfulLines += tickMetrics.MeaningfulLinesAdded +
		tickMetrics.MeaningfulLinesRemoved + tickMetrics.MeaningfulLinesChanged
}

// findClosestTick finds the tick <= targetTick in sorted ticks array.
func findClosestTick(sortedTicks []int, targetTick int) int {
	if len(sortedTicks) == 0 {
		return -1
	}

	// Binary search for closest tick <= target
	idx := sort.Search(len(sortedTicks), func(i int) bool {
		return sortedTicks[i] > targetTick
	})

	if idx == 0 {
		// All ticks are > target, no valid tick
		return -1
	}

	return sortedTicks[idx-1]
}

// copyFileSet creates a copy of a file set.
func copyFileSet(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	maps.Copy(dst, src)

	return dst
}

// Finalize returns the result of the analysis.
func (oa *OnboardingAnalysis) Finalize() any {
	authors := make(map[int]*AuthorOnboardingData, len(oa.authorTimeline))
	cohortGroups := map[string][]int{} // cohort -> author IDs

	for authorID, timeline := range oa.authorTimeline {
		author := oa.finalizeAuthor(timeline)
		if author == nil {
			continue
		}

		authors[authorID] = author
		cohortGroups[author.JoinCohort] = append(cohortGroups[author.JoinCohort], authorID)
	}

	return oa.finalizeCohorts(authors, cohortGroups)
}

func onboardingSnapshot(days int, metrics *cumulativeMetrics) *OnboardingSnapshot {
	return &OnboardingSnapshot{
		DaysSinceJoin: days, TotalCommits: metrics.commits, TotalFiles: len(metrics.files),
		TotalLines: metrics.lines, MeaningfulCommits: metrics.meaningfulCommits,
		MeaningfulFiles: len(metrics.meaningfulFiles), MeaningfulLines: metrics.meaningfulLines,
	}
}

func averageOnboardingSnapshots(
	authors map[int]*AuthorOnboardingData, authorIDs []int,
) map[int]*OnboardingSnapshot {
	sums := map[int]*OnboardingSnapshot{}

	for _, authorID := range authorIDs {
		for days, snapshot := range authors[authorID].Snapshots {
			if sums[days] == nil {
				sums[days] = &OnboardingSnapshot{DaysSinceJoin: days}
			}

			addOnboardingSnapshot(sums[days], snapshot)
		}
	}

	for _, snapshot := range sums {
		divideOnboardingSnapshot(snapshot, len(authorIDs))
	}

	return sums
}

func addOnboardingSnapshot(target, source *OnboardingSnapshot) {
	target.TotalCommits += source.TotalCommits
	target.TotalFiles += source.TotalFiles
	target.TotalLines += source.TotalLines
	target.MeaningfulCommits += source.MeaningfulCommits
	target.MeaningfulFiles += source.MeaningfulFiles
	target.MeaningfulLines += source.MeaningfulLines
}

func divideOnboardingSnapshot(snapshot *OnboardingSnapshot, count int) {
	snapshot.TotalCommits /= count
	snapshot.TotalFiles /= count
	snapshot.TotalLines /= count
	snapshot.MeaningfulCommits /= count
	snapshot.MeaningfulFiles /= count
	snapshot.MeaningfulLines /= count
}

// Fork clones this pipeline item.
func (oa *OnboardingAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(oa, n)
}

func writeOnboardingAuthors(writer io.Writer, authors map[int]*AuthorOnboardingData) {
	authorIDs := make([]int, 0, len(authors))
	for id := range authors {
		authorIDs = append(authorIDs, id)
	}

	sort.Ints(authorIDs)
	fmt.Fprintln(writer, "    authors:")

	for _, authorID := range authorIDs {
		author := authors[authorID]
		fmt.Fprintf(writer, "      %d:\n", int(protobufAuthorID(authorID)))
		fmt.Fprintf(writer, "        first_commit_tick: %d\n", author.FirstCommitTick)
		fmt.Fprintf(writer, "        join_cohort: %s\n", yaml.SafeString(author.JoinCohort))
		fmt.Fprintln(writer, "        snapshots:")
		writeOnboardingSnapshots(writer, author.Snapshots)
	}
}

func writeOnboardingCohorts(writer io.Writer, cohorts map[string]*CohortStats) {
	cohortNames := make([]string, 0, len(cohorts))
	for name := range cohorts {
		cohortNames = append(cohortNames, name)
	}

	sort.Strings(cohortNames)
	fmt.Fprintln(writer, "    cohorts:")

	for _, name := range cohortNames {
		cohort := cohorts[name]
		fmt.Fprintf(writer, "      %s:\n", yaml.SafeString(name))
		fmt.Fprintf(writer, "        author_count: %d\n", cohort.AuthorCount)
		fmt.Fprintln(writer, "        average_snapshots:")
		writeOnboardingSnapshots(writer, cohort.AverageSnapshots)
	}
}

func writeOnboardingSnapshots(writer io.Writer, snapshots map[int]*OnboardingSnapshot) {
	windowDays := make([]int, 0, len(snapshots))
	for days := range snapshots {
		windowDays = append(windowDays, days)
	}

	sort.Ints(windowDays)

	for _, days := range windowDays {
		snapshot := snapshots[days]
		fmt.Fprintf(
			writer,
			"          %d: {days: %d, commits: %d, files: %d, lines: %d, "+
				"meaningful_commits: %d, meaningful_files: %d, meaningful_lines: %d}\n",
			days,
			snapshot.DaysSinceJoin,
			snapshot.TotalCommits,
			snapshot.TotalFiles,
			snapshot.TotalLines,
			snapshot.MeaningfulCommits,
			snapshot.MeaningfulFiles,
			snapshot.MeaningfulLines,
		)
	}
}

func onboardingAuthorsToProto(
	authors map[int]*AuthorOnboardingData,
) (map[int32]*pb.AuthorOnboardingData, error) {
	result := make(map[int32]*pb.AuthorOnboardingData, len(authors))
	for authorID, author := range authors {
		firstCommitTick, err := intToProtoInt32(author.FirstCommitTick, "onboarding first commit tick")
		if err != nil {
			return nil, err
		}

		pbAuthor := &pb.AuthorOnboardingData{
			FirstCommitTick: firstCommitTick,
			JoinCohort:      author.JoinCohort,
			Snapshots:       make(map[int32]*pb.OnboardingSnapshot, len(author.Snapshots)),
		}
		for days, snap := range author.Snapshots {
			pbDays, err := intToProtoInt32(days, "onboarding snapshot day")
			if err != nil {
				return nil, err
			}

			pbSnapshot, err := onboardingSnapshotToProto(snap)
			if err != nil {
				return nil, err
			}

			pbAuthor.Snapshots[pbDays] = pbSnapshot
		}

		pbAuthorID, err := intToProtoInt32(authorID, "onboarding author")
		if err != nil {
			return nil, err
		}

		if authorID == core.AuthorMissing {
			pbAuthorID = -1
		}

		result[pbAuthorID] = pbAuthor
	}

	return result, nil
}

func onboardingSnapshotToProto(snapshot *OnboardingSnapshot) (*pb.OnboardingSnapshot, error) {
	values := []int{
		snapshot.DaysSinceJoin, snapshot.TotalCommits, snapshot.TotalFiles, snapshot.TotalLines,
		snapshot.MeaningfulCommits, snapshot.MeaningfulFiles, snapshot.MeaningfulLines,
	}

	converted := make([]int32, len(values))
	for index, value := range values {
		pbValue, err := intToProtoInt32(value, "onboarding snapshot value")
		if err != nil {
			return nil, err
		}

		converted[index] = pbValue
	}

	return &pb.OnboardingSnapshot{
		DaysSinceJoin: converted[0], TotalCommits: converted[1],
		TotalFiles: converted[2], TotalLines: converted[3],
		MeaningfulCommits: converted[4], MeaningfulFiles: converted[5], MeaningfulLines: converted[6],
	}, nil
}

func onboardingCohortsToProto(cohorts map[string]*CohortStats) (map[string]*pb.CohortStats, error) {
	result := make(map[string]*pb.CohortStats, len(cohorts))
	for cohortName, cohort := range cohorts {
		authorCount, err := intToProtoInt32(cohort.AuthorCount, "onboarding cohort author count")
		if err != nil {
			return nil, err
		}

		pbCohort := &pb.CohortStats{
			Cohort:           cohort.Cohort,
			AuthorCount:      authorCount,
			AverageSnapshots: make(map[int32]*pb.OnboardingAverageSnapshot, len(cohort.AverageSnapshots)),
		}
		for days, snap := range cohort.AverageSnapshots {
			pbDays, err := intToProtoInt32(days, "onboarding cohort snapshot day")
			if err != nil {
				return nil, err
			}

			pbSnapshot, err := onboardingAverageToProto(snap)
			if err != nil {
				return nil, err
			}

			pbCohort.AverageSnapshots[pbDays] = pbSnapshot
		}

		result[cohortName] = pbCohort
	}

	return result, nil
}

func onboardingAverageToProto(snapshot *OnboardingSnapshot) (*pb.OnboardingAverageSnapshot, error) {
	daysSinceJoin, err := intToProtoInt32(snapshot.DaysSinceJoin, "onboarding average days since join")
	if err != nil {
		return nil, err
	}

	return &pb.OnboardingAverageSnapshot{
		DaysSinceJoin: daysSinceJoin, AvgTotalCommits: float64(snapshot.TotalCommits),
		AvgTotalFiles: float64(snapshot.TotalFiles), AvgTotalLines: float64(snapshot.TotalLines),
		AvgMeaningfulCommits: float64(snapshot.MeaningfulCommits),
		AvgMeaningfulFiles:   float64(snapshot.MeaningfulFiles),
		AvgMeaningfulLines:   float64(snapshot.MeaningfulLines),
	}, nil
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (oa *OnboardingAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	onboardingResult, err := requiredResult[OnboardingResult](result)
	if err != nil {
		return err
	}

	if binary {
		return oa.serializeBinary(&onboardingResult, writer)
	}

	oa.serializeText(&onboardingResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to OnboardingResult.
func (oa *OnboardingAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.OnboardingResults{}

	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal onboarding result: %w", err)
	}

	result := OnboardingResult{
		Authors:             make(map[int]*AuthorOnboardingData, len(message.GetAuthors())),
		Cohorts:             make(map[string]*CohortStats, len(message.GetCohorts())),
		WindowDays:          make([]int, len(message.GetWindowDays())),
		MeaningfulThreshold: int(message.GetMeaningfulThreshold()),
		reversedPeopleDict:  message.GetDevIndex(),
		tickSize:            time.Duration(message.GetTickSize()),
	}

	for i, days := range message.GetWindowDays() {
		result.WindowDays[i] = int(days)
	}

	result.Authors = onboardingAuthorsFromProto(message.GetAuthors())
	result.Cohorts = onboardingCohortsFromProto(message.GetCohorts())

	return result, nil
}

// getOrCreateTickMetrics retrieves or creates tick metrics for an author.
func (oa *OnboardingAnalysis) getOrCreateTickMetrics(author, tick int) *onboardingTickMetrics {
	timeline, exists := oa.authorTimeline[author]
	if !exists {
		timeline = map[int]*onboardingTickMetrics{}
		oa.authorTimeline[author] = timeline
	}

	metrics, exists := timeline[tick]
	if !exists {
		metrics = &onboardingTickMetrics{
			Files:           map[string]bool{},
			MeaningfulFiles: map[string]bool{},
		}
		timeline[tick] = metrics
	}

	return metrics
}

func (oa *OnboardingAnalysis) finalizeAuthor(
	timeline map[int]*onboardingTickMetrics,
) *AuthorOnboardingData {
	sortedTicks := make([]int, 0, len(timeline))
	for tick := range timeline {
		sortedTicks = append(sortedTicks, tick)
	}

	sort.Ints(sortedTicks)

	if len(sortedTicks) == 0 {
		return nil
	}

	firstTick := sortedTicks[0]
	firstTimestamp := items.FloorTime(
		time.Unix(0, 0).Add(time.Duration(firstTick)*oa.tickSize), oa.tickSize,
	)

	return &AuthorOnboardingData{
		FirstCommitTick: firstTick,
		JoinCohort:      firstTimestamp.Format("2006-01"),
		Snapshots:       oa.buildOnboardingSnapshots(timeline, sortedTicks),
	}
}

func (oa *OnboardingAnalysis) buildOnboardingSnapshots(
	timeline map[int]*onboardingTickMetrics, sortedTicks []int,
) map[int]*OnboardingSnapshot {
	cumulative := newCumulativeMetrics()

	tickToMetrics := make(map[int]*cumulativeMetrics, len(sortedTicks))
	for _, tick := range sortedTicks {
		cumulative.accumulate(timeline[tick])
		metricsCopy := *cumulative
		metricsCopy.files = copyFileSet(cumulative.files)
		metricsCopy.meaningfulFiles = copyFileSet(cumulative.meaningfulFiles)
		tickToMetrics[tick] = &metricsCopy
	}

	snapshots := map[int]*OnboardingSnapshot{}

	ticksPerDay := int(24 * time.Hour / oa.tickSize)
	for _, windowDays := range oa.WindowDays {
		closestTick := findClosestTick(sortedTicks, sortedTicks[0]+windowDays*ticksPerDay)
		if closestTick >= 0 {
			snapshots[windowDays] = onboardingSnapshot(windowDays, tickToMetrics[closestTick])
		}
	}

	return snapshots
}

// finalizeCohorts computes cohort aggregates and returns final result.
func (oa *OnboardingAnalysis) finalizeCohorts(
	authors map[int]*AuthorOnboardingData,
	cohortGroups map[string][]int,
) OnboardingResult {
	cohorts := make(map[string]*CohortStats, len(cohortGroups))

	for cohort, authorIDs := range cohortGroups {
		if len(authorIDs) == 0 {
			continue
		}

		authorCount := len(authorIDs)
		cohorts[cohort] = &CohortStats{
			Cohort:           cohort,
			AuthorCount:      authorCount,
			AverageSnapshots: averageOnboardingSnapshots(authors, authorIDs),
		}
	}

	return OnboardingResult{
		Authors:             authors,
		Cohorts:             cohorts,
		WindowDays:          oa.WindowDays,
		MeaningfulThreshold: oa.MeaningfulThreshold,
		reversedPeopleDict:  oa.reversedPeopleDict,
		tickSize:            oa.tickSize,
	}
}

// serializeText outputs YAML format.
func (oa *OnboardingAnalysis) serializeText(result *OnboardingResult, writer io.Writer) {
	fmt.Fprintln(writer, "  onboarding:")

	// Configuration
	windowStrs := make([]string, len(result.WindowDays))
	for i, days := range result.WindowDays {
		windowStrs[i] = strconv.Itoa(days)
	}

	fmt.Fprintf(writer, "    window_days: [%s]\n", strings.Join(windowStrs, ", "))
	fmt.Fprintf(writer, "    meaningful_threshold: %d\n", result.MeaningfulThreshold)
	writeOnboardingAuthors(writer, result.Authors)
	writeOnboardingCohorts(writer, result.Cohorts)

	fmt.Fprintln(writer, "    people:")

	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}

	fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

// serializeBinary outputs Protocol Buffers format.
func (oa *OnboardingAnalysis) serializeBinary(result *OnboardingResult, writer io.Writer) error {
	meaningfulThreshold, err := intToProtoInt32(result.MeaningfulThreshold, "onboarding meaningful threshold")
	if err != nil {
		return err
	}

	message := pb.OnboardingResults{
		DevIndex:            result.reversedPeopleDict,
		TickSize:            int64(result.tickSize),
		WindowDays:          make([]int32, len(result.WindowDays)),
		MeaningfulThreshold: meaningfulThreshold,
	}

	for index, days := range result.WindowDays {
		pbDays, err := intToProtoInt32(days, "onboarding window days")
		if err != nil {
			return err
		}

		message.WindowDays[index] = pbDays
	}

	message.Authors, err = onboardingAuthorsToProto(result.Authors)
	if err != nil {
		return err
	}

	message.Cohorts, err = onboardingCohortsToProto(result.Cohorts)
	if err != nil {
		return err
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal onboarding result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write onboarding result: %w", err)
	}

	return nil
}

func onboardingAuthorsFromProto(
	authors map[int32]*pb.AuthorOnboardingData,
) map[int]*AuthorOnboardingData {
	result := make(map[int]*AuthorOnboardingData, len(authors))
	for authorID, pbAuthor := range authors {
		author := &AuthorOnboardingData{
			FirstCommitTick: int(pbAuthor.GetFirstCommitTick()),
			JoinCohort:      pbAuthor.GetJoinCohort(),
			Snapshots:       make(map[int]*OnboardingSnapshot, len(pbAuthor.GetSnapshots())),
		}

		for days, pbSnap := range pbAuthor.GetSnapshots() {
			author.Snapshots[int(days)] = &OnboardingSnapshot{
				DaysSinceJoin:     int(pbSnap.GetDaysSinceJoin()),
				TotalCommits:      int(pbSnap.GetTotalCommits()),
				TotalFiles:        int(pbSnap.GetTotalFiles()),
				TotalLines:        int(pbSnap.GetTotalLines()),
				MeaningfulCommits: int(pbSnap.GetMeaningfulCommits()),
				MeaningfulFiles:   int(pbSnap.GetMeaningfulFiles()),
				MeaningfulLines:   int(pbSnap.GetMeaningfulLines()),
			}
		}

		result[int(authorID)] = author
	}

	return result
}

func onboardingCohortsFromProto(cohorts map[string]*pb.CohortStats) map[string]*CohortStats {
	result := make(map[string]*CohortStats, len(cohorts))
	for cohortName, pbCohort := range cohorts {
		cohort := &CohortStats{
			Cohort:           pbCohort.GetCohort(),
			AuthorCount:      int(pbCohort.GetAuthorCount()),
			AverageSnapshots: make(map[int]*OnboardingSnapshot, len(pbCohort.GetAverageSnapshots())),
		}

		for days, pbSnap := range pbCohort.GetAverageSnapshots() {
			cohort.AverageSnapshots[int(days)] = &OnboardingSnapshot{
				DaysSinceJoin:     int(pbSnap.GetDaysSinceJoin()),
				TotalCommits:      int(pbSnap.GetAvgTotalCommits()),
				TotalFiles:        int(pbSnap.GetAvgTotalFiles()),
				TotalLines:        int(pbSnap.GetAvgTotalLines()),
				MeaningfulCommits: int(pbSnap.GetAvgMeaningfulCommits()),
				MeaningfulFiles:   int(pbSnap.GetAvgMeaningfulFiles()),
				MeaningfulLines:   int(pbSnap.GetAvgMeaningfulLines()),
			}
		}

		result[cohortName] = cohort
	}

	return result
}

var _ = core.RegisterPipelineItem(&OnboardingAnalysis{})
