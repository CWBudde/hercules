package graphics

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cwbudde/matplotlib-go/backends"
	_ "github.com/cwbudde/matplotlib-go/backends/agg"
	_ "github.com/cwbudde/matplotlib-go/backends/svg"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/burndown"
)

// PlotStackedBurndownMatplotlib renders a stacked burndown chart from a raw
// per-layer matrix and date range using the matplotlib-go backend. It backs the
// legacy/fallback burndown path that operates on already-interpolated matrices
// (the header-driven path uses PlotBurndownMatplotlib).
func PlotStackedBurndownMatplotlib(matrix [][]float64, dateRange []time.Time, output string, relative bool) error {
	return PlotStackedBurndownMatplotlibWithOptions(matrix, dateRange, output, relative, DefaultOptions())
}

// PlotStackedBurndownMatplotlibWithOptions renders using an explicit visual
// configuration.
func PlotStackedBurndownMatplotlibWithOptions(
	matrix [][]float64,
	dateRange []time.Time,
	output string,
	relative bool,
	opts Options,
) error {
	if len(matrix) == 0 || len(dateRange) == 0 {
		return errors.New("empty matrix or date range")
	}

	numPoints := len(dateRange)
	if cols := len(matrix[0]); cols < numPoints {
		numPoints = cols
		dateRange = dateRange[:numPoints]
	}

	colors := GetBurndownColorsForTheme(opts.Theme, len(matrix))

	series := make([]MatplotlibTimeAreaSeries, len(matrix))
	for i, row := range matrix {
		values := make([]float64, numPoints)
		copy(values, row[:min(numPoints, len(row))])
		series[i] = MatplotlibTimeAreaSeries{
			Label:  fmt.Sprintf("Layer %d", i),
			Values: values,
			Color:  colors[i%len(colors)],
		}
	}

	yLabel := "Lines of Code"
	if relative {
		yLabel = "Relative Fraction"
	}

	title := "Burndown Chart"
	if opts.HideTitle {
		title = ""
	}

	return PlotTimeAreasMatplotlib(dateRange, series, MatplotlibTimeAreaOptions{
		Title:            title,
		XLabel:           "Time",
		YLabel:           yLabel,
		Output:           output,
		Stacked:          true,
		FullNumberYTicks: !relative,
		ShowGrid:         true,
		Legend:           true,
		FontSize:         opts.PlotFontSize(),
	})
}

// PlotBurndownMatplotlib creates a burndown plot with matplotlib-go stackplot rendering.
func PlotBurndownMatplotlib(data *burndown.ProcessedBurndown, output string, relative bool) error {
	return PlotBurndownMatplotlibWithOptions(data, output, relative, DefaultOptions())
}

// PlotBurndownMatplotlibWithOptions creates a burndown plot using opts.
func PlotBurndownMatplotlibWithOptions(
	data *burndown.ProcessedBurndown,
	output string,
	relative bool,
	opts Options,
) error {
	if data == nil || len(data.Matrix) == 0 || len(data.DateRange) == 0 {
		return errors.New("empty burndown data")
	}

	numSeries := len(data.Matrix)
	if numSeries == 0 {
		return errors.New("empty matrix")
	}

	matrix := data.Matrix
	timeValues, renderColors, labels := prepareBurndownStackData(data)
	width, height := pythonPlotPixelSize(PythonPlotDefaultWidthInches, PythonPlotDefaultHeightInches, opts.Size)
	background, foreground := LaboursPlotColors(opts.Background)
	transparentBackground := background
	transparentBackground.A = 0

	fig, ax := newPythonBurndownFigure(
		width, height, opts.PlotFontSize(), background, foreground, transparentBackground,
		matrix, relative,
	)
	if ax == nil {
		return errors.New("failed to create burndown axes")
	}

	configureBurndownStackAxes(ax, data, matrix, timeValues, renderColors, labels, relative, opts)

	return saveMatplotlibFigureWithoutTightLayout(fig, output, width, height, transparentBackground)
}

