package modes

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/matplotlib-go/backends"
	_ "github.com/cwbudde/matplotlib-go/backends/agg"
	_ "github.com/cwbudde/matplotlib-go/backends/svg"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

var (
	errNoOwnershipData = errors.New("no ownership burndown data found")
	errNoOwnershipPlot = errors.New("no ownership burndown data to plot")
	errOwnershipAxes   = errors.New("failed to create ownership axes")
)

func OwnershipBurndown(reader readers.Reader, output string) error {
	return OwnershipBurndownWithOptions(reader, output, defaultOptions())
}

func OwnershipBurndownWithOptions(reader readers.Reader, output string, opts Options) error {
	progEstimator := progress.NewProgressEstimator(!opts.Quiet)
	progEstimator.StartMultiOperation(4, "Ownership Burndown Analysis")

	defer progEstimator.FinishMultiOperation()

	progEstimator.NextOperation("Validating output path")

	output = ownershipOutputPath(output, opts.Quiet)

	err := createOwnershipOutputDirectory(output)
	if err != nil {
		return err
	}

	progEstimator.NextOperation("Extracting ownership data")

	input, err := loadOwnershipBurndown(reader)
	if err != nil {
		return err
	}

	progEstimator.NextOperation("Processing ownership data")
	names, peopleMatrix, dateRange := processOwnershipBurndownWithProgress(
		input.startTime,
		input.lastTime,
		input.sampling,
		input.tickSize,
		input.people,
		input.ownership,
		ownershipMaxPeople(opts.MaxPeople),
		opts.OrderOwnershipByTime,
		progEstimator,
	)

	progEstimator.NextOperation("Generating visualization")

	if filepath.Ext(output) == ".json" {
		return saveOwnershipBurndownAsJSON(output, names, peopleMatrix, dateRange, input.lastTime)
	}

	err = plotOwnershipBurndown(
		reader.GetName(), names, peopleMatrix, dateRange, input.lastTime, output, opts,
	)
	if err != nil {
		return fmt.Errorf("failed to plot ownership burndown: %w", err)
	}

	err = reportOwnershipSuccess(opts.Quiet)
	if err != nil {
		return err
	}

	return nil
}

type ownershipBurndownInput struct {
	people    []string
	ownership map[string][][]int
	startTime time.Time
	lastTime  time.Time
	sampling  int
	tickSize  float64
}

func ownershipOutputPath(output string, quiet bool) string {
	if output != "" {
		return output
	}

	output = "ownership.png"
	if !quiet {
		_, _ = fmt.Fprintf(os.Stdout, "Output not provided, using default: %s\n", output)
	}

	return output
}

func createOwnershipOutputDirectory(output string) error {
	outputDir := filepath.Dir(output)

	err := os.MkdirAll(outputDir, 0o750)
	if err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return nil
}

func reportOwnershipSuccess(quiet bool) error {
	if quiet {
		return nil
	}

	_, err := fmt.Fprintln(os.Stdout, "Ownership burndown chart generated successfully.")
	if err != nil {
		return fmt.Errorf("failed to report ownership output: %w", err)
	}

	return nil
}

func loadOwnershipBurndown(reader readers.Reader) (ownershipBurndownInput, error) {
	people, ownership, err := reader.GetOwnershipBurndown()
	if err != nil {
		return ownershipBurndownInput{}, fmt.Errorf("failed to get ownership burndown data: %w", err)
	}

	if len(people) == 0 {
		return ownershipBurndownInput{}, errNoOwnershipData
	}

	params, err := reader.GetBurndownParameters()
	if err != nil {
		return ownershipBurndownInput{}, fmt.Errorf("failed to get burndown parameters: %w", err)
	}

	startUnix, lastUnix := reader.GetHeader()
	tickSize := ownershipTickSize(params.TickSize)
	sampling := ownershipSampling(params.Sampling)
	startTime := floorTimeBySeconds(time.Unix(startUnix, 0), tickSize).
		Add(secondsDuration(float64(sampling) * tickSize))

	return ownershipBurndownInput{
		people:    people,
		ownership: ownership,
		startTime: startTime,
		lastTime:  ownershipLastTime(startTime, lastUnix),
		sampling:  sampling,
		tickSize:  tickSize,
	}, nil
}

func ownershipTickSize(tickSize float64) float64 {
	if tickSize <= 0 {
		return 24 * 60 * 60
	}

	return tickSize
}

