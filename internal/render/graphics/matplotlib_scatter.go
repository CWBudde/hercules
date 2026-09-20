package graphics

import (
	"image/color"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"
)

type MatplotlibScatterPoint struct {
	X     float64
	Y     float64
	Label string
}

type MatplotlibScatterSeries struct {
	Name   string
	Points []MatplotlibScatterPoint
	Color  color.Color
	Size   float64
}

type MatplotlibScatterOptions struct {
	Title          string
	Subtitle       string
	XLabel         string
	YLabel         string
	Output         string
	WidthInches    float64
	HeightInches   float64
	ShowGrid       bool
	Legend         bool
	ZeroLine       bool
	AnnotateLabels bool
	// XTickLabels, when set, replaces the numeric x-axis with categorical tick
	// labels (one per index 0..len-1), mirroring gonum's NominalX.
	XTickLabels []string
	RotateX     bool
	FontSize    float64
}

// PlotScatterMatplotlib renders one or more scatter series via matplotlib-go,
// optionally annotating points with text labels and drawing a dashed y=0
// reference line.
func PlotScatterMatplotlib(series []MatplotlibScatterSeries, opts MatplotlibScatterOptions) error {
	if len(series) == 0 {
		return errNoScatterDataToPlot
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height, pythonTransparentFigureOptions(opts.FontSize)...)

	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return errCreateAxes
	}

	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)

	if opts.ShowGrid {
		ax.AddXGrid()
		ax.AddYGrid()
	}

	addMatplotlibScatterSeries(ax, series, opts.AnnotateLabels)
	configureMatplotlibScatterXAxis(ax, opts)

	if opts.ZeroLine {
		ax.AxHLine(0, core.HLineOptions{Dashes: []float64{5, 5}})
	}

	if opts.Legend {
		ax.AddLegend()
	}

	drawSubtitle(ax, opts.Subtitle)

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func addMatplotlibScatterSeries(
	ax *core.Axes,
	series []MatplotlibScatterSeries,
	annotateLabels bool,
) {
	palette := PythonLaboursColorPalette(len(series))
	for i, item := range series {
		if len(item.Points) == 0 {
			continue
		}

		x := make([]float64, len(item.Points))

		y := make([]float64, len(item.Points))
		for j, point := range item.Points {
			x[j] = point.X
			y[j] = point.Y
		}

		c := item.Color
		if c == nil {
			c = palette[i%len(palette)]
		}

		renderedColor := renderColor(c)

		size := item.Size
		if size <= 0 {
			size = 24
		}

		_, _ = ax.Scatter(x, y, core.ScatterOptions{
			Color: optional.Of(renderedColor), Size: optional.Of(size), Label: item.Name,
		})
		if annotateLabels {
			addMatplotlibScatterLabels(ax, item.Points, x, y)
		}
	}
}

func addMatplotlibScatterLabels(ax *core.Axes, points []MatplotlibScatterPoint, x, y []float64) {
	for i, point := range points {
		if point.Label == "" {
			continue
		}

		ax.Text(x[i], y[i], point.Label, core.TextOptions{
			FontSize: 9,
			Color:    render.Color{R: 0, G: 0, B: 0, A: 1},
			HAlign:   core.TextAlignLeft,
			VAlign:   core.TextVAlignBottom,
		})
	}
}

func configureMatplotlibScatterXAxis(ax *core.Axes, opts MatplotlibScatterOptions) {
	if len(opts.XTickLabels) == 0 {
		return
	}

	ax.SetXLim(-0.5, float64(len(opts.XTickLabels))-0.5)
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: indexPositions(len(opts.XTickLabels))}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), opts.XTickLabels...)}

	if opts.RotateX {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{
			Rotation: 45,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
		}
	}
}
