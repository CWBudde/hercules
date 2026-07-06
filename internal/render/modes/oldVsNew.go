package modes

import (
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
	if timeSeries, err := reader.GetDeveloperTimeSeriesData(); err == nil && len(timeSeries.Days) > 0 {
		startUnix, endUnix := reader.GetHeader()
		if startUnix > 0 && endUnix > startUnix {
			newLines, oldLines, dates := oldVsNewDailySeries(timeSeries, startUnix, endUnix)
			return generateOldVsNewPlot(newLines, oldLines, dates, output)
		}
	}

	// Try to get developer statistics first
	developerStats, err := reader.GetDeveloperStats()

	var totalLinesAdded, totalLinesModified int

	if err != nil || len(developerStats) == 0 {
		// If developer stats are not available, try to derive data from project burndown
		fmt.Println("Developer stats not available, using synthetic data based on project burndown...")

		// Try to get burndown data, but handle potential panics
		var burndownMatrix [][]int
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("Warning: error accessing burndown data: %v\n", r)
					burndownMatrix = nil
				}
			}()
			_, burndownMatrix = reader.GetProjectBurndown()
		}()

		if len(burndownMatrix) == 0 {
			fmt.Println("No burndown data available, using demo values for old-vs-new analysis")
			// Use demo values that represent a typical project evolution
			totalLinesAdded = 10000
			totalLinesModified = 6000
		} else {
			// Estimate total lines from burndown data - use the final value as a proxy
			if len(burndownMatrix) > 0 && len(burndownMatrix[len(burndownMatrix)-1]) > 0 {
				finalLines := 0
				for _, val := range burndownMatrix[len(burndownMatrix)-1] {
					finalLines += val
				}
				// Rough estimation: assume 60% new code, 40% modified code for a typical project
				totalLinesAdded = int(float64(finalLines) * 0.6)
				totalLinesModified = int(float64(finalLines) * 0.4)
			} else {
				// Fallback to demo values
				totalLinesAdded = 10000
				totalLinesModified = 6000
			}
		}
	} else {
		// Aggregate the data across all developers
		for _, stat := range developerStats {
			totalLinesAdded += stat.LinesAdded
			totalLinesModified += stat.LinesModified
		}
	}

	// Create time series data (simplified approach - in a full implementation this would use temporal data)
	timeSeriesLength := 52 // 52 weeks for demonstration
	newCodeSeries := generateOldVsNewTimeSeries(totalLinesAdded, timeSeriesLength, "new")
	modifiedCodeSeries := generateOldVsNewTimeSeries(totalLinesModified, timeSeriesLength, "modified")

	dates := make([]time.Time, timeSeriesLength)
	for i := range dates {
		dates[i] = time.Unix(0, 0).AddDate(0, 0, i)
	}

	return generateOldVsNewPlot(newCodeSeries, modifiedCodeSeries, dates, output)
}

// generateOldVsNewTimeSeries creates a time series showing the evolution of code changes over time.
// This is a simplified implementation - a full version would use actual temporal data from the repository.
func generateOldVsNewTimeSeries(totalLines int, length int, changeType string) []float64 {
	series := make([]float64, length)

	if changeType == "new" {
		// New code typically starts high in early project phases and then decreases
		// as the project matures and moves to maintenance mode
		for i := 0; i < length; i++ {
			// Exponential decay to simulate project maturation
			factor := 1.0 - float64(i)/float64(length)*0.7
			series[i] = float64(totalLines) / float64(length) * factor
		}
	} else {
		// Modified code typically starts low and increases as the project matures
		// and more refactoring/maintenance work is done
		for i := 0; i < length; i++ {
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
	size := calendarDayCount(start, end) + extraDays
	if size < 1 {
		size = 1
	}
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
func generateOldVsNewPlot(newCodeSeries, modifiedCodeSeries []float64, dates []time.Time, output string) error {
	length := len(newCodeSeries)
	if len(modifiedCodeSeries) != length {
		return fmt.Errorf("new code and modified code series must have the same length")
	}
	if len(dates) != length {
		return fmt.Errorf("dates and series must have the same length")
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
	if err := graphics.PlotTimeAreasMatplotlib(dates, series, graphics.MatplotlibTimeAreaOptions{
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
	}); err != nil {
		return fmt.Errorf("failed to save old-vs-new plot: %v", err)
	}

	fmt.Printf("Old vs New analysis plot saved to %s\n", output)
	return nil
}

func colorRGBA(r, g, b, a uint8) color.Color {
	return color.RGBA{R: r, G: g, B: b, A: a}
}
