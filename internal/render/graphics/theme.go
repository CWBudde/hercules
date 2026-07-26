package graphics

import (
	"fmt"
	"image/color"
	"math"
)

// Python labours render-style constants. These mirror matplotlib's savefig
// defaults as used by the reference `labours` tool and are the single source of
// truth for the matplotlib-go render path: every mode and bridge helper reads
// figure DPI, font family/size, and the fallback figure size from here rather
// than re-declaring the literals.
const (
	// pythonPlotDPI is matplotlib's savefig dpi; figure inches * DPI = pixels.
	pythonPlotDPI = 100
	// PythonPlotFontFamily is the font Python labours renders with (matplotlib's
	// bundled default face).
	PythonPlotFontFamily = "DejaVu Sans"
	// PythonPlotMonoFontFamily is the monospace face used for code/path labels.
	PythonPlotMonoFontFamily = "DejaVu Sans Mono"
	// pythonPlotDefaultFontSize is the fallback when --font-size is unset.
	pythonPlotDefaultFontSize = 12.0
	// PythonPlotDefaultWidthInches / PythonPlotDefaultHeightInches are the
	// fallback figure size (matplotlib labours' 16x12 default) used when both a
	// mode and the --size flag leave the dimensions unspecified.
	PythonPlotDefaultWidthInches  = 16.0
	PythonPlotDefaultHeightInches = 12.0
)

// PythonPlotFontSize resolves the configured font size (--font-size), falling
// back to the Python-parity default of 12pt.
func PythonPlotFontSize() float64 {
	return pythonPlotDefaultFontSize
}

// InchesToPixels converts a figure dimension in inches to device pixels at the
// labours render DPI (matplotlib savefig dpi=100), clamped to at least 1px.
func InchesToPixels(inches float64) int {
	pixels := int(math.Round(inches * pythonPlotDPI))
	if pixels < 1 {
		return 1
	}
	return pixels
}

// Theme represents a complete visual theme configuration
type Theme struct {
	Name         string     `yaml:"name" json:"name"`
	ColorPalette []ColorRGB `yaml:"colors" json:"colors"`
	Background   ColorRGB   `yaml:"background" json:"background"`
	Grid         GridStyle  `yaml:"grid" json:"grid"`
	Text         TextStyle  `yaml:"text" json:"text"`
	Chart        ChartStyle `yaml:"chart" json:"chart"`
	HeatMap      HeatStyle  `yaml:"heatmap" json:"heatmap"`
}

// ColorRGB represents an RGB color that can be serialized
type ColorRGB struct {
	R uint8 `yaml:"r" json:"r"`
	G uint8 `yaml:"g" json:"g"`
	B uint8 `yaml:"b" json:"b"`
	A uint8 `yaml:"a" json:"a"`
}

