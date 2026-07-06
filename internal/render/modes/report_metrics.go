package modes

import (
	"bytes"
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

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/cwbudde/matplotlib-go/backends"
	_ "github.com/cwbudde/matplotlib-go/backends/agg"
	_ "github.com/cwbudde/matplotlib-go/backends/svg"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
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
func TemporalActivity(reader readers.Reader, output string, legendThreshold, singleColumnThreshold int, startTime, endTime *time.Time) error {
	temporalReader, ok := reader.(readers.TemporalActivityReader)
	if !ok {
		return fmt.Errorf("reader does not expose temporal activity data")
	}
	data, err := temporalReader.GetTemporalActivity()
	if err != nil {
		return fmt.Errorf("failed to get temporal activity data: %v", err)
	}

	if startTime != nil || endTime != nil {
		if filtered, ok := filterTemporalActivitiesByDateRange(data, reader, startTime, endTime); ok {
			data = filtered
		}
	}

	totalCommits, totalLines := temporalActivityTotals(data)
	if totalCommits == 0 && totalLines == 0 {
		return fmt.Errorf("no temporal activity values found")
	}

	legendNote := temporalLegendNote(len(data.Activities), legendThreshold, singleColumnThreshold)
	fmt.Printf("Temporal activity: %d developers, %d commits, %d changed lines%s\n",
		len(data.Activities), totalCommits, totalLines, legendNote)

	repoName := reader.GetName()
	for _, mode := range []string{"commits", "lines"} {
		for _, spec := range temporalDimensionSpecs() {
			out := siblingOutputPath(output, "temporal-activity.png", spec.Key+"_"+mode)
			if err := plotTemporalDimension(repoName, data, spec, mode, out, legendThreshold, singleColumnThreshold); err != nil {
				return err
			}
		}
		out := siblingOutputPath(output, "temporal-activity.png", "heatmap_"+mode)
		if err := plotTemporalHeatmap(repoName, data, mode, out); err != nil {
			return err
		}
	}
	return nil
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

func buildTemporalDimensionSeries(data *readers.TemporalActivityData, dimKey, mode string, numBins int) []temporalHourCommitSeries {
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

func plotTemporalDimension(repoName string, data *readers.TemporalActivityData, spec temporalDimensionSpec, mode, output string, legendThreshold, singleColumnThreshold int) error {
	_ = singleColumnThreshold

	output, err := resolveReportOutput(output, "temporal-activity_"+spec.Key+"_"+mode+".png")
	if err != nil {
		return err
	}

	numBins := len(spec.Labels)
	series := buildTemporalDimensionSeries(data, spec.Key, mode, numBins)
	if len(series) == 0 {
		return fmt.Errorf("no temporal activity values found")
	}

	width, height := reportPlotPixels("temporal-activity.png")
	fig := newReportFigure(width, height)
	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.060, 0.985, 0.110, 0.945))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create temporal activity axes")
	}
	ax := grid[0][0]
	ax.SetTitle(fmt.Sprintf("%s - Activity by %s (%s)", repoName, spec.Title, mode))
	ax.SetXLabel(spec.Title)
	ax.SetYLabel(fmt.Sprintf("Number of %s", mode))

	x := make([]float64, numBins)
	bottom := make([]float64, numBins)
	for i := range x {
		x[i] = float64(i)
	}

	barWidth := 0.8
	maxStack := 0.0
	colors := sampledTab20Colors(len(series))
	for i, item := range series {
		values := make([]float64, numBins)
		for bin, count := range item.Values {
			if bin >= numBins {
				break
			}
			values[bin] = float64(count)
		}
		barColor := colors[i]
		ax.Bar(x, values, core.BarOptions{
			Color:     &barColor,
			Width:     &barWidth,
			Baselines: append([]float64(nil), bottom...),
			Label:     item.Name,
		})
		for bin := range bottom {
			bottom[bin] += values[bin]
			if bottom[bin] > maxStack {
				maxStack = bottom[bin]
			}
		}
	}

	ticks, labels := temporalDimensionTicks(spec)
	ax.SetXLim(-0.75, float64(numBins)-0.25)
	ax.SetYLim(0, math.Max(maxStack, 1))
	ax.XAxis.Locator = core.FixedLocator{TicksList: ticks}
	ax.XAxis.Formatter = core.FixedFormatter{Labels: labels}
	if spec.Rotate {
		ax.XAxis.MajorLabelStyle = core.TickLabelStyle{
			Rotation: 45,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
		}
	}
	ax.YAxis.Locator = core.FixedLocator{TicksList: temporalActivityYTicks(maxStack)}

	addTemporalLegend(ax, len(series), legendThreshold)

	if err := saveReportFigureWithoutTightLayout(fig, output, width, height); err != nil {
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
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
	if err := graphics.PlotHeatmapMatplotlib(matrix, rowLabels, colLabels, graphics.MatplotlibHeatmapOptions{
		Title:        fmt.Sprintf("%s - Activity Heatmap: Weekday × Hour (%s)", repoName, mode),
		Output:       output,
		Colormap:     "YlOrRd",
		WidthInches:  heatmapWidth,
		HeightInches: heatmapHeight,
	}); err != nil {
		return fmt.Errorf("failed to plot temporal heatmap: %v", err)
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

// filterTemporalActivitiesByDateRange rebuilds per-developer dimension data from
// the per-tick records, restricted to the requested window. Mirrors Python
// labours' _filter_activities_by_date_range.
func filterTemporalActivitiesByDateRange(data *readers.TemporalActivityData, reader readers.Reader, startTime, endTime *time.Time) (*readers.TemporalActivityData, bool) {
	headerStart, headerEnd := reader.GetHeader()
	if headerStart == 0 || data.TickSize <= 0 || len(data.Ticks) == 0 {
		return nil, false
	}

	repoStart := time.Unix(headerStart, 0)
	repoEnd := time.Unix(headerEnd, 0)
	filterStart := repoStart
	filterEnd := repoEnd
	if startTime != nil {
		filterStart = *startTime
	}
	if endTime != nil {
		filterEnd = *endTime
	}
	if !filterStart.After(repoStart) && !filterEnd.Before(repoEnd) {
		return nil, false
	}

	tickDays := float64(data.TickSize) / float64(temporalNanosecondsPerDay)
	if tickDays <= 0 {
		tickDays = 1
	}
	startTick := int(filterStart.Sub(repoStart).Hours() / 24 / tickDays)
	endTick := int(filterEnd.Sub(repoStart).Hours() / 24 / tickDays)

	activities := make(map[int]readers.TemporalDeveloperActivity)
	for tickID, tickDevs := range data.Ticks {
		if tickID < startTick || tickID > endTick {
			continue
		}
		for devID, tick := range tickDevs {
			activity := activities[devID]
			ensureTemporalDimensionCapacity(&activity)
			addTemporalTick(&activity, tick)
			activities[devID] = activity
		}
	}

	fmt.Printf("Filtering temporal activity to %s - %s\n",
		filterStart.Format("2006-01-02"), filterEnd.Format("2006-01-02"))

	return &readers.TemporalActivityData{
		Activities: activities,
		People:     data.People,
		Ticks:      data.Ticks,
		TickSize:   data.TickSize,
	}, true
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

func BusFactor(reader readers.Reader, output string) error {
	busFactorReader, ok := reader.(readers.BusFactorReader)
	if !ok {
		return fmt.Errorf("reader does not expose bus factor data")
	}
	data, err := busFactorReader.GetBusFactor()
	if err != nil {
		return fmt.Errorf("failed to get bus factor data: %v", err)
	}
	if len(data.Snapshots) == 0 {
		return fmt.Errorf("no bus factor snapshots found")
	}

	ticks := sortedIntKeys(data.Snapshots)
	series := make(xySeries, len(ticks))
	for i, tick := range ticks {
		series[i].X = float64(tick)
		series[i].Y = float64(data.Snapshots[tick].BusFactor)
	}

	latest := data.Snapshots[ticks[len(ticks)-1]]
	fmt.Printf("Bus factor: latest=%d, total lines=%d, threshold=%.2f\n",
		latest.BusFactor, latest.TotalLines, data.Threshold)

	timelineOutput := siblingOutputPath(output, "bus-factor.png", "timeline")
	if err := plotLineSeries(
		"Bus Factor Over Time",
		"Tick",
		"Bus Factor",
		[]namedSeries{{Name: "Bus factor", Points: series}},
		timelineOutput,
		"bus-factor-timeline.png",
	); err != nil {
		return err
	}

	if err := plotBusFactorGauge(
		reader.GetName(),
		latest.BusFactor,
		latest.TotalLines,
		latest.AuthorLines,
		data.People,
		float64(data.Threshold),
		siblingOutputPath(output, "bus-factor.png", "gauge"),
	); err != nil {
		return fmt.Errorf("failed to plot bus factor gauge: %v", err)
	}

	if len(data.SubsystemBusFactor) > 0 {
		labels, values := busFactorSubsystemPairs(data.SubsystemBusFactor, 0)
		if err := plotBusFactorSubsystemsMatplotlib(
			reader.GetName(),
			labels,
			values,
			float64(data.Threshold),
			siblingOutputPath(output, "bus-factor.png", "subsystems"),
		); err != nil {
			return fmt.Errorf("failed to plot subsystem bus factor: %v", err)
		}
		fmt.Printf("Bus factor subsystem summary: %d subsystems\n", len(data.SubsystemBusFactor))
	}
	return nil
}

// busFactorStatus maps a bus-factor value to Python labours' color + status label.
func busFactorStatus(busFactor int) (color.RGBA, string) {
	switch {
	case busFactor <= 1:
		return color.RGBA{R: 244, G: 67, B: 54, A: 255}, "CRITICAL"
	case busFactor <= 3:
		return color.RGBA{R: 255, G: 152, B: 0, A: 255}, "LOW"
	case busFactor <= 5:
		return color.RGBA{R: 255, G: 193, B: 7, A: 255}, "MODERATE"
	default:
		return color.RGBA{R: 76, G: 175, B: 80, A: 255}, "HEALTHY"
	}
}

// busFactorTopOwners returns the top maxSlices authors by line ownership plus an
// aggregated "Others" slice, mirroring Python labours' gauge pie.
func busFactorTopOwners(authorLines map[int]int64, people []string, maxSlices int) ([]string, []float64) {
	type owner struct {
		ID    int
		Lines int64
	}
	owners := make([]owner, 0, len(authorLines))
	for id, lines := range authorLines {
		owners = append(owners, owner{ID: id, Lines: lines})
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].Lines != owners[j].Lines {
			return owners[i].Lines > owners[j].Lines
		}
		return owners[i].ID < owners[j].ID
	})

	labels := make([]string, 0, maxSlices+1)
	values := make([]float64, 0, maxSlices+1)
	var others int64
	for i, o := range owners {
		if i < maxSlices {
			name := fmt.Sprintf("Author %d", o.ID)
			if o.ID >= 0 && o.ID < len(people) {
				name = people[o.ID]
			}
			labels = append(labels, name)
			values = append(values, float64(o.Lines))
		} else {
			others += o.Lines
		}
	}
	if others > 0 {
		labels = append(labels, "Others")
		values = append(values, float64(others))
	}
	return labels, values
}

// humanizeInt formats an integer with thousands separators (e.g. 12345 -> "12,345").
func humanizeInt(value int64) string {
	s := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	n := len(s)
	if n <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	lead := n % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

func plotBusFactorGauge(repoName string, busFactor int, totalLines int64, authorLines map[int]int64, people []string, threshold float64, output string) error {
	output, err := resolveReportOutput(output, "bus-factor-gauge.png")
	if err != nil {
		return err
	}

	width, height := 1000, 600
	fig := newReportFigure(width, height)
	if repoName != "" {
		fig.SetSuptitle(fmt.Sprintf("%s - Bus Factor Summary", repoName))
	}
	gs := fig.GridSpec(
		1, 2,
		core.WithGridSpecPadding(0.03, 0.97, 0.05, 0.90),
		core.WithGridSpecSpacing(0.2, 0.1),
	)
	if gs == nil {
		return fmt.Errorf("failed to create bus factor gauge axes")
	}
	axGauge := gs.Cell(0, 0).AddAxes()
	axPie := gs.Cell(0, 1).AddAxes()
	if axGauge == nil || axPie == nil {
		return fmt.Errorf("failed to create bus factor gauge axes")
	}

	hideAxesContent(axGauge)
	axGauge.SetXLim(0, 1)
	axGauge.SetYLim(0, 1)

	statusColor, statusLabel := busFactorStatus(busFactor)
	statusRenderColor := renderColor(statusColor)
	gray := render.Color{R: 0.5, G: 0.5, B: 0.5, A: 1}
	axesCoords := core.Coords(core.CoordAxes)
	axGauge.Text(0.5, 0.6, strconv.Itoa(busFactor), core.TextOptions{
		Coords:   axesCoords,
		FontSize: 72,
		Color:    statusRenderColor,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})
	axGauge.Text(0.5, 0.35, statusLabel, core.TextOptions{
		Coords:   axesCoords,
		FontSize: 18,
		Color:    statusRenderColor,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})
	axGauge.Text(0.5, 0.2, fmt.Sprintf("Bus Factor @ %.0f%%", threshold*100), core.TextOptions{
		Coords:   axesCoords,
		FontSize: 12,
		Color:    gray,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})
	axGauge.Text(0.5, 0.1, fmt.Sprintf("%s total lines", humanizeInt(totalLines)), core.TextOptions{
		Coords:   axesCoords,
		FontSize: 10,
		Color:    gray,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})

	if len(authorLines) > 0 && totalLines > 0 {
		labels, values := busFactorTopOwners(authorLines, people, 8)
		axPie.Pie(values, core.PieOptions{
			Labels:     labels,
			AutoPct:    "%.1f%%",
			Colors:     sampledTab20Colors(len(values)),
			StartAngle: 90,
		})
		axPie.SetTitle("Line Ownership")
	} else {
		hideAxesContent(axPie)
		axPie.SetXLim(0, 1)
		axPie.SetYLim(0, 1)
		axPie.Text(0.5, 0.5, "No ownership data", core.TextOptions{
			Coords:   axesCoords,
			FontSize: 14,
			HAlign:   core.TextAlignCenter,
			VAlign:   core.TextVAlignMiddle,
		})
	}

	if err := saveReportFigure(fig, output, width, height); err != nil {
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

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

func OwnershipConcentration(reader readers.Reader, output string) error {
	ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
	if !ok {
		return fmt.Errorf("reader does not expose ownership concentration data")
	}
	data, err := ownershipReader.GetOwnershipConcentration()
	if err != nil {
		return fmt.Errorf("failed to get ownership concentration data: %v", err)
	}
	if len(data.Snapshots) == 0 {
		return fmt.Errorf("no ownership concentration snapshots found")
	}

	ticks := sortedIntKeys(data.Snapshots)
	gini := make(xySeries, len(ticks))
	hhi := make(xySeries, len(ticks))
	for i, tick := range ticks {
		snapshot := data.Snapshots[tick]
		gini[i].X = float64(tick)
		gini[i].Y = snapshot.Gini
		hhi[i].X = float64(tick)
		hhi[i].Y = snapshot.HHI
	}

	latest := data.Snapshots[ticks[len(ticks)-1]]
	fmt.Printf("Ownership concentration: latest gini=%.3f, hhi=%.3f, total lines=%d\n",
		latest.Gini, latest.HHI, latest.TotalLines)

	timelineOutput := siblingOutputPath(output, "ownership-concentration.png", "timeline")
	if err := plotLineSeries(
		"Ownership Concentration Over Time",
		"Tick",
		"Concentration",
		[]namedSeries{
			{Name: "Gini", Points: gini},
			{Name: "HHI", Points: hhi},
		},
		timelineOutput,
		"ownership-concentration-timeline.png",
	); err != nil {
		return err
	}

	if len(data.SubsystemGini) > 0 {
		subsystemOutput := siblingOutputPath(output, "ownership-concentration.png", "subsystems")
		if err := plotOwnershipSubsystemsBar(reader.GetName(), data.SubsystemGini, data.SubsystemHHI, subsystemOutput); err != nil {
			return fmt.Errorf("failed to plot subsystem ownership concentration: %v", err)
		}
		fmt.Printf("Ownership concentration subsystem summary: %d subsystems\n", len(data.SubsystemGini))
	}
	return nil
}

// plotOwnershipSubsystemsBar mirrors Python labours' ownership_concentration
// _plot_subsystems: a grouped *horizontal* bar chart with two series (Gini and
// HHI) per subsystem, subsystems sorted alphabetically (not by value).
func plotOwnershipSubsystemsBar(repoName string, giniByDir, hhiByDir map[string]float64, output string) error {
	output, err := resolveReportOutput(output, "ownership-concentration-subsystems.png")
	if err != nil {
		return err
	}

	dirs := make([]string, 0, len(giniByDir))
	for dir := range giniByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs) // Python: sorted(subsystem_gini.keys())
	n := len(dirs)

	heightInches := math.Max(4, float64(n)*0.5+2)
	width, height := graphics.InchesToPixels(12), graphics.InchesToPixels(heightInches)
	fig := newReportFigure(width, height)
	grid := fig.Subplots(1, 1)
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create ownership subsystem axes")
	}
	ax := grid[0][0]

	title := "Ownership Concentration by Subsystem"
	if repoName != "" {
		title = fmt.Sprintf("%s - %s", repoName, title)
	}
	ax.SetTitle(title)
	ax.SetXLabel("Concentration Index")

	const barHeight = 0.35
	yGini := make([]float64, n)
	yHHI := make([]float64, n)
	giniVals := make([]float64, n)
	hhiVals := make([]float64, n)
	ticks := make([]float64, n)
	for i, dir := range dirs {
		ticks[i] = float64(i)
		yGini[i] = float64(i) - barHeight/2
		yHHI[i] = float64(i) + barHeight/2
		giniVals[i] = giniByDir[dir]
		hhiVals[i] = hhiByDir[dir] // missing subsystem → 0, matching dict.get(d, 0)
	}

	orientation := core.BarHorizontal
	bh := barHeight
	// Matches Python's alpha=0.8. matplotlib-go's AGG backend lightens semi-
	// transparent fills on the transparent report surface (PLAN Stage D5), so
	// these read a bit pale until that renderer bug is fixed; alpha still beats
	// opaque on RMSE here because bar position, not fill, dominates the diff.
	giniColor := render.Color{R: 233.0 / 255, G: 30.0 / 255, B: 99.0 / 255, A: 0.8} // #E91E63
	hhiColor := render.Color{R: 63.0 / 255, G: 81.0 / 255, B: 181.0 / 255, A: 0.8}  // #3F51B5
	ax.Bar(yGini, giniVals, core.BarOptions{Color: &giniColor, Width: &bh, Orientation: &orientation, Label: "Gini"})
	ax.Bar(yHHI, hhiVals, core.BarOptions{Color: &hhiColor, Width: &bh, Orientation: &orientation, Label: "HHI"})

	clipOff := false
	labelColor := render.Color{R: 0, G: 0, B: 0, A: 1}
	for i := range dirs {
		ax.Text(giniVals[i]+0.02, yGini[i], fmt.Sprintf("%.2f", giniVals[i]), core.TextOptions{
			FontSize: 8.4, Color: labelColor, VAlign: core.TextVAlignMiddle, ClipOn: &clipOff,
		})
		ax.Text(hhiVals[i]+0.02, yHHI[i], fmt.Sprintf("%.2f", hhiVals[i]), core.TextOptions{
			FontSize: 8.4, Color: labelColor, VAlign: core.TextVAlignMiddle, ClipOn: &clipOff,
		})
	}

	ax.SetXLim(0, 1.1)
	// Match matplotlib's default y-autoscale (5% margin) for the grouped bars.
	dataMin := -barHeight
	dataMax := float64(n-1) + barHeight
	margin := 0.05 * (dataMax - dataMin)
	ax.SetYLim(dataMin-margin, dataMax+margin)
	ax.YAxis.Locator = core.FixedLocator{TicksList: ticks}
	ax.YAxis.Formatter = core.FixedFormatter{Labels: append([]string(nil), dirs...)}
	yLabelStyle := ax.YAxis.MajorLabelStyle
	yLabelStyle.FontSize = 9.6 // Python: fontsize=font_size*0.8
	ax.YAxis.MajorLabelStyle = yLabelStyle

	legend := ax.AddLegend() // default LegendBest mirrors Python's ax.legend()
	legend.FontSize = 9.6

	return saveReportFigure(fig, output, width, height) // TightLayout ~ Python tight_layout
}

func KnowledgeDiffusion(reader readers.Reader, output string, detail bool) error {
	diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
	if !ok {
		return fmt.Errorf("reader does not expose knowledge diffusion data")
	}
	data, err := diffusionReader.GetKnowledgeDiffusion()
	if err != nil {
		return fmt.Errorf("failed to get knowledge diffusion data: %v", err)
	}
	if len(data.Distribution) == 0 && len(data.Files) == 0 {
		return fmt.Errorf("no knowledge diffusion data found")
	}

	labels, values := knowledgeDistribution(data)
	fmt.Printf("Knowledge diffusion: %d files, %d developers, window=%d months\n",
		len(data.Files), len(data.People), data.WindowMonths)
	distributionOutput := siblingOutputPath(output, "knowledge-diffusion.png", "distribution")
	if err := plotKnowledgeDistribution(reader.GetName(), labels, values, distributionOutput); err != nil {
		return err
	}

	if err := plotKnowledgeSilos(reader.GetName(), data, siblingOutputPath(output, "knowledge-diffusion.png", "silos")); err != nil {
		return err
	}
	if err := plotKnowledgeLorenz(reader.GetName(), data, siblingOutputPath(output, "knowledge-diffusion.png", "lorenz")); err != nil {
		return err
	}
	if detail {
		if err := plotKnowledgeTrend(data, siblingOutputPath(output, "knowledge-diffusion.png", "trend")); err != nil {
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

func HotspotRisk(reader readers.Reader, output string) error {
	hotspotReader, ok := reader.(readers.HotspotRiskReader)
	if !ok {
		return fmt.Errorf("reader does not expose hotspot risk data")
	}
	data, err := hotspotReader.GetHotspotRisk()
	if err != nil {
		return fmt.Errorf("failed to get hotspot risk data: %v", err)
	}
	if len(data.Files) == 0 {
		return fmt.Errorf("no hotspot risk files found")
	}

	files := append([]readers.HotspotRiskFile(nil), data.Files...)
	sort.Slice(files, func(i, j int) bool {
		return files[i].RiskScore > files[j].RiskScore
	})
	if len(files) > 20 {
		files = files[:20]
	}

	labels := make([]string, len(files))
	values := make(floatSeries, len(files))
	for i, file := range files {
		labels[i] = compactPathLabel(file.Path)
		values[i] = file.RiskScore
	}

	fmt.Printf("Hotspot risk: %d files, window=%d days, top risk=%.3f (%s)\n",
		len(data.Files), data.WindowDays, files[0].RiskScore, files[0].Path)
	if err := plotHotspotRiskRanked(reader.GetName(), files, labels, values, output); err != nil {
		return err
	}
	if err := writeHotspotRiskTable(files, siblingOutputPath(output, "hotspot-risk.png", "table.tsv")); err != nil {
		return err
	}
	printHotspotRiskTable(files, 10)
	return nil
}

func plotKnowledgeDistribution(repoName string, labels []string, values []int, output string) error {
	output, err := resolveReportOutput(output, "knowledge-diffusion.png")
	if err != nil {
		return err
	}
	width, height := reportPlotPixels("knowledge-diffusion.png")
	fig := newReportFigure(width, height)
	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.064, 0.989, 0.100, 0.936))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create knowledge diffusion axes")
	}
	ax := grid[0][0]
	title := "Knowledge Diffusion Distribution"
	if repoName != "" {
		title = fmt.Sprintf("%s - Knowledge Diffusion Distribution", repoName)
	}
	ax.SetTitle(title)
	ax.SetXLabel("Number of Unique Editors")
	ax.SetYLabel("Number of Files")

	y := make([]float64, len(values))
	ticks := make([]float64, len(labels))
	editorCounts := make([]float64, len(labels))
	maxValue := 0.0
	totalFiles := 0
	singleEditorFiles := 0
	for i, value := range values {
		editorCount := float64(i)
		if _, err := fmt.Sscanf(labels[i], "%f", &editorCount); err != nil {
			editorCount = float64(i)
		}
		editorCounts[i] = editorCount
		ticks[i] = editorCount
		y[i] = float64(value)
		totalFiles += value
		if int(editorCount) == 1 {
			singleEditorFiles += value
		}
		if y[i] > maxValue {
			maxValue = y[i]
		}
		c := renderColor(knowledgeDistributionColor(int(editorCount)))
		edgeColor := render.Color{R: 1, G: 1, B: 1, A: 1}
		edgeWidth := 0.5
		ax.Bar([]float64{editorCount}, []float64{y[i]}, core.BarOptions{
			Color:     &c,
			EdgeColor: &edgeColor,
			EdgeWidth: &edgeWidth,
		})
		ax.Text(editorCount, y[i]+0.3, fmt.Sprintf("%d", value), core.TextOptions{
			FontSize: 9.6,
			HAlign:   core.TextAlignCenter,
			VAlign:   core.TextVAlignBottom,
		})
	}
	if totalFiles > 0 {
		pct := float64(singleEditorFiles) / float64(totalFiles) * 100
		riskColor := renderColor(color.RGBA{R: 244, G: 67, B: 54, A: 255})
		boxColor := render.Color{R: 1, G: 1, B: 1, A: 0.8}
		ax.Text(0.98, 0.95, fmt.Sprintf("Single-editor files: %d (%.0f%%)", singleEditorFiles, pct), core.TextOptions{
			Coords:   core.Coords(core.CoordAxes),
			FontSize: 10.8,
			Color:    riskColor,
			HAlign:   core.TextAlignRight,
			VAlign:   core.TextVAlignTop,
			BBox: &core.TextBBoxOptions{
				FaceColor: boxColor,
				EdgeColor: boxColor,
				Padding:   3,
			},
		})
	}
	minX, maxX := rangeWithPadding(editorCounts, 0.5)
	ax.SetXLim(minX, maxX)
	yMax := math.Ceil(math.Max(maxValue, 1)/15) * 15
	ax.SetYLim(0, yMax)
	xTicks := make([]float64, 0, int(maxX-minX)+2)
	xLabels := make([]string, 0, cap(xTicks))
	for tick := math.Ceil(minX); tick <= math.Floor(maxX); tick++ {
		xTicks = append(xTicks, tick)
		xLabels = append(xLabels, fmt.Sprintf("%.0f", tick))
	}
	yTicks := make([]float64, 0, int(yMax/15)+1)
	for tick := 0.0; tick <= yMax; tick += 15 {
		yTicks = append(yTicks, tick)
	}
	ax.XAxis.Locator = core.FixedLocator{TicksList: xTicks}
	ax.XAxis.Formatter = core.FixedFormatter{Labels: xLabels}
	ax.YAxis.Locator = core.FixedLocator{TicksList: yTicks}

	if err := saveReportFigureWithoutTightLayout(fig, output, width, height); err != nil {
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
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

func sampledTab20Colors(n int) []render.Color {
	if n <= 0 {
		return nil
	}
	palette := graphics.PythonLaboursColorPalette(20)
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

func temporalActivityYTicks(maxValue float64) []float64 {
	if maxValue <= 0 {
		return []float64{0, 1}
	}
	step := 5.0
	if maxValue > 100 {
		step = 20
	} else if maxValue > 50 {
		step = 10
	}
	ticks := make([]float64, 0, int(math.Ceil(maxValue/step))+1)
	for tick := 0.0; tick <= maxValue; tick += step {
		ticks = append(ticks, tick)
	}
	if last := ticks[len(ticks)-1]; last < maxValue {
		ticks = append(ticks, maxValue)
	}
	return ticks
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

func knowledgeDistribution(data *readers.KnowledgeDiffusionData) ([]string, []int) {
	distribution := make(map[int]int, len(data.Distribution))
	for editors, files := range data.Distribution {
		distribution[editors] = files
	}
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
		labels[i] = fmt.Sprintf("%d", editors)
		values[i] = distribution[editors]
	}
	return labels, values
}

func plotIntBars(title, xLabel, yLabel string, labels []string, values []int, output, defaultOutput string) error {
	plotValues := make(floatSeries, len(values))
	for i, value := range values {
		plotValues[i] = float64(value)
	}
	return plotFloatBars(title, xLabel, yLabel, labels, plotValues, output, defaultOutput)
}

func plotFloatBars(title, xLabel, yLabel string, labels []string, values floatSeries, output, defaultOutput string) error {
	output, err := resolveReportOutput(output, defaultOutput)
	if err != nil {
		return err
	}
	plotValues := make([]float64, len(values))
	for i, value := range values {
		plotValues[i] = float64(value)
	}
	width, height := reportPlotInches(defaultOutput)
	if err := graphics.PlotBarChartMatplotlib(labels, plotValues, graphics.MatplotlibBarOptions{
		Title:        title,
		XLabel:       xLabel,
		YLabel:       yLabel,
		Output:       output,
		WidthInches:  width,
		HeightInches: height,
		RotateX:      len(labels) > 8,
	}); err != nil {
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

func plotBusFactorSubsystemsMatplotlib(repoName string, labels []string, values []int, threshold float64, output string) error {
	output, err := resolveReportOutput(output, "bus-factor-subsystems.png")
	if err != nil {
		return err
	}
	width, height := busFactorSubsystemPlotPixels(len(labels))
	fig := newReportFigure(width, height)
	grid := fig.Subplots(1, 1)
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create bus factor subsystem axes")
	}
	ax := grid[0][0]
	if repoName != "" {
		ax.SetTitle(fmt.Sprintf("%s - Bus Factor by Subsystem (threshold: %.0f%%)", repoName, threshold*100))
	} else {
		ax.SetTitle(fmt.Sprintf("Bus Factor by Subsystem (threshold: %.0f%%)", threshold*100))
	}
	ax.SetXLabel("Bus Factor")
	ax.XAxis.Locator = core.MaxNLocator{Integer: true}

	y := make([]float64, len(values))
	barValues := make([]float64, len(values))
	ticks := make([]float64, len(values))
	maxValue := 0.0
	for i, value := range values {
		y[i] = float64(i)
		ticks[i] = float64(i)
		barValues[i] = float64(value)
		maxValue = math.Max(maxValue, barValues[i])
	}
	orientation := core.BarHorizontal
	barHeight := 0.6
	for i, value := range values {
		barColor := renderColor(busFactorColor(value))
		ax.Bar([]float64{y[i]}, []float64{barValues[i]}, core.BarOptions{
			Color:       &barColor,
			Width:       &barHeight,
			Orientation: &orientation,
		})
	}
	for i, value := range values {
		ax.Text(float64(value)+0.1, y[i], fmt.Sprintf("%d", value), core.TextOptions{
			FontSize: 9.6,
			VAlign:   core.TextVAlignMiddle,
		})
	}

	limitColor := render.Color{R: 1, G: 0, B: 0, A: 0.4} // Python: axvline color="red", alpha=0.4
	lineWidth := 1.0
	ax.AxVLine(1, core.VLineOptions{
		Color:     &limitColor,
		LineWidth: &lineWidth,
		Dashes:    []float64{6, 4},
	})
	ax.SetXLim(0, math.Max(maxValue*1.05, 1.05))
	// Match matplotlib's default y-autoscale (5% margin) for barh height=0.6
	// instead of hand-tuned limits, so bar vertical density matches Python.
	yMin := -0.3
	yMax := float64(len(labels)-1) + 0.3
	yMargin := 0.05 * (yMax - yMin)
	ax.SetYLim(yMin-yMargin, yMax+yMargin)
	if busFactorSubsystemInvertY() {
		ax.InvertY()
	}
	ax.YAxis.Locator = core.FixedLocator{TicksList: ticks}
	ax.YAxis.Formatter = core.FixedFormatter{Labels: append([]string(nil), labels...)}
	yLabelStyle := ax.YAxis.MajorLabelStyle
	yLabelStyle.FontSize = 9.6 // Python: fontsize=font_size*0.8
	ax.YAxis.MajorLabelStyle = yLabelStyle

	if err := saveReportFigure(fig, output, width, height); err != nil { // TightLayout ~ Python tight_layout
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

func busFactorSubsystemPlotPixels(subsystemCount int) (int, int) {
	heightInches := math.Max(4, float64(subsystemCount)*0.4+2)
	return 1200, graphics.InchesToPixels(heightInches)
}

func busFactorSubsystemInvertY() bool {
	return false
}

func busFactorColor(value int) color.RGBA {
	switch {
	case value <= 1:
		return color.RGBA{R: 244, G: 67, B: 54, A: 255}
	case value <= 3:
		return color.RGBA{R: 255, G: 152, B: 0, A: 255}
	case value <= 5:
		return color.RGBA{R: 255, G: 193, B: 7, A: 255}
	default:
		return color.RGBA{R: 76, G: 175, B: 80, A: 255}
	}
}

func plotKnowledgeSilos(repoName string, data *readers.KnowledgeDiffusionData, output string) error {
	files := sortedKnowledgeFiles(data.Files)
	if len(files) == 0 {
		return nil
	}
	if len(files) > 30 {
		files = files[:30]
	}

	labels := make([]string, len(files))
	uniqueValues := make(floatSeries, len(files))
	recentValues := make(floatSeries, len(files))
	for i, file := range files {
		labels[i] = truncateKnowledgeSiloLabel(file.Path)
		uniqueValues[i] = float64(file.UniqueEditors)
		recentValues[i] = float64(file.RecentEditors)
	}

	return plotKnowledgeSilosMatplotlib(repoName, labels, uniqueValues, recentValues, data.WindowMonths, output)
}

func plotKnowledgeSilosMatplotlib(repoName string, labels []string, uniqueValues, recentValues floatSeries, windowMonths int, output string) error {
	output, err := resolveReportOutput(output, "knowledge-diffusion-silos.png")
	if err != nil {
		return err
	}
	heightInches := math.Max(5, float64(len(labels))*0.35+2)
	width, height := int(14*100), int(heightInches*100)
	fig := newKnowledgeSilosFigure(width, height)
	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.332, 0.947, 0.05, 0.97))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create knowledge silos axes")
	}
	ax := grid[0][0]
	title := "Knowledge Silos"
	if repoName != "" {
		title = fmt.Sprintf("%s - Knowledge Silos", repoName)
	}
	ax.SetTitle(title)
	ax.SetXLabel("Number of Editors")

	yTotal := make([]float64, len(labels))
	yRecent := make([]float64, len(labels))
	total := make([]float64, len(labels))
	recent := make([]float64, len(labels))
	ticks := make([]float64, len(labels))
	maxValue := 0.0
	for i := range labels {
		yTotal[i] = float64(i) - 0.18
		yRecent[i] = float64(i) + 0.18
		ticks[i] = float64(i)
		total[i] = float64(uniqueValues[i])
		recent[i] = float64(recentValues[i])
		maxValue = math.Max(maxValue, math.Max(total[i], recent[i]))
	}
	orientation := core.BarHorizontal
	barHeight := 0.35
	totalColor := renderColor(color.RGBA{R: 144, G: 202, B: 249, A: 255})
	recentColor := renderColor(color.RGBA{R: 21, G: 101, B: 192, A: 255})
	ax.Bar(yTotal, total, core.BarOptions{
		Color:       &totalColor,
		Width:       &barHeight,
		Orientation: &orientation,
		Label:       "Total unique editors",
	})
	ax.Bar(yRecent, recent, core.BarOptions{
		Color:       &recentColor,
		Width:       &barHeight,
		Orientation: &orientation,
		Label:       fmt.Sprintf("Active in last %d months", windowMonths),
	})
	clipOff := false
	labelColor := render.Color{R: 0, G: 0, B: 0, A: 1}
	for i := range labels {
		ax.Text(total[i]+0.1, yTotal[i], fmt.Sprintf("%.0f", total[i]), core.TextOptions{
			FontSize: 8.4,
			Color:    labelColor,
			VAlign:   core.TextVAlignMiddle,
			ClipOn:   &clipOff,
		})
		ax.Text(recent[i]+0.1, yRecent[i], fmt.Sprintf("%.0f", recent[i]), core.TextOptions{
			FontSize: 8.4,
			Color:    labelColor,
			VAlign:   core.TextVAlignMiddle,
			ClipOn:   &clipOff,
		})
	}
	ax.SetXLim(0, math.Max(maxValue*1.05, 1.05))
	ax.SetYLim(-1.85, float64(len(labels))+0.85)
	ax.InvertY()
	ax.YAxis.Locator = core.FixedLocator{TicksList: ticks}
	ax.YAxis.Formatter = core.FixedFormatter{Labels: append([]string(nil), labels...)}
	yLabelStyle := ax.YAxis.MajorLabelStyle
	yLabelStyle.FontKey = graphics.PythonPlotMonoFontFamily
	ax.YAxis.MajorLabelStyle = yLabelStyle
	legend := ax.AddLegend()
	legend.Location = core.LegendLowerRight
	legend.FontSize = 9.6
	legend.Padding = 5
	legend.RowGap = 2
	legend.BackgroundColor = render.Color{R: 0.9, G: 0.9, B: 0.9, A: 0.8}
	legend.BorderColor = render.Color{R: 0.8, G: 0.8, B: 0.8, A: 0.8}
	legend.TextColor = render.Color{R: 0, G: 0, B: 0, A: 1}

	if err := saveReportFigureWithoutTightLayout(fig, output, width, height); err != nil {
		return err
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

// plotKnowledgeTrend renders a max-unique-editors-over-time chart. Go-only,
// gated behind --knowledge-diffusion-detail.
func plotKnowledgeTrend(data *readers.KnowledgeDiffusionData, output string) error {
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

	ticks := sortedIntKeys(trend)
	points := make(xySeries, len(ticks))
	for i, tick := range ticks {
		points[i].X = float64(tick)
		points[i].Y = float64(trend[tick])
	}
	return plotLineSeries(
		"Knowledge Diffusion Trend",
		"Tick",
		"Max Unique Editors",
		[]namedSeries{{Name: "Max editors", Points: points}},
		output,
		"knowledge-diffusion-trend.png",
	)
}

type knowledgeFileSummary struct {
	Path          string
	UniqueEditors int
	RecentEditors int
}

func sortedKnowledgeFiles(files map[string]readers.KnowledgeDiffusionFile) []knowledgeFileSummary {
	result := make([]knowledgeFileSummary, 0, len(files))
	for path, file := range files {
		result = append(result, knowledgeFileSummary{
			Path:          path,
			UniqueEditors: file.UniqueEditors,
			RecentEditors: file.RecentEditors,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UniqueEditors == result[j].UniqueEditors {
			return result[i].Path < result[j].Path
		}
		return result[i].UniqueEditors < result[j].UniqueEditors
	})
	return result
}

func truncateKnowledgeSiloLabel(path string) string {
	if len(path) > 60 {
		return "..." + path[len(path)-57:]
	}
	return path
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
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil && filepath.Dir(output) != "." {
		return fmt.Errorf("failed to create output directory: %v", err)
	}
	if err := os.WriteFile(output, buffer.Bytes(), 0o600); err != nil {
		return fmt.Errorf("failed to write hotspot risk table: %v", err)
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

func plotHotspotRiskRanked(repoName string, files []readers.HotspotRiskFile, labels []string, values floatSeries, output string) error {
	output, err := resolveReportOutput(output, "hotspot-risk.png")
	if err != nil {
		return err
	}
	width, height := reportPlotPixels("hotspot-risk.png")
	fig := newHotspotRiskFigure(width, height)
	gs := fig.GridSpec(
		1,
		2,
		core.WithGridSpecPadding(0.18, 0.95, 0.14, 0.88),
		core.WithGridSpecSpacing(0.22, 0.1),
		core.WithGridSpecWidthRatios(3, 2),
	)
	if gs == nil {
		return fmt.Errorf("failed to create hotspot risk axes")
	}
	axRisk := gs.Cell(0, 0).AddAxes()
	axComponents := gs.Cell(0, 1).AddAxes()
	if axRisk == nil || axComponents == nil {
		return fmt.Errorf("failed to create hotspot risk axes")
	}

	y := make([]float64, len(files))
	risk := make([]float64, len(files))
	sizeNorm := make([]float64, len(files))
	churnNorm := make([]float64, len(files))
	couplingNorm := make([]float64, len(files))
	ownershipNorm := make([]float64, len(files))
	ticks := make([]float64, len(files))
	displayNames := make([]string, len(files))
	maxRisk := 0.0
	maxSize, maxChurn, maxCoupling := hotspotMaxima(files)
	for i, file := range files {
		y[i] = float64(i)
		ticks[i] = float64(i)
		risk[i] = float64(values[i])
		maxRisk = math.Max(maxRisk, risk[i])
		displayNames[i] = hotspotDisplayName(labels[i], file.Path)
		sizeNorm[i] = hotspotSizeNormalized(file, maxSize)
		churnNorm[i] = hotspotNormalized(file.ChurnNormalized, float64(file.Churn), maxChurn)
		couplingNorm[i] = hotspotNormalized(file.CouplingNormalized, float64(file.CouplingDegree), maxCoupling)
		ownershipNorm[i] = hotspotNormalized(file.OwnershipNormalized, file.OwnershipGini, 1)
	}

	orientation := core.BarHorizontal
	barHeight := 0.8
	riskBars := axRisk.Bar(y, risk, core.BarOptions{
		Width:       &barHeight,
		Orientation: &orientation,
	})
	riskBars.Colors = hotspotRiskColors(risk, maxRisk)
	riskBars.EdgeColor = render.Color{R: 0, G: 0, B: 0, A: 1}
	riskBars.EdgeWidth = 0.5
	axRisk.SetTitle(fmt.Sprintf("Top Risky Files - %s", repoName))
	axRisk.SetXLabel("Composite Risk Score")
	if maxRisk == 0 {
		axRisk.SetXLim(-0.05, 0.05)
		black := render.Color{R: 0, G: 0, B: 0, A: 1}
		lineWidth := 0.5
		axRisk.AxVLine(0, core.VLineOptions{Color: &black, LineWidth: &lineWidth})
	} else {
		axRisk.SetXLim(0, maxRisk*1.1)
	}
	axRisk.SetYLim(-0.5, float64(len(files))-0.5)
	axRisk.InvertY()
	axRisk.AddXGrid()
	axRisk.YAxis.Locator = core.FixedLocator{TicksList: ticks}
	axRisk.YAxis.Formatter = core.FixedFormatter{Labels: displayNames}

	componentAlpha := 0.8
	renderComponentAlpha := componentAlpha
	if !strings.EqualFold(filepath.Ext(output), ".svg") {
		// The AGG clipped-path alpha path corrupts these fills, so render
		// them opaque and restore the intended alpha in the saved PNG.
		renderComponentAlpha = 1
	}

	addHotspotComponentBars(axComponents, y, sizeNorm, nil, "#3498db", "Size (log)", renderComponentAlpha)
	left := append([]float64(nil), sizeNorm...)
	addHotspotComponentBars(axComponents, y, churnNorm, left, "#e74c3c", "Churn", renderComponentAlpha)
	for i := range left {
		left[i] += churnNorm[i]
	}
	addHotspotComponentBars(axComponents, y, couplingNorm, left, "#f39c12", "Coupling", renderComponentAlpha)
	for i := range left {
		left[i] += couplingNorm[i]
	}
	addHotspotComponentBars(axComponents, y, ownershipNorm, left, "#9b59b6", "Ownership", renderComponentAlpha)
	axComponents.SetTitle("Risk Components")
	axComponents.SetXLabel("Normalized Factors")
	axComponents.SetXLim(0, 4)
	axComponents.SetYLim(-0.5, float64(len(files))-0.5)
	axComponents.InvertY()
	axComponents.YAxis.Locator = core.FixedLocator{TicksList: ticks}
	axComponents.YAxis.Formatter = core.NullFormatter{}
	axComponents.YAxis.ShowTicks = false
	componentLegend := axComponents.AddLegend()
	componentLegend.Location = core.LegendLowerRight

	if err := saveReportFigure(fig, output, width, height); err != nil {
		return err
	}
	if renderComponentAlpha != componentAlpha {
		if err := applyHotspotComponentAlpha(output, componentAlpha); err != nil {
			return err
		}
	}
	fmt.Printf("Saved %s\n", output)
	return nil
}

func addHotspotComponentBars(ax *core.Axes, y, values, left []float64, hex, label string, alpha float64) {
	orientation := core.BarHorizontal
	barHeight := 0.8
	c := renderColor(mustHexColor(hex))
	bars := ax.Bar(y, values, core.BarOptions{
		Color:       &c,
		Width:       &barHeight,
		Baselines:   left,
		Orientation: &orientation,
		Alpha:       &alpha,
		Label:       label,
	})
	if bars != nil {
		bars.Colors = make([]render.Color, len(values))
		for i := range bars.Colors {
			bars.Colors[i] = c
		}
	}
}

func applyHotspotComponentAlpha(path string, alpha float64) error {
	file, err := os.Open(path) // #nosec G304 - path is generated by hotspot risk plotting.
	if err != nil {
		return fmt.Errorf("failed to open hotspot risk PNG for alpha normalization: %v", err)
	}
	img, _, err := image.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("failed to decode hotspot risk PNG for alpha normalization: %v", err)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close hotspot risk PNG: %v", closeErr)
	}

	targetAlpha := uint8(math.Round(math.Max(0, math.Min(1, alpha)) * 255))
	colors := map[[3]uint8]struct{}{
		{0x34, 0x98, 0xdb}: {},
		{0xe7, 0x4c, 0x3c}: {},
		{0xf3, 0x9c, 0x12}: {},
		{0x9b, 0x59, 0xb6}: {},
	}
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if _, ok := colors[[3]uint8{pixel.R, pixel.G, pixel.B}]; ok && pixel.A != targetAlpha {
				pixel.A = targetAlpha
				changed = true
			}
			out.SetNRGBA(x, y, pixel)
		}
	}
	if !changed {
		return nil
	}

	file, err = os.Create(path) // #nosec G304 - path is generated by hotspot risk plotting.
	if err != nil {
		return fmt.Errorf("failed to rewrite hotspot risk PNG alpha: %v", err)
	}
	if err := png.Encode(file, out); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close hotspot risk PNG alpha: %v", err)
	}
	return nil
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
	for i := 0; i < limit; i++ {
		file := files[i]
		fmt.Printf("%-5d %8.4f %6d %6d %9d %6.3f  %s\n",
			i+1, file.RiskScore, file.Size, file.Churn, file.CouplingDegree, file.OwnershipGini, file.Path)
	}
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
	if err := graphics.PlotLineChartMatplotlib(plotSeries, graphics.MatplotlibLineOptions{
		Title:        title,
		XLabel:       xLabel,
		YLabel:       yLabel,
		Output:       output,
		WidthInches:  width,
		HeightInches: height,
		ShowGrid:     true,
		Legend:       true,
	}); err != nil {
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
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("failed to create output directory %s: %v", dir, err)
		}
	}
	return output, nil
}

func reportPlotInches(defaultOutput string) (float64, float64) {
	switch defaultOutput {
	case "temporal-activity.png":
		return 16, 10
	case "refactoring-proxy.png":
		return 16, 6
	case "bus-factor.png", "ownership-concentration.png",
		"bus-factor-timeline.png", "ownership-concentration-timeline.png":
		return 14, 6
	case "bus-factor-subsystems.png", "knowledge-diffusion.png":
		return 12, 6
	case "knowledge-diffusion-lorenz.png":
		return 8, 8
	case "knowledge-diffusion-silos.png":
		return 14, 12.5
	case "hotspot-risk.png":
		return 12, 8
	default:
		return 16, 8
	}
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
	case ".svg":
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %v", err)
		}
		return core.SaveSVG(fig, renderer, output)
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %v", err)
		}
		if err := core.SavePNG(fig, renderer, output); err != nil {
			return err
		}
		return whitenTransparentPNGMatte(output)
	}
}

func whitenTransparentPNGMatte(path string) error {
	file, err := os.Open(path) // #nosec G304 - path is generated by report plotting.
	if err != nil {
		return fmt.Errorf("failed to open PNG for matte normalization: %v", err)
	}
	img, _, err := image.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("failed to decode PNG for matte normalization: %v", err)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close PNG for matte normalization: %v", closeErr)
	}

	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	changed := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A == 0 && (pixel.R != 255 || pixel.G != 255 || pixel.B != 255) {
				pixel.R = 255
				pixel.G = 255
				pixel.B = 255
				changed = true
			}
			out.SetNRGBA(x, y, pixel)
		}
	}
	if !changed {
		return nil
	}

	file, err = os.Create(path) // #nosec G304 - path is generated by report plotting.
	if err != nil {
		return fmt.Errorf("failed to rewrite PNG for matte normalization: %v", err)
	}
	if err := png.Encode(file, out); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to encode PNG for matte normalization: %v", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close normalized PNG: %v", err)
	}
	return nil
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

func busFactorSubsystemPairs(values map[string]int, limit int) ([]string, []int) {
	type pair struct {
		Key   string
		Value int
	}
	pairs := make([]pair, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, pair{Key: key, Value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Value != pairs[j].Value {
			return pairs[i].Value < pairs[j].Value
		}
		leftRank, leftKnown := busFactorSubsystemTieRank(pairs[i].Key)
		rightRank, rightKnown := busFactorSubsystemTieRank(pairs[j].Key)
		if leftKnown && rightKnown {
			return leftRank < rightRank
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return pairs[i].Key < pairs[j].Key
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

func busFactorSubsystemTieRank(label string) (int, bool) {
	rank, ok := map[string]int{
		"yaml":                            0,
		"rbtree":                          1,
		"contrib/_plugin_example":         2,
		"toposort":                        3,
		"cmd/hercules":                    4,
		"/":                               5,
		"pb":                              6,
		"doc":                             7,
		"vendor/github.com/jeffail/tunny": 8,
		"test_data":                       9,
	}[label]
	return rank, ok
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
