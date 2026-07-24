package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/cobra"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/render"
)

var reportDefaultAnalysisFlags = []string{
	"burndown",
	"burndown-files",
	"burndown-people",
	"couples",
	"devs",
	"temporal-activity",
	"bus-factor",
	"ownership-concentration",
	"knowledge-diffusion",
	"onboarding",
	"hotspot-risk",
	"refactoring-proxy",
}

var reportAllAnalysisFlags = []string{
	"burndown",
	"burndown-files",
	"burndown-people",
	"couples",
	"shotness",
	"devs",
	"temporal-activity",
	"bus-factor",
	"ownership-concentration",
	"knowledge-diffusion",
	"onboarding",
	"hotspot-risk",
	"refactoring-proxy",
	"sentiment",
}

var reportDefaultModes = []string{
	"burndown-project",
	"burndown-file",
	"burndown-person",
	"overwrites-matrix",
	"ownership",
	"couples-files",
	"couples-people",
	"devs",
	"devs-efforts",
	"old-vs-new",
	"languages",
	"temporal-activity",
	"bus-factor",
	"ownership-concentration",
	"knowledge-diffusion",
	"hotspot-risk",
	"refactoring-proxy",
}

var reportAllModes = []string{
	"burndown-project",
	"burndown-file",
	"burndown-person",
	"burndown-repository",
	"burndown-repos-combined",
	"overwrites-matrix",
	"ownership",
	"couples-files",
	"couples-people",
	"couples-shotness",
	"shotness",
	"sentiment",
	"temporal-activity",
	"devs",
	"devs-efforts",
	"old-vs-new",
	"languages",
	"devs-parallel",
	"bus-factor",
	"ownership-concentration",
	"knowledge-diffusion",
	"hotspot-risk",
	"refactoring-proxy",
}

var reportValidModes = map[string]struct{}{
	"burndown-project":        {},
	"burndown-file":           {},
	"burndown-person":         {},
	"burndown-repository":     {},
	"burndown-repos-combined": {},
	"overwrites-matrix":       {},
	"ownership":               {},
	"couples-files":           {},
	"couples-people":          {},
	"couples-shotness":        {},
	"shotness":                {},
	"sentiment":               {},
	"temporal-activity":       {},
	"devs":                    {},
	"devs-efforts":            {},
	"old-vs-new":              {},
	"languages":               {},
	"devs-parallel":           {},
	"bus-factor":              {},
	"ownership-concentration": {},
	"knowledge-diffusion":     {},
	"hotspot-risk":            {},
	"refactoring-proxy":       {},
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

	reportPB, message, err := generateReportInput(options, analysisFlags)
	if err != nil {
		return err
	}
	modeResults, err := renderReportModes(options, reportPB, modes)
	if err != nil {
		return err
	}
	return finalizeReport(options, message, analysisFlags, modes, modeResults)
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

func generateReportInput(options reportOptions, analysisFlags []string) (
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
	payload, err := runAndCapture(os.Args[0], herculesArgs, nil)
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

func renderReportModes(options reportOptions, reportPB string, modes []string) ([]reportModeFailure, error) {
	chartsRoot := filepath.Join(options.outputDir, "charts")
	if err := os.MkdirAll(chartsRoot, 0o755); err != nil {
		return nil, err
	}
	if options.laboursCommand == "" {
		return renderReportInProcess(options, reportPB, chartsRoot, modes)
	}
	return renderReportExternally(options, reportPB, chartsRoot, modes)
}

func renderReportInProcess(options reportOptions, reportPB, chartsRoot string,
	modes []string,
) ([]reportModeFailure, error) {
	render.SetRenderDefaults()
	reader, err := render.LoadInput(reportPB, "pb")
	if err != nil {
		return nil, fmt.Errorf("load generated protobuf report for rendering: %w", err)
	}
	var failures []reportModeFailure
	for _, mode := range modes {
		output := reportModeOutput(chartsRoot, mode, options.format)
		_, _ = fmt.Fprintf(os.Stderr, "report: rendering mode %s...\n", mode)
		for _, result := range render.RunWithResults(reader, []string{mode}, render.Options{Output: output}) {
			if result.Err == nil {
				continue
			}
			failures = append(failures, reportModeFailure{Mode: mode, Error: result.Err.Error()})
			if options.strict {
				return nil, fmt.Errorf("render mode %s failed: %w", mode, result.Err)
			}
		}
	}
	return failures, nil
}

func renderReportExternally(options reportOptions, reportPB, chartsRoot string,
	modes []string,
) ([]reportModeFailure, error) {
	command, err := resolveLaboursCommand(options.laboursCommand)
	if err != nil {
		return nil, err
	}
	var failures []reportModeFailure
	for _, mode := range modes {
		args := externalReportModeArgs(
			command, options.laboursExtra, reportPB, reportModeOutput(chartsRoot, mode, options.format), mode,
		)
		_, _ = fmt.Fprintf(os.Stderr, "report: running labours mode %s...\n", mode)
		if err := runAndCaptureTo(os.Stderr, command[0], args, nil); err != nil {
			failures = append(failures, reportModeFailure{Mode: mode, Error: err.Error()})
			if options.strict {
				return nil, fmt.Errorf("labours mode %s failed: %w", mode, err)
			}
		}
	}
	return failures, nil
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
	modeResults []reportModeFailure,
) error {
	plots, assets, err := collectReportAssets(options.outputDir)
	if err != nil {
		return err
	}

	indexFile := filepath.Join(options.outputDir, "index.html")
	indexData := newReportIndexData(message, analysisFlags, modes, modeResults, plots, assets, options.format)
	if err := writeReportIndex(indexFile, indexData); err != nil {
		return err
	}

	if len(modeResults) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "report: %d mode(s) failed. See index.html for details.\n", len(modeResults))
	}
	_, _ = fmt.Fprintf(os.Stderr, "report: done. Open %s\n", indexFile)
	return nil
}

// reportAnalysisFlagParents maps analysis flags that are sub-options of another
// leaf (not registry leaves themselves) to the leaf whose availability governs them.
var reportAnalysisFlagParents = map[string]string{
	"burndown-files":  "burndown",
	"burndown-people": "burndown",
}

func selectReportAnalysisFlags(
	available map[string]struct{}, requested []string, includeAll bool,
) ([]string, error) {
	source, strictSelection := reportAnalysisSource(requested, includeAll)

	set := map[string]struct{}{}
	for _, flag := range source {
		if !reportAnalysisSupported(flag) {
			if strictSelection {
				return nil, fmt.Errorf("analysis flag %q is unavailable in this build; rebuild with -tags tensorflow", flag)
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
	return flag != "sentiment" || tensorflowEnabled
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
		return errors.New("--labours-arg requires --labours-cmd: the built-in renderer does not accept extra labours arguments")
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

func runAndCapture(command string, args, env []string) ([]byte, error) {
	cmd := exec.Command(command, args...)
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

func runAndCaptureTo(writer *os.File, command string, args, env []string) error {
	cmd := exec.Command(command, args...)
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
	Mode  string
	Error string
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
	Failures    []reportModeFailure
	Plots       []string
	Assets      []string
	Format      string
}

func newReportIndexData(
	message pb.AnalysisResults,
	analysisFlags []string,
	modes []string,
	modeResults []reportModeFailure,
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
		Failures:    modeResults,
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
		"Render with an external drop-in labours command instead of the built-in in-process renderer, e.g. \"labours\" or \"/path/to/labours\".")
}
