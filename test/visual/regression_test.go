package visual

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// VisualTestCase defines a test case for visual regression testing.
type VisualTestCase struct {
	Name            string
	Mode            string
	InputFile       string
	ExpectedPath    string
	ValidationLevel ValidationLevel
	Description     string
}

// TestVisualRegression runs comprehensive visual regression tests.
func TestVisualRegression(t *testing.T) {
	requireVisualParityOptIn(t)

	// Define test cases for different chart types and modes
	testCases := []VisualTestCase{
		{
			Name:            "BurndownProject",
			Mode:            "burndown-project",
			InputFile:       "../../internal/render/testdata/hercules/report_default.pb",
			ExpectedPath:    "golden/burndown_project_golden.png",
			ValidationLevel: ValidationStandard,
			Description:     "Project-level burndown chart visual consistency",
		},
		{
			Name:            "BurndownProjectRelative",
			Mode:            "burndown-project-relative",
			InputFile:       "../../internal/render/testdata/hercules/report_default.pb",
			ExpectedPath:    "golden/burndown_project_relative_golden.png",
			ValidationLevel: ValidationStandard,
			Description:     "Relative burndown chart (percentage-based)",
		},
		{
			Name:            "Ownership",
			Mode:            "ownership",
			InputFile:       "../../internal/render/testdata/hercules/report_default.pb",
			ExpectedPath:    "golden/ownership_golden.png",
			ValidationLevel: ValidationLenient, // More tolerance for complex heatmaps
			Description:     "Code ownership visualization",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runVisualRegressionTest(t, tc)
		})
	}
}

// TestPythonCompatibility runs visual compatibility tests against Python reference images.
func TestPythonCompatibility(t *testing.T) {
	requirePythonParityOptIn(t)

	pythonComparisonCases := []VisualTestCase{
		{
			Name:            "BurndownPythonCompatibility",
			Mode:            "burndown-project",
			InputFile:       "../../internal/render/testdata/example_data/hercules_burndown.yaml",
			ExpectedPath:    "reference/python_burndown_absolute.png",
			ValidationLevel: ValidationLenient, // Different rendering engines need more tolerance
			Description:     "Functional compatibility with Python matplotlib output",
		},
		{
			Name:            "BurndownRelativePythonCompatibility",
			Mode:            "burndown-project-relative",
			InputFile:       "../../internal/render/testdata/example_data/hercules_burndown.yaml",
			ExpectedPath:    "reference/python_burndown_relative.png",
			ValidationLevel: ValidationLenient,
			Description:     "Relative burndown compatibility with Python output",
		},
	}

	for _, tc := range pythonComparisonCases {
		t.Run(tc.Name, func(t *testing.T) {
			runPythonCompatibilityTest(t, tc)
		})
	}
}

// TestChartStructuralValidation performs functional validation of chart components.
func TestChartStructuralValidation(t *testing.T) {
	// Generate a test chart
	outputPath := generateTestChart(
		t,
		"burndown-project",
		"../../internal/render/testdata/example_data/hercules_burndown.yaml",
	)

	// Validate chart structure and components
	t.Run("ChartFileGeneration", func(t *testing.T) {
		_, err := os.Stat(outputPath)
		if os.IsNotExist(err) {
			t.Errorf("Chart file was not generated: %s", outputPath)
		}
	})

	t.Run("ChartDimensions", func(t *testing.T) {
		validateChartDimensions(t, outputPath)
	})

	t.Run("ChartColorScheme", func(t *testing.T) {
		validateChartColorScheme(t, outputPath)
	})
}

