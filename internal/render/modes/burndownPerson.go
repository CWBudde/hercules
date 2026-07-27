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

	header, _, _, headerErr := reader.GetProjectBurndownWithHeader()
	usePythonRenderer := headerErr == nil
	if headerErr != nil && !errors.Is(headerErr, readers.ErrAnalysisMissing) {
		return fmt.Errorf("get burndown header: %w", headerErr)
	}
	if !usePythonRenderer && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "Warning: falling back to legacy person burndown renderer: %v\n", headerErr)
	}
	if opts.Resample == "" {
		opts.Resample = "year"
	}

	identities := make([]string, len(peopleBurndowns))
	for index, person := range peopleBurndowns {
		identities[index] = person.Person
	}
	outputFiles, err := outputpath.FanoutPaths(output, "burndown_person", identities)
	if err != nil {
		return fmt.Errorf("plan person burndown outputs: %w", err)
	}

	// Generate a chart for each person
	for index, person := range peopleBurndowns {
		outputFile := outputFiles[index]
		displayName := peopleChartLabel(person.Person)

		if usePythonRenderer {
			processedData, err := burndown.LoadBurndown(header, displayName, person.Matrix, opts.Resample, false, false)
			if err != nil {
				return fmt.Errorf("failed to process burndown for person %s: %w", person.Person, err)
			}
			if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, outputFile, opts.Relative, opts.Graphics); err != nil {
				return fmt.Errorf("failed to generate burndown for person %s: %w", person.Person, err)
			}

			continue
		}

		renderErr := generateBurndownPlotWithOptions(
			displayName, person.Matrix, outputFile, startDate, endDate, opts,
		)
		if renderErr != nil {
			return fmt.Errorf("failed to generate burndown for person %s: %w", person.Person, renderErr)
		}
	}

	return nil
}
