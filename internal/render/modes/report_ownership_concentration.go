package modes

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// The two concentration measures are labelled identically on the timeline and
// on the subsystem bars, so they are named once.
const (
	giniSeriesLabel = "Gini"
	hhiSeriesLabel  = "HHI"
)

func OwnershipConcentration(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
	if !ok {
		return fmt.Errorf("%w: ownership concentration", readers.ErrAnalysisMissing)
	}

	data, err := ownershipReader.GetOwnershipConcentration()
	if err != nil {
		return fmt.Errorf("failed to get ownership concentration data: %w", err)
	}

	if len(data.Snapshots) == 0 {
		return errNoOwnershipSnapshots
	}

	begin, _ := reader.GetHeader()
	axis, dated := newTickAxis(begin, data.TickSize)

	ticks := selectReportTicks(
		sortedIntKeys(data.Snapshots), axis, dated, startTime, endTime, "ownership concentration",
	)
	if len(ticks) == 0 {
		return fmt.Errorf("%w: ownership concentration", errNoSnapshotsInRange)
	}

	latest := data.Snapshots[ticks[len(ticks)-1]]
	fmt.Printf("Ownership concentration: latest gini=%.3f, hhi=%.3f, total lines=%d\n",
		latest.Gini, latest.HHI, latest.TotalLines)

	timelineOutput := siblingOutputPath(output, "ownership-concentration.png", "timeline")

	err = plotOwnershipConcentrationTimeline(data, ticks, axis, dated, timelineOutput)
	if err != nil {
		return err
	}

	if len(data.SubsystemGini) > 0 {
		subsystemOutput := siblingOutputPath(output, "ownership-concentration.png", "subsystems")

		// As with bus factor, SubsystemGini/SubsystemHHI are single whole-analysis
		// readings with no per-tick breakdown, so this chart cannot follow the
		// requested range and says as much.
		scope := reportRangeScope(startTime, endTime, "ownership concentration subsystem summary")

		err := plotOwnershipSubsystemsBar(
			titleRepositoryName(reader.GetName()), scope, data.SubsystemGini, data.SubsystemHHI, subsystemOutput,
		)
		if err != nil {
			return fmt.Errorf("failed to plot subsystem ownership concentration: %w", err)
		}

		fmt.Printf("Ownership concentration subsystem summary: %d subsystems\n", len(data.SubsystemGini))
	}

	return nil
}

// plotOwnershipConcentrationTimeline draws the Gini and HHI timelines, over real
// dates where the input supports it and over bare tick indices otherwise.
func plotOwnershipConcentrationTimeline(
	data *readers.OwnershipConcentrationData,
	ticks []int,
	axis tickAxis,
	dated bool,
	output string,
) error {
	gini, hhi := ownershipConcentrationSeries(data, ticks)

	if dated {
		dates := axis.datesOf(ticks)

		return plotTimeSeries(
			"Ownership Concentration Over Time",
			"Concentration",
			[]namedTimeSeries{
				{Name: giniSeriesLabel, Dates: dates, Values: gini},
				{Name: hhiSeriesLabel, Dates: dates, Values: hhi},
			},
			output,
			"ownership-concentration-timeline.png",
		)
	}

	return plotLineSeries(
		"Ownership Concentration Over Time",
		"Tick",
		"Concentration",
		[]namedSeries{
			{Name: giniSeriesLabel, Points: tickIndexSeries(ticks, gini)},
			{Name: hhiSeriesLabel, Points: tickIndexSeries(ticks, hhi)},
		},
		output,
		"ownership-concentration-timeline.png",
	)
}

// ownershipConcentrationSeries turns the per-tick snapshots into the Gini and
// HHI values, in the order given by ticks.
func ownershipConcentrationSeries(
	data *readers.OwnershipConcentrationData,
	ticks []int,
) (gini, hhi []float64) {
	gini = make([]float64, len(ticks))

	hhi = make([]float64, len(ticks))
	for i, tick := range ticks {
		snapshot := data.Snapshots[tick]
		gini[i] = snapshot.Gini
		hhi[i] = snapshot.HHI
	}

	return gini, hhi
}

// plotOwnershipSubsystemsBar mirrors Python labours' ownership_concentration
// _plot_subsystems: a grouped *horizontal* bar chart with two series (Gini and
// HHI) per subsystem, subsystems sorted alphabetically (not by value).
func plotOwnershipSubsystemsBar(
	repoName, scope string, giniByDir, hhiByDir map[string]float64, output string,
) error {
	output, err := resolveReportOutput(output, "ownership-concentration-subsystems.png")
	if err != nil {
		return err
	}

	series := newOwnershipSubsystemSeries(giniByDir, hhiByDir)

	plot, err := newOwnershipSubsystemPlot(repoName, scope, len(series.directories))
	if err != nil {
		return err
	}

	drawOwnershipSubsystemBars(plot.axes, series)
	configureOwnershipSubsystemAxes(plot.axes, series)

	return saveReportFigure(plot.figure, output, plot.width, plot.height)
}

const ownershipSubsystemBarHeight = 0.35

