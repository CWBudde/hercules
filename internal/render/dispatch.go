package render

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/modes"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// Map of mode names to their handlers
type modeHandler func(reader readers.Reader, output string, startTime, endTime *time.Time, opts modes.Options) error

var modeHandlers = map[string]modeHandler{
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

func executeModes(modeNames []string, reader readers.Reader, output string, startTime, endTime *time.Time) Result {
	opts := DefaultOptions()
	opts.Output, opts.StartTime, opts.EndTime = output, startTime, endTime
	renderer, err := NewRenderer(opts)
	if err != nil {
		return Result{OutputError: err}
	}
	return renderer.executeModes(modeNames, reader)
}

func (r *Renderer) executeModes(modeNames []string, reader readers.Reader) Result {
	output := r.options.Output
	startTime, endTime := r.options.StartTime, r.options.EndTime
	modesConfig := r.modeOptions()
	if len(modeNames) == 0 {
		return Result{}
	}
	if err := r.validateModeOutputPlan(output, modeNames); err != nil {
		return Result{OutputError: fmt.Errorf("plan renderer outputs: %w", err)}
	}
	modeResults := make([]ModeResult, 0, len(modeNames))

	// Check if JSON output is requested
	jsonOutput := strings.HasSuffix(strings.ToLower(output), ".json")

	// Initialize progress tracking for multiple modes
	quiet := r.options.Quiet
	progEstimator := progress.NewProgressEstimator(!quiet)

	// If JSON output, collect all results and save as JSON
	if jsonOutput {
		results := make(map[string]interface{}, len(modeNames))

		if len(modeNames) > 1 {
			progEstimator.StartMultiOperation(len(modeNames), "Analysis Modes")
		}

		for _, mode := range modeNames {
			if len(modeNames) > 1 {
				progEstimator.NextOperation(fmt.Sprintf("Running %s", mode))
			}

			if !quiet {
				fmt.Printf("Running: %s\n", mode)
			}

			if _, ok := modeHandlers[mode]; !ok {
				printModeUnavailable(mode)
				modeResults = append(modeResults, ModeResult{
					Mode: mode,
					Err:  errors.New(modeUnavailableMessage(mode)),
				})
				results[mode] = map[string]interface{}{
					"error": "mode not implemented",
				}
				continue
			}

			data, err := extractModeDataForJSON(reader, mode)
			if err != nil {
				result := handleModeError(mode, err)
				modeResults = append(modeResults, result)
				key := "error"
				message := err.Error()
				if result.Warning != "" {
					key = "warning"
					message = result.Warning
				}
				results[mode] = map[string]interface{}{
					key: message,
				}
				continue
			}
			modeResults = append(modeResults, ModeResult{Mode: mode})
			results[mode] = data
		}

		if len(modeNames) > 1 {
			progEstimator.FinishMultiOperation()
		}

		// Save results as JSON
		if err := saveJSONResults(results, output); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving JSON results: %v\n", err)
			return Result{Modes: modeResults, OutputError: err}
		} else if !quiet {
			fmt.Printf("Results saved as JSON to: %s\n", output)
		}
		return Result{Modes: modeResults}
	} else {
		// Regular image output
		if len(modeNames) > 1 {
			// Start multi-mode progress tracking
			progEstimator.StartMultiOperation(len(modeNames), "Analysis Modes")

			for _, mode := range modeNames {
				progEstimator.NextOperation(fmt.Sprintf("Running %s", mode))

				if !quiet {
					fmt.Printf("Running: %s\n", mode)
				}

				modeResults = append(modeResults, r.runSingleMode(mode, reader, output, len(modeNames), startTime, endTime, modesConfig))
			}

			progEstimator.FinishMultiOperation()
		} else {
			// Single mode - let the individual mode handle its own progress
			for _, mode := range modeNames {
				if !quiet {
					fmt.Printf("Running: %s\n", mode)
				}

				modeResults = append(modeResults, r.runSingleMode(mode, reader, output, len(modeNames), startTime, endTime, modesConfig))
			}
		}
	}
	return Result{Modes: modeResults}
}

