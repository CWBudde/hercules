package modes

import (
	"fmt"
	"image/color"
	"maps"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// KnowledgeDiffusion renders the diffusion charts.
//
// startTime/endTime reach only the trend chart. It is the sole output with a
// time axis: UniqueEditorsOverTime is tick-indexed, while the distribution, silo
// and Lorenz charts are whole-history totals per file, carrying no timestamps a
// date range could be applied to.
func KnowledgeDiffusion(reader readers.Reader, output string, detail bool, startTime, endTime *time.Time) error {
	diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
	if !ok {
		return fmt.Errorf("%w: knowledge diffusion", readers.ErrAnalysisMissing)
	}

	data, err := diffusionReader.GetKnowledgeDiffusion()
	if err != nil {
		return fmt.Errorf("failed to get knowledge diffusion data: %w", err)
	}

	if len(data.Distribution) == 0 && len(data.Files) == 0 {
		return errNoKnowledgeDiffusionData
	}

	labels, values := knowledgeDistribution(data)
	fmt.Printf("Knowledge diffusion: %d files, %d developers, window=%d months\n",
		len(data.Files), len(data.People), data.WindowMonths)

	distributionOutput := siblingOutputPath(output, "knowledge-diffusion.png", "distribution")

	err = plotKnowledgeDistribution(titleRepositoryName(reader.GetName()), labels, values, distributionOutput)
	if err != nil {
		return err
	}

	err = plotKnowledgeSilos(
		titleRepositoryName(reader.GetName()), data, siblingOutputPath(output, "knowledge-diffusion.png", "silos"),
	)
	if err != nil {
		return err
	}

	err = plotKnowledgeLorenz(
		titleRepositoryName(reader.GetName()), data, siblingOutputPath(output, "knowledge-diffusion.png", "lorenz"),
	)
	if err != nil {
		return err
	}

	if detail {
		begin, _ := reader.GetHeader()
		axis, dated := newTickAxis(begin, data.TickSize)

		err := plotKnowledgeTrend(
			data, axis, dated, startTime, endTime,
			siblingOutputPath(output, "knowledge-diffusion.png", "trend"),
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// plotKnowledgeLorenz renders a Lorenz curve of unique-editor distribution across files.
// X-axis: cumulative fraction of files (sorted by editor count ascending).
// Y-axis: cumulative fraction of total unique-editor slots.
// The diagonal represents perfect equality; bowing below indicates concentration.
func plotKnowledgeLorenz(repoName string, data *readers.KnowledgeDiffusionData, output string) error {
	counts := make([]int, 0, len(data.Files))
	for _, f := range data.Files {
		counts = append(counts, f.UniqueEditors)
	}

	sort.Ints(counts)
	n := len(counts)

	total := 0
	for _, c := range counts {
		total += c
	}

	if n == 0 || total == 0 {
		return nil
	}

	lorenz := make(xySeries, n+1)
	lorenz[0] = xyPoint{X: 0, Y: 0}

	cum := 0
	for i, c := range counts {
		cum += c
		lorenz[i+1] = xyPoint{
			X: float64(i+1) / float64(n),
			Y: float64(cum) / float64(total),
		}
	}

	// Gini = 1 - 2 * area under the Lorenz curve (trapezoidal rule).
	area := 0.0

	for i := 1; i < len(lorenz); i++ {
		dx := lorenz[i].X - lorenz[i-1].X
		area += dx * (lorenz[i].Y + lorenz[i-1].Y) / 2
	}

	gini := 1 - 2*area

	diagonal := xySeries{{X: 0, Y: 0}, {X: 1, Y: 1}}

	title := fmt.Sprintf("Editor Distribution (Lorenz Curve) - Gini=%.3f", gini)
	if repoName != "" {
		title = fmt.Sprintf("%s - %s", repoName, title)
	}

	return plotLineSeries(
		title,
		"Cumulative Fraction of Files",
		"Cumulative Fraction of Editors",
		[]namedSeries{
			{Name: fmt.Sprintf("Lorenz (Gini=%.3f)", gini), Points: lorenz},
			{Name: "Perfect equality", Points: diagonal},
		},
		output,
		"knowledge-diffusion-lorenz.png",
	)
}

func plotKnowledgeDistribution(repoName string, labels []string, values []int, output string) error {
	output, err := resolveReportOutput(output, "knowledge-diffusion.png")
	if err != nil {
		return err
	}

	series := buildKnowledgeDistributionSeries(labels, values)

	plot, err := newKnowledgeDistributionPlot(repoName)
	if err != nil {
		return err
	}

	drawKnowledgeDistribution(plot.axes, series)
	configureKnowledgeDistributionAxes(plot.axes, series)

	err = saveReportFigureWithoutTightLayout(plot.figure, output, plot.width, plot.height)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Saved %s\n", output)

	return nil
}

type knowledgeDistributionSeries struct {
	editorCounts      []float64
	fileCounts        []float64
	totalFiles        int
	singleEditorFiles int
	maxValue          float64
}

type knowledgeDistributionPlot struct {
	figure *core.Figure
	axes   *core.Axes
	width  int
	height int
}

func buildKnowledgeDistributionSeries(labels []string, values []int) knowledgeDistributionSeries {
	series := knowledgeDistributionSeries{
		editorCounts: make([]float64, len(values)),
		fileCounts:   make([]float64, len(values)),
	}
	for index, value := range values {
		editorCount := parseKnowledgeEditorCount(labels, index)
		series.editorCounts[index] = editorCount
		series.fileCounts[index] = float64(value)
		series.totalFiles += value

		series.maxValue = math.Max(series.maxValue, float64(value))
		if int(editorCount) == 1 {
			series.singleEditorFiles += value
		}
	}

	return series
}

func parseKnowledgeEditorCount(labels []string, index int) float64 {
	if index >= len(labels) {
		return float64(index)
	}

	editorCount := float64(index)

	_, err := fmt.Sscanf(labels[index], "%f", &editorCount)
	if err != nil {
		return float64(index)
	}

	return editorCount
}

func newKnowledgeDistributionPlot(repoName string) (knowledgeDistributionPlot, error) {
	width, height := reportPlotPixels("knowledge-diffusion.png")
	figure := newReportFigure(width, height)

	grid := figure.Subplots(1, 1, core.WithSubplotPadding(0.064, 0.989, 0.100, 0.936))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return knowledgeDistributionPlot{}, errKnowledgeDistributionAxes
	}

	axes := grid[0][0]
	axes.SetTitle(knowledgeDistributionTitle(repoName))
	axes.SetXLabel("Number of Unique Editors")
	axes.SetYLabel("Number of Files")

	return knowledgeDistributionPlot{figure: figure, axes: axes, width: width, height: height}, nil
}

func knowledgeDistributionTitle(repoName string) string {
	if repoName == "" {
		return "Knowledge Diffusion Distribution"
	}

	return repoName + " - Knowledge Diffusion Distribution"
}

func drawKnowledgeDistribution(axes *core.Axes, series knowledgeDistributionSeries) {
	for index, editorCount := range series.editorCounts {
		drawKnowledgeDistributionBar(axes, editorCount, series.fileCounts[index])
	}

	if series.totalFiles > 0 {
		drawSingleEditorSummary(axes, series.totalFiles, series.singleEditorFiles)
	}
}

func drawKnowledgeDistributionBar(axes *core.Axes, editorCount, fileCount float64) {
	barColor := renderColor(knowledgeDistributionColor(int(editorCount)))
	edgeColor := render.Color{R: 1, G: 1, B: 1, A: 1}
	edgeWidth := 0.5
	_, _ = axes.Bar([]float64{editorCount}, []float64{fileCount}, core.BarOptions{
		Color: optional.Of(barColor), EdgeColor: optional.Of(edgeColor), EdgeWidth: optional.Of(edgeWidth),
	})
	axes.Text(editorCount, fileCount+0.3, fmt.Sprintf("%.0f", fileCount), core.TextOptions{
		FontSize: 9.6,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignBottom,
	})
}

func drawSingleEditorSummary(axes *core.Axes, totalFiles, singleEditorFiles int) {
	percentage := float64(singleEditorFiles) / float64(totalFiles) * 100
	riskColor := renderColor(color.RGBA{R: 244, G: 67, B: 54, A: 255})
	boxColor := render.Color{R: 1, G: 1, B: 1, A: 0.8}
	axes.Text(
		0.98,
		0.95,
		fmt.Sprintf("Single-editor files: %d (%.0f%%)", singleEditorFiles, percentage),
		core.TextOptions{
			Coords:   core.Coords(core.CoordAxes),
			FontSize: 10.8,
			Color:    riskColor,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
			BBox: optional.Of(core.TextBBoxOptions{
				FaceColor: boxColor,
				EdgeColor: boxColor,
				Padding:   3,
			}),
		},
	)
}

func configureKnowledgeDistributionAxes(axes *core.Axes, series knowledgeDistributionSeries) {
	minimumX, maximumX := rangeWithPadding(series.editorCounts, 0.5)
	step := temporalActivityTickStep(math.Max(series.maxValue, 1))
	maximumY := math.Ceil(math.Max(series.maxValue, 1)/step) * step
	xTicks, xLabels := knowledgeDistributionXTicks(minimumX, maximumX)

	axes.SetXLim(minimumX, maximumX)
	axes.SetYLim(0, maximumY)
	axes.XAxis.Locator = ticker.FixedLocator{TicksList: xTicks}
	axes.XAxis.Formatter = ticker.FixedFormatter{Labels: xLabels}
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: knowledgeDistributionYTicks(maximumY, step)}
}

func knowledgeDistributionXTicks(minimum, maximum float64) ([]float64, []string) {
	ticks := make([]float64, 0, int(maximum-minimum)+2)

	labels := make([]string, 0, cap(ticks))
	for tick := math.Ceil(minimum); tick <= math.Floor(maximum); tick++ {
		ticks = append(ticks, tick)
		labels = append(labels, fmt.Sprintf("%.0f", tick))
	}

	return ticks, labels
}

// knowledgeDistributionYTicks labels the axis every step counts. The step used
// to be a flat 15, which suited a single repository but drew ~640 overlapping
// labels once a combined run put nine thousand files in the single-editor
// bucket - the same solid black bar temporalActivityTickStep was written to
// cure, so it does the choosing here too.
func knowledgeDistributionYTicks(maximum, step float64) []float64 {
	ticks := make([]float64, 0, int(maximum/step)+1)
	for tick := 0.0; tick <= maximum; tick += step {
		ticks = append(ticks, tick)
	}

	return ticks
}

func knowledgeDistribution(data *readers.KnowledgeDiffusionData) ([]string, []int) {
	distribution := make(map[int]int, len(data.Distribution))
	maps.Copy(distribution, data.Distribution)

	if len(distribution) == 0 {
		for _, file := range data.Files {
			distribution[file.UniqueEditors]++
		}
	}

	keys := make([]int, 0, len(distribution))
	for editors := range distribution {
		keys = append(keys, editors)
	}

	sort.Ints(keys)

	labels := make([]string, len(keys))

	values := make([]int, len(keys))
	for i, editors := range keys {
		labels[i] = strconv.Itoa(editors)
		values[i] = distribution[editors]
	}

	return labels, values
}

// knowledgeSiloChartedFiles caps the silo chart. The figure grows in height with the bar count
// (newKnowledgeSiloPlot), so this is a legibility limit, not a data limit.
const knowledgeSiloChartedFiles = 30

func plotKnowledgeSilos(repoName string, data *readers.KnowledgeDiffusionData, output string) error {
	files, ranked := rankedKnowledgeFiles(data.Files, data.WindowMonths, data.TickSize)
	if len(files) == 0 {
		return nil
	}

	if len(files) > knowledgeSiloChartedFiles {
		files = files[:knowledgeSiloChartedFiles]
	}

	labels := make([]string, len(files))
	uniqueValues := make(floatSeries, len(files))

	recentValues := make(floatSeries, len(files))
	for i, file := range files {
		labels[i] = truncateKnowledgeSiloLabel(file.Path)
		uniqueValues[i] = float64(file.UniqueEditors)
		recentValues[i] = float64(file.RecentEditors)
	}

	return plotKnowledgeSilosMatplotlib(
		repoName, labels, uniqueValues, recentValues, data.WindowMonths, ranked, output,
	)
}

func plotKnowledgeSilosMatplotlib(
	repoName string,
	labels []string,
	uniqueValues, recentValues floatSeries,
	windowMonths int,
	ranked bool,
	output string,
) error {
	output, err := resolveReportOutput(output, "knowledge-diffusion-silos.png")
	if err != nil {
		return err
	}

	series := buildKnowledgeSiloSeries(labels, uniqueValues, recentValues)

	plot, err := newKnowledgeSiloPlot(repoName, len(labels), ranked)
	if err != nil {
		return err
	}

	drawKnowledgeSiloBars(plot.axes, series, windowMonths)
	configureKnowledgeSiloAxes(plot.axes, series)

	err = saveReportFigureWithoutTightLayout(plot.figure, output, plot.width, plot.height)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Saved %s\n", output)

	return nil
}

type knowledgeSiloSeries struct {
	labels       []string
	totalY       []float64
	recentY      []float64
	totalValues  []float64
	recentValues []float64
	ticks        []float64
	maxValue     float64
}

type knowledgeSiloPlot struct {
	figure *core.Figure
	axes   *core.Axes
	width  int
	height int
}

func buildKnowledgeSiloSeries(
	labels []string,
	uniqueValues, recentValues floatSeries,
) knowledgeSiloSeries {
	series := knowledgeSiloSeries{
		labels:       labels,
		totalY:       make([]float64, len(labels)),
		recentY:      make([]float64, len(labels)),
		totalValues:  make([]float64, len(labels)),
		recentValues: make([]float64, len(labels)),
		ticks:        make([]float64, len(labels)),
	}
	for index := range labels {
		series.totalY[index] = float64(index) - 0.18
		series.recentY[index] = float64(index) + 0.18
		series.ticks[index] = float64(index)
		series.totalValues[index] = float64(uniqueValues[index])
		series.recentValues[index] = float64(recentValues[index])
		series.maxValue = math.Max(
			series.maxValue,
			math.Max(series.totalValues[index], series.recentValues[index]),
		)
	}

	return series
}

func newKnowledgeSiloPlot(repoName string, fileCount int, ranked bool) (knowledgeSiloPlot, error) {
	heightInches := math.Max(5, float64(fileCount)*0.35+2)
	width := 14 * 100
	height := int(heightInches * 100)
	figure := newKnowledgeSilosFigure(width, height)

	grid := figure.Subplots(1, 1, core.WithSubplotPadding(0.332, 0.947, 0.05, 0.97))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return knowledgeSiloPlot{}, errKnowledgeSilosAxes
	}

	axes := grid[0][0]
	axes.SetTitle(knowledgeSiloTitle(repoName, ranked))
	axes.SetXLabel("Number of Editors")

	return knowledgeSiloPlot{figure: figure, axes: axes, width: width, height: height}, nil
}

// knowledgeSiloTitle names the selection rule in the title, not only in the docs: the chart shows
// a prefix of the files, and which prefix depends on whether the input carries the ranking
// factors. Somebody looking at the PNG later has no other way to tell the two apart.
func knowledgeSiloTitle(repoName string, ranked bool) string {
	title := "Knowledge Silos"
	if repoName != "" {
		title = repoName + " - " + title
	}

	if ranked {
		return title + " (ranked by risk)"
	}

	return title + " (fewest editors first)"
}

func drawKnowledgeSiloBars(axes *core.Axes, series knowledgeSiloSeries, windowMonths int) {
	orientation := core.BarHorizontal
	barHeight := 0.35
	totalColor := renderColor(color.RGBA{R: 144, G: 202, B: 249, A: 255})
	recentColor := renderColor(color.RGBA{R: 21, G: 101, B: 192, A: 255})

	_, _ = axes.Bar(series.totalY, series.totalValues, core.BarOptions{
		Color:       optional.Of(totalColor),
		Width:       optional.Of(barHeight),
		Orientation: optional.Of(orientation),
		Label:       "Total unique editors",
	})
	_, _ = axes.Bar(series.recentY, series.recentValues, core.BarOptions{
		Color:       optional.Of(recentColor),
		Width:       optional.Of(barHeight),
		Orientation: optional.Of(orientation),
		Label:       fmt.Sprintf("Active in last %d months", windowMonths),
	})
	drawKnowledgeSiloLabels(axes, series)
}

func drawKnowledgeSiloLabels(axes *core.Axes, series knowledgeSiloSeries) {
	clipOff := false

	labelColor := render.Color{R: 0, G: 0, B: 0, A: 1}
	for index := range series.labels {
		drawKnowledgeSiloLabel(
			axes, series.totalValues[index], series.totalY[index], labelColor, optional.Of(clipOff),
		)
		drawKnowledgeSiloLabel(
			axes, series.recentValues[index], series.recentY[index], labelColor, optional.Of(clipOff),
		)
	}
}

func drawKnowledgeSiloLabel(
	axes *core.Axes,
	value, y float64,
	labelColor render.Color,
	clipOff optional.Value[bool],
) {
	axes.Text(value+0.1, y, fmt.Sprintf("%.0f", value), core.TextOptions{
		FontSize: 8.4,
		Color:    labelColor,
		VAlign:   core.TextVAlignMiddle,
		ClipOn:   clipOff,
	})
}

func configureKnowledgeSiloAxes(axes *core.Axes, series knowledgeSiloSeries) {
	axes.SetXLim(0, math.Max(series.maxValue*1.05, 1.05))
	axes.SetYLim(-1.85, float64(len(series.labels))+0.85)
	axes.InvertY()
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: series.ticks}
	axes.YAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), series.labels...)}
	labelStyle := axes.YAxis.MajorLabelStyle
	labelStyle.FontKey = graphics.PythonPlotMonoFontFamily
	axes.YAxis.MajorLabelStyle = labelStyle
	configureKnowledgeSiloLegend(axes)
}