func prepareBurndownStackData(
	data *burndown.ProcessedBurndown,
) ([]float64, []render.Color, []string) {
	timeValues := unixTimeValues(data.DateRange)
	colors := PythonLaboursColorPalette(len(data.Matrix))

	renderColors := make([]render.Color, len(colors))
	for i, itemColor := range colors {
		renderColors[i] = renderColor(itemColor)
	}

	labels := make([]string, len(data.Matrix))
	for i := range labels {
		labels[i] = fmt.Sprintf("Layer %d", i)
		if i < len(data.Labels) {
			labels[i] = data.Labels[i]
		}
	}

	return timeValues, renderColors, labels
}

func newPythonBurndownFigure(
	width, height int,
	fontSize float64,
	background, foreground, transparentBackground render.Color,
	matrix [][]float64,
	relative bool,
) (*core.Figure, *core.Axes) {
	fig := core.NewFigure(
		width,
		height,
		style.WithTheme(style.ThemeGGPlot),
		style.WithFont(PythonPlotFontFamily, fontSize),
		style.WithBackground(background.R, background.G, background.B, 0),
		style.WithAxesBackground(transparentBackground),
		style.WithAxesEdgeColor(foreground),
		style.WithTextColor(foreground.R, foreground.G, foreground.B, foreground.A),
		style.WithLegendColors(background, background, foreground),
	)
	ax := fig.GridSpec(
		1, 1, core.WithGridSpecPadding(pythonBurndownAxesPadding(matrix, relative)),
	).Cell(0, 0).AddAxes()

	return fig, ax
}

func configureBurndownStackAxes(
	ax *core.Axes,
	data *burndown.ProcessedBurndown,
	matrix [][]float64,
	timeValues []float64,
	colors []render.Color,
	labels []string,
	relative bool,
	opts Options,
) {
	if !opts.HideTitle {
		ax.SetTitle(fmt.Sprintf("%s %d x %d (granularity %d, sampling %d)",
			data.Name, len(data.Matrix), len(data.DateRange), data.Granularity, data.Sampling))
	}

	ax.SetXLabel("Time")
	ax.SetYLabel("Lines of code")

	plotMatrix := matrix
	if relative {
		// Python lets matplotlib clip the unnormalized stackplot after ylim(0, 1).
		// matplotlib-go's AGG backend currently drops those out-of-range fills,
		// so pre-clip the stacked layers to the same visible result.
		plotMatrix = clippedStackMatrix(matrix, 0, 1)
	}

	ax.StackPlot(timeValues, plotMatrix, core.StackPlotOptions{
		Colors: colors,
		Labels: labels,
	})
	ax.SetXLim(timeValues[0], timeValues[len(timeValues)-1])

	if relative {
		ax.SetYLim(0, 1)
	} else {
		configureMatplotlibBurndownYAxis(ax, matrix)
	}

	configureMatplotlibBurndownTimeAxis(ax, data.DateRange, data.ResampleMode)

	legend := ax.AddLegend()

	legend.Location = core.LegendUpperLeft
	if relative {
		legend.Location = core.LegendLowerLeft
	}
}

func pythonBurndownAxesPadding(matrix [][]float64, relative bool) (left, right, bottom, top float64) {
	left = 0.036
	if relative {
		left = 0.045
	} else if maxStackY(matrix) >= 1000 {
		// Full line-count labels need more room than the old scaled labels.
		left = 0.075
	} else {
		left = 0.041
	}

	return left, 0.991, 0.049, 0.968
}

func clippedStackMatrix(matrix [][]float64, minY, maxY float64) [][]float64 {
	if len(matrix) == 0 {
		return nil
	}

	points := 0
	for _, row := range matrix {
		if points == 0 || len(row) < points {
			points = len(row)
		}
	}

	if points == 0 {
		return nil
	}

	clipped := make([][]float64, len(matrix))

	cumulative := make([]float64, points)
	for i, row := range matrix {
		clipped[i] = make([]float64, points)
		for j := range points {
			lower := cumulative[j]
			upper := lower + row[j]
			clippedLower := clampFloat(lower, minY, maxY)

			clippedUpper := clampFloat(upper, minY, maxY)
			if clippedUpper > clippedLower {
				clipped[i][j] = clippedUpper - clippedLower
			}

			cumulative[j] = upper
		}
	}

	return clipped
}

