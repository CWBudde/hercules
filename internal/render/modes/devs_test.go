package modes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

type developerTimeSeriesErrorReader struct {
	readers.Reader

	err error
}

var errDeveloperTimeSeriesRead = errors.New("read developer time series")

func (reader developerTimeSeriesErrorReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, reader.err
}

func TestDeveloperTimeSeriesRenderersPropagateReadErrors(t *testing.T) {
	reader := developerTimeSeriesErrorReader{err: errDeveloperTimeSeriesRead}

	rendered, err := plotDeveloperTimeSeries(reader, filepath.Join(t.TempDir(), "devs.png"), Options{})
	if rendered || !errors.Is(err, errDeveloperTimeSeriesRead) {
		t.Fatalf("plotDeveloperTimeSeries() = (%v, %v), want (false, wrapped read error)", rendered, err)
	}

	rendered, err = renderActualOldVsNew(reader, filepath.Join(t.TempDir(), "old-vs-new.png"), graphics.Options{})
	if rendered || !errors.Is(err, errDeveloperTimeSeriesRead) {
		t.Fatalf("renderActualOldVsNew() = (%v, %v), want (false, wrapped read error)", rendered, err)
	}
}

func TestGenerateDevsPlot(t *testing.T) {
	// Create temporary directory for test outputs
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_devs.png")

	// Sample developer statistics for testing
	testDevStats := []readers.DeveloperStat{
		{
			Name:          "Alice",
			Commits:       50,
			LinesAdded:    1000,
			LinesRemoved:  200,
			LinesModified: 300,
			FilesTouched:  25,
			Languages:     map[string]int{"Go": 800, "Python": 200},
		},
		{
			Name:          "Bob",
			Commits:       30,
			LinesAdded:    600,
			LinesRemoved:  150,
			LinesModified: 200,
			FilesTouched:  15,
			Languages:     map[string]int{"Go": 400, "JavaScript": 200},
		},
		{
			Name:          "Charlie",
			Commits:       70,
			LinesAdded:    1200,
			LinesRemoved:  100,
			LinesModified: 400,
			FilesTouched:  35,
			Languages:     map[string]int{"Python": 700, "Go": 500},
		},
	}

	err := mockGenerateDevsPlot("test", testDevStats, outputPath)
	if err != nil {
		t.Errorf("generateDevsPlot() error = %v", err)
	}

	// Check if output file was created
	_, err = os.Stat(outputPath)
	if os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestGenerateDevsPlotEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_devs_empty.png")

	// Test with empty developer stats
	emptyDevStats := []readers.DeveloperStat{}

	err := mockGenerateDevsPlot("test_empty", emptyDevStats, outputPath)
	if err == nil {
		t.Error("Expected error for empty developer stats, but got nil")
	}
}

func TestGenerateDevsPlotSingleDeveloper(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_devs_single.png")

	singleDevStats := []readers.DeveloperStat{
		{
			Name:          "Solo Developer",
			Commits:       100,
			LinesAdded:    2000,
			LinesRemoved:  300,
			LinesModified: 500,
			FilesTouched:  50,
			Languages:     map[string]int{"Go": 1500, "YAML": 300, "Markdown": 200},
		},
	}

	err := mockGenerateDevsPlot("test_single", singleDevStats, outputPath)
	if err != nil {
		t.Errorf("generateDevsPlot() with single developer error = %v", err)
	}

	_, err = os.Stat(outputPath)
	if os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestBuildDeveloperSeriesRowsUsesAllTicks(t *testing.T) {
	path := filepath.Join("..", "testdata", "example_data", "hercules_devs.yaml")
	file, err := os.Open(path) // #nosec G304 - test fixture path is fixed above.
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	reader := &readers.YamlReader{}
	err = reader.Read(file)
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}

	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		t.Fatalf("GetDeveloperTimeSeriesData() unexpected error: %v", err)
	}

	rows, _ := buildDeveloperSeriesRows(timeSeries, 1734315181, 1754512384, 20)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Commits != 8 {
		t.Fatalf("commits = %d, want 8", row.Commits)
	}
	if row.LinesAdded != 9330 {
		t.Fatalf("lines added = %d, want 9330", row.LinesAdded)
	}
	if row.LinesRemove != 1663 {
		t.Fatalf("lines removed = %d, want 1663", row.LinesRemove)
	}
	if row.LinesChange != 957 {
		t.Fatalf("lines changed = %d, want 957", row.LinesChange)
	}
}

func TestSortDevelopersByCommits(t *testing.T) {
	devStats := []readers.DeveloperStat{
		{Name: "Alice", Commits: 30},
		{Name: "Bob", Commits: 50},
		{Name: "Charlie", Commits: 20},
	}

	sorted := sortDevelopersByCommits(devStats)

	// Should be sorted in descending order by commits
	expected := []string{"Bob", "Alice", "Charlie"}
	for i, dev := range sorted {
		if dev.Name != expected[i] {
			t.Errorf("Expected developer %s at position %d, got %s", expected[i], i, dev.Name)
		}
	}
}