func configureKnowledgeSiloLegend(axes *core.Axes) {
	legend := axes.AddLegend()
	legend.Location = core.LegendLowerRight
	legend.FontSize = 9.6
	legend.Padding = 5
	legend.RowGap = 2
	legend.BackgroundColor = render.Color{R: 0.9, G: 0.9, B: 0.9, A: 0.8}
	legend.BorderColor = render.Color{R: 0.8, G: 0.8, B: 0.8, A: 0.8}
	legend.TextColor = render.Color{R: 0, G: 0, B: 0, A: 1}
}

// plotKnowledgeTrend renders a max-unique-editors-over-time chart. Go-only,
// gated behind --knowledge-diffusion-detail.
func plotKnowledgeTrend(
	data *readers.KnowledgeDiffusionData,
	axis tickAxis,
	dated bool,
	startTime, endTime *time.Time,
	output string,
) error {
	trend := make(map[int]int)

	for _, file := range data.Files {
		for tick, editors := range file.UniqueEditorsOverTime {
			if editors > trend[tick] {
				trend[tick] = editors
			}
		}
	}

	if len(trend) == 0 {
		return nil
	}

	ticks := selectReportTicks(sortedIntKeys(trend), axis, dated, startTime, endTime, "knowledge diffusion")
	if len(ticks) == 0 {
		return fmt.Errorf("%w: knowledge diffusion trend", errNoSnapshotsInRange)
	}

	values := make([]float64, len(ticks))
	for i, tick := range ticks {
		values[i] = float64(trend[tick])
	}

	if dated {
		return plotTimeSeries(
			"Knowledge Diffusion Trend",
			"Max Unique Editors",
			[]namedTimeSeries{{Name: "Max editors", Dates: axis.datesOf(ticks), Values: values}},
			output,
			"knowledge-diffusion-trend.png",
		)
	}

	return plotLineSeries(
		"Knowledge Diffusion Trend",
		"Tick",
		"Max Unique Editors",
		[]namedSeries{{Name: "Max editors", Points: tickIndexSeries(ticks, values)}},
		output,
		"knowledge-diffusion-trend.png",
	)
}

