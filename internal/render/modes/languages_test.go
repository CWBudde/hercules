package modes

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// Mock reader for testing the languages mode.
type MockLanguageReader struct {
	languageStats []readers.LanguageStat
}

func (m *MockLanguageReader) Read(file io.Reader) error                         { return nil }
func (m *MockLanguageReader) GetName() string                                   { return "mock-repo" }
func (m *MockLanguageReader) GetHeader() (int64, int64)                         { return 0, 0 }
func (m *MockLanguageReader) GetProjectBurndown() (string, [][]int)             { return "", nil }
func (m *MockLanguageReader) GetFilesBurndown() ([]readers.FileBurndown, error) { return nil, nil }
func (m *MockLanguageReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) {
	return nil, nil
}

func (m *MockLanguageReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	return nil, nil, nil
}

func (m *MockLanguageReader) GetPeopleInteraction() ([]string, [][]int, error) { return nil, nil, nil }

func (m *MockLanguageReader) GetFileCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, nil
}

func (m *MockLanguageReader) GetPeopleCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, nil
}

func (m *MockLanguageReader) GetShotnessCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, nil
}

func (m *MockLanguageReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) { return nil, nil }

func (m *MockLanguageReader) GetDeveloperStats() ([]readers.DeveloperStat, error) { return nil, nil }

func (m *MockLanguageReader) GetRuntimeStats() (map[string]float64, error) { return nil, nil }

func (m *MockLanguageReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	return burndown.BurndownParameters{}, nil
}

func (m *MockLanguageReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	return burndown.BurndownHeader{}, "", nil, nil
}

func (m *MockLanguageReader) GetLanguageStats() ([]readers.LanguageStat, error) {
	return m.languageStats, nil
}

func (m *MockLanguageReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, nil
}

func TestLanguages(t *testing.T) {
	// Create temporary directory for test outputs
	tmpDir := t.TempDir()

	// Sample language statistics for testing
	testLangStats := []readers.LanguageStat{
		{Language: "Go", Lines: 15000},
		{Language: "Python", Lines: 8000},
		{Language: "JavaScript", Lines: 5000},
		{Language: "HTML", Lines: 3000},
		{Language: "CSS", Lines: 2000},
	}

	mockReader := &MockLanguageReader{
		languageStats: testLangStats,
	}

	err := Languages(mockReader, tmpDir)
	if err != nil {
		t.Errorf("Languages() error = %v", err)
	}

	// Check if output files were created
	pngFile := filepath.Join(tmpDir, "languages.png")
	_, err = os.Stat(pngFile)
	if os.IsNotExist(err) {
		t.Errorf("PNG output file was not created: %s", pngFile)
	}

	svgFile := filepath.Join(tmpDir, "languages.svg")
	_, err = os.Stat(svgFile)
	if os.IsNotExist(err) {
		t.Errorf("SVG output file was not created: %s", svgFile)
	}
}

func TestLanguagesEmptyData(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with empty language stats
	mockReader := &MockLanguageReader{
		languageStats: []readers.LanguageStat{},
	}

	err := Languages(mockReader, tmpDir)
	if err == nil {
		t.Error("Expected error for empty language stats, but got nil")
	}

	expectedErrorMessage := "no language statistics found in the data"
	if err.Error() != expectedErrorMessage+" - the input file may not contain language analysis results" {
		t.Errorf("Expected error message containing '%s', got: %v", expectedErrorMessage, err)
	}
}

func TestLanguagesSingleLanguage(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with single language
	testLangStats := []readers.LanguageStat{
		{Language: "Go", Lines: 25000},
	}

	mockReader := &MockLanguageReader{
		languageStats: testLangStats,
	}

	err := Languages(mockReader, tmpDir)
	if err != nil {
		t.Errorf("Languages() with single language error = %v", err)
	}

	// Check if output files were created
	pngFile := filepath.Join(tmpDir, "languages.png")
	_, err = os.Stat(pngFile)
	if os.IsNotExist(err) {
		t.Errorf("PNG output file was not created: %s", pngFile)
	}
}

