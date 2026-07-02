// Package render exposes the labours rendering pipeline as an in-process
// API: it turns a hercules analysis result (YAML or protobuf) into rendered
// chart files, mirroring the behavior of the standalone labours CLI.
//
// The typical flow is:
//
//	reader, err := render.LoadInput("analysis.pb", "pb")
//	modes, err := render.ResolveModes([]string{"burndown-project,devs"})
//	render.Run(reader, modes, render.Options{Output: "out.png"})
//
// Known coupling: the mode implementations (and several helpers in this
// package) read additional settings from spf13/viper globals — e.g.
// "relative", "resample", "max-people", "max-repos", "quiet", "backend",
// "temporal-legend-threshold" and friends. Callers that do not go through
// the labours CLI should set the relevant viper keys (or rely on the
// defaults) before calling Run. Options only carries the values that the
// labours CLI does not route through viper.
package render

import (
	"time"

	"github.com/meko-christian/hercules/internal/render/readers"
	"github.com/spf13/viper"
)

// Options carries per-run settings for Run that are not read from viper.
type Options struct {
	// Output is the requested output path (file or directory). An empty
	// value falls back to per-mode default filenames in the current
	// directory. A ".json" extension switches to JSON data output instead
	// of rendered images.
	Output string
	// StartTime and EndTime optionally restrict time-based plots.
	StartTime *time.Time
	EndTime   *time.Time
	// SentimentFallback allows heuristic sentiment charts when collected
	// sentiment data is missing (labours --sentiment-fallback).
	SentimentFallback bool
	// DevsParallelFallback allows synthetic devs-parallel charts when
	// people burndown data is missing (labours --devs-parallel-fallback).
	DevsParallelFallback bool
}

// ModeResult reports the outcome of a single mode executed by
// RunWithResults. For a successful mode both Err and Warning are empty.
// Warning carries the Python-compatible message when a failure was
// downgraded (missing analysis data) or the mode was unavailable, while Err
// reports a hard rendering failure.
type ModeResult struct {
	Mode    string
	Err     error
	Warning string
}

// Run executes the given render modes against reader and writes the
// resulting files, mirroring the labours CLI semantics exactly: progress
// output honors the viper "quiet" key, a ".json" output collects raw data
// for all modes into a single JSON file, unknown or unimplemented modes are
// reported on stdout, and missing-analysis errors are downgraded to
// Python-compatible warnings. Failures in individual modes never abort the
// run, which is why Run does not return an error.
func Run(reader readers.Reader, modeNames []string, opts Options) {
	RunWithResults(reader, modeNames, opts)
}

// RunWithResults behaves exactly like Run (same output, same warnings, no
// early abort) but additionally returns the per-mode outcomes so that
// embedding callers (e.g. `hercules report --strict`) can distinguish hard
// mode failures from Python-compatible missing-analysis warnings.
func RunWithResults(reader readers.Reader, modeNames []string, opts Options) []ModeResult {
	sentimentFallbackEnabled = opts.SentimentFallback
	devsParallelFallbackEnabled = opts.DevsParallelFallback
	return executeModes(modeNames, reader, opts.Output, opts.StartTime, opts.EndTime)
}

// SetRenderDefaults installs viper defaults for every setting the render
// modes read from viper globals, mirroring the flag defaults established by
// the labours CLI (cmd/labours/root.go). Callers that embed the renderer in
// another CLI (e.g. `hercules report`) should call this once before Run or
// RunWithResults. viper.SetDefault has the lowest precedence, so values
// already set or bound to flags are never overridden.
func SetRenderDefaults() {
	viper.SetDefault("relative", false)
	viper.SetDefault("resample", "year")
	viper.SetDefault("max-people", 20)
	viper.SetDefault("max-repos", 25)
	viper.SetDefault("quiet", false)
	viper.SetDefault("backend", "")
	viper.SetDefault("background", "white")
	viper.SetDefault("font-size", 12)
	viper.SetDefault("size", "")
	viper.SetDefault("tmpdir", "")
	viper.SetDefault("temporal-legend-threshold", 32)
	viper.SetDefault("temporal-legend-single-col-threshold", 10)
	viper.SetDefault("order-ownership-by-time", false)
	viper.SetDefault("disable-projector", false)
	viper.SetDefault("run-times-detail", false)
	viper.SetDefault("devs-efforts-detail", false)
	viper.SetDefault("devs-parallel-detail", false)
	viper.SetDefault("knowledge-diffusion-detail", false)
}

// LoadInput builds a Reader from an input path ("-" for stdin) and a format
// hint ("auto", "yaml", or "pb"; empty means "auto"). The input is fully
// read and parsed before returning.
func LoadInput(input, inputFormat string) (readers.Reader, error) {
	format, err := NormalizeInputFormat(inputFormat)
	if err != nil {
		return nil, err
	}
	return readers.DetectAndReadInput(input, format)
}
