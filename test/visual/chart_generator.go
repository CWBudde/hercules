package visual

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/modes"
	"github.com/cwbudde/hercules/internal/render/readers"
)

const (
	chartModeBurndownProject         = "burndown-project"
	chartModeBurndownProjectRelative = "burndown-project-relative"
	chartModeOwnership               = "ownership"
)

var (
	errUnsupportedChartMode    = errors.New("unsupported chart mode")
	errChartNotCreated         = errors.New("chart file was not created")
	errChartDimensionsTooSmall = errors.New("chart dimensions too small")
	errChartDimensionsTooLarge = errors.New("chart dimensions too large")
	errTooFewChartColors       = errors.New("chart has too few colors")
	errChartMostlyWhite        = errors.New("chart is mostly white")
	errChartMostlyBlack        = errors.New("chart is mostly black")
	errImageDimensionsMismatch = errors.New("image dimensions don't match")
)

// ChartGenerator handles chart generation for visual testing.
type ChartGenerator struct {
	OutputDir string
}

// NewChartGenerator creates a new chart generator instance.
func NewChartGenerator(outputDir string) *ChartGenerator {
	return &ChartGenerator{
		OutputDir: outputDir,
	}
}

// GenerateChart creates a chart using the specified mode and input data.
func (cg *ChartGenerator) GenerateChart(t *testing.T, mode, inputFile string) (string, error) {
	t.Helper()

	// Ensure output directory exists
	err := os.MkdirAll(cg.OutputDir, 0o750)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create output file path
	outputPath := filepath.Join(cg.OutputDir, fmt.Sprintf("test_%s.png", mode))

	// Read input data - auto-detect format
	reader := chartReader(inputFile)

	file, err := os.Open(inputFile) // #nosec G304 - visual test input path is provided by test setup.
	if err != nil {
		return "", fmt.Errorf("failed to open input file %s: %w", inputFile, err)
	}
	defer func() { _ = file.Close() }()

	err = reader.Read(file)
	if err != nil {
		return "", fmt.Errorf("failed to read input data: %w", err)
	}

	err = generateChartByMode(cg, mode, reader, outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to generate %s chart: %w", mode, err)
	}

	// Verify output file was created
	_, err = os.Stat(outputPath)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%w: %s", errChartNotCreated, outputPath)
	}

	return outputPath, nil
}

func chartReader(inputFile string) readers.Reader {
	switch filepath.Ext(inputFile) {
	case ".yaml", ".yml":
		return &readers.YamlReader{}
	default:
		return &readers.ProtobufReader{}
	}
}

func generateChartByMode(generator *ChartGenerator, mode string, reader readers.Reader, outputPath string) error {
	switch mode {
	case chartModeBurndownProject:
		return generator.generateBurndownProject(reader, outputPath, false)
	case chartModeBurndownProjectRelative:
		return generator.generateBurndownProject(reader, outputPath, true)
	case "burndown-file":
		return generator.generateBurndownFile(reader, outputPath)
	case "burndown-person":
		return generator.generateBurndownPerson(reader, outputPath)
	case chartModeOwnership:
		return generator.generateOwnership(reader, outputPath)
	case "devs":
		return generator.generateDevs(reader, outputPath)
	case "couples-people":
		return generator.generateCouplesPeople(reader, outputPath)
	case "couples-files":
		return generator.generateCouplesFiles(reader, outputPath)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedChartMode, mode)
	}
}

// GenerateReferenceSet creates a complete set of reference images for golden file testing.
func (cg *ChartGenerator) GenerateReferenceSet(t *testing.T, inputFile string) map[string]string {
	t.Helper()

	generatedFiles := make(map[string]string)

	// List of modes to generate reference images for
	modes := []string{
		chartModeBurndownProject,
		chartModeBurndownProjectRelative,
		chartModeOwnership,
		"devs",
	}

	for _, mode := range modes {
		outputPath, err := cg.GenerateChart(t, mode, inputFile)
		if err != nil {
			t.Logf("Warning: Failed to generate reference for %s: %v", mode, err)
			continue
		}

		generatedFiles[mode] = outputPath
		t.Logf("✅ Generated reference image for %s: %s", mode, outputPath)
	}

	return generatedFiles
}

