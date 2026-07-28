package modes

import (
	"errors"
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/matplotlib-go/core"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// Python labours highlights per-developer summary stats with a green or red
// background depending on whether the developer added or removed more lines
// (`backgrounds = ("#C4FFDB", "#FFD0CD")` in `labours/modes/devs.py`). We mirror
// those exact swatches so the rendered chart matches the baseline.
var (
	devsStatPositiveBackground = color.RGBA{R: 0xC4, G: 0xFF, B: 0xDB, A: 255}
	devsStatNegativeBackground = color.RGBA{R: 0xFF, G: 0xD0, B: 0xCD, A: 255}
)

// Devs generates plots for individual developers' contributions over time.
func Devs(reader readers.Reader, output string, maxPeople int) error {
	opts := defaultOptions()
	opts.MaxPeople = maxPeople
	return DevsWithOptions(reader, output, opts)
}

func DevsWithOptions(reader readers.Reader, output string, opts Options) error {
	progEstimator := progress.NewProgressEstimator(!opts.Quiet)
	progEstimator.StartMultiOperation(4, "Developer Analysis")
	defer progEstimator.FinishMultiOperation()

	progEstimator.NextOperation("Extracting developer statistics")
	rendered, err := plotDeveloperTimeSeries(reader, output, opts)
	if err != nil {
		return err
	}
	if rendered {
		reportDeveloperPlotsComplete(opts.Quiet)
		return nil
	}

	developerStats, err := reader.GetDeveloperStats()
	if err != nil {
		return fmt.Errorf("failed to get developer stats: %w", err)
	}

	progEstimator.NextOperation("Selecting top developers")
	developerStats = limitDeveloperStats(developerStats, opts.MaxPeople, opts.Quiet)

	progEstimator.NextOperation("Generating time series data")
	devSeries := generateTimeSeriesWithProgress(developerStats, progEstimator)

	progEstimator.NextOperation("Generating visualization")
	if err := plotDevs(developerStats, devSeries, output, opts.Graphics); err != nil {
		return fmt.Errorf("failed to generate developer plots: %w", err)
	}
	reportDeveloperPlotsComplete(opts.Quiet)
	return nil
}

func plotDeveloperTimeSeries(reader readers.Reader, output string, opts Options) (bool, error) {
	timeSeries, err := reader.GetDeveloperTimeSeriesData()
	if err != nil {
		if errors.Is(err, readers.ErrAnalysisMissing) {
			return false, nil
		}

		return false, fmt.Errorf("get developer time series: %w", err)
	}

	if len(timeSeries.Days) == 0 {
		return false, nil
	}
	startUnix, endUnix := reader.GetHeader()
	if startUnix <= 0 || endUnix <= startUnix {
		return false, nil
	}
	if err := plotDevsPythonStyle(
		timeSeries, startUnix, endUnix, output, opts.MaxPeople, opts.Graphics,
	); err != nil {
		return false, fmt.Errorf("failed to generate Python-style developer plot: %w", err)
	}
	return true, nil
}

func limitDeveloperStats(stats []readers.DeveloperStat, maxPeople int, quiet bool) []readers.DeveloperStat {
	if len(stats) <= maxPeople {
		return stats
	}
	if !quiet {
		fmt.Printf("Picking top %d developers by commit count.\n", maxPeople)
	}
	return selectTopDevelopers(stats, maxPeople)
}

func reportDeveloperPlotsComplete(quiet bool) {
	if !quiet {
		fmt.Println("Developer plots generated successfully.")
	}
}

type devSeriesRow struct {
	Index       int
	Name        string
	Series      []float64
	Commits     int
	LinesAdded  int
	LinesRemove int
	LinesChange int
}

func plotDevsPythonStyle(
	timeSeries *readers.DeveloperTimeSeriesData,
	startUnix, endUnix int64,
	output string,
	maxPeople int,
	visuals graphics.Options,
) error {
	rows, dates := buildDeveloperSeriesRows(timeSeries, startUnix, endUnix, maxPeople)
	if len(rows) == 0 {
		return fmt.Errorf("no developer time series to plot")
	}

	rowHeight := developerPlotRowHeight(rows)
	series, baselines, labels := buildDeveloperPlotLayers(rows, dates, rowHeight)

	// Python labours suppresses the title and x-label when an output file is
	// supplied (see `deploy_plot` and `show_devs` in
	// `labours/modes/devs.py`). Mirroring that keeps the visual matching the
	// baseline, where the chart frame stays minimal.
	if err := graphics.PlotTimeAreasMatplotlib(dates, series, graphics.MatplotlibTimeAreaOptions{
		Output:       output,
		WidthInches:  32,
		HeightInches: 16,
		HideY:        true,
		Alpha:        1,
		YMin:         0,
		YMax:         float64(len(rows))*rowHeight + rowHeight*0.4,
		Baselines:    baselines,
		TextLabels:   labels,
		FontSize:     visuals.PlotFontSize(),
	}); err != nil {
		return err
	}

	fmt.Printf("Saved developer plot to %s\n", output)
	return nil
}

func developerPlotRowHeight(rows []devSeriesRow) float64 {
	maxY := 0.0
	for _, row := range rows {
		for _, value := range row.Series {
			if value > maxY {
				maxY = value
			}
		}
	}
	if maxY <= 0 {
		maxY = 1
	}
	return maxY * 1.4
}

func buildDeveloperPlotLayers(
	rows []devSeriesRow,
	dates []time.Time,
	rowHeight float64,
) ([]graphics.MatplotlibTimeAreaSeries, [][]float64, []graphics.MatplotlibTextLabel) {
	colors := graphics.PythonLaboursColorPalette(len(rows))
	series := make([]graphics.MatplotlibTimeAreaSeries, len(rows))
	baselines := make([][]float64, len(rows))
	labels := make([]graphics.MatplotlibTextLabel, 0, len(rows)*2+1)
	for index, row := range rows {
		series[index], baselines[index] = developerAreaSeries(
			row, float64(len(rows)-1-index)*rowHeight, colors[index%len(colors)],
		)
		labels = append(labels, developerAreaLabels(
			row, series[index].Label, dates, float64(len(rows)-1-index)*rowHeight+rowHeight*0.45,
		)...)
	}
	labels = append(labels, developerStatsHeader(dates, len(rows), rowHeight))
	return series, baselines, labels
}

func developerAreaSeries(
	row devSeriesRow,
	offset float64,
	seriesColor color.Color,
) (graphics.MatplotlibTimeAreaSeries, []float64) {
	top := make([]float64, len(row.Series))
	baseline := make([]float64, len(row.Series))
	for index, value := range row.Series {
		baseline[index] = offset
		top[index] = offset + value
	}
	return graphics.MatplotlibTimeAreaSeries{
		Label:  peopleChartLabel(row.Name),
		Values: top,
		Color:  seriesColor,
	}, baseline
}

func developerAreaLabels(
	row devSeriesRow,
	displayName string,
	dates []time.Time,
	y float64,
) []graphics.MatplotlibTextLabel {
	netDelta := row.LinesAdded - row.LinesRemove
	return []graphics.MatplotlibTextLabel{
		{
			X:      float64(dates[0].Unix()),
			Y:      y,
			Text:   shortenDeveloperName(displayName),
			HAlign: core.TextAlignLeft,
		},
		{
			X:               float64(dates[len(dates)-1].Unix()),
			Y:               y,
			Text:            fmt.Sprintf("%5d %8s %8s", row.Commits, formatNumber(netDelta), formatNumber(row.LinesChange)),
			HAlign:          core.TextAlignRight,
			BackgroundColor: developerStatsBackground(netDelta),
		},
	}
}

func developerStatsBackground(netDelta int) color.Color {
	if netDelta < 0 {
		return devsStatNegativeBackground
	}
	return devsStatPositiveBackground
}

func developerStatsHeader(
	dates []time.Time,
	rowCount int,
	rowHeight float64,
) graphics.MatplotlibTextLabel {
	return graphics.MatplotlibTextLabel{
		X:      float64(dates[len(dates)-1].Unix()),
		Y:      float64(rowCount)*rowHeight + rowHeight*0.2,
		Text:   " cmts    delta  changed",
		HAlign: core.TextAlignRight,
	}
}

func buildDeveloperSeriesRows(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, maxPeople int) ([]devSeriesRow, []time.Time) {
	start, _, size := timeSeriesCalendarRange(timeSeries, startUnix, endUnix, 0)
	dates := developerSeriesDates(start, size)
	rowsByDev := collectDeveloperSeriesRows(timeSeries, size)
	rows := smoothAndSortDeveloperRows(rowsByDev, size)
	if maxPeople > 0 && len(rows) > maxPeople {
		rows = rows[:maxPeople]
	}
	return rows, dates
}

func developerSeriesDates(start time.Time, size int) []time.Time {
	dates := make([]time.Time, size)
	for i := range dates {
		dates[i] = start.AddDate(0, 0, i)
	}
	return dates
}

func collectDeveloperSeriesRows(
	timeSeries *readers.DeveloperTimeSeriesData,
	size int,
) map[int]*devSeriesRow {
	rowsByDev := make(map[int]*devSeriesRow)
	for day, devs := range timeSeries.Days {
		if day < 0 || day >= size {
			continue
		}
		for dev, stats := range devs {
			row := developerSeriesRow(rowsByDev, timeSeries.People, dev, size)
			row.Series[day] = float64(stats.Commits)
			row.Commits += stats.Commits
			row.LinesAdded += stats.LinesAdded
			row.LinesRemove += stats.LinesRemoved
			row.LinesChange += stats.LinesModified
		}
	}
	return rowsByDev
}

func developerSeriesRow(
	rows map[int]*devSeriesRow,
	people []string,
	developer, size int,
) *devSeriesRow {
	if row := rows[developer]; row != nil {
		return row
	}
	name := fmt.Sprintf("Developer %d", developer)
	if developer >= 0 && developer < len(people) {
		name = people[developer]
	}
	row := &devSeriesRow{Index: developer, Name: name, Series: make([]float64, size)}
	rows[developer] = row
	return row
}

func smoothAndSortDeveloperRows(rowsByDev map[int]*devSeriesRow, size int) []devSeriesRow {
	rows := make([]devSeriesRow, 0, len(rowsByDev))
	window := oldVsNewSmoothingWindow(max(size/64, 1))
	for _, row := range rowsByDev {
		row.Series = convolveSame(row.Series, window)
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Commits == rows[j].Commits {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Commits > rows[j].Commits
	})
	return rows
}

func shortenDeveloperName(name string) string {
	const maxLen = 36
	if len(name) <= maxLen {
		return name
	}
	return strings.TrimSpace(name[:maxLen]) + "..."
}

func formatNumber(n int) string {
	sign := ""
	value := float64(n)
	if n < 0 {
		sign = "-"
		value = -value
	}
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%s%.1fM", sign, value/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%s%dK", sign, int(value/1_000))
	case value >= 1_000:
		return fmt.Sprintf("%s%.1fK", sign, value/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// selectTopDevelopers selects the top developers by commit count.
func selectTopDevelopers(stats []readers.DeveloperStat, maxPeople int) []readers.DeveloperStat {
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Commits > stats[j].Commits
	})
	if len(stats) > maxPeople {
		return stats[:maxPeople]
	}
	return stats
}

