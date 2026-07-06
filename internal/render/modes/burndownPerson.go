package modes

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/spf13/viper"
)

// BurndownPerson generates burndown charts for individual people/developers.
func BurndownPerson(reader readers.Reader, output string, relative bool, startDate, endDate *time.Time, resample string) error {
	peopleBurndowns, err := reader.GetPeopleBurndown()
	if err != nil {
		return fmt.Errorf("failed to get people burndown data: %v", err)
	}

	header, _, _, headerErr := reader.GetProjectBurndownWithHeader()
	usePythonRenderer := headerErr == nil
	if !usePythonRenderer && !viper.GetBool("quiet") {
		fmt.Printf("Warning: falling back to legacy person burndown renderer: %v\n", headerErr)
	}
	if resample == "" {
		resample = "year"
	}

	// Generate a chart for each person
	for _, person := range peopleBurndowns {
		outputFile := fmt.Sprintf("burndown_person_%s.png", sanitizeFilename(person.Person))
		if output != "" {
			dir := filepath.Dir(output)
			ext := filepath.Ext(output)
			if ext == "" {
				ext = ".png"
			}
			base := filepath.Base(output)
			base = base[:len(base)-len(filepath.Ext(base))]
			outputFile = filepath.Join(dir, fmt.Sprintf("%s_%s%s", base, sanitizeFilename(person.Person), ext))
		}

		if usePythonRenderer {
			processedData, err := burndown.LoadBurndown(header, person.Person, person.Matrix, resample, false, false)
			if err != nil {
				return fmt.Errorf("failed to process burndown for person %s: %v", person.Person, err)
			}
			if err := graphics.PlotBurndownMatplotlib(processedData, outputFile, relative); err != nil {
				return fmt.Errorf("failed to generate burndown for person %s: %v", person.Person, err)
			}
		} else if err := generateBurndownPlot(person.Person, person.Matrix, outputFile, relative, startDate, endDate, resample); err != nil {
			return fmt.Errorf("failed to generate burndown for person %s: %v", person.Person, err)
		}
	}

	return nil
}