func configureMatplotlibBurndownYAxis(axes *core.Axes, matrix [][]float64) {
	maxY := maxStackY(matrix)
	if maxY <= 0 {
		axes.SetYLim(0, 1)

		return
	}

	yMax := maxY * 1.05
	axes.SetYLim(0, yMax)
	ConfigureLineCountYAxis(axes, yMax)
}

func maxStackY(matrix [][]float64) float64 {
	points := 0
	for _, row := range matrix {
		if points == 0 || len(row) < points {
			points = len(row)
		}
	}

	maxY := 0.0

	for j := range points {
		total := 0.0
		for i := range matrix {
			total += matrix[i][j]
		}

		if total > maxY {
			maxY = total
		}
	}

	return maxY
}

// LaboursPlotColors returns the (background, foreground) figure colors for the
// given --background flag value, matching Python labours' black-on-white default
// and white-on-black "black" theme.
func LaboursPlotColors(backgroundName string) (background, foreground render.Color) {
	if strings.EqualFold(backgroundName, "black") {
		return render.Color{R: 0, G: 0, B: 0, A: 1}, render.Color{R: 1, G: 1, B: 1, A: 1}
	}

	return render.Color{R: 1, G: 1, B: 1, A: 1}, render.Color{R: 0, G: 0, B: 0, A: 1}
}

func configureMatplotlibBurndownTimeAxis(ax *core.Axes, dates []time.Time, resampleMode string) {
	if ax == nil || len(dates) == 0 {
		return
	}

	ticks, labels := burndownDateTicks(dates, resampleMode)
	if len(ticks) == 0 {
		return
	}

	ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: labels}

	if shouldRotateDateLabels(labels) {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 30, AutoAlign: true}
	}
}

// shouldRotateDateLabels mirrors matplotlib's pragmatic rule: rotate only when
// the labels would actually collide. We approximate label width as 7 px per
// character at 12 pt and assume a default 16-inch (1600 px) plot. Long-format
// labels (e.g. "2017-01-01") are wide enough that even a handful overlap, so
// we still rotate them, but compact "YYYY-MM" labels fit horizontally up to
// ~12 ticks the way Python's labours-equivalent old-vs-new chart renders.
func shouldRotateDateLabels(labels []string) bool {
	if len(labels) <= 1 {
		return false
	}

	maxLen := 0
	for _, label := range labels {
		if len(label) > maxLen {
			maxLen = len(label)
		}
	}

	approxAxisWidth := 1500.0
	approxLabelWidth := float64(maxLen) * 8.0 // px per character at default fontsize
	totalLabelWidth := approxLabelWidth * float64(len(labels))

	return totalLabelWidth > approxAxisWidth*0.9
}

// burndownDateTicks chooses tick locations for the burndown family of charts.
// Burndown.py in Python labours conditionally appends the data range
// endpoints when the natural locator leaves a wide gap on either side
// (`if locs[0] - xlim()[0] > (locs[1] - locs[0]) / 2` etc.), so we mirror that
// here via includeEndpoints=true.
func burndownDateTicks(dates []time.Time, resampleMode string) ([]float64, []string) {
	return chooseDateTicks(dates, resampleMode, true)
}

// timeAxisDateTicks chooses tick locations for general time-axis charts that
// rely on matplotlib's `AutoDateLocator` rather than burndown.py's hand-rolled
// endpoint extension. We therefore never inject the data-range endpoints.
func timeAxisDateTicks(dates []time.Time, resampleMode string) ([]float64, []string) {
	return chooseDateTicks(dates, resampleMode, false)
}

func chooseDateTicks(dates []time.Time, resampleMode string, includeEndpoints bool) ([]float64, []string) {
	if len(dates) == 0 {
		return nil, nil
	}

	start := dates[0]

	end := dates[len(dates)-1]
	if end.Before(start) {
		start, end = end, start
	}

	mode := strings.ToLower(resampleMode)
	switch {
	case strings.Contains(mode, "m"):
		return buildMonthlyTicks(start, end, includeEndpoints)
	case strings.Contains(mode, "a"), strings.Contains(mode, "y"), strings.Contains(mode, "year"):
		return buildYearlyTicks(start, end, includeEndpoints)
	}

	days := end.Sub(start).Hours() / 24
	switch {
	case days <= 45:
		return buildDailyTicks(start, end, includeEndpoints)
	case days <= 730:
		return buildMonthlyTicks(start, end, includeEndpoints)
	default:
		return buildYearlyTicks(start, end, includeEndpoints)
	}
}

