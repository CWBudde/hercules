package modes

import "github.com/cwbudde/hercules/internal/render/graphics"

// Options is the explicit configuration passed from a Renderer to mode
// handlers. It deliberately has no dependency on Viper or another global
// configuration store.
type Options struct {
	Quiet                      bool
	Relative                   bool
	Resample                   string
	MaxPeople                  int
	MaxRepos                   int
	TempDir                    string
	TemporalLegendThreshold    int
	TemporalLegendSingleColumn int
	OrderOwnershipByTime       bool
	DisableProjector           bool
	RunTimesDetail             bool
	DevsEffortsDetail          bool
	DevsParallelDetail         bool
	KnowledgeDiffusionDetail   bool
	SentimentFallback          bool
	DevsParallelFallback       bool
	Graphics                   graphics.Options
}

func defaultOptions() Options {
	return Options{
		Resample:                   "year",
		MaxPeople:                  20,
		MaxRepos:                   25,
		TemporalLegendThreshold:    32,
		TemporalLegendSingleColumn: 10,
		Graphics:                   graphics.DefaultOptions(),
	}
}
