package modes

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// temporalWeekdayLabels matches Python labours' WEEKDAY_LABELS ordering, which
// follows Go's time.Weekday (Sunday = 0).
var temporalWeekdayLabels = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

var temporalMonthLabels = []string{
	"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
}

// temporalNanosecondsPerDay mirrors Python's NANOSECONDS_PER_DAY: hercules tick
// sizes are reported in nanoseconds.
const temporalNanosecondsPerDay = int64(24) * 60 * 60 * 1_000_000_000

type temporalDimensionSpec struct {
	Key      string
	Labels   []string
	Title    string
	TickStep int
	Rotate   bool
}

func temporalDimensionSpecs() []temporalDimensionSpec {
	return []temporalDimensionSpec{
		{Key: "weekdays", Labels: temporalWeekdayLabels, Title: "Weekday", TickStep: 1, Rotate: false},
		{Key: "hours", Labels: temporalClockLabels(), Title: "Hour of Day", TickStep: 3, Rotate: true},
		{Key: "months", Labels: temporalMonthLabels, Title: "Month", TickStep: 1, Rotate: true},
		{Key: "weeks", Labels: temporalWeekLabels(), Title: "ISO Week", TickStep: 5, Rotate: true},
	}
}

func temporalClockLabels() []string {
	labels := make([]string, 24)
	for hour := range labels {
		labels[hour] = fmt.Sprintf("%02d:00", hour)
	}

	return labels
}

func temporalWeekLabels() []string {
	labels := make([]string, 53)
	for week := range labels {
		labels[week] = fmt.Sprintf("W%d", week+1)
	}

	return labels
}

// TemporalActivity renders the Python labours temporal-activity file set: eight
// stacked bar charts (weekdays/hours/months/weeks × commits/lines) plus two
// weekday×hour heatmaps (commits/lines). Each is written as a sibling of the
// requested output path using Python's underscore-suffix basenames.
func TemporalActivity(
	reader readers.Reader,
	output string,
	legendThreshold, singleColumnThreshold int,
	startTime, endTime *time.Time,
) error {
	data, totalCommits, totalLines, err := loadTemporalActivity(reader, startTime, endTime)
	if err != nil {
		return err
	}

	legendNote := temporalLegendNote(len(data.Activities), legendThreshold, singleColumnThreshold)
	_, _ = fmt.Fprintf(os.Stdout, "Temporal activity: %d developers, %d commits, %d changed lines%s\n",
		len(data.Activities), totalCommits, totalLines, legendNote)

	return renderTemporalActivity(
		titleRepositoryName(reader.GetName()), data, output, legendThreshold, singleColumnThreshold,
	)
}

func loadTemporalActivity(
	reader readers.Reader,
	startTime, endTime *time.Time,
) (*readers.TemporalActivityData, int, int, error) {
	temporalReader, ok := reader.(readers.TemporalActivityReader)
	if !ok {
		return nil, 0, 0, fmt.Errorf("%w: temporal activity", readers.ErrAnalysisMissing)
	}

	data, err := temporalReader.GetTemporalActivity()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to get temporal activity data: %w", err)
	}

	data = filteredTemporalActivity(data, reader, startTime, endTime)

	totalCommits, totalLines := temporalActivityTotals(data)
	if totalCommits == 0 && totalLines == 0 {
		return nil, 0, 0, errNoTemporalActivityValues
	}

	return data, totalCommits, totalLines, nil
}

func filteredTemporalActivity(
	data *readers.TemporalActivityData,
	reader readers.Reader,
	startTime, endTime *time.Time,
) *readers.TemporalActivityData {
	if startTime == nil && endTime == nil {
		return data
	}

	filtered, ok := filterTemporalActivitiesByDateRange(data, reader, startTime, endTime)
	if ok {
		return filtered
	}

	return data
}

