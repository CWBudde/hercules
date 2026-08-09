package modes

import (
	"errors"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
)

var errNoProjectBurndownData = errors.New("no burndown data available for project")

// BurndownProject generates a burndown chart for the entire project.
func BurndownProject(
	reader readers.Reader,
	output string,
	relative bool,
	startTime, endTime *time.Time,
	resample string,
) error {
	repoName, burndownMatrix := reader.GetProjectBurndown()
	if len(burndownMatrix) == 0 {
		return errNoProjectBurndownData
	}

	// Generate plot
	return generateBurndownPlot(repoName, burndownMatrix, output, relative, startTime, endTime, resample)
}
