package modes

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/render/readers"
)

type onboardingTestReader struct {
	*NoDataReader

	data *readers.OnboardingData
}

func (r *onboardingTestReader) GetOnboarding() (*readers.OnboardingData, error) {
	return r.data, nil
}

func newOnboardingTestReader() *onboardingTestReader {
	return &onboardingTestReader{NoDataReader: &NoDataReader{}, data: onboardingTestData()}
}

func onboardingTestData() *readers.OnboardingData {
	return &readers.OnboardingData{
		WindowDays:          []int{7, 30, 90},
		MeaningfulThreshold: 10,
		People:              []string{"Alice|alice@example.com", "Bob <bob@example.com>"},
		TickSize:            86400,
		Authors: map[int]readers.OnboardingAuthorData{
			0: {
				FirstCommitTick:             1,
				DaysToFirstMeaningfulCommit: 3,
				JoinCohort:                  "2020-01",
				Snapshots: map[int]readers.OnboardingSnapshotData{
					7:  {DaysSinceJoin: 7, MeaningfulLines: 120, TotalLines: 200},
					30: {DaysSinceJoin: 30, MeaningfulLines: 640, TotalLines: 900},
					90: {DaysSinceJoin: 90, MeaningfulLines: 1500, TotalLines: 2100},
				},
			},
			1: {
				FirstCommitTick:             40,
				DaysToFirstMeaningfulCommit: -1,
				JoinCohort:                  "2020-03",
				Snapshots: map[int]readers.OnboardingSnapshotData{
					7:  {DaysSinceJoin: 7, MeaningfulLines: 0, TotalLines: 4},
					30: {DaysSinceJoin: 30, MeaningfulLines: 0, TotalLines: 9},
					90: {DaysSinceJoin: 90, MeaningfulLines: 0, TotalLines: 9},
				},
			},
			-1: {
				FirstCommitTick:             12,
				DaysToFirstMeaningfulCommit: 45,
				JoinCohort:                  "2020-01",
				Snapshots: map[int]readers.OnboardingSnapshotData{
					90: {DaysSinceJoin: 90, MeaningfulLines: 70},
				},
			},
		},
		Cohorts: map[string]readers.OnboardingCohortData{
			"2020-01": {
				Cohort:      "2020-01",
				AuthorCount: 2,
				AverageSnapshots: map[int]readers.OnboardingAverageSnapshotData{
					7:  {DaysSinceJoin: 7, AvgMeaningfulLines: 60},
					30: {DaysSinceJoin: 30, AvgMeaningfulLines: 320},
					90: {DaysSinceJoin: 90, AvgMeaningfulLines: 785},
				},
			},
			"2020-03": {
				Cohort:      "2020-03",
				AuthorCount: 1,
				AverageSnapshots: map[int]readers.OnboardingAverageSnapshotData{
					7:  {DaysSinceJoin: 7, AvgMeaningfulLines: 0},
					30: {DaysSinceJoin: 30, AvgMeaningfulLines: 0},
					90: {DaysSinceJoin: 90, AvgMeaningfulLines: 0},
				},
			},
		},
	}
}

func onboardingCompanionPaths(dir, ext string) []string {
	return []string{
		filepath.Join(dir, "onboarding_rampup."+ext),
		filepath.Join(dir, "onboarding_time-to-first."+ext),
		filepath.Join(dir, "onboarding_authors."+ext),
	}
}

func TestOnboardingWritesCharts(t *testing.T) {
	dir := t.TempDir()

	err := Onboarding(newOnboardingTestReader(), filepath.Join(dir, "onboarding.png"))
	require.NoError(t, err)

	for _, path := range onboardingCompanionPaths(dir, "png") {
		assertNonEmptyFile(t, path)
	}
}

func TestOnboardingMatchesPythonDimensions(t *testing.T) {
	dir := t.TempDir()

	err := Onboarding(newOnboardingTestReader(), filepath.Join(dir, "onboarding.png"))
	require.NoError(t, err)

	tests := []struct {
		defaultOutput string
		path          string
	}{
		{onboardingRampupOutput, filepath.Join(dir, "onboarding_rampup.png")},
		{onboardingTimeToFirstOutput, filepath.Join(dir, "onboarding_time-to-first.png")},
		{onboardingAuthorsOutput, filepath.Join(dir, "onboarding_authors.png")},
	}

	for _, tt := range tests {
		wantWidth, wantHeight := reportPlotPixels(tt.defaultOutput)
		bounds := readPNG(t, tt.path).Bounds()
		require.Equalf(
			t, [2]int{wantWidth, wantHeight}, [2]int{bounds.Dx(), bounds.Dy()},
			"unexpected size for %s", tt.path,
		)
	}
}

