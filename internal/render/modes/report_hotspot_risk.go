package modes

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// hotspotRiskChartedFiles is how many bars the ranked chart draws. It bounds the chart only:
// hotspot-risk_table.tsv carries every file the run reported, however large
// --hotspot-risk-top was.
const hotspotRiskChartedFiles = 20

func HotspotRisk(reader readers.Reader, output string) error {
	hotspotReader, ok := reader.(readers.HotspotRiskReader)
	if !ok {
		return fmt.Errorf("%w: hotspot risk", readers.ErrAnalysisMissing)
	}

	data, err := hotspotReader.GetHotspotRisk()
	if err != nil {
		return fmt.Errorf("failed to get hotspot risk data: %w", err)
	}

	if len(data.Files) == 0 {
		return errNoHotspotRiskFiles
	}

	files := append([]readers.HotspotRiskFile(nil), data.Files...)
	sort.Slice(files, func(i, j int) bool {
		return files[i].RiskScore > files[j].RiskScore
	})

	// The table gets every file the run reported; the chart gets a readable prefix of them.
	// The figure is a fixed size and does not grow with the bar count, so honouring a large
	// --hotspot-risk-top here would only overcrowd it - the full ranking is in the TSV.
	charted := files
	if len(charted) > hotspotRiskChartedFiles {
		charted = charted[:hotspotRiskChartedFiles]
	}

	labels := make([]string, len(charted))

	values := make(floatSeries, len(charted))
	for i, file := range charted {
		labels[i] = compactPathLabel(file.Path)
		values[i] = file.RiskScore
	}

	_, _ = fmt.Fprintf(os.Stdout, "Hotspot risk: %d files, window=%d days, top risk=%.3f (%s)\n",
		len(data.Files), data.WindowDays, files[0].RiskScore, files[0].Path)

	err = plotHotspotRiskRanked(titleRepositoryName(reader.GetName()), charted, labels, values, output)
	if err != nil {
		return err
	}

	err = writeHotspotRiskTable(files, siblingOutputPath(output, "hotspot-risk.png", "table.tsv"))
	if err != nil {
		return err
	}

	printHotspotRiskTable(files, 10)

	return nil
}

