package modes

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// OldVsNew generates an analysis showing the evolution of new code vs modifications to existing code over time.
// This provides insights into development patterns - whether the project is in growth mode (lots of new code)
// vs maintenance mode (lots of modifications to existing code).
func OldVsNew(reader readers.Reader, output string, startTime, endTime *time.Time, resample string) error {
	opts := defaultOptions()
	opts.Resample = resample

	return OldVsNewWithOptions(reader, output, startTime, endTime, opts)
}

func OldVsNewWithOptions(
	reader readers.Reader,
	output string,
	startTime, endTime *time.Time,
	opts Options,
) error {
	rendered, err := renderActualOldVsNew(reader, output, opts.Graphics)
	if err != nil {
		return err
	}

	if rendered {
		return nil
	}

	totalLinesAdded, totalLinesModified, err := oldVsNewTotals(reader)
	if err != nil {
		return err
	}
	const timeSeriesLength = 52
	newCodeSeries := generateOldVsNewTimeSeries(totalLinesAdded, timeSeriesLength, "new")
	modifiedCodeSeries := generateOldVsNewTimeSeries(totalLinesModified, timeSeriesLength, "modified")
	dates := syntheticOldVsNewDates(timeSeriesLength)

	return generateOldVsNewPlot(newCodeSeries, modifiedCodeSeries, dates, output, opts.Graphics)
}

func renderActualOldVsNew(
	reader readers.Reader,
	output string,
	visuals graphics.Options,
) (bool, error) {
	timeSeries, timeSeriesErr := reader.GetDeveloperTimeSeriesData()
	if timeSeriesErr != nil && !errors.Is(timeSeriesErr, readers.ErrAnalysisMissing) {
		return false, fmt.Errorf("get developer time series: %w", timeSeriesErr)
	}

	if errors.Is(timeSeriesErr, readers.ErrAnalysisMissing) || len(timeSeries.Days) == 0 {
		return false, nil
	}

	startUnix, endUnix := reader.GetHeader()
	if startUnix <= 0 || endUnix <= startUnix {
		return false, nil
	}

	newLines, oldLines, dates := oldVsNewDailySeries(timeSeries, startUnix, endUnix)

	return true, generateOldVsNewPlot(newLines, oldLines, dates, output, visuals)
}

func oldVsNewTotals(reader readers.Reader) (int, int, error) {
	developerStats, err := reader.GetDeveloperStats()
	if err != nil && !errors.Is(err, readers.ErrAnalysisMissing) {
		return 0, 0, fmt.Errorf("get developer stats: %w", err)
	}

	if err != nil || len(developerStats) == 0 {
		return oldVsNewBurndownTotals(reader)
	}

	totalLinesAdded, totalLinesModified := 0, 0
	for _, stat := range developerStats {
		totalLinesAdded += stat.LinesAdded
		totalLinesModified += stat.LinesModified
	}

	return totalLinesAdded, totalLinesModified, nil
}

func oldVsNewBurndownTotals(reader readers.Reader) (int, int, error) {
	fmt.Println("Developer stats not available, using synthetic data based on project burndown...")

	_, _, matrix, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return 0, 0, fmt.Errorf("get project burndown fallback: %w", err)
	}

	if len(matrix) == 0 {
		return 0, 0, fmt.Errorf("%w: old-vs-new", readers.ErrAnalysisMissing)
	}

	finalLines := 0
	for _, value := range matrix[len(matrix)-1] {
		finalLines += value
	}

	return int(float64(finalLines) * 0.6), int(float64(finalLines) * 0.4), nil
}

func syntheticOldVsNewDates(length int) []time.Time {
	dates := make([]time.Time, length)
	for i := range dates {
		dates[i] = time.Unix(0, 0).AddDate(0, 0, i)
	}

	return dates
}

// generateOldVsNewTimeSeries creates a time series showing the evolution of code changes over time.
// This is a simplified implementation - a full version would use actual temporal data from the repository.
func generateOldVsNewTimeSeries(totalLines, length int, changeType string) []float64 {
	series := make([]float64, length)

	if changeType == "new" {
		// New code typically starts high in early project phases and then decreases
		// as the project matures and moves to maintenance mode
		for i := range length {
			// Exponential decay to simulate project maturation
			factor := 1.0 - float64(i)/float64(length)*0.7
			series[i] = float64(totalLines) / float64(length) * factor
		}
	} else {
		// Modified code typically starts low and increases as the project matures
		// and more refactoring/maintenance work is done
		for i := range length {
			// Gradual increase to simulate transition to maintenance mode
			factor := 0.3 + float64(i)/float64(length)*0.7
			series[i] = float64(totalLines) / float64(length) * factor
		}
	}

	return series
}

