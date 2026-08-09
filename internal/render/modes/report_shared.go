package modes

import (
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

	"github.com/cwbudde/hercules/internal/render/graphics"
)

var (
	errNoTemporalActivityValues  = errors.New("no temporal activity values found")
	errTemporalActivityAxes      = errors.New("failed to create temporal activity axes")
	errNoBusFactorSnapshots      = errors.New("no bus factor snapshots found")
	errNoSnapshotsInRange        = errors.New("no snapshots inside the requested date range")
	errBusFactorGaugeAxes        = errors.New("failed to create bus factor gauge axes")
	errOwnershipSubsystemAxes    = errors.New("failed to create ownership subsystem axes")
	errKnowledgeDistributionAxes = errors.New("failed to create knowledge diffusion axes")
	errKnowledgeSilosAxes        = errors.New("failed to create knowledge silos axes")
	errHotspotRiskAxes           = errors.New("failed to create hotspot risk axes")
	errNoOwnershipSnapshots      = errors.New("no ownership concentration snapshots found")
	errNoKnowledgeDiffusionData  = errors.New("no knowledge diffusion data found")
	errNoHotspotRiskFiles        = errors.New("no hotspot risk files found")
	errBusFactorSubsystemAxes    = errors.New("failed to create bus factor subsystem axes")
	errFigureNoAxes              = errors.New("figure has no axes")
)

// hideAxesContent removes spines, ticks, and labels from an axes, mirroring
// matplotlib's ax.axis("off") for text-only / pie panels.
func hideAxesContent(ax *core.Axes) {
	if ax == nil {
		return
	}

	ax.ShowFrame = false
	if ax.XAxis != nil {
		ax.XAxis.ShowSpine = false
		ax.XAxis.ShowTicks = false
		ax.XAxis.ShowLabels = false
	}

	if ax.YAxis != nil {
		ax.YAxis.ShowSpine = false
		ax.YAxis.ShowTicks = false
		ax.YAxis.ShowLabels = false
	}
}

type xyPoint struct {
	X, Y float64
}

type xySeries []xyPoint

type floatSeries []float64

type namedSeries struct {
	Name   string
	Points xySeries
}

// namedTimeSeries is namedSeries over a real date axis. It is a separate type
// rather than an optional field on namedSeries so that a caller cannot half-fill
// one and get a chart with two unit systems on one axis.
type namedTimeSeries struct {
	Name   string
	Dates  []time.Time
	Values []float64
}

// sampledTab20Colors returns n series colors spread across tab20.
//
// The spread is only usable while it stays injective: the step between
// consecutive entries is len(palette)/(n-1), so once n exceeds the palette size
// neighbouring series round onto the same entry. A combined run with 25
// developers put the first two of them on tab20[0], stacked directly on top of
// each other and indistinguishable. Beyond the palette size, cycle instead.
func sampledTab20Colors(n int) []render.Color {
	if n <= 0 {
		return nil
	}

	palette := graphics.PythonLaboursColorPalette(20)
	if n > len(palette) {
		return cycledPaletteColors(palette, n)
	}

	colors := make([]render.Color, n)
	for i := range colors {
		index := 0
		if n > 1 {
			index = int(float64(i) * float64(len(palette)) / float64(n-1))
			if index >= len(palette) {
				index = len(palette) - 1
			}
		}

		colors[i] = renderColor(palette[index])
	}

	return colors
}

// cycledPaletteColors walks the palette in order and darkens it once per
// completed pass, so a series that has to reuse an entry is still told apart
// from the earlier series wearing it - and, more importantly, is never the same
// color as the series stacked next to it.
func cycledPaletteColors(palette []color.Color, n int) []render.Color {
	colors := make([]render.Color, n)
	for i := range colors {
		colors[i] = darkenColor(renderColor(palette[i%len(palette)]), i/len(palette))
	}

	return colors
}

