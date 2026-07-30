package modes

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/outputpath"
	"github.com/cwbudde/hercules/internal/render/readers"
)

var (
	errEmptyPersonBurndown    = errors.New("empty person burndown data")
	errPersonBurndownShape    = errors.New("person burndown row length mismatch")
	errNoPersonActivity       = errors.New("person burndown has no contributor activity")
	errEmptyPersonBurndownRaw = errors.New("empty raw person burndown data")
)

// BurndownPerson generates burndown charts for individual people/developers.
func BurndownPerson(reader readers.Reader, output string, relative bool, startDate, endDate *time.Time, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample

	return BurndownPersonWithOptions(reader, output, startDate, endDate, opts)
}

func BurndownPersonWithOptions(reader readers.Reader, output string, startDate, endDate *time.Time, opts Options) error {
	peopleBurndowns, err := reader.GetPeopleBurndown()
	if err != nil {
		return fmt.Errorf("failed to get people burndown data: %w", err)
	}

	header, usePythonRenderer, err := personBurndownHeader(reader, opts.Quiet)
	if err != nil {
		return err
	}

	if opts.Resample == "" {
		opts.Resample = "year"
	}

	displayNames, outputFiles, err := personBurndownOutputPaths(peopleBurndowns, output)
	if err != nil {
		return err
	}

	rendered := 0

	for index, person := range peopleBurndowns {
		err := renderPersonBurndown(
			person, displayNames[index], outputFiles[index], header,
			usePythonRenderer, startDate, endDate, opts,
		)
		// A contributor with no alive lines in the analysed range has nothing to
		// plot. That is ordinary — a people-dict routinely covers more people
		// than any single repository set does — so skip them instead of failing
		// the whole fan-out and losing every other person's chart too.
		if errors.Is(err, errNoPersonActivity) {
			if !opts.Quiet {
				fmt.Fprintf(
					os.Stderr,
					"Skipping person burndown for %s: no contributor activity in range\n",
					displayNames[index],
				)
			}

			continue
		}

		if err != nil {
			return err
		}

		rendered++
	}

	if rendered == 0 && len(peopleBurndowns) > 0 {
		return errNoPersonActivity
	}

	return nil
}

func personBurndownHeader(
	reader readers.Reader,
	quiet bool,
) (burndown.BurndownHeader, bool, error) {
	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil && !errors.Is(err, readers.ErrAnalysisMissing) {
		return burndown.BurndownHeader{}, false, fmt.Errorf("get burndown header: %w", err)
	}

	usePythonRenderer := err == nil
	if !usePythonRenderer && !quiet {
		fmt.Fprintf(os.Stderr, "Warning: falling back to legacy person burndown renderer: %v\n", err)
	}

	return header, usePythonRenderer, nil
}

func personBurndownOutputPaths(
	people []readers.PeopleBurndown,
	output string,
) ([]string, []string, error) {
	identities := make([]string, len(people))

	displayNames := make([]string, len(people))
	for index, person := range people {
		identities[index] = person.Person
		displayNames[index] = peopleChartLabel(person.Person)
	}

	outputFiles, err := outputpath.FanoutLabeledPaths(
		output, "burndown_person", displayNames, identities,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("plan person burndown outputs: %w", err)
	}

	return displayNames, outputFiles, nil
}

func renderPersonBurndown(
	person readers.PeopleBurndown,
	displayName, outputFile string,
	header burndown.BurndownHeader,
	usePythonRenderer bool,
	startDate, endDate *time.Time,
	opts Options,
) error {
	if !usePythonRenderer {
		return renderLegacyPersonBurndown(person, displayName, outputFile, startDate, endDate, opts)
	}

	processedData, err := burndown.LoadBurndown(
		header, displayName, person.Matrix, opts.Resample, false, false,
	)
	if err != nil {
		return fmt.Errorf("failed to process burndown for person %s: %w", person.Person, err)
	}

	if err := compactPersonBurndown(processedData); err != nil {
		return fmt.Errorf("compact burndown for person %s: %w", person.Person, err)
	}

	if err := graphics.PlotBurndownMatplotlibWithOptions(
		processedData, outputFile, opts.Relative, opts.Graphics,
	); err != nil {
		return fmt.Errorf("failed to generate burndown for person %s: %w", person.Person, err)
	}

	return nil
}

