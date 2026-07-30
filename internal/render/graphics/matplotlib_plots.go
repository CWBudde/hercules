package graphics

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"time"

	matcolor "github.com/cwbudde/matplotlib-go/color"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"
)

type MatplotlibTimeAreaSeries struct {
	Label  string
	Values []float64
	Color  color.Color
}

type MatplotlibTextLabel struct {
	X        float64
	Y        float64
	Text     string
	HAlign   core.TextAlign
	FontSize float64
	// BackgroundColor draws a filled rectangle behind the label, mirroring
	// matplotlib's `text(..., backgroundcolor=...)`. Leave nil for no fill.
	BackgroundColor color.Color
}

type MatplotlibTimeAreaOptions struct {
	Title        string
	Subtitle     string
	XLabel       string
	YLabel       string
	Output       string
	WidthInches  float64
	HeightInches float64
	Stacked      bool
	HideY        bool
	ShowGrid     bool
	Legend       bool
	LegendLeft   bool
	LegendTop    bool
	HideFrame    bool
	AutoXMargin  bool
	// FullNumberYTicks disables scientific/offset notation and renders the
	// complete magnitude in every y-axis tick label.
	FullNumberYTicks bool
	LegendFace       color.Color
	Alpha            float64
	YMin             float64
	YMax             float64
	Baselines        [][]float64
	TextLabels       []MatplotlibTextLabel
	FontSize         float64
}

type MatplotlibBarOptions struct {
	Title        string
	XLabel       string
	YLabel       string
	Output       string
	WidthInches  float64
	HeightInches float64
	RotateX      bool
	Color        color.Color
	DisableGrid  bool
	Opaque       bool
	DefaultStyle bool
	ManualXLim   bool
	XMin         float64
	XMax         float64
	YMax         float64
	// BarLabels, when set (len == values), draws a rotated text annotation above
	// each bar with a non-empty label. Mirrors gonum's per-bar XYLabels.
	BarLabels     []string
	BarLabelAngle float64
	FontSize      float64
}

type MatplotlibGroupedBarSeries struct {
	Name   string
	Values []float64
	Color  color.Color
}

type MatplotlibGroupedBarOptions struct {
	Title        string
	Subtitle     string
	XLabel       string
	YLabel       string
	Output       string
	WidthInches  float64
	HeightInches float64
	RotateX      bool
	FontSize     float64
}

type MatplotlibLineSeries struct {
	Name   string
	X      []float64
	Y      []float64
	Color  color.Color
	Marker bool
	// Dashes sets a dash pattern (e.g. {5, 5}); nil draws a solid line.
	Dashes []float64
	// Fill shades the area between the series and y=0.
	Fill bool
}

type MatplotlibLineOptions struct {
	Title        string
	XLabel       string
	YLabel       string
	Output       string
	WidthInches  float64
	HeightInches float64
	ShowGrid     bool
	Legend       bool
	FontSize     float64
}

type MatplotlibHeatmapOptions struct {
	Title        string
	Output       string
	Colormap     string
	WidthInches  float64
	HeightInches float64
	XLabelLimit  int
	YLabelLimit  int
	FontSize     float64
}

// drawSubtitle renders an optional secondary caption line centered just below
// the axes title, inside the top of the plot area. It is drawn last (on top of
// the plotted data) so it stays legible. An empty subtitle is a no-op, so the
// call is safe on every plot even though only sentiment charts currently set it.
func drawSubtitle(ax *core.Axes, subtitle string) {
	if ax == nil || subtitle == "" {
		return
	}
	ax.Text(0.5, 0.98, subtitle, core.TextOptions{
		Coords:   core.Coords(core.CoordAxes),
		FontSize: 9,
		Color:    render.Color{R: 0.5, G: 0.5, B: 0.5, A: 1},
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignTop,
	})
}