func writeHotspotRiskTable(files []readers.HotspotRiskFile, output string) error {
	var buffer bytes.Buffer
	buffer.WriteString("rank\trisk_score\tsize\tchurn\tcoupling_degree\townership_gini\tfile\n")

	for i, file := range files {
		fmt.Fprintf(&buffer, "%d\t%.6f\t%d\t%d\t%d\t%.6f\t%s\n",
			i+1, file.RiskScore, file.Size, file.Churn, file.CouplingDegree, file.OwnershipGini, file.Path)
	}

	if output == "" {
		output = "hotspot-risk-table.tsv"
	}

	err := os.MkdirAll(filepath.Dir(output), 0o750)
	if err != nil && filepath.Dir(output) != "." {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	err = os.WriteFile(output, buffer.Bytes(), 0o600)
	if err != nil {
		return fmt.Errorf("failed to write hotspot risk table: %w", err)
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

func plotHotspotRiskRanked(
	repoName string,
	files []readers.HotspotRiskFile,
	labels []string,
	values floatSeries,
	output string,
) error {
	output, err := resolveReportOutput(output, "hotspot-risk.png")
	if err != nil {
		return err
	}

	series := buildHotspotRiskSeries(files, labels, values)

	plot, err := newHotspotRiskPlot()
	if err != nil {
		return err
	}

	err = drawHotspotRiskBars(plot.riskAxes, series, repoName)
	if err != nil {
		return err
	}

	renderAlpha := hotspotComponentRenderAlpha(output)

	err = drawHotspotRiskComponents(plot.componentAxes, series, renderAlpha)
	if err != nil {
		return err
	}

	err = saveReportFigure(plot.figure, output, plot.width, plot.height)
	if err != nil {
		return err
	}

	err = restoreHotspotComponentAlpha(output, renderAlpha)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Saved %s\n", output)

	return nil
}

type hotspotRiskSeries struct {
	positions    []float64
	risk         []float64
	size         []float64
	churn        []float64
	coupling     []float64
	ownership    []float64
	ticks        []float64
	displayNames []string
	maxRisk      float64
}

type hotspotRiskPlot struct {
	figure        *core.Figure
	riskAxes      *core.Axes
	componentAxes *core.Axes
	width         int
	height        int
}

func buildHotspotRiskSeries(
	files []readers.HotspotRiskFile,
	labels []string,
	values floatSeries,
) hotspotRiskSeries {
	series := newHotspotRiskSeries(len(files))
	maxSize, maxChurn, maxCoupling := hotspotMaxima(files)

	for index, file := range files {
		series.positions[index] = float64(index)
		series.ticks[index] = float64(index)
		series.risk[index] = float64(values[index])
		series.maxRisk = math.Max(series.maxRisk, series.risk[index])
		series.displayNames[index] = hotspotDisplayName(labels[index], file.Path)
		series.size[index] = hotspotSizeNormalized(file, maxSize)
		series.churn[index] = hotspotNormalized(file.ChurnNormalized, float64(file.Churn), maxChurn)
		series.coupling[index] = hotspotNormalized(
			file.CouplingNormalized, float64(file.CouplingDegree), maxCoupling,
		)
		series.ownership[index] = hotspotNormalized(
			file.OwnershipNormalized, file.OwnershipGini, 1,
		)
	}

	return series
}

func newHotspotRiskSeries(count int) hotspotRiskSeries {
	return hotspotRiskSeries{
		positions:    make([]float64, count),
		risk:         make([]float64, count),
		size:         make([]float64, count),
		churn:        make([]float64, count),
		coupling:     make([]float64, count),
		ownership:    make([]float64, count),
		ticks:        make([]float64, count),
		displayNames: make([]string, count),
	}
}

func newHotspotRiskPlot() (hotspotRiskPlot, error) {
	width, height := reportPlotPixels("hotspot-risk.png")
	figure := newHotspotRiskFigure(width, height)

	grid := figure.GridSpec(
		1,
		2,
		core.WithGridSpecPadding(0.18, 0.95, 0.14, 0.88),
		core.WithGridSpecSpacing(0.22, 0.1),
		core.WithGridSpecWidthRatios(3, 2),
	)
	if grid == nil {
		return hotspotRiskPlot{}, errHotspotRiskAxes
	}

	riskAxes := grid.Cell(0, 0).AddAxes()

	componentAxes := grid.Cell(0, 1).AddAxes()
	if riskAxes == nil || componentAxes == nil {
		return hotspotRiskPlot{}, errHotspotRiskAxes
	}

	return hotspotRiskPlot{
		figure: figure, riskAxes: riskAxes, componentAxes: componentAxes, width: width, height: height,
	}, nil
}

func drawHotspotRiskBars(axes *core.Axes, series hotspotRiskSeries, repoName string) error {
	orientation := core.BarHorizontal
	barHeight := 0.8

	bars, err := axes.Bar(series.positions, series.risk, core.BarOptions{
		Width: optional.Of(barHeight), Orientation: optional.Of(orientation),
	})
	if err != nil {
		return fmt.Errorf("failed to plot hotspot risk bars: %w", err)
	}

	bars.Colors = hotspotRiskColors(series.risk, series.maxRisk)
	bars.EdgeColor = render.Color{R: 0, G: 0, B: 0, A: 1}
	bars.EdgeWidth = 0.5

	axes.SetTitle("Top Risky Files - " + repoName)
	axes.SetXLabel("Composite Risk Score")
	configureHotspotRiskXRange(axes, series.maxRisk)
	axes.SetYLim(-0.5, float64(len(series.risk))-0.5)
	axes.InvertY()
	axes.AddXGrid()
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: series.ticks}
	axes.YAxis.Formatter = ticker.FixedFormatter{Labels: series.displayNames}

	return nil
}

func configureHotspotRiskXRange(axes *core.Axes, maxRisk float64) {
	if maxRisk != 0 {
		axes.SetXLim(0, maxRisk*1.1)
		return
	}

	axes.SetXLim(-0.05, 0.05)

	black := render.Color{R: 0, G: 0, B: 0, A: 1}
	lineWidth := 0.5
	axes.AxVLine(0, core.VLineOptions{Color: optional.Of(black), LineWidth: optional.Of(lineWidth)})
}

func hotspotComponentRenderAlpha(output string) float64 {
	if strings.EqualFold(filepath.Ext(output), extensionSVG) {
		return 0.8
	}

	return 1
}

func drawHotspotRiskComponents(axes *core.Axes, series hotspotRiskSeries, alpha float64) error {
	err := addHotspotComponentBars(
		axes, series.positions, series.size, nil, "#3498db", "Size (log)", alpha,
	)
	if err != nil {
		return err
	}

	left := append([]float64(nil), series.size...)

	err = addHotspotComponentBars(
		axes, series.positions, series.churn, left, "#e74c3c", "Churn", alpha,
	)
	if err != nil {
		return err
	}

	addHotspotComponentValues(left, series.churn)

	err = addHotspotComponentBars(
		axes, series.positions, series.coupling, left, "#f39c12", "Coupling", alpha,
	)
	if err != nil {
		return err
	}

	addHotspotComponentValues(left, series.coupling)

	err = addHotspotComponentBars(
		axes, series.positions, series.ownership, left, "#9b59b6", "Ownership", alpha,
	)
	if err != nil {
		return err
	}

	configureHotspotComponentAxes(axes, series)

	return nil
}

func addHotspotComponentValues(left, values []float64) {
	for index := range left {
		left[index] += values[index]
	}
}

func configureHotspotComponentAxes(axes *core.Axes, series hotspotRiskSeries) {
	axes.SetTitle("Risk Components")
	axes.SetXLabel("Normalized Factors")
	axes.SetXLim(0, 4)
	axes.SetYLim(-0.5, float64(len(series.risk))-0.5)
	axes.InvertY()
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: series.ticks}
	axes.YAxis.Formatter = ticker.NullFormatter{}
	axes.YAxis.ShowTicks = false
	legend := axes.AddLegend()
	legend.Location = core.LegendLowerRight
}

func restoreHotspotComponentAlpha(output string, renderAlpha float64) error {
	const componentAlpha = 0.8
	if renderAlpha == componentAlpha {
		return nil
	}

	return applyHotspotComponentAlpha(output, componentAlpha)
}

func addHotspotComponentBars(ax *core.Axes, y, values, left []float64, hex, label string, alpha float64) error {
	orientation := core.BarHorizontal
	barHeight := 0.8
	c := renderColor(mustHexColor(hex))

	bars, err := ax.Bar(y, values, core.BarOptions{
		Color:       optional.Of(c),
		Width:       optional.Of(barHeight),
		Baselines:   left,
		Orientation: optional.Of(orientation),
		Alpha:       optional.Of(alpha),
		Label:       label,
	})
	if err != nil {
		return fmt.Errorf("failed to plot hotspot %s bars: %w", label, err)
	}

	bars.Colors = make([]render.Color, len(values))
	for i := range bars.Colors {
		bars.Colors[i] = c
	}

	return nil
}

func applyHotspotComponentAlpha(path string, alpha float64) error {
	img, err := readReportMetricPNG(path, "hotspot risk")
	if err != nil {
		return err
	}

	targetAlpha := uint8(math.Round(math.Max(0, math.Min(1, alpha)) * 255))

	out, changed := normalizeHotspotComponentImage(img, targetAlpha)
	if !changed {
		return nil
	}

	return writeReportMetricPNG(path, out, "hotspot risk alpha")
}

func normalizeHotspotComponentImage(img image.Image, targetAlpha uint8) (*image.NRGBA, bool) {
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel, _ := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			pixel, pixelChanged := normalizeHotspotComponentPixel(pixel, targetAlpha)
			changed = changed || pixelChanged

			out.SetNRGBA(x, y, pixel)
		}
	}

	return out, changed
}

