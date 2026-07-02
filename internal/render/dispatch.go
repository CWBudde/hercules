package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meko-christian/hercules/internal/render/modes"
	"github.com/meko-christian/hercules/internal/render/progress"
	"github.com/meko-christian/hercules/internal/render/readers"
	"github.com/spf13/viper"
)

// Fallback toggles set by Run from Options; they mirror the labours CLI
// --sentiment-fallback and --devs-parallel-fallback flags.
var (
	sentimentFallbackEnabled    bool
	devsParallelFallbackEnabled bool
)

// Map of mode names to their handlers
var modeHandlers = map[string]func(reader readers.Reader, output string, startTime, endTime *time.Time) error{
	"burndown-project":        burndownProject,
	"burndown-file":           burndownFile,
	"burndown-person":         burndownPerson,
	"burndown-repository":     burndownRepository,
	"burndown-repos-combined": burndownReposCombined,
	"overwrites-matrix":       overwritesMatrix,
	"ownership":               ownershipBurndown,
	"couples-files":           couplesFiles,
	"couples-people":          couplesPeople,
	"couples-shotness":        couplesShotness,
	"shotness":                shotness,
	"devs":                    devs,
	"devs-efforts":            devsEfforts,
	"old-vs-new":              oldVsNew,
	"languages":               languages,
	"temporal-activity":       temporalActivity,
	"devs-parallel":           devsParallel,
	"run-times":               runTimes,
	"bus-factor":              busFactor,
	"ownership-concentration": ownershipConcentration,
	"knowledge-diffusion":     knowledgeDiffusion,
	"hotspot-risk":            hotspotRisk,
	"sentiment":               sentiment,
	"refactoring-proxy":       refactoringProxy,
}

func executeModes(modes []string, reader readers.Reader, output string, startTime, endTime *time.Time) []ModeResult {
	if len(modes) == 0 {
		return nil
	}
	modeResults := make([]ModeResult, 0, len(modes))

	// Check if JSON output is requested
	jsonOutput := strings.HasSuffix(strings.ToLower(output), ".json")

	// Initialize progress tracking for multiple modes
	quiet := viper.GetBool("quiet")
	progEstimator := progress.NewProgressEstimator(!quiet)

	// If JSON output, collect all results and save as JSON
	if jsonOutput {
		results := make(map[string]interface{}, len(modes))

		if len(modes) > 1 {
			progEstimator.StartMultiOperation(len(modes), "Analysis Modes")
		}

		for _, mode := range modes {
			if len(modes) > 1 {
				progEstimator.NextOperation(fmt.Sprintf("Running %s", mode))
			}

			if !quiet {
				fmt.Printf("Running: %s\n", mode)
			}

			if _, ok := modeHandlers[mode]; !ok {
				printModeUnavailable(mode)
				modeResults = append(modeResults, ModeResult{Mode: mode, Warning: modeUnavailableMessage(mode)})
				results[mode] = map[string]interface{}{
					"error": "mode not implemented",
				}
				continue
			}

			data, err := extractModeDataForJSON(reader, mode)
			if err != nil {
				modeResults = append(modeResults, handleModeError(mode, err))
				results[mode] = map[string]interface{}{
					"error": err.Error(),
				}
				continue
			}
			modeResults = append(modeResults, ModeResult{Mode: mode})
			results[mode] = data
		}

		if len(modes) > 1 {
			progEstimator.FinishMultiOperation()
		}

		// Save results as JSON
		if err := saveJSONResults(results, output); err != nil {
			fmt.Printf("Error saving JSON results: %v\n", err)
		} else if !quiet {
			fmt.Printf("Results saved as JSON to: %s\n", output)
		}
		return modeResults
	} else {
		// Regular image output
		if len(modes) > 1 {
			// Start multi-mode progress tracking
			progEstimator.StartMultiOperation(len(modes), "Analysis Modes")

			for _, mode := range modes {
				progEstimator.NextOperation(fmt.Sprintf("Running %s", mode))

				if !quiet {
					fmt.Printf("Running: %s\n", mode)
				}

				modeResults = append(modeResults, runSingleMode(mode, reader, output, len(modes), startTime, endTime))
			}

			progEstimator.FinishMultiOperation()
		} else {
			// Single mode - let the individual mode handle its own progress
			for _, mode := range modes {
				if !quiet {
					fmt.Printf("Running: %s\n", mode)
				}

				modeResults = append(modeResults, runSingleMode(mode, reader, output, len(modes), startTime, endTime))
			}
		}
	}
	return modeResults
}