func renderLegacyPersonBurndown(
	person readers.PeopleBurndown,
	displayName, outputFile string,
	startDate, endDate *time.Time,
	opts Options,
) error {
	compactedMatrix, err := compactRawPersonBurndown(person.Matrix)
	if err != nil {
		return fmt.Errorf("compact burndown for person %s: %w", person.Person, err)
	}

	if err := generateBurndownPlotWithOptions(
		displayName, compactedMatrix, outputFile, startDate, endDate, opts,
	); err != nil {
		return fmt.Errorf("failed to generate burndown for person %s: %w", person.Person, err)
	}

	return nil
}

func compactPersonBurndown(data *burndown.ProcessedBurndown) error {
	if data == nil {
		return errEmptyPersonBurndown
	}

	matrix, dates, labels, err := compactPersonBurndownData(
		data.Matrix, data.DateRange, data.Labels,
	)
	if err != nil {
		return err
	}

	data.Matrix, data.DateRange, data.Labels = matrix, dates, labels

	return nil
}

func compactPersonBurndownData(
	matrix [][]float64,
	dates []time.Time,
	labels []string,
) ([][]float64, []time.Time, []string, error) {
	if len(matrix) == 0 || len(dates) == 0 {
		return nil, nil, nil, errEmptyPersonBurndown
	}

	activeRows, firstActive, lastActive, err := personBurndownActivity(
		matrix, len(dates),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	firstActive, lastActive = expandSinglePersonPoint(
		firstActive, lastActive, len(dates),
	)
	compacted := make([][]float64, len(activeRows))
	compactedLabels := make([]string, len(activeRows))

	for outputIndex, sourceIndex := range activeRows {
		compacted[outputIndex] = append(
			[]float64(nil), matrix[sourceIndex][firstActive:lastActive+1]...,
		)
		compactedLabels[outputIndex] = personBurndownLabel(labels, sourceIndex)
	}

	compactedDates := append([]time.Time(nil), dates[firstActive:lastActive+1]...)

	return compacted, compactedDates, compactedLabels, nil
}

func personBurndownActivity(
	matrix [][]float64,
	pointCount int,
) ([]int, int, int, error) {
	activeRows := make([]int, 0, len(matrix))
	firstActive, lastActive := pointCount, -1

	for rowIndex, row := range matrix {
		if len(row) != pointCount {
			return nil, 0, 0, fmt.Errorf(
				"%w: row %d has %d points, want %d",
				errPersonBurndownShape, rowIndex, len(row), pointCount,
			)
		}

		rowFirst, rowLast, active := nonzeroFloatBounds(row)
		if !active {
			continue
		}

		activeRows = append(activeRows, rowIndex)
		firstActive = min(firstActive, rowFirst)
		lastActive = max(lastActive, rowLast)
	}

	if len(activeRows) == 0 {
		return nil, 0, 0, errNoPersonActivity
	}

	return activeRows, firstActive, lastActive, nil
}

func nonzeroFloatBounds(row []float64) (int, int, bool) {
	first, last := len(row), -1

	for point, value := range row {
		if value == 0 {
			continue
		}

		first = min(first, point)
		last = point
	}

	return first, last, last >= 0
}

func expandSinglePersonPoint(first, last, pointCount int) (int, int) {
	if first != last || pointCount <= 1 {
		return first, last
	}

	if first > 0 {
		return first - 1, last
	}

	return first, last + 1
}

func personBurndownLabel(labels []string, sourceIndex int) string {
	if sourceIndex < len(labels) {
		return labels[sourceIndex]
	}

	return fmt.Sprintf("Layer %d", sourceIndex)
}

func compactRawPersonBurndown(matrix [][]int) ([][]int, error) {
	if len(matrix) == 0 {
		return nil, errEmptyPersonBurndownRaw
	}

	compacted := make([][]int, 0, len(matrix))
	for _, row := range matrix {
		if intRowHasActivity(row) {
			compacted = append(compacted, row)
		}
	}

	if len(compacted) == 0 {
		return nil, errNoPersonActivity
	}

	return compacted, nil
}

func intRowHasActivity(row []int) bool {
	for _, value := range row {
		if value != 0 {
			return true
		}
	}

	return false
}
