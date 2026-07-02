package burndown

import (
	"testing"
	"time"
)

func TestResampleBurndownDataUsesPythonYearEndBoundaries(t *testing.T) {
	start := time.Date(2024, time.December, 16, 0, 0, 0, 0, time.UTC)
	finish := start.Add(240 * 24 * time.Hour)
	daily := make([][]float64, 240)
	for i := range daily {
		daily[i] = make([]float64, 240)
		for j := range daily[i] {
			daily[i][j] = 1
		}
	}

	dateRange, matrix, labels, err := resampleBurndownData(daily, start, finish, "year")
	if err != nil {
		t.Fatalf("resampleBurndownData() error = %v", err)
	}

	if got, want := len(dateRange), 225; got != want {
		t.Fatalf("len(dateRange) = %d, want %d", got, want)
	}
	if got, want := dateRange[0], time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("dateRange[0] = %s, want %s", got, want)
	}
	if got, want := dateRange[len(dateRange)-1], time.Date(2025, time.August, 12, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("dateRange[-1] = %s, want %s", got, want)
	}
	if len(labels) != 2 || labels[0] != "2024" || labels[1] != "2025" {
		t.Fatalf("labels = %#v, want [2024 2025]", labels)
	}
	if got, want := len(matrix), 2; got != want {
		t.Fatalf("len(matrix) = %d, want %d", got, want)
	}
	if got, want := len(matrix[0]), 225; got != want {
		t.Fatalf("len(matrix[0]) = %d, want %d", got, want)
	}
	if got, want := matrix[0][0], 15.0; got != want {
		t.Fatalf("matrix[0][0] = %.1f, want %.1f", got, want)
	}
	if got, want := matrix[1][0], 225.0; got != want {
		t.Fatalf("matrix[1][0] = %.1f, want %.1f", got, want)
	}
}