// buildDailyTicks emits day-aligned ticks across [start, end]. Python's
// matplotlib AutoDateLocator picks "nice" day boundaries; we step to keep at
// most ~10 ticks. Endpoints are added only when includeEndpoints is true and
// the natural ticks leave a meaningful gap, mirroring Python burndown.py.
func buildDailyTicks(start, end time.Time, includeEndpoints bool) ([]float64, []string) {
	days := int(math.Ceil(end.Sub(start).Hours() / 24))
	step := atLeastOne(int(math.Ceil(float64(atLeastOne(days)) / 10)))

	first := start.Truncate(24 * time.Hour)
	if first.Before(start) {
		first = first.AddDate(0, 0, 1)
	}

	natural := make([]time.Time, 0, days/step+2)
	for t := first; !t.After(end); t = t.AddDate(0, 0, step) {
		natural = append(natural, t)
	}

	stepDuration := time.Duration(step) * 24 * time.Hour

	return formatDateTicks(maybeAddEndpoints(natural, start, end, stepDuration, includeEndpoints), "2006-01-02")
}

// buildMonthlyTicks emits month-aligned ticks across [start, end]. Endpoints
// are added only when no natural tick formats to the same "YYYY-MM" string,
// avoiding the overlapping-label artifact Python's locator does not produce.
func buildMonthlyTicks(start, end time.Time, includeEndpoints bool) ([]float64, []string) {
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	step := atLeastOne(int(math.Ceil(float64(atLeastOne(months)) / 10)))

	first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	if first.Before(start) {
		first = first.AddDate(0, step, 0)
	}

	natural := make([]time.Time, 0, months/step+2)
	for t := first; !t.After(end); t = t.AddDate(0, step, 0) {
		natural = append(natural, t)
	}

	stepDuration := time.Duration(step*30) * 24 * time.Hour

	return formatDateTicks(maybeAddEndpoints(natural, start, end, stepDuration, includeEndpoints), "2006-01")
}

// buildYearlyTicks emits year-aligned ticks across [start, end]. Mirrors
// matplotlib's YearLocator selection used by Python labours when sampling is
// yearly.
func buildYearlyTicks(start, end time.Time, includeEndpoints bool) ([]float64, []string) {
	years := atLeastOne(end.Year() - start.Year() + 1)
	step := atLeastOne(int(math.Ceil(float64(years) / 10)))
	natural := make([]time.Time, 0, years)

	for year := start.Year(); year <= end.Year(); year += step {
		t := time.Date(year, 1, 1, 0, 0, 0, 0, start.Location())
		if !t.Before(start) && !t.After(end) {
			natural = append(natural, t)
		}
	}

	stepDuration := time.Duration(step) * 365 * 24 * time.Hour

	out := maybeAddEndpoints(natural, start, end, stepDuration, includeEndpoints)
	if len(out) <= 2 {
		// Short spans look better as dated endpoints (e.g. "2017-01-01") so
		// the reader can tell what range is shown without a year-only label.
		return formatDateTicks(out, "2006-01-02")
	}

	return formatDateTicks(out, "2006")
}

