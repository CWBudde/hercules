package graphics

import "image/color"

// Options contains immutable visual settings for one renderer instance.
type Options struct {
	Theme      Theme
	FontSize   int
	Background string
	Size       string
	HideTitle  bool
}

// DefaultOptions returns Python-compatible visual defaults.
func DefaultOptions() Options {
	return Options{
		Theme:      DefaultTheme,
		FontSize:   int(pythonPlotDefaultFontSize),
		Background: "white",
	}
}

func (opts Options) normalized() Options {
	defaults := DefaultOptions()
	if opts.Theme.Name == "" {
		opts.Theme = defaults.Theme
	}

	if opts.FontSize <= 0 {
		opts.FontSize = defaults.FontSize
	}

	if opts.Background == "" {
		opts.Background = defaults.Background
	}

	return opts
}

// PlotFontSize returns the configured font size.
func (opts Options) PlotFontSize() float64 {
	return float64(opts.normalized().FontSize)
}

// Palette returns a fresh copy of the configured theme palette.
func (opts Options) Palette() []color.Color {
	normalized := opts.normalized()
	return normalized.Theme.GetColorPalette()
}
