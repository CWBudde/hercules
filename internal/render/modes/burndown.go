package modes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/progress"
)

// generateBurndownPlot creates the burndown plot with stacking, resampling, and survival ratio output.
func generateBurndownPlot(name string, matrix [][]int, output string, relative bool, startTime, endTime *time.Time, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample

	return generateBurndownPlotWithOptions(name, matrix, output, startTime, endTime, opts)
}

func generateBurndownPlotWithOptions(name string, matrix [][]int, output string, startTime, endTime *time.Time, opts Options) error {
	fmt.Println("Running: burndown-project")

	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return errors.New("empty burndown matrix")
	}

	// Initialize progress tracking
	quiet := opts.Quiet
	progEstimator := progress.NewProgressEstimator(!quiet)

	// Start multi-phase operation
	totalPhases := 4 // validation, resampling, interpolation, plotting
	progEstimator.StartMultiOperation(totalPhases, "Burndown Analysis")

	progEstimator.NextOperation("Validating output path")

	output, err := prepareBurndownOutput(name, output, quiet)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return err
	}

	progEstimator.NextOperation("Setting up resampling")

	opts.Resample = defaultBurndownResample(opts.Resample)

	printBurndownResampling(opts.Resample, quiet)
	start, end := burndownTimeRange(matrix, startTime, endTime, opts.Resample)

	progEstimator.NextOperation("Interpolating burndown data")

	survival, err := burndown.FitKaplanMeier(matrix)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("calculate survival: %w", err)
	}

	interpolatedMatrix, dateRange := interpolateBurndownMatrixWithProgress(
		matrix, start, end, opts.Resample, progEstimator,
	)
	progEstimator.NextOperation("Generating visualization")

	err = writeBurndownSurvival(survival, quiet)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return err
	}

	if opts.Relative {
		interpolatedMatrix = normalizeMatrix(interpolatedMatrix)
	}

	err = graphics.PlotStackedBurndownMatplotlibWithOptions(interpolatedMatrix, dateRange, output, opts.Relative, opts.Graphics)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("error creating burndown plot: %w", err)
	}

	progEstimator.FinishMultiOperation()
	printBurndownChartSaved(output, quiet)

	return nil
}

func printBurndownResampling(resample string, quiet bool) {
	if !quiet {
		fmt.Printf("resampling to %s, please wait...\n", resample)
	}
}

func printBurndownChartSaved(output string, quiet bool) {
	if !quiet {
		fmt.Printf("Chart saved to %s\n", output)
	}
}

func prepareBurndownOutput(name, output string, quiet bool) (string, error) {
	if output == "" {
		output = fmt.Sprintf("burndown_%s.png", name)
		if !quiet {
			fmt.Printf("Output not provided, using default: %s\n", output)
		}
	}

	outputDir := filepath.Dir(output)

	err := os.MkdirAll(outputDir, 0o750)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return output, nil
}

func burndownTimeRange(
	matrix [][]int,
	startTime, endTime *time.Time,
	resample string,
) (time.Time, time.Time) {
	end := time.Now()
	if endTime != nil {
		end = *endTime
	}

	if startTime != nil {
		return *startTime, end
	}

	tickSizes := map[string]time.Duration{
		resampleMonth: 30 * 24 * time.Hour,
		resampleDay:   24 * time.Hour,
	}

	tickSize := 365 * 24 * time.Hour
	if configured, ok := tickSizes[resample]; ok {
		tickSize = configured
	}

	return findEarliestTime(matrix, tickSize, end), end
}

func writeBurndownSurvival(survival []burndown.SurvivalPoint, quiet bool) error {
	if quiet {
		return nil
	}

	return burndown.WriteSurvivalFunction(os.Stdout, survival, 1)
}

// resampleDateRange creates a date range based on the given resampling interval.
func resampleDateRange(start, end time.Time, resample string) []time.Time {
	generators := map[string]func(time.Time, time.Time) []time.Time{
		resampleYear: yearlyDateRange,
		"month":      monthlyDateRange,
		"M":          monthlyDateRange,
		"week":       weeklyDateRange,
		"W":          weeklyDateRange,
		resampleDay:  dailyDateRange,
		"D":          dailyDateRange,
	}

	generate, ok := generators[resample]
	if !ok {
		generate = dailyDateRange
	}

	dates := generate(start, end)
	if len(dates) == 0 {
		dates = append(dates, start, end)
	} else if len(dates) == 1 {
		if !dates[0].Equal(end) {
			dates = append(dates, end)
		}
	}

	return dates
}