func PlotTimeAreasMatplotlib(dates []time.Time, series []MatplotlibTimeAreaSeries, opts MatplotlibTimeAreaOptions) error {
	if len(dates) == 0 {
		return fmt.Errorf("no dates to plot")
	}
	if len(series) == 0 {
		return fmt.Errorf("no series to plot")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(
		width,
		height,
		pythonTransparentFigureOptions(opts.FontSize)...,
	)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	configureTimeAreaAxes(ax, dates, opts)
	x := unixTimeValues(dates)
	colors, matrix, labels, err := prepareTimeAreaSeries(series, len(dates))
	if err != nil {
		return err
	}
	alpha := opts.Alpha
	if alpha <= 0 || alpha > 1 {
		alpha = 1
	}
	if err := plotTimeAreaSeries(ax, x, series, colors, matrix, labels, opts, alpha); err != nil {
		return err
	}
	configureTimeAreaYTicks(ax, matrix, opts)
	addTimeAreaTextLabels(ax, opts.TextLabels)
	configureTimeAreaLegend(ax, opts)
	if opts.HideFrame {
		ax.XAxis.ShowSpine = false
		ax.YAxis.ShowSpine = false
	}

	drawSubtitle(ax, opts.Subtitle)

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func unixTimeValues(dates []time.Time) []float64 {
	values := make([]float64, len(dates))
	for i, date := range dates {
		values[i] = float64(date.Unix())
	}
	return values
}

func prepareTimeAreaSeries(
	series []MatplotlibTimeAreaSeries,
	pointCount int,
) ([]render.Color, [][]float64, []string, error) {
	colors := make([]render.Color, len(series))
	matrix := make([][]float64, len(series))
	labels := make([]string, len(series))
	palette := PythonLaboursColorPalette(len(series))
	for i, item := range series {
		if len(item.Values) != pointCount {
			return nil, nil, nil, fmt.Errorf(
				"series %q has %d values for %d dates", item.Label, len(item.Values), pointCount,
			)
		}
		itemColor := item.Color
		if itemColor == nil {
			itemColor = palette[i%len(palette)]
		}
		colors[i] = renderColor(itemColor)
		matrix[i] = append([]float64(nil), item.Values...)
		labels[i] = item.Label
	}
	return colors, matrix, labels, nil
}

func plotTimeAreaSeries(
	ax *core.Axes,
	x []float64,
	series []MatplotlibTimeAreaSeries,
	colors []render.Color,
	matrix [][]float64,
	labels []string,
	opts MatplotlibTimeAreaOptions,
	alpha float64,
) error {
	edgeWidth := 0.0
	if opts.Stacked {
		ax.StackPlot(x, matrix, core.StackPlotOptions{
			Colors: colors, Labels: labels, Alpha: optional.Of(alpha), EdgeWidth: optional.Of(edgeWidth),
		})
		return nil
	}
	zero := make([]float64, len(x))
	for i, item := range series {
		baseline, err := timeAreaBaseline(opts.Baselines, i, zero)
		if err != nil {
			return err
		}
		color := colors[i]
		_, _ = ax.FillBetween(x, item.Values, baseline, core.FillOptions{
			Color: optional.Of(color), Alpha: optional.Of(alpha), EdgeWidth: optional.Of(edgeWidth), Label: item.Label,
		})
	}
	return nil
}

func timeAreaBaseline(baselines [][]float64, index int, zero []float64) ([]float64, error) {
	if index >= len(baselines) {
		return zero, nil
	}
	if len(baselines[index]) != len(zero) {
		return nil, fmt.Errorf(
			"baseline %d has %d values for %d dates", index, len(baselines[index]), len(zero),
		)
	}
	return baselines[index], nil
}

func configureTimeAreaYTicks(ax *core.Axes, matrix [][]float64, opts MatplotlibTimeAreaOptions) {
	if !opts.FullNumberYTicks {
		return
	}
	maxY := opts.YMax
	if maxY <= opts.YMin && opts.Stacked {
		maxY = stackedSumMax(matrix) * 1.05
		ax.SetYLim(0, math.Max(maxY, 1))
	}
	ConfigureLineCountYAxis(ax, maxY)
}

func addTimeAreaTextLabels(ax *core.Axes, labels []MatplotlibTextLabel) {
	for _, label := range labels {
		fontSize := label.FontSize
		if fontSize == 0 {
			fontSize = 12
		}
		textOpts := core.TextOptions{
			FontSize: fontSize,
			Color:    render.Color{R: 0, G: 0, B: 0, A: 1},
			HAlign:   label.HAlign,
			VAlign:   core.TextVAlignMiddle,
		}
		if label.BackgroundColor != nil {
			fill := renderColor(label.BackgroundColor)
			textOpts.BBox = optional.Of(core.TextBBoxOptions{FaceColor: fill, EdgeColor: fill, Padding: 2})
		}
		ax.Text(label.X, label.Y, label.Text, textOpts)
	}
}

func configureTimeAreaLegend(ax *core.Axes, opts MatplotlibTimeAreaOptions) {
	if !opts.Legend {
		return
	}
	legend := ax.AddLegend()
	if opts.LegendLeft && opts.LegendTop {
		legend.Location = core.LegendUpperLeft
	} else if opts.LegendLeft {
		legend.Location = core.LegendLowerLeft
	}
	if opts.LegendFace != nil {
		face := renderColor(opts.LegendFace)
		legend.BackgroundColor = face
		legend.BorderColor = face
	}
}

func PlotLineChartMatplotlib(series []MatplotlibLineSeries, opts MatplotlibLineOptions) error {
	if len(series) == 0 {
		return fmt.Errorf("no line data to plot")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(
		width,
		height,
		pythonTransparentFigureOptions(opts.FontSize)...,
	)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)
	if opts.ShowGrid {
		ax.AddXGrid()
		ax.AddYGrid()
	}
	if err := addMatplotlibLineSeries(ax, series); err != nil {
		return err
	}
	if opts.Legend {
		ax.AddLegend()
	}
	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func addMatplotlibLineSeries(ax *core.Axes, series []MatplotlibLineSeries) error {
	palette := PythonLaboursColorPalette(len(series))
	for i, item := range series {
		if len(item.X) == 0 || len(item.Y) == 0 {
			continue
		}
		if len(item.X) != len(item.Y) {
			return fmt.Errorf("line series %q x/y length mismatch", item.Name)
		}
		c := item.Color
		if c == nil {
			c = palette[i%len(palette)]
		}
		color := renderColor(c)
		addMatplotlibLine(ax, item, color)
	}
	return nil
}

func addMatplotlibLine(ax *core.Axes, item MatplotlibLineSeries, color render.Color) {
	lineWidth := 2.0
	if item.Fill {
		fillAlpha, fillEdge := 0.3, 0.0
		zero := make([]float64, len(item.Y))
		_, _ = ax.FillBetween(item.X, item.Y, zero, core.FillOptions{
			Color: optional.Of(color), Alpha: optional.Of(fillAlpha), EdgeWidth: optional.Of(fillEdge),
		})
	}

	_, _ = ax.Plot(item.X, item.Y, core.PlotOptions{
		Color: optional.Of(color), LineWidth: optional.Of(lineWidth), Dashes: item.Dashes, Label: item.Name,
	})
	if item.Marker {
		size := 24.0
		_, _ = ax.Scatter(item.X, item.Y, core.ScatterOptions{Color: optional.Of(color), Size: optional.Of(size), Label: ""})
	}
}

func PlotHeatmapMatplotlib(matrix [][]float64, rowLabels, colLabels []string, opts MatplotlibHeatmapOptions) error {
	if err := ValidateHeatMap(matrix, rowLabels, colLabels); err != nil {
		return err
	}

	RegisterPythonLaboursHeatmapColormaps()

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height)
	fig.RC.XTickLabelFontSize = 8
	fig.RC.YTickLabelFontSize = 8
	gs := fig.GridSpec(
		1, 1,
		core.WithGridSpecPadding(0.132, 0.893, 0.087, 0.970),
		core.WithGridSpecSpacing(0, 0),
	)
	ax := gs.Cell(0, 0).AddAxes()
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}

	ax.SetTitle(opts.Title)
	cmap := opts.Colormap
	if cmap == "" {
		cmap = "Reds"
	}
	vmin := 0.0
	vmax := maxMatrixFloat64(matrix)
	img := ax.ImShow(matrix, core.ImShowOptions{
		Colormap: optional.Of(cmap),
		VMin:     optional.Of(vmin),
		VMax:     optional.Of(vmax),
		Aspect:   optional.Of(core.ImageAspect("auto")),
		Origin:   optional.Of(core.ImageOriginUpper),
	})
	if img == nil {
		return fmt.Errorf("failed to create heatmap image")
	}

	configureMatplotlibHeatmapTicks(ax, rowLabels, colLabels, opts)
	addMatplotlibHeatmapColorbar(fig, cmap, vmin, vmax)

	return saveMatplotlibFigureWithoutTightLayout(fig, opts.Output, width, height, render.Color{R: 1, G: 1, B: 1, A: 1})
}

func addMatplotlibHeatmapColorbar(fig *core.Figure, colormap string, vmin, vmax float64) {
	// These bounds match matplotlib's fig.colorbar(...); fig.tight_layout()
	// output for the Python labours parity heatmaps.
	gs := fig.GridSpec(
		1, 1,
		core.WithGridSpecPadding(0.927, 0.965, 0.142, 0.915),
		core.WithGridSpecSpacing(0, 0),
	)
	ax := gs.Cell(0, 0).AddAxes()
	if ax == nil {
		return
	}

	ax.ShowFrame = false
	ax.SetXLim(0, 1)
	ax.SetYLim(vmin, vmax)
	if ax.XAxis != nil {
		ax.XAxis.ShowSpine = false
		ax.XAxis.ShowTicks = false
		ax.XAxis.ShowLabels = false
	}
	if ax.YAxis != nil {
		ax.YAxis.ShowSpine = false
		ax.YAxis.ShowTicks = false
		ax.YAxis.ShowLabels = false
		ax.YAxis.MinorLocator = nil
	}
	if right := ax.RightAxis(); right != nil {
		right.MinorLocator = nil
	}
	_ = ax.SetYTickLabelPosition("right")
	_ = ax.SetYLabelPosition("right")
	ax.Add(&core.Colorbar{
		Colormap:    colormap,
		Alpha:       1,
		BorderColor: render.Color{R: 0.2, G: 0.2, B: 0.2, A: 0.9},
		BorderWidth: 1,
	})
}

func PlotBarChartMatplotlib(labels []string, values []float64, opts MatplotlibBarOptions) error {
	if len(labels) == 0 || len(values) == 0 {
		return fmt.Errorf("no bar data to plot")
	}
	if len(labels) != len(values) {
		return fmt.Errorf("bar labels and values length mismatch")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	figureOptions := pythonTransparentFigureOptions(opts.FontSize)
	if opts.DefaultStyle {
		figureOptions = nil
	}
	fig := core.NewFigure(width, height, figureOptions...)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)
	if !opts.DisableGrid {
		ax.AddYGrid()
	}

	x := indexPositions(len(values))
	barColor := opts.Color
	if barColor == nil {
		barColor = PythonLaboursColorPalette(1)[0]
	}
	renderedColor := renderColor(barColor)

	_, err := ax.Bar(x, values, core.BarOptions{Color: optional.Of(renderedColor)})
	if err != nil {
		return fmt.Errorf("failed to plot bars: %w", err)
	}
	addMatplotlibBarLabels(ax, x, values, opts)
	configureMatplotlibBarAxes(ax, labels, values, x, opts)
	return saveMatplotlibBarFigure(fig, opts, width, height)
}

func addMatplotlibBarLabels(
	ax *core.Axes,
	x, values []float64,
	opts MatplotlibBarOptions,
) {
	angle := opts.BarLabelAngle
	if angle == 0 {
		angle = 70
	}
	for i, label := range opts.BarLabels {
		if i >= len(values) || label == "" {
			continue
		}
		ax.Text(x[i], values[i], label, core.TextOptions{
			FontSize: 7,
			Color:    render.Color{R: 0, G: 0, B: 0, A: 1},
			HAlign:   core.TextAlignLeft,
			VAlign:   core.TextVAlignBottom,
			Angle:    angle,
		})
	}
}

func configureMatplotlibBarAxes(
	ax *core.Axes,
	labels []string,
	values, ticks []float64,
	opts MatplotlibBarOptions,
) {
	if opts.ManualXLim {
		ax.SetXLim(opts.XMin, opts.XMax)
	} else {
		ax.SetXLim(-0.5, float64(len(values))-0.5)
	}
	if opts.YMax > 0 {
		ax.SetYLim(0, opts.YMax)
	} else {
		ax.SetYLim(0, math.Max(maxFloat64(values)*1.05, 1))
	}
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), labels...)}
	if opts.RotateX {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{
			Rotation: 45,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
		}
	}
}

