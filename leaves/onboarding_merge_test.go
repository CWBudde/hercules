package leaves

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

// onboardingTestCommit is one commit fed to a per-repository OnboardingAnalysis.
type onboardingTestCommit struct {
	author int
	tick   int
	when   time.Time
	files  map[string]int
}

const onboardingMergeThreshold = 10

// buildOnboardingTestResult runs one repository's analysis to completion, which is the only way to
// obtain a result with a well-formed trail.
func buildOnboardingTestResult(
	t *testing.T, people []string, windows []int, commits ...onboardingTestCommit,
) OnboardingResult {
	t.Helper()

	oa := &OnboardingAnalysis{
		WindowDays:          windows,
		MeaningfulThreshold: onboardingMergeThreshold,
		tickSize:            24 * time.Hour,
		reversedPeopleDict:  people,
	}
	require.NoError(t, oa.Initialize(test.Repository))

	for _, commit := range commits {
		_, err := oa.Consume(makeTestDepsAt(commit.author, commit.tick, commit.when, commit.files))
		require.NoError(t, err)
	}

	result, ok := oa.Finalize().(OnboardingResult)
	require.True(t, ok)

	return result
}

func mergeOnboardingResults(
	t *testing.T, first, second OnboardingResult, common1, common2 *core.CommonAnalysisResult,
) OnboardingResult {
	t.Helper()

	merged := (&OnboardingAnalysis{}).MergeResults(first, second, common1, common2)
	if err, isError := merged.(error); isError {
		t.Fatalf("merge onboarding results: %v", err)
	}

	result, ok := merged.(OnboardingResult)
	require.True(t, ok)

	return result
}

// onboardingAuthorsByName re-keys the authors by identity name, so that two merges which happen to
// number the merged dictionary differently stay comparable.
func onboardingAuthorsByName(result OnboardingResult) map[string]*AuthorOnboardingData {
	byName := make(map[string]*AuthorOnboardingData, len(result.Authors))

	for authorID, author := range result.Authors {
		name := "<unnamed>"
		if authorID >= 0 && authorID < len(result.reversedPeopleDict) {
			name = result.reversedPeopleDict[authorID]
		}

		byName[name] = author
	}

	return byName
}

func onboardingDay(day int) time.Time {
	return time.Date(2020, time.January, 1, 12, 0, 0, 0, time.UTC).
		Add(time.Duration(day) * 24 * time.Hour)
}

// TestOnboardingMergeReanchorsLateJoiner is the headline case: somebody who joined the
// organisation in 2020 through one repository and first touched another in 2023 must not have
// those 2023 commits counted inside their 30-day ramp-up.
func TestOnboardingMergeReanchorsLateJoiner(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}

	early := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a1.go": 20},
		},
		onboardingTestCommit{
			author: 0, tick: 10, when: onboardingDay(10),
			files: map[string]int{"a2.go": 30},
		},
	)
	late := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0,
			when:  time.Date(2023, time.June, 5, 9, 0, 0, 0, time.UTC),
			files: map[string]int{"b1.go": 100},
		},
	)

	require.Equal(t, 50, early.Authors[0].Snapshots[30].TotalLines)
	require.Equal(t, 100, late.Authors[0].Snapshots[30].TotalLines)

	merged := mergeOnboardingResults(t, early, late, nil, nil)
	author := onboardingAuthorsByName(merged)["alice"]
	require.NotNil(t, author)

	assert.Equal(t, "2020-01", author.JoinCohort,
		"the cohort follows the organisation-wide first commit")
	assert.Equal(t, onboardingDay(0).Unix(), author.FirstCommitUnix)
	assert.Equal(t, 50, author.Snapshots[30].TotalLines,
		"the 2023 commits are outside the re-anchored 30-day window; a naive sum would say 150")
	assert.Equal(t, 2, author.Snapshots[30].TotalCommits)
	assert.Len(t, author.Trail, 2, "the trail is truncated back to the retention horizon")

	require.Contains(t, merged.Cohorts, "2020-01")
	assert.NotContains(t, merged.Cohorts, "2023-06")
}