func TestBuildLanguageEvolutionIncludesLastTickAcrossDST(t *testing.T) {
	path := filepath.Join("..", "testdata", "example_data", "hercules_devs.yaml")
	file, err := os.Open(path) // #nosec G304 - test fixture path is fixed above.
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	reader := &readers.YamlReader{}
	err = reader.Read(file)
	if err != nil {
		t.Fatalf("Read() unexpected error = %v", err)
	}

	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		t.Fatalf("GetDeveloperTimeSeriesData() unexpected error = %v", err)
	}

	data, err := buildLanguageEvolution(timeSeries, 1734315181, 1754512384, "raw")
	if err != nil {
		t.Fatalf("buildLanguageEvolution() unexpected error = %v", err)
	}
	if len(data.Matrix) == 0 {
		t.Fatal("expected language matrix rows")
	}

	last := data.Matrix[len(data.Matrix)-1]
	total := 0.0
	for _, value := range last {
		total += value
	}
	// Unchanged by the switch away from carry-forward gap filling, and that is
	// verifiable rather than lucky: this fixture holds activity on exactly two
	// days, one of which is the last day of the range, so the final row was
	// always written directly and never patched. It also has no language with a
	// zero or negative net total and no unclassified ("") bucket, so neither the
	// clamp nor the unclassified accounting can move it. The synthetic tests
	// below cover those paths.
	if total != 7667 {
		t.Fatalf("last cumulative language total = %.0f, want 7667", total)
	}

	if data.Total != 7667 || data.Peak != 7667 {
		t.Errorf("Total/Peak = %.0f/%.0f, want 7667/7667 for a monotonically growing corpus",
			data.Total, data.Peak)
	}

	if data.ClampedAway != 0 {
		t.Errorf("ClampedAway = %.0f, want 0: no language in this fixture goes net-negative", data.ClampedAway)
	}
}

// testLanguageRust is an arbitrary second language name used by the synthetic
// evolution tests below.
const testLanguageRust = "Rust"

// languageDays builds a DeveloperTimeSeriesData from {dayIndex: {language: [added, removed, changed]}}.
func languageDays(entries map[int]map[string][]int) *readers.DeveloperTimeSeriesData {
	days := make(map[int]map[int]readers.DevDay, len(entries))
	for day, languages := range entries {
		days[day] = map[int]readers.DevDay{0: {Languages: languages}}
	}

	return &readers.DeveloperTimeSeriesData{People: []string{"dev"}, Days: days}
}

// languageWindow is a fixed five-day range (2024-01-01..2024-01-05), taken at
// midday so that neither a DST boundary nor the local UTC offset can shift a
// date into the neighbouring calendar day.
func languageWindow() (int64, int64) {
	start := time.Date(2024, time.January, 1, 12, 0, 0, 0, time.UTC)

	return start.Unix(), start.AddDate(0, 0, 4).Unix()
}

// The headline used to be maximumLanguageTotal, so a corpus that shrank kept
// advertising its historic high-water mark forever.
func TestBuildLanguageEvolutionTotalIsFinalNotPeak(t *testing.T) {
	start, end := languageWindow()
	timeSeries := languageDays(map[int]map[string][]int{
		0: {"Go": {1000, 0, 0}},
		2: {"Go": {0, 400, 0}},
	})

	data, err := buildLanguageEvolution(timeSeries, start, end, "raw")
	if err != nil {
		t.Fatalf("buildLanguageEvolution() unexpected error = %v", err)
	}

	if data.Total != 600 {
		t.Errorf("Total = %.0f, want 600 (the value at the last sample)", data.Total)
	}

	if data.Peak != 1000 {
		t.Errorf("Peak = %.0f, want 1000 (the high-water mark, for the y-axis)", data.Peak)
	}

	if got := sumFloat64(data.Matrix[len(data.Matrix)-1]); got != data.Total {
		t.Errorf("last drawn row = %.0f but Total = %.0f; they must agree when nothing is clamped",
			got, data.Total)
	}
}

