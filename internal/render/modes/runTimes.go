package modes

import (
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cwbudde/matplotlib-go/backends"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// RunTimes generates runtime analysis. Python labours is text-only for this
// mode, so by default we only print the summary; the Go-only breakdown chart is
// gated behind detail (--run-times-detail).
func RunTimes(reader readers.Reader, output string, detail bool) error {
	opts := defaultOptions()
	opts.RunTimesDetail = detail

	return RunTimesWithOptions(reader, output, opts)
}

func RunTimesWithOptions(reader readers.Reader, output string, opts Options) error {
	quiet := opts.Quiet
	detail := opts.RunTimesDetail
	progEstimator := progress.NewProgressEstimator(!quiet)

	totalPhases := 3 // data extraction, analysis, plotting
	progEstimator.StartMultiOperation(totalPhases, "Runtime Analysis")

	// Phase 1: Extract runtime data
	progEstimator.NextOperation("Extracting runtime statistics")

	runtimeStats, err := reader.GetRuntimeStats()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to get runtime stats: %w", err)
	}

	if len(runtimeStats) == 0 {
		progEstimator.FinishMultiOperation()

		if !quiet {
			fmt.Println("No runtime data available")
		}

		return nil
	}

	// Phase 2: Analyze runtime patterns
	progEstimator.NextOperation("Analyzing runtime patterns")

	runtimeAnalysis := analyzeRuntimeStats(runtimeStats)

	// Phase 3: Generate visualizations (detail only) and print the summary.
	progEstimator.NextOperation("Generating visualization")

	if detail {
		err := plotRuntimeBreakdown(runtimeAnalysis, runtimeOutputPath(output), opts.Graphics)
		if err != nil {
			progEstimator.FinishMultiOperation()
			return fmt.Errorf("failed to generate runtime plots: %w", err)
		}
	}

	printRuntimeSummary(runtimeAnalysis)

	progEstimator.FinishMultiOperation()

	if !quiet {
		if !detail {
			fmt.Println("Runtime breakdown chart skipped (pass --run-times-detail to render it).")
		}

		fmt.Println("Runtime analysis completed successfully.")
	}

	return nil
}

func runtimeOutputPath(output string) string {
	if output == "" {
		return "run-times.png"
	}

	return output
}

// RuntimeMetric represents a single runtime measurement.
type RuntimeMetric struct {
	Operation  string
	TimeMs     float64
	Percentage float64
}

// RuntimeAnalysis represents the complete runtime analysis results.
type RuntimeAnalysis struct {
	Metrics    []RuntimeMetric
	TotalTime  float64
	Statistics RuntimeStatistics
}

// RuntimeStatistics provides summary statistics about runtime performance.
type RuntimeStatistics struct {
	TotalOperations int
	TotalTimeMs     float64
	AverageTime     float64
	MaxTime         float64
	MinTime         float64
	SlowestOp       string
	FastestOp       string
}

// analyzeRuntimeStats performs analysis on runtime statistics.
func analyzeRuntimeStats(runtimeStats map[string]float64) RuntimeAnalysis {
	var metrics []RuntimeMetric
	totalTime := 0.0
	maxTime := 0.0
	minTime := float64(^uint(0) >> 1) // Max float
	slowestOp := ""
	fastestOp := ""

	// Calculate total time first
	for _, time := range runtimeStats {
		totalTime += time
	}

	// Create metrics with percentages
	for operation, time := range runtimeStats {
		percentage := 0.0
		if totalTime > 0 {
			percentage = (time / totalTime) * 100
		}

		metrics = append(metrics, RuntimeMetric{
			Operation:  operation,
			TimeMs:     time,
			Percentage: percentage,
		})

		// Track min/max
		if time > maxTime {
			maxTime = time
			slowestOp = operation
		}

		if time < minTime {
			minTime = time
			fastestOp = operation
		}
	}

	// Sort by time (descending)
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].TimeMs > metrics[j].TimeMs
	})

	// Calculate average
	avgTime := 0.0
	if len(metrics) > 0 {
		avgTime = totalTime / float64(len(metrics))
	}

	return RuntimeAnalysis{
		Metrics:   metrics,
		TotalTime: totalTime,
		Statistics: RuntimeStatistics{
			TotalOperations: len(metrics),
			TotalTimeMs:     totalTime,
			AverageTime:     avgTime,
			MaxTime:         maxTime,
			MinTime:         minTime,
			SlowestOp:       slowestOp,
			FastestOp:       fastestOp,
		},
	}
}