func oldVsNewDailySeries(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64) ([]float64, []float64, []time.Time) {
	start, _, seriesLength := timeSeriesCalendarRange(timeSeries, startUnix, endUnix, 1)

	newLines := make([]float64, seriesLength)

	oldLines := make([]float64, seriesLength)
	for day, devs := range timeSeries.Days {
		if day < 0 || day >= seriesLength {
			continue
		}

		for _, stats := range devs {
			newLines[day] += float64(stats.LinesAdded)
			oldLines[day] += float64(stats.LinesRemoved + stats.LinesModified)
		}
	}

	windowSize := max(seriesLength/32, 1)
	window := oldVsNewSmoothingWindow(windowSize)
	newLines = convolveSame(newLines, window)
	oldLines = convolveSame(oldLines, window)

	dates := make([]time.Time, seriesLength)
	for i := range dates {
		dates[i] = start.AddDate(0, 0, i)
	}

	return newLines, oldLines, dates
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func timeSeriesCalendarRange(timeSeries *readers.DeveloperTimeSeriesData, startUnix, endUnix int64, extraDays int) (time.Time, time.Time, int) {
	start := dateOnly(time.Unix(startUnix, 0))
	end := dateOnly(time.Unix(endUnix, 0))

	size := max(calendarDayCount(start, end)+extraDays, 1)

	for day := range timeSeries.Days {
		if day < 0 {
			continue
		}

		if needed := day + 1; needed > size {
			size = needed
		}
	}

	return start, end, size
}

func calendarDayCount(start, end time.Time) int {
	start = dateOnly(start)

	end = dateOnly(end)
	if end.Before(start) {
		return 1
	}

	count := 1
	for current := start; current.Before(end); current = current.AddDate(0, 0, 1) {
		count++
	}

	return count
}

func calendarDayIndex(start, target time.Time) int {
	start = dateOnly(start)

	target = dateOnly(target)
	if target.Before(start) {
		return -calendarDayCount(target, start) + 1
	}

	index := 0
	for current := start; current.Before(target); current = current.AddDate(0, 0, 1) {
		index++
	}

	return index
}

func oldVsNewSmoothingWindow(size int) []float64 {
	if size <= 1 {
		return []float64{1}
	}

	window := make([]float64, size)
	mid := float64(size-1) / 2

	sigma := float64(size) / 2.8
	for i := range window {
		x := (float64(i) - mid) / sigma
		window[i] = math.Exp(-0.5 * x * x)
	}

	return window
}

func convolveSame(series, window []float64) []float64 {
	if len(series) == 0 || len(window) == 0 {
		return series
	}

	out := make([]float64, len(series))

	center := len(window) / 2
	for i := range series {
		for j, weight := range window {
			idx := i + j - center
			if idx >= 0 && idx < len(series) {
				out[i] += series[idx] * weight
			}
		}
	}

	return out
}

// generateOldVsNewPlot creates an overlaid area chart showing new vs modified code over time.
func generateOldVsNewPlot(
	newCodeSeries, modifiedCodeSeries []float64,
	dates []time.Time,
	output string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}

	length := len(newCodeSeries)
	if len(modifiedCodeSeries) != length {
		return errors.New("new code and modified code series must have the same length")
	}

	if len(dates) != length {
		return errors.New("dates and series must have the same length")
	}

	series := []graphics.MatplotlibTimeAreaSeries{
		{
			Label:  "Changed new lines",
			Values: newCodeSeries,
			Color:  colorRGBA(141, 184, 67, 255),
		},
		{
			Label:  "Changed existing lines",
			Values: modifiedCodeSeries,
			Color:  colorRGBA(225, 76, 53, 255),
		},
	}

	if output == "" {
		output = "old-vs-new.png"
	}

	err := graphics.PlotTimeAreasMatplotlib(dates, series, graphics.MatplotlibTimeAreaOptions{
		Title:        "Additions vs changes",
		Output:       output,
		WidthInches:  6.4,
		HeightInches: 4.8,
		Legend:       true,
		LegendLeft:   true,
		LegendTop:    true,
		HideFrame:    true,
		AutoXMargin:  true,
		Alpha:        1,
		FontSize:     visuals.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save old-vs-new plot: %w", err)
	}

	fmt.Printf("Old vs New analysis plot saved to %s\n", output)

	return nil
}

func colorRGBA(r, g, b, a uint8) color.Color {
	return color.RGBA{R: r, G: g, B: b, A: a}
}