func ownershipSampling(sampling int) int {
	if sampling <= 0 {
		return 1
	}

	return sampling
}

func ownershipLastTime(startTime time.Time, lastUnix int64) time.Time {
	if lastUnix == 0 {
		return startTime
	}

	return time.Unix(lastUnix, 0)
}

func ownershipMaxPeople(maxPeople int) int {
	if maxPeople <= 0 {
		return 20
	}

	return maxPeople
}

func processOwnershipBurndown(
	start, _ time.Time, sampling int, tickSize float64,
	sequence []string, data map[string][][]int,
	maxPeople int, orderByTime bool,
) ([]string, [][]float64, []time.Time) {
	return processOwnershipData(start, sampling, tickSize, sequence, data, maxPeople, orderByTime, nil)
}

// processOwnershipBurndownWithProgress processes ownership data with progress tracking.
func processOwnershipBurndownWithProgress(
	start, _ time.Time, sampling int, tickSize float64,
	sequence []string, data map[string][][]int,
	maxPeople int, orderByTime bool,
	progEstimator *progress.ProgressEstimator,
) ([]string, [][]float64, []time.Time) {
	progEstimator.StartOperation("Aggregating ownership data", len(sequence)+2)
	defer progEstimator.FinishOperation()

	updateProgress := func() {
		progEstimator.UpdateProgress(1)
	}

	return processOwnershipData(
		start, sampling, tickSize, sequence, data, maxPeople, orderByTime, updateProgress,
	)
}

func processOwnershipData(
	start time.Time,
	sampling int,
	tickSize float64,
	sequence []string,
	data map[string][][]int,
	maxPeople int,
	orderByTime bool,
	updateProgress func(),
) ([]string, [][]float64, []time.Time) {
	people := aggregateOwnership(sequence, data, updateProgress)

	pointCount := ownershipPointCount(people)
	if pointCount == 0 {
		return sequence, people, nil
	}

	reportOwnershipProgress(updateProgress)

	dateRange := ownershipDateRange(start, pointCount, sampling, tickSize)
	sequence, people = truncateOwnershipPeople(sequence, people, maxPeople)

	reportOwnershipProgress(updateProgress)

	sequence, people = sortOwnershipPeople(sequence, people, orderByTime)

	return sequence, people, dateRange
}

func aggregateOwnership(
	sequence []string,
	data map[string][][]int,
	updateProgress func(),
) [][]float64 {
	people := make([][]float64, len(sequence))
	for index, name := range sequence {
		reportOwnershipProgress(updateProgress)

		people[index] = sumOwnershipRows(data[name])
	}

	return people
}

func reportOwnershipProgress(updateProgress func()) {
	if updateProgress != nil {
		updateProgress()
	}
}

func sumOwnershipRows(rows [][]int) []float64 {
	if len(rows) == 0 {
		return nil
	}

	total := make([]float64, len(rows))
	for rowIndex, row := range rows {
		for _, value := range row {
			total[rowIndex] += float64(value)
		}
	}

	return total
}

func ownershipDateRange(start time.Time, pointCount, sampling int, tickSize float64) []time.Time {
	dateRange := make([]time.Time, pointCount)
	step := ownershipSamplingDuration(sampling, tickSize)

	for index := range dateRange {
		dateRange[index] = start.Add(time.Duration(index) * step)
	}

	return dateRange
}

func truncateOwnershipPeople(
	sequence []string,
	people [][]float64,
	maxPeople int,
) ([]string, [][]float64) {
	if maxPeople < 0 || len(people) <= maxPeople {
		return sequence, people
	}

	indices := argsortDescending(ownershipTotals(people))
	chosen := indices[:maxPeople]
	truncatedPeople := reorder(people, chosen)
	truncatedNames := reorderStrings(sequence, chosen)

	truncatedPeople = append(truncatedPeople, aggregateOwnershipRows(people, indices[maxPeople:]))
	truncatedNames = append(truncatedNames, "others")

	return truncatedNames, truncatedPeople
}

func ownershipTotals(people [][]float64) []float64 {
	totals := make([]float64, len(people))
	for index, row := range people {
		for _, value := range row {
			totals[index] += value
		}
	}

	return totals
}

func aggregateOwnershipRows(people [][]float64, indices []int) []float64 {
	total := make([]float64, ownershipPointCount(people))
	for _, index := range indices {
		for point, value := range people[index] {
			total[point] += value
		}
	}

	return total
}

