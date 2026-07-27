package modes

import (
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
)

func TestCompactPersonBurndownDropsEmptyBandsAndInactiveDates(t *testing.T) {
	dates := []time.Time{
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data := &burndown.ProcessedBurndown{
		Matrix: [][]float64{
			{0, 0, 0, 0, 0},
			{0, 12, 10, 0, 0},
			{0, 0, 4, 2, 0},
			{0, 0, 0, 0, 0},
		},
		DateRange: dates,
		Labels:    []string{"unused-old", "2021", "2022", "unused-new"},
	}

	err := compactPersonBurndown(data)
	if err != nil {
		t.Fatalf("compactPersonBurndown() failed: %v", err)
	}

	wantMatrix := [][]float64{
		{12, 10, 0},
		{0, 4, 2},
	}
	if !reflect.DeepEqual(data.Matrix, wantMatrix) {
		t.Fatalf("compacted matrix = %v, want %v", data.Matrix, wantMatrix)
	}
	if want := []string{"2021", "2022"}; !reflect.DeepEqual(data.Labels, want) {
		t.Fatalf("compacted labels = %v, want %v", data.Labels, want)
	}
	if want := dates[1:4]; !reflect.DeepEqual(data.DateRange, want) {
		t.Fatalf("compacted dates = %v, want %v", data.DateRange, want)
	}
}

func TestCompactPersonBurndownKeepsNeighborForSinglePoint(t *testing.T) {
	dates := []time.Time{
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data := &burndown.ProcessedBurndown{
		Matrix:    [][]float64{{0, 7}},
		DateRange: dates,
		Labels:    []string{"2024"},
	}

	err := compactPersonBurndown(data)
	if err != nil {
		t.Fatalf("compactPersonBurndown() failed: %v", err)
	}
	if !reflect.DeepEqual(data.DateRange, dates) {
		t.Fatalf("single-point date range = %v, want %v", data.DateRange, dates)
	}
}

func TestCompactPersonBurndownRejectsEmptyActivity(t *testing.T) {
	data := &burndown.ProcessedBurndown{
		Matrix:    [][]float64{{0, 0}},
		DateRange: []time.Time{time.Unix(0, 0), time.Unix(1, 0)},
		Labels:    []string{"unused"},
	}
	err := compactPersonBurndown(data)
	if err == nil {
		t.Fatal("compactPersonBurndown() accepted an all-zero contributor")
	}
}