func saveMatplotlibBarFigure(fig *core.Figure, opts MatplotlibBarOptions, width, height int) error {
	if opts.Opaque {
		return saveMatplotlibFigure(fig, opts.Output, width, height, render.Color{R: 1, G: 1, B: 1, A: 1})
	}
	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func PlotGroupedBarChartMatplotlib(labels []string, series []MatplotlibGroupedBarSeries, opts MatplotlibGroupedBarOptions) error {
	if len(labels) == 0 || len(series) == 0 {
		return fmt.Errorf("no grouped bar data to plot")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(
		width,
		height,
		pythonTransparentFigureOptions(opts.FontSize)...,
	)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)
	ax.AddYGrid()

	barWidth := 0.8 / float64(len(series))
	maxValue, err := addGroupedBarSeries(ax, labels, series, barWidth)
	if err != nil {
		return err
	}
	ticks := indexPositions(len(labels))
	ax.SetXLim(-0.5, float64(len(labels))-0.5)
	ax.SetYLim(0, math.Max(maxValue*1.05, 1))
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), labels...)}
	if opts.RotateX {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 45, AutoAlign: true}
	}
	ax.AddLegend()

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func addGroupedBarSeries(
	ax *core.Axes,
	labels []string,
	series []MatplotlibGroupedBarSeries,
	barWidth float64,
) (float64, error) {
	palette := PythonLaboursColorPalette(len(series))
	maxValue := 0.0
	for i, item := range series {
		if len(item.Values) != len(labels) {
			return 0, fmt.Errorf("bar series %q has %d values for %d labels", item.Name, len(item.Values), len(labels))
		}
		x := make([]float64, len(labels))
		offset := (float64(i) - float64(len(series)-1)/2) * barWidth
		for j, value := range item.Values {
			x[j] = float64(j) + offset
			if value > maxValue {
				maxValue = value
			}
		}
		seriesColor := palette[i%len(palette)]
		if item.Color != nil {
			seriesColor = item.Color
		}
		color := renderColor(seriesColor)
		_, _ = ax.Bar(x, item.Values, core.BarOptions{
			Color: optional.Of(color),
			Width: optional.Of(barWidth),
			Label: item.Name,
		})
	}
	return maxValue, nil
}

