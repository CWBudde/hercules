package modes

import (
	"fmt"
	"time"

	"github.com/meko-christian/hercules/internal/render/readers"
)

// BurndownFile generates burndown charts for individual files.
func BurndownFile(reader readers.Reader, output string, relative bool, startDate, endDate *time.Time, resample string) error {
	fileBurndowns, err := reader.GetFilesBurndown()
	if err != nil {
		return fmt.Errorf("failed to get files burndown data: %v", err)
	}

	// Generate a chart for each file
	for _, file := range fileBurndowns {
		outputFile := fmt.Sprintf("%s_%s.png", output, file.Filename)
		if err := generateBurndownPlot(file.Filename, file.Matrix, outputFile, relative, startDate, endDate, resample); err != nil {
			return fmt.Errorf("failed to generate burndown for file %s: %v", file.Filename, err)
		}
	}

	return nil
}
