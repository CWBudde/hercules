package burndown

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historicalSurvivalMatrix() [][]int {
	return [][]int{
		{100, 80, 60, 40, 20, 0, 0, 0},
		{0, 50, 40, 30, 20, 10, 5, 0},
		{0, 0, 30, 25, 20, 15, 10, 5},
	}
}

func TestFitKaplanMeierMatchesHistoricalPython(t *testing.T) {
	curve, err := FitKaplanMeier(historicalSurvivalMatrix())
	if err != nil {
		t.Fatalf("FitKaplanMeier() error = %v", err)
	}

	expected := []SurvivalPoint{
		{Duration: 0, Ratio: 1},
		{Duration: 1, Ratio: 29.0 / 36},
		{Duration: 2, Ratio: 11.0 / 18},
		{Duration: 3, Ratio: 5.0 / 12},
		{Duration: 4, Ratio: 2.0 / 9},
		{Duration: 5, Ratio: 1.0 / 18},
		{Duration: 6, Ratio: 1.0 / 36},
	}
	if len(curve) != len(expected) {
		t.Fatalf("len(curve) = %d, want %d: %#v", len(curve), len(expected), curve)
	}
	for index, want := range expected {
		got := curve[index]
		if got.Duration != want.Duration || math.Abs(got.Ratio-want.Ratio) > 1e-12 {
			t.Errorf("curve[%d] = %#v, want %#v", index, got, want)
		}
	}
}