func indexPositions(count int) []float64 {
	positions := make([]float64, count)
	for i := range positions {
		positions[i] = float64(i)
	}
	return positions
}

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
		return fmt.Errorf("no scatter data to plot")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height, pythonTransparentFigureOptions(opts.FontSize)...)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
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

		_, _ = ax.Scatter(x, y, core.ScatterOptions{Color: optional.Of(renderedColor), Size: optional.Of(size), Label: item.Name})
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

// PlotStackedBarChartMatplotlib renders categorical stacked bars (one stack per
// label) using per-bar baselines, mirroring gonum's BarChart.StackOn chains.
func PlotStackedBarChartMatplotlib(labels []string, series []MatplotlibGroupedBarSeries, opts MatplotlibGroupedBarOptions) error {
	if len(labels) == 0 || len(series) == 0 {
		return fmt.Errorf("no stacked bar data to plot")
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height, pythonTransparentFigureOptions(opts.FontSize)...)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)
	ax.AddYGrid()

	x := indexPositions(len(labels))
	maxTotal, err := addMatplotlibStackedBars(ax, x, len(labels), series)
	if err != nil {
		return err
	}
	ax.SetXLim(-0.5, float64(len(labels))-0.5)
	ax.SetYLim(0, math.Max(maxTotal*1.05, 1))
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: append([]float64(nil), x...)}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), labels...)}
	if opts.RotateX {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{
			Rotation: 45,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
		}
	}
	ax.AddLegend()

	drawSubtitle(ax, opts.Subtitle)

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func addMatplotlibStackedBars(
	ax *core.Axes,
	x []float64,
	labelCount int,
	series []MatplotlibGroupedBarSeries,
) (float64, error) {
	baseline := make([]float64, labelCount)
	palette := PythonLaboursColorPalette(len(series))
	for i, item := range series {
		if len(item.Values) != labelCount {
			return 0, fmt.Errorf(
				"stacked bar series %q has %d values for %d labels",
				item.Name, len(item.Values), labelCount,
			)
		}
		seriesColor := palette[i%len(palette)]
		if item.Color != nil {
			seriesColor = item.Color
		}
		color := renderColor(seriesColor)

		_, _ = ax.Bar(x, item.Values, core.BarOptions{
			Color: optional.Of(color), Baselines: append([]float64(nil), baseline...), Label: item.Name,
		})
		for j, value := range item.Values {
			baseline[j] += value
		}
	}
	return maxFloat64(baseline), nil
}

