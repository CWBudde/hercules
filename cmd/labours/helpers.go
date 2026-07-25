package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// parseFlexibleDate parses a date string into a time.Time object.
// Returns an error if the date cannot be parsed.
func parseFlexibleDate(dateStr string) (time.Time, error) {
	parsedDate, err := dateparse.ParseAny(dateStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date format: %v", err)
	}
	return parsedDate, nil
}

func parseDates() (startTime, endTime *time.Time, err error) {
	if startTimeStr := viper.GetString("start-date"); startTimeStr != "" {
		parsedStartTime, parseErr := parseFlexibleDate(startTimeStr)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse start date: %w", parseErr)
		}
		startTime = &parsedStartTime
	}

	if endTimeStr := viper.GetString("end-date"); endTimeStr != "" {
		parsedEndTime, parseErr := parseFlexibleDate(endTimeStr)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse end date: %w", parseErr)
		}
		endTime = &parsedEndTime
	}

	return startTime, endTime, nil
}

func validateDateRange(startTime, endTime *time.Time) error {
	if startTime != nil && endTime != nil && endTime.Before(*startTime) {
		return fmt.Errorf("end date must be after start date")
	}
	return nil
}

func detectAndReadInput(input, inputFormat string) (readers.Reader, error) {
	reader, err := render.LoadInput(input, inputFormat)
	if err != nil {
		return nil, fmt.Errorf("detect or read input: %w", err)
	}
	return reader, nil
}

func resolveModes() ([]string, error) {
	rawModes := append([]string{}, viper.GetStringSlice("modes")...)
	rawModes = append(rawModes, viper.GetStringSlice("mode")...)
	modes, err := render.ResolveModes(rawModes)
	if err != nil {
		return nil, err
	}
	return modes, nil
}

// isExecutable checks if a file exists and is executable
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&0o111 != 0
}

// isGitRepository checks if a directory is a git repository
func isGitRepository(path string) bool {
	gitDir := filepath.Join(path, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// mapModesToHerculesAnalyses maps labours-go modes to hercules analysis types
func mapModesToHerculesAnalyses(modes []string) []string {
	analysisMap := make(map[string]bool)

	for _, mode := range modes {
		switch {
		case strings.HasPrefix(mode, "burndown"):
			analysisMap["burndown"] = true
		case mode == "devs" || mode == "devs-efforts":
			analysisMap["devs"] = true
		case strings.HasPrefix(mode, "couples"):
			analysisMap["couples"] = true
		case mode == "ownership":
			analysisMap["file-history"] = true
		case mode == "overwrites-matrix":
			analysisMap["couples"] = true // overwrites uses couples data
		}
	}

	result := make([]string, 0, len(analysisMap))
	for analysis := range analysisMap {
		result = append(result, analysis)
	}

	// Default to burndown if no specific analyses found
	if len(result) == 0 {
		result = []string{"burndown"}
	}

	return result
}

// runHerculesAndVisualize runs hercules analysis and then visualizes with labours-go
func runHerculesAndVisualize(herculesPath, repoPath, analysis string) error {
	// Generate temporary file for hercules output
	outputFile := fmt.Sprintf("/tmp/hercules_%s.yaml", analysis)

	// Build hercules command
	var herculesFlags []string
	switch analysis {
	case "burndown":
		herculesFlags = []string{"--burndown", "--burndown-files", "--burndown-people"}
	case "devs":
		herculesFlags = []string{"--devs"}
	case "couples":
		herculesFlags = []string{"--couples"}
	case "file-history":
		herculesFlags = []string{"--file-history"}
	default:
		herculesFlags = []string{"--" + analysis}
	}

	// Add any additional user-specified flags
	if userFlags := viper.GetString("hercules-flags"); userFlags != "" {
		herculesFlags = append(herculesFlags, strings.Fields(userFlags)...)
	}

	// Add repository path
	herculesFlags = append(herculesFlags, repoPath)

	fmt.Printf("Running hercules %s analysis...\n", analysis)

	// Execute hercules
	cmd := exec.Command(herculesPath, herculesFlags...) // #nosec G204 - user-configured Hercules executable is the purpose of this helper.
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("hercules command failed: %v", err)
	}

	// Write output to temporary file
	if err := os.WriteFile(outputFile, output, 0o600); err != nil {
		return fmt.Errorf("failed to write hercules output: %v", err)
	}

	fmt.Printf("Hercules analysis complete, creating visualizations...\n")

	// Determine labours-go modes for this analysis
	var laboursGoModes []string
	switch analysis {
	case "burndown":
		laboursGoModes = []string{"burndown-project"}
	case "devs":
		laboursGoModes = []string{"devs"}
	case "couples":
		laboursGoModes = []string{"couples-files"}
	case "file-history":
		laboursGoModes = []string{"ownership"}
	}

	// Run visualization for each mode
	for _, mode := range laboursGoModes {
		outputPath := viper.GetString("output")
		var format string

		if outputPath == "" {
			// Default to centralized analysis_results directory
			if err := os.MkdirAll("analysis_results", 0o750); err != nil {
				return fmt.Errorf("failed to create analysis_results directory: %v", err)
			}
			format = render.DetectOutputFormat("") // Will use backend flag or default to PNG
			basePath := fmt.Sprintf("analysis_results/%s_%s", analysis, mode)
			outputPath = render.GenerateOutputPath(basePath, format)
		} else {
			// If output is a directory, create filename
			if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
				format = render.DetectOutputFormat("") // Will use backend flag or default to PNG
				basePath := filepath.Join(outputPath, fmt.Sprintf("%s_%s", analysis, mode))
				outputPath = render.GenerateOutputPath(basePath, format)
			} else {
				// outputPath is a file, detect format from it
				format = render.DetectOutputFormat(outputPath)
				outputPath = render.GenerateOutputPath(outputPath, format)
			}
		}

		fmt.Printf("Creating %s visualization...\n", mode)

		// Read the hercules output and create visualization
		reader, err := detectAndReadInput(outputFile, "yaml")
		if err != nil {
			return err
		}
		startDate, endDate, err := parseDates()
		if err != nil {
			return err
		}

		result := render.Run(reader, []string{mode}, render.Options{
			Output:    outputPath,
			StartTime: startDate,
			EndTime:   endDate,
		})
		if err := result.Err(); err != nil {
			return err
		}

		fmt.Printf("Saved: %s\n", outputPath)
	}

	// Clean up temporary file
	if err := os.Remove(outputFile); err != nil {
		return fmt.Errorf("failed to remove temporary output file: %v", err)
	}

	return nil
}
