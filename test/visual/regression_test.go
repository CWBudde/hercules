package visual

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const testExampleDataPath = "../../internal/render/testdata/example_data"

// VisualTestCase defines a test case for visual regression testing.
type VisualTestCase struct {
	Name            string
	Mode            string
	InputFile       string
	ExpectedPath    string
	ValidationLevel ValidationLevel
	Description     string
	Artifacts       map[string]string
}

// TestVisualRegression is the small, deterministic pull-request gate. It
// covers the stacked-area, annotated time-area, heatmap, and bar-chart
// renderer families using license-safe fixtures and committed references.
func TestVisualRegression(t *testing.T) {
	requireVisualParityOptIn(t)

	testCases := []VisualTestCase{
		{
			Name:            "BurndownProject",
			Mode:            chartModeBurndownProject,
			InputFile:       filepath.Join(testExampleDataPath, "hercules_burndown.pb"),
			ValidationLevel: ValidationStrict,
			Description:     "stacked project burndown",
			Artifacts: map[string]string{
				"test_burndown-project.png": "golden/burndown_project.png",
			},
		},
		{
			Name:            "Developers",
			Mode:            chartModeDevs,
			InputFile:       "testdata/devs.yaml",
			ValidationLevel: ValidationStrict,
			Description:     "annotated developer time-area chart",
			Artifacts: map[string]string{
				"test_devs.png": "golden/devs.png",
			},
		},
		{
			Name:            "FileCoupling",
			Mode:            chartModeCouplesFiles,
			InputFile:       filepath.Join(testExampleDataPath, "hercules_couples.pb"),
			ValidationLevel: ValidationStrict,
			Description:     "coupling heatmap and ranked bar chart",
			Artifacts: map[string]string{
				"file_coupling_heatmap.png":   "golden/file_coupling_heatmap.png",
				"top_file_coupling_pairs.png": "golden/top_file_coupling_pairs.png",
			},
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
			Mode:            chartModeBurndownProject,
			InputFile:       "../../internal/render/testdata/example_data/hercules_burndown.yaml",
			ExpectedPath:    "reference/python_burndown_absolute.png",
			ValidationLevel: ValidationLenient, // Different rendering engines need more tolerance
			Description:     "Functional compatibility with Python matplotlib output",
		},
		{
			Name:            "BurndownRelativePythonCompatibility",
			Mode:            chartModeBurndownProjectRelative,
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
		chartModeBurndownProject,
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

func TestArtifactSetValidation(t *testing.T) {
	expected := map[string]string{"chart.png": "golden/chart.png"}

	if err := validateArtifactSet(
		map[string]string{"chart.png": "/tmp/chart.png"},
		expected,
	); err != nil {
		t.Fatalf("matching artifact set rejected: %v", err)
	}
	if err := validateArtifactSet(map[string]string{}, expected); err == nil {
		t.Fatal("missing required artifact was accepted")
	}
	if err := validateArtifactSet(
		map[string]string{
			"chart.png":      "/tmp/chart.png",
			"unexpected.png": "/tmp/unexpected.png",
		},
		expected,
	); err == nil {
		t.Fatal("unexpected artifact was accepted")
	}
}

// runVisualRegressionTest executes a single visual regression test case.
func runVisualRegressionTest(t *testing.T, tc VisualTestCase) {
	t.Helper()

	generator := NewChartGenerator(t.TempDir())
	current, err := generator.GenerateChartSet(t, tc.Mode, tc.InputFile)
	if err != nil {
		t.Fatalf("Failed to generate %s: %v", tc.Description, err)
	}
	if setErr := validateArtifactSet(current, tc.Artifacts); setErr != nil {
		t.Fatalf("%s artifact contract failed: %v", tc.Description, setErr)
	}

	for name, expectedPath := range tc.Artifacts {
		currentPath := current[name]
		if os.Getenv("UPDATE_VISUAL_GOLDENS") == "1" {
			if mkdirErr := os.MkdirAll(filepath.Dir(expectedPath), 0o750); mkdirErr != nil {
				t.Fatalf("create golden directory: %v", mkdirErr)
			}
			copyFile(t, currentPath, expectedPath)
		}

		_, readErr := os.Stat(expectedPath)
		if readErr != nil {
			t.Fatalf("required visual reference %s is unavailable: %v", expectedPath, readErr)
		}

		metrics, compareErr := CompareImages(currentPath, expectedPath)
		if compareErr != nil {
			t.Fatalf("Failed to compare %s: %v", name, compareErr)
		}
		report := metrics.GetDetailedReport(tc.ValidationLevel)
		t.Logf("%s visual regression results:\n%s", name, report)

		// Rasterization can vary slightly across runner CPUs even when dimensions,
		// labels, palette, and layout are unchanged. Compare decoded pixels using
		// the strict regression threshold instead of platform-specific PNG bytes.
		if !metrics.IsValidationPassing(tc.ValidationLevel) {
			t.Errorf("visual regression for %s (%s): generated PNG differs from committed golden\n%s",
				tc.Name, name, report)
			saveDifferenceAnalysis(t, tc.Name+"_"+name, currentPath, expectedPath, metrics)
		}
	}
}

func validateArtifactSet(actual, expected map[string]string) error {
	var missing, unexpected []string
	for name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range actual {
		if _, ok := expected[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	if len(missing) == 0 && len(unexpected) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return fmt.Errorf("missing=%v unexpected=%v", missing, unexpected)
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
		t.Fatalf("required Python reference image not found: %s", tc.ExpectedPath)
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

	err = png.Encode(file, img)
	if err != nil {
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