// maybeAddEndpoints implements two policies for the data-range endpoints.
//
// When includeEndpoints is true (burndown family), this matches Python
// `burndown.py`'s `if locs[0] - xlim()[0] > (locs[1] - locs[0]) / 2`
// rule: prepend `start` when the gap to the first natural tick exceeds half
// the inter-tick distance, and likewise for `end`. When natural ticks are
// empty, both endpoints are added so the chart is not unlabeled.
//
// When includeEndpoints is false, the natural ticks alone are returned —
// matching matplotlib's default `AutoDateLocator` behavior used by every
// non-burndown date chart in Python labours (e.g. `old_vs_new.py`).
func maybeAddEndpoints(natural []time.Time, start, end time.Time, stepDuration time.Duration, includeEndpoints bool) []time.Time {
	if !includeEndpoints {
		if len(natural) == 0 {
			// Without natural ticks the axis would be entirely unlabeled,
			// which is worse than diverging from Python's auto-locator.
			return []time.Time{start, end}
		}

		return append([]time.Time(nil), natural...)
	}

	out := make([]time.Time, 0, len(natural)+2)

	tolerance := stepDuration / 2
	if tolerance <= 0 {
		tolerance = 24 * time.Hour
	}

	if len(natural) == 0 || natural[0].Sub(start) > tolerance {
		out = append(out, start)
	}

	out = append(out, natural...)

	if len(natural) == 0 || end.Sub(natural[len(natural)-1]) > tolerance {
		out = append(out, end)
	}

	return out
}

// formatDateTicks deduplicates by formatted label so two dates that render to
// the same string (e.g. two days within the same month for a "2006-01"
// formatter) do not both appear on the axis.
func formatDateTicks(dates []time.Time, layout string) ([]float64, []string) {
	seenLabel := map[string]bool{}
	ticks := make([]float64, 0, len(dates))

	labels := make([]string, 0, len(dates))
	for _, date := range dates {
		label := date.Format(layout)
		if seenLabel[label] {
			continue
		}

		seenLabel[label] = true

		ticks = append(ticks, float64(date.Unix()))
		labels = append(labels, label)
	}

	return ticks, labels
}

func saveMatplotlibFigure(fig *core.Figure, output string, width, height int, backgrounds ...render.Color) error {
	return saveMatplotlibFigureWithLayout(fig, output, width, height, true, backgrounds...)
}

func saveMatplotlibFigureWithoutTightLayout(fig *core.Figure, output string, width, height int, backgrounds ...render.Color) error {
	return saveMatplotlibFigureWithLayout(fig, output, width, height, false, backgrounds...)
}

func saveMatplotlibFigureWithLayout(fig *core.Figure, output string, width, height int, tight bool, backgrounds ...render.Color) error {
	if output == "" {
		output = "burndown_project_python.png"
	}

	err := os.MkdirAll(filepath.Dir(output), 0o750)
	if err != nil {
		return fmt.Errorf("failed to create output directory for %s: %w", output, err)
	}

	if tight {
		fig.TightLayout()
	}

	background := matplotlibBackground(backgrounds)
	config := backends.Config{
		Width:       width,
		Height:      height,
		Background:  background,
		DPI:         100,
		Transparent: background.A == 0,
	}

	return saveMatplotlibFigureWithConfig(fig, output, config)
}

func matplotlibBackground(backgrounds []render.Color) render.Color {
	if len(backgrounds) > 0 {
		return backgrounds[0]
	}

	return render.Color{R: 1, G: 1, B: 1, A: 0}
}

func saveMatplotlibFigureWithConfig(fig *core.Figure, output string, config backends.Config) error {
	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %w", err)
		}

		return core.SaveSVG(fig, renderer, output)
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %w", err)
		}

		if err := core.SavePNG(fig, renderer, output); err != nil {
			return err
		}

		if config.Background.A == 0 {
			return setTransparentPNGRGB(output, config.Background)
		}

		return nil
	}
}

func renderColor(c color.Color) render.Color {
	r, g, b, a := c.RGBA()

	return render.Color{
		R: float64(r) / 0xffff,
		G: float64(g) / 0xffff,
		B: float64(b) / 0xffff,
		A: float64(a) / 0xffff,
	}
}

func pythonPlotPixelSize(defaultWidth, defaultHeight float64, configuredSize ...string) (int, int) {
	width := defaultWidth
	height := defaultHeight

	sizeStr := ""
	if len(configuredSize) > 0 {
		sizeStr = configuredSize[0]
	}

	if sizeStr != "" {
		parsedWidth, parsedHeight, err := parsePlotSizeFloats(sizeStr)
		if err == nil {
			width, height = parsedWidth, parsedHeight
		} else {
			fmt.Fprintf(os.Stderr, "Warning: %v, using default size\n", err)
		}
	}

	return InchesToPixels(width), InchesToPixels(height)
}