type knowledgeFileSummary struct {
	Path          string
	UniqueEditors int
	RecentEditors int
	Lines         int
	Churn         int
	Age           int
	SiloRisk      float64
}

// knowledgeSiloDays is the average month the leaf uses to convert WindowMonths into ticks
// (leaves/knowledge_diffusion.go windowTicks). The renderer has to reproduce it to express a
// file's age as a fraction of the same window.
const knowledgeSiloMonthDays = 30.44

// rankedKnowledgeFiles orders files by how much their sole ownership costs: large, churning,
// single-editor and recently touched first.
//
// The score is a product of four factors, each in (0,1]:
//
//	size    = log(lines+1) / log(maxLines+1)      heavy-tailed, so logarithmic
//	churn   = log(churn+1) / log(maxChurn+1)      lifetime, not windowed - see below
//	silo    = 1 / uniqueEditors                   1.0 for a true silo, 0.5 for two editors
//	recency = 1 / (1 + age/windowTicks)           1.0 today, 0.5 one window ago, never 0
//
// Two choices in that formula are about not producing a degenerate all-zero ranking. The churn
// factor is the *lifetime* count rather than RecentChurn, because every file here has been
// edited at least once, so it is always positive - a repository whose whole history predates the
// window would otherwise score every file zero and fall straight back to the alphabetical
// ordering this ranking exists to replace. "Recently touched" is carried by the recency factor,
// which decays rather than cutting off for the same reason.
//
// Scoring happens here rather than in the leaf on purpose: normalising against a single run's
// maxima is what made merged hotspot-risk scores incomparable (PLAN.md T1.2c). The leaf stores
// the raw factors and this function normalises once, over whatever file set it was handed -
// one repository or a merge of 231.
// The bool reports whether the files could actually be ranked; false means the fallback
// ordering was used.
func rankedKnowledgeFiles(
	files map[string]readers.KnowledgeDiffusionFile, windowMonths int, tickSize int64,
) ([]knowledgeFileSummary, bool) {
	result := make([]knowledgeFileSummary, 0, len(files))
	for path, file := range files {
		result = append(result, knowledgeFileSummary{
			Path:          path,
			UniqueEditors: file.UniqueEditors,
			RecentEditors: file.RecentEditors,
			Lines:         file.Lines,
			Churn:         file.Churn,
			Age:           file.TicksSinceLastEdit,
		})
	}

	scoreKnowledgeSiloRisk(result, knowledgeSiloWindowTicks(windowMonths, tickSize))

	if knowledgeSiloRiskIsUniform(result) {
		// Nothing to rank: a result written before the leaf collected these factors carries no
		// lines and no churn. Keep the historical ordering so such a file renders exactly the
		// chart it always did, rather than an arbitrary one.
		sortKnowledgeFilesByEditors(result)

		return result, false
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].SiloRisk == result[j].SiloRisk {
			return result[i].Path < result[j].Path
		}

		return result[i].SiloRisk > result[j].SiloRisk
	})

	return result, true
}