func TestOnboardingMergeIsCommutative(t *testing.T) {
	windows := []int{7, 30}
	people := []string{"alice", "bob"}

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
		onboardingTestCommit{
			author: 1, tick: 4, when: onboardingDay(4),
			files: map[string]int{"b.go": 15},
		},
	)
	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 2, when: onboardingDay(2),
			files: map[string]int{"c.go": 40},
		},
		onboardingTestCommit{
			author: 1, tick: 1, when: onboardingDay(1),
			files: map[string]int{"d.go": 11},
		},
	)

	forward := mergeOnboardingResults(t, first, second, nil, nil)
	backward := mergeOnboardingResults(t, second, first, nil, nil)

	assert.Equal(t, onboardingAuthorsByName(forward), onboardingAuthorsByName(backward))
	assert.Equal(t, forward.Cohorts, backward.Cohorts)
}

// TestOnboardingMergeIsAssociative pins the monotonicity lemma: the third repository holds the
// earliest anchor, so the second grouping re-anchors after a truncation has already happened.
func TestOnboardingMergeIsAssociative(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(100),
			files: map[string]int{"a.go": 20},
		},
		onboardingTestCommit{
			author: 0, tick: 5, when: onboardingDay(105),
			files: map[string]int{"a2.go": 20},
		},
	)
	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(60),
			files: map[string]int{"b.go": 30},
		},
	)
	third := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(50),
			files: map[string]int{"c.go": 40},
		},
		onboardingTestCommit{
			author: 0, tick: 20, when: onboardingDay(70),
			files: map[string]int{"c2.go": 50},
		},
	)

	left := mergeOnboardingResults(
		t, mergeOnboardingResults(t, first, second, nil, nil), third, nil, nil,
	)
	right := mergeOnboardingResults(
		t, first, mergeOnboardingResults(t, second, third, nil, nil), nil, nil,
	)

	assert.Equal(t, onboardingAuthorsByName(left), onboardingAuthorsByName(right))

	author := onboardingAuthorsByName(left)["alice"]
	require.NotNil(t, author)
	assert.Equal(t, onboardingDay(50).Unix(), author.FirstCommitUnix)
	assert.Equal(t, 3, author.Snapshots[30].TotalCommits,
		"day 50, 60 and 70 are inside the window; day 100 and 105 are not")
	assert.Equal(t, 120, author.Snapshots[30].TotalLines)
}

func TestOnboardingMergeDoesNotDoubleCountFilesWithinOneRepository(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}

	repository := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 1, when: onboardingDay(1),
			files: map[string]int{"same.go": 20},
		},
		onboardingTestCommit{
			author: 0, tick: 5, when: onboardingDay(5),
			files: map[string]int{"same.go": 30},
		},
	)
	empty := buildOnboardingTestResult(t, people, windows)

	merged := mergeOnboardingResults(t, repository, empty, nil, nil)
	author := onboardingAuthorsByName(merged)["alice"]
	require.NotNil(t, author)

	assert.Equal(t, 2, author.Snapshots[30].TotalCommits)
	assert.Equal(t, 1, author.Snapshots[30].TotalFiles,
		"one file touched twice is one file; a per-commit file counter would say 2")
	assert.Equal(t, 1, author.Snapshots[30].MeaningfulFiles)
}

func TestOnboardingMergeJoinsAliasedIdentities(t *testing.T) {
	windows := []int{30}

	first := buildOnboardingTestResult(
		t, []string{"Bob|bob@x"}, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
	)
	second := buildOnboardingTestResult(
		t, []string{"bob@x"}, windows,
		onboardingTestCommit{
			author: 0, tick: 3, when: onboardingDay(3),
			files: map[string]int{"b.go": 30},
		},
	)

	merged := mergeOnboardingResults(t, first, second, nil, nil)

	require.Len(t, merged.Authors, 1, "the two aliases collapse into one merged author")

	for _, author := range merged.Authors {
		assert.Len(t, author.Trail, 2, "both trails are concatenated, never overwritten")
		assert.Equal(t, 2, author.Snapshots[30].TotalCommits)
		assert.Equal(t, 50, author.Snapshots[30].TotalLines)
	}
}