func TestCalculateLanguageDistribution(t *testing.T) {
	devStats := []readers.DeveloperStat{
		{
			Name: "Alice",
			Languages: map[string]int{
				"Go":     800,
				"Python": 200,
			},
		},
		{
			Name: "Bob",
			Languages: map[string]int{
				"Go":         400,
				"JavaScript": 300,
				"Python":     100,
			},
		},
	}

	langDist := calculateLanguageDistribution(devStats)

	expectedLangs := map[string]int{
		"Go":         1200,
		"Python":     300,
		"JavaScript": 300,
	}

	for lang, expectedLines := range expectedLangs {
		if actualLines, exists := langDist[lang]; !exists {
			t.Errorf("Expected language %s not found in distribution", lang)
		} else if actualLines != expectedLines {
			t.Errorf("Expected %d lines for %s, got %d", expectedLines, lang, actualLines)
		}
	}
}

func TestCalculateTeamMetrics(t *testing.T) {
	devStats := []readers.DeveloperStat{
		{
			Name:         "Alice",
			Commits:      50,
			LinesAdded:   1000,
			LinesRemoved: 200,
			FilesTouched: 25,
		},
		{
			Name:         "Bob",
			Commits:      30,
			LinesAdded:   600,
			LinesRemoved: 150,
			FilesTouched: 15,
		},
	}

	metrics := calculateTeamMetrics(devStats)

	expectedTotalCommits := 80
	expectedTotalLinesAdded := 1600
	expectedTotalLinesRemoved := 350
	expectedTotalFiles := 40

	if metrics.TotalCommits != expectedTotalCommits {
		t.Errorf("Expected total commits %d, got %d", expectedTotalCommits, metrics.TotalCommits)
	}

	if metrics.TotalLinesAdded != expectedTotalLinesAdded {
		t.Errorf("Expected total lines added %d, got %d", expectedTotalLinesAdded, metrics.TotalLinesAdded)
	}

	if metrics.TotalLinesRemoved != expectedTotalLinesRemoved {
		t.Errorf("Expected total lines removed %d, got %d", expectedTotalLinesRemoved, metrics.TotalLinesRemoved)
	}

	if metrics.TotalFilesTouched != expectedTotalFiles {
		t.Errorf("Expected total files touched %d, got %d", expectedTotalFiles, metrics.TotalFilesTouched)
	}

	if metrics.AverageCommitsPerDeveloper != float64(expectedTotalCommits)/2 {
		t.Errorf("Expected average commits per developer %f, got %f", float64(expectedTotalCommits)/2, metrics.AverageCommitsPerDeveloper)
	}
}

// Helper type for team metrics testing.
type TeamMetrics struct {
	TotalCommits               int
	TotalLinesAdded            int
	TotalLinesRemoved          int
	TotalFilesTouched          int
	AverageCommitsPerDeveloper float64
	AverageLinesPerDeveloper   float64
}

// Mock function for calculating team metrics.
func calculateTeamMetrics(devStats []readers.DeveloperStat) TeamMetrics {
	var metrics TeamMetrics

	for _, dev := range devStats {
		metrics.TotalCommits += dev.Commits
		metrics.TotalLinesAdded += dev.LinesAdded
		metrics.TotalLinesRemoved += dev.LinesRemoved
		metrics.TotalFilesTouched += dev.FilesTouched
	}

	if len(devStats) > 0 {
		metrics.AverageCommitsPerDeveloper = float64(metrics.TotalCommits) / float64(len(devStats))
		metrics.AverageLinesPerDeveloper = float64(metrics.TotalLinesAdded) / float64(len(devStats))
	}

	return metrics
}

// Mock function for sorting developers.
func sortDevelopersByCommits(devStats []readers.DeveloperStat) []readers.DeveloperStat {
	sorted := make([]readers.DeveloperStat, len(devStats))
	copy(sorted, devStats)

	// Simple bubble sort for testing
	for i := range len(sorted) - 1 {
		for j := range len(sorted) - i - 1 {
			if sorted[j].Commits < sorted[j+1].Commits {
				sorted[j], sorted[j+1] = sorted[j+1], sorted[j]
			}
		}
	}

	return sorted
}

// Mock function for calculating language distribution.
func calculateLanguageDistribution(devStats []readers.DeveloperStat) map[string]int {
	langDist := make(map[string]int)

	for _, dev := range devStats {
		for lang, lines := range dev.Languages {
			langDist[lang] += lines
		}
	}

	return langDist
}

// Mock function for testing.
func mockGenerateDevsPlot(name string, devStats []readers.DeveloperStat, outputPath string) error {
	// Simple mock implementation
	if len(devStats) == 0 {
		return errors.New("empty developer stats")
	}

	// Create a simple test file to simulate chart generation
	content := fmt.Sprintf("Mock developer chart for %s with %d developers", name, len(devStats))
	return os.WriteFile(outputPath, []byte(content), 0o600)
}
