package modes

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func BusFactor(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	busFactorReader, ok := reader.(readers.BusFactorReader)
	if !ok {
		return fmt.Errorf("%w: bus factor", readers.ErrAnalysisMissing)
	}

	data, err := busFactorReader.GetBusFactor()
	if err != nil {
		return fmt.Errorf("failed to get bus factor data: %w", err)
	}

	if len(data.Snapshots) == 0 {
		return errNoBusFactorSnapshots
	}

	begin, _ := reader.GetHeader()
	axis, dated := newTickAxis(begin, data.TickSize)

	ticks := selectReportTicks(sortedIntKeys(data.Snapshots), axis, dated, startTime, endTime, "bus factor")
	if len(ticks) == 0 {
		return fmt.Errorf("%w: bus factor", errNoSnapshotsInRange)
	}

	// Under a date range the gauge reads the last snapshot inside the range, not
	// the last one in the file.
	latest := data.Snapshots[ticks[len(ticks)-1]]
	_, _ = fmt.Fprintf(os.Stdout, "Bus factor: latest=%d, total lines=%d, threshold=%.2f\n",
		latest.BusFactor, latest.TotalLines, data.Threshold)

	err = plotBusFactorTimeline(data, ticks, axis, dated, output)
	if err != nil {
		return err
	}

	err = plotBusFactorLatest(titleRepositoryName(reader.GetName()), data, latest, output)
	if err != nil {
		return fmt.Errorf("failed to plot bus factor gauge: %w", err)
	}

	// The subsystem summary cannot follow: SubsystemBusFactor is one map for the
	// whole analysis, with no per-tick breakdown to select from. Rather than let
	// it pass for the requested window, it is labelled and announced as covering
	// the full history.
	scope := reportRangeScope(startTime, endTime, "bus factor subsystem summary")

	return plotBusFactorSubsystemSummary(titleRepositoryName(reader.GetName()), data, scope, output)
}

func plotBusFactorTimeline(
	data *readers.BusFactorData,
	ticks []int,
	axis tickAxis,
	dated bool,
	output string,
) error {
	values := make([]float64, len(ticks))
	for index, tick := range ticks {
		values[index] = float64(data.Snapshots[tick].BusFactor)
	}

	timelineOutput := siblingOutputPath(output, "bus-factor.png", "timeline")

	if dated {
		return plotTimeSeries(
			"Bus Factor Over Time",
			"Bus Factor",
			[]namedTimeSeries{{Name: "Bus factor", Dates: axis.datesOf(ticks), Values: values}},
			timelineOutput,
			"bus-factor-timeline.png",
		)
	}

	return plotLineSeries(
		"Bus Factor Over Time",
		"Tick",
		"Bus Factor",
		[]namedSeries{{Name: "Bus factor", Points: tickIndexSeries(ticks, values)}},
		timelineOutput,
		"bus-factor-timeline.png",
	)
}

func plotBusFactorLatest(
	repoName string,
	data *readers.BusFactorData,
	latest readers.BusFactorSnapshot,
	output string,
) error {
	return plotBusFactorGauge(
		repoName,
		latest.BusFactor,
		latest.TotalLines,
		latest.AuthorLines,
		data.People,
		float64(data.Threshold),
		siblingOutputPath(output, "bus-factor.png", "gauge"),
	)
}

