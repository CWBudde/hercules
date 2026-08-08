package modes

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/dates"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/readers"
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

	return plotRefactoringProxy(titleRepositoryName(reader.GetName()), data, output)
}

func plotRefactoringProxy(repoName string, data *readers.RefactoringProxyData, output string) error {
	output, err := resolveReportOutput(output, "refactoring-proxy.png")
	if err != nil {
		return err
	}

	timestamps, dateNumbers := refactoringProxyDateNumbers(data.Ticks)
	rates := make([]float64, len(data.Ticks))
	maxRate := 0.0

	for i, tick := range data.Ticks {
		rates[i] = float64(tick.RefactoringRate)
		maxRate = math.Max(maxRate, rates[i])
	}

	width, height := reportPlotPixels("refactoring-proxy.png")
	fig := newReportFigure(width, height)

	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.080, 0.950, 0.140, 0.900))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return errors.New("failed to create refactoring proxy axes")
	}

	ax := grid[0][0]

	lineColor := render.Color{R: 0x2e / 255.0, G: 0x86 / 255.0, B: 0xab / 255.0, A: 1}
	lineWidth := 2.0

	_, err = ax.Plot(timestamps, rates, core.PlotOptions{
		Color:     optional.Of(lineColor),
		LineWidth: optional.Of(lineWidth),
		Label:     "Refactoring Rate",
	})
	if err != nil {
		return fmt.Errorf("failed to plot refactoring rate: %w", err)
	}

	threshold := float64(data.Threshold)
	thresholdColor := render.Color{R: 0xe6 / 255.0, G: 0x39 / 255.0, B: 0x46 / 255.0, A: 1}
	thresholdWidth := 1.5

	firstDate, lastDate := dateNumbers[0], dateNumbers[len(dateNumbers)-1]

	_, err = ax.Plot([]float64{firstDate, lastDate}, []float64{threshold, threshold}, core.PlotOptions{
		Color:     optional.Of(thresholdColor),
		LineWidth: optional.Of(thresholdWidth),
		Dashes:    []float64{6, 4},
		Label:     fmt.Sprintf("Threshold (%.1f%%)", threshold*100),
	})
	if err != nil {
		return fmt.Errorf("failed to plot refactoring threshold: %w", err)
	}

	spanColor := render.Color{R: 0xa8 / 255.0, G: 0xda / 255.0, B: 0xdc / 255.0, A: 1}

	spanAlpha := 0.2
	for _, region := range refactoringProxyRegions(data.Ticks, threshold, dateNumbers) {
		ax.AxVSpan(region.Start, region.End, core.VSpanOptions{
			Color: optional.Of(spanColor),
			Alpha: optional.Of(spanAlpha),
		})
	}

	configureRefactoringProxyAxes(ax, repoName, maxRate, threshold)

	err = saveReportFigureWithoutTightLayout(fig, output, width, height)
	if err != nil {
		return fmt.Errorf("failed to save refactoring proxy chart: %w", err)
	}

	fmt.Printf("Saved refactoring proxy chart to %s\n", output)

	return nil
}

func configureRefactoringProxyAxes(ax *core.Axes, repoName string, maxRate, threshold float64) {
	ax.SetXLabel("Date")
	ax.SetYLabel("Refactoring Rate (Renames/Moves per Commit)")

	title := "Refactoring Proxy Timeline"
	if repoName != "" {
		title = fmt.Sprintf("%s - %s", repoName, title)
	}

	ax.SetTitle(title)
	ax.YAxis.Formatter = ticker.PercentFormatter{XMax: 1, Decimals: 0}
	ax.SetYLim(0, math.Max(maxRate, threshold)*1.08)
	ax.AddLegend().Location = core.LegendUpperRight
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

// refactoringProxyDateNumbers returns the tick timestamps together with their
// axis coordinates. Plotting a []time.Time series switches the axis to date
// units, so every overlay drawn from raw float64 x values (the threshold line,
// the vertical spans) has to use the same dates.Date2Num conversion the series
// went through. Unix seconds here would stretch the axis by five orders of
// magnitude and collapse the series into a spike at the left edge.
func refactoringProxyDateNumbers(ticks []readers.RefactoringProxyTick) ([]time.Time, []float64) {
	timestamps := make([]time.Time, len(ticks))
	dateNumbers := make([]float64, len(ticks))

	for i, tick := range ticks {
		timestamps[i] = time.Unix(tick.Timestamp, 0).UTC()
		dateNumbers[i] = dates.Date2Num(timestamps[i])
	}

	return timestamps, dateNumbers
}