func renderTemporalActivity(
	repoName string,
	data *readers.TemporalActivityData,
	output string,
	legendThreshold, singleColumnThreshold int,
) error {
	for _, mode := range []string{"commits", "lines"} {
		err := renderTemporalActivityMode(
			repoName, data, mode, output, legendThreshold, singleColumnThreshold,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func renderTemporalActivityMode(
	repoName string,
	data *readers.TemporalActivityData,
	mode, output string,
	legendThreshold, singleColumnThreshold int,
) error {
	for _, spec := range temporalDimensionSpecs() {
		dimensionOutput := siblingOutputPath(output, "temporal-activity.png", spec.Key+"_"+mode)

		err := plotTemporalDimension(
			repoName, data, spec, mode, dimensionOutput, legendThreshold, singleColumnThreshold,
		)
		if err != nil {
			return err
		}
	}

	heatmapOutput := siblingOutputPath(output, "temporal-activity.png", "heatmap_"+mode)

	return plotTemporalHeatmap(repoName, data, mode, heatmapOutput)
}

func temporalDimensionValues(activity readers.TemporalDeveloperActivity, dimKey, mode string) []int {
	var dim readers.TemporalDimensionData

	switch dimKey {
	case "weekdays":
		dim = activity.Weekdays
	case "hours":
		dim = activity.Hours
	case "months":
		dim = activity.Months
	case "weeks":
		dim = activity.Weeks
	}

	if mode == "lines" {
		return dim.Lines
	}

	return dim.Commits
}

func temporalActivityTotals(data *readers.TemporalActivityData) (int, int) {
	commits, lines := 0, 0
	for _, activity := range data.Activities {
		commits += sumInts(activity.Hours.Commits)
		lines += sumInts(activity.Hours.Lines)
	}

	return commits, lines
}

func buildTemporalDimensionSeries(
	data *readers.TemporalActivityData,
	dimKey, mode string,
	numBins int,
) []temporalHourCommitSeries {
	developers := sortedIntKeys(data.Activities)

	series := make([]temporalHourCommitSeries, 0, len(developers))
	for _, developer := range developers {
		values := make([]int, numBins)
		for i, value := range temporalDimensionValues(data.Activities[developer], dimKey, mode) {
			if i < numBins {
				values[i] = value
			}
		}

		name := "Unknown"
		if developer >= 0 && developer < len(data.People) {
			name = data.People[developer]
		}

		series = append(series, temporalHourCommitSeries{Name: name, Values: values})
	}

	return series
}

func plotTemporalDimension(
	repoName string,
	data *readers.TemporalActivityData,
	spec temporalDimensionSpec,
	mode, output string,
	legendThreshold, singleColumnThreshold int,
) error {
	_ = singleColumnThreshold

	output, err := resolveReportOutput(output, "temporal-activity_"+spec.Key+"_"+mode+".png")
	if err != nil {
		return err
	}

	numBins := len(spec.Labels)

	series := buildTemporalDimensionSeries(data, spec.Key, mode, numBins)
	if len(series) == 0 {
		return errNoTemporalActivityValues
	}

	plot, err := newTemporalDimensionPlot(repoName, spec, mode)
	if err != nil {
		return err
	}

	maxStack := plotTemporalBars(plot.axes, series, numBins)
	configureTemporalDimensionAxes(plot.axes, spec, mode, maxStack)
	addTemporalLegend(plot.axes, len(series), legendThreshold)

	err = saveReportFigureWithoutTightLayout(plot.figure, output, plot.width, plot.height)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Saved %s\n", output)

	return nil
}

type temporalDimensionPlot struct {
	figure *core.Figure
	axes   *core.Axes
	width  int
	height int
}

func newTemporalDimensionPlot(
	repoName string,
	spec temporalDimensionSpec,
	mode string,
) (temporalDimensionPlot, error) {
	width, height := reportPlotPixels("temporal-activity.png")
	figure := newReportFigure(width, height)

	grid := figure.Subplots(1, 1, core.WithSubplotPadding(0.060, 0.985, 0.110, 0.945))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return temporalDimensionPlot{}, errTemporalActivityAxes
	}

	axes := grid[0][0]
	axes.SetTitle(fmt.Sprintf("%s - Activity by %s (%s)", repoName, spec.Title, mode))
	axes.SetXLabel(spec.Title)
	axes.SetYLabel("Number of " + mode)

	return temporalDimensionPlot{figure: figure, axes: axes, width: width, height: height}, nil
}

func plotTemporalBars(axes *core.Axes, series []temporalHourCommitSeries, numBins int) float64 {
	positions := temporalBarPositions(numBins)
	bottom := make([]float64, numBins)
	barWidth := 0.8
	maxStack := 0.0
	colors := sampledTab20Colors(len(series))

	for index, item := range series {
		values := temporalBarValues(item.Values, numBins)
		barColor := colors[index]
		_, _ = axes.Bar(positions, values, core.BarOptions{
			Color:     optional.Of(barColor),
			Width:     optional.Of(barWidth),
			Baselines: append([]float64(nil), bottom...),
			Label:     item.Name,
		})
		maxStack = addTemporalBarStack(bottom, values, maxStack)
	}

	return maxStack
}

func temporalBarPositions(numBins int) []float64 {
	positions := make([]float64, numBins)
	for index := range positions {
		positions[index] = float64(index)
	}

	return positions
}

func temporalBarValues(counts []int, numBins int) []float64 {
	values := make([]float64, numBins)
	for index := 0; index < len(counts) && index < numBins; index++ {
		values[index] = float64(counts[index])
	}

	return values
}

func addTemporalBarStack(bottom, values []float64, maxStack float64) float64 {
	for index := range bottom {
		bottom[index] += values[index]
		maxStack = math.Max(maxStack, bottom[index])
	}

	return maxStack
}

func configureTemporalDimensionAxes(
	axes *core.Axes,
	spec temporalDimensionSpec,
	mode string,
	maxStack float64,
) {
	ticks, labels := temporalDimensionTicks(spec)
	axes.SetXLim(-0.75, float64(len(spec.Labels))-0.25)
	axes.SetYLim(0, math.Max(maxStack, 1))
	axes.XAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	axes.XAxis.Formatter = ticker.FixedFormatter{Labels: labels}
	configureTemporalTickLabels(axes, spec.Rotate)
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: temporalActivityYTicks(maxStack)}
	axes.SetYLabel("Number of " + mode)
}

func configureTemporalTickLabels(axes *core.Axes, rotate bool) {
	if !rotate {
		return
	}

	axes.XAxis.MajorLabelStyle = core.TickLabelStyle{
		Rotation: 45,
		HAlign:   core.TextAlignRight,
		VAlign:   core.TextVAlignTop,
	}
}

func temporalDimensionTicks(spec temporalDimensionSpec) ([]float64, []string) {
	numBins := len(spec.Labels)
	ticks := make([]float64, 0, numBins)

	labels := make([]string, 0, numBins)
	for bin := 0; bin < numBins; bin += spec.TickStep {
		ticks = append(ticks, float64(bin))
		labels = append(labels, spec.Labels[bin])
	}

	return ticks, labels
}

func addTemporalLegend(ax *core.Axes, seriesCount, legendThreshold int) {
	if seriesCount <= 1 || (legendThreshold > 0 && seriesCount >= legendThreshold) {
		return
	}

	legend := ax.AddLegend()
	legend.Location = core.LegendUpperRight
	legend.FontSize = 9.6
	legend.BackgroundColor = render.Color{R: 1, G: 1, B: 1, A: 1}
	legend.BorderColor = render.Color{R: 1, G: 1, B: 1, A: 0}
	legend.TextColor = render.Color{R: 0, G: 0, B: 0, A: 1}
}

// temporalWeekdayHourMatrix reconstructs a weekday×hour activity matrix from the
// per-developer marginal distributions, mirroring Python labours' independence
// (outer-product) approximation since hercules only emits the marginals.
func temporalWeekdayHourMatrix(data *readers.TemporalActivityData, mode string) [][]float64 {
	matrix := make([][]float64, 7)
	for i := range matrix {
		matrix[i] = make([]float64, 24)
	}

	for _, activity := range data.Activities {
		weekday := temporalDimensionValues(activity, "weekdays", mode)
		hour := temporalDimensionValues(activity, "hours", mode)
		totalWeekday := sumInts(weekday)

		totalHour := sumInts(hour)
		if totalWeekday == 0 || totalHour == 0 {
			continue
		}

		for wi := 0; wi < 7 && wi < len(weekday); wi++ {
			if weekday[wi] == 0 {
				continue
			}

			weekdayProb := float64(weekday[wi]) / float64(totalWeekday)

			for hi := 0; hi < 24 && hi < len(hour); hi++ {
				hourProb := float64(hour[hi]) / float64(totalHour)
				matrix[wi][hi] += math.Trunc(weekdayProb * hourProb * float64(totalWeekday))
			}
		}
	}

	return matrix
}

func plotTemporalHeatmap(repoName string, data *readers.TemporalActivityData, mode, output string) error {
	matrix := temporalWeekdayHourMatrix(data, mode)
	total := 0.0

	for _, row := range matrix {
		for _, value := range row {
			total += value
		}
	}

	if total == 0 {
		fmt.Printf("No data for weekday×hour heatmap (%s)\n", mode)
		return nil
	}

	output, err := resolveReportOutput(output, "temporal-activity_heatmap_"+mode+".png")
	if err != nil {
		return err
	}

	colLabels := make([]string, 24)
	for hour := range colLabels {
		colLabels[hour] = fmt.Sprintf("%02d", hour)
	}

	rowLabels := append([]string(nil), temporalWeekdayLabels...)

	// Python's heatmap sets figsize=(19.2,8) at creation but apply_plot_style
	// then overrides it back to args.size or "16,10", so the effective size
	// matches the temporal-activity bar charts (1600×1000).
	heatmapWidth, heatmapHeight := reportPlotInches("temporal-activity.png")

	err = graphics.PlotHeatmapMatplotlib(matrix, rowLabels, colLabels, graphics.MatplotlibHeatmapOptions{
		Title:        fmt.Sprintf("%s - Activity Heatmap: Weekday × Hour (%s)", repoName, mode),
		Output:       output,
		Colormap:     "YlOrRd",
		WidthInches:  heatmapWidth,
		HeightInches: heatmapHeight,
	})
	if err != nil {
		return fmt.Errorf("failed to plot temporal heatmap: %w", err)
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

// filterTemporalActivitiesByDateRange rebuilds per-developer dimension data from
// the per-tick records, restricted to the requested window. Mirrors Python
// labours' _filter_activities_by_date_range.
func filterTemporalActivitiesByDateRange(
	data *readers.TemporalActivityData,
	reader readers.Reader,
	startTime, endTime *time.Time,
) (*readers.TemporalActivityData, bool) {
	filterRange, ok := temporalActivityFilterRange(data, reader, startTime, endTime)
	if !ok {
		return nil, false
	}

	startTick, endTick := temporalFilterTicks(filterRange)
	activities := temporalActivitiesInTickRange(data.Ticks, startTick, endTick)
	_, _ = fmt.Fprintf(
		os.Stdout,
		"Filtering temporal activity to %s - %s\n",
		filterRange.start.Format("2006-01-02"),
		filterRange.end.Format("2006-01-02"),
	)

	return &readers.TemporalActivityData{
		Activities: activities,
		People:     data.People,
		Ticks:      data.Ticks,
		TickSize:   data.TickSize,
	}, true
}

type temporalFilterRange struct {
	// axis dates the ticks. It is anchored on the floored tick-0 boundary, not
	// on repositoryStart - see tickAxis.
	axis            tickAxis
	repositoryStart time.Time
	start           time.Time
	end             time.Time
}

func temporalActivityFilterRange(
	data *readers.TemporalActivityData,
	reader readers.Reader,
	startTime, endTime *time.Time,
) (temporalFilterRange, bool) {
	headerStart, headerEnd := reader.GetHeader()

	axis, dated := newTickAxis(headerStart, data.TickSize)
	if !dated || len(data.Ticks) == 0 {
		return temporalFilterRange{}, false
	}

	// The repository bounds stay on the raw header times: they decide whether
	// the user asked for a narrower window than the data covers, which is a
	// question about wall-clock instants rather than about tick boundaries.
	repositoryStart := time.Unix(headerStart, 0)
	repositoryEnd := time.Unix(headerEnd, 0)

	filterRange := temporalFilterRange{
		axis:            axis,
		repositoryStart: repositoryStart,
		start:           temporalFilterBoundary(startTime, repositoryStart),
		end:             temporalFilterBoundary(endTime, repositoryEnd),
	}
	if !filterRange.start.After(repositoryStart) && !filterRange.end.Before(repositoryEnd) {
		return temporalFilterRange{}, false
	}

	return filterRange, true
}

func temporalFilterBoundary(boundary *time.Time, fallback time.Time) time.Time {
	if boundary != nil {
		return *boundary
	}

	return fallback
}

// temporalFilterTicks converts the requested window into an inclusive tick
// range. It used to divide by the repository's raw begin time, which is the
// first commit's timestamp rather than the boundary tick 0 starts at, so a
// boundary could land one tick off.
func temporalFilterTicks(filterRange temporalFilterRange) (int, int) {
	return filterRange.axis.tickOf(filterRange.start), filterRange.axis.tickOf(filterRange.end)
}

func temporalActivitiesInTickRange(
	ticks map[int]map[int]readers.TemporalActivityTick,
	startTick, endTick int,
) map[int]readers.TemporalDeveloperActivity {
	activities := make(map[int]readers.TemporalDeveloperActivity)

	for tickID, tickDevelopers := range ticks {
		if tickID < startTick || tickID > endTick {
			continue
		}

		addTemporalTickActivities(activities, tickDevelopers)
	}

	return activities
}

func addTemporalTickActivities(
	activities map[int]readers.TemporalDeveloperActivity,
	tickDevelopers map[int]readers.TemporalActivityTick,
) {
	for developerID, tick := range tickDevelopers {
		activity := activities[developerID]
		ensureTemporalDimensionCapacity(&activity)
		addTemporalTick(&activity, tick)
		activities[developerID] = activity
	}
}

func ensureTemporalDimensionCapacity(activity *readers.TemporalDeveloperActivity) {
	if len(activity.Weekdays.Commits) == 0 {
		activity.Weekdays = readers.TemporalDimensionData{Commits: make([]int, 7), Lines: make([]int, 7)}
		activity.Hours = readers.TemporalDimensionData{Commits: make([]int, 24), Lines: make([]int, 24)}
		activity.Months = readers.TemporalDimensionData{Commits: make([]int, 12), Lines: make([]int, 12)}
		activity.Weeks = readers.TemporalDimensionData{Commits: make([]int, 53), Lines: make([]int, 53)}
	}
}

func addTemporalTick(activity *readers.TemporalDeveloperActivity, tick readers.TemporalActivityTick) {
	addTemporalBin(activity.Weekdays.Commits, activity.Weekdays.Lines, tick.Weekday, tick.Commits, tick.Lines)
	addTemporalBin(activity.Hours.Commits, activity.Hours.Lines, tick.Hour, tick.Commits, tick.Lines)
	addTemporalBin(activity.Months.Commits, activity.Months.Lines, tick.Month, tick.Commits, tick.Lines)
	addTemporalBin(activity.Weeks.Commits, activity.Weeks.Lines, tick.Week, tick.Commits, tick.Lines)
}

func addTemporalBin(commits, lines []int, index, commitDelta, lineDelta int) {
	if index < 0 || index >= len(commits) {
		return
	}

	commits[index] += commitDelta
	lines[index] += lineDelta
}

type temporalHourCommitSeries struct {
	Name   string
	Values []int
}

func buildTemporalHourCommitSeries(data *readers.TemporalActivityData) []temporalHourCommitSeries {
	if data == nil || len(data.Activities) == 0 {
		return nil
	}

	developers := sortedIntKeys(data.Activities)

	series := make([]temporalHourCommitSeries, 0, len(developers))
	for _, developer := range developers {
		activity := data.Activities[developer]

		values := make([]int, 24)
		for hour, commits := range activity.Hours.Commits {
			if hour >= 0 && hour < len(values) {
				values[hour] = commits
			}
		}

		name := "Unknown"
		if developer >= 0 && developer < len(data.People) {
			name = data.People[developer]
		}

		series = append(series, temporalHourCommitSeries{
			Name:   name,
			Values: values,
		})
	}

	return series
}

func temporalActivityYTicks(maxValue float64) []float64 {
	if maxValue <= 0 {
		return []float64{0, 1}
	}

	step := temporalActivityTickStep(maxValue)

	ticks := make([]float64, 0, int(math.Ceil(maxValue/step))+1)
	for tick := 0.0; tick <= maxValue; tick += step {
		ticks = append(ticks, tick)
	}

	return ticks
}

// temporalActivityTickStep picks a round step that keeps the axis to roughly ten
// labels at any magnitude. The steps used to be capped at 20, which was fine for
// a single repository but drew ~110 overlapping labels once a combined run
// reached a couple of thousand commits - a solid black bar where the axis should
// be.
func temporalActivityTickStep(maxValue float64) float64 {
	const targetTicks = 10

	magnitude := math.Pow(10, math.Floor(math.Log10(maxValue/targetTicks)))
	for _, multiple := range []float64{1, 2, 5, 10} {
		// Commits and lines are counts, so never label a fraction of one.
		step := math.Max(multiple*magnitude, 1)
		if maxValue/step <= targetTicks {
			return step
		}
	}

	return math.Max(10*magnitude, 1)
}

func temporalLegendNote(developers, legendThreshold, singleColumnThreshold int) string {
	if legendThreshold > 0 && developers > legendThreshold {
		return fmt.Sprintf(" (legend suppressed above %d developers)", legendThreshold)
	}

	if singleColumnThreshold > 0 && developers <= singleColumnThreshold {
		return " (single-column legend eligible)"
	}

	return ""
}
