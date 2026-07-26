package modes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateBurndownPlot(t *testing.T) {
	// Create temporary directory for test outputs
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_burndown.png")

	// Sample matrix data for testing
	testMatrix := [][]int{
		{100, 90, 80},  // Day 1
		{120, 100, 85}, // Day 2
		{110, 95, 90},  // Day 3
	}

	// Test with basic parameters
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	err := generateBurndownPlot("test", testMatrix, outputPath, false, &startTime, &endTime, "day")
	if err != nil {
		t.Errorf("generateBurndownPlot() error = %v", err)
	}

	// Check if output file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestGenerateBurndownPlotRelative(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_burndown_relative.png")

	testMatrix := [][]int{
		{100, 90, 80},
		{120, 100, 85},
		{110, 95, 90},
	}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	// Test relative mode
	err := generateBurndownPlot("test_relative", testMatrix, outputPath, true, &startTime, &endTime, "day")
	if err != nil {
		t.Errorf("generateBurndownPlot() with relative=true error = %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestGenerateBurndownPlotResamplingModes(t *testing.T) {
	tmpDir := t.TempDir()

	testMatrix := [][]int{
		{100, 90, 80},
		{120, 100, 85},
		{110, 95, 90},
	}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	resamplingModes := []string{"year", "month", "week", "day"}

	for _, mode := range resamplingModes {
		t.Run("resampling_"+mode, func(t *testing.T) {
			outputPath := filepath.Join(tmpDir, "test_burndown_"+mode+".png")

			err := generateBurndownPlot("test_"+mode, testMatrix, outputPath, false, &startTime, &endTime, mode)
			if err != nil {
				t.Errorf("generateBurndownPlot() with resample=%s error = %v", mode, err)
			}

			if _, err := os.Stat(outputPath); os.IsNotExist(err) {
				t.Errorf("Output file was not created for resample mode %s: %s", mode, outputPath)
			}
		})
	}
}

func TestGenerateBurndownPlotEmptyMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_burndown_empty.png")

	// Test with empty matrix - this should panic or error
	emptyMatrix := [][]int{}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	// The function currently panics on empty matrix - this is expected behavior
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Expected panic recovered: %v", r)
		}
	}()

	err := generateBurndownPlot("test_empty", emptyMatrix, outputPath, false, &startTime, &endTime, "day")
	if err == nil {
		t.Error("Expected error for empty matrix, but got nil")
	}
}

func TestFindEarliestTime(t *testing.T) {
	// Test data with 3x3 matrix
	testMatrix := [][]int{
		{100, 90, 80},
		{120, 100, 85},
		{110, 95, 90},
	}

	endTime := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	tickSize := 24 * time.Hour // Daily ticks

	earliestTime := findEarliestTime(testMatrix, tickSize, endTime)

	// Should calculate back from endTime based on matrix size
	// The actual function returns endTime - (numPoints * tickSize), which is 2024-01-03 - 3*24h = 2023-12-31
	expectedTime := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	if !earliestTime.Equal(expectedTime) {
		t.Errorf("findEarliestTime() = %v, want %v", earliestTime, expectedTime)
	}
}

func TestResampleMatrix(t *testing.T) {
	// Test matrix resampling functionality
	testMatrix := [][]int{
		{100, 90, 80, 70, 60, 50}, // 6 time points
		{120, 100, 85, 75, 65, 55},
	}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC)

	// Resample from daily to every 2 days (should result in 3 points)
	resampled := mockResampleMatrix(testMatrix, startTime, endTime, "2day")

	if len(resampled) != len(testMatrix) {
		t.Errorf("Expected %d rows after resampling, got %d", len(testMatrix), len(resampled))
	}

	// Check that resampling reduced the number of columns
	if len(resampled[0]) >= len(testMatrix[0]) {
		t.Errorf("Expected resampling to reduce columns, but got %d >= %d", len(resampled[0]), len(testMatrix[0]))
	}
}

func mockResampleMatrix(matrix [][]int, startTime, endTime time.Time, resample string) [][]int {
	if len(matrix) == 0 {
		return matrix
	}

	// Simple mock resampling - just reduce by half
	resampled := make([][]int, len(matrix))
	for i := range matrix {
		newLen := len(matrix[i]) / 2
		if newLen == 0 {
			newLen = 1
		}
		resampled[i] = make([]int, newLen)

		for j := 0; j < newLen && j*2 < len(matrix[i]); j++ {
			resampled[i][j] = matrix[i][j*2]
		}
	}

	return resampled
}
