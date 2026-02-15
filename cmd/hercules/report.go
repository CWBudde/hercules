package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules"
	"github.com/meko-christian/hercules/internal/pb"
	"github.com/spf13/cobra"
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
	"hotspot-risk",
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
	"hotspot-risk",
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
}

// reportCmd generates a complete labours report in one command.
var reportCmd = &cobra.Command{
	Use:   "report [flags] <repository> [cache-path]",
	Short: "Generate a complete report directory with charts and summary.",
	Long: `Runs Hercules in Protocol Buffers mode, invokes labours internally and writes
an output directory with generated chart assets and index.html summary.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		outputDir, err := flags.GetString("output")
		if err != nil {
			return err
		}
		if outputDir == "" {
			return fmt.Errorf("--output must not be empty")
		}
		allAnalyses, err := flags.GetBool("all")
		if err != nil {
			return err
		}
		requestedAnalyses, err := flags.GetStringSlice("analysis")
		if err != nil {
			return err
		}
		requestedModes, err := flags.GetStringSlice("mode")
		if err != nil {
			return err
		}
		format, err := flags.GetString("format")
		if err != nil {
			return err
		}
		format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
		if format != "png" && format != "svg" {
			return fmt.Errorf("unsupported --format %q: expected png or svg", format)
		}
		strict, err := flags.GetBool("strict")
		if err != nil {
			return err
		}
		herculesExtra, err := flags.GetStringArray("hercules-arg")
		if err != nil {
			return err
		}
		laboursExtra, err := flags.GetStringArray("labours-arg")
		if err != nil {
			return err
		}
		laboursCmdOverride, err := flags.GetString("labours-cmd")
		if err != nil {
			return err
		}

		availableAnalysisFlags := make(map[string]struct{})
		for _, leaf := range hercules.Registry.GetLeaves() {
			flag := leaf.Flag()
			if flag != "" {
				availableAnalysisFlags[flag] = struct{}{}
			}
		}
		analysisFlags, err := selectReportAnalysisFlags(availableAnalysisFlags, requestedAnalyses, allAnalyses)
		if err != nil {
			return err
		}
		modes, err := selectReportModes(requestedModes, allAnalyses)
		if err != nil {
			return err
		}

		outputDir = filepath.Clean(outputDir)
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return err
		}
		reportPB := filepath.Join(outputDir, "report.pb")

		herculesArgs := make([]string, 0, len(analysisFlags)+len(herculesExtra)+len(args)+3)
		herculesArgs = append(herculesArgs, "--pb", "--quiet")
		for _, flag := range analysisFlags {
			herculesArgs = append(herculesArgs, "--"+flag)
		}
		herculesArgs = append(herculesArgs, herculesExtra...)
		herculesArgs = append(herculesArgs, args...)

		_, _ = fmt.Fprintf(os.Stderr, "report: running hercules (%d analysis flags)...\n", len(analysisFlags))
		pbPayload, err := runAndCapture(os.Args[0], herculesArgs, nil)
		if err != nil {
			return fmt.Errorf("failed to run hercules for report: %w", err)
		}
		if err := os.WriteFile(reportPB, pbPayload, 0o644); err != nil {
			return err
		}

		var pbMessage pb.AnalysisResults
		if err := proto.Unmarshal(pbPayload, &pbMessage); err != nil {
			return fmt.Errorf("failed to parse generated protobuf report: %w", err)
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		laboursCmd, extraEnv, err := resolveLaboursCommand(cwd, laboursCmdOverride)
		if err != nil {
			return err
		}

		chartsRoot := filepath.Join(outputDir, "charts")
		if err := os.MkdirAll(chartsRoot, 0o755); err != nil {
			return err
		}

		var modeResults []reportModeFailure
		for _, mode := range modes {
			modeOutput := filepath.Join(chartsRoot, sanitizePathComponent(mode)+"."+format)
			cmdArgs := make([]string, 0, len(laboursCmd)+len(laboursExtra)+8)
			cmdArgs = append(cmdArgs, laboursCmd[1:]...)
			cmdArgs = append(cmdArgs,
				"-f", "pb",
				"-i", reportPB,
				"-o", modeOutput,
				"-m", mode,
				"--backend", "Agg",
			)
			cmdArgs = append(cmdArgs, laboursExtra...)
			_, _ = fmt.Fprintf(os.Stderr, "report: running labours mode %s...\n", mode)
			if _, err := runAndCaptureTo(os.Stderr, laboursCmd[0], cmdArgs, extraEnv); err != nil {
				modeResults = append(modeResults, reportModeFailure{Mode: mode, Error: err.Error()})
				if strict {
					return fmt.Errorf("labours mode %s failed: %w", mode, err)
				}
			}
		}

		plots, assets, err := collectReportAssets(outputDir)
		if err != nil {
			return err
		}

		indexFile := filepath.Join(outputDir, "index.html")
		indexData := newReportIndexData(pbMessage, analysisFlags, modes, modeResults, plots, assets, format)
		if err := writeReportIndex(indexFile, indexData); err != nil {
			return err
		}

		if len(modeResults) > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "report: %d mode(s) failed. See index.html for details.\n", len(modeResults))
		}
		_, _ = fmt.Fprintf(os.Stderr, "report: done. Open %s\n", indexFile)
		return nil
	},
}

func selectReportAnalysisFlags(
	available map[string]struct{}, requested []string, includeAll bool,
) ([]string, error) {
	set := map[string]struct{}{}
	isSupportedInBuild := func(flag string) bool {
		if flag == "sentiment" && !tensorflowEnabled {
			return false
		}
		return true
	}
	if includeAll {
		for _, flag := range reportAllAnalysisFlags {
			if !isSupportedInBuild(flag) {
				continue
			}
			if _, exists := available[flag]; exists {
				set[flag] = struct{}{}
			}
		}
	} else if len(requested) > 0 {
		for _, flag := range requested {
			if !isSupportedInBuild(flag) {
				return nil, fmt.Errorf("analysis flag %q is unavailable in this build; rebuild with -tags tensorflow", flag)
			}
			if _, exists := available[flag]; !exists {
				return nil, fmt.Errorf("unknown analysis flag %q", flag)
			}
			set[flag] = struct{}{}
		}
	} else {
		for _, flag := range reportDefaultAnalysisFlags {
			if !isSupportedInBuild(flag) {
				continue
			}
			if _, exists := available[flag]; exists {
				set[flag] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for flag := range set {
		result = append(result, flag)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, fmt.Errorf("no analysis flags selected for report")
	}
	return result, nil
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

func resolveLaboursCommand(cwd string, override string) ([]string, []string, error) {
	if override != "" {
		parts := strings.Fields(override)
		if len(parts) == 0 {
			return nil, nil, fmt.Errorf("--labours-cmd is empty")
		}
		return parts, nil, nil
	}
	if _, err := exec.LookPath("labours"); err == nil {
		return []string{"labours"}, nil, nil
	}
	if _, err := exec.LookPath("python3"); err == nil {
		localPython := filepath.Join(cwd, "python")
		laboursPkg := filepath.Join(localPython, "labours")
		if stat, statErr := os.Stat(laboursPkg); statErr == nil && stat.IsDir() {
			return []string{"python3", "-m", "labours"}, []string{prependPythonPath(localPython)}, nil
		}
		return []string{"python3", "-m", "labours"}, nil, nil
	}
	return nil, nil, fmt.Errorf("labours was not found in PATH and python3 is unavailable")
}

func prependPythonPath(path string) string {
	existing := os.Getenv("PYTHONPATH")
	if existing == "" {
		return "PYTHONPATH=" + path
	}
	return "PYTHONPATH=" + path + string(os.PathListSeparator) + existing
}

func runAndCapture(command string, args []string, env []string) ([]byte, error) {
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

func runAndCaptureTo(writer *os.File, command string, args []string, env []string) ([]byte, error) {
	cmd := exec.Command(command, args...)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", command, strings.Join(args, " "), err)
	}
	return nil, nil
}

func sanitizePathComponent(value string) string {
	if value == "" {
		return "chart"
	}
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
		case ch == '.', ch == '-', ch == '_':
			builder.WriteRune(ch)
		default:
			builder.WriteRune('_')
		}
	}
	return builder.String()
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
	version := int32(hercules.BinaryVersion)
	gitHash := hercules.BinaryGitHash
	commits := int32(0)
	runtimeMS := int64(0)

	if message.Header != nil {
		repository = message.Header.Repository
		version = message.Header.Version
		gitHash = message.Header.Hash
		commits = message.Header.Commits
		runtimeMS = message.Header.RunTime
		if message.Header.BeginUnixTime > 0 {
			begin = time.Unix(message.Header.BeginUnixTime, 0).UTC().Format(time.RFC3339)
		}
		if message.Header.EndUnixTime > 0 {
			end = time.Unix(message.Header.EndUnixTime, 0).UTC().Format(time.RFC3339)
		}
	}

	analyses := make([]string, 0, len(message.Contents))
	for key := range message.Contents {
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
		"Fail immediately if any labours mode fails.")
	reportCmd.Flags().StringSlice("analysis", nil,
		"Enable only selected analysis flags (without leading --).")
	reportCmd.Flags().StringSlice("mode", nil,
		"Run only selected labours modes.")
	reportCmd.Flags().StringArray("hercules-arg", nil,
		"Additional argument passed through to the internal hercules run.")
	reportCmd.Flags().StringArray("labours-arg", nil,
		"Additional argument passed through to each labours mode run.")
	reportCmd.Flags().String("labours-cmd", "",
		"Override labours launcher, e.g. \"labours\" or \"python3 -m labours\".")
}
