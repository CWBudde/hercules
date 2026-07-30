package graphics

import (
	"image/color"
)

// ColorPalette is the legacy color palette for backwards compatibility
// It's updated automatically when themes are changed.
var ColorPalette = []color.Color{
	color.RGBA{R: 31, G: 119, B: 180, A: 255},  // Blue
	color.RGBA{R: 255, G: 127, B: 14, A: 255},  // Orange
	color.RGBA{R: 44, G: 160, B: 44, A: 255},   // Green
	color.RGBA{R: 214, G: 39, B: 40, A: 255},   // Red
	color.RGBA{R: 148, G: 103, B: 189, A: 255}, // Purple
	color.RGBA{R: 140, G: 86, B: 75, A: 255},   // Brown
	color.RGBA{R: 227, G: 119, B: 194, A: 255}, // Pink
	color.RGBA{R: 127, G: 127, B: 127, A: 255}, // Gray
	color.RGBA{R: 188, G: 189, B: 34, A: 255},  // Olive
	color.RGBA{R: 23, G: 190, B: 207, A: 255},  // Cyan
}

// HeatColor generates a color ranging from blue (cold, ratio=0) to red (hot, ratio=1)
// This is useful for heatmap-style visualizations where higher values should appear "hotter"
// This function now uses the current theme's heat color settings.
func HeatColor(ratio float64) color.Color {
	return CurrentTheme.GetHeatColor(ratio)
}

// GetColor returns a color from the current theme's palette by index.
func GetColor(index int) color.Color {
	palette := CurrentTheme.GetColorPalette()
	if len(palette) == 0 {
		// Fallback to default if somehow no colors are available
		return color.RGBA{R: 100, G: 100, B: 100, A: 255}
	}

	return palette[index%len(palette)]
}

// GetColorPalette returns the current theme's color palette.
func GetColorPalette() []color.Color {
	return CurrentTheme.GetColorPalette()
}

// GetMatplotlibBurndownColors returns the exact matplotlib colors for burndown charts
// Red (#d62728) for bottom/older layer, Blue (#1f77b4) for top/newer layer.
func GetMatplotlibBurndownColors(opacity uint8) []color.Color {
	return []color.Color{
		color.RGBA{R: 214, G: 39, B: 40, A: opacity},  // Red (C3) - matplotlib #d62728
		color.RGBA{R: 31, G: 119, B: 180, A: opacity}, // Blue (C0) - matplotlib #1f77b4
	}
}

// GetBurndownColors returns appropriate colors for burndown charts
// Uses matplotlib colors if matplotlib theme is active, otherwise uses theme colors.
func GetBurndownColors(numColors int) []color.Color {
	return GetBurndownColorsForTheme(CurrentTheme, numColors)
}

// GetBurndownColorsForTheme returns burndown colors without consulting mutable
// package theme state.
func GetBurndownColorsForTheme(theme Theme, numColors int) []color.Color {
	if theme.Name == "" {
		theme = DefaultTheme
	}

	opacity := uint8(float64(255) * theme.Chart.FillOpacity)

	// Use matplotlib colors for 2-layer burndown when matplotlib theme is active
	if numColors == 2 && theme.Name == "matplotlib" {
		return GetMatplotlibBurndownColors(opacity)
	}

	// Otherwise use standard theme colors
	themePalette := theme.GetColorPalette()
	colors := make([]color.Color, numColors)

	for i := range numColors {
		if i < len(themePalette) {
			if rgba, ok := themePalette[i].(color.RGBA); ok {
				colors[i] = color.RGBA{R: rgba.R, G: rgba.G, B: rgba.B, A: opacity}
			} else {
				colors[i] = themePalette[i]
			}
		} else {
			// Generate additional colors using simple fallback
			colors[i] = color.RGBA{
				R: boundedByte(i*60 + 120),
				G: boundedByte(i*80 + 80),
				B: boundedByte(i*40 + 180),
				A: opacity,
			}
		}
	}

	return colors
}

func boundedByte(value int) uint8 {
	return uint8(value % 255) // #nosec G115 - modulo bounds value to [0,254].
}