// TestSimilarityMetricsAccuracy validates the similarity calculation algorithms.
func TestSimilarityMetricsAccuracy(t *testing.T) {
	// Create temp directory for test images
	tmpDir := t.TempDir()

	// Test with identical images (should be 100% similar)
	t.Run("IdenticalImages", func(t *testing.T) {
		img1 := filepath.Join(tmpDir, "image1.png")
		img2 := filepath.Join(tmpDir, "identical.png")
		writeSolidPNG(t, img1, 640, 320, color.RGBA{R: 32, G: 96, B: 160, A: 255})

		// Copy image to create identical reference
		copyFile(t, img1, img2)

		metrics, err := CompareImages(img1, img2)
		if err != nil {
			t.Fatalf("Failed to compare identical images: %v", err)
		}

		if metrics.OverallSimilarity < 0.99 {
			t.Errorf("Identical images should have >99%% similarity, got %.2f%%",
				metrics.OverallSimilarity*100)
		}

		t.Logf("Identical images metrics: %s",
			metrics.GetDetailedReport(ValidationStandard))
	})

	// Test with different sized images (should fail gracefully)
	t.Run("DifferentDimensions", func(t *testing.T) {
		img1 := filepath.Join(tmpDir, "wide.png")
		img2 := filepath.Join(tmpDir, "narrow.png")
		writeSolidPNG(t, img1, 640, 320, color.RGBA{R: 32, G: 96, B: 160, A: 255})
		writeSolidPNG(t, img2, 320, 320, color.RGBA{R: 32, G: 96, B: 160, A: 255})

		_, err := CompareImages(img1, img2)
		if err == nil {
			t.Error("Expected error when comparing images with different dimensions")
		}
	})
}

// runVisualRegressionTest executes a single visual regression test case.
func runVisualRegressionTest(t *testing.T, tc VisualTestCase) {
	t.Helper()

	// Check if golden file exists before doing expensive chart generation.
	_, err := os.Stat(tc.ExpectedPath)
	if os.IsNotExist(err) {
		t.Skipf("Golden file not found: %s (run with GENERATE_REFERENCES=true to create)",
			tc.ExpectedPath)
		return
	}

	// Generate current output
	currentOutput := generateTestChart(t, tc.Mode, tc.InputFile)
	defer func() {
		// Clean up generated file
		err := os.Remove(currentOutput)
		if err != nil {
			t.Logf("Failed to clean up test file %s: %v", currentOutput, err)
		}
	}()

	// Compare images
	metrics, err := CompareImages(currentOutput, tc.ExpectedPath)
	if err != nil {
		t.Fatalf("Failed to compare images: %v", err)
	}

	// Generate detailed report
	report := metrics.GetDetailedReport(tc.ValidationLevel)
	t.Logf("Visual regression test results:\n%s", report)

	// Check if validation passes
	if !metrics.IsValidationPassing(tc.ValidationLevel) {
		t.Errorf("Visual regression test failed for %s:\n%s\nDescription: %s",
			tc.Name, report, tc.Description)

		// Save difference image for manual inspection
		saveDifferenceAnalysis(t, tc.Name, currentOutput, tc.ExpectedPath, metrics)
	}
}

// runPythonCompatibilityTest executes compatibility test against Python reference.
func runPythonCompatibilityTest(t *testing.T, tc VisualTestCase) {
	t.Helper()

	// Generate Go output
	goOutput := generateTestChart(t, tc.Mode, tc.InputFile)
	defer func() {
		err := os.Remove(goOutput)
		if err != nil {
			t.Logf("Failed to clean up Go output file %s: %v", goOutput, err)
		}
	}()

	// Check if Python reference exists
	_, err := os.Stat(tc.ExpectedPath)
	if os.IsNotExist(err) {
		t.Skipf("Python reference image not found: %s", tc.ExpectedPath)
		return
	}

	// Compare with Python output
	metrics, err := CompareImages(goOutput, tc.ExpectedPath)
	if err != nil {
		t.Fatalf("Failed to compare Go output with Python reference: %v", err)
	}

	report := metrics.GetDetailedReport(tc.ValidationLevel)
	t.Logf("Python compatibility test results:\n%s", report)

	if !metrics.IsValidationPassing(tc.ValidationLevel) {
		t.Errorf("Python compatibility test failed for %s:\n%s", tc.Name, report)
		saveDifferenceAnalysis(t, tc.Name+"_python_compat",
			goOutput, tc.ExpectedPath, metrics)
	} else {
		t.Logf("✅ Python compatibility maintained for %s", tc.Name)
	}
}

// generateTestChart creates a chart using the current implementation.
func generateTestChart(t *testing.T, mode, inputFile string) string {
	t.Helper()

	// Create temporary output directory
	tmpDir := t.TempDir()

	// Use chart generator
	generator := NewChartGenerator(tmpDir)
	outputPath, err := generator.GenerateChart(t, mode, inputFile)
	if err != nil {
		t.Fatalf("Failed to generate test chart: %v", err)
	}

	// Validate chart structure
	err = generator.ValidateChartStructure(t, outputPath)
	if err != nil {
		t.Errorf("Chart structure validation failed: %v", err)
	}

	return outputPath
}

