package modes

import (
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
		return fmt.Errorf("empty burndown matrix")
	}

	// Initialize progress tracking
	quiet := opts.Quiet
	progEstimator := progress.NewProgressEstimator(!quiet)

	// Start multi-phase operation
	totalPhases := 4 // validation, resampling, interpolation, plotting
	progEstimator.StartMultiOperation(totalPhases, "Burndown Analysis")

	// Phase 1: Validation and setup
	progEstimator.NextOperation("Validating output path")
	if output == "" {
		output = fmt.Sprintf("burndown_%s.png", name)
		if !quiet {
			fmt.Printf("Output not provided, using default: %s\n", output)
		}
	}

	outputDir := filepath.Dir(output)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to create output directory %s: %v", outputDir, err)
	}

	// Phase 2: Resampling setup
	progEstimator.NextOperation("Setting up resampling")
	if opts.Resample == "" {
		opts.Resample = "year"
	}
	if !quiet {
		fmt.Printf("resampling to %s, please wait...\n", opts.Resample)
	}

	// Use default endTime if not provided
	if endTime == nil {
		now := time.Now()
		endTime = &now
	}

	// Use earliest time in the matrix if startTime is not provided
	if startTime == nil {
		tickSize := time.Duration(365*24) * time.Hour // Assuming yearly granularity by default
		switch opts.Resample {
		case "month":
			tickSize = time.Duration(30*24) * time.Hour
		case "day":
			tickSize = 24 * time.Hour
		}
		earliest := findEarliestTime(matrix, tickSize, *endTime)
		startTime = &earliest
	}

	// Phase 3: Interpolation with enhanced progress tracking
	progEstimator.NextOperation("Interpolating burndown data")
	survival, err := burndown.FitKaplanMeier(matrix)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("calculate survival: %w", err)
	}
	interpolatedMatrix, dateRange := interpolateBurndownMatrixWithProgress(matrix, *startTime, *endTime, opts.Resample, progEstimator)

	// Phase 4: Final processing and visualization
	progEstimator.NextOperation("Generating visualization")

	if !quiet {
		if err := burndown.WriteSurvivalFunction(os.Stdout, survival, 1); err != nil {
			progEstimator.FinishMultiOperation()
			return err
		}
	}

	// Normalize if relative is true
	if opts.Relative {
		interpolatedMatrix = normalizeMatrix(interpolatedMatrix)
	}

	// Create plot
	if err := graphics.PlotStackedBurndownMatplotlibWithOptions(interpolatedMatrix, dateRange, output, opts.Relative, opts.Graphics); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("error creating burndown plot: %v", err)
	}

	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Printf("Chart saved to %s\n", output)
	}
	return nil
}

// resampleDateRange creates a date range based on the given resampling interval.
func resampleDateRange(start, end time.Time, resample string) []time.Time {
	var dates []time.Time

	switch resample {
	case "year":
		// Yearly samples - start of each year
		for year := start.Year(); year <= end.Year(); year++ {
			yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, start.Location())
			if yearStart.After(end) {
				break
			}
			if yearStart.After(start) || yearStart.Equal(start) {
				dates = append(dates, yearStart)
			}
		}

	case "month", "M":
		// Monthly samples - start of each month
		current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		if current.Before(start) {
			current = current.AddDate(0, 1, 0)
		}

		for current.Before(end) || current.Equal(end) {
			dates = append(dates, current)
			current = current.AddDate(0, 1, 0)
		}

	case "week", "W":
		// Weekly samples - start of each week (Monday)
		// Find the first Monday on or after start
		current := start
		for current.Weekday() != time.Monday {
			current = current.AddDate(0, 0, 1)
		}

		for current.Before(end) || current.Equal(end) {
			dates = append(dates, current)
			current = current.AddDate(0, 0, 7)
		}

	case "day", "D":
		// Daily samples
		for current := start; current.Before(end) || current.Equal(end); current = current.AddDate(0, 0, 1) {
			dates = append(dates, current)
		}

	default:
		// Default to daily sampling
		for current := start; current.Before(end) || current.Equal(end); current = current.AddDate(0, 0, 1) {
			dates = append(dates, current)
		}
	}

	// Ensure we have at least two points for interpolation
	if len(dates) == 0 {
		dates = append(dates, start, end)
	} else if len(dates) == 1 {
		if !dates[0].Equal(end) {
			dates = append(dates, end)
		}
	}

	return dates
}

// interpolateBurndownMatrixWithProgress interpolates and resamples the matrix with progress tracking
func interpolateBurndownMatrixWithProgress(matrix [][]int, startTime, endTime time.Time, resample string, progEstimator *progress.ProgressEstimator) ([][]float64, []time.Time) {
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return [][]float64{}, []time.Time{}
	}

	numBands := len(matrix)
	originalTicks := len(matrix[0])

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
	for band := 0; band < numBands; band++ {
		progEstimator.UpdateProgress(1)

		// If target resolution matches original, direct copy
		if targetTicks == originalTicks {
			for tick := 0; tick < originalTicks; tick++ {
				interpolated[band][tick] = float64(matrix[band][tick])
			}
			continue
		}

		// Interpolate between original data points
		for targetTick := 0; targetTick < targetTicks; targetTick++ {
			// Map target tick to original tick space
			originalPos := float64(targetTick) * float64(originalTicks-1) / float64(targetTicks-1)

			// Find surrounding original ticks
			leftTick := int(originalPos)
			rightTick := leftTick + 1

			// Handle boundary cases
			if leftTick >= originalTicks-1 {
				interpolated[band][targetTick] = float64(matrix[band][originalTicks-1])
				continue
			}
			if rightTick >= originalTicks {
				interpolated[band][targetTick] = float64(matrix[band][leftTick])
				continue
			}

			// Linear interpolation
			fraction := originalPos - float64(leftTick)
			leftValue := float64(matrix[band][leftTick])
			rightValue := float64(matrix[band][rightTick])

			interpolated[band][targetTick] = leftValue + fraction*(rightValue-leftValue)
		}
	}

	progEstimator.FinishOperation()
	return interpolated, dateRange
}

// normalizeMatrix normalizes each column to sum to 1.
func normalizeMatrix(matrix [][]float64) [][]float64 {
	for j := 0; j < len(matrix[0]); j++ {
		sum := 0.0
		for i := 0; i < len(matrix); i++ {
			sum += matrix[i][j]
		}
		if sum == 0 {
			continue
		}
		for i := 0; i < len(matrix); i++ {
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
