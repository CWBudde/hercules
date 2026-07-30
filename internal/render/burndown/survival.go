package burndown

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ErrInvalidSurvivalMatrix indicates that survival input is ragged or
// contains values which cannot represent line counts.
var ErrInvalidSurvivalMatrix = errors.New("invalid burndown survival matrix")

const survivalColumnTitle = "Ratio of survived lines"

// SurvivalPoint is one point of the Kaplan-Meier survival function. Duration
// is measured in burndown samples; callers multiply it by the analysis
// sampling interval when presenting the value as days.
type SurvivalPoint struct {
	Duration int
	Ratio    float64
}

type survivalObservation struct {
	duration int
	weight   float64
	observed bool
}

// FitKaplanMeier calculates the weighted Kaplan-Meier survival curve used by
// historical Python labours. The calculation intentionally operates on the
// raw age-band matrix: interpolation, resampling, and granularity do not alter
// line lifetimes.
//
// A decrease in a band is a weighted death. An increase records the entry time
// for that band. Lines still present in a live band at the last sample are
// right-censored. Empty matrices and matrices containing no observations have
// no survival curve and return an empty result.
func FitKaplanMeier(matrix [][]int) ([]SurvivalPoint, error) {
	columns, err := validateSurvivalMatrix(matrix)
	if err != nil {
		return nil, err
	}

	if columns == 0 {
		return nil, nil
	}

	observations, survivors := collectSurvivalObservations(matrix, columns)
	if len(observations) == 0 {
		return nil, nil
	}

	if survivors == 0 {
		for index := range observations {
			observations[index].observed = false
		}
	}

	return buildSurvivalCurve(observations), nil
}

func collectSurvivalObservations(matrix [][]int, columns int) ([]survivalObservation, int) {
	entries := make([]int, len(matrix))
	dead := make([]bool, len(matrix))

	observations := make([]survivalObservation, 0)
	for column := 1; column < columns; column++ {
		observations = collectSurvivalDeaths(matrix, column, entries, dead, observations)
	}

	survivors := 0

	for rowIndex, row := range matrix {
		entered := entries[rowIndex] != 0 || rowIndex == 0
		if entered && !dead[rowIndex] {
			observations = append(observations, survivalObservation{
				duration: columns - entries[rowIndex],
				weight:   float64(row[columns-1]),
				observed: false,
			})
			survivors++
		}
	}

	return observations, survivors
}

func collectSurvivalDeaths(
	matrix [][]int,
	column int,
	entries []int,
	dead []bool,
	observations []survivalObservation,
) []survivalObservation {
	for rowIndex, row := range matrix {
		difference := int64(row[column-1]) - int64(row[column])
		if difference < 0 {
			entries[rowIndex] = column
		} else if difference > 0 {
			observations = append(observations, survivalObservation{
				duration: column - entries[rowIndex],
				weight:   float64(difference),
				observed: true,
			})
		}
	}

	for rowIndex, row := range matrix {
		entered := entries[rowIndex] > 0 || rowIndex == 0
		if entered && row[column] == 0 {
			dead[rowIndex] = true
		}
	}

	return observations
}

func buildSurvivalCurve(observations []survivalObservation) []SurvivalPoint {
	totalWeight := 0.0
	timelineSet := map[int]struct{}{0: {}}
	removedByDuration := make(map[int]float64, len(observations))

	deathsByDuration := make(map[int]float64, len(observations))
	for _, observation := range observations {
		totalWeight += observation.weight
		timelineSet[observation.duration] = struct{}{}

		removedByDuration[observation.duration] += observation.weight
		if observation.observed {
			deathsByDuration[observation.duration] += observation.weight
		}
	}

	if totalWeight == 0 {
		return nil
	}

	timeline := make([]int, 0, len(timelineSet))
	for duration := range timelineSet {
		timeline = append(timeline, duration)
	}

	sort.Ints(timeline)

	curve := make([]SurvivalPoint, 0, len(timeline))
	atRisk := totalWeight
	ratio := 1.0

	for _, duration := range timeline {
		if duration > 0 && atRisk > 0 {
			ratio *= 1 - deathsByDuration[duration]/atRisk
		}

		curve = append(curve, SurvivalPoint{Duration: duration, Ratio: ratio})
		atRisk -= removedByDuration[duration]
	}

	return curve
}

func validateSurvivalMatrix(matrix [][]int) (int, error) {
	if len(matrix) == 0 {
		return 0, nil
	}

	columns := len(matrix[0])
	for rowIndex, row := range matrix {
		if len(row) != columns {
			return 0, fmt.Errorf(
				"%w: row %d has %d columns, want %d",
				ErrInvalidSurvivalMatrix, rowIndex, len(row), columns,
			)
		}

		for columnIndex, value := range row {
			if value < 0 {
				return 0, fmt.Errorf(
					"%w: negative line count at row %d, column %d",
					ErrInvalidSurvivalMatrix, rowIndex, columnIndex,
				)
			}
		}
	}

	return columns, nil
}

// WriteSurvivalFunction writes the same deterministic six-slice table as
// Python labours' print_survival_function(). Curves shorter than six rows are
// intentionally omitted, matching the historical slice-step behavior.
func WriteSurvivalFunction(writer io.Writer, curve []SurvivalPoint, sampling int) error {
	if writer == nil {
		return errors.New("write survival function: nil writer")
	}

	if sampling <= 0 {
		return fmt.Errorf("write survival function: invalid sampling %d", sampling)
	}

	step := len(curve) / 6
	if step == 0 {
		return nil
	}

	rows := make([]SurvivalPoint, 0, 7)
	for index := step; index < len(curve); index += step {
		rows = append(rows, curve[index])
	}
	// pandas.DataFrame.append(sf.tail(1)) retained the final row even when the
	// slice already contained it, so historical output includes that duplicate.
	rows = append(rows, curve[len(curve)-1])

	labels := make([]string, len(rows))
	indexWidth := 0

	for index, point := range rows {
		labels[index] = fmt.Sprintf("%d days", int64(point.Duration)*int64(sampling))
		if len(labels[index]) > indexWidth {
			indexWidth = len(labels[index])
		}
	}

	if _, err := fmt.Fprintf(writer, "%s%s\n", strings.Repeat(" ", indexWidth+2), survivalColumnTitle); err != nil {
		return fmt.Errorf("write survival header: %w", err)
	}

	for index, point := range rows {
		if _, err := fmt.Fprintf(writer, "%-*s  %23.6f\n", indexWidth, labels[index], point.Ratio); err != nil {
			return fmt.Errorf("write survival row: %w", err)
		}
	}

	return nil
}