// ToColor converts ColorRGB to color.Color
func (c ColorRGB) ToColor() color.Color {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

// GridStyle configures grid appearance
type GridStyle struct {
	Show  bool     `yaml:"show" json:"show"`
	Color ColorRGB `yaml:"color" json:"color"`
	Width float64  `yaml:"width" json:"width"`
}

// TextStyle configures text appearance
type TextStyle struct {
	Font      string   `yaml:"font" json:"font"`
	Size      float64  `yaml:"size" json:"size"`
	Color     ColorRGB `yaml:"color" json:"color"`
	TitleSize float64  `yaml:"title_size" json:"title_size"`
	LabelSize float64  `yaml:"label_size" json:"label_size"`
}

// ChartStyle configures chart-specific styling
type ChartStyle struct {
	LineWidth   float64  `yaml:"line_width" json:"line_width"`
	BorderWidth float64  `yaml:"border_width" json:"border_width"`
	BorderColor ColorRGB `yaml:"border_color" json:"border_color"`
	FillOpacity float64  `yaml:"fill_opacity" json:"fill_opacity"`
	LegendShow  bool     `yaml:"legend_show" json:"legend_show"`
	LegendPos   string   `yaml:"legend_position" json:"legend_position"`
}

// HeatStyle configures heatmap-specific styling
type HeatStyle struct {
	ColdColor   ColorRGB `yaml:"cold_color" json:"cold_color"`
	HotColor    ColorRGB `yaml:"hot_color" json:"hot_color"`
	MidColor    ColorRGB `yaml:"mid_color" json:"mid_color"`
	UseMidPoint bool     `yaml:"use_mid_point" json:"use_mid_point"`
}

// Default themes
var (
	DefaultTheme       = newTheme("default", matplotlibPalette(), rgba(255, 255, 255, 255), grid(true, rgba(224, 224, 224, 255), 0.5), text("Arial", 10, rgba(0, 0, 0, 255), 14, 10), chart(1.0, 1.0, rgba(0, 0, 0, 255), 0.7, true, "right"), heat(rgba(31, 119, 180, 255), rgba(214, 39, 40, 255), rgba(148, 103, 189, 255), false))
	DarkTheme          = newTheme("dark", darkPalette(), rgba(35, 39, 42, 255), grid(true, rgba(68, 74, 79, 255), 0.5), text("Arial", 10, rgba(240, 240, 240, 255), 14, 10), chart(1.0, 1.0, rgba(200, 200, 200, 255), 0.8, true, "right"), heat(rgba(0, 100, 200, 255), rgba(255, 80, 80, 255), rgba(150, 50, 200, 255), false))
	MinimalTheme       = newTheme("minimal", minimalPalette(), rgba(255, 255, 255, 255), grid(false, rgba(240, 240, 240, 255), 0.25), text("Arial", 9, rgba(60, 60, 60, 255), 12, 8), chart(0.8, 0.5, rgba(120, 120, 120, 255), 0.9, false, "bottom"), heat(rgba(240, 240, 240, 255), rgba(60, 60, 60, 255), rgba(150, 150, 150, 255), true))
	VibranthColorTheme = newTheme("vibrant", vibrantPalette(), rgba(250, 250, 250, 255), grid(true, rgba(230, 230, 230, 255), 0.8), text("Arial", 11, rgba(40, 40, 40, 255), 16, 11), chart(1.5, 1.2, rgba(80, 80, 80, 255), 0.6, true, "right"), heat(rgba(0, 100, 255, 255), rgba(255, 50, 50, 255), rgba(255, 200, 0, 255), true))
	MatplotlibTheme    = newTheme("matplotlib", matplotlibPalette(), rgba(255, 255, 255, 255), grid(true, rgba(224, 224, 224, 255), 0.5), text("Arial", 10, rgba(0, 0, 0, 255), 14, 10), chart(1.0, 1.0, rgba(0, 0, 0, 255), 0.7, true, "right"), heat(rgba(31, 119, 180, 255), rgba(214, 39, 40, 255), rgba(148, 103, 189, 255), false))
)

//nolint:unparam // Theme literals pass explicit alpha values for readability and future theme data.
func rgba(r, g, b, a uint8) ColorRGB { return ColorRGB{R: r, G: g, B: b, A: a} }

func grid(show bool, color ColorRGB, width float64) GridStyle {
	return GridStyle{Show: show, Color: color, Width: width}
}

//nolint:unparam // Built-in themes currently share a font, but external themes do not have to.
func text(font string, size float64, color ColorRGB, titleSize, labelSize float64) TextStyle {
	return TextStyle{Font: font, Size: size, Color: color, TitleSize: titleSize, LabelSize: labelSize}
}

func chart(lineWidth, borderWidth float64, borderColor ColorRGB, fillOpacity float64, legendShow bool, legendPos string) ChartStyle {
	return ChartStyle{LineWidth: lineWidth, BorderWidth: borderWidth, BorderColor: borderColor, FillOpacity: fillOpacity, LegendShow: legendShow, LegendPos: legendPos}
}

func heat(coldColor, hotColor, midColor ColorRGB, useMidPoint bool) HeatStyle {
	return HeatStyle{ColdColor: coldColor, HotColor: hotColor, MidColor: midColor, UseMidPoint: useMidPoint}
}

func newTheme(name string, palette []ColorRGB, background ColorRGB, gridStyle GridStyle, textStyle TextStyle, chartStyle ChartStyle, heatStyle HeatStyle) Theme {
	return Theme{Name: name, ColorPalette: palette, Background: background, Grid: gridStyle, Text: textStyle, Chart: chartStyle, HeatMap: heatStyle}
}

func matplotlibPalette() []ColorRGB {
	return []ColorRGB{rgba(31, 119, 180, 255), rgba(255, 127, 14, 255), rgba(44, 160, 44, 255), rgba(214, 39, 40, 255), rgba(148, 103, 189, 255), rgba(140, 86, 75, 255), rgba(227, 119, 194, 255), rgba(127, 127, 127, 255), rgba(188, 189, 34, 255), rgba(23, 190, 207, 255)}
}

func darkPalette() []ColorRGB {
	return []ColorRGB{rgba(99, 165, 255, 255), rgba(255, 159, 64, 255), rgba(75, 192, 75, 255), rgba(255, 99, 132, 255), rgba(186, 148, 255, 255), rgba(200, 150, 130, 255), rgba(255, 159, 226, 255), rgba(180, 180, 180, 255), rgba(220, 220, 100, 255), rgba(100, 220, 240, 255)}
}

func minimalPalette() []ColorRGB {
	return []ColorRGB{rgba(70, 70, 70, 255), rgba(150, 150, 150, 255), rgba(200, 200, 200, 255), rgba(100, 100, 100, 255), rgba(50, 50, 50, 255), rgba(180, 180, 180, 255), rgba(120, 120, 120, 255), rgba(80, 80, 80, 255), rgba(160, 160, 160, 255), rgba(110, 110, 110, 255)}
}

func vibrantPalette() []ColorRGB {
	return []ColorRGB{rgba(255, 0, 128, 255), rgba(0, 255, 128, 255), rgba(128, 0, 255, 255), rgba(255, 128, 0, 255), rgba(0, 128, 255, 255), rgba(255, 255, 0, 255), rgba(255, 0, 0, 255), rgba(0, 255, 0, 255), rgba(0, 255, 255, 255), rgba(255, 0, 255, 255)}
}

// BuiltinThemes contains all built-in themes
var BuiltinThemes = map[string]Theme{
	"default":    DefaultTheme,
	"dark":       DarkTheme,
	"minimal":    MinimalTheme,
	"vibrant":    VibranthColorTheme,
	"matplotlib": MatplotlibTheme,
}

// GetColorPalette returns the color palette as color.Color slice
func (t *Theme) GetColorPalette() []color.Color {
	colors := make([]color.Color, len(t.ColorPalette))
	for i, c := range t.ColorPalette {
		colors[i] = c.ToColor()
	}
	return colors
}

// GetHeatColor generates a heat map color based on ratio and theme settings
func (t *Theme) GetHeatColor(ratio float64) color.Color {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}

	cold := t.HeatMap.ColdColor
	hot := t.HeatMap.HotColor

	switch {
	case t.HeatMap.UseMidPoint && ratio <= 0.5:
		// Interpolate from cold to mid
		mid := t.HeatMap.MidColor
		ratio *= 2 // Scale to 0-1 for cold->mid
		r := uint8(float64(cold.R) + ratio*(float64(mid.R)-float64(cold.R)))
		g := uint8(float64(cold.G) + ratio*(float64(mid.G)-float64(cold.G)))
		b := uint8(float64(cold.B) + ratio*(float64(mid.B)-float64(cold.B)))
		return color.RGBA{R: r, G: g, B: b, A: 255}
	case t.HeatMap.UseMidPoint:
		// Interpolate from mid to hot
		mid := t.HeatMap.MidColor
		ratio = (ratio - 0.5) * 2 // Scale to 0-1 for mid->hot
		r := uint8(float64(mid.R) + ratio*(float64(hot.R)-float64(mid.R)))
		g := uint8(float64(mid.G) + ratio*(float64(hot.G)-float64(mid.G)))
		b := uint8(float64(mid.B) + ratio*(float64(hot.B)-float64(mid.B)))
		return color.RGBA{R: r, G: g, B: b, A: 255}
	default:
		// Direct interpolation from cold to hot
		r := uint8(float64(cold.R) + ratio*(float64(hot.R)-float64(cold.R)))
		g := uint8(float64(cold.G) + ratio*(float64(hot.G)-float64(cold.G)))
		b := uint8(float64(cold.B) + ratio*(float64(hot.B)-float64(cold.B)))
		return color.RGBA{R: r, G: g, B: b, A: 255}
	}
}

// Validate checks if theme configuration is valid
func (t *Theme) Validate() error {
	if len(t.ColorPalette) == 0 {
		return fmt.Errorf("theme must have at least one color in palette")
	}

	if t.Name == "" {
		return fmt.Errorf("theme must have a name")
	}

	if t.Text.Size <= 0 {
		return fmt.Errorf("text size must be positive")
	}

	if t.Chart.FillOpacity < 0 || t.Chart.FillOpacity > 1 {
		return fmt.Errorf("fill opacity must be between 0 and 1")
	}

	return nil
}

// CurrentTheme holds the active theme (defaults to DefaultTheme)
var CurrentTheme = DefaultTheme