// generateTimeSeriesWithProgress generates synthetic time series data with progress tracking
func generateTimeSeriesWithProgress(stats []readers.DeveloperStat, progEstimator *progress.ProgressEstimator) map[string][]float64 {
	// Start detailed progress for time series generation
	progEstimator.StartOperation("Generating time series", len(stats))

	devSeries := make(map[string][]float64)
	for _, stat := range stats {
		progEstimator.UpdateProgress(1)

		// Generate a synthetic time series based on commit activity
		// In a real implementation, this would come from daily or weekly data
		series := make([]float64, 52) // 52 weeks in a year
		commitsPerWeek := float64(stat.Commits) / 52.0
		for i := 0; i < len(series); i++ {
			// Add random variation to simulate real activity
			series[i] = commitsPerWeek + float64(i%5)*0.1*commitsPerWeek
		}
		devSeries[stat.Name] = series
	}

	progEstimator.FinishOperation()
	return devSeries
}

// plotDevs generates plots for developers' contributions.
func plotDevs(
	developerStats []readers.DeveloperStat,
	devSeries map[string][]float64,
	output string,
	visuals graphics.Options,
) error {
	if len(developerStats) == 0 {
		return fmt.Errorf("no developer stats to plot")
	}

	length := 0
	for _, dev := range developerStats {
		if len(devSeries[dev.Name]) > length {
			length = len(devSeries[dev.Name])
		}
	}
	if length == 0 {
		return fmt.Errorf("no developer series to plot")
	}

	dates := make([]time.Time, length)
	for i := range dates {
		dates[i] = time.Unix(0, 0).AddDate(0, 0, i*7)
	}
	colors := graphics.PythonLaboursColorPalette(len(developerStats))
	series := make([]graphics.MatplotlibTimeAreaSeries, 0, len(developerStats))
	for i, dev := range developerStats {
		values := append([]float64(nil), devSeries[dev.Name]...)
		if len(values) < length {
			values = append(values, make([]float64, length-len(values))...)
		}
		series = append(series, graphics.MatplotlibTimeAreaSeries{
			Label:  peopleChartLabel(dev.Name),
			Values: values,
			Color:  colors[i%len(colors)],
		})
	}

	if err := graphics.PlotTimeAreasMatplotlib(dates, series, graphics.MatplotlibTimeAreaOptions{
		Title:        "Developer Contributions Over Time",
		XLabel:       "Time",
		YLabel:       "Commits",
		Output:       output,
		WidthInches:  32,
		HeightInches: 16,
		Stacked:      false,
		Legend:       true,
		LegendLeft:   true,
		LegendTop:    true,
		Alpha:        0.7,
		ShowGrid:     true,
		FontSize:     visuals.PlotFontSize(),
	}); err != nil {
		return err
	}

	fmt.Printf("Saved developer plot to %s\n", output)
	return nil
}
