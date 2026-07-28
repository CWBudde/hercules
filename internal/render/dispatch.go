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
	if len(modeNames) == 0 {
		return Result{}
	}
	if err := r.validateModeOutputPlan(r.options.Output, modeNames); err != nil {
		return Result{OutputError: fmt.Errorf("plan renderer outputs: %w", err)}
	}
	if strings.HasSuffix(strings.ToLower(r.options.Output), ".json") {
		return r.executeJSONModes(modeNames, reader)
	}
	return r.executeImageModes(modeNames, reader)
}

func (r *Renderer) executeJSONModes(modeNames []string, reader readers.Reader) Result {
	results := make(map[string]interface{}, len(modeNames))
	modeResults := make([]ModeResult, 0, len(modeNames))
	estimator := progress.NewProgressEstimator(!r.options.Quiet)
	startMultiModeProgress(estimator, modeNames)
	for _, mode := range modeNames {
		nextModeProgress(estimator, mode, len(modeNames))
		r.printRunningMode(mode)
		modeResult, data := runJSONMode(reader, mode)
		modeResults = append(modeResults, modeResult)
		results[mode] = data
	}
	finishMultiModeProgress(estimator, modeNames)
	if err := saveJSONResults(results, r.options.Output); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving JSON results: %v\n", err)
		return Result{Modes: modeResults, OutputError: err}
	}
	if !r.options.Quiet {
		fmt.Printf("Results saved as JSON to: %s\n", r.options.Output)
	}
	return Result{Modes: modeResults}
}

func runJSONMode(reader readers.Reader, mode string) (ModeResult, interface{}) {
	if _, ok := modeHandlers[mode]; !ok {
		printModeUnavailable(mode)
		return ModeResult{Mode: mode, Err: errors.New(modeUnavailableMessage(mode))},
			map[string]interface{}{"error": "mode not implemented"}
	}
	data, err := extractModeDataForJSON(reader, mode)
	if err == nil {
		return ModeResult{Mode: mode}, data
	}
	result := handleModeError(mode, err)
	key, message := "error", err.Error()
	if result.Warning != "" {
		key, message = "warning", result.Warning
	}
	return result, map[string]interface{}{key: message}
}

func (r *Renderer) executeImageModes(modeNames []string, reader readers.Reader) Result {
	modeResults := make([]ModeResult, 0, len(modeNames))
	estimator := progress.NewProgressEstimator(!r.options.Quiet)
	startMultiModeProgress(estimator, modeNames)
	for _, mode := range modeNames {
		nextModeProgress(estimator, mode, len(modeNames))
		r.printRunningMode(mode)
		modeResults = append(modeResults, r.runSingleMode(
			mode, reader, r.options.Output, len(modeNames),
			r.options.StartTime, r.options.EndTime, r.modeOptions(),
		))
	}
	finishMultiModeProgress(estimator, modeNames)
	return Result{Modes: modeResults}
}

func startMultiModeProgress(estimator *progress.ProgressEstimator, modeNames []string) {
	if len(modeNames) > 1 {
		estimator.StartMultiOperation(len(modeNames), "Analysis Modes")
	}
}

func nextModeProgress(estimator *progress.ProgressEstimator, mode string, modeCount int) {
	if modeCount > 1 {
		estimator.NextOperation(fmt.Sprintf("Running %s", mode))
	}
}

func finishMultiModeProgress(estimator *progress.ProgressEstimator, modeNames []string) {
	if len(modeNames) > 1 {
		estimator.FinishMultiOperation()
	}
}

func (r *Renderer) printRunningMode(mode string) {
	if !r.options.Quiet {
		fmt.Printf("Running: %s\n", mode)
	}
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
	if mode == "devs-parallel" {
		return devsParallelMissingAnalysisWarning(err), true
	}
	warning, ok := standardMissingAnalysisWarnings()[mode]
	return warning, ok
}

func standardMissingAnalysisWarnings() map[string]string {
	const (
		burndown       = "Burndown stats were not collected. Re-run hercules with --burndown."
		burndownFiles  = "Burndown stats for files were not collected. Re-run hercules with --burndown --burndown-files."
		burndownPeople = "Burndown stats for people were not collected. Re-run hercules with --burndown --burndown-people."
		couples        = "Coupling stats were not collected. Re-run hercules with --couples."
		shotness       = "Structural hotness stats were not collected. Re-run hercules with --shotness. Also check --languages - the output may be empty."
		devs           = "Devs stats were not collected. Re-run hercules with --devs."
	)
	return map[string]string{
		"burndown-project":        "project: " + burndown,
		"burndown-file":           "files: " + burndownFiles,
		"burndown-person":         "people: " + burndownPeople,
		"ownership":               "ownership: " + burndownPeople,
		"overwrites-matrix":       "overwrites_matrix: " + burndownPeople,
		"burndown-repository":     "repositories: burndown data not available or repositories not tracked",
		"burndown-repos-combined": "repositories-combined: burndown data not available or repositories not tracked",
		"couples-files":           couples,
		"couples-people":          couples,
		"couples-shotness":        shotness,
		"shotness":                shotness,
		"sentiment":               "Sentiment stats were not collected. Re-run hercules with --sentiment.",
		"devs":                    devs,
		"devs-efforts":            devs,
		"old-vs-new":              devs,
		"languages":               devs,
		"temporal-activity":       "Temporal activity stats were not collected. Re-run hercules with --temporal-activity.",
		"bus-factor":              "Bus factor stats were not collected. Re-run hercules with --bus-factor.",
		"ownership-concentration": "Ownership concentration stats were not collected. Re-run hercules with --ownership-concentration.",
		"knowledge-diffusion":     "Knowledge diffusion stats were not collected. Re-run hercules with --knowledge-diffusion.",
		"hotspot-risk":            "Hotspot risk scores were not collected. Re-run hercules with --hotspot-risk.",
		"refactoring-proxy":       "Refactoring proxy data was not collected. Re-run hercules with --refactoring-proxy.",
	}
}

func devsParallelMissingAnalysisWarning(err error) string {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "people cooccurrence") {
		return "Coupling stats were not collected. Re-run hercules with --couples."
	}
	if strings.Contains(message, "devs time series") {
		return "Devs stats were not collected. Re-run hercules with --devs."
	}
	return "devs-parallel: Burndown stats for people were not collected. Re-run hercules with --burndown --burndown-people."
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
