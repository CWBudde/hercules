package modes

import (
	"fmt"
	"math"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/render"
)

func RefactoringProxy(reader readers.Reader, output string) error {
	proxyReader, ok := reader.(readers.RefactoringProxyReader)
	if !ok {
		return fmt.Errorf("%w: RefactoringProxy", readers.ErrAnalysisMissing)
	}
	data, err := proxyReader.GetRefactoringProxy()
	if err != nil {
		return err
	}
	if data == nil || len(data.Ticks) == 0 {
		return fmt.Errorf("%w: RefactoringProxy", readers.ErrAnalysisMissing)
	}
	return plotRefactoringProxy(reader.GetName(), data, output)
}

func plotRefactoringProxy(repoName string, data *readers.RefactoringProxyData, output string) error {
	output, err := resolveReportOutput(output, "refactoring-proxy.png")
	if err != nil {
		return err
	}

	timestamps := make([]time.Time, len(data.Ticks))
	x := make([]float64, len(data.Ticks))
	rates := make([]float64, len(data.Ticks))
	maxRate := 0.0
	for i, tick := range data.Ticks {
		timestamps[i] = time.Unix(tick.Timestamp, 0).UTC()
		x[i] = refactoringProxyDateNumber(timestamps[i])
		rates[i] = float64(tick.RefactoringRate)
		maxRate = math.Max(maxRate, rates[i])
	}

	width, height := reportPlotPixels("refactoring-proxy.png")
	fig := newReportFigure(width, height)
	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.080, 0.950, 0.140, 0.900))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return fmt.Errorf("failed to create refactoring proxy axes")
	}
	ax := grid[0][0]

	lineColor := render.Color{R: 0x2e / 255.0, G: 0x86 / 255.0, B: 0xab / 255.0, A: 1}
	lineWidth := 2.0
	if _, err := ax.PlotUnits(timestamps, rates, core.PlotOptions{
		Color:     &lineColor,
		LineWidth: &lineWidth,
		Label:     "Refactoring Rate",
	}); err != nil {
		return fmt.Errorf("failed to plot refactoring rate: %v", err)
	}

	threshold := float64(data.Threshold)
	thresholdColor := render.Color{R: 0xe6 / 255.0, G: 0x39 / 255.0, B: 0x46 / 255.0, A: 1}
	thresholdWidth := 1.5
	ax.Plot([]float64{x[0], x[len(x)-1]}, []float64{threshold, threshold}, core.PlotOptions{
		Color:     &thresholdColor,
		LineWidth: &thresholdWidth,
		Dashes:    []float64{6, 4},
		Label:     fmt.Sprintf("Threshold (%.1f%%)", threshold*100),
	})

	spanColor := render.Color{R: 0xa8 / 255.0, G: 0xda / 255.0, B: 0xdc / 255.0, A: 1}
	spanAlpha := 0.2
	for _, region := range refactoringProxyRegions(data.Ticks, threshold, x) {
		ax.AxVSpan(region.Start, region.End, core.VSpanOptions{
			Color: &spanColor,
			Alpha: &spanAlpha,
		})
	}

	ax.SetXLabel("Date")
	ax.SetYLabel("Refactoring Rate (Renames/Moves per Commit)")
	if repoName != "" {
		ax.SetTitle(fmt.Sprintf("%s - Refactoring Proxy Timeline", repoName))
	} else {
		ax.SetTitle("Refactoring Proxy Timeline")
	}
	ax.YAxis.Formatter = core.PercentFormatter{XMax: 1, Decimals: 0}
	ax.SetYLim(0, math.Max(maxRate, threshold)*1.08)
	legend := ax.AddLegend()
	legend.Location = core.LegendUpperRight

	if err := saveReportFigureWithoutTightLayout(fig, output, width, height); err != nil {
		return fmt.Errorf("failed to save refactoring proxy chart: %v", err)
	}
	fmt.Printf("Saved refactoring proxy chart to %s\n", output)
	return nil
}

type refactoringProxyRegion struct {
	Start float64
	End   float64
}

func refactoringProxyRegions(ticks []readers.RefactoringProxyTick, threshold float64, x []float64) []refactoringProxyRegion {
	regions := []refactoringProxyRegion{}
	currentStart := math.NaN()
	for i, tick := range ticks {
		isRefactoring := float64(tick.RefactoringRate) >= threshold
		switch {
		case isRefactoring && math.IsNaN(currentStart):
			currentStart = x[i]
		case !isRefactoring && !math.IsNaN(currentStart):
			regions = append(regions, refactoringProxyRegion{Start: currentStart, End: x[i]})
			currentStart = math.NaN()
		}
	}
	if !math.IsNaN(currentStart) && len(x) > 0 {
		regions = append(regions, refactoringProxyRegion{Start: currentStart, End: x[len(x)-1]})
	}
	return regions
}

func refactoringProxyDateNumber(t time.Time) float64 {
	t = t.UTC()
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}