// TestOnboardingMergePreservesCohortMonthAcrossTimezones guards the documented contract that the
// cohort is the calendar month in the commit's own UTC offset. Recomputing it from the stored Unix
// seconds would report 2024-12 here.
func TestOnboardingMergePreservesCohortMonthAcrossTimezones(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}
	athens := time.FixedZone("Europe/Athens", 2*60*60)
	joined := time.Date(2025, time.January, 1, 0, 30, 0, 0, athens)

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: joined,
			files: map[string]int{"a.go": 20},
		},
	)
	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 10, when: joined.Add(10 * 24 * time.Hour),
			files: map[string]int{"b.go": 20},
		},
	)

	require.Equal(t, "2025-01", first.Authors[0].JoinCohort)

	merged := mergeOnboardingResults(t, first, second, nil, nil)
	author := onboardingAuthorsByName(merged)["alice"]
	require.NotNil(t, author)

	assert.Equal(t, "2025-01", author.JoinCohort)
	assert.Contains(t, merged.Cohorts, "2025-01")
	assert.NotContains(t, merged.Cohorts, "2024-12")
}

func TestOnboardingMergeRebasesFirstCommitTick(t *testing.T) {
	windows := []int{30}
	people := []string{"alice", "bob"}

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
	)
	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 1, tick: 3,
			when:  time.Date(2021, time.January, 4, 12, 0, 0, 0, time.UTC),
			files: map[string]int{"b.go": 20},
		},
	)

	common1 := &core.CommonAnalysisResult{
		BeginTime: time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}
	common2 := &core.CommonAnalysisResult{
		BeginTime: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}

	merged := mergeOnboardingResults(t, first, second, common1, common2)
	authors := onboardingAuthorsByName(merged)

	require.NotNil(t, authors["alice"])
	require.NotNil(t, authors["bob"])
	assert.Equal(t, 0, authors["alice"].FirstCommitTick, "the earlier repository keeps its axis")
	assert.Equal(t, 3+366, authors["bob"].FirstCommitTick,
		"2020 is a leap year, so the later repository's ticks shift by 366")
}

func TestOnboardingMergeOfOneEqualsFinalize(t *testing.T) {
	windows := []int{7, 30, 90}
	people := []string{"alice", "bob"}

	result := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20, "b.go": 5},
		},
		onboardingTestCommit{
			author: 0, tick: 9, when: onboardingDay(9),
			files: map[string]int{"a.go": 30},
		},
		onboardingTestCommit{
			author: 1, tick: 3, when: onboardingDay(3),
			files: map[string]int{"c.go": 40},
		},
		onboardingTestCommit{
			author: 0, tick: 200, when: onboardingDay(200),
			files: map[string]int{"d.go": 60},
		},
	)
	empty := buildOnboardingTestResult(t, people, windows)

	merged := mergeOnboardingResults(t, result, empty, nil, nil)

	assert.Equal(t, result.Authors, merged.Authors)
	assert.Equal(t, result.Cohorts, merged.Cohorts)
	assert.Equal(t, result.WindowDays, merged.WindowDays)
	assert.Equal(t, result.TrailDays, merged.TrailDays)
	assert.Equal(t, result.MeaningfulThreshold, merged.MeaningfulThreshold)
}

// TestOnboardingMergeIncludesCommitExactlyAtHorizon covers the tight bound: an exclusive
// comparison in either the trail builder or the window sum drops precisely these commits.
func TestOnboardingMergeIncludesCommitExactlyAtHorizon(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
		onboardingTestCommit{
			author: 0, tick: 30, when: onboardingDay(30),
			files: map[string]int{"a2.go": 20},
		},
	)
	require.Len(t, first.Authors[0].Trail, 2, "the retention horizon is inclusive")

	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 30, when: onboardingDay(30),
			files: map[string]int{"b.go": 20},
		},
	)

	merged := mergeOnboardingResults(t, first, second, nil, nil)
	author := onboardingAuthorsByName(merged)["alice"]
	require.NotNil(t, author)

	assert.Equal(t, 3, author.Snapshots[30].TotalCommits)
	assert.Equal(t, 60, author.Snapshots[30].TotalLines)
}

// TestOnboardingMergeCountsSamePathInTwoRepositoriesTwice pins the argument against qualifying
// paths: files in different repositories are different files.
func TestOnboardingMergeCountsSamePathInTwoRepositoriesTwice(t *testing.T) {
	windows := []int{30}
	people := []string{"alice"}

	first := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"README.md": 20},
		},
	)
	second := buildOnboardingTestResult(
		t, people, windows,
		onboardingTestCommit{
			author: 0, tick: 2, when: onboardingDay(2),
			files: map[string]int{"README.md": 20},
		},
	)

	merged := mergeOnboardingResults(t, first, second, nil, nil)
	author := onboardingAuthorsByName(merged)["alice"]
	require.NotNil(t, author)

	assert.Equal(t, 2, author.Snapshots[30].TotalFiles)
	assert.Equal(t, 2, author.Snapshots[30].MeaningfulFiles)
}