func printRuntimeSummary(analysis RuntimeAnalysis) {
	fmt.Printf("Runtime Analysis Summary:\n")
	fmt.Printf("  Total operations: %d\n", analysis.Statistics.TotalOperations)
	fmt.Printf("  Total runtime: %.2f ms\n", analysis.Statistics.TotalTimeMs)
	fmt.Printf("  Average runtime per operation: %.2f ms\n", analysis.Statistics.AverageTime)
	fmt.Printf("  Slowest operation: %s (%.2f ms)\n", analysis.Statistics.SlowestOp, analysis.Statistics.MaxTime)
	fmt.Printf("  Fastest operation: %s (%.2f ms)\n", analysis.Statistics.FastestOp, analysis.Statistics.MinTime)
}

// plotRuntimeBreakdown creates a bar chart showing runtime for each operation.
func plotRuntimeBreakdown(analysis RuntimeAnalysis, output string, visuals graphics.Options) error {
	if len(analysis.Metrics) == 0 {
		return errors.New("no runtime metrics available")
	}

	// Prepare data for bar chart (show top 15 operations)
	maxOps := min(len(analysis.Metrics), 15)

	labels := make([]string, maxOps)

	values := make([]float64, maxOps)
	for i := range maxOps {
		labels[i] = compactRuntimeLabel(analysis.Metrics[i].Operation, 12)
		values[i] = analysis.Metrics[i].TimeMs
	}

	xMargin := 0.05 * (float64(maxOps) - 0.2)

	barColor := color.RGBA{R: 84, G: 162, B: 75, A: 255}

	err := graphics.PlotBarChartMatplotlib(labels, values, graphics.MatplotlibBarOptions{
		Title:        "Runtime Analysis Breakdown",
		XLabel:       "Operations (by time)",
		YLabel:       "Time",
		Output:       output,
		WidthInches:  15.36,
		HeightInches: 7.68,
		RotateX:      true,
		Color:        barColor,
		DisableGrid:  true,
		Opaque:       true,
		DefaultStyle: true,
		ManualXLim:   true,
		XMin:         -0.4 - xMargin,
		XMax:         float64(maxOps) - 0.6 + xMargin,
		FontSize:     visuals.PlotFontSize(),
	})
	if err != nil {
		return fmt.Errorf("failed to save runtime breakdown plot: %w", err)
	}

	fmt.Printf("Saved runtime breakdown plot to %s\n", output)

	return nil
}

func compactRuntimeLabel(label string, limit int) string {
	if len(label) <= limit {
		return label
	}

	return "..." + label[len(label)-(limit-3):]
}

