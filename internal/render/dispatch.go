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

// Map of mode names to their handlers.
type modeHandler func(reader readers.Reader, output string, startTime, endTime *time.Time, opts modes.Options) error

var modeHandlers = map[string]modeHandler{
	ModeBurndownProject:        burndownProject,
	ModeBurndownFile:           burndownFile,
	ModeBurndownPerson:         burndownPerson,
	ModeBurndownRepository:     burndownRepository,
	ModeBurndownReposCombined:  burndownReposCombined,
	ModeOverwritesMatrix:       overwritesMatrix,
	ModeOwnership:              ownershipBurndown,
	ModeCouplesFiles:           couplesFiles,
	ModeCouplesPeople:          couplesPeople,
	ModeCouplesShotness:        couplesShotness,
	ModeShotness:               shotness,
	ModeDevs:                   devs,
	ModeDevsEfforts:            devsEfforts,
	ModeOldVsNew:               oldVsNew,
	ModeLanguages:              languages,
	ModeTemporalActivity:       temporalActivity,
	ModeDevsParallel:           devsParallel,
	ModeRunTimes:               runTimes,
	ModeBusFactor:              busFactor,
	ModeOwnershipConcentration: ownershipConcentration,
	ModeKnowledgeDiffusion:     knowledgeDiffusion,
	ModeHotspotRisk:            hotspotRisk,
	ModeSentiment:              sentiment,
	ModeRefactoringProxy:       refactoringProxy,
}

// executeModes renders modeNames with the default options, reporting failures
// on stderr the way the CLI does.
func executeModes(modeNames []string, reader readers.Reader, output string) {
	opts := DefaultOptions()
	opts.Output = output

	renderer, err := NewRenderer(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating renderer: %v\n", err)

		return
	}

	renderer.executeModes(modeNames, reader)
}

func (r *Renderer) executeModes(modeNames []string, reader readers.Reader) Result {
	if len(modeNames) == 0 {
		return Result{}
	}

	err := r.validateModeOutputPlan(r.options.Output, modeNames)
	if err != nil {
		return Result{OutputError: fmt.Errorf("plan renderer outputs: %w", err)}
	}

	if strings.HasSuffix(strings.ToLower(r.options.Output), ".json") {
		return r.executeJSONModes(modeNames, reader)
	}

	return r.executeImageModes(modeNames, reader)
}

func (r *Renderer) executeJSONModes(modeNames []string, reader readers.Reader) Result {
	results := make(map[string]any, len(modeNames))
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

	err := saveJSONResults(results, r.options.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving JSON results: %v\n", err)
		return Result{Modes: modeResults, OutputError: err}
	}

	if !r.options.Quiet {
		fmt.Printf("Results saved as JSON to: %s\n", r.options.Output)
	}

	return Result{Modes: modeResults}
}

func runJSONMode(reader readers.Reader, mode string) (ModeResult, any) {
	if _, ok := modeHandlers[mode]; !ok {
		printModeUnavailable(mode)

		return ModeResult{Mode: mode, Err: modeUnavailableError(mode)},
			map[string]any{"error": "mode not implemented"}
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

	return result, map[string]any{key: message}
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
		estimator.NextOperation("Running " + mode)
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
		return ModeResult{Mode: mode, Err: modeUnavailableError(mode)}
	}

	formattedOutput := r.planModeOutput(output, mode, modeCount)

	err := modeFunc(reader, formattedOutput, startTime, endTime, opts)
	if err != nil {
		return handleModeError(mode, err)
	}

	return ModeResult{Mode: mode}
}

// errModeNotImplemented reports a mode name that is valid but has no entry in
// modeHandlers.
var errModeNotImplemented = errors.New("mode not implemented yet")

// modeUnavailableError explains why mode cannot be dispatched. errUnknownMode
// is shared with ResolveModes so both entry points classify the same way.
func modeUnavailableError(mode string) error {
	if isValidMode(mode) {
		return fmt.Errorf("%w: %s", errModeNotImplemented, mode)
	}

	return fmt.Errorf("%w: %s", errUnknownMode, mode)
}

// modeUnavailableMessage renders modeUnavailableError for the console, where
// the message starts a line and is therefore capitalized.
func modeUnavailableMessage(mode string) string {
	message := modeUnavailableError(mode).Error()

	return strings.ToUpper(message[:1]) + message[1:]
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

	if mode == ModeDevsParallel {
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
		shotness       = "Structural hotness stats were not collected. Re-run hercules with --shotness. " +
			"Also check --languages - the output may be empty."
		devs = "Devs stats were not collected. Re-run hercules with --devs."
	)

	return map[string]string{
		ModeBurndownProject:       "project: " + burndown,
		ModeBurndownFile:          "files: " + burndownFiles,
		ModeBurndownPerson:        "people: " + burndownPeople,
		ModeOwnership:             "ownership: " + burndownPeople,
		ModeOverwritesMatrix:      "overwrites_matrix: " + burndownPeople,
		ModeBurndownRepository:    "repositories: burndown data not available or repositories not tracked",
		ModeBurndownReposCombined: "repositories-combined: burndown data not available or repositories not tracked",
		ModeCouplesFiles:          couples,
		ModeCouplesPeople:         couples,
		ModeCouplesShotness:       shotness,
		ModeShotness:              shotness,
		ModeSentiment:             "Sentiment stats were not collected. Re-run hercules with --sentiment.",
		ModeDevs:                  devs,
		ModeDevsEfforts:           devs,
		ModeOldVsNew:              devs,
		ModeLanguages:             devs,
		ModeTemporalActivity:      "Temporal activity stats were not collected. Re-run hercules with --temporal-activity.",
		ModeBusFactor:             "Bus factor stats were not collected. Re-run hercules with --bus-factor.",
		ModeOwnershipConcentration: "Ownership concentration stats were not collected. " +
			"Re-run hercules with --ownership-concentration.",
		ModeKnowledgeDiffusion: "Knowledge diffusion stats were not collected. " +
			"Re-run hercules with --knowledge-diffusion.",
		ModeHotspotRisk:      "Hotspot risk scores were not collected. Re-run hercules with --hotspot-risk.",
		ModeRefactoringProxy: "Refactoring proxy data was not collected. Re-run hercules with --refactoring-proxy.",
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

	return "devs-parallel: Burndown stats for people were not collected. " +
		"Re-run hercules with --burndown --burndown-people."
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
	return modes.TemporalActivity(
		reader, output,
		opts.TemporalLegendThreshold, opts.TemporalLegendSingleColumn,
		startTime, endTime,
	)
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
			failures = append(failures, modeUnavailableError(modeName))

			continue
		}

		modeOutput := r.planModeOutput(output, modeName, len(pythonAllModes))

		err := modeFunc(reader, modeOutput, startTime, endTime, r.modeOptions())
		if err != nil {
			result := handleModeError(modeName, err)
			if result.Err != nil {
				failures = append(failures, fmt.Errorf("render mode %s: %w", modeName, result.Err))
			}
		}
	}

	return errors.Join(failures...)
}
