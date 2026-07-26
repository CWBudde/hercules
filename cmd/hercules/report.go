package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/cobra"
	"golang.org/x/sys/execabs"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/render"
)

const (
	reportAnalysisBurndown               = "burndown"
	reportAnalysisBurndownFiles          = "burndown-files"
	reportAnalysisBurndownPeople         = "burndown-people"
	reportAnalysisDevs                   = "devs"
	reportAnalysisTemporalActivity       = "temporal-activity"
	reportAnalysisBusFactor              = "bus-factor"
	reportAnalysisOwnershipConcentration = "ownership-concentration"
	reportAnalysisKnowledgeDiffusion     = "knowledge-diffusion"
	reportAnalysisOnboarding             = "onboarding"
	reportAnalysisHotspotRisk            = "hotspot-risk"
	reportAnalysisRefactoringProxy       = "refactoring-proxy"
	reportAnalysisShotness               = "shotness"
	reportAnalysisSentiment              = "sentiment"
	reportModeBurndownProject            = "burndown-project"
	reportModeBurndownFile               = "burndown-file"
	reportModeBurndownPerson             = "burndown-person"
	reportModeOverwritesMatrix           = "overwrites-matrix"
	reportModeOwnership                  = "ownership"
	reportModeCouplesFiles               = "couples-files"
	reportModeCouplesPeople              = "couples-people"
	reportModeDevsEfforts                = "devs-efforts"
	reportModeOldVsNew                   = "old-vs-new"
	reportModeLanguages                  = "languages"
)

var reportDefaultAnalysisFlags = []string{
	reportAnalysisBurndown,
	reportAnalysisBurndownFiles,
	reportAnalysisBurndownPeople,
	"couples",
	reportAnalysisDevs,
	reportAnalysisTemporalActivity,
	reportAnalysisBusFactor,
	reportAnalysisOwnershipConcentration,
	reportAnalysisKnowledgeDiffusion,
	reportAnalysisOnboarding,
	reportAnalysisHotspotRisk,
	reportAnalysisRefactoringProxy,
}

var reportAllAnalysisFlags = []string{
	reportAnalysisBurndown,
	reportAnalysisBurndownFiles,
	reportAnalysisBurndownPeople,
	"couples",
	reportAnalysisShotness,
	reportAnalysisDevs,
	reportAnalysisTemporalActivity,
	reportAnalysisBusFactor,
	reportAnalysisOwnershipConcentration,
	reportAnalysisKnowledgeDiffusion,
	reportAnalysisOnboarding,
	reportAnalysisHotspotRisk,
	reportAnalysisRefactoringProxy,
	reportAnalysisSentiment,
}

var reportDefaultModes = []string{
	reportModeBurndownProject,
	reportModeBurndownFile,
	reportModeBurndownPerson,
	reportModeOverwritesMatrix,
	reportModeOwnership,
	reportModeCouplesFiles,
	reportModeCouplesPeople,
	reportAnalysisDevs,
	reportModeDevsEfforts,
	reportModeOldVsNew,
	reportModeLanguages,
	reportAnalysisTemporalActivity,
	reportAnalysisBusFactor,
	reportAnalysisOwnershipConcentration,
	reportAnalysisKnowledgeDiffusion,
	reportAnalysisHotspotRisk,
	reportAnalysisRefactoringProxy,
}

var reportAllModes = []string{
	reportModeBurndownProject,
	reportModeBurndownFile,
	reportModeBurndownPerson,
	"burndown-repository",
	"burndown-repos-combined",
	reportModeOverwritesMatrix,
	reportModeOwnership,
	reportModeCouplesFiles,
	reportModeCouplesPeople,
	"couples-shotness",
	reportAnalysisShotness,
	reportAnalysisSentiment,
	reportAnalysisTemporalActivity,
	reportAnalysisDevs,
	reportModeDevsEfforts,
	reportModeOldVsNew,
	reportModeLanguages,
	"devs-parallel",
	reportAnalysisBusFactor,
	reportAnalysisOwnershipConcentration,
	reportAnalysisKnowledgeDiffusion,
	reportAnalysisHotspotRisk,
	reportAnalysisRefactoringProxy,
}

