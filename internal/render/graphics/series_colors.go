package graphics

import (
	"image/color"
	"math"
)

// darkeningPerPass scales a reused palette entry towards black, and
// minDarkeningFactor keeps a third or fourth pass from collapsing into black.
const (
	darkeningPerPass   = 0.62
	minDarkeningFactor = 0.3
)

// DistinctSeriesColors returns one color per series for a chart whose series are
// categorical - repositories, developers, languages - and all visible at once.
//
// Up to the palette size the entries are spread across tab20 so that series
// stacked next to each other are far apart in hue. Beyond it the palette is
// walked in order and darkened once per completed pass, so a series that has to
// reuse an entry is still told apart from the earlier series wearing it. Plain
// modulo cycling (PythonLaboursColorPalette, which is what matplotlib does)
// gives the 21st series the color of the first, and an org-wide chart with
// thirty repositories then has several bands it is impossible to name.
//
// Age-band burndowns keep PythonLaboursColorPalette: their layers are ordered
// time buckets rather than categories, and their coloring is pinned to the
// Python original (see docs/RENDER_PARITY.md).
func DistinctSeriesColors(n int) []color.Color {
	if n <= 0 {
		return nil
	}

	palette := tab20Palette()
	if n > len(palette) {
		return cycledSeriesColors(palette, n)
	}

	colors := make([]color.Color, n)
	for i := range colors {
		index := 0
		if n > 1 {
			index = int(float64(i) * float64(len(palette)) / float64(n-1))
			if index >= len(palette) {
				index = len(palette) - 1
			}
		}

		colors[i] = palette[index]
	}

	return colors
}

func cycledSeriesColors(palette []color.Color, n int) []color.Color {
	colors := make([]color.Color, n)
	for i := range colors {
		colors[i] = darkenSeriesColor(palette[i%len(palette)], i/len(palette))
	}

	return colors
}

// darkenSeriesColor scales a color towards black by one step per completed pass
// over the palette.
func darkenSeriesColor(c color.Color, passes int) color.Color {
	if passes <= 0 {
		return c
	}

	factor := math.Max(math.Pow(darkeningPerPass, float64(passes)), minDarkeningFactor)

	rgba, ok := color.RGBAModel.Convert(c).(color.RGBA)
	if !ok {
		return c
	}

	return color.RGBA{
		R: scaleChannel(rgba.R, factor),
		G: scaleChannel(rgba.G, factor),
		B: scaleChannel(rgba.B, factor),
		A: rgba.A,
	}
}

func scaleChannel(channel uint8, factor float64) uint8 {
	return uint8(math.Round(float64(channel) * factor))
}