type ownershipSubsystemSeries struct {
	directories []string
	giniY       []float64
	hhiY        []float64
	giniValues  []float64
	hhiValues   []float64
	ticks       []float64
}

type ownershipSubsystemPlot struct {
	figure *core.Figure
	axes   *core.Axes
	width  int
	height int
}

func newOwnershipSubsystemSeries(
	giniByDirectory, hhiByDirectory map[string]float64,
) ownershipSubsystemSeries {
	directories := make([]string, 0, len(giniByDirectory))
	for directory := range giniByDirectory {
		directories = append(directories, directory)
	}

	sort.Strings(directories)

	series := ownershipSubsystemSeries{
		directories: directories,
		giniY:       make([]float64, len(directories)),
		hhiY:        make([]float64, len(directories)),
		giniValues:  make([]float64, len(directories)),
		hhiValues:   make([]float64, len(directories)),
		ticks:       make([]float64, len(directories)),
	}
	for index, directory := range directories {
		series.ticks[index] = float64(index)
		series.giniY[index] = float64(index) - ownershipSubsystemBarHeight/2
		series.hhiY[index] = float64(index) + ownershipSubsystemBarHeight/2
		series.giniValues[index] = giniByDirectory[directory]
		series.hhiValues[index] = hhiByDirectory[directory]
	}

	return series
}

func newOwnershipSubsystemPlot(repoName, scope string, subsystemCount int) (ownershipSubsystemPlot, error) {
	heightInches := math.Max(4, float64(subsystemCount)*0.5+2)
	width := graphics.InchesToPixels(12)
	height := graphics.InchesToPixels(heightInches)
	figure := newReportFigure(width, height)

	grid := figure.Subplots(1, 1)
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return ownershipSubsystemPlot{}, errOwnershipSubsystemAxes
	}

	axes := grid[0][0]
	axes.SetTitle(ownershipSubsystemTitle(repoName, scope))
	axes.SetXLabel("Concentration Index")

	return ownershipSubsystemPlot{figure: figure, axes: axes, width: width, height: height}, nil
}

func ownershipSubsystemTitle(repoName, scope string) string {
	const title = "Ownership Concentration by Subsystem"
	if repoName == "" {
		return title + scope
	}

	return fmt.Sprintf("%s - %s%s", repoName, title, scope)
}

func drawOwnershipSubsystemBars(axes *core.Axes, series ownershipSubsystemSeries) {
	orientation := core.BarHorizontal
	barHeight := ownershipSubsystemBarHeight
	giniColor := render.Color{R: 233.0 / 255, G: 30.0 / 255, B: 99.0 / 255, A: 0.8}
	hhiColor := render.Color{R: 63.0 / 255, G: 81.0 / 255, B: 181.0 / 255, A: 0.8}

	_, _ = axes.Bar(series.giniY, series.giniValues, core.BarOptions{
		Color: optional.Of(giniColor), Width: optional.Of(barHeight),
		Orientation: optional.Of(orientation), Label: giniSeriesLabel,
	})
	_, _ = axes.Bar(series.hhiY, series.hhiValues, core.BarOptions{
		Color: optional.Of(hhiColor), Width: optional.Of(barHeight),
		Orientation: optional.Of(orientation), Label: hhiSeriesLabel,
	})
	drawOwnershipSubsystemLabels(axes, series)
}

func drawOwnershipSubsystemLabels(axes *core.Axes, series ownershipSubsystemSeries) {
	clipOff := false

	labelColor := render.Color{R: 0, G: 0, B: 0, A: 1}
	for index := range series.directories {
		axes.Text(
			series.giniValues[index]+0.02,
			series.giniY[index],
			fmt.Sprintf("%.2f", series.giniValues[index]),
			core.TextOptions{
				FontSize: 8.4, Color: labelColor, VAlign: core.TextVAlignMiddle, ClipOn: optional.Of(clipOff),
			},
		)
		axes.Text(
			series.hhiValues[index]+0.02,
			series.hhiY[index],
			fmt.Sprintf("%.2f", series.hhiValues[index]),
			core.TextOptions{
				FontSize: 8.4, Color: labelColor, VAlign: core.TextVAlignMiddle, ClipOn: optional.Of(clipOff),
			},
		)
	}
}

func configureOwnershipSubsystemAxes(axes *core.Axes, series ownershipSubsystemSeries) {
	axes.SetXLim(0, 1.1)

	dataMin := -ownershipSubsystemBarHeight
	dataMax := float64(len(series.directories)-1) + ownershipSubsystemBarHeight
	margin := 0.05 * (dataMax - dataMin)
	axes.SetYLim(dataMin-margin, dataMax+margin)
	axes.YAxis.Locator = ticker.FixedLocator{TicksList: series.ticks}
	axes.YAxis.Formatter = ticker.FixedFormatter{
		Labels: append([]string(nil), series.directories...),
	}
	labelStyle := axes.YAxis.MajorLabelStyle
	labelStyle.FontSize = 9.6
	axes.YAxis.MajorLabelStyle = labelStyle
	legend := axes.AddLegend()
	legend.FontSize = 9.6
}