func TestFitKaplanMeierCensoringAndEmptyBehavior(t *testing.T) {
	tests := []struct {
		name   string
		matrix [][]int
		want   []SurvivalPoint
	}{
		{
			name:   "all zero",
			matrix: [][]int{{0, 0, 0}, {0, 0, 0}},
		},
		{
			name: "one sample survivor",
			matrix: [][]int{
				{10},
				{0},
			},
			want: []SurvivalPoint{{Duration: 0, Ratio: 1}, {Duration: 1, Ratio: 1}},
		},
		{
			name: "no final survivors follows historical censoring",
			matrix: [][]int{
				{100, 50, 0},
				{200, 100, 50},
			},
			want: []SurvivalPoint{
				{Duration: 0, Ratio: 1},
				{Duration: 1, Ratio: 1},
				{Duration: 2, Ratio: 1},
			},
		},
		{name: "empty"},
		{name: "empty columns", matrix: [][]int{{}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FitKaplanMeier(test.matrix)
			if err != nil {
				t.Fatalf("FitKaplanMeier() error = %v", err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("FitKaplanMeier() = %#v, want %#v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Errorf("curve[%d] = %#v, want %#v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestFitKaplanMeierRejectsInvalidMatrices(t *testing.T) {
	tests := []struct {
		name   string
		matrix [][]int
	}{
		{name: "ragged", matrix: [][]int{{1, 2}, {1}}},
		{name: "ragged after empty row", matrix: [][]int{{}, {1}}},
		{name: "negative", matrix: [][]int{{1, -1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := FitKaplanMeier(test.matrix)
			if !errors.Is(err, ErrInvalidSurvivalMatrix) {
				t.Fatalf("FitKaplanMeier() error = %v, want ErrInvalidSurvivalMatrix", err)
			}
		})
	}
}

func TestLoadBurndownSurvivalGoldenRawAndResampled(t *testing.T) {
	header := BurndownHeader{
		Start:       1704067417,
		Last:        1704672000,
		Sampling:    1,
		Granularity: 1,
		TickSize:    86400,
	}
	tests := []struct {
		name     string
		resample string
		golden   string
	}{
		{name: "raw", resample: "raw", golden: "survival_raw.golden.txt"},
		{name: "resampled", resample: "day", golden: "survival_resampled.golden.txt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processed, err := LoadBurndown(
				header, "project", historicalSurvivalMatrix(), test.resample, true, false,
			)
			if err != nil {
				t.Fatalf("LoadBurndown() error = %v", err)
			}
			var output bytes.Buffer
			if err := WriteSurvivalFunction(&output, processed.Survival, header.Sampling); err != nil {
				t.Fatalf("WriteSurvivalFunction() error = %v", err)
			}
			expected, err := os.ReadFile(filepath.Join("testdata", test.golden))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if got := output.String(); got != string(expected) {
				t.Errorf("survival output mismatch\n--- got ---\n%s--- want ---\n%s", got, expected)
			}
		})
	}
}

func TestLoadBurndownReportSurvivalFlag(t *testing.T) {
	header := BurndownHeader{
		Start:       1704067200,
		Last:        1704672000,
		Sampling:    1,
		Granularity: 1,
		TickSize:    86400,
	}
	withReport, err := LoadBurndown(header, "project", historicalSurvivalMatrix(), "raw", true, false)
	if err != nil {
		t.Fatalf("LoadBurndown(reportSurvival=true) error = %v", err)
	}
	withoutReport, err := LoadBurndown(header, "project", historicalSurvivalMatrix(), "raw", false, false)
	if err != nil {
		t.Fatalf("LoadBurndown(reportSurvival=false) error = %v", err)
	}
	if len(withReport.Survival) == 0 {
		t.Fatal("reportSurvival=true produced no survival curve")
	}
	if withoutReport.Survival != nil {
		t.Fatalf("reportSurvival=false Survival = %#v, want nil", withoutReport.Survival)
	}
}

func TestWriteSurvivalFunctionIsStableAndUsesSampling(t *testing.T) {
	curve, err := FitKaplanMeier(historicalSurvivalMatrix())
	if err != nil {
		t.Fatalf("FitKaplanMeier() error = %v", err)
	}
	var first string
	for iteration := 0; iteration < 100; iteration++ {
		var output bytes.Buffer
		if err := WriteSurvivalFunction(&output, curve, 3); err != nil {
			t.Fatalf("WriteSurvivalFunction() error = %v", err)
		}
		if iteration == 0 {
			first = output.String()
		} else if output.String() != first {
			t.Fatalf("iteration %d output changed", iteration)
		}
	}
	if !strings.Contains(first, "3 days") || !strings.Contains(first, "18 days") {
		t.Fatalf("sampling was not applied to durations:\n%s", first)
	}
}

func TestWriteSurvivalFunctionOmitsShortAndZeroCurves(t *testing.T) {
	tests := []struct {
		name  string
		curve []SurvivalPoint
	}{
		{name: "empty"},
		{name: "fewer than six rows", curve: []SurvivalPoint{{Duration: 0, Ratio: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := WriteSurvivalFunction(&output, test.curve, 1); err != nil {
				t.Fatalf("WriteSurvivalFunction() error = %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("WriteSurvivalFunction() = %q, want no output", output.String())
			}
		})
	}
}

func TestFloorDateTimeUsesUnixTickBoundaries(t *testing.T) {
	location := time.FixedZone("UTC+1", 60*60)
	input := time.Unix(1704067417, 750000000).In(location)
	tests := []struct {
		name     string
		tickSize float64
		wantUnix float64
	}{
		{name: "day", tickSize: 86400, wantUnix: 1704067200},
		{name: "non-calendar tick", tickSize: 1000, wantUnix: 1704067000},
		{name: "fractional tick", tickSize: 2.5, wantUnix: 1704067417.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FloorDateTime(input, test.tickSize)
			gotUnix := float64(got.Unix()) + float64(got.Nanosecond())/float64(time.Second)
			if math.Abs(gotUnix-test.wantUnix) > 1e-9 {
				t.Fatalf("FloorDateTime() Unix = %.9f, want %.9f", gotUnix, test.wantUnix)
			}
			if got.Location() != location {
				t.Fatalf("FloorDateTime() location = %v, want %v", got.Location(), location)
			}
		})
	}
}

func TestFloorDateTimeFloorsBeforeUnixEpoch(t *testing.T) {
	input := time.Unix(-2, 750000000)
	got := FloorDateTime(input, 2.5)
	gotUnix := float64(got.Unix()) + float64(got.Nanosecond())/float64(time.Second)
	if gotUnix != -2.5 {
		t.Fatalf("FloorDateTime() Unix = %.9f, want -2.5", gotUnix)
	}
}
