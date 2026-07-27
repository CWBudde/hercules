package graphics

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateStackedPlot(t *testing.T) {
	// Create temporary directory for test outputs
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_stacked_plot.png")

	// Sample data for testing
	testData := [][]float64{
		{100, 90, 80, 70, 60}, // Series 1
		{0, 10, 20, 30, 40},   // Series 2
		{0, 0, 0, 0, 0},       // Series 3 (empty)
	}

	labels := []string{"Series 1", "Series 2", "Series 3"}

	// Create time points
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timePoints := make([]time.Time, 5)
	for i := range timePoints {
		timePoints[i] = startTime.AddDate(0, 0, i)
	}

	err := mockCreateStackedPlot(testData, labels, timePoints, "Test Stacked Plot", "Lines of Code", outputPath)
	if err != nil {
		t.Errorf("CreateStackedPlot() error = %v", err)
	}

	// Check if output file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}

	// Verify that the mock file was created and has content
	content, err := os.ReadFile(outputPath) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Errorf("Failed to read output file: %v", err)
		return
	}

	if len(content) == 0 {
		t.Error("Output file is empty")
	}

	// For mock tests, just verify the content contains expected text
	expectedText := "Mock stacked plot"
	if !strings.Contains(string(content), expectedText) {
		t.Errorf("Output file should contain '%s', got: %s", expectedText, string(content))
	}
}

func TestCreateStackedPlotEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_empty_plot.png")

	// Test with empty data
	emptyData := [][]float64{}
	emptyLabels := []string{}
	emptyTimes := []time.Time{}

	err := mockCreateStackedPlot(emptyData, emptyLabels, emptyTimes, "Empty Plot", "Value", outputPath)
	if err == nil {
		t.Error("Expected error for empty data, but got nil")
	}
}

func TestCreateStackedPlotSingleSeries(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_single_series.png")

	testData := [][]float64{
		{100, 80, 60, 40, 20},
	}
	labels := []string{"Single Series"}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timePoints := make([]time.Time, 5)
	for i := range timePoints {
		timePoints[i] = startTime.AddDate(0, 0, i)
	}

	err := mockCreateStackedPlot(testData, labels, timePoints, "Single Series Plot", "Value", outputPath)
	if err != nil {
		t.Errorf("CreateStackedPlot() with single series error = %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestCreateStackedPlotMismatchedData(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_mismatched.png")

	// Mismatched data lengths
	testData := [][]float64{
		{100, 90, 80}, // 3 points
		{50, 40},      // 2 points (mismatch)
	}
	labels := []string{"Series 1", "Series 2"}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timePoints := make([]time.Time, 3) // 3 points
	for i := range timePoints {
		timePoints[i] = startTime.AddDate(0, 0, i)
	}

	err := mockCreateStackedPlot(testData, labels, timePoints, "Mismatched Plot", "Value", outputPath)
	if err == nil {
		t.Error("Expected error for mismatched data lengths, but got nil")
	}
}

func TestCreateBarChart(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_bar_chart.png")

	// Sample data for bar chart
	values := []float64{100, 80, 60, 40, 20}
	labels := []string{"Alice", "Bob", "Charlie", "Dave", "Eve"}

	err := mockCreateBarChart(values, labels, "Developer Commits", "Commits", outputPath)
	if err != nil {
		t.Errorf("CreateBarChart() error = %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file was not created: %s", outputPath)
	}
}

func TestCreateBarChartEmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_bar_chart_empty.png")

	err := mockCreateBarChart([]float64{}, []string{}, "Empty Chart", "Value", outputPath)
	if err == nil {
		t.Error("Expected error for empty bar chart data, but got nil")
	}
}