// carryLanguageTotalsForward copied the previous row wherever a value was
// exactly zero, so a language deleted down to nothing kept its last positive
// value on the chart for the rest of history.
func TestBuildLanguageEvolutionDoesNotResurrectDeletedLanguages(t *testing.T) {
	start, end := languageWindow()
	timeSeries := languageDays(map[int]map[string][]int{
		0: {"Go": {500, 0, 0}},
		2: {"Go": {0, 500, 0}},
	})

	data, err := buildLanguageEvolution(timeSeries, start, end, "raw")
	if err != nil {
		t.Fatalf("buildLanguageEvolution() unexpected error = %v", err)
	}

	want := []float64{500, 500, 0, 0, 0}
	if len(data.Matrix) != len(want) {
		t.Fatalf("got %d rows, want %d", len(data.Matrix), len(want))
	}

	for day, expected := range want {
		if got := sumFloat64(data.Matrix[day]); got != expected {
			// Day 1 checks the other half: a day with no activity must still
			// carry the running cumulative rather than reading as zero.
			t.Errorf("day %d total = %.0f, want %.0f", day, got, expected)
		}
	}
}

// Periodic resampling used to stop at the last completed period, dropping
// everything after it from both the plot and the headline. On the default
// yearly resample that silently omitted the entire current year — on the
// org-wide corpus it reported 2,881,056 lines as of 2025-12-31 instead of
// ~9,3M, hiding 69 % of the data.
func TestBuildLanguageEvolutionReachesEndOfDataOnPeriodicResample(t *testing.T) {
	// 2024-01-01 .. 2025-06-30: a year-end boundary falls inside the range, and
	// the data continues for six months past it.
	start := time.Date(2024, time.January, 1, 12, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 5, 29)

	timeSeries := languageDays(map[int]map[string][]int{
		0: {"Go": {1000, 0, 0}},
		// Day 500 lands in 2025, well after 2024-12-31.
		500: {"Go": {700, 0, 0}},
	})

	for _, resample := range []string{resampleYear, resampleMonth, resampleWeek} {
		t.Run(resample, func(t *testing.T) {
			data, err := buildLanguageEvolution(timeSeries, start.Unix(), end.Unix(), resample)
			if err != nil {
				t.Fatalf("buildLanguageEvolution() unexpected error = %v", err)
			}

			if data.Total != 1700 {
				t.Errorf("Total = %.0f, want 1700: the last sample must reach the end of the data",
					data.Total)
			}

			// Compare calendar dates as text: sample dates are built in the
			// local zone (timeSeriesCalendarRange goes through time.Unix)
			// while `end` here is UTC, so comparing the instants would trip
			// over the offset rather than the date.
			got := data.Dates[len(data.Dates)-1].Format(time.DateOnly)
			if want := end.Format(time.DateOnly); got < want {
				t.Errorf("last sample = %s, want on or after %s", got, want)
			}
		})
	}
}

func TestBuildLanguageEvolutionReportsClampedNegatives(t *testing.T) {
	start, end := languageWindow()
	timeSeries := languageDays(map[int]map[string][]int{
		0: {"Go": {100, 0, 0}, testLanguageRust: {0, 300, 0}},
	})

	data, err := buildLanguageEvolution(timeSeries, start, end, "raw")
	if err != nil {
		t.Fatalf("buildLanguageEvolution() unexpected error = %v", err)
	}

	// The drawn band floors at zero, because a stacked area cannot render a
	// negative segment.
	if got := sumFloat64(data.Matrix[len(data.Matrix)-1]); got != 100 {
		t.Errorf("last drawn row = %.0f, want 100 (Rust floored to zero)", got)
	}

	// The headline must not inherit that floor.
	if data.Total != -200 {
		t.Errorf("Total = %.0f, want -200 (100 + -300, unclamped)", data.Total)
	}

	if data.ClampedAway != 300 {
		t.Errorf("ClampedAway = %.0f, want 300", data.ClampedAway)
	}
}