func normalizeHotspotComponentPixel(pixel color.NRGBA, targetAlpha uint8) (color.NRGBA, bool) {
	if !isHotspotComponentColor(pixel) || pixel.A == targetAlpha {
		return pixel, false
	}

	pixel.A = targetAlpha

	return pixel, true
}

func isHotspotComponentColor(pixel color.NRGBA) bool {
	switch [3]uint8{pixel.R, pixel.G, pixel.B} {
	case [3]uint8{0x34, 0x98, 0xdb},
		[3]uint8{0xe7, 0x4c, 0x3c},
		[3]uint8{0xf3, 0x9c, 0x12},
		[3]uint8{0x9b, 0x59, 0xb6}:
		return true
	default:
		return false
	}
}

func hotspotMaxima(files []readers.HotspotRiskFile) (float64, float64, float64) {
	maxSize, maxChurn, maxCoupling := 1.0, 1.0, 1.0
	for _, file := range files {
		maxSize = math.Max(maxSize, float64(file.Size))
		maxChurn = math.Max(maxChurn, float64(file.Churn))
		maxCoupling = math.Max(maxCoupling, float64(file.CouplingDegree))
	}

	return maxSize, maxChurn, maxCoupling
}

func hotspotSizeNormalized(file readers.HotspotRiskFile, maxSize float64) float64 {
	if file.SizeNormalized > 0 {
		return file.SizeNormalized
	}

	if maxSize <= 0 {
		return 0
	}

	return math.Log(float64(file.Size)+1) / math.Log(maxSize+1)
}