func sortOwnershipPeople(
	sequence []string,
	people [][]float64,
	orderByTime bool,
) ([]string, [][]float64) {
	var indices []int
	if orderByTime {
		indices = argsortAscending(ownershipFirstAppearances(people))
	} else {
		indices = argsortDescending(ownershipTotals(people))
	}

	return reorderStrings(sequence, indices), reorder(people, indices)
}

func ownershipFirstAppearances(people [][]float64) []int {
	appearances := make([]int, len(people))
	for index, row := range people {
		appearances[index] = findFirstNonZero(row)
	}

	return appearances
}

func plotOwnershipBurndown(
	repoName string,
	names []string,
	people [][]float64,
	dateRange []time.Time,
	lastTime time.Time,
	output string,
	optionValues ...Options,
) error {
	opts := ownershipOptions(optionValues)

	series, err := prepareOwnershipSeries(names, people, dateRange, lastTime)
	if err != nil {
		return err
	}

	if opts.Relative {
		normalizeOwnershipColumns(series.matrix)
	}

	plot, err := newOwnershipPlot(opts)
	if err != nil {
		return err
	}

	configureOwnershipPlot(plot, repoName, series, opts)

	return saveOwnershipMatplotlibFigure(
		plot.figure, output, plot.width, plot.height, plot.transparentBackground,
	)
}

type ownershipSeries struct {
	x         []float64
	matrix    [][]float64
	labels    []string
	dateRange []time.Time
	lastTime  time.Time
}

type ownershipPlot struct {
	figure                *core.Figure
	axes                  *core.Axes
	width                 int
	height                int
	foreground            render.Color
	legendBackground      render.Color
	transparentBackground render.Color
}

func ownershipOptions(optionValues []Options) Options {
	if len(optionValues) > 0 {
		return optionValues[0]
	}

	return defaultOptions()
}

func prepareOwnershipSeries(
	names []string,
	people [][]float64,
	dateRange []time.Time,
	lastTime time.Time,
) (ownershipSeries, error) {
	if len(people) == 0 || len(dateRange) == 0 {
		return ownershipSeries{}, errNoOwnershipPlot
	}

	if lastTime.Before(dateRange[len(dateRange)-1]) {
		lastTime = dateRange[len(dateRange)-1]
	}

	series := ownershipSeries{
		x:         ownershipTimestamps(dateRange),
		matrix:    make([][]float64, 0, len(people)),
		labels:    make([]string, 0, len(people)),
		dateRange: dateRange,
		lastTime:  lastTime,
	}
	for index, row := range people {
		if len(row) == 0 {
			continue
		}

		series.matrix = append(series.matrix, ownershipValues(row, len(dateRange)))
		series.labels = append(series.labels, ownershipLabel(names, index))
	}

	if len(series.matrix) == 0 {
		return ownershipSeries{}, errNoOwnershipPlot
	}

	return series, nil
}

func ownershipTimestamps(dateRange []time.Time) []float64 {
	timestamps := make([]float64, len(dateRange))
	for index, date := range dateRange {
		timestamps[index] = float64(date.Unix())
	}

	return timestamps
}

func ownershipValues(row []float64, pointCount int) []float64 {
	values := make([]float64, pointCount)
	copy(values, row)

	return values
}

func ownershipLabel(names []string, index int) string {
	label := fmt.Sprintf("Developer %d", index+1)
	if index < len(names) {
		label = peopleChartLabel(names[index])
	}

	return truncateOwnershipLabel(label)
}

func newOwnershipPlot(opts Options) (ownershipPlot, error) {
	width, height := ownershipPlotPixelSize(
		graphics.PythonPlotDefaultWidthInches,
		graphics.PythonPlotDefaultHeightInches,
		opts.Graphics.Size,
	)
	background, foreground := graphics.LaboursPlotColors(opts.Graphics.Background)
	transparentBackground := background
	transparentBackground.A = 0
	legendBackground := background
	legendBackground.A = 0.8
	fig := core.NewFigure(
		width,
		height,
		style.WithTheme(style.ThemeGGPlot),
		style.WithFont(graphics.PythonPlotFontFamily, opts.Graphics.PlotFontSize()),
		style.WithBackground(background.R, background.G, background.B, 0),
		style.WithAxesBackground(transparentBackground),
		style.WithAxesEdgeColor(foreground),
		style.WithTextColor(foreground.R, foreground.G, foreground.B, foreground.A),
		style.WithLegendColors(
			legendBackground,
			legendBackground,
			foreground,
		),
	)

	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.075, 0.965, 0.11, 0.968))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return ownershipPlot{}, errOwnershipAxes
	}

	return ownershipPlot{
		figure:                fig,
		axes:                  grid[0][0],
		width:                 width,
		height:                height,
		foreground:            foreground,
		legendBackground:      legendBackground,
		transparentBackground: transparentBackground,
	}, nil
}