type MatplotlibDevsEffortsOptions struct {
	Title        string
	Output       string
	WidthInches  float64
	HeightInches float64
	FontSize     float64
}

// PlotDevsEffortsMatplotlib renders the "Efforts through time" chart as
// non-negative cumulative effort layers over a shared date x-axis. The last
// layer is the aggregated "others" series.
func PlotDevsEffortsMatplotlib(
	dates []time.Time,
	cumLayers [][]float64,
	labels []string,
	opts MatplotlibDevsEffortsOptions,
) error {
	if len(dates) < 2 {
		return fmt.Errorf("not enough dates to plot devs-efforts time series")
	}
	if len(cumLayers) == 0 {
		return fmt.Errorf("no effort layers to plot")
	}

	x := make([]float64, len(dates))
	for i, date := range dates {
		x[i] = float64(date.Unix())
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height, pythonTransparentFigureOptions(opts.FontSize)...)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)

	renderedLayers := nonNegativeEffortLayers(cumLayers)
	edge := 0.0
	alpha := 1.0
	ax.StackPlot(x, renderedLayers, core.StackPlotOptions{
		Colors:    effortLayerColors(len(cumLayers)),
		Labels:    labels,
		EdgeWidth: optional.Of(edge),
		Alpha:     optional.Of(alpha),
	})

	ax.SetXLim(x[0], x[len(x)-1])

	yRange := devsEffortsYRange(renderedLayers)
	ax.SetYLim(yRange.AxisMin, yRange.AxisMax)
	ConfigureLineCountYAxis(ax, yRange.DataMax)

	ticks, tlabels := timeAxisDateTicks(dates, "")
	if len(ticks) > 0 {
		ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
		ax.XAxis.Formatter = ticker.FixedFormatter{Labels: tlabels}
		if shouldRotateDateLabels(tlabels) {
			ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 30, AutoAlign: true}
		}
	}

	legend := ax.AddLegend()
	legend.Location = core.LegendUpperLeft
	legend.NumColumns = 2

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

