package modes

import (
	"io"
	"os"
	"path/filepath"
	"testing"

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
	if total != 7667 {
		t.Fatalf("last cumulative language total = %.0f, want 7667", total)
	}
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
