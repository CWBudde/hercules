package graphics

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// ChartType represents different types of charts with their default dimensions
type ChartType int

const (
	ChartTypeDefault ChartType = iota // Standard rectangular charts (burndown, devs, etc.)
	ChartTypeSquare                   // Square charts (heatmaps, coupling matrices)
	ChartTypeCompact                  // Compact charts (ownership, simple plots)
	ChartTypeWide                     // Wide charts (timeline-heavy charts)
)

// defaultSizes defines the default dimensions for each chart type
var defaultSizes = map[ChartType][2]float64{
	ChartTypeDefault: {16.0, 8.0},  // Python labours default (16, 12) adapted for Go's typical 16x8
	ChartTypeSquare:  {12.0, 12.0}, // Square for heatmaps and matrices
	ChartTypeCompact: {10.0, 6.0},  // Compact for simple charts
	ChartTypeWide:    {16.0, 10.0}, // Wide for timeline-heavy charts
}

// GetPlotSizeInches returns the plot size in inches based on the --size flag and
// chart type. matplotlib-go renderers take figure sizes in inches, so this is the
// single sizing entry point modes should use.
func GetPlotSizeInches(chartType ChartType) (width, height float64) {
	sizeStr := viper.GetString("size")
	if sizeStr == "" {
		defaultSize := defaultSizes[chartType]
		return defaultSize[0], defaultSize[1]
	}

	width, height, err := parsePlotSizeFloats(sizeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v, using default size\n", err)
		defaultSize := defaultSizes[chartType]
		return defaultSize[0], defaultSize[1]
	}
	return width, height
}

// parsePlotSizeFloats parses a "width,height" size string (in inches) into
// floats, validating that the dimensions are positive and within sane bounds.
func parsePlotSizeFloats(sizeStr string) (width, height float64, err error) {
	parts := strings.Split(strings.TrimSpace(sizeStr), ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid size format '%s': expected 'width,height' (e.g., '12,9')", sizeStr)
	}

	width, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid width '%s': %w", parts[0], err)
	}

	height, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid height '%s': %w", parts[1], err)
	}

	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("dimensions must be positive: got width=%.1f, height=%.1f", width, height)
	}
	if width > 50 || height > 50 {
		return 0, 0, fmt.Errorf("dimensions too large: got width=%.1f, height=%.1f (max 50 inches)", width, height)
	}
	return width, height, nil
}
