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
	"errors"
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/readers"
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

// ModeResult reports the outcome of a single mode executed by Run. For a
// successful mode both Err and Warning are empty.
// Warning carries the Python-compatible message when a failure was
// downgraded because optional analysis data is missing, while Err reports a
// hard rendering failure.
type ModeResult struct {
	Mode    string
	Err     error
	Warning string
}

// Result is the aggregate outcome of a render run. Modes contains one result
// per requested mode. OutputError is reserved for a run-level output failure,
// such as failing to create or write the combined JSON output.
type Result struct {
	Modes       []ModeResult
	OutputError error
}

// Err joins all hard failures in the result. Warning-only runs return nil so
// callers can use the error value as a stable process-status contract without
// parsing diagnostics.
func (r Result) Err() error {
	failures := make([]error, 0, len(r.Modes)+1)
	for _, mode := range r.Modes {
		if mode.Err != nil {
			failures = append(failures, fmt.Errorf("render mode %s: %w", mode.Mode, mode.Err))
		}
	}
	if r.OutputError != nil {
		failures = append(failures, fmt.Errorf("write render output: %w", r.OutputError))
	}
	return errors.Join(failures...)
}

// HasWarnings reports whether any mode completed with an optional-data
// warning.
func (r Result) HasWarnings() bool {
	for _, mode := range r.Modes {
		if mode.Warning != "" {
			return true
		}
	}
	return false
}

// Run executes the given render modes against reader and writes the
// resulting files. Independent modes continue after a failure. Typed
// missing-analysis errors are warnings; all other mode and output failures
// are retained in the returned aggregate result.
func Run(reader readers.Reader, modeNames []string, opts Options) Result {
	sentimentFallbackEnabled = opts.SentimentFallback
	devsParallelFallbackEnabled = opts.DevsParallelFallback
	return executeModes(modeNames, reader, opts.Output, opts.StartTime, opts.EndTime)
}

// RunWithResults is retained for source compatibility. New callers should
// use Run.
func RunWithResults(reader readers.Reader, modeNames []string, opts Options) []ModeResult {
	return Run(reader, modeNames, opts).Modes
}

// SetRenderDefaults installs viper defaults for every setting the render
// modes read from viper globals, mirroring the flag defaults established by
// the labours CLI (cmd/labours/root.go). Callers that embed the renderer in
// another CLI (e.g. `hercules report`) should call this once before Run or
// Run. viper.SetDefault has the lowest precedence, so values
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