func effortLayerColors(count int) []render.Color {
	palette := tab20Palette()

	colors := make([]render.Color, count)
	for i := range colors {
		colors[i] = renderColor(palette[i%len(palette)])
	}

	return colors
}

func nonNegativeEffortLayers(layers [][]float64) [][]float64 {
	result := make([][]float64, len(layers))
	for i, layer := range layers {
		result[i] = make([]float64, len(layer))
		for j, value := range layer {
			if value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
				result[i][j] = value
			}
		}
	}

	return result
}

type devsEffortsRange struct {
	DataMin float64
	DataMax float64
	AxisMin float64
	AxisMax float64
}

// devsEffortsYRange returns both the rendered cumulative-stack extent and the
// padded axis range derived from it. The baseline is always zero; a small top
// margin keeps the highest polygon clear of the frame without reserving space
// for data that is not rendered.
func devsEffortsYRange(cumLayers [][]float64) devsEffortsRange {
	dataMax := stackedSumMax(cumLayers)

	if dataMax <= 0 || math.IsInf(dataMax, 0) || math.IsNaN(dataMax) {
		dataMax = 1
	}

	return devsEffortsRange{
		DataMin: 0,
		DataMax: dataMax,
		AxisMin: 0,
		AxisMax: dataMax * 1.02,
	}
}

type MatplotlibParallelCoordinatesSeries struct {
	// Values holds the normalized y position (0..1) of one developer at each
	// vertical axis, ordered left to right.
	Values []float64
}

type MatplotlibParallelCoordinatesOptions struct {
	Title        string
	Output       string
	WidthInches  float64
	HeightInches float64
	FontSize     float64
	// Axes is the number of vertical axes (Python uses 5).
	Axes int
}

// PlotParallelCoordinatesMatplotlib renders the Python labours devs-parallel
// chart: each developer is a cubic-spline curve flowing across the vertical
// axes, drawn as short segments tinted along the viridis colormap.
func PlotParallelCoordinatesMatplotlib(series []MatplotlibParallelCoordinatesSeries, opts MatplotlibParallelCoordinatesOptions) error {
	if len(series) == 0 {
		return fmt.Errorf("no series to plot")
	}
	axesCount := opts.Axes
	if axesCount <= 0 {
		axesCount = 5
	}

	width, height := pythonPlotPixelSize(defaultPlotWidth(opts.WidthInches), defaultPlotHeight(opts.HeightInches))
	fig := core.NewFigure(width, height, pythonTransparentFigureOptions(opts.FontSize)...)
	ax := fig.AddSubplot(1, 1, 1)
	if ax == nil {
		return fmt.Errorf("failed to create axes")
	}
	ax.SetTitle(opts.Title)

	cmap := matcolor.LookupColormap("viridis")
	const perGap = 20
	// Slightly wider than matplotlib's default so the per-segment gradient reads
	// as a continuous ribbon instead of stippling at 1px.
	lineWidth := 1.5
	for _, item := range series {
		px, py := parallelSplinePolyline(item.Values, perGap)
		if len(px) < 2 {
			continue
		}
		segments := len(px) - 1
		for k := 0; k < segments; k++ {
			t := 0.0
			if segments > 1 {
				t = float64(k) / float64(segments-1)
			}
			c := cmap.At(t)
			if _, err := ax.Plot(px[k:k+2], py[k:k+2], core.PlotOptions{Color: optional.Of(c), LineWidth: optional.Of(lineWidth)}); err != nil {
				return fmt.Errorf("failed to plot parallel coordinates segment: %w", err)
			}
		}
	}

	ax.SetXLim(0, float64(axesCount)+1)
	ax.SetYLim(-0.1, 1.1)

	return saveMatplotlibFigure(fig, opts.Output, width, height)
}