func configureOwnershipPlot(plot ownershipPlot, repoName string, series ownershipSeries, opts Options) {
	if !opts.Graphics.HideTitle {
		plot.axes.SetTitle(ownershipChartTitle(repoName))
	}

	plot.axes.StackPlot(series.x, series.matrix, core.StackPlotOptions{
		Colors: ownershipColors(len(series.matrix)),
		Labels: series.labels,
	})
	configureOwnershipXRange(plot.axes, series.dateRange, series.lastTime)
	configureOwnershipYRange(plot.axes, series.matrix, opts.Relative)
	configureOwnershipTimeAxis(plot.axes, series.dateRange)
	configureOwnershipLegend(plot, opts.Relative)
}

func ownershipColors(count int) []render.Color {
	colors := graphics.PythonLaboursColorPalette(count)

	renderColors := make([]render.Color, len(colors))
	for index, paletteColor := range colors {
		renderColors[index] = ownershipRenderColor(paletteColor)
	}

	return renderColors
}

func configureOwnershipXRange(axes *core.Axes, dateRange []time.Time, lastTime time.Time) {
	xMin := float64(dateRange[0].Unix())

	xMax := float64(lastTime.Unix())
	if xMin == xMax {
		xMin = float64(dateRange[0].AddDate(-2, 0, 0).Unix())
		xMax = float64(dateRange[0].AddDate(2, 0, 0).Unix())
	}

	axes.SetXLim(xMin, xMax)
}

func configureOwnershipYRange(axes *core.Axes, matrix [][]float64, relative bool) {
	if relative {
		axes.SetYLim(0, 1)
		return
	}

	yMax := math.Max(maxOwnershipStackY(matrix)*1.05, 1)
	axes.SetYLim(0, yMax)
	graphics.ConfigureLineCountYAxis(axes, yMax)
}

func configureOwnershipLegend(plot ownershipPlot, relative bool) {
	legend := plot.axes.AddLegend()
	legend.Location = core.LegendUpperLeft
	legend.BackgroundColor = plot.legendBackground
	legend.BorderColor = plot.legendBackground
	legend.TextColor = plot.foreground

	if relative {
		legend.Location = core.LegendLowerLeft
	}
}

// ownershipChartTitle builds the chart title, tolerating the repository names
// that reach it in practice. Analysing the current directory gives a name of "."
// (and an unnamed input gives ""), which used to render as ". code ownership
// through time".
func ownershipChartTitle(repoName string) string {
	name := strings.TrimSpace(repoName)
	if name == "" || name == "." {
		return "Code ownership through time"
	}

	return name + " code ownership through time"
}

func ownershipSamplingDuration(sampling int, tickSize float64) time.Duration {
	if sampling <= 0 {
		sampling = 1
	}

	if tickSize <= 0 {
		tickSize = 86400
	}

	return secondsDuration(float64(sampling) * tickSize)
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func ownershipPointCount(people [][]float64) int {
	for _, row := range people {
		if len(row) > 0 {
			return len(row)
		}
	}

	return 0
}

func truncateOwnershipLabel(label string) string {
	const maxLabelLength = 40
	if len(label) <= maxLabelLength {
		return label
	}

	return label[:maxLabelLength-3] + "..."
}

func normalizeOwnershipColumns(matrix [][]float64) {
	if len(matrix) == 0 {
		return
	}

	points := len(matrix[0])
	for col := range points {
		total := 0.0

		for _, row := range matrix {
			if col < len(row) {
				total += row[col]
			}
		}

		if total == 0 {
			continue
		}

		for _, row := range matrix {
			if col < len(row) {
				row[col] /= total
			}
		}
	}
}

func floorTimeBySeconds(t time.Time, seconds float64) time.Time {
	if seconds <= 0 {
		return t
	}

	step := int64(seconds)
	if step <= 0 {
		return t
	}

	return time.Unix((t.Unix()/step)*step, 0)
}

func configureOwnershipTimeAxis(ax *core.Axes, dates []time.Time) {
	if len(dates) == 0 {
		return
	}

	limit := 8

	step := max(int(math.Ceil(float64(len(dates))/float64(limit))), 1)

	ticks := make([]float64, 0, limit+1)
	labels := make([]string, 0, limit+1)

	for i := 0; i < len(dates); i += step {
		ticks = append(ticks, float64(dates[i].Unix()))
		labels = append(labels, dates[i].Format("2006-01-02"))
	}

	lastTick := float64(dates[len(dates)-1].Unix())
	if len(ticks) == 0 || ticks[len(ticks)-1] != lastTick {
		ticks = append(ticks, lastTick)
		labels = append(labels, dates[len(dates)-1].Format("2006-01-02"))
	}

	ax.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}

	ax.XAxis.Formatter = ticker.FixedFormatter{Labels: labels}
	if len(labels) > 6 {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{Rotation: 30, AutoAlign: true}
	}
}