func runSingleMode(mode string, reader readers.Reader, output string, modeCount int, startTime, endTime *time.Time) ModeResult {
	modeFunc, ok := modeHandlers[mode]
	if !ok {
		printModeUnavailable(mode)
		return ModeResult{Mode: mode, Warning: modeUnavailableMessage(mode)}
	}
	formattedOutput := planModeOutput(output, mode, modeCount)
	if err := modeFunc(reader, formattedOutput, startTime, endTime); err != nil {
		return handleModeError(mode, err)
	}
	return ModeResult{Mode: mode}
}

func modeUnavailableMessage(mode string) string {
	if isValidMode(mode) {
		return "Mode not implemented yet: " + mode
	}
	return "Unknown mode: " + mode
}

func printModeUnavailable(mode string) {
	fmt.Println(modeUnavailableMessage(mode))
}

// handleModeError prints the outcome of a failed mode and classifies it:
// missing-analysis failures become Python-compatible warnings, everything
// else stays a hard error.
func handleModeError(mode string, err error) ModeResult {
	if warning, ok := missingAnalysisWarning(mode, err); ok {
		fmt.Println(warning)
		return ModeResult{Mode: mode, Warning: warning}
	}
	fmt.Printf("Error in mode %s: %v\n", mode, err)
	return ModeResult{Mode: mode, Err: err}
}

func missingAnalysisWarning(mode string, err error) (string, bool) {
	if !isMissingAnalysisError(err) {
		return "", false
	}

	burndownWarning := "Burndown stats were not collected. Re-run hercules with --burndown."
	burndownFilesWarning := "Burndown stats for files were not collected. Re-run hercules with --burndown --burndown-files."
	burndownPeopleWarning := "Burndown stats for people were not collected. Re-run hercules with --burndown --burndown-people."
	couplesWarning := "Coupling stats were not collected. Re-run hercules with --couples."
	shotnessWarning := "Structural hotness stats were not collected. Re-run hercules with --shotness. Also check --languages - the output may be empty."
	devsWarning := "Devs stats were not collected. Re-run hercules with --devs."

	switch mode {
	case "burndown-project":
		return "project: " + burndownWarning, true
	case "burndown-file":
		return "files: " + burndownFilesWarning, true
	case "burndown-person", "ownership", "overwrites-matrix":
		prefix := map[string]string{
			"burndown-person":   "people",
			"ownership":         "ownership",
			"overwrites-matrix": "overwrites_matrix",
		}[mode]
		return prefix + ": " + burndownPeopleWarning, true
	case "burndown-repository":
		return "repositories: burndown data not available or repositories not tracked", true
	case "burndown-repos-combined":
		return "repositories-combined: burndown data not available or repositories not tracked", true
	case "couples-files", "couples-people":
		return couplesWarning, true
	case "couples-shotness", "shotness":
		return shotnessWarning, true
	case "sentiment":
		return "Sentiment stats were not collected. Re-run hercules with --sentiment.", true
	case "devs-parallel":
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "people cooccurrence") {
			return couplesWarning, true
		}
		if strings.Contains(msg, "devs time series") {
			return devsWarning, true
		}
		return "devs-parallel: " + burndownPeopleWarning, true
	case "devs", "devs-efforts", "old-vs-new", "languages":
		return devsWarning, true
	case "temporal-activity":
		return "Temporal activity stats were not collected. Re-run hercules with --temporal-activity.", true
	case "bus-factor":
		return "Bus factor stats were not collected. Re-run hercules with --bus-factor.", true
	case "ownership-concentration":
		return "Ownership concentration stats were not collected. Re-run hercules with --ownership-concentration.", true
	case "knowledge-diffusion":
		return "Knowledge diffusion stats were not collected. Re-run hercules with --knowledge-diffusion.", true
	case "hotspot-risk":
		return "Hotspot risk scores were not collected. Re-run hercules with --hotspot-risk.", true
	case "refactoring-proxy":
		return "Refactoring proxy data was not collected. Re-run hercules with --refactoring-proxy.", true
	}

	return "", false
}

