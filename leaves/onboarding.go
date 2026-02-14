package leaves

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
)

// onboardingTickMetrics tracks activity for one author at one tick
type onboardingTickMetrics struct {
	Commits      int
	Files        map[string]bool // set of unique files
	LinesAdded   int
	LinesRemoved int
	LinesChanged int

	// Filtered metrics (meaningful commits only)
	MeaningfulCommits      int
	MeaningfulFiles        map[string]bool
	MeaningfulLinesAdded   int
	MeaningfulLinesRemoved int
	MeaningfulLinesChanged int
}

// OnboardingAnalysis measures how quickly new contributors ramp up
type OnboardingAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// WindowDays specifies milestone days for snapshots (e.g., [7, 30, 90])
	WindowDays []int
	// MeaningfulThreshold is minimum lines for "meaningful" commit
	MeaningfulThreshold int

	// author -> tick -> metrics
	authorTimeline     map[int]map[int]*onboardingTickMetrics
	reversedPeopleDict []string
	tickSize           time.Duration

	l core.Logger
}

const (
	// ConfigOnboardingWindows is the name of the option to set OnboardingAnalysis.WindowDays
	ConfigOnboardingWindows = "Onboarding.Windows"
	// ConfigOnboardingMeaningfulThreshold is the name of the option to set OnboardingAnalysis.MeaningfulThreshold
	ConfigOnboardingMeaningfulThreshold = "Onboarding.MeaningfulThreshold"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (oa *OnboardingAnalysis) Name() string {
	return "Onboarding"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (oa *OnboardingAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (oa *OnboardingAnalysis) Requires() []string {
	return []string{
		identity.DependencyAuthor,
		items.DependencyTick,
		items.DependencyLineStats,
		items.DependencyTreeChanges,
	}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (oa *OnboardingAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigOnboardingWindows,
			Description: "Comma-separated list of days for snapshot milestones (e.g., '7,30,90').",
			Flag:        "onboarding-windows",
			Type:        core.StringConfigurationOption,
			Default:     "7,30,90",
		},
		{
			Name:        ConfigOnboardingMeaningfulThreshold,
			Description: "Minimum lines changed for a commit to count as 'meaningful'.",
			Flag:        "onboarding-meaningful-threshold",
			Type:        core.IntConfigurationOption,
			Default:     10,
		},
	}
	return options[:]
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (oa *OnboardingAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		oa.l = l
	}
	if val, exists := facts[ConfigOnboardingWindows].(string); exists {
		// Parse comma-separated window days
		parts := strings.Split(val, ",")
		oa.WindowDays = make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			days, err := strconv.Atoi(p)
			if err != nil {
				return fmt.Errorf("invalid window days value '%s': %w", p, err)
			}
			oa.WindowDays = append(oa.WindowDays, days)
		}
		sort.Ints(oa.WindowDays)
	}
	if val, exists := facts[ConfigOnboardingMeaningfulThreshold].(int); exists {
		oa.MeaningfulThreshold = val
	}
	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		oa.reversedPeopleDict = val
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		oa.tickSize = val
	}
	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*OnboardingAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (oa *OnboardingAnalysis) Flag() string {
	return "onboarding"
}

// Description returns the text which explains what the analysis is doing.
func (oa *OnboardingAnalysis) Description() string {
	return "Measures how quickly new contributors ramp up: time-to-first-change, breadth-of-files in first N days, convergence to stable contribution patterns."
}