// ValidateChartStructure performs structural validation on a generated chart.
func (cg *ChartGenerator) ValidateChartStructure(t *testing.T, chartPath string) error {
	t.Helper()

	// Load the chart image
	img, err := loadImage(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart image: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	err = validateImageDimensions(width, height)
	if err != nil {
		return err
	}

	// Check for reasonable aspect ratio (should be wider than tall for most charts)
	aspectRatio := float64(width) / float64(height)
	if aspectRatio < 0.5 || aspectRatio > 5.0 {
		t.Logf("Warning: Unusual aspect ratio: %.2f (width/height)", aspectRatio)
	}

	// Validate color usage
	histogram := buildColorHistogram(img)

	whitePixels, err := validateChartColors(histogram)
	if err != nil {
		return err
	}

	t.Logf("Chart structure validation passed: %dx%d, %d colors, %.1f%% white",
		width, height, len(histogram), whitePixels*100)

	return nil
}

func validateImageDimensions(width, height int) error {
	if width < 400 || height < 200 {
		return fmt.Errorf("%w: %dx%d", errChartDimensionsTooSmall, width, height)
	}

	if width > 4000 || height > 4000 {
		return fmt.Errorf("%w: %dx%d", errChartDimensionsTooLarge, width, height)
	}

	return nil
}

func validateChartColors(histogram map[string]float64) (float64, error) {
	if len(histogram) < 5 {
		return 0, fmt.Errorf("%w (%d), may be incorrectly rendered", errTooFewChartColors, len(histogram))
	}

	whitePixels := histogram["248,248,248"] + histogram["255,255,255"]
	if whitePixels > 0.9 {
		return 0, fmt.Errorf("%w (%.1f%%), may be empty", errChartMostlyWhite, whitePixels*100)
	}

	blackPixels := histogram["0,0,0"] + histogram["8,8,8"]
	if blackPixels > 0.9 {
		return 0, fmt.Errorf("%w (%.1f%%), may have rendering issues", errChartMostlyBlack, blackPixels*100)
	}

	return whitePixels, nil
}

// generateBurndownProject creates a project burndown chart.
func (cg *ChartGenerator) generateBurndownProject(reader readers.Reader, outputPath string, relative bool) error {
	// Set viper config for relative mode
	viper.Set("relative", relative)
	viper.Set("resample", "year") // Default resampling for consistency

	// Call the actual burndown project generation using Python-compatible version
	err := modes.GenerateBurndownProjectPython(reader, outputPath, relative, "year")
	if err != nil {
		return fmt.Errorf("generate project burndown chart: %w", err)
	}

	return nil
}

// generateBurndownFile creates file-level burndown charts.
func (cg *ChartGenerator) generateBurndownFile(reader readers.Reader, outputPath string) error {
	// Use Python-compatible file burndown generation
	viper.Set("relative", false) // Default to absolute
	viper.Set("resample", "year")

	err := modes.GenerateBurndownFilePython(reader, outputPath, false, "year")
	if err != nil {
		return fmt.Errorf("generate file burndown chart: %w", err)
	}

	return nil
}

// generateBurndownPerson creates person-level burndown charts.
func (cg *ChartGenerator) generateBurndownPerson(reader readers.Reader, outputPath string) error {
	// Use regular burndown person function with nil time parameters for defaults
	err := modes.BurndownPerson(reader, outputPath, false, nil, nil, "year")
	if err != nil {
		return fmt.Errorf("generate person burndown chart: %w", err)
	}

	return nil
}

// generateOwnership creates code ownership visualization.
func (cg *ChartGenerator) generateOwnership(reader readers.Reader, outputPath string) error {
	// Call the ownership mode
	err := modes.OwnershipBurndown(reader, outputPath)
	if err != nil {
		return fmt.Errorf("generate ownership chart: %w", err)
	}

	return nil
}

// generateDevs creates developer statistics visualization.
func (cg *ChartGenerator) generateDevs(reader readers.Reader, outputPath string) error {
	// Call the devs mode with default max people (20)
	err := modes.Devs(reader, outputPath, 20)
	if err != nil {
		return fmt.Errorf("generate developers chart: %w", err)
	}

	return nil
}

// generateCouplesPeople creates people coupling visualization.
func (cg *ChartGenerator) generateCouplesPeople(reader readers.Reader, outputPath string) error {
	// Call the couples-people mode
	err := modes.CouplesPeople(reader, outputPath)
	if err != nil {
		return fmt.Errorf("generate people coupling chart: %w", err)
	}

	return nil
}

// generateCouplesFiles creates file coupling visualization.
func (cg *ChartGenerator) generateCouplesFiles(reader readers.Reader, outputPath string) error {
	// Call the couples-files mode
	err := modes.CouplesFiles(reader, outputPath)
	if err != nil {
		return fmt.Errorf("generate file coupling chart: %w", err)
	}

	return nil
}