func (r *Renderer) runSingleMode(
	mode string,
	reader readers.Reader,
	output string,
	modeCount int,
	startTime, endTime *time.Time,
	opts modes.Options,
) ModeResult {
	modeFunc, ok := modeHandlers[mode]
	if !ok {
		printModeUnavailable(mode)
		return ModeResult{Mode: mode, Err: errors.New(modeUnavailableMessage(mode))}
	}
	formattedOutput := r.planModeOutput(output, mode, modeCount)
	if err := modeFunc(reader, formattedOutput, startTime, endTime, opts); err != nil {
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
	fmt.Fprintln(os.Stderr, modeUnavailableMessage(mode))
}

// handleModeError prints the outcome of a failed mode and classifies it:
// missing-analysis failures become Python-compatible warnings, everything
// else stays a hard error.
func handleModeError(mode string, err error) ModeResult {
	if warning, ok := missingAnalysisWarning(mode, err); ok {
		fmt.Fprintln(os.Stderr, warning)
		return ModeResult{Mode: mode, Warning: warning}
	}
	fmt.Fprintf(os.Stderr, "Error in mode %s: %v\n", mode, err)
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
	return errors.Is(err, readers.ErrAnalysisMissing)
}

func burndownProject(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	// Use Python-compatible implementation
	return modes.GenerateBurndownProjectPythonWithOptions(reader, output, opts)
}

func burndownFile(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	// Use Python-compatible implementation
	return modes.GenerateBurndownFilePythonWithOptions(reader, output, opts)
}

func burndownPerson(reader readers.Reader, output string, startTime, endTime *time.Time, opts modes.Options) error {
	return modes.BurndownPersonWithOptions(reader, output, startTime, endTime, opts)
}

func burndownRepository(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.GenerateBurndownRepositoryPythonWithOptions(reader, output, opts)
}

func burndownReposCombined(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.GenerateBurndownReposCombinedPythonWithOptions(reader, output, opts)
}

func overwritesMatrix(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.OverwritesMatrixWithOptions(reader, output, opts)
}

func ownershipBurndown(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.OwnershipBurndownWithOptions(reader, output, opts)
}

func couplesFiles(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	// Note: --disable-projector flag is supported for Python compatibility but not used
	// Our Go implementation focuses on core coupling analysis without TensorFlow embeddings
	return modes.CouplesFilesWithOptions(reader, output, opts)
}

func couplesPeople(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.CouplesPeopleWithOptions(reader, output, opts)
}

func couplesShotness(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.CouplesShotnessWithOptions(reader, output, opts)
}

func shotness(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.ShotnessWithOptions(reader, output, opts)
}

func devs(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.DevsWithOptions(reader, output, opts)
}

func devsEfforts(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.DevsEffortsWithOptions(reader, output, opts)
}

func oldVsNew(reader readers.Reader, output string, startTime, endTime *time.Time, opts modes.Options) error {
	return modes.OldVsNewWithOptions(reader, output, startTime, endTime, opts)
}

func languages(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.LanguagesWithOptions(reader, output, opts)
}

func temporalActivity(reader readers.Reader, output string, startTime, endTime *time.Time, opts modes.Options) error {
	return modes.TemporalActivity(reader, output, opts.TemporalLegendThreshold, opts.TemporalLegendSingleColumn, startTime, endTime)
}

func devsParallel(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.DevsParallelWithOptions(reader, output, opts)
}

func runTimes(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.RunTimesWithOptions(reader, output, opts)
}

func busFactor(reader readers.Reader, output string, _, _ *time.Time, _ modes.Options) error {
	return modes.BusFactor(reader, output)
}

func ownershipConcentration(reader readers.Reader, output string, _, _ *time.Time, _ modes.Options) error {
	return modes.OwnershipConcentration(reader, output)
}

func knowledgeDiffusion(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.KnowledgeDiffusion(reader, output, opts.KnowledgeDiffusionDetail)
}

func hotspotRisk(reader readers.Reader, output string, _, _ *time.Time, _ modes.Options) error {
	return modes.HotspotRisk(reader, output)
}

func sentiment(reader readers.Reader, output string, _, _ *time.Time, opts modes.Options) error {
	return modes.SentimentWithOptions(reader, output, opts)
}

func refactoringProxy(reader readers.Reader, output string, _, _ *time.Time, _ modes.Options) error {
	return modes.RefactoringProxy(reader, output)
}

//nolint:unparam // Mode handlers share an error-returning signature.
func runAllModes(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	renderer, err := NewRenderer(DefaultOptions())
	if err != nil {
		return err
	}
	return renderer.runAllModes(reader, output, startTime, endTime)
}

func (r *Renderer) runAllModes(reader readers.Reader, output string, startTime, endTime *time.Time) error {
	if !r.options.Quiet {
		fmt.Printf("Running 'all' mode: executing %d analysis modes\n", len(pythonAllModes))
	}

	var failures []error
	for _, modeName := range pythonAllModes {
		if !r.options.Quiet {
			fmt.Printf("  Running %s...\n", modeName)
		}

		modeFunc, ok := modeHandlers[modeName]
		if !ok {
			printModeUnavailable(modeName)
			failures = append(failures, errors.New(modeUnavailableMessage(modeName)))
			continue
		}
		modeOutput := r.planModeOutput(output, modeName, len(pythonAllModes))
		if err := modeFunc(reader, modeOutput, startTime, endTime, r.modeOptions()); err != nil {
			result := handleModeError(modeName, err)
			if result.Err != nil {
				failures = append(failures, fmt.Errorf("render mode %s: %w", modeName, result.Err))
			}
		}
	}

	return errors.Join(failures...)
}
