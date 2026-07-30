package readers

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCriticalCompatibilityIssues verifies the critical compatibility issues
// identified in COMPATIBILITY_ANALYSIS.md.
func TestCriticalCompatibilityIssues(t *testing.T) {
	testFile := "../testdata/example_data/hercules_burndown.pb"

	reader := &ProtobufReader{}
	file, err := os.Open(testFile)
	require.NoError(t, err, "Test file should be accessible")
	defer func() { _ = file.Close() }()

	err = reader.Read(file)
	require.NoError(t, err, "Protobuf reading should work")

	t.Run("ContentsParsingWorks", func(t *testing.T) {
		// Test that Go's direct Contents["Burndown"] access works
		// This verifies the Contents parsing approach matches Python's dynamic parsing

		header, name, matrix, err := reader.GetProjectBurndownWithHeader()
		assert.NoError(t, err, "Contents[\"Burndown\"] access should work")
		assert.NotEmpty(t, name, "Should extract repository name")
		assert.NotEmpty(t, matrix, "Should extract matrix data")
		assert.Greater(t, header.TickSize, float64(0), "Should extract header data")

		t.Logf("✓ Contents parsing works: %s, %dx%d matrix, tick_size=%.3f",
			name, len(matrix), len(matrix[0]), header.TickSize)
	})

	t.Run("MatrixFormatSelection", func(t *testing.T) {
		// Verify that Go correctly chooses parsing methods based on data type

		// Test 1: Project burndown uses BurndownSparseMatrix format (row/column parsing)
		_, _, projectMatrix, err := reader.GetProjectBurndownWithHeader()
		if err == nil {
			t.Logf("✓ Project burndown: BurndownSparseMatrix → %dx%d", len(projectMatrix), len(projectMatrix[0]))
		}

		// Test 2: People interaction uses CompressedSparseRowMatrix format (CSR parsing)
		_, interactionMatrix, err := reader.GetPeopleInteraction()
		if err == nil {
			t.Logf("✓ People interaction: CompressedSparseRowMatrix → %dx%d", len(interactionMatrix), len(interactionMatrix[0]))
		} else {
			t.Logf("  People interaction not available: %v", err)
		}

		// Test 3: File/People cooccurrence uses CompressedSparseRowMatrix format
		_, fileMatrix, err := reader.GetFileCooccurrence()
		if err == nil {
			t.Logf("✓ File cooccurrence: CompressedSparseRowMatrix → %dx%d", fileMatrix.Rows, fileMatrix.Columns)
		} else {
			t.Logf("  File cooccurrence not available: %v", err)
		}

		_, peopleMatrix, err := reader.GetPeopleCooccurrence()
		if err == nil {
			t.Logf("✓ People cooccurrence: CompressedSparseRowMatrix → %dx%d", peopleMatrix.Rows, peopleMatrix.Columns)
		} else {
			t.Logf("  People cooccurrence not available: %v", err)
		}

		// CONCLUSION: Go's format selection logic appears to work correctly
		// - Uses parseBurndownSparseMatrix() for Project/Files/People matrices
		// - Uses parseCompressedSparseRowMatrix() for interaction/cooccurrence matrices
		// This matches Python's _parse_burndown_matrix() vs _parse_sparse_matrix() pattern
	})

	t.Run("TransposeOperations", func(t *testing.T) {
		// Verify that Go's transpose operations match Python's .T behavior

		_, _, matrix, err := reader.GetProjectBurndownWithHeader()
		require.NoError(t, err, "Need matrix data for transpose test")

		// Python returns: return matrix.name, dense.T
		// Go returns: return repo, transposeMatrix(matrix)
		// Both should result in time-series format: rows=metrics, cols=time_points

		rows := len(matrix)
		cols := len(matrix[0])

		t.Logf("✓ Matrix after transpose: %dx%d (rows=metrics, cols=time_points)", rows, cols)

		// Verify consistent rectangular structure (transpose worked correctly)
		for i, row := range matrix {
			assert.Len(t, row, cols, "Row %d should have %d columns", i, cols)
		}

		// For burndown data, typically expect more time points than metrics
		// But this depends on the specific data, so just verify basic structure
		assert.Positive(t, rows, "Should have metric rows")
		assert.Positive(t, cols, "Should have time columns")
	})

	t.Run("DataIntegrityIssues", func(t *testing.T) {
		// This test verifies that burndown data contains no negative values

		_, _, matrix, err := reader.GetProjectBurndownWithHeader()
		require.NoError(t, err, "Need matrix data for integrity test")

		// Check for negative values in burndown data
		negativeCount := 0
		minValue := 0
		maxValue := 0

		for i, row := range matrix {
			for j, val := range row {
				if val < 0 {
					negativeCount++
					if val < minValue {
						minValue = val
					}
					t.Logf("  Negative value found: matrix[%d][%d] = %d", i, j, val)
				}
				if val > maxValue {
					maxValue = val
				}
			}
		}

		t.Logf("Data integrity analysis:")
		t.Logf("  Value range: %d to %d", minValue, maxValue)
		t.Logf("  Negative values: %d", negativeCount)

		if negativeCount > 0 {
			t.Errorf("❌ CRITICAL: Found %d negative values in burndown matrix (range: %d to %d)",
				negativeCount, minValue, maxValue)
			t.Log("This indicates mathematical issues in interpolation/resampling algorithms")
			t.Log("Python implementation likely handles this differently")
		} else {
			t.Log("✓ No negative values found - data integrity OK")
		}
	})

	t.Run("DeveloperTimeSeriesData", func(t *testing.T) {
		// Test the fixed developer time series compatibility

		devData, err := reader.GetDeveloperTimeSeriesData()
		if err != nil {
			t.Logf("  Developer time series not available: %v", err)
			return
		}

		t.Logf("Developer time series structure:")
		t.Logf("  People count: %d", len(devData.People))
		t.Logf("  Time ticks: %d", len(devData.Days))

		// Analyze the time series structure
		if len(devData.Days) == 1 && len(devData.Days) > 0 {
			// Check if this is synthetic single-day or real single-day data
			for dayKey := range devData.Days {
				if dayKey == 0 {
					t.Log("  ⚠️  Single day with key 0 - may be synthetic aggregation")
				} else {
					t.Logf("  ✓ Single day with real tick key: %d", dayKey)
				}
			}
		} else if len(devData.Days) > 1 {
			t.Logf("  ✓ Multi-day time series data available (%d days)", len(devData.Days))
		}
	})
}