func TestOnboardingMergeRejectsMismatchingTickSizes(t *testing.T) {
	first := buildOnboardingTestResult(
		t, []string{"alice"}, []int{30},
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
	)
	second := first
	second.tickSize = 48 * time.Hour

	merged := (&OnboardingAnalysis{}).MergeResults(first, second, nil, nil)
	err, isError := merged.(error)
	require.True(t, isError)
	require.ErrorIs(t, err, errOnboardingMismatchingTickSizes)
}

func TestOnboardingMergeRejectsMismatchingThresholds(t *testing.T) {
	first := buildOnboardingTestResult(
		t, []string{"alice"}, []int{30},
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
	)
	second := first
	second.MeaningfulThreshold = 42

	merged := (&OnboardingAnalysis{}).MergeResults(first, second, nil, nil)
	err, isError := merged.(error)
	require.True(t, isError)
	require.ErrorIs(t, err, errOnboardingMismatchingThresholds)
}

func TestOnboardingMergeRejectsMismatchingWindows(t *testing.T) {
	people := []string{"alice"}
	commit := onboardingTestCommit{
		author: 0, tick: 0, when: onboardingDay(0),
		files: map[string]int{"a.go": 20},
	}

	first := buildOnboardingTestResult(t, people, []int{30}, commit)
	second := buildOnboardingTestResult(t, people, []int{7, 30}, commit)

	merged := (&OnboardingAnalysis{}).MergeResults(first, second, nil, nil)
	err, isError := merged.(error)
	require.True(t, isError)
	require.ErrorIs(t, err, errOnboardingMismatchingWindows)
}

func TestOnboardingMergeRejectsTrailLessInput(t *testing.T) {
	first := buildOnboardingTestResult(
		t, []string{"alice"}, []int{30},
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"a.go": 20},
		},
	)

	stale := buildOnboardingTestResult(
		t, []string{"alice"}, []int{30},
		onboardingTestCommit{
			author: 0, tick: 0, when: onboardingDay(0),
			files: map[string]int{"b.go": 20},
		},
	)
	stale.TrailDays = 0
	stale.Authors[0].Trail = nil

	merged := (&OnboardingAnalysis{}).MergeResults(first, stale, nil, nil)
	err, isError := merged.(error)
	require.True(t, isError)
	require.ErrorIs(t, err, errOnboardingMissingTrail)
}

// TestOnboardingMergePreservesAuthorMissingBucket checks that the unattributed bucket, which
// arrives as -1 from every deserialized result, survives the identity translation unmapped.
func TestOnboardingMergePreservesAuthorMissingBucket(t *testing.T) {
	anchor := onboardingDay(0).Unix()
	missingSide := func(unixTime int64, lines int) OnboardingResult {
		return OnboardingResult{
			Authors: map[int]*AuthorOnboardingData{
				-1: {
					FirstCommitUnix: unixTime,
					JoinCohort:      "2020-01",
					Snapshots:       map[int]*OnboardingSnapshot{30: {DaysSinceJoin: 30}},
					Trail: []OnboardingTrailEntry{{
						UnixTime: unixTime, Commits: 1, NewFiles: 1, Lines: lines,
						MeaningfulCommits: 1, NewMeaningfulFiles: 1, MeaningfulLines: lines,
					}},
				},
			},
			WindowDays:          []int{30},
			MeaningfulThreshold: onboardingMergeThreshold,
			TrailDays:           30,
			reversedPeopleDict:  []string{"alice"},
			tickSize:            24 * time.Hour,
		}
	}

	merged := mergeOnboardingResults(
		t, missingSide(anchor, 20), missingSide(anchor+24*3600, 30), nil, nil,
	)

	require.Contains(t, merged.Authors, -1)
	assert.Len(t, merged.Authors, 1)
	assert.Equal(t, 2, merged.Authors[-1].Snapshots[30].TotalCommits)
	assert.Equal(t, 50, merged.Authors[-1].Snapshots[30].TotalLines)
}
