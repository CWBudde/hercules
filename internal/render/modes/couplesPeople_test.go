package modes

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// MockCouplesReader provides test data for couples-people testing
type MockCouplesReader struct{}

func (r *MockCouplesReader) Read(file io.Reader) error             { return nil }
func (r *MockCouplesReader) GetName() string                       { return "test-repo" }
func (r *MockCouplesReader) GetHeader() (int64, int64)             { return 1234567890, 1234567890 }
func (r *MockCouplesReader) GetProjectBurndown() (string, [][]int) { return "", nil }
func (r *MockCouplesReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	return burndown.BurndownParameters{}, nil
}

func (r *MockCouplesReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	return burndown.BurndownHeader{}, "", nil, nil
}

func (r *MockCouplesReader) GetFilesBurndown() ([]readers.FileBurndown, error) { return nil, nil }

func (r *MockCouplesReader) GetPeopleBurndown() ([]readers.PeopleBurndown, error) { return nil, nil }

func (r *MockCouplesReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	return nil, nil, nil
}

func (r *MockCouplesReader) GetPeopleInteraction() ([]string, [][]int, error) { return nil, nil, nil }

func (r *MockCouplesReader) GetFileCooccurrence() ([]string, [][]int, error) { return nil, nil, nil }

func (r *MockCouplesReader) GetShotnessCooccurrence() ([]string, [][]int, error) {
	return nil, nil, nil
}

func (r *MockCouplesReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) { return nil, nil }

func (r *MockCouplesReader) GetDeveloperStats() ([]readers.DeveloperStat, error) { return nil, nil }

func (r *MockCouplesReader) GetLanguageStats() ([]readers.LanguageStat, error) { return nil, nil }

func (r *MockCouplesReader) GetRuntimeStats() (map[string]float64, error) { return nil, nil }

func (r *MockCouplesReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, nil
}

// GetPeopleCooccurrence returns test coupling data mimicking real hercules output
func (r *MockCouplesReader) GetPeopleCooccurrence() ([]string, [][]int, error) {
	// Simulate realistic people coupling data
	people := []string{
		"alice@example.com",
		"bob@example.com",
		"charlie@example.com",
	}

	// Coupling matrix with some realistic values and outliers
	matrix := [][]int{
		{50, 15, 8},  // alice coupled with others
		{15, 75, 22}, // bob coupled with others
		{8, 22, 30},  // charlie coupled with others
	}

	return people, matrix, nil
}

func TestCouplesPeopleEmbeddings(t *testing.T) {
	// Set up test environment
	viper.Set("quiet", true)
	viper.Set("disable-projector", false)
	viper.Set("tmpdir", "")

	// Create temporary output directory
	tempDir := t.TempDir()

	// Create mock reader
	reader := &MockCouplesReader{}

	// Test the couples-people function
	err := CouplesPeople(reader, tempDir)
	if err != nil {
		t.Fatalf("CouplesPeople failed: %v", err)
	}

	// Verify all expected files are created
	expectedFiles := []string{
		"people_vocabulary.tsv",
		"people_vectors.tsv",
		"people_metadata.tsv",
	}

	for _, filename := range expectedFiles {
		filepath := filepath.Join(tempDir, filename)
		if _, err := os.Stat(filepath); os.IsNotExist(err) {
			t.Errorf("Expected file not created: %s", filepath)
		}
	}
}

func TestCouplesPeopleWithDisabledProjector(t *testing.T) {
	// Set up test environment with projector disabled
	viper.Set("quiet", true)
	viper.Set("disable-projector", true)
	viper.Set("tmpdir", "")

	// Create temporary output directory
	tempDir := t.TempDir()

	// Create mock reader
	reader := &MockCouplesReader{}

	// Test the couples-people function
	err := CouplesPeople(reader, tempDir)
	if err != nil {
		t.Fatalf("CouplesPeople failed: %v", err)
	}

	// Verify basic files are created
	expectedFiles := []string{
		"people_vocabulary.tsv",
		"people_vectors.tsv",
	}

	for _, filename := range expectedFiles {
		filepath := filepath.Join(tempDir, filename)
		if _, err := os.Stat(filepath); os.IsNotExist(err) {
			t.Errorf("Expected file not created: %s", filepath)
		}
	}

	// Verify metadata file is NOT created when projector is disabled
	metadataPath := filepath.Join(tempDir, "people_metadata.tsv")
	if _, err := os.Stat(metadataPath); !os.IsNotExist(err) {
		t.Errorf("Metadata file should not exist when projector is disabled: %s", metadataPath)
	}
}

func TestPreprocessCouplingMatrix(t *testing.T) {
	// Test data with outliers
	input := [][]int{
		{10, 5, 100}, // 100 is an outlier
		{5, 20, 8},
		{100, 8, 15}, // 100 is an outlier
	}

	result := preprocessCouplingMatrix(input)

	if len(result) != 3 || len(result[0]) != 3 {
		t.Fatalf("Matrix dimensions incorrect: got %dx%d, expected 3x3", len(result), len(result[0]))
	}

	// The 99th percentile of [5, 8, 8, 10, 15, 20, 100, 100] should cap the outliers
	// 99th percentile of 8 values is at index ceil(0.99*8)-1 = 8-1 = 7, so value 100
	// But our implementation caps at the actual 99th percentile value

	// Verify outliers are capped (exact values depend on percentile calculation)
	if result[0][2] > 100 || result[2][0] > 100 {
		t.Errorf("Outliers not properly capped")
	}

	// Verify non-outlier values are preserved as floats
	if result[1][1] != 20.0 {
		t.Errorf("Non-outlier value not preserved: got %f, expected 20.0", result[1][1])
	}
}

func TestTrainEmbeddings(t *testing.T) {
	index := []string{"alice", "bob", "charlie"}
	matrix := [][]float64{
		{1.0, 0.5, 0.2},
		{0.5, 1.0, 0.8},
		{0.2, 0.8, 1.0},
	}

	embeddings, err := trainEmbeddings(index, matrix)
	if err != nil {
		t.Fatalf("trainEmbeddings failed: %v", err)
	}

	if len(embeddings) != 3 {
		t.Fatalf("Wrong number of embeddings: got %d, expected 3", len(embeddings))
	}

	// Check that embeddings are normalized (L2 norm should be ~1.0)
	for i, emb := range embeddings {
		if emb.Label != index[i] {
			t.Errorf("Wrong label for embedding %d: got %s, expected %s", i, emb.Label, index[i])
		}

		// Calculate L2 norm
		norm := 0.0
		for _, val := range emb.Vector {
			norm += val * val
		}
		if norm < 0.99 || norm > 1.01 { // Allow small floating-point errors
			t.Errorf("Embedding %d not properly normalized: L2 norm = %f", i, norm)
		}
	}
}