func isMissingAnalysisError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, readers.ErrAnalysisMissing) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "missing") ||
		strings.Contains(msg, "not collected") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no ") ||
		strings.Contains(msg, "does not expose")
}

func burndownProject(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	relative := viper.GetBool("relative")
	resample := viper.GetString("resample")
	// Use Python-compatible implementation
	return modes.GenerateBurndownProjectPython(reader, output, relative, resample)
}

func burndownFile(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	relative := viper.GetBool("relative")
	resample := viper.GetString("resample")
	// Use Python-compatible implementation
	return modes.GenerateBurndownFilePython(reader, output, relative, resample)
}

func burndownPerson(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	relative := viper.GetBool("relative")
	resample := viper.GetString("resample")
	return modes.BurndownPerson(reader, output, relative, startTime, endTime, resample)
}

func burndownRepository(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	relative := viper.GetBool("relative")
	resample := viper.GetString("resample")
	return modes.GenerateBurndownRepositoryPython(reader, output, relative, resample)
}

func burndownReposCombined(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	relative := viper.GetBool("relative")
	resample := viper.GetString("resample")
	maxRepos := viper.GetInt("max-repos")
	return modes.GenerateBurndownReposCombinedPython(reader, output, relative, resample, maxRepos)
}

func overwritesMatrix(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.OverwritesMatrix(reader, output)
}

func ownershipBurndown(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.OwnershipBurndown(reader, output)
}

func couplesFiles(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	// Note: --disable-projector flag is supported for Python compatibility but not used
	// Our Go implementation focuses on core coupling analysis without TensorFlow embeddings
	return modes.CouplesFiles(reader, output)
}

func couplesPeople(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.CouplesPeople(reader, output)
}

func couplesShotness(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.CouplesShotness(reader, output)
}

func shotness(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.Shotness(reader, output)
}

func devs(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	maxPeople := viper.GetInt("max-people")
	return modes.Devs(reader, output, maxPeople)
}

func devsEfforts(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	maxPeople := viper.GetInt("max-people")
	return modes.DevsEfforts(reader, output, maxPeople, viper.GetBool("devs-efforts-detail"))
}

func oldVsNew(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	resample := viper.GetString("resample")
	return modes.OldVsNew(reader, output, startTime, endTime, resample)
}

func languages(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.Languages(reader, output)
}

func temporalActivity(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	legendThreshold := viper.GetInt("temporal-legend-threshold")
	singleColumnThreshold := viper.GetInt("temporal-legend-single-col-threshold")
	return modes.TemporalActivity(reader, output, legendThreshold, singleColumnThreshold, startTime, endTime)
}

func devsParallel(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.DevsParallel(reader, output, viper.GetInt("max-people"), devsParallelFallbackEnabled, viper.GetBool("devs-parallel-detail"))
}

func runTimes(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.RunTimes(reader, output, viper.GetBool("run-times-detail"))
}

func busFactor(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.BusFactor(reader, output)
}

func ownershipConcentration(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.OwnershipConcentration(reader, output)
}

func knowledgeDiffusion(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.KnowledgeDiffusion(reader, output, viper.GetBool("knowledge-diffusion-detail"))
}

func hotspotRisk(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.HotspotRisk(reader, output)
}

func sentiment(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.Sentiment(reader, output, sentimentFallbackEnabled)
}

func refactoringProxy(reader readers.Reader, output string, _ *time.Time, _ *time.Time) error {
	return modes.RefactoringProxy(reader, output)
}

//nolint:unparam // Mode handlers share an error-returning signature.
func runAllModes(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	if !viper.GetBool("quiet") {
		fmt.Printf("Running 'all' mode: executing %d analysis modes\n", len(pythonAllModes))
	}

	for _, modeName := range pythonAllModes {
		if !viper.GetBool("quiet") {
			fmt.Printf("  Running %s...\n", modeName)
		}

		modeFunc, ok := modeHandlers[modeName]
		if !ok {
			printModeUnavailable(modeName)
			continue
		}
		modeOutput := planModeOutput(output, modeName, len(pythonAllModes))
		if err := modeFunc(reader, modeOutput, startTime, endTime); err != nil {
			handleModeError(modeName, err)
		}
	}

	return nil
}