// darkenColor scales a color towards black by one step per pass, with a floor so
// a third or fourth pass does not collapse everything into black.
func darkenColor(c render.Color, passes int) render.Color {
	if passes <= 0 {
		return c
	}

	factor := math.Max(math.Pow(0.62, float64(passes)), 0.3)

	return render.Color{R: c.R * factor, G: c.G * factor, B: c.B * factor, A: c.A}
}

func plotIntBars(title, xLabel, yLabel string, labels []string, values []int, output, defaultOutput string) error {
	plotValues := make(floatSeries, len(values))
	for i, value := range values {
		plotValues[i] = float64(value)
	}

	return plotFloatBars(title, xLabel, yLabel, labels, plotValues, output, defaultOutput)
}

func plotFloatBars(
	title, xLabel, yLabel string,
	labels []string,
	values floatSeries,
	output, defaultOutput string,
) error {
	output, err := resolveReportOutput(output, defaultOutput)
	if err != nil {
		return err
	}

	plotValues := make([]float64, len(values))
	for i, value := range values {
		plotValues[i] = float64(value)
	}

	width, height := reportPlotInches(defaultOutput)

	err = graphics.PlotBarChartMatplotlib(labels, plotValues, graphics.MatplotlibBarOptions{
		Title:        title,
		XLabel:       xLabel,
		YLabel:       yLabel,
		Output:       output,
		WidthInches:  width,
		HeightInches: height,
		RotateX:      len(labels) > 8,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

func reportFigureAxes(fig *core.Figure) (*core.Axes, error) {
	grid := fig.Subplots(1, 1)
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return nil, errFigureNoAxes
	}

	return grid[0][0], nil
}

func readReportMetricPNG(path, purpose string) (image.Image, error) {
	file, err := os.Open(path) // #nosec G304 - path is generated by report plotting.
	if err != nil {
		return nil, fmt.Errorf("failed to open %s PNG for normalization: %w", purpose, err)
	}

	img, _, decodeErr := image.Decode(file)
	closeErr := file.Close()

	if decodeErr != nil {
		return nil, fmt.Errorf("failed to decode %s PNG for normalization: %w", purpose, decodeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("failed to close %s PNG for normalization: %w", purpose, closeErr)
	}

	return img, nil
}

func writeReportMetricPNG(path string, img image.Image, purpose string) error {
	file, err := os.Create(path) // #nosec G304 - path is generated by report plotting.
	if err != nil {
		return fmt.Errorf("failed to rewrite %s PNG: %w", purpose, err)
	}

	encodeErr := png.Encode(file, img)
	if encodeErr != nil {
		_ = file.Close()
		return fmt.Errorf("failed to encode %s PNG: %w", purpose, encodeErr)
	}

	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("failed to close %s PNG: %w", purpose, closeErr)
	}

	return nil
}

func plotLineSeries(title, xLabel, yLabel string, series []namedSeries, output, defaultOutput string) error {
	output, err := resolveReportOutput(output, defaultOutput)
	if err != nil {
		return err
	}

	plotSeries := make([]graphics.MatplotlibLineSeries, len(series))
	for i, item := range series {
		x := make([]float64, len(item.Points))

		y := make([]float64, len(item.Points))
		for j, point := range item.Points {
			x[j] = point.X
			y[j] = point.Y
		}

		plotSeries[i] = graphics.MatplotlibLineSeries{Name: item.Name, X: x, Y: y, Marker: true}
	}

	width, height := reportPlotInches(defaultOutput)

	err = graphics.PlotLineChartMatplotlib(plotSeries, graphics.MatplotlibLineOptions{
		Title:        title,
		XLabel:       xLabel,
		YLabel:       yLabel,
		Output:       output,
		WidthInches:  width,
		HeightInches: height,
		ShowGrid:     true,
		Legend:       true,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

// plotTimeSeries is plotLineSeries over a date axis. The x label is fixed:
// every caller plots calendar time, and letting one of them pass "Tick" while
// handing over dates is exactly the mislabelling this path exists to remove.
func plotTimeSeries(title, yLabel string, series []namedTimeSeries, output, defaultOutput string) error {
	output, err := resolveReportOutput(output, defaultOutput)
	if err != nil {
		return err
	}

	plotSeries := make([]graphics.MatplotlibLineSeries, len(series))
	for i, item := range series {
		plotSeries[i] = graphics.MatplotlibLineSeries{
			Name:   item.Name,
			Dates:  item.Dates,
			Y:      item.Values,
			Marker: true,
		}
	}

	width, height := reportPlotInches(defaultOutput)

	err = graphics.PlotLineChartMatplotlib(plotSeries, graphics.MatplotlibLineOptions{
		Title:        title,
		XLabel:       "Date",
		YLabel:       yLabel,
		Output:       output,
		WidthInches:  width,
		HeightInches: height,
		ShowGrid:     true,
		Legend:       true,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

func resolveReportOutput(output, defaultOutput string) (string, error) {
	if output == "" {
		output = defaultOutput
	}

	if dir := filepath.Dir(output); dir != "." {
		err := os.MkdirAll(dir, 0o750)
		if err != nil {
			return "", fmt.Errorf("failed to create output directory %s: %w", dir, err)
		}
	}

	return output, nil
}

// reportPlotSizesInches holds the per-chart figure sizes. It used to be a
// switch, which grew one branch per chart until the linter called it: a lookup
// table says the same thing and stays flat as charts are added.
var reportPlotSizesInches = map[string][2]float64{
	"temporal-activity.png":                {16, 10},
	"refactoring-proxy.png":                {16, 6},
	"bus-factor.png":                       {14, 6},
	"ownership-concentration.png":          {14, 6},
	"bus-factor-timeline.png":              {14, 6},
	"ownership-concentration-timeline.png": {14, 6},
	"bus-factor-subsystems.png":            {12, 6},
	"knowledge-diffusion.png":              {12, 6},
	"knowledge-diffusion-trend.png":        {14, 6},
	"knowledge-diffusion-lorenz.png":       {8, 8},
	"knowledge-diffusion-silos.png":        {14, 12.5},
	"hotspot-risk.png":                     {12, 8},
	onboardingRampupOutput:                 {12, 6},
	onboardingTimeToFirstOutput:            {12, 6},
	onboardingAuthorsOutput:                {14, 8},
}

func reportPlotInches(defaultOutput string) (float64, float64) {
	if size, ok := reportPlotSizesInches[defaultOutput]; ok {
		return size[0], size[1]
	}

	return 16, 8
}

func reportPlotPixels(defaultOutput string) (int, int) {
	width, height := reportPlotInches(defaultOutput)
	return graphics.InchesToPixels(width), graphics.InchesToPixels(height)
}

func newReportFigure(width, height int) *core.Figure {
	background := render.Color{R: 1, G: 1, B: 1, A: 0}
	text := render.Color{R: 0, G: 0, B: 0, A: 1}

	return core.NewFigure(
		width,
		height,
		style.WithTheme(style.ThemeGGPlot),
		style.WithFont(graphics.PythonPlotFontFamily, graphics.PythonPlotFontSize()),
		style.WithTextColor(0, 0, 0, 1),
		style.WithBackground(1, 1, 1, 0),
		style.WithAxesBackground(background),
		style.WithAxesEdgeColor(text),
		style.WithLegendColors(render.Color{R: 1, G: 1, B: 1, A: 0.8}, background, text),
	)
}

func saveReportFigure(fig *core.Figure, output string, width, height int) error {
	fig.TightLayout()
	return saveReportFigureDirect(fig, output, width, height)
}

func saveReportFigureWithoutTightLayout(fig *core.Figure, output string, width, height int) error {
	return saveReportFigureDirect(fig, output, width, height)
}

func saveReportFigureDirect(fig *core.Figure, output string, width, height int) error {
	config := backends.Config{
		Width:       width,
		Height:      height,
		Background:  render.Color{R: 1, G: 1, B: 1, A: 0},
		DPI:         100,
		Transparent: true,
	}

	switch strings.ToLower(filepath.Ext(output)) {
	case extensionSVG:
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %w", err)
		}

		err = core.SaveSVG(fig, renderer, output)
		if err != nil {
			return fmt.Errorf("failed to save SVG to %s: %w", output, err)
		}

		return nil
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %w", err)
		}

		err = core.SavePNG(fig, renderer, output)
		if err != nil {
			return fmt.Errorf("failed to save PNG to %s: %w", output, err)
		}

		return whitenTransparentPNGMatte(output)
	}
}

func whitenTransparentPNGMatte(path string) error {
	img, err := readReportMetricPNG(path, "report")
	if err != nil {
		return err
	}

	out, changed := whitenTransparentImage(img)
	if !changed {
		return nil
	}

	return writeReportMetricPNG(path, out, "normalized report")
}

func whitenTransparentImage(img image.Image) (*image.NRGBA, bool) {
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel, _ := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			pixel, pixelChanged := whitenTransparentPixel(pixel)
			changed = changed || pixelChanged

			out.SetNRGBA(x, y, pixel)
		}
	}

	return out, changed
}

func whitenTransparentPixel(pixel color.NRGBA) (color.NRGBA, bool) {
	if pixel.A != 0 || (pixel.R == 255 && pixel.G == 255 && pixel.B == 255) {
		return pixel, false
	}

	pixel.R = 255
	pixel.G = 255
	pixel.B = 255

	return pixel, true
}

func rangeWithPadding(values []float64, padding float64) (float64, float64) {
	if len(values) == 0 {
		return -padding, padding
	}

	minValue, maxValue := values[0], values[0]
	for _, value := range values[1:] {
		minValue = math.Min(minValue, value)
		maxValue = math.Max(maxValue, value)
	}

	return minValue - padding, maxValue + padding
}

func renderColor(c color.Color) render.Color {
	r, g, b, a := c.RGBA()

	return render.Color{
		R: float64(r) / 65535,
		G: float64(g) / 65535,
		B: float64(b) / 65535,
		A: float64(a) / 65535,
	}
}

func mustHexColor(hex string) color.RGBA {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return color.RGBA{A: 255}
	}

	r, err := strconv.ParseUint(hex[0:2], 16, 8)
	if err != nil {
		return color.RGBA{A: 255}
	}

	g, err := strconv.ParseUint(hex[2:4], 16, 8)
	if err != nil {
		return color.RGBA{A: 255}
	}

	b, err := strconv.ParseUint(hex[4:6], 16, 8)
	if err != nil {
		return color.RGBA{A: 255}
	}

	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}

func interpolateColor(a, b color.RGBA, t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))

	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

func sortedIntKeys[T any](values map[int]T) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Ints(keys)

	return keys
}

func topStringIntPairs(values map[string]int, limit int, descending bool) ([]string, []int) {
	type pair struct {
		Key   string
		Value int
	}

	pairs := make([]pair, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, pair{Key: key, Value: value})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Value == pairs[j].Value {
			return pairs[i].Key < pairs[j].Key
		}

		if descending {
			return pairs[i].Value > pairs[j].Value
		}

		return pairs[i].Value < pairs[j].Value
	})

	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}

	labels := make([]string, len(pairs))

	resultValues := make([]int, len(pairs))
	for i, pair := range pairs {
		labels[i] = pair.Key
		resultValues[i] = pair.Value
	}

	return labels, resultValues
}

func siblingOutputPath(output, defaultOutput, suffix string) string {
	if output == "" {
		output = defaultOutput
	}

	ext := filepath.Ext(output)
	if ext == "" {
		ext = ".png"
	}

	base := output[:len(output)-len(filepath.Ext(output))]
	if filepath.Ext(suffix) != "" {
		return base + "_" + suffix
	}

	return base + "_" + suffix + ext
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}

	return total
}

func compactPathLabel(path string) string {
	base := filepath.Base(path)
	if len(base) <= 24 {
		return base
	}

	return base[:21] + "..."
}
