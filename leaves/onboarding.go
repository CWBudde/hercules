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