// validateChartDimensions checks if chart has expected dimensions.
func validateChartDimensions(t *testing.T, chartPath string) {
	t.Helper()

	img, err := loadImage(chartPath)
	if err != nil {
		t.Fatalf("Failed to load chart for dimension validation: %v", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Expected dimensions based on current chart generation (16x8 inches at standard DPI)
	expectedMinWidth := 800  // Minimum reasonable width
	expectedMinHeight := 400 // Minimum reasonable height

	if width < expectedMinWidth || height < expectedMinHeight {
		t.Errorf("Chart dimensions too small: %dx%d (expected at least %dx%d)",
			width, height, expectedMinWidth, expectedMinHeight)
	}

	t.Logf("Chart dimensions: %dx%d", width, height)
}

// validateChartColorScheme checks for expected color usage.
func validateChartColorScheme(t *testing.T, chartPath string) {
	t.Helper()

	img, err := loadImage(chartPath)
	if err != nil {
		t.Fatalf("Failed to load chart for color validation: %v", err)
	}

	// Build color histogram to check that the rendered chart contains real
	// foreground content. Palette parity belongs in the opt-in parity tests.
	histogram := buildColorHistogram(img)

	if len(histogram) < 5 {
		t.Errorf("Chart has too few distinct colors: %d", len(histogram))
	}

	t.Logf("Chart color diversity: %d quantized colors", len(histogram))
}

func requireVisualParityOptIn(t *testing.T) {
	t.Helper()
	if os.Getenv("LABOURS_GO_VISUAL_PARITY") != "1" {
		t.Skip("visual parity tests are opt-in; set LABOURS_GO_VISUAL_PARITY=1")
	}
}

func requirePythonParityOptIn(t *testing.T) {
	t.Helper()
	if os.Getenv("LABOURS_GO_PYTHON_PARITY") != "1" {
		t.Skip("Python parity tests are opt-in; set LABOURS_GO_PYTHON_PARITY=1")
	}
}

func writeSolidPNG(t *testing.T, path string, width, height int, c color.RGBA) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, c)
		}
	}

	file, err := os.Create(path) // #nosec G304 - visual test path is provided by test setup.
	if err != nil {
		t.Fatalf("Failed to create PNG: %v", err)
	}
	defer func() { _ = file.Close() }()

	if err := png.Encode(file, img); err != nil {
		t.Fatalf("Failed to write PNG: %v", err)
	}
}

// copyFile copies a file for testing purposes.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()

	srcFile, err := os.Open(src) // #nosec G304 - visual test path is provided by test setup.
	if err != nil {
		t.Fatalf("Failed to open source file: %v", err)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.Create(dst) // #nosec G304 - visual test path is provided by test setup.
	if err != nil {
		t.Fatalf("Failed to create destination file: %v", err)
	}
	defer func() { _ = dstFile.Close() }()

	_, err = dstFile.ReadFrom(srcFile)
	if err != nil {
		t.Fatalf("Failed to copy file: %v", err)
	}
}

// saveDifferenceAnalysis saves detailed difference analysis for manual review.
func saveDifferenceAnalysis(t *testing.T, testName, currentPath, expectedPath string, metrics *SimilarityMetrics) {
	t.Helper()

	// Create analysis directory
	analysisDir := filepath.Join("analysis_output", testName)
	err := os.MkdirAll(analysisDir, 0o750)
	if err != nil {
		t.Logf("Failed to create analysis directory: %v", err)
		return
	}

	// Copy current and expected images for comparison
	copyFile(t, currentPath, filepath.Join(analysisDir, "current.png"))
	copyFile(t, expectedPath, filepath.Join(analysisDir, "expected.png"))

	// Save detailed report
	reportPath := filepath.Join(analysisDir, "analysis_report.txt")
	report := metrics.GetDetailedReport(ValidationStandard)

	err = os.WriteFile(reportPath, []byte(report), 0o600)
	if err != nil {
		t.Logf("Failed to save analysis report: %v", err)
	} else {
		t.Logf("📊 Detailed analysis saved to: %s", analysisDir)
	}
}