func TestOnboardingPreservesTransparentBackground(t *testing.T) {
	dir := t.TempDir()

	err := Onboarding(newOnboardingTestReader(), filepath.Join(dir, "onboarding.png"))
	require.NoError(t, err)

	for _, path := range onboardingCompanionPaths(dir, "png") {
		require.Truef(t, hasTransparentPixel(readPNG(t, path)), "expected transparent background in %s", path)
	}
}

// TestOnboardingRendersNoMeaningfulCommitBucket pins the -1 sentinel: day 0 is a
// legitimate and common value, so an author who never made a meaningful commit
// inside the retained trail must land in its own labelled overflow bucket rather
// than on the left-most bar.
func TestOnboardingRendersNoMeaningfulCommitBucket(t *testing.T) {
	data := onboardingTestData()
	windows := onboardingWindows(data.WindowDays)

	labels, counts := onboardingTimeToFirstBuckets(data, windows)

	require.Equal(t, []string{"0-7d", "8-30d", "31-90d", "none within 90d"}, labels)
	require.Equal(t, []int{1, 0, 1, 1}, counts)

	// The same author read as day zero would have inflated the first bucket.
	zeroDay := onboardingBucketIndex(0, windows, len(labels)-1)
	sentinel := onboardingBucketIndex(-1, windows, len(labels)-1)
	require.Equal(t, 0, zeroDay)
	require.Equal(t, len(labels)-1, sentinel)
}

func TestOnboardingReturnsMissingOnEmptyData(t *testing.T) {
	tests := []struct {
		name string
		data *readers.OnboardingData
	}{
		{name: "nil data", data: nil},
		{name: "no authors", data: &readers.OnboardingData{WindowDays: []int{7}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &onboardingTestReader{NoDataReader: &NoDataReader{}, data: tt.data}
			err := Onboarding(reader, filepath.Join(t.TempDir(), "onboarding.png"))
			require.ErrorIs(t, err, readers.ErrAnalysisMissing)
		})
	}
}

// TestOnboardingRendersWithoutCohorts covers a result whose authors are known
// but whose cohort map is empty: the heatmap is skipped, the other two charts
// still render.
func TestOnboardingRendersWithoutCohorts(t *testing.T) {
	dir := t.TempDir()
	data := onboardingTestData()
	data.Cohorts = nil

	reader := &onboardingTestReader{NoDataReader: &NoDataReader{}, data: data}
	err := Onboarding(reader, filepath.Join(dir, "onboarding.png"))
	require.NoError(t, err)

	require.NoFileExists(t, filepath.Join(dir, "onboarding_rampup.png"))
	assertNonEmptyFile(t, filepath.Join(dir, "onboarding_time-to-first.png"))
	assertNonEmptyFile(t, filepath.Join(dir, "onboarding_authors.png"))
}

func TestOnboardingAuthorLabelsFallBackForUnknownIdentities(t *testing.T) {
	people := []string{"Alice|alice@example.com"}

	require.Equal(t, "Alice", onboardingAuthorLabel(people, 0))
	require.Equal(t, unknownContributorLabel, onboardingAuthorLabel(people, -1))
	require.Equal(t, unknownContributorLabel, onboardingAuthorLabel(people, 262143))
}

// TestOnboardingBucketIndexWithoutWindowsUsesTheOpenEndedBin pins the empty-window
// case against the labels it has to agree with: onboardingBucketLabels degrades to
// ["0+d", "none"], so a real ramp-up must land in bin 0. Before the guard in
// onboardingBucketIndex the loop simply never ran and every non-negative value fell
// through to the sentinel, leaving "0+d" permanently empty.
func TestOnboardingBucketIndexWithoutWindowsUsesTheOpenEndedBin(t *testing.T) {
	labels := onboardingBucketLabels(nil)
	require.Equal(t, []string{"0+d", "none"}, labels)

	sentinel := len(labels) - 1

	require.Equal(t, 0, onboardingBucketIndex(0, nil, sentinel))
	require.Equal(t, 0, onboardingBucketIndex(120, nil, sentinel))
	require.Equal(t, sentinel, onboardingBucketIndex(-1, nil, sentinel))
}

// TestOnboardingBucketIndexKeepsBeyondWindowWithTheSentinel guards the opposite
// end: with windows configured, a value past the largest one cannot have come from
// the trail, so it belongs with "none" rather than in the last day bucket.
func TestOnboardingBucketIndexKeepsBeyondWindowWithTheSentinel(t *testing.T) {
	windows := []int{7, 30, 90}
	sentinel := len(onboardingBucketLabels(windows)) - 1

	require.Equal(t, 0, onboardingBucketIndex(7, windows, sentinel))
	require.Equal(t, 1, onboardingBucketIndex(8, windows, sentinel))
	require.Equal(t, 2, onboardingBucketIndex(90, windows, sentinel))
	require.Equal(t, sentinel, onboardingBucketIndex(91, windows, sentinel))
	require.Equal(t, sentinel, onboardingBucketIndex(-1, windows, sentinel))
}