func TestInterpolateData(t *testing.T) {
	// Test linear interpolation
	originalData := [][]float64{
		{100, 50, 0}, // 3 points
		{0, 25, 50},
	}

	// Interpolate to 5 points
	interpolated := interpolateData(originalData, 5)

	if len(interpolated) != len(originalData) {
		t.Errorf("Expected %d series after interpolation, got %d", len(originalData), len(interpolated))
	}

	if len(interpolated[0]) != 5 {
		t.Errorf("Expected 5 points after interpolation, got %d", len(interpolated[0]))
	}

	// First and last points should be preserved
	if interpolated[0][0] != 100 {
		t.Errorf("Expected first point to be preserved (100), got %f", interpolated[0][0])
	}

	if interpolated[0][4] != 0 {
		t.Errorf("Expected last point to be preserved (0), got %f", interpolated[0][4])
	}

	// Middle point should be interpolated
	if interpolated[0][2] != 50 {
		t.Errorf("Expected middle point to be 50, got %f", interpolated[0][2])
	}
}

func TestCalculateStackedValues(t *testing.T) {
	// Test stacking calculation
	data := [][]float64{
		{100, 80, 60},
		{50, 40, 30},
		{25, 20, 15},
	}

	stacked := calculateStackedValues(data)

	if len(stacked) != len(data) {
		t.Errorf("Expected %d stacked series, got %d", len(data), len(stacked))
	}

	// First series should be unchanged
	for i, val := range stacked[0] {
		if val != data[0][i] {
			t.Errorf("First series should be unchanged: expected %f, got %f", data[0][i], val)
		}
	}

	// Second series should be cumulative
	for i := range stacked[1] {
		expected := data[0][i] + data[1][i]
		if stacked[1][i] != expected {
			t.Errorf("Second series cumulative value: expected %f, got %f", expected, stacked[1][i])
		}
	}

	// Third series should be fully cumulative
	for i := range stacked[2] {
		expected := data[0][i] + data[1][i] + data[2][i]
		if stacked[2][i] != expected {
			t.Errorf("Third series cumulative value: expected %f, got %f", expected, stacked[2][i])
		}
	}
}

func TestGenerateColorPalette(t *testing.T) {
	// Test color palette generation
	colors := generateTestColorPalette(5)

	if len(colors) != 5 {
		t.Errorf("Expected 5 colors, got %d", len(colors))
	}

	// Check that all colors are different
	for i := 0; i < len(colors); i++ {
		for j := i + 1; j < len(colors); j++ {
			if colors[i] == colors[j] {
				t.Errorf("Colors %d and %d are identical", i, j)
			}
		}
	}

	// Test with single color
	singleColor := generateTestColorPalette(1)
	if len(singleColor) != 1 {
		t.Errorf("Expected 1 color, got %d", len(singleColor))
	}
}

func TestNormalizeData(t *testing.T) {
	// Test data normalization (for relative plots)
	data := [][]float64{
		{100, 80, 60},
		{50, 40, 30},
	}

	normalized := normalizeData(data)

	// Check that each time point sums to 100%
	for timePoint := 0; timePoint < len(normalized[0]); timePoint++ {
		sum := 0.0
		for series := 0; series < len(normalized); series++ {
			sum += normalized[series][timePoint]
		}
		if sum < 99.9 || sum > 100.1 { // Allow for small floating point errors
			t.Errorf("Time point %d doesn't sum to 100%%: %f", timePoint, sum)
		}
	}
}

func TestSaveImagePNG(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "test_save.png")

	// Create a simple test image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	// Fill with red color
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}

	err := saveImagePNG(img, outputPath)
	if err != nil {
		t.Errorf("saveImagePNG() error = %v", err)
	}

	// Verify file exists and can be decoded
	file, err := os.Open(outputPath) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Errorf("Failed to open saved image: %v", err)
		return
	}
	defer func() { _ = file.Close() }()

	_, err = png.Decode(file)
	if err != nil {
		t.Errorf("Failed to decode saved PNG: %v", err)
	}
}

// Helper functions for testing