// knowledgeSiloWindowTicks converts the recent-editor window into ticks, reproducing the leaf's
// conversion. It returns 0 when the tick size is unknown, which makes the recency factor a
// neutral 1 instead of a division by zero.
func knowledgeSiloWindowTicks(windowMonths int, tickSize int64) float64 {
	if windowMonths <= 0 || tickSize <= 0 {
		return 0
	}

	window := float64(windowMonths) * knowledgeSiloMonthDays * float64(24*time.Hour)

	return math.Floor(window / float64(tickSize))
}

func scoreKnowledgeSiloRisk(files []knowledgeFileSummary, windowTicks float64) {
	var maxLines, maxChurn float64

	for _, file := range files {
		maxLines = math.Max(maxLines, float64(file.Lines))
		maxChurn = math.Max(maxChurn, float64(file.Churn))
	}

	for i := range files {
		sizeFactor := normalizedLogFactor(files[i].Lines, maxLines)
		churnFactor := normalizedLogFactor(files[i].Churn, maxChurn)
		siloFactor := 1 / float64(max(1, files[i].UniqueEditors))
		recencyFactor := 1.0

		if windowTicks > 0 {
			recencyFactor = 1 / (1 + float64(max(0, files[i].Age))/windowTicks)
		}

		files[i].SiloRisk = sizeFactor * churnFactor * siloFactor * recencyFactor
	}
}