// extractModeDataForJSON extracts raw reader data for JSON output without rendering plots.
func extractModeDataForJSON(reader readers.Reader, mode string) (interface{}, error) {
	switch mode {
	case "devs":
		stats, err := reader.GetDeveloperStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"developer_stats": stats}, nil
	case "devs-efforts", "old-vs-new", "devs-parallel":
		data, err := reader.GetDeveloperTimeSeriesData()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"developer_time_series": data}, nil
	case "burndown-project":
		header, name, matrix, err := reader.GetProjectBurndownWithHeader()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "project", "header": header, "name": name, "matrix": matrix}, nil
	case "burndown-file":
		files, err := reader.GetFilesBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "file", "files": files}, nil
	case "burndown-person":
		people, err := reader.GetPeopleBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "person", "people": people}, nil
	case "burndown-repository", "burndown-repos-combined":
		repoReader, ok := reader.(readers.RepositoryBurndownReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose repository burndown data")
		}
		repos, err := repoReader.GetRepositoriesBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "repository", "repositories": repos}, nil
	case "ownership":
		names, matrices, err := reader.GetOwnershipBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "ownership", "file_names": names, "matrices": matrices}, nil
	case "overwrites-matrix":
		people, matrix, err := reader.GetPeopleInteraction()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "overwrites_matrix", "people": people, "matrix": matrix}, nil
	case "couples-files":
		names, matrix, err := reader.GetFileCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"file_names": names, "coupling_matrix": matrix}, nil
	case "couples-people":
		names, matrix, err := reader.GetPeopleCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"people_names": names, "coupling_matrix": matrix}, nil
	case "couples-shotness":
		names, matrix, err := reader.GetShotnessCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"entity_names": names, "coupling_matrix": matrix}, nil
	case "shotness":
		records, err := reader.GetShotnessRecords()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"shotness_records": records}, nil
	case "run-times":
		stats, err := reader.GetRuntimeStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"runtime_stats": stats}, nil
	case "languages":
		stats, err := reader.GetLanguageStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"language_stats": stats}, nil
	case "sentiment":
		sentimentReader, ok := reader.(readers.SentimentReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose sentiment data")
		}
		data, err := sentimentReader.GetSentimentByTick()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"sentiment_by_tick": data}, nil
	case "temporal-activity":
		temporalReader, ok := reader.(readers.TemporalActivityReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose temporal activity data")
		}
		data, err := temporalReader.GetTemporalActivity()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"temporal_activity": data}, nil
	case "bus-factor":
		busFactorReader, ok := reader.(readers.BusFactorReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose bus factor data")
		}
		data, err := busFactorReader.GetBusFactor()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"bus_factor": data}, nil
	case "ownership-concentration":
		ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose ownership concentration data")
		}
		data, err := ownershipReader.GetOwnershipConcentration()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"ownership_concentration": data}, nil
	case "knowledge-diffusion":
		diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose knowledge diffusion data")
		}
		data, err := diffusionReader.GetKnowledgeDiffusion()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"knowledge_diffusion": data}, nil
	case "hotspot-risk":
		hotspotReader, ok := reader.(readers.HotspotRiskReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose hotspot risk data")
		}
		data, err := hotspotReader.GetHotspotRisk()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"hotspot_risk": data}, nil
	case "refactoring-proxy":
		refactoringReader, ok := reader.(readers.RefactoringProxyReader)
		if !ok {
			return nil, fmt.Errorf("reader does not expose refactoring proxy data")
		}
		data, err := refactoringReader.GetRefactoringProxy()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"refactoring_proxy": data}, nil
	}

	return nil, fmt.Errorf("JSON output is not implemented for mode %s", mode)
}

// saveJSONResults saves the analysis results as JSON
func saveJSONResults(results map[string]interface{}, outputPath string) error {
	file, err := os.Create(outputPath) // #nosec G304 - JSON output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %v", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // Pretty print

	// Add metadata
	output := map[string]interface{}{
		"meta": map[string]interface{}{
			"generated_by":   "labours-go",
			"generated_at":   time.Now().Format(time.RFC3339),
			"modes_executed": len(results),
		},
		"results": results,
	}

	return encoder.Encode(output)
}
