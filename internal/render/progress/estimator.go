package progress

import (
	"fmt"
	"time"

	"github.com/schollz/progressbar/v3"
)

// ProgressEstimator provides estimation and tracking for long-running operations
type ProgressEstimator struct {
	enabled          bool
	currentBar       *progressbar.ProgressBar
	totalOperations  int
	currentOperation int
}

// NewProgressEstimator creates a new progress estimator
func NewProgressEstimator(enabled bool) *ProgressEstimator {
	return &ProgressEstimator{
		enabled: enabled,
	}
}

// OperationType represents different types of operations with different cost weights
type OperationType int

const (
	FileRead OperationType = iota
	DataParsing
	MatrixInterpolation
	ChartGeneration
	FileSave
	DataProcessing
)

// OperationWeights defines the relative computational cost of each operation type
var OperationWeights = map[OperationType]int{
	FileRead:            1,
	DataParsing:         2,
	MatrixInterpolation: 5,
	ChartGeneration:     3,
	FileSave:            1,
	DataProcessing:      2,
}

// EstimateFileReadSteps estimates progress steps for file reading based on file size
func (pe *ProgressEstimator) EstimateFileReadSteps(fileSizeBytes int64) int {
	// Estimate ~1MB per step for file reading
	const bytesPerStep = 1024 * 1024
	steps := int(fileSizeBytes/bytesPerStep) + 1
	if steps < 1 {
		steps = 1
	}
	return steps
}

// EstimateMatrixSteps estimates progress steps for matrix operations
func (pe *ProgressEstimator) EstimateMatrixSteps(rows, cols int) int {
	// Base estimation on matrix size - more complex matrices take longer
	totalElements := rows * cols

	switch {
	case totalElements < 1000:
		return 1
	case totalElements < 10000:
		return totalElements / 100
	default:
		return totalElements / 1000
	}
}

// EstimateProcessingSteps estimates steps for general data processing
func (pe *ProgressEstimator) EstimateProcessingSteps(dataSize int, complexity OperationType) int {
	baseSteps := dataSize / 100
	if baseSteps < 1 {
		baseSteps = 1
	}

	weight := OperationWeights[complexity]
	return baseSteps * weight
}

// StartOperation begins a new operation with progress tracking
func (pe *ProgressEstimator) StartOperation(operationName string, estimatedSteps int) {
	if !pe.enabled {
		return
	}

	pe.currentBar = progressbar.NewOptions(
		estimatedSteps,
		progressbar.OptionSetDescription(operationName),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
}

// UpdateProgress updates the current operation's progress
func (pe *ProgressEstimator) UpdateProgress(increment int) {
	if !pe.enabled || pe.currentBar == nil {
		return
	}
	_ = pe.currentBar.Add(increment)
}

// SetProgress sets the absolute progress value
func (pe *ProgressEstimator) SetProgress(current int) {
	if !pe.enabled || pe.currentBar == nil {
		return
	}
	_ = pe.currentBar.Set(current)
}

// FinishOperation completes the current operation
func (pe *ProgressEstimator) FinishOperation() {
	if !pe.enabled || pe.currentBar == nil {
		return
	}
	_ = pe.currentBar.Finish()
	pe.currentBar = nil
}

// StartMultiOperation begins tracking multiple operations
func (pe *ProgressEstimator) StartMultiOperation(totalOperations int, operationName string) {
	if !pe.enabled {
		return
	}

	pe.totalOperations = totalOperations
	pe.currentOperation = 0

	pe.currentBar = progressbar.NewOptions(
		totalOperations,
		progressbar.OptionSetDescription(operationName),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "█",
			SaucerHead:    "█",
			SaucerPadding: "░",
			BarStart:      "│",
			BarEnd:        "│",
		}),
	)
}

// NextOperation moves to the next operation in a multi-operation sequence
func (pe *ProgressEstimator) NextOperation(operationName string) {
	if !pe.enabled {
		return
	}

	pe.currentOperation++
	if pe.currentBar != nil {
		_ = pe.currentBar.Set(pe.currentOperation)
		// Update description to show current operation
		description := fmt.Sprintf("%s (%d/%d)", operationName, pe.currentOperation, pe.totalOperations)
		pe.currentBar.Describe(description)
	}
}

// FinishMultiOperation completes the multi-operation sequence
func (pe *ProgressEstimator) FinishMultiOperation() {
	if !pe.enabled || pe.currentBar == nil {
		return
	}
	_ = pe.currentBar.Finish()
	pe.currentBar = nil
	pe.totalOperations = 0
	pe.currentOperation = 0
}

// SimpleProgress creates a simple progress bar for quick operations
func (pe *ProgressEstimator) SimpleProgress(description string, total int) *progressbar.ProgressBar {
	if !pe.enabled {
		return progressbar.NewOptions(total, progressbar.OptionClearOnFinish())
	}

	return progressbar.NewOptions(
		total,
		progressbar.OptionSetDescription(description),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(true),
	)
}

// EstimateTimeBasedSteps estimates steps for time-based operations
func (pe *ProgressEstimator) EstimateTimeBasedSteps(startTime, endTime time.Time, resampleInterval string) int {
	duration := endTime.Sub(startTime)

	var stepDuration time.Duration
	switch resampleInterval {
	case "day", "D":
		stepDuration = 24 * time.Hour
	case "week", "W":
		stepDuration = 7 * 24 * time.Hour
	case "month", "M":
		stepDuration = 30 * 24 * time.Hour
	case "year":
		stepDuration = 365 * 24 * time.Hour
	default:
		stepDuration = 365 * 24 * time.Hour
	}

	steps := int(duration / stepDuration)
	if steps < 1 {
		steps = 1
	}
	return steps
}

// IsEnabled returns whether progress tracking is enabled
func (pe *ProgressEstimator) IsEnabled() bool {
	return pe.enabled
}