// The top-N selection ranked on added+removed+changed while the bands plotted
// added-removed, so a heavily-churned language could take a slot from a
// steadier, substantially larger one.
func TestNetLanguageTotalsRanksByNetNotChurn(t *testing.T) {
	totals, _ := netLanguageTotals(languageDays(map[int]map[string][]int{
		0: {
			"Churny": {1000, 1000, 5000},
			"Steady": {800, 0, 0},
		},
	}).Days)

	if totals["Churny"] != 0 {
		t.Errorf("Churny net = %d, want 0: it added and removed the same amount", totals["Churny"])
	}

	if totals["Steady"] != 800 {
		t.Errorf("Steady net = %d, want 800", totals["Steady"])
	}

	if totals["Steady"] <= totals["Churny"] {
		t.Errorf("Steady (%d) must outrank Churny (%d)", totals["Steady"], totals["Churny"])
	}
}

func TestNetLanguageTotalsReportsUnclassifiedShare(t *testing.T) {
	totals, share := netLanguageTotals(languageDays(map[int]map[string][]int{
		0: {
			"":   {100, 0, 0},
			"Go": {300, 0, 0},
		},
	}).Days)

	if _, ok := totals[""]; ok {
		t.Error(`the "" bucket must not become a language band`)
	}

	if share != 0.25 {
		t.Errorf("unclassified share = %v, want 0.25 (100 of 400 changed lines)", share)
	}
}

func TestFormatFloatWithCommas(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{9298469, "9,298,469"},
		// Line counts are whole numbers; this used to render as "1,234.0".
		{1234.4, "1,234"},
		{1234.6, "1,235"},
		{-1234, "-1,234"},
		// Must not produce "-0".
		{-0.4, "0"},
	}

	for _, tt := range tests {
		if got := formatFloatWithCommas(tt.value); got != tt.want {
			t.Errorf("formatFloatWithCommas(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestLanguageEvolutionSubtitle(t *testing.T) {
	date := time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)

	t.Run("clean corpus stays short", func(t *testing.T) {
		got := languageEvolutionSubtitle(languageEvolution{
			Languages: []string{"Go", testLanguageRust},
			Dates:     []time.Time{date},
			Total:     1000,
			Peak:      1000,
		})

		want := "Total 1,000 lines at 2026-08-05 · 2 languages"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("reports every caveat that applies", func(t *testing.T) {
		got := languageEvolutionSubtitle(languageEvolution{
			Languages:         []string{"Go", testLanguageRust, otherLanguageLabel},
			Dates:             []time.Time{date},
			Total:             1000,
			Peak:              1500,
			ClampedAway:       25,
			UnclassifiedShare: 0.238,
		})

		want := "Total 1,000 lines at 2026-08-05 · peak 1,500 · top 2 languages + Other · " +
			"24% of changed lines unclassified · 25 lines net-negative, not shown"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestLanguagesSorting(t *testing.T) {
	// This test validates that languages are properly sorted by line count
	tmpDir := t.TempDir()

	// Unsorted language stats
	testLangStats := []readers.LanguageStat{
		{Language: "CSS", Lines: 1000},
		{Language: "Go", Lines: 15000},
		{Language: "Python", Lines: 8000},
		{Language: "HTML", Lines: 2000},
		{Language: "JavaScript", Lines: 5000},
	}

	mockReader := &MockLanguageReader{
		languageStats: testLangStats,
	}

	err := Languages(mockReader, tmpDir)
	if err != nil {
		t.Errorf("Languages() sorting test error = %v", err)
	}

	// The function should handle sorting internally, so we just verify it completes successfully
	pngFile := filepath.Join(tmpDir, "languages.png")
	_, err = os.Stat(pngFile)
	if os.IsNotExist(err) {
		t.Errorf("PNG output file was not created: %s", pngFile)
	}
}