func maxOwnershipStackY(matrix [][]float64) float64 {
	if len(matrix) == 0 {
		return 0
	}

	points := len(matrix[0])
	maxY := 0.0

	for i := range points {
		total := 0.0

		for _, row := range matrix {
			if i < len(row) {
				total += row[i]
			}
		}

		if total > maxY {
			maxY = total
		}
	}

	return maxY
}

func ownershipRenderColor(c color.Color) render.Color {
	r, g, b, a := c.RGBA()

	return render.Color{
		R: float64(r) / 0xffff,
		G: float64(g) / 0xffff,
		B: float64(b) / 0xffff,
		A: float64(a) / 0xffff,
	}
}

func ownershipPlotPixelSize(defaultWidth, defaultHeight float64, configuredSize ...string) (int, int) {
	width := defaultWidth
	height := defaultHeight

	sizeStr := ""
	if len(configuredSize) > 0 {
		sizeStr = configuredSize[0]
	}

	if sizeStr != "" {
		parsedWidth, parsedHeight, err := parseOwnershipPlotSize(sizeStr)
		if err == nil {
			width, height = parsedWidth, parsedHeight
		} else {
			fmt.Fprintf(os.Stderr, "Warning: %v, using default size\n", err)
		}
	}

	return graphics.InchesToPixels(width), graphics.InchesToPixels(height)
}

func parseOwnershipPlotSize(sizeStr string) (float64, float64, error) {
	parts := strings.Split(sizeStr, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid size %q: expected width,height", sizeStr)
	}

	width, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || width <= 0 {
		return 0, 0, fmt.Errorf("invalid plot width %q", parts[0])
	}

	height, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || height <= 0 {
		return 0, 0, fmt.Errorf("invalid plot height %q", parts[1])
	}

	return width, height, nil
}

func saveOwnershipMatplotlibFigure(fig *core.Figure, output string, width, height int, background render.Color) error {
	if output == "" {
		output = "ownership.png"
	}

	err := os.MkdirAll(filepath.Dir(output), 0o750)
	if err != nil {
		return fmt.Errorf("failed to create output directory for %s: %w", output, err)
	}

	config := backends.Config{
		Width:       width,
		Height:      height,
		Background:  background,
		DPI:         100,
		Transparent: background.A == 0,
	}

	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		return saveOwnershipSVG(fig, output, config)
	default:
		return saveOwnershipPNG(fig, output, config, background)
	}
}

func saveOwnershipSVG(fig *core.Figure, output string, config backends.Config) error {
	renderer, _, err := backends.NewRenderer("svg", config, nil)
	if err != nil {
		return fmt.Errorf("failed to create SVG renderer: %w", err)
	}

	err = core.SaveSVG(fig, renderer, output)
	if err != nil {
		return fmt.Errorf("failed to save ownership SVG: %w", err)
	}

	return nil
}

func saveOwnershipPNG(
	fig *core.Figure,
	output string,
	config backends.Config,
	background render.Color,
) error {
	renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
	if err != nil {
		return fmt.Errorf("failed to create AGG renderer: %w", err)
	}

	err = core.SavePNG(fig, renderer, output)
	if err != nil {
		return fmt.Errorf("failed to save ownership PNG: %w", err)
	}

	if ownershipNeedsWhiteMatte(background) {
		return normalizeOwnershipPNGMatte(output)
	}

	return nil
}