func yearlyDateRange(start, end time.Time) []time.Time {
	var dates []time.Time

	for year := start.Year(); year <= end.Year(); year++ {
		yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, start.Location())
		if !yearStart.Before(start) && !yearStart.After(end) {
			dates = append(dates, yearStart)
		}
	}

	return dates
}

func monthlyDateRange(start, end time.Time) []time.Time {
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	if current.Before(start) {
		current = current.AddDate(0, 1, 0)
	}

	var dates []time.Time
	for !current.After(end) {
		dates = append(dates, current)
		current = current.AddDate(0, 1, 0)
	}

	return dates
}

func weeklyDateRange(start, end time.Time) []time.Time {
	current := start
	for current.Weekday() != time.Monday {
		current = current.AddDate(0, 0, 1)
	}

	var dates []time.Time
	for !current.After(end) {
		dates = append(dates, current)
		current = current.AddDate(0, 0, 7)
	}

	return dates
}

func dailyDateRange(start, end time.Time) []time.Time {
	var dates []time.Time
	for current := start; !current.After(end); current = current.AddDate(0, 0, 1) {
		dates = append(dates, current)
	}

	return dates
}

// interpolateBurndownMatrixWithProgress interpolates and resamples the matrix with progress tracking.
func interpolateBurndownMatrixWithProgress(matrix [][]int, startTime, endTime time.Time, resample string, progEstimator *progress.ProgressEstimator) ([][]float64, []time.Time) {
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return [][]float64{}, []time.Time{}
	}

	numBands := len(matrix)

	// Generate the target date range based on resampling
	dateRange := resampleDateRange(startTime, endTime, resample)
	targetTicks := len(dateRange)

	// Create interpolated matrix
	interpolated := make([][]float64, numBands)
	for i := range interpolated {
		interpolated[i] = make([]float64, targetTicks)
	}

	// Start detailed progress tracking for interpolation
	progEstimator.StartOperation("Interpolating matrix bands", numBands)

	// Interpolate each band (developer/file/etc)
	for band := range numBands {
		progEstimator.UpdateProgress(1)

		interpolated[band] = interpolateBurndownBand(matrix[band], targetTicks)
	}

	progEstimator.FinishOperation()

	return interpolated, dateRange
}

func interpolateBurndownBand(values []int, targetTicks int) []float64 {
	result := make([]float64, targetTicks)
	if targetTicks == len(values) {
		for tick, value := range values {
			result[tick] = float64(value)
		}

		return result
	}

	for targetTick := range result {
		originalPosition := float64(targetTick) * float64(len(values)-1) / float64(targetTicks-1)
		leftTick := int(originalPosition)

		rightTick := leftTick + 1
		if leftTick >= len(values)-1 {
			result[targetTick] = float64(values[len(values)-1])
			continue
		}

		if rightTick >= len(values) {
			result[targetTick] = float64(values[leftTick])
			continue
		}

		fraction := originalPosition - float64(leftTick)
		result[targetTick] = float64(values[leftTick]) +
			fraction*float64(values[rightTick]-values[leftTick])
	}

	return result
}

// normalizeMatrix normalizes each column to sum to 1.
func normalizeMatrix(matrix [][]float64) [][]float64 {
	for j := range len(matrix[0]) {
		sum := 0.0
		for i := range matrix {
			sum += matrix[i][j]
		}

		if sum == 0 {
			continue
		}

		for i := range matrix {
			matrix[i][j] /= sum
		}
	}

	return matrix
}

// findEarliestTime determines the earliest non-zero time entry in the data matrix.
func findEarliestTime(matrix [][]int, tickSize time.Duration, endTime time.Time) time.Time {
	for rowIndex, row := range matrix {
		for colIndex, val := range row {
			if val > 0 {
				// Calculate the corresponding time for this column
				earliestTime := endTime.Add(-tickSize * time.Duration(len(row)-colIndex))
				fmt.Printf("Earliest time found at row %d, col %d: %s\n", rowIndex, colIndex, earliestTime)

				return earliestTime
			}
		}
	}

	return time.Unix(0, 0) // Fallback, should never hit if matrix has data
}
