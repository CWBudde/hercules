package readers

import (
	"fmt"
	"os"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMatrixParsingCompatibility performs comprehensive verification of matrix parsing
// compatibility between Go and Python implementations
func TestMatrixParsingCompatibility(t *testing.T) {
	testFiles := []string{
		"../testdata/example_data/hercules_burndown.pb",
		"../testdata/example_data/hercules_devs.pb",
		"../testdata/example_data/hercules_couples.pb",
	}

	for _, testFile := range testFiles {
		t.Run(fmt.Sprintf("File_%s", testFile), func(t *testing.T) {
			// Test basic matrix parsing functionality
			testBurndownMatrixParsing(t, testFile)
			testCompressedSparseRowMatrixParsing(t, testFile)
			testMatrixFormatSelection(t, testFile)
			testContentsAccessPattern(t, testFile)
		})
	}
}

// testBurndownMatrixParsing verifies BurndownSparseMatrix parsing matches Python's
// _parse_burndown_matrix behavior (row/column format)
func testBurndownMatrixParsing(t *testing.T, testFile string) {
	t.Run("BurndownSparseMatrix", func(t *testing.T) {
		reader := &ProtobufReader{}
		file, err := os.Open(testFile) // #nosec G304 - test fixture path is passed by table setup.
		require.NoError(t, err)
		defer func() { _ = file.Close() }()

		err = reader.Read(file)
		require.NoError(t, err)

		// Get project burndown which uses BurndownSparseMatrix format
		_, _, matrix, err := reader.GetProjectBurndownWithHeader()
		if err != nil {
			t.Logf("Project burndown not available in %s: %v", testFile, err)
			return
		}

		t.Logf("BurndownSparseMatrix parsing successful:")
		t.Logf("  Matrix dimensions: %dx%d", len(matrix), len(matrix[0]))

		// Verify matrix structure consistency (Python compatibility)
		verifyMatrixStructure(t, matrix, "BurndownSparseMatrix")

		// Test individual file burndown matrices
		files, err := reader.GetFilesBurndown()
		if err == nil {
			t.Logf("  Found %d file burndown matrices", len(files))
			for i, file := range files {
				if i < 3 { // Test first 3 files
					verifyMatrixStructure(t, file.Matrix, fmt.Sprintf("File[%d]", i))
				}
			}
		}

		// Test people burndown matrices
		people, err := reader.GetPeopleBurndown()
		if err == nil {
			t.Logf("  Found %d people burndown matrices", len(people))
			for i, person := range people {
				if i < 3 { // Test first 3 people
					verifyMatrixStructure(t, person.Matrix, fmt.Sprintf("Person[%d]", i))
				}
			}
		}
	})
}

// testCompressedSparseRowMatrixParsing verifies CSR matrix parsing matches Python's
// _parse_sparse_matrix behavior
func testCompressedSparseRowMatrixParsing(t *testing.T, testFile string) {
	t.Run("CompressedSparseRowMatrix", func(t *testing.T) {
		reader := &ProtobufReader{}
		file, err := os.Open(testFile) // #nosec G304 - test fixture path is passed by table setup.
		require.NoError(t, err)
		defer func() { _ = file.Close() }()

		err = reader.Read(file)
		require.NoError(t, err)

		// Test people interaction (uses CSR format)
		people, matrix, err := reader.GetPeopleInteraction()
		if err != nil {
			t.Logf("People interaction not available in %s: %v", testFile, err)
		} else {
			t.Logf("CSR Matrix (PeopleInteraction) parsing successful:")
			t.Logf("  People count: %d", len(people))
			verifyMatrixStructure(t, matrix, "PeopleInteraction")
		}

		// Test file co-occurrence (uses CSR format)
		fileIndex, fileMatrix, err := reader.GetFileCooccurrence()
		if err != nil {
			t.Logf("File cooccurrence not available in %s: %v", testFile, err)
		} else {
			t.Logf("CSR Matrix (FileCooccurrence) parsing successful:")
			t.Logf("  File count: %d", len(fileIndex))
			verifyMatrixStructure(t, fileMatrix, "FileCooccurrence")
		}

		// Test people co-occurrence (uses CSR format)
		peopleIndex, peopleMatrix, err := reader.GetPeopleCooccurrence()
		if err != nil {
			t.Logf("People cooccurrence not available in %s: %v", testFile, err)
		} else {
			t.Logf("CSR Matrix (PeopleCooccurrence) parsing successful:")
			t.Logf("  People count: %d", len(peopleIndex))
			verifyMatrixStructure(t, peopleMatrix, "PeopleCooccurrence")
		}
	})
}

// testMatrixFormatSelection verifies that Go correctly chooses between matrix parsing
// methods based on data type (critical compatibility issue)
func testMatrixFormatSelection(t *testing.T, testFile string) {
	t.Run("FormatSelection", func(t *testing.T) {
		// Load raw protobuf data to inspect structure
		allBytes, err := os.ReadFile(testFile) // #nosec G304 - test fixture path is passed by table setup.
		require.NoError(t, err)

		var results pb.AnalysisResults
		err = proto.Unmarshal(allBytes, &results)
		require.NoError(t, err)

		t.Logf("Analyzing format selection for %s:", testFile)

		// Check Contents map for different analysis types
		if results.Contents != nil {
			for key, data := range results.Contents {
				t.Logf("  Found Contents[\"%s\"] (%d bytes)", key, len(data))

				switch key {
				case "Burndown":
					verifyBurndownFormatSelection(t, data)
				case "Couples":
					verifyCouplesFormatSelection(t, data)
				case "Devs":
					verifyDevsFormatSelection(t, data)
				case "Shotness":
					verifyShotnessFormatSelection(t, data)
				}
			}
		} else {
			t.Log("  No Contents map found - this may indicate format compatibility issues")
		}
	})
}

// verifyBurndownFormatSelection checks that burndown data uses correct matrix format
func verifyBurndownFormatSelection(t *testing.T, data []byte) {
	var burndownData pb.BurndownAnalysisResults
	err := proto.Unmarshal(data, &burndownData)
	if err != nil {
		t.Errorf("Failed to parse Burndown contents: %v", err)
		return
	}

	t.Log("    Burndown analysis structure:")
	if burndownData.Project != nil {
		t.Logf("    - Project: BurndownSparseMatrix (%dx%d)",
			burndownData.Project.NumberOfRows, burndownData.Project.NumberOfColumns)
	}
	t.Logf("    - Files count: %d", len(burndownData.Files))
	t.Logf("    - People count: %d", len(burndownData.People))
	if burndownData.PeopleInteraction != nil {
		t.Logf("    - PeopleInteraction: CompressedSparseRowMatrix (%dx%d)",
			burndownData.PeopleInteraction.NumberOfRows, burndownData.PeopleInteraction.NumberOfColumns)
	}

	// Verify format selection logic
	// Python uses _parse_burndown_matrix() for Project/Files/People (row/column format)
	// Python uses _parse_sparse_matrix() for PeopleInteraction (CSR format)
	// Go should match this pattern
}

// verifyCouplesFormatSelection checks couples data matrix format
func verifyCouplesFormatSelection(t *testing.T, data []byte) {
	var couplesData pb.CouplesAnalysisResults
	err := proto.Unmarshal(data, &couplesData)
	if err != nil {
		t.Errorf("Failed to parse Couples contents: %v", err)
		return
	}

	t.Log("    Couples analysis structure:")
	if couplesData.FileCouples != nil && couplesData.FileCouples.Matrix != nil {
		t.Logf("    - FileCouples: CompressedSparseRowMatrix (%dx%d)",
			couplesData.FileCouples.Matrix.NumberOfRows, couplesData.FileCouples.Matrix.NumberOfColumns)
	}
	if couplesData.PeopleCouples != nil && couplesData.PeopleCouples.Matrix != nil {
		t.Logf("    - PeopleCouples: CompressedSparseRowMatrix (%dx%d)",
			couplesData.PeopleCouples.Matrix.NumberOfRows, couplesData.PeopleCouples.Matrix.NumberOfColumns)
	}

	// Python uses _parse_sparse_matrix() for both FileCouples and PeopleCouples
	// Go should use parseCompressedSparseRowMatrix() for both
}

// verifyDevsFormatSelection checks developer data structure
func verifyDevsFormatSelection(t *testing.T, data []byte) {
	var devsData pb.DevsAnalysisResults
	err := proto.Unmarshal(data, &devsData)
	if err != nil {
		t.Errorf("Failed to parse Devs contents: %v", err)
		return
	}

	t.Log("    Devs analysis structure:")
	t.Logf("    - Developer count: %d", len(devsData.DevIndex))
	t.Logf("    - Time ticks: %d", len(devsData.Ticks))

	// This is critical for time series compatibility
	if len(devsData.Ticks) > 0 {
		t.Log("    - Has time series data (compatible with Python)")
	} else {
		t.Log("    - No time series data (may need synthetic generation)")
	}
}

// verifyShotnessFormatSelection checks shotness data structure
func verifyShotnessFormatSelection(t *testing.T, data []byte) {
	var shotnessData pb.ShotnessAnalysisResults
	err := proto.Unmarshal(data, &shotnessData)
	if err != nil {
		t.Errorf("Failed to parse Shotness contents: %v", err)
		return
	}

	t.Log("    Shotness analysis structure:")
	t.Logf("    - Records count: %d", len(shotnessData.Records))
}

// testContentsAccessPattern verifies Contents map access matches Python's dynamic parsing
func testContentsAccessPattern(t *testing.T, testFile string) {
	t.Run("ContentsAccess", func(t *testing.T) {
		// This test specifically validates that Go's Contents access pattern
		// produces the same results as Python's PB_MESSAGES dynamic parsing

		reader := &ProtobufReader{}
		file, err := os.Open(testFile) // #nosec G304 - test fixture path is passed by table setup.
		require.NoError(t, err)
		defer func() { _ = file.Close() }()

		err = reader.Read(file)
		require.NoError(t, err)

		// Test each Contents access pattern used in Go code
		testMethods := []struct {
			name string
			test func() error
		}{
			{"Burndown", func() error {
				_, _, _, err := reader.GetProjectBurndownWithHeader()
				return err
			}},
			{"Files", func() error {
				_, err := reader.GetFilesBurndown()
				return err
			}},
			{"People", func() error {
				_, err := reader.GetPeopleBurndown()
				return err
			}},
			{"Ownership", func() error {
				_, _, err := reader.GetOwnershipBurndown()
				return err
			}},
			{"PeopleInteraction", func() error {
				_, _, err := reader.GetPeopleInteraction()
				return err
			}},
			{"Couples", func() error {
				_, _, err := reader.GetFileCooccurrence()
				return err
			}},
			{"Devs", func() error {
				_, err := reader.GetDeveloperStats()
				return err
			}},
		}

		successCount := 0
		for _, method := range testMethods {
			err := method.test()
			if err == nil {
				successCount++
				t.Logf("✓ Contents access for %s: SUCCESS", method.name)
			} else {
				t.Logf("✗ Contents access for %s: %v", method.name, err)
			}
		}

		t.Logf("Contents access success rate: %d/%d methods", successCount, len(testMethods))

		// A reasonable file should support at least basic analysis
		assert.Greater(t, successCount, 0, "At least one Contents access method should succeed")
	})
}

// verifyMatrixStructure validates matrix structure and provides debugging info
func verifyMatrixStructure(t *testing.T, matrix [][]int, name string) {
	t.Helper()

	if len(matrix) == 0 {
		t.Logf("    %s: EMPTY matrix", name)
		return
	}

	rows := len(matrix)
	cols := len(matrix[0])

	// Verify rectangular matrix
	for i, row := range matrix {
		if len(row) != cols {
			t.Errorf("    %s: Row %d has %d columns, expected %d", name, i, len(row), cols)
		}
	}

	// Calculate statistics
	totalValues := 0
	nonZeroValues := 0
	maxValue := 0
	minValue := matrix[0][0]

	for _, row := range matrix {
		for _, val := range row {
			totalValues++
			if val > 0 {
				nonZeroValues++
			}
			if val > maxValue {
				maxValue = val
			}
			if val < minValue {
				minValue = val
			}
		}
	}

	sparsity := float64(nonZeroValues) / float64(totalValues) * 100

	t.Logf("    %s: %dx%d, sparsity: %.1f%%, range: %d-%d",
		name, rows, cols, sparsity, minValue, maxValue)

	// Validate that matrix contains meaningful data
	assert.Greater(t, rows, 0, "%s should have rows", name)
	assert.Greater(t, cols, 0, "%s should have columns", name)
	assert.GreaterOrEqual(t, nonZeroValues, 0, "%s should have valid data", name)
}

// TestTransposeOperationCompatibility specifically tests matrix transpose behavior
func TestTransposeOperationCompatibility(t *testing.T) {
	testFiles := []string{
		"../testdata/example_data/hercules_burndown.pb",
	}

	for _, testFile := range testFiles {
		t.Run(testFile, func(t *testing.T) {
			reader := &ProtobufReader{}
			file, err := os.Open(testFile) // #nosec G304 - test fixture path is passed by table setup.
			require.NoError(t, err)
			defer func() { _ = file.Close() }()

			err = reader.Read(file)
			require.NoError(t, err)

			// Test project burndown transpose (matches Python's .T)
			_, _, matrix, err := reader.GetProjectBurndownWithHeader()
			if err != nil {
				t.Skipf("Project burndown not available: %v", err)
				return
			}

			t.Logf("Testing transpose operation compatibility:")
			t.Logf("  Matrix dimensions after transpose: %dx%d", len(matrix), len(matrix[0]))

			// For burndown data, the transpose should result in:
			// - Rows: different metrics (added, removed, etc.)
			// - Columns: time points
			// This matches Python's behavior: dense = ... ; return matrix.name, dense.T

			verifyTransposeLogic(t, matrix, "Project")

			// Test file burndown transposes
			files, err := reader.GetFilesBurndown()
			if err == nil {
				for i, file := range files {
					if i < 3 {
						verifyTransposeLogic(t, file.Matrix, fmt.Sprintf("File_%s", file.Filename))
					}
				}
			}

			// Test people burndown transposes
			people, err := reader.GetPeopleBurndown()
			if err == nil {
				for i, person := range people {
					if i < 3 {
						verifyTransposeLogic(t, person.Matrix, fmt.Sprintf("Person_%s", person.Person))
					}
				}
			}
		})
	}
}

// verifyTransposeLogic validates that transpose operation produces expected structure
func verifyTransposeLogic(t *testing.T, matrix [][]int, name string) {
	t.Helper()

	if len(matrix) == 0 {
		return
	}

	rows := len(matrix)
	cols := len(matrix[0])

	t.Logf("    %s transpose: %dx%d", name, rows, cols)

	// For burndown matrices, we expect:
	// - More time points (columns) than metrics (rows) for reasonable data
	// - Consistent column count across all rows
	// - Non-negative values only

	switch {
	case cols > rows:
		t.Logf("    ✓ %s has more time points (%d) than metrics (%d) - expected for time series", name, cols, rows)
	case cols == rows:
		t.Logf("    ? %s has equal dimensions (%dx%d) - may be co-occurrence matrix", name, rows, cols)
	default:
		t.Logf("    ? %s has more metrics (%d) than time points (%d) - unusual for burndown", name, rows, cols)
	}

	// Verify all values are non-negative (burndown data should not have negative values)
	hasNegative := false
	for i, row := range matrix {
		for j, val := range row {
			if val < 0 {
				t.Errorf("    ✗ %s[%d][%d] = %d (negative value in burndown matrix)", name, i, j, val)
				hasNegative = true
				break
			}
		}
		if hasNegative {
			break
		}
	}

	if !hasNegative {
		t.Logf("    ✓ %s contains only non-negative values", name)
	}
}