// parallelSplinePolyline expands per-axis y values into a smooth polyline using
// the same zero-slope cubic between adjacent axes that Python labours uses.
func parallelSplinePolyline(values []float64, perGap int) (xs, ys []float64) {
	if len(values) < 2 {
		return nil, nil
	}
	if perGap < 1 {
		perGap = 1
	}
	for i := 0; i < len(values)-1; i++ {
		x1 := float64(i + 1)
		y1 := values[i]
		x2 := float64(i + 2)
		y2 := values[i+1]
		a, b, c, d := solveSplineEquations(x1, y1, x2, y2)
		for j := 0; j <= perGap; j++ {
			if i > 0 && j == 0 {
				continue // the join point was already emitted by the previous gap
			}
			t := x1 + (x2-x1)*float64(j)/float64(perGap)
			xs = append(xs, t)
			ys = append(ys, a*t*t*t+b*t*t+c*t+d)
		}
	}
	return xs, ys
}

func solveSplineEquations(x1, y1, x2, y2 float64) (a, b, c, d float64) {
	delta := x1 - x2
	xcube := delta * delta * delta

	if xcube == 0 {
		return 0, 0, 0, y1
	}
	a = 2 * (y2 - y1) / xcube
	b = 3 * (y1 - y2) * (x1 + x2) / xcube
	c = 6 * (y2 - y1) * x1 * x2 / xcube
	d = y1 - a*x1*x1*x1 - b*x1*x1 - c*x1
	return a, b, c, d
}

// stackedSumMax returns the largest column sum across stacked layers.
func stackedSumMax(layers [][]float64) float64 {
	if len(layers) == 0 || len(layers[0]) == 0 {
		return 0
	}
	maxValue := math.Inf(-1)
	for d := range layers[0] {
		sum := 0.0
		for _, row := range layers {
			if d < len(row) {
				sum += row[d]
			}
		}
		if sum > maxValue {
			maxValue = sum
		}
	}
	if math.IsInf(maxValue, -1) {
		return 0
	}
	return maxValue
}

// nonNegativeNiceTicks produces 1-2-5 rounded ticks from 0 up to maxValue.
func nonNegativeNiceTicks(maxValue float64) []float64 {
	if maxValue <= 0 || math.IsInf(maxValue, 0) || math.IsNaN(maxValue) {
		return []float64{0}
	}
	const target = 6.0
	raw := maxValue / target
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	step := magnitude
	switch norm := raw / magnitude; {
	case norm <= 1:
		step = magnitude
	case norm <= 2:
		step = 2 * magnitude
	case norm <= 5:
		step = 5 * magnitude
	default:
		step = 10 * magnitude
	}
	ticks := make([]float64, 0, int(maxValue/step)+1)
	for v := 0.0; v <= maxValue+step*0.001; v += step {
		ticks = append(ticks, v)
	}
	return ticks
}

// ConfigureLineCountYAxis fixes both the tick locations and their complete
// decimal labels. This keeps the magnitude attached to the axis instead of
// relying on a detachable scientific-notation offset such as "1e4".
func ConfigureLineCountYAxis(axes *core.Axes, maxValue float64) {
	if axes == nil || axes.YAxis == nil {
		return
	}

	ticks, labels := lineCountYAxisTicks(maxValue)
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	axes.YAxis.Formatter = ticker.FixedFormatter{Labels: labels}
}

func lineCountYAxisTicks(maxValue float64) ([]float64, []string) {
	ticks := nonNegativeNiceTicks(maxValue)

	precision := 0
	if len(ticks) > 1 {
		for step := math.Abs(ticks[1] - ticks[0]); step > 0 && step < 1 && precision < 9; step *= 10 {
			precision++
		}
	}

	labels := make([]string, len(ticks))
	for i, tick := range ticks {
		labels[i] = strconv.FormatFloat(tick, 'f', precision, 64)
	}

	return ticks, labels
}