// plotRuntimePieChart creates a pie chart showing percentage breakdown of runtime.
// Currently unreferenced — kept as scaffolding for a future `--run-times-detail` flag.
//
//nolint:unused
func plotRuntimePieChart(analysis RuntimeAnalysis, output string) error {
	if len(analysis.Metrics) == 0 {
		return errors.New("no runtime metrics available")
	}

	// Prepare data for stacked representation (top 10 operations)
	maxOps := min(len(analysis.Metrics), 10)

	labels := make([]string, maxOps)

	values := make([]float64, maxOps)
	for i := range maxOps {
		labels[i] = fmt.Sprintf("%s (%.1f%%)", compactRuntimeLabel(analysis.Metrics[i].Operation, 18), analysis.Metrics[i].Percentage)
		values[i] = analysis.Metrics[i].Percentage
	}

	pngFile := filepath.Join(output, "runtime_percentage.png")

	err := plotRuntimePercentageMatplotlib(labels, values, pngFile)
	if err != nil {
		return fmt.Errorf("failed to save runtime percentage PNG plot: %w", err)
	}

	svgFile := filepath.Join(output, "runtime_percentage.svg")

	err = plotRuntimePercentageMatplotlib(labels, values, svgFile)
	if err != nil {
		return fmt.Errorf("failed to save runtime percentage SVG plot: %w", err)
	}

	fmt.Printf("Saved runtime percentage plots to %s and %s\n", pngFile, svgFile)

	// Print summary information
	fmt.Printf("Runtime Analysis Summary:\n")
	fmt.Printf("  Total operations: %d\n", analysis.Statistics.TotalOperations)
	fmt.Printf("  Total runtime: %.2f ms\n", analysis.Statistics.TotalTimeMs)
	fmt.Printf("  Average runtime per operation: %.2f ms\n", analysis.Statistics.AverageTime)
	fmt.Printf("  Slowest operation: %s (%.2f ms)\n", analysis.Statistics.SlowestOp, analysis.Statistics.MaxTime)
	fmt.Printf("  Fastest operation: %s (%.2f ms)\n", analysis.Statistics.FastestOp, analysis.Statistics.MinTime)

	return nil
}

func plotRuntimePercentageMatplotlib(labels []string, values []float64, output string) error {
	width, height := 1536, 960
	fig := core.NewFigure(
		width,
		height,
		style.WithTheme(style.ThemeDefault),
		style.WithFont(graphics.PythonPlotFontFamily, graphics.PythonPlotFontSize()),
		style.WithTextColor(0, 0, 0, 1),
		style.WithBackground(1, 1, 1, 1),
		style.WithAxesBackground(render.Color{R: 1, G: 1, B: 1, A: 1}),
		style.WithAxesEdgeColor(render.Color{R: 0, G: 0, B: 0, A: 1}),
	)

	grid := fig.Subplots(1, 1, core.WithSubplotPadding(0.149, 0.991, 0.058, 0.964))
	if len(grid) == 0 || len(grid[0]) == 0 || grid[0][0] == nil {
		return errors.New("failed to create runtime percentage axes")
	}

	ax := grid[0][0]
	ax.SetTitle("Runtime Percentage Distribution")
	ax.SetXLabel("Cumulative Percentage")
	ax.SetYLabel("Operations")

	y := make([]float64, len(values))
	ticks := make([]float64, len(values))
	maxValue := 0.0

	for i, value := range values {
		y[i] = float64(i)
		ticks[i] = float64(i)

		if value > maxValue {
			maxValue = value
		}
	}

	orientation := core.BarHorizontal
	barHeight := 0.8
	barColor := renderColor(color.RGBA{R: 228, G: 87, B: 86, A: 255})

	_, err := ax.Bar(y, values, core.BarOptions{
		Color:       optional.Of(barColor),
		Width:       optional.Of(barHeight),
		Orientation: optional.Of(orientation),
	})
	if err != nil {
		return fmt.Errorf("failed to plot runtime percentage bars: %w", err)
	}

	ax.SetXLim(0, maxValue*1.05)
	ax.SetYLim(-0.89, float64(len(values))-0.11)
	ax.XAxis.Locator = ticker.FixedLocator{TicksList: []float64{0, 20, 40, 60, 80}}
	ax.YAxis.Locator = ticker.FixedLocator{TicksList: ticks}
	ax.YAxis.Formatter = ticker.FixedFormatter{Labels: append([]string(nil), labels...)}

	return saveRuntimeFigure(fig, output, width, height)
}

func saveRuntimeFigure(fig *core.Figure, output string, width, height int) error {
	config := backends.Config{
		Width:       width,
		Height:      height,
		Background:  render.Color{R: 1, G: 1, B: 1, A: 1},
		DPI:         96,
		Transparent: false,
	}

	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %w", err)
		}

		return core.SaveSVG(fig, renderer, output)
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %w", err)
		}

		return core.SavePNG(fig, renderer, output)
	}
}