func atLeastOne(value int) int {
	if value < 1 {
		return 1
	}

	return value
}

func clampFloat(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}

	if v > maxVal {
		return maxVal
	}

	return v
}

func setTransparentPNGRGB(path string, background render.Color) error {
	file, err := os.Open(path) // #nosec G304 - image path is generated by this plotting code.
	if err != nil {
		return err
	}

	img, err := png.Decode(file)
	closeErr := file.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	bounds := img.Bounds()
	rgba := imageToNRGBA(img)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			offset := rgba.PixOffset(x, y)
			if rgba.Pix[offset+3] == 0 {
				rgba.Pix[offset+0] = uint8(math.Round(background.R * 255))
				rgba.Pix[offset+1] = uint8(math.Round(background.G * 255))
				rgba.Pix[offset+2] = uint8(math.Round(background.B * 255))
			}
		}
	}

	file, err = os.Create(path) // #nosec G304 - image path is generated by this plotting code.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	return png.Encode(file, rgba)
}

// SetTransparentPNGRGB rewrites fully transparent PNG pixels so their hidden
// RGB channels match the intended matte/background color.
func SetTransparentPNGRGB(path string, background render.Color) error {
	return setTransparentPNGRGB(path, background)
}

func imageToNRGBA(img image.Image) *image.NRGBA {
	if rgba, ok := img.(*image.NRGBA); ok {
		return rgba
	}

	bounds := img.Bounds()
	rgba := image.NewNRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	return rgba
}

// PythonLaboursColorPalette returns the color cycle Python labours actually
// produces. Python labours applies the requested matplotlib style and then
// overrides axes.prop_cycle with pyplot.cm.tab20.colors in plotting.import_pyplot.
// Matching this palette is what makes burndown layers, devs spikes and
// old-vs-new fills look right relative to the Python baseline.
//
// When more series are needed than palette entries, callers cycle modulo
// length, which mirrors matplotlib's `axes.prop_cycle` wrap-around.
func PythonLaboursColorPalette(n int) []color.Color {
	palette := tab20Palette()

	colors := make([]color.Color, n)
	for i := range n {
		colors[i] = palette[i%len(palette)]
	}

	return colors
}

func tab20Palette() []color.Color {
	return []color.Color{
		color.RGBA{R: 0x1F, G: 0x77, B: 0xB4, A: 255},
		color.RGBA{R: 0xAE, G: 0xC7, B: 0xE8, A: 255},
		color.RGBA{R: 0xFF, G: 0x7F, B: 0x0E, A: 255},
		color.RGBA{R: 0xFF, G: 0xBB, B: 0x78, A: 255},
		color.RGBA{R: 0x2C, G: 0xA0, B: 0x2C, A: 255},
		color.RGBA{R: 0x98, G: 0xDF, B: 0x8A, A: 255},
		color.RGBA{R: 0xD6, G: 0x27, B: 0x28, A: 255},
		color.RGBA{R: 0xFF, G: 0x98, B: 0x96, A: 255},
		color.RGBA{R: 0x94, G: 0x67, B: 0xBD, A: 255},
		color.RGBA{R: 0xC5, G: 0xB0, B: 0xD5, A: 255},
		color.RGBA{R: 0x8C, G: 0x56, B: 0x4B, A: 255},
		color.RGBA{R: 0xC4, G: 0x9C, B: 0x94, A: 255},
		color.RGBA{R: 0xE3, G: 0x77, B: 0xC2, A: 255},
		color.RGBA{R: 0xF7, G: 0xB6, B: 0xD2, A: 255},
		color.RGBA{R: 0x7F, G: 0x7F, B: 0x7F, A: 255},
		color.RGBA{R: 0xC7, G: 0xC7, B: 0xC7, A: 255},
		color.RGBA{R: 0xBC, G: 0xBD, B: 0x22, A: 255},
		color.RGBA{R: 0xDB, G: 0xDB, B: 0x8D, A: 255},
		color.RGBA{R: 0x17, G: 0xBE, B: 0xCF, A: 255},
		color.RGBA{R: 0x9E, G: 0xDA, B: 0xE5, A: 255},
	}
}