func pythonTransparentFigureOptions(fontSize float64) []style.Option {
	if fontSize <= 0 {
		fontSize = PythonPlotFontSize()
	}
	transparent := render.Color{R: 1, G: 1, B: 1, A: 0}
	white := render.Color{R: 1, G: 1, B: 1, A: 1}
	text := render.Color{R: 0, G: 0, B: 0, A: 1}
	// Python labours' `apply_plot_style` sets the legend frame face/edge to
	// the (opaque) background color and the text to the foreground. Mirror
	// that here so the legend doesn't render dark when the saved PNG is
	// composited against a non-white surface.
	return []style.Option{
		style.WithTheme(style.ThemeGGPlot),
		style.WithFont(PythonPlotFontFamily, fontSize),
		style.WithBackground(1, 1, 1, 0),
		style.WithAxesBackground(transparent),
		style.WithAxesEdgeColor(text),
		style.WithLegendColors(white, white, text),
	}
}

func configureTimeAreaAxes(ax *core.Axes, dates []time.Time, opts MatplotlibTimeAreaOptions) {
	ax.SetTitle(opts.Title)
	ax.SetXLabel(opts.XLabel)
	ax.SetYLabel(opts.YLabel)
	xMin := float64(dates[0].Unix())
	xMax := float64(dates[len(dates)-1].Unix())
	if xMin == xMax {
		xMin = float64(dates[0].AddDate(-2, 0, 0).Unix())
		xMax = float64(dates[0].AddDate(2, 0, 0).Unix())
	} else if opts.AutoXMargin {
		padding := (xMax - xMin) * 0.05
		xMin -= padding
		xMax += padding
	}
	ax.SetXLim(xMin, xMax)
	if opts.YMax > opts.YMin {
		ax.SetYLim(opts.YMin, opts.YMax)
	}
	// Generic time-area charts use matplotlib's AutoDateLocator behavior in
	// Python labours, which never injects the data-range endpoints. We use
	// timeAxisDateTicks (instead of burndownDateTicks) so a chart like
	// old_vs_new shows clean monthly labels without the duplicated end-of-
	// range artifact the burndown-only logic would introduce.
	ticks, labels := timeAxisDateTicks(dates, "")
	if len(ticks) > 0 {
		ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
		ax.XAxis.Formatter = ticker.FixedFormatter{Labels: labels}
		if shouldRotateDateLabels(labels) {
			ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 30, AutoAlign: true}
		}
	}
	if opts.HideY {
		ax.YAxis.ShowSpine = false
		ax.YAxis.ShowTicks = false
		ax.YAxis.ShowLabels = false
	}
	if opts.ShowGrid {
		ax.AddXGrid()
		ax.AddYGrid()
	}
}

func defaultPlotWidth(width float64) float64 {
	if width > 0 {
		return width
	}
	return PythonPlotDefaultWidthInches
}

func defaultPlotHeight(height float64) float64 {
	if height > 0 {
		return height
	}
	return PythonPlotDefaultHeightInches
}

func maxFloat64(values []float64) float64 {
	maxValue := 0.0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func maxMatrixFloat64(matrix [][]float64) float64 {
	maxValue := 0.0
	for _, row := range matrix {
		for _, value := range row {
			if value > maxValue {
				maxValue = value
			}
		}
	}
	return maxValue
}

func configureMatplotlibHeatmapTicks(ax *core.Axes, rowLabels, colLabels []string, opts MatplotlibHeatmapOptions) {
	xTicks := make([]float64, len(colLabels))
	xLabels := make([]string, len(colLabels))
	xLimit := opts.XLabelLimit
	if xLimit <= 0 {
		xLimit = 18
	}
	for i, label := range colLabels {
		xTicks[i] = float64(i)
		xLabels[i] = compactMatplotlibLabel(label, xLimit)
	}
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: xTicks}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: xLabels}
	ax.XAxis.MajorLabelStyle = core.TickLabelStyle{
		Rotation: 90,
		HAlign:   core.TextAlignRight,
		VAlign:   core.TextVAlignMiddle,
	}

	yTicks := make([]float64, len(rowLabels))
	yLabels := make([]string, len(rowLabels))
	yLimit := opts.YLabelLimit
	if yLimit <= 0 {
		yLimit = 28
	}
	for i, label := range rowLabels {
		yTicks[i] = float64(i)
		yLabels[i] = compactMatplotlibLabel(label, yLimit)
	}
	ax.YAxis.Locator = ticker.FixedLocator{TicksList: yTicks}
	ax.YAxis.Formatter = ticker.FixedFormatter{Labels: yLabels}
}

func compactMatplotlibLabel(label string, limit int) string {
	if limit <= 0 || len(label) <= limit {
		return label
	}
	if limit <= 3 {
		return label[len(label)-limit:]
	}
	return "..." + label[len(label)-(limit-3):]
}