// normalizedLogFactor scales a heavy-tailed count into [0,1], the same way the hotspot-risk
// analysis does (leaves/hotspot_risk.go). It is duplicated rather than shared because the
// renderer must not import the analysis packages - that would link go-git and tree-sitter into
// the labours binary.
func normalizedLogFactor(value int, maximum float64) float64 {
	if value <= 0 || maximum <= 0 {
		return 0
	}

	return math.Log(float64(value)+1) / math.Log(maximum+1)
}

func knowledgeSiloRiskIsUniform(files []knowledgeFileSummary) bool {
	for _, file := range files {
		if file.SiloRisk != files[0].SiloRisk {
			return false
		}
	}

	return true
}

func sortKnowledgeFilesByEditors(files []knowledgeFileSummary) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].UniqueEditors == files[j].UniqueEditors {
			return files[i].Path < files[j].Path
		}

		return files[i].UniqueEditors < files[j].UniqueEditors
	})
}

func truncateKnowledgeSiloLabel(path string) string {
	if len(path) > 60 {
		return "..." + path[len(path)-57:]
	}

	return path
}

func newKnowledgeSilosFigure(width, height int) *core.Figure {
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
			rc.YTickLabelFontSize = 12
			rc.LegendFontSize = 9.6
		},
	)
}

func knowledgeDistributionColor(editors int) color.Color {
	switch {
	case editors <= 1:
		return mustHexColor("#F44336")
	case editors <= 2:
		return mustHexColor("#FF9800")
	case editors <= 3:
		return mustHexColor("#FFC107")
	default:
		return mustHexColor("#4CAF50")
	}
}