var reportValidModes = map[string]struct{}{
	reportModeBurndownProject:            {},
	reportModeBurndownFile:               {},
	reportModeBurndownPerson:             {},
	"burndown-repository":                {},
	"burndown-repos-combined":            {},
	reportModeOverwritesMatrix:           {},
	reportModeOwnership:                  {},
	reportModeCouplesFiles:               {},
	reportModeCouplesPeople:              {},
	"couples-shotness":                   {},
	reportAnalysisShotness:               {},
	reportAnalysisSentiment:              {},
	reportAnalysisTemporalActivity:       {},
	reportAnalysisDevs:                   {},
	reportModeDevsEfforts:                {},
	reportModeOldVsNew:                   {},
	reportModeLanguages:                  {},
	"devs-parallel":                      {},
	reportAnalysisBusFactor:              {},
	reportAnalysisOwnershipConcentration: {},
	reportAnalysisKnowledgeDiffusion:     {},
	reportAnalysisHotspotRisk:            {},
	reportAnalysisRefactoringProxy:       {},
}

// reportCmd generates a complete labours report in one command.
var reportCmd = &cobra.Command{
	Use:   "report [flags] <repository> [cache-path]",
	Short: "Generate a complete report directory with charts and summary.",
	Long: `Runs Hercules in Protocol Buffers mode, renders the charts with the built-in
in-process renderer (or an external labours command when --labours-cmd is
given) and writes an output directory with generated chart assets and
index.html summary.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runReport,
}

func runReport(cmd *cobra.Command, args []string) error {
	options, err := readReportOptions(cmd, args)
	if err != nil {
		return err
	}
	available := availableReportAnalysisFlags()
	analysisFlags, err := selectReportAnalysisFlags(
		available, options.requestedAnalyses, options.allAnalyses,
	)
	if err != nil {
		return err
	}
	modes, err := selectReportModes(options.requestedModes, options.allAnalyses)
	if err != nil {
		return err
	}

	destination, stagingDir, err := prepareReportStaging(options.outputDir)
	if err != nil {
		return err
	}
	stagingPublished := false
	defer func() {
		if !stagingPublished {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	stagingOptions := options
	stagingOptions.outputDir = stagingDir

	reportOutcomeErr, err := buildStagedReport(
		cmd.Context(), stagingOptions, analysisFlags, modes,
	)
	if err != nil {
		return err
	}
	if err := publishReport(stagingDir, destination); err != nil {
		return err
	}
	stagingPublished = true
	if reportOutcomeErr != nil {
		_, _ = fmt.Fprintf(
			os.Stderr, "report: published with mode failures. See %s\n",
			filepath.Join(destination.path, "index.html"),
		)
		return reportOutcomeErr
	}
	_, _ = fmt.Fprintf(os.Stderr, "report: done. Open %s\n",
		filepath.Join(destination.path, "index.html"))
	return nil
}

func buildStagedReport(
	ctx context.Context,
	options reportOptions,
	analysisFlags, modes []string,
) (error, error) {
	reportPB, message, err := generateReportInput(ctx, options, analysisFlags)
	if err != nil {
		return nil, err
	}
	modeResults, err := renderReportModes(ctx, options, reportPB, modes)
	if err != nil {
		return nil, err
	}
	finalizeErr := finalizeReport(options, message, analysisFlags, modes, modeResults)
	var renderErr *reportRenderError
	if finalizeErr != nil && !errors.As(finalizeErr, &renderErr) {
		return nil, finalizeErr
	}
	return finalizeErr, nil
}

type reportOptions struct {
	outputDir         string
	format            string
	allAnalyses       bool
	strict            bool
	requestedAnalyses []string
	requestedModes    []string
	herculesExtra     []string
	laboursExtra      []string
	laboursCommand    string
	repositoryArgs    []string
}

type reportDestination struct {
	path   string
	exists bool
}

var (
	installReportDirectory = atomicInstallCacheDirectory
	swapReportDirectories  = atomicSwapCacheDirectories
)

func prepareReportStaging(outputDir string) (reportDestination, string, error) {
	destination, err := resolveReportDestination(outputDir)
	if err != nil {
		return reportDestination{}, "", err
	}
	stagingDir, err := createReportStaging(destination.path)
	if err != nil {
		return reportDestination{}, "", err
	}
	return destination, stagingDir, nil
}

func resolveReportDestination(outputDir string) (reportDestination, error) {
	absolutePath, err := filepath.Abs(outputDir)
	if err != nil {
		return reportDestination{}, fmt.Errorf("resolve report output directory: %w", err)
	}
	absolutePath = filepath.Clean(absolutePath)
	if err := validateReportDestination(absolutePath); err != nil {
		return reportDestination{}, err
	}

	parent := filepath.Dir(absolutePath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return reportDestination{}, fmt.Errorf("create report output parent: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return reportDestination{}, fmt.Errorf("resolve report output parent: %w", err)
	}
	destinationPath := filepath.Join(resolvedParent, filepath.Base(absolutePath))
	info, err := os.Lstat(destinationPath)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return reportDestination{}, fmt.Errorf("inspect report output directory: %w", err)
	}
	if exists && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return reportDestination{}, fmt.Errorf(
			"report output %s must be a directory and not a symbolic link", destinationPath,
		)
	}
	return reportDestination{path: destinationPath, exists: exists}, nil
}

func createReportStaging(destinationPath string) (string, error) {
	stagingDir, err := os.MkdirTemp(
		filepath.Dir(destinationPath), "."+filepath.Base(destinationPath)+".hercules-report-",
	)
	if err != nil {
		return "", fmt.Errorf("create report staging directory: %w", err)
	}
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		_ = os.Remove(stagingDir)
		return "", fmt.Errorf("set report staging permissions: %w", err)
	}
	return stagingDir, nil
}

func validateReportDestination(path string) error {
	if isFilesystemRoot(path) {
		return fmt.Errorf("refusing to replace filesystem root %s with a report", path)
	}
	currentDirectory, err := canonicalExistingPath(".")
	if err != nil {
		return fmt.Errorf("resolve current working directory: %w", err)
	}
	if sameCachePath(path, currentDirectory) {
		return fmt.Errorf("refusing to replace current working directory %s with a report", path)
	}
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	homeDirectory, err = canonicalExistingPath(homeDirectory)
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	if sameCachePath(path, homeDirectory) {
		return fmt.Errorf("refusing to replace home directory %s with a report", path)
	}
	return nil
}

func publishReport(stagingDir string, destination reportDestination) error {
	currentlyExists, err := revalidateReportDestination(destination)
	if err != nil {
		return err
	}
	if !currentlyExists {
		if err := installReportDirectory(stagingDir, destination.path); err != nil {
			return fmt.Errorf("atomically publish report at %s: %w", destination.path, err)
		}
		return nil
	}
	if err := swapReportDirectories(stagingDir, destination.path); err != nil {
		return fmt.Errorf("atomically replace report at %s: %w", destination.path, err)
	}
	if err := os.RemoveAll(stagingDir); err != nil {
		return fmt.Errorf("remove previous report after publication: %w", err)
	}
	return nil
}

func revalidateReportDestination(destination reportDestination) (bool, error) {
	info, err := os.Lstat(destination.path)
	currentlyExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("revalidate report destination: %w", err)
	}
	if currentlyExists != destination.exists {
		return false, fmt.Errorf("report destination %s changed while generating", destination.path)
	}
	if currentlyExists && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return false, fmt.Errorf("report destination %s changed while generating", destination.path)
	}
	return currentlyExists, nil
}

func readReportOptions(cmd *cobra.Command, args []string) (reportOptions, error) {
	flags := cmd.Flags()
	reader := commandFlagReader{flags: flags}
	outputDir := reader.string("output")
	allAnalyses := reader.bool("all")
	requestedAnalyses := reader.stringSlice("analysis")
	requestedModes := reader.stringSlice("mode")
	format := reader.string("format")
	strict := reader.bool("strict")
	herculesExtra := reader.stringArray("hercules-arg")
	laboursExtra := reader.stringArray("labours-arg")
	laboursCmdOverride := reader.string("labours-cmd")
	if reader.err != nil {
		return reportOptions{}, reader.err
	}
	if outputDir == "" {
		return reportOptions{}, errors.New("--output must not be empty")
	}
	format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	if format != "png" && format != "svg" {
		return reportOptions{}, fmt.Errorf("unsupported --format %q: expected png or svg", format)
	}
	if err := validateReportLaboursFlags(laboursCmdOverride, laboursExtra); err != nil {
		return reportOptions{}, err
	}
	return reportOptions{
		outputDir:         filepath.Clean(outputDir),
		format:            format,
		allAnalyses:       allAnalyses,
		strict:            strict,
		requestedAnalyses: requestedAnalyses,
		requestedModes:    requestedModes,
		herculesExtra:     herculesExtra,
		laboursExtra:      laboursExtra,
		laboursCommand:    laboursCmdOverride,
		repositoryArgs:    args,
	}, nil
}

func availableReportAnalysisFlags() map[string]struct{} {
	available := make(map[string]struct{})
	for _, leaf := range hercules.Registry.GetLeaves() {
		flag := leaf.Flag()
		if flag != "" {
			available[flag] = struct{}{}
		}
	}
	return available
}

func generateReportInput(ctx context.Context, options reportOptions, analysisFlags []string) (
	string, pb.AnalysisResults, error,
) {
	if err := os.MkdirAll(options.outputDir, 0o755); err != nil {
		return "", pb.AnalysisResults{}, err
	}
	reportPB := filepath.Join(options.outputDir, "report.pb")

	herculesArgs := make(
		[]string, 0, len(analysisFlags)+len(options.herculesExtra)+len(options.repositoryArgs)+3,
	)
	herculesArgs = append(herculesArgs, "--pb", "--quiet")
	for _, flag := range analysisFlags {
		herculesArgs = append(herculesArgs, "--"+flag)
	}
	herculesArgs = append(herculesArgs, options.herculesExtra...)
	herculesArgs = append(herculesArgs, options.repositoryArgs...)

	_, _ = fmt.Fprintf(os.Stderr, "report: running hercules (%d analysis flags)...\n", len(analysisFlags))
	payload, err := runAndCapture(ctx, os.Args[0], herculesArgs, nil)
	if err != nil {
		return "", pb.AnalysisResults{}, fmt.Errorf("run hercules for report: %w", err)
	}
	if err := os.WriteFile(reportPB, payload, 0o600); err != nil {
		return "", pb.AnalysisResults{}, err
	}

	var message pb.AnalysisResults
	if err := proto.Unmarshal(payload, &message); err != nil {
		return "", pb.AnalysisResults{}, fmt.Errorf("parse generated protobuf report: %w", err)
	}
	return reportPB, message, nil
}

func renderReportModes(ctx context.Context, options reportOptions, reportPB string,
	modes []string,
) (reportModeResults, error) {
	chartsRoot := filepath.Join(options.outputDir, "charts")
	if err := os.MkdirAll(chartsRoot, 0o755); err != nil {
		return reportModeResults{}, err
	}
	if options.laboursCommand == "" {
		return renderReportInProcess(options, reportPB, chartsRoot, modes)
	}
	return renderReportExternally(ctx, options, reportPB, chartsRoot, modes)
}

func renderReportInProcess(options reportOptions, reportPB, chartsRoot string,
	modes []string,
) (reportModeResults, error) {
	reader, err := render.LoadInput(reportPB, "pb")
	if err != nil {
		return reportModeResults{}, fmt.Errorf("load generated protobuf report for rendering: %w", err)
	}
	var outcomes reportModeResults
	for _, mode := range modes {
		output := reportModeOutput(chartsRoot, mode, options.format)
		_, _ = fmt.Fprintf(os.Stderr, "report: rendering mode %s...\n", mode)
		renderOptions := render.DefaultOptions()
		renderOptions.Output = output
		result := render.Run(reader, []string{mode}, renderOptions)
		if err := recordInProcessResult(&outcomes, mode, result, options.strict); err != nil {
			return reportModeResults{}, err
		}
	}
	return outcomes, nil
}

func recordInProcessResult(
	outcomes *reportModeResults, mode string, result render.Result, strict bool,
) error {
	for _, modeResult := range result.Modes {
		if modeResult.Warning != "" {
			outcomes.Warnings = append(
				outcomes.Warnings, reportModeWarning{Mode: mode, Warning: modeResult.Warning},
			)
		}
		if modeResult.Err == nil {
			continue
		}
		outcomes.Failures = append(
			outcomes.Failures, reportModeFailure{Mode: mode, Error: modeResult.Err.Error()},
		)
		if strict {
			return fmt.Errorf("render mode %s failed: %w", mode, modeResult.Err)
		}
	}
	if result.OutputError == nil {
		return nil
	}
	outcomes.Failures = append(outcomes.Failures, reportModeFailure{
		Mode: mode, Error: result.OutputError.Error(),
	})
	if strict {
		return fmt.Errorf("write render mode %s output: %w", mode, result.OutputError)
	}
	return nil
}

func renderReportExternally(ctx context.Context, options reportOptions, reportPB, chartsRoot string,
	modes []string,
) (reportModeResults, error) {
	command, err := resolveLaboursCommand(options.laboursCommand)
	if err != nil {
		return reportModeResults{}, err
	}
	var outcomes reportModeResults
	for _, mode := range modes {
		args := externalReportModeArgs(
			command, options.laboursExtra, reportPB, reportModeOutput(chartsRoot, mode, options.format), mode,
		)
		_, _ = fmt.Fprintf(os.Stderr, "report: running labours mode %s...\n", mode)
		if err := runAndCaptureTo(ctx, os.Stderr, command[0], args, nil); err != nil {
			outcomes.Failures = append(
				outcomes.Failures, reportModeFailure{Mode: mode, Error: err.Error()},
			)
			if options.strict {
				return reportModeResults{}, fmt.Errorf("labours mode %s failed: %w", mode, err)
			}
		}
	}
	return outcomes, nil
}

func externalReportModeArgs(command, extra []string, reportPB, output, mode string) []string {
	args := make([]string, 0, len(command)+len(extra)+8)
	args = append(args, command[1:]...)
	args = append(args, "-f", "pb", "-i", reportPB, "-o", output, "-m", mode, "--backend", "Agg")
	return append(args, extra...)
}

func reportModeOutput(chartsRoot, mode, format string) string {
	return filepath.Join(chartsRoot, sanitizePathComponent(mode)+"."+format)
}

func finalizeReport(options reportOptions, message pb.AnalysisResults, analysisFlags, modes []string,
	modeResults reportModeResults,
) error {
	plots, assets, err := collectReportAssets(options.outputDir)
	if err != nil {
		return err
	}
	assets = append(assets, "manifest.json")
	sort.Strings(assets)

	indexFile := filepath.Join(options.outputDir, "index.html")
	indexData := newReportIndexData(message, analysisFlags, modes, modeResults, plots, assets, options.format)
	if err := writeReportIndex(indexFile, indexData); err != nil {
		return err
	}
	files, err := collectReportManifestFiles(options.outputDir)
	if err != nil {
		return err
	}
	files = append(files, "manifest.json")
	sort.Strings(files)
	if err := writeReportManifest(
		filepath.Join(options.outputDir, "manifest.json"),
		reportManifest{
			Version:   1,
			Files:     files,
			Warnings:  modeResults.Warnings,
			Failures:  modeResults.Failures,
			Generated: indexData.GeneratedAt,
		},
	); err != nil {
		return err
	}

	if len(modeResults.Failures) > 0 {
		return reportFailuresError(modeResults.Failures)
	}
	return nil
}

func reportFailuresError(failures []reportModeFailure) error {
	errs := make([]error, 0, len(failures))
	for _, failure := range failures {
		errs = append(errs, fmt.Errorf("render mode %s: %s", failure.Mode, failure.Error))
	}
	return &reportRenderError{err: errors.Join(errs...)}
}

// reportAnalysisFlagParents maps analysis flags that are sub-options of another
// leaf (not registry leaves themselves) to the leaf whose availability governs them.
var reportAnalysisFlagParents = map[string]string{
	reportAnalysisBurndownFiles:  reportAnalysisBurndown,
	reportAnalysisBurndownPeople: reportAnalysisBurndown,
}

func selectReportAnalysisFlags(
	available map[string]struct{}, requested []string, includeAll bool,
) ([]string, error) {
	source, strictSelection := reportAnalysisSource(requested, includeAll)

	set := map[string]struct{}{}
	for _, flag := range source {
		if !reportAnalysisSupported(flag) {
			if strictSelection {
				return nil, fmt.Errorf(
					"analysis flag %q is unavailable in this build; rebuild with -tags tensorflow",
					flag,
				)
			}
			continue
		}
		if !reportAnalysisAvailable(available, flag) {
			if strictSelection {
				return nil, fmt.Errorf("unknown analysis flag %q", flag)
			}
			continue
		}
		set[flag] = struct{}{}
	}

	result := make([]string, 0, len(set))
	for flag := range set {
		result = append(result, flag)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, errors.New("no analysis flags selected for report")
	}
	return result, nil
}

func reportAnalysisSource(requested []string, includeAll bool) ([]string, bool) {
	if len(requested) > 0 {
		return requested, true
	}
	if includeAll {
		return reportAllAnalysisFlags, false
	}
	return reportDefaultAnalysisFlags, false
}

func reportAnalysisSupported(flag string) bool {
	return flag != reportAnalysisSentiment || tensorflowEnabled
}

func reportAnalysisAvailable(available map[string]struct{}, flag string) bool {
	if parent, ok := reportAnalysisFlagParents[flag]; ok {
		flag = parent
	}
	_, exists := available[flag]
	return exists
}

func selectReportModes(requested []string, includeAll bool) ([]string, error) {
	var source []string
	switch {
	case len(requested) > 0:
		source = requested
	case includeAll:
		source = reportAllModes
	default:
		source = reportDefaultModes
	}
	set := map[string]struct{}{}
	result := make([]string, 0, len(source))
	for _, mode := range source {
		if _, exists := reportValidModes[mode]; !exists {
			return nil, fmt.Errorf("unknown report mode %q", mode)
		}
		if _, exists := set[mode]; exists {
			continue
		}
		set[mode] = struct{}{}
		result = append(result, mode)
	}
	return result, nil
}

// validateReportLaboursFlags rejects flag combinations that only make sense
// for the external labours subprocess path.
func validateReportLaboursFlags(laboursCmdOverride string, laboursExtra []string) error {
	if laboursCmdOverride == "" && len(laboursExtra) > 0 {
		return errors.New(
			"--labours-arg requires --labours-cmd: the built-in renderer does not accept extra labours arguments",
		)
	}
	for _, argument := range laboursExtra {
		for _, reserved := range []string{
			"-o", "--output", "-i", "--input", "-f", "--input-format",
			"-m", "--mode", "--modes", "--backend",
		} {
			if argument == reserved || strings.HasPrefix(argument, reserved+"=") {
				return fmt.Errorf(
					"--labours-arg may not override report-controlled flag %q", reserved,
				)
			}
		}
	}
	return nil
}

func resolveLaboursCommand(override string) ([]string, error) {
	parts := strings.Fields(override)
	if len(parts) == 0 {
		return nil, errors.New("--labours-cmd is empty")
	}
	return parts, nil
}

func runAndCapture(ctx context.Context, command string, args, env []string) ([]byte, error) {
	cmd := execabs.CommandContext(ctx, command, args...)
	cmd.Stderr = os.Stderr
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", command, strings.Join(args, " "), err)
	}
	return output.Bytes(), nil
}

func runAndCaptureTo(ctx context.Context, writer *os.File, command string, args, env []string) error {
	cmd := execabs.CommandContext(ctx, command, args...)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", command, strings.Join(args, " "), err)
	}
	return nil
}

func sanitizePathComponent(value string) string {
	if value == "" {
		return "chart"
	}
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, ch := range value {
		if isSafeReportPathRune(ch) {
			builder.WriteRune(ch)
		} else {
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

func isSafeReportPathRune(character rune) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	case character >= '0' && character <= '9':
		return true
	default:
		return character == '.' || character == '-' || character == '_'
	}
}

func collectReportAssets(root string) ([]string, []string, error) {
	var plots []string
	var assets []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".png", ".svg":
			plots = append(plots, rel)
		case ".json", ".tsv", ".pb":
			assets = append(assets, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(plots)
	sort.Strings(assets)
	return plots, assets, nil
}

type reportModeFailure struct {
	Mode  string `json:"mode"`
	Error string `json:"error"`
}

type reportModeWarning struct {
	Mode    string `json:"mode"`
	Warning string `json:"warning"`
}

type reportModeResults struct {
	Warnings []reportModeWarning
	Failures []reportModeFailure
}

type reportRenderError struct {
	err error
}

func (err *reportRenderError) Error() string {
	return err.err.Error()
}

func (err *reportRenderError) Unwrap() error {
	return err.err
}

type reportManifest struct {
	Version   int                 `json:"version"`
	Generated string              `json:"generatedAt"`
	Files     []string            `json:"files"`
	Warnings  []reportModeWarning `json:"warnings,omitempty"`
	Failures  []reportModeFailure `json:"failures,omitempty"`
}

type reportIndexData struct {
	GeneratedAt string
	Repository  string
	Version     int32
	GitHash     string
	BeginTime   string
	EndTime     string
	Commits     int32
	RuntimeMS   int64
	Analyses    []string
	Modes       []string
	Warnings    []reportModeWarning
	Failures    []reportModeFailure
	Plots       []string
	Assets      []string
	Format      string
}

func newReportIndexData(
	message pb.AnalysisResults,
	analysisFlags []string,
	modes []string,
	modeResults reportModeResults,
	plots []string,
	assets []string,
	format string,
) reportIndexData {
	begin := "n/a"
	end := "n/a"
	repository := ""
	version := int32(pb.SchemaVersion)
	gitHash := hercules.BinaryGitHash
	commits := int32(0)
	runtimeMS := int64(0)

	if message.GetHeader() != nil {
		repository = message.GetHeader().GetRepository()
		version = message.GetHeader().GetVersion()
		gitHash = message.GetHeader().GetHash()
		commits = message.GetHeader().GetCommits()
		runtimeMS = message.GetHeader().GetRunTime()
		if message.GetHeader().GetBeginUnixTime() > 0 {
			begin = time.Unix(message.GetHeader().GetBeginUnixTime(), 0).UTC().Format(time.RFC3339)
		}
		if message.GetHeader().GetEndUnixTime() > 0 {
			end = time.Unix(message.GetHeader().GetEndUnixTime(), 0).UTC().Format(time.RFC3339)
		}
	}

	analyses := make([]string, 0, len(message.GetContents()))
	for key := range message.GetContents() {
		analyses = append(analyses, key)
	}
	sort.Strings(analyses)
	if len(analyses) == 0 {
		analyses = append([]string{}, analysisFlags...)
	}

	return reportIndexData{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Repository:  repository,
		Version:     version,
		GitHash:     gitHash,
		BeginTime:   begin,
		EndTime:     end,
		Commits:     commits,
		RuntimeMS:   runtimeMS,
		Analyses:    analyses,
		Modes:       modes,
		Warnings:    modeResults.Warnings,
		Failures:    modeResults.Failures,
		Plots:       plots,
		Assets:      assets,
		Format:      strings.ToUpper(format),
	}
}

const reportIndexTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Hercules Report</title>
  <style>
    :root {
      color-scheme: light;
      font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
    }
    body {
      margin: 2rem;
      line-height: 1.4;
      color: #111;
      background: #f6f8fb;
    }
    h1, h2 {
      margin-bottom: 0.5rem;
    }
    .card {
      background: #fff;
      border: 1px solid #d8dee9;
      border-radius: 8px;
      padding: 1rem;
      margin-bottom: 1rem;
    }
    .muted {
      color: #556;
      font-size: 0.95rem;
    }
    ul {
      padding-left: 1.2rem;
    }
    img {
      width: 100%;
      max-width: 1400px;
      border: 1px solid #d8dee9;
      border-radius: 6px;
      background: #fff;
    }
    .plot {
      margin-bottom: 1.25rem;
    }
    code {
      background: #eef3fb;
      padding: 0.1rem 0.3rem;
      border-radius: 4px;
    }
  </style>
</head>
<body>
  <h1>Hercules Report</h1>
  <p class="muted">Generated: {{.GeneratedAt}}</p>

  <section class="card">
    <h2>Summary</h2>
    <ul>
      <li>Repository: <code>{{.Repository}}</code></li>
      <li>Hercules version: <code>{{.Version}}</code> (<code>{{.GitHash}}</code>)</li>
      <li>Commits: <code>{{.Commits}}</code></li>
      <li>Range: <code>{{.BeginTime}}</code> → <code>{{.EndTime}}</code></li>
      <li>Run time: <code>{{.RuntimeMS}}</code> ms</li>
      <li>Requested modes ({{len .Modes}}): <code>{{join .Modes ", "}}</code></li>
      <li>Image format: <code>{{.Format}}</code></li>
    </ul>
  </section>

  <section class="card">
    <h2>Collected Analyses</h2>
    <ul>
      {{range .Analyses}}<li><code>{{.}}</code></li>{{end}}
    </ul>
  </section>

  {{if .Warnings}}
  <section class="card">
    <h2>Mode Warnings</h2>
    <ul>
      {{range .Warnings}}<li><code>{{.Mode}}</code>: {{.Warning}}</li>{{end}}
    </ul>
  </section>
  {{end}}

  {{if .Failures}}
  <section class="card">
    <h2>Mode Failures</h2>
    <ul>
      {{range .Failures}}<li><code>{{.Mode}}</code>: {{.Error}}</li>{{end}}
    </ul>
  </section>
  {{end}}

  {{if .Assets}}
  <section class="card">
    <h2>Other Assets</h2>
    <ul>
      {{range .Assets}}<li><a href="{{.}}">{{.}}</a></li>{{end}}
    </ul>
  </section>
  {{end}}

  <section class="card">
    <h2>Charts ({{len .Plots}})</h2>
    {{if .Plots}}
      {{range .Plots}}
      <div class="plot">
        <p><a href="{{.}}">{{.}}</a></p>
        <img loading="lazy" src="{{.}}" alt="{{.}}">
      </div>
      {{end}}
    {{else}}
      <p>No chart files were generated.</p>
    {{end}}
  </section>
</body>
</html>
`

func writeReportIndex(path string, data reportIndexData) error {
	fnMap := template.FuncMap{
		"join": strings.Join,
	}
	tmpl := template.Must(template.New("report-index").Funcs(fnMap).Parse(reportIndexTemplate))
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return tmpl.Execute(file, data)
}

func collectReportManifestFiles(root string) ([]string, error) {
	files := make([]string, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func writeReportManifest(path string, manifest reportManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report manifest: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write report manifest: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(reportCmd)
	reportCmd.SetUsageFunc(reportCmd.UsageFunc())

	reportCmd.Flags().Bool("all", false,
		"Enable all report analysis flags and request all labours modes.")
	reportCmd.Flags().StringP("output", "o", "./report",
		"Output directory for report.pb, chart assets and index.html.")
	reportCmd.Flags().String("format", "png", "Chart output format: png or svg.")
	reportCmd.Flags().Bool("strict", false,
		"Fail immediately if any report mode fails (hard rendering errors; missing-analysis warnings do not count).")
	reportCmd.Flags().StringSlice("analysis", nil,
		"Enable only selected analysis flags (without leading --).")
	reportCmd.Flags().StringSlice("mode", nil,
		"Run only selected labours modes.")
	reportCmd.Flags().StringArray("hercules-arg", nil,
		"Additional argument passed through to the internal hercules run.")
	reportCmd.Flags().StringArray("labours-arg", nil,
		"Additional argument passed through to each labours mode run (requires --labours-cmd).")
	reportCmd.Flags().String("labours-cmd", "",
		"Render with an external drop-in labours command instead of the built-in in-process renderer, "+
			"e.g. \"labours\" or \"/path/to/labours\".")
}