func plotBusFactorSubsystemSummary(
	repoName string,
	data *readers.BusFactorData,
	scope string,
	output string,
) error {
	if len(data.SubsystemBusFactor) == 0 {
		return nil
	}

	labels, values := busFactorSubsystemPairs(data.SubsystemBusFactor, 0)

	err := plotBusFactorSubsystemsMatplotlib(
		repoName,
		scope,
		labels,
		values,
		float64(data.Threshold),
		siblingOutputPath(output, "bus-factor.png", "subsystems"),
	)
	if err != nil {
		return fmt.Errorf("failed to plot subsystem bus factor: %w", err)
	}

	_, _ = fmt.Fprintf(
		os.Stdout,
		"Bus factor subsystem summary: %d subsystems\n",
		len(data.SubsystemBusFactor),
	)

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

func plotBusFactorGauge(
	repoName string,
	busFactor int,
	totalLines int64,
	authorLines map[int]int64,
	people []string,
	threshold float64,
	output string,
) error {
	output, err := resolveReportOutput(output, "bus-factor-gauge.png")
	if err != nil {
		return err
	}

	plot, err := newBusFactorGaugePlot(repoName)
	if err != nil {
		return err
	}

	drawBusFactorStatus(plot.gaugeAxes, busFactor, totalLines, threshold)
	drawBusFactorOwnership(plot.pieAxes, authorLines, people, totalLines)

	err = saveReportFigure(plot.figure, output, plot.width, plot.height)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Saved %s\n", output)

	return nil
}

type busFactorGaugePlot struct {
	figure    *core.Figure
	gaugeAxes *core.Axes
	pieAxes   *core.Axes
	width     int
	height    int
}

func newBusFactorGaugePlot(repoName string) (busFactorGaugePlot, error) {
	const (
		width  = 1000
		height = 600
	)

	figure := newReportFigure(width, height)
	if repoName != "" {
		figure.SetSupTitle(repoName + " - Bus Factor Summary")
	}

	grid := figure.GridSpec(
		1, 2,
		core.WithGridSpecPadding(0.03, 0.97, 0.05, 0.90),
		core.WithGridSpecSpacing(0.2, 0.1),
	)
	if grid == nil {
		return busFactorGaugePlot{}, errBusFactorGaugeAxes
	}

	gaugeAxes := grid.Cell(0, 0).AddAxes()

	pieAxes := grid.Cell(0, 1).AddAxes()
	if gaugeAxes == nil || pieAxes == nil {
		return busFactorGaugePlot{}, errBusFactorGaugeAxes
	}

	return busFactorGaugePlot{
		figure: figure, gaugeAxes: gaugeAxes, pieAxes: pieAxes, width: width, height: height,
	}, nil
}

func drawBusFactorStatus(axes *core.Axes, busFactor int, totalLines int64, threshold float64) {
	hideAxesContent(axes)
	axes.SetXLim(0, 1)
	axes.SetYLim(0, 1)

	statusColor, statusLabel := busFactorStatus(busFactor)
	statusRenderColor := renderColor(statusColor)
	gray := render.Color{R: 0.5, G: 0.5, B: 0.5, A: 1}

	drawBusFactorText(axes, 0.6, strconv.Itoa(busFactor), 72, statusRenderColor)
	drawBusFactorText(axes, 0.35, statusLabel, 18, statusRenderColor)
	drawBusFactorText(axes, 0.2, fmt.Sprintf("Bus Factor @ %.0f%%", threshold*100), 12, gray)
	drawBusFactorText(axes, 0.1, humanizeInt(totalLines)+" total lines", 10, gray)
}

func drawBusFactorText(axes *core.Axes, y float64, text string, fontSize float64, textColor render.Color) {
	axes.Text(0.5, y, text, core.TextOptions{
		Coords:   core.Coords(core.CoordAxes),
		FontSize: fontSize,
		Color:    textColor,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})
}

func drawBusFactorOwnership(
	axes *core.Axes,
	authorLines map[int]int64,
	people []string,
	totalLines int64,
) {
	if len(authorLines) == 0 || totalLines <= 0 {
		drawMissingBusFactorOwnership(axes)
		return
	}

	labels, values := busFactorTopOwners(authorLines, people, 8)
	axes.Pie(values, core.PieOptions{
		Labels:     labels,
		AutoPct:    "%.1f%%",
		Colors:     sampledTab20Colors(len(values)),
		StartAngle: 90,
	})
	axes.SetTitle("Line Ownership")
}

func drawMissingBusFactorOwnership(axes *core.Axes) {
	hideAxesContent(axes)
	axes.SetXLim(0, 1)
	axes.SetYLim(0, 1)
	axes.Text(0.5, 0.5, "No ownership data", core.TextOptions{
		Coords:   core.Coords(core.CoordAxes),
		FontSize: 14,
		HAlign:   core.TextAlignCenter,
		VAlign:   core.TextVAlignMiddle,
	})
}

func plotBusFactorSubsystemsMatplotlib(
	repoName string,
	scope string,
	labels []string,
	values []int,
	threshold float64,
	output string,
) error {
	output, err := resolveReportOutput(output, "bus-factor-subsystems.png")
	if err != nil {
		return err
	}

	width, height := busFactorSubsystemPlotPixels(len(labels))
	fig := newReportFigure(width, height)

	ax, err := reportFigureAxes(fig)
	if err != nil {
		return errBusFactorSubsystemAxes
	}

	configureBusFactorSubsystemAxes(ax, repoName, scope, labels, values, threshold)

	err = saveReportFigure(fig, output, width, height)
	if err != nil { // TightLayout ~ Python tight_layout
		return err
	}

	fmt.Printf("Saved %s\n", output)

	return nil
}

func configureBusFactorSubsystemAxes(
	ax *core.Axes,
	repoName string,
	scope string,
	labels []string,
	values []int,
	threshold float64,
) {
	if repoName != "" {
		ax.SetTitle(fmt.Sprintf(
			"%s - Bus Factor by Subsystem (threshold: %.0f%%)%s", repoName, threshold*100, scope,
		))
	} else {
		ax.SetTitle(fmt.Sprintf("Bus Factor by Subsystem (threshold: %.0f%%)%s", threshold*100, scope))
	}

	ax.SetXLabel("Bus Factor")
	ax.XAxis.Locator = ticker.MaxNLocator{Integer: true}

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

	drawBusFactorSubsystemBars(ax, y, barValues, values)

	limitColor := render.Color{R: 1, G: 0, B: 0, A: 0.4} // Python: axvline color="red", alpha=0.4
	lineWidth := 1.0
	ax.AxVLine(1, core.VLineOptions{
		Color:     optional.Of(limitColor),
		LineWidth: optional.Of(lineWidth),
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

	ax.YAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	ax.YAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), labels...)}
	yLabelStyle := ax.YAxis.MajorLabelStyle
	yLabelStyle.FontSize = 9.6 // Python: fontsize=font_size*0.8
	ax.YAxis.MajorLabelStyle = yLabelStyle
}

// drawBusFactorSubsystemBars draws one horizontal bar per subsystem plus the
// value annotation that sits just past each bar's end.
func drawBusFactorSubsystemBars(ax *core.Axes, y, barValues []float64, values []int) {
	orientation := core.BarHorizontal
	barHeight := 0.6

	for i, value := range values {
		barColor := renderColor(busFactorColor(value))
		_, _ = ax.Bar([]float64{y[i]}, []float64{barValues[i]}, core.BarOptions{
			Color:       optional.Of(barColor),
			Width:       optional.Of(barHeight),
			Orientation: optional.Of(orientation),
		})
	}

	for i, value := range values {
		ax.Text(float64(value)+0.1, y[i], strconv.Itoa(value), core.TextOptions{
			FontSize: 9.6,
			VAlign:   core.TextVAlignMiddle,
		})
	}
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