func hotspotNormalized(normalized, raw, maxValue float64) float64 {
	if normalized > 0 {
		return normalized
	}

	if maxValue <= 0 {
		return 0
	}

	return raw / maxValue
}

func hotspotDisplayName(label, path string) string {
	if strings.Contains(path, "/") {
		parts := strings.Split(path, "/")
		if len(parts) > 3 {
			return ".../" + strings.Join(parts[len(parts)-2:], "/")
		}

		return path
	}

	return label
}

func hotspotRiskColors(values []float64, maxValue float64) []render.Color {
	if maxValue <= 0 {
		maxValue = 1
	}

	colors := make([]render.Color, len(values))
	for i, value := range values {
		colors[i] = renderColor(riskGradient(value / maxValue))
	}

	return colors
}

func riskGradient(ratio float64) color.Color {
	ratio = math.Max(0, math.Min(1, ratio))
	if ratio < 0.5 {
		t := ratio * 2

		return interpolateColor(
			color.RGBA{R: 26, G: 152, B: 80, A: 255},
			color.RGBA{R: 255, G: 255, B: 191, A: 255},
			t,
		)
	}

	return interpolateColor(
		color.RGBA{R: 255, G: 255, B: 191, A: 255},
		color.RGBA{R: 215, G: 48, B: 39, A: 255},
		(ratio-0.5)*2,
	)
}

func printHotspotRiskTable(files []readers.HotspotRiskFile, limit int) {
	if len(files) < limit {
		limit = len(files)
	}

	fmt.Printf("\nTop %d High-Risk Files\n", limit)
	fmt.Printf("%-5s %8s %6s %6s %9s %6s  %s\n", "Rank", "Risk", "Size", "Churn", "Coupling", "Gini", "File")

	for i := range limit {
		file := files[i]
		fmt.Printf("%-5d %8.4f %6d %6d %9d %6.3f  %s\n",
			i+1, file.RiskScore, file.Size, file.Churn, file.CouplingDegree, file.OwnershipGini, file.Path)
	}
}

func newHotspotRiskFigure(width, height int) *core.Figure {
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
		func(rc *style.RC) {
			rc.TitleFontSize = 12
			rc.AxisLabelFontSize = 11
			rc.YTickLabelFontSize = 9
			rc.LegendFontSize = 9
		},
	)
}
