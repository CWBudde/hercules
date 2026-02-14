package leaves

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
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

// onboardingTickMetrics tracks activity for one author at one tick
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

// OnboardingSnapshot captures metrics at a specific milestone
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

// AuthorOnboardingData contains onboarding progression for one author
type AuthorOnboardingData struct {
	FirstCommitTick int
	JoinCohort      string // "YYYY-MM"

	// Indexed by window days (e.g., 7, 30, 90)
	Snapshots map[int]*OnboardingSnapshot
}

// CohortStats contains aggregated statistics for a cohort
type CohortStats struct {
	Cohort       string // "YYYY-MM"
	AuthorCount  int

	// Averaged across all authors in cohort
	AverageSnapshots map[int]*OnboardingSnapshot
}

// OnboardingResult is returned by OnboardingAnalysis.Finalize()
type OnboardingResult struct {
	Authors             map[int]*AuthorOnboardingData
	Cohorts             map[string]*CohortStats
	WindowDays          []int
	MeaningfulThreshold int
	reversedPeopleDict  []string
	tickSize            time.Duration
}

// OnboardingAnalysis measures how quickly new contributors ramp up
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
	// ConfigOnboardingWindows is the name of the option to set OnboardingAnalysis.WindowDays
	ConfigOnboardingWindows = "Onboarding.Windows"
	// ConfigOnboardingMeaningfulThreshold is the name of the option to set OnboardingAnalysis.MeaningfulThreshold
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
func (oa *OnboardingAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		oa.l = l
	}
	if val, exists := facts[ConfigOnboardingWindows].(string); exists {
		// Parse comma-separated window days
		parts := strings.Split(val, ",")
		oa.WindowDays = make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			days, err := strconv.Atoi(p)
			if err != nil {
				return fmt.Errorf("invalid window days value '%s': %w", p, err)
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
func (*OnboardingAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (oa *OnboardingAnalysis) Flag() string {
	return "onboarding"
}

// Description returns the text which explains what the analysis is doing.
func (oa *OnboardingAnalysis) Description() string {
	return "Measures how quickly new contributors ramp up: time-to-first-change, breadth-of-files in first N days, convergence to stable contribution patterns."
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

// getOrCreateTickMetrics retrieves or creates tick metrics for an author
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

// Consume runs this PipelineItem on the next commit data.
func (oa *OnboardingAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !oa.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	author := deps[identity.DependencyAuthor].(int)
	tick := deps[items.DependencyTick].(int)
	treeChanges := deps[items.DependencyTreeChanges].(object.Changes)
	lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)

	if len(treeChanges) == 0 {
		return nil, nil
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

	return nil, nil
}

// cumulativeMetrics represents running totals across an author's timeline
type cumulativeMetrics struct {
	commits           int
	files             map[string]bool
	lines             int
	meaningfulCommits int
	meaningfulFiles   map[string]bool
	meaningfulLines   int
}

// newCumulativeMetrics creates an empty cumulative metrics tracker
func newCumulativeMetrics() *cumulativeMetrics {
	return &cumulativeMetrics{
		files:           map[string]bool{},
		meaningfulFiles: map[string]bool{},
	}
}

// accumulate adds tick metrics to cumulative totals
func (cm *cumulativeMetrics) accumulate(tm *onboardingTickMetrics) {
	cm.commits += tm.Commits
	for file := range tm.Files {
		cm.files[file] = true
	}
	cm.lines += tm.LinesAdded + tm.LinesRemoved + tm.LinesChanged
	cm.meaningfulCommits += tm.MeaningfulCommits
	for file := range tm.MeaningfulFiles {
		cm.meaningfulFiles[file] = true
	}
	cm.meaningfulLines += tm.MeaningfulLinesAdded + tm.MeaningfulLinesRemoved + tm.MeaningfulLinesChanged
}

// findClosestTick finds the tick <= targetTick in sorted ticks array
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

// copyFileSet creates a copy of a file set
func copyFileSet(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Finalize returns the result of the analysis.
func (oa *OnboardingAnalysis) Finalize() interface{} {
	authors := make(map[int]*AuthorOnboardingData, len(oa.authorTimeline))
	cohortGroups := map[string][]int{} // cohort -> author IDs

	// Process each author
	for authorID, timeline := range oa.authorTimeline {
		// Find first commit tick
		firstTick := -1
		sortedTicks := make([]int, 0, len(timeline))
		for tick := range timeline {
			sortedTicks = append(sortedTicks, tick)
			if firstTick == -1 || tick < firstTick {
				firstTick = tick
			}
		}
		sort.Ints(sortedTicks)

		if firstTick == -1 {
			continue // No commits for this author
		}

		// Determine join cohort (YYYY-MM)
		firstTimestamp := items.FloorTime(time.Unix(0, 0).Add(time.Duration(firstTick)*oa.tickSize), oa.tickSize)
		joinCohort := firstTimestamp.Format("2006-01")

		// Build cumulative timeline
		cumulative := newCumulativeMetrics()
		tickToMetrics := map[int]*cumulativeMetrics{}

		for _, tick := range sortedTicks {
			tickMetrics := timeline[tick]
			cumulative.accumulate(tickMetrics)

			// Store snapshot of cumulative state at this tick
			tickToMetrics[tick] = &cumulativeMetrics{
				commits:           cumulative.commits,
				files:             copyFileSet(cumulative.files),
				lines:             cumulative.lines,
				meaningfulCommits: cumulative.meaningfulCommits,
				meaningfulFiles:   copyFileSet(cumulative.meaningfulFiles),
				meaningfulLines:   cumulative.meaningfulLines,
			}
		}

		// Compute window snapshots
		snapshots := map[int]*OnboardingSnapshot{}
		ticksPerDay := int(24 * time.Hour / oa.tickSize)

		for _, windowDays := range oa.WindowDays {
			targetTick := firstTick + (windowDays * ticksPerDay)
			closestTick := findClosestTick(sortedTicks, targetTick)

			if closestTick == -1 {
				continue // No data within this window
			}

			cm := tickToMetrics[closestTick]
			snapshots[windowDays] = &OnboardingSnapshot{
				DaysSinceJoin:     windowDays,
				TotalCommits:      cm.commits,
				TotalFiles:        len(cm.files),
				TotalLines:        cm.lines,
				MeaningfulCommits: cm.meaningfulCommits,
				MeaningfulFiles:   len(cm.meaningfulFiles),
				MeaningfulLines:   cm.meaningfulLines,
			}
		}

		authors[authorID] = &AuthorOnboardingData{
			FirstCommitTick: firstTick,
			JoinCohort:      joinCohort,
			Snapshots:       snapshots,
		}

		cohortGroups[joinCohort] = append(cohortGroups[joinCohort], authorID)
	}

	// Continue to cohort aggregation
	return oa.finalizeCohorts(authors, cohortGroups)
}

// finalizeCohorts computes cohort aggregates and returns final result
func (oa *OnboardingAnalysis) finalizeCohorts(
	authors map[int]*AuthorOnboardingData,
	cohortGroups map[string][]int,
) OnboardingResult {
	cohorts := make(map[string]*CohortStats, len(cohortGroups))

	for cohort, authorIDs := range cohortGroups {
		if len(authorIDs) == 0 {
			continue
		}

		// Aggregate snapshots across all authors in cohort
		windowSums := map[int]*OnboardingSnapshot{}

		for _, authorID := range authorIDs {
			authorData := authors[authorID]
			for windowDays, snapshot := range authorData.Snapshots {
				sum, exists := windowSums[windowDays]
				if !exists {
					sum = &OnboardingSnapshot{
						DaysSinceJoin: windowDays,
					}
					windowSums[windowDays] = sum
				}

				sum.TotalCommits += snapshot.TotalCommits
				sum.TotalFiles += snapshot.TotalFiles
				sum.TotalLines += snapshot.TotalLines
				sum.MeaningfulCommits += snapshot.MeaningfulCommits
				sum.MeaningfulFiles += snapshot.MeaningfulFiles
				sum.MeaningfulLines += snapshot.MeaningfulLines
			}
		}

		// Compute averages
		authorCount := len(authorIDs)
		averageSnapshots := make(map[int]*OnboardingSnapshot, len(windowSums))

		for windowDays, sum := range windowSums {
			averageSnapshots[windowDays] = &OnboardingSnapshot{
				DaysSinceJoin:     windowDays,
				TotalCommits:      sum.TotalCommits / authorCount,
				TotalFiles:        sum.TotalFiles / authorCount,
				TotalLines:        sum.TotalLines / authorCount,
				MeaningfulCommits: sum.MeaningfulCommits / authorCount,
				MeaningfulFiles:   sum.MeaningfulFiles / authorCount,
				MeaningfulLines:   sum.MeaningfulLines / authorCount,
			}
		}

		cohorts[cohort] = &CohortStats{
			Cohort:           cohort,
			AuthorCount:      authorCount,
			AverageSnapshots: averageSnapshots,
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

// Fork clones this pipeline item.
func (oa *OnboardingAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(oa, n)
}

// serializeText outputs YAML format
func (oa *OnboardingAnalysis) serializeText(result *OnboardingResult, writer io.Writer) {
	fmt.Fprintln(writer, "  onboarding:")

	// Configuration
	windowStrs := make([]string, len(result.WindowDays))
	for i, days := range result.WindowDays {
		windowStrs[i] = strconv.Itoa(days)
	}
	fmt.Fprintf(writer, "    window_days: [%s]\n", strings.Join(windowStrs, ", "))
	fmt.Fprintf(writer, "    meaningful_threshold: %d\n", result.MeaningfulThreshold)

	// Authors (sorted by ID)
	authorIDs := make([]int, 0, len(result.Authors))
	for id := range result.Authors {
		authorIDs = append(authorIDs, id)
	}
	sort.Ints(authorIDs)

	fmt.Fprintln(writer, "    authors:")
	for _, authorID := range authorIDs {
		author := result.Authors[authorID]
		if authorID == core.AuthorMissing {
			authorID = -1
		}
		fmt.Fprintf(writer, "      %d:\n", authorID)
		fmt.Fprintf(writer, "        first_commit_tick: %d\n", author.FirstCommitTick)
		fmt.Fprintf(writer, "        join_cohort: %s\n", yaml.SafeString(author.JoinCohort))

		// Snapshots (sorted by days)
		windowDays := make([]int, 0, len(author.Snapshots))
		for days := range author.Snapshots {
			windowDays = append(windowDays, days)
		}
		sort.Ints(windowDays)

		fmt.Fprintln(writer, "        snapshots:")
		for _, days := range windowDays {
			snap := author.Snapshots[days]
			fmt.Fprintf(writer, "          %d: {days: %d, commits: %d, files: %d, lines: %d, meaningful_commits: %d, meaningful_files: %d, meaningful_lines: %d}\n",
				days, snap.DaysSinceJoin, snap.TotalCommits, snap.TotalFiles, snap.TotalLines,
				snap.MeaningfulCommits, snap.MeaningfulFiles, snap.MeaningfulLines)
		}
	}

	// Cohorts (sorted alphabetically)
	cohortNames := make([]string, 0, len(result.Cohorts))
	for name := range result.Cohorts {
		cohortNames = append(cohortNames, name)
	}
	sort.Strings(cohortNames)

	fmt.Fprintln(writer, "    cohorts:")
	for _, name := range cohortNames {
		cohort := result.Cohorts[name]
		fmt.Fprintf(writer, "      %s:\n", yaml.SafeString(name))
		fmt.Fprintf(writer, "        author_count: %d\n", cohort.AuthorCount)

		// Average snapshots (sorted by days)
		windowDays := make([]int, 0, len(cohort.AverageSnapshots))
		for days := range cohort.AverageSnapshots {
			windowDays = append(windowDays, days)
		}
		sort.Ints(windowDays)

		fmt.Fprintln(writer, "        average_snapshots:")
		for _, days := range windowDays {
			snap := cohort.AverageSnapshots[days]
			fmt.Fprintf(writer, "          %d: {days: %d, commits: %d, files: %d, lines: %d, meaningful_commits: %d, meaningful_files: %d, meaningful_lines: %d}\n",
				days, snap.DaysSinceJoin, snap.TotalCommits, snap.TotalFiles, snap.TotalLines,
				snap.MeaningfulCommits, snap.MeaningfulFiles, snap.MeaningfulLines)
		}
	}

	// People
	fmt.Fprintln(writer, "    people:")
	for _, person := range result.reversedPeopleDict {
		fmt.Fprintf(writer, "    - %s\n", yaml.SafeString(person))
	}

	fmt.Fprintln(writer, "    tick_size:", int(result.tickSize.Seconds()))
}

// serializeBinary outputs Protocol Buffers format
func (oa *OnboardingAnalysis) serializeBinary(result *OnboardingResult, writer io.Writer) error {
	message := pb.OnboardingResults{
		DevIndex:            result.reversedPeopleDict,
		TickSize:            int64(result.tickSize),
		WindowDays:          make([]int32, len(result.WindowDays)),
		MeaningfulThreshold: int32(result.MeaningfulThreshold),
	}

	for i, days := range result.WindowDays {
		message.WindowDays[i] = int32(days)
	}

	// Authors
	message.Authors = make(map[int32]*pb.AuthorOnboardingData, len(result.Authors))
	for authorID, author := range result.Authors {
		if authorID == core.AuthorMissing {
			authorID = -1
		}

		pbAuthor := &pb.AuthorOnboardingData{
			FirstCommitTick: int32(author.FirstCommitTick),
			JoinCohort:      author.JoinCohort,
			Snapshots:       make(map[int32]*pb.OnboardingSnapshot, len(author.Snapshots)),
		}

		for days, snap := range author.Snapshots {
			pbAuthor.Snapshots[int32(days)] = &pb.OnboardingSnapshot{
				DaysSinceJoin:     int32(snap.DaysSinceJoin),
				TotalCommits:      int32(snap.TotalCommits),
				TotalFiles:        int32(snap.TotalFiles),
				TotalLines:        int32(snap.TotalLines),
				MeaningfulCommits: int32(snap.MeaningfulCommits),
				MeaningfulFiles:   int32(snap.MeaningfulFiles),
				MeaningfulLines:   int32(snap.MeaningfulLines),
			}
		}

		message.Authors[int32(authorID)] = pbAuthor
	}

	// Cohorts
	message.Cohorts = make(map[string]*pb.CohortStats, len(result.Cohorts))
	for cohortName, cohort := range result.Cohorts {
		pbCohort := &pb.CohortStats{
			Cohort:           cohort.Cohort,
			AuthorCount:      int32(cohort.AuthorCount),
			AverageSnapshots: make(map[int32]*pb.OnboardingAverageSnapshot, len(cohort.AverageSnapshots)),
		}

		for days, snap := range cohort.AverageSnapshots {
			pbCohort.AverageSnapshots[int32(days)] = &pb.OnboardingAverageSnapshot{
				DaysSinceJoin:        int32(snap.DaysSinceJoin),
				AvgTotalCommits:      float64(snap.TotalCommits),
				AvgTotalFiles:        float64(snap.TotalFiles),
				AvgTotalLines:        float64(snap.TotalLines),
				AvgMeaningfulCommits: float64(snap.MeaningfulCommits),
				AvgMeaningfulFiles:   float64(snap.MeaningfulFiles),
				AvgMeaningfulLines:   float64(snap.MeaningfulLines),
			}
		}

		message.Cohorts[cohortName] = pbCohort
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (oa *OnboardingAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	onboardingResult := result.(OnboardingResult)
	if binary {
		return oa.serializeBinary(&onboardingResult, writer)
	}
	oa.serializeText(&onboardingResult, writer)
	return nil
}