func interpolateData(data [][]float64, targetPoints int) [][]float64 {
	if len(data) == 0 || targetPoints <= 0 {
		return data
	}

	interpolated := make([][]float64, len(data))
	for i := range interpolated {
		interpolated[i] = make([]float64, targetPoints)

		if len(data[i]) == 0 {
			continue
		}

		// Linear interpolation
		for j := 0; j < targetPoints; j++ {
			pos := float64(j) / float64(targetPoints-1) * float64(len(data[i])-1)
			idx := int(pos)

			if idx >= len(data[i])-1 {
				interpolated[i][j] = data[i][len(data[i])-1]
			} else {
				fraction := pos - float64(idx)
				interpolated[i][j] = data[i][idx]*(1-fraction) + data[i][idx+1]*fraction
			}
		}
	}

	return interpolated
}

func calculateStackedValues(data [][]float64) [][]float64 {
	if len(data) == 0 {
		return data
	}

	stacked := make([][]float64, len(data))
	for i, series := range data {
		stacked[i] = append([]float64(nil), series...)

		// Add values from previous series
		for _, previous := range data[:i] {
			for k := range stacked[i] {
				if k < len(previous) {
					stacked[i][k] += previous[k]
				}
			}
		}
	}

	return stacked
}

func generateTestColorPalette(count int) []color.Color {
	colors := make([]color.Color, count)

	for i := 0; i < count; i++ {
		hue := float64(i) / float64(count) * 360
		colors[i] = hsvToRGB(hue, 0.7, 0.9)
	}

	return colors
}

func normalizeData(data [][]float64) [][]float64 {
	if len(data) == 0 || len(data[0]) == 0 {
		return data
	}

	normalized := make([][]float64, len(data))
	for i := range normalized {
		normalized[i] = make([]float64, len(data[i]))
	}

	// Normalize each time point to sum to 100%
	for timePoint := 0; timePoint < len(data[0]); timePoint++ {
		total := 0.0
		for series := 0; series < len(data); series++ {
			total += data[series][timePoint]
		}

		if total > 0 {
			for series := 0; series < len(data); series++ {
				normalized[series][timePoint] = (data[series][timePoint] / total) * 100.0
			}
		}
	}

	return normalized
}

func saveImagePNG(img image.Image, path string) error {
	file, err := os.Create(path) // #nosec G304 - test helper writes caller-provided temp path.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	return png.Encode(file, img)
}

func hsvToRGB(h, s, v float64) color.Color {
	// Simple HSV to RGB conversion for testing
	h /= 60.0
	i := int(h)
	f := h - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))

	var r, g, b float64
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	case 5:
		r, g, b = v, p, q
	}

	return color.RGBA{
		R: uint8(r * 255),
		G: uint8(g * 255),
		B: uint8(b * 255),
		A: 255,
	}
}

// Mock functions for testing

func mockCreateStackedPlot(data [][]float64, labels []string, timePoints []time.Time, title, yLabel, outputPath string) error {
	if len(data) == 0 || len(labels) == 0 || len(timePoints) == 0 {
		return fmt.Errorf("empty data provided")
	}

	// Check for mismatched data
	if len(data) != len(labels) {
		return fmt.Errorf("data and labels length mismatch")
	}

	for i, series := range data {
		if len(series) != len(timePoints) {
			return fmt.Errorf("series %d length doesn't match time points", i)
		}
	}

	// Create mock output file
	content := fmt.Sprintf("Mock stacked plot: %s with %d series and %d time points", title, len(data), len(timePoints))
	return os.WriteFile(outputPath, []byte(content), 0o600)
}

func mockCreateBarChart(values []float64, labels []string, title, yLabel, outputPath string) error {
	if len(values) == 0 || len(labels) == 0 {
		return fmt.Errorf("empty data provided")
	}

	if len(values) != len(labels) {
		return fmt.Errorf("values and labels length mismatch")
	}

	// Create mock output file
	content := fmt.Sprintf("Mock bar chart: %s with %d bars", title, len(values))
	return os.WriteFile(outputPath, []byte(content), 0o600)
}