func ownershipNeedsWhiteMatte(background render.Color) bool {
	return background.A == 0 && background.R == 1 && background.G == 1 && background.B == 1
}

func normalizeOwnershipPNGMatte(path string) error {
	img, err := readOwnershipPNG(path)
	if err != nil {
		return err
	}

	out, changed := normalizedOwnershipImage(img)
	if !changed {
		return nil
	}

	return writeOwnershipPNG(path, out)
}

func readOwnershipPNG(path string) (image.Image, error) {
	file, err := os.Open(path) // #nosec G304 - path is generated by ownership plotting.
	if err != nil {
		return nil, fmt.Errorf("failed to open ownership PNG for matte normalization: %w", err)
	}

	img, _, decodeErr := image.Decode(file)
	closeErr := file.Close()

	if decodeErr != nil {
		return nil, fmt.Errorf("failed to decode ownership PNG for matte normalization: %w", decodeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("failed to close ownership PNG for matte normalization: %w", closeErr)
	}

	return img, nil
}

func normalizedOwnershipImage(img image.Image) (*image.NRGBA, bool) {
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel, _ := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			pixel, pixelChanged := normalizeOwnershipPixel(pixel)
			changed = changed || pixelChanged

			out.SetNRGBA(x, y, pixel)
		}
	}

	return out, changed
}

func normalizeOwnershipPixel(pixel color.NRGBA) (color.NRGBA, bool) {
	if pixel.A == 0 {
		if pixel.R == 255 && pixel.G == 255 && pixel.B == 255 {
			return pixel, false
		}

		return whiteOwnershipPixel(pixel), true
	}

	if pixel.A == 204 && similarRGB(pixel.R, pixel.G, pixel.B) && pixel.R < 128 {
		return whiteOwnershipPixel(pixel), true
	}

	return pixel, false
}

func whiteOwnershipPixel(pixel color.NRGBA) color.NRGBA {
	pixel.R = 255
	pixel.G = 255
	pixel.B = 255

	return pixel
}

func writeOwnershipPNG(path string, img image.Image) error {
	file, err := os.Create(path) // #nosec G304 - path is generated by ownership plotting.
	if err != nil {
		return fmt.Errorf("failed to rewrite ownership PNG matte: %w", err)
	}

	err = png.Encode(file, img)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to encode ownership PNG matte: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("failed to close ownership PNG matte: %w", err)
	}

	return nil
}

func similarRGB(r, g, b uint8) bool {
	return absDiffUint8(r, g) <= 1 && absDiffUint8(g, b) <= 1
}

func absDiffUint8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}

	return b - a
}

func saveOwnershipBurndownAsJSON(output string, names []string, people [][]float64, dateRange []time.Time, lastTime time.Time) error {
	data := struct {
		Type      string      `json:"type"`
		Names     []string    `json:"names"`
		People    [][]float64 `json:"people"`
		DateRange []time.Time `json:"date_range"`
		Last      time.Time   `json:"last"`
	}{
		Type:      "ownership",
		Names:     names,
		People:    people,
		DateRange: dateRange,
		Last:      lastTime,
	}

	file, err := os.Create(output) // #nosec G304 - output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %w", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	err = encoder.Encode(data)
	if err != nil {
		return fmt.Errorf("failed to write JSON data: %w", err)
	}

	fmt.Printf("JSON data saved to %s\n", output)

	return nil
}

func argsortDescending(data []float64) []int {
	indices := make([]int, len(data))
	for i := range indices {
		indices[i] = i
	}

	sort.Slice(indices, func(i, j int) bool {
		return data[indices[i]] > data[indices[j]]
	})

	return indices
}

func argsortAscending(data []int) []int {
	indices := make([]int, len(data))
	for i := range indices {
		indices[i] = i
	}

	sort.Slice(indices, func(i, j int) bool {
		return data[indices[i]] < data[indices[j]]
	})

	return indices
}

func findFirstNonZero(row []float64) int {
	for i, val := range row {
		if val > 0 {
			return i
		}
	}

	return math.MaxInt
}

func reorder(data [][]float64, indices []int) [][]float64 {
	reordered := make([][]float64, len(indices))
	for i, idx := range indices {
		reordered[i] = data[idx]
	}

	return reordered
}

func reorderStrings(data []string, indices []int) []string {
	reordered := make([]string, len(indices))
	for i, idx := range indices {
		reordered[i] = data[idx]
	}

	return reordered
}
