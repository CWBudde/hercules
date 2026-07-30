package modes

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// MockCouplesReader provides test data for couples-people testing.
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

func (r *MockCouplesReader) GetFileCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, nil
}

func (r *MockCouplesReader) GetShotnessCooccurrence() ([]string, readers.SparseMatrix, error) {
	return nil, readers.SparseMatrix{}, nil
}

func (r *MockCouplesReader) GetShotnessRecords() ([]readers.ShotnessRecord, error) { return nil, nil }

func (r *MockCouplesReader) GetDeveloperStats() ([]readers.DeveloperStat, error) { return nil, nil }

func (r *MockCouplesReader) GetLanguageStats() ([]readers.LanguageStat, error) { return nil, nil }

func (r *MockCouplesReader) GetRuntimeStats() (map[string]float64, error) { return nil, nil }

func (r *MockCouplesReader) GetDeveloperTimeSeriesData() (*readers.DeveloperTimeSeriesData, error) {
	return nil, nil
}

// GetPeopleCooccurrence returns test coupling data mimicking real hercules output.
func (r *MockCouplesReader) GetPeopleCooccurrence() ([]string, readers.SparseMatrix, error) {
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

	return people, readers.SparseMatrixFromDense(matrix), nil
}

func TestCouplesPeopleEmbeddings(t *testing.T) {
	// Create temporary output directory
	tempDir := t.TempDir()

	// Create mock reader
	reader := &MockCouplesReader{}

	// Test the couples-people function
	err := CouplesPeopleWithOptions(reader, tempDir, Options{Quiet: true})
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
	// Create temporary output directory
	tempDir := t.TempDir()

	// Create mock reader
	reader := &MockCouplesReader{}

	// Test the couples-people function
	err := CouplesPeopleWithOptions(reader, tempDir, Options{Quiet: true, DisableProjector: true})
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

func TestSparseCouplingOutlierThreshold(t *testing.T) {
	entries := make([]readers.SparseEntry, 101)
	for index := range entries {
		value := index + 1
		if index == len(entries)-1 {
			value = 1_000
		}
		entries[index] = readers.SparseEntry{
			Row: index, Column: index, Value: value,
		}
	}
	matrix, err := readers.NewSparseMatrix(101, 101, entries)
	require.NoError(t, err)
	require.Equal(t, 100, sparseCouplingOutlierThreshold(matrix))
	require.Equal(t, 100.0, cappedCouplingValue(1_000, 100))
}

func TestSparseEmbeddingWriterNormalizesRows(t *testing.T) {
	index := []string{"alice", "bob", "charlie"}
	dense := [][]int{
		{50, 15, 8},
		{15, 75, 22},
		{8, 22, 30},
	}
	sparse := readers.SparseMatrixFromDense(dense)
	threshold := sparseCouplingOutlierThreshold(sparse)

	sparseOutput := t.TempDir()
	require.NoError(t, writeSparseEmbeddings(
		"people", sparseOutput, index, sparse, threshold,
	))

	data, err := os.ReadFile(filepath.Join(sparseOutput, "people_vectors.tsv"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, len(index))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		require.Len(t, fields, len(index))
		normSquared := 0.0
		for _, field := range fields {
			value, err := strconv.ParseFloat(field, 64)
			require.NoError(t, err)
			normSquared += value * value
		}
		require.InDelta(t, 1.0, math.Sqrt(normSquared), 0.00001)
	}
}
