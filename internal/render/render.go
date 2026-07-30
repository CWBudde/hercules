// Package render exposes the labours rendering pipeline as an in-process
// API: it turns a hercules analysis result (YAML or protobuf) into rendered
// chart files, mirroring the behavior of the standalone labours CLI.
//
// The typical flow is:
//
//	reader, err := render.LoadInput("analysis.pb", "pb")
//	modes, err := render.ResolveModes([]string{"burndown-project,devs"})
//	render.Run(reader, modes, render.Options{Output: "out.png"})
package render

import (
	"errors"
	"fmt"
	"time"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/modes"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// Options is the complete, instance-scoped renderer configuration. NewRenderer
// copies it, including the theme palette and optional time bounds, so callers
// may safely reuse or mutate their input after construction.
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

	Quiet      bool
	Relative   bool
	Resample   string
	MaxPeople  int
	MaxRepos   int
	Backend    string
	Background string
	FontSize   int
	Size       string
	TempDir    string
	// NoBurndownTitle suppresses titles on burndown and ownership charts.
	NoBurndownTitle bool

	TemporalLegendThreshold    int
	TemporalLegendSingleColumn int
	OrderOwnershipByTime       bool
	DisableProjector           bool
	RunTimesDetail             bool
	DevsEffortsDetail          bool
	DevsParallelDetail         bool
	KnowledgeDiffusionDetail   bool
	Theme                      graphics.Theme
}

type outputPolicy struct {
	backend string
}

type fallbackPolicy struct {
	sentiment    bool
	devsParallel bool
}

// Renderer is a reusable rendering instance with an immutable configuration
// snapshot. It contains no process-global state.
type Renderer struct {
	options   Options
	theme     graphics.Theme
	output    outputPolicy
	fallbacks fallbackPolicy
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
	renderer, err := NewRenderer(opts)
	if err != nil {
		return Result{OutputError: fmt.Errorf("configure renderer: %w", err)}
	}

	return renderer.Render(reader, modeNames)
}

// RunWithResults is retained for source compatibility. New callers should
// use Run.
func RunWithResults(reader readers.Reader, modeNames []string, opts Options) []ModeResult {
	return Run(reader, modeNames, opts).Modes
}

// DefaultOptions returns the standalone renderer defaults used by labours.
func DefaultOptions() Options {
	return Options{
		Resample:                   "year",
		MaxPeople:                  20,
		MaxRepos:                   25,
		Background:                 "white",
		FontSize:                   12,
		TemporalLegendThreshold:    32,
		TemporalLegendSingleColumn: 10,
		Theme:                      graphics.DefaultTheme,
	}
}

// SetRenderDefaults is retained for source compatibility. Configuration is
// now instance-scoped; use DefaultOptions and override the desired fields.
func SetRenderDefaults() {}

// NewRenderer validates and snapshots opts.
func NewRenderer(opts Options) (*Renderer, error) {
	opts = normalizeOptions(opts)

	err := opts.Theme.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid renderer theme: %w", err)
	}

	opts.Theme = cloneTheme(opts.Theme)
	opts.StartTime = cloneTime(opts.StartTime)
	opts.EndTime = cloneTime(opts.EndTime)

	return &Renderer{
		options: opts,
		theme:   opts.Theme,
		output: outputPolicy{
			backend: opts.Backend,
		},
		fallbacks: fallbackPolicy{
			sentiment:    opts.SentimentFallback,
			devsParallel: opts.DevsParallelFallback,
		},
	}, nil
}

// Render executes modeNames with this renderer's immutable configuration.
func (r *Renderer) Render(reader readers.Reader, modeNames []string) Result {
	return r.executeModes(modeNames, reader)
}

func normalizeOptions(opts Options) Options {
	defaults := DefaultOptions()
	if opts.Resample == "" {
		opts.Resample = defaults.Resample
	}

	if opts.MaxPeople == 0 {
		opts.MaxPeople = defaults.MaxPeople
	}

	if opts.MaxRepos == 0 {
		opts.MaxRepos = defaults.MaxRepos
	}

	if opts.Background == "" {
		opts.Background = defaults.Background
	}

	if opts.FontSize <= 0 {
		opts.FontSize = defaults.FontSize
	}

	if opts.TemporalLegendThreshold == 0 {
		opts.TemporalLegendThreshold = defaults.TemporalLegendThreshold
	}

	if opts.TemporalLegendSingleColumn == 0 {
		opts.TemporalLegendSingleColumn = defaults.TemporalLegendSingleColumn
	}

	if opts.Theme.Name == "" {
		opts.Theme = defaults.Theme
	}

	return opts
}

func cloneTheme(theme graphics.Theme) graphics.Theme {
	theme.ColorPalette = append([]graphics.ColorRGB(nil), theme.ColorPalette...)
	return theme
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	cloned := *value

	return &cloned
}

func (r *Renderer) modeOptions() modes.Options {
	return modes.Options{
		Quiet:                      r.options.Quiet,
		Relative:                   r.options.Relative,
		Resample:                   r.options.Resample,
		MaxPeople:                  r.options.MaxPeople,
		MaxRepos:                   r.options.MaxRepos,
		TempDir:                    r.options.TempDir,
		TemporalLegendThreshold:    r.options.TemporalLegendThreshold,
		TemporalLegendSingleColumn: r.options.TemporalLegendSingleColumn,
		OrderOwnershipByTime:       r.options.OrderOwnershipByTime,
		DisableProjector:           r.options.DisableProjector,
		RunTimesDetail:             r.options.RunTimesDetail,
		DevsEffortsDetail:          r.options.DevsEffortsDetail,
		DevsParallelDetail:         r.options.DevsParallelDetail,
		KnowledgeDiffusionDetail:   r.options.KnowledgeDiffusionDetail,
		SentimentFallback:          r.fallbacks.sentiment,
		DevsParallelFallback:       r.fallbacks.devsParallel,
		Graphics: graphics.Options{
			Theme:      cloneTheme(r.theme),
			FontSize:   r.options.FontSize,
			Background: r.options.Background,
			Size:       r.options.Size,
			HideTitle:  r.options.NoBurndownTitle,
		},
	}
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
