package modes

import (
	"errors"
	"testing"

	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestDevsParallelRequiresPeopleBurndownByDefault(t *testing.T) {
	err := DevsParallel(&NoDataReader{}, t.TempDir(), 20, false, false)
	if !errors.Is(err, readers.ErrAnalysisMissing) {
		t.Fatalf("Expected missing-analysis error when people burndown is absent, got %v", err)
	}
}

func TestFilterPeopleBurndownByActivityRespectsMaxPeople(t *testing.T) {
	people := []readers.PeopleBurndown{
		{Person: "low", Matrix: [][]int{{1}}},
		{Person: "high", Matrix: [][]int{{10}}},
		{Person: "middle", Matrix: [][]int{{5}}},
	}

	filtered := filterPeopleBurndownByActivity(people, 2)
	if len(filtered) != 2 {
		t.Fatalf("Expected 2 people after filtering, got %d", len(filtered))
	}
	if filtered[0].Person != "high" || filtered[1].Person != "middle" {
		t.Fatalf("Unexpected filter order: %#v", filtered)
	}
}

func TestCalculateParallelDeveloperDataUsesPythonInputs(t *testing.T) {
	people := []string{"Alice", "Bob", "Carol"}
	ownership := map[string][][]int{
		"Alice": {{1, 2}, {10, 5}},
		"Bob":   {{1, 1}, {3, 2}},
		"Carol": {{0, 1}, {20, 10}},
	}
	couplingPeople := []string{"Alice", "Bob", "Carol"}
	couplingMatrix := [][]int{
		{0, 5, 1},
		{5, 0, 2},
		{1, 2, 0},
	}
	timeSeries := &readers.DeveloperTimeSeriesData{
		People: people,
		Days: map[int]map[int]readers.DevDay{
			0: {
				0: {Commits: 2, LinesAdded: 10},
				1: {Commits: 1, LinesAdded: 30},
			},
			1: {
				0: {Commits: 1, LinesAdded: 5, LinesRemoved: 5},
				2: {Commits: 3, LinesAdded: 1},
			},
			2: {
				2: {Commits: 1, LinesModified: 4},
			},
		},
	}

	data := calculateParallelDeveloperData(
		people, ownership, couplingPeople,
		readers.SparseMatrixFromDense(couplingMatrix), timeSeries, 2,
	)
	if len(data) != 2 {
		t.Fatalf("Expected 2 developers after max-people filtering, got %d", len(data))
	}
	if data[0].Name != "Carol" || data[0].Commits != 4 || data[0].OwnershipRank != 0 {
		t.Fatalf("Unexpected first developer data: %#v", data[0])
	}
	if data[1].Name != "Alice" || data[1].Commits != 3 || data[1].LinesRank != 0 {
		t.Fatalf("Unexpected second developer data: %#v", data[1])
	}
	if data[0].CommitCooccIndex != 1 || data[1].CommitCooccIndex != 0 {
		t.Fatalf("Expected commit co-occurrence order by shared active days, got %#v", data)
	}
}

func TestCalculateParallelismMetricsFromTimeSeries(t *testing.T) {
	data := []ParallelDeveloperData{
		{Name: "Alice"},
		{Name: "Bob"},
		{Name: "Carol"},
	}
	timeSeries := &readers.DeveloperTimeSeriesData{
		People: []string{"Alice", "Bob", "Carol"},
		Days: map[int]map[int]readers.DevDay{
			0: {0: {Commits: 1}},
			1: {0: {Commits: 1}, 1: {Commits: 1}},
			4: {2: {LinesModified: 3}},
		},
	}

	metrics := calculateParallelismMetricsFromParallelData(data, timeSeries)
	if metrics.TotalPeriods != 3 {
		t.Fatalf("TotalPeriods = %d, want 3 active days", metrics.TotalPeriods)
	}
	wantConcurrency := []int{1, 2, 1}
	for i, want := range wantConcurrency {
		if metrics.PeriodConcurrency[i] != want {
			t.Fatalf("PeriodConcurrency[%d] = %d, want %d", i, metrics.PeriodConcurrency[i], want)
		}
	}
	if metrics.PeakConcurrency != 2 {
		t.Fatalf("PeakConcurrency = %d, want 2", metrics.PeakConcurrency)
	}
	if metrics.ParallelPeriods != 1 {
		t.Fatalf("ParallelPeriods = %d, want 1", metrics.ParallelPeriods)
	}
}
