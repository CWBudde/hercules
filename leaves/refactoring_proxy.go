package leaves

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/yaml"
)

const (
	// ConfigRefactoringThreshold is the name of the configuration option
	ConfigRefactoringThreshold = "RefactoringProxy.Threshold"
)

// tickChangeMetrics tracks changes for one tick
type tickChangeMetrics struct {
	TotalChanges int // All additions, modifications, deletions, renames
	Renames      int // Changes where From.Name != To.Name (both non-empty)
}

// RefactoringProxyResult is returned by RefactoringProxy.Finalize()
type RefactoringProxyResult struct {
	// Time series data (parallel arrays, same length)
	Ticks        []int       // Tick indices
	RenameRatios []float64   // Rename ratio per tick (0.0 to 1.0)
	IsRefactoring []bool      // true if ratio > threshold
	TotalChanges []int       // Total changes per tick (for context)

	// Configuration metadata
	Threshold float64       // The threshold used for classification
	TickSize  time.Duration
}

// RefactoringProxy measures rename/move rate to identify refactoring phases
type RefactoringProxy struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// RefactoringThreshold is the rename ratio threshold (0.0-1.0)
	RefactoringThreshold float64

	// tickMetrics maps tick -> metrics
	tickMetrics map[int]*tickChangeMetrics
	tickSize    time.Duration

	l core.Logger
}

// Name of this PipelineItem
func (rp *RefactoringProxy) Name() string {
	return "RefactoringProxy"
}

// Provides returns entities produced
func (rp *RefactoringProxy) Provides() []string {
	return []string{}
}

// Requires returns entities needed
func (rp *RefactoringProxy) Requires() []string {
	return []string{
		items.DependencyTreeChanges,
		items.DependencyTick,
	}
}

// Flag for command line switch
func (rp *RefactoringProxy) Flag() string {
	return "refactoring-proxy"
}

// Description explains what the analysis does
func (rp *RefactoringProxy) Description() string {
	return "Tracks rename/move rate over time to distinguish refactoring phases from feature work."
}

// ListConfigurationOptions returns changeable properties
func (rp *RefactoringProxy) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigRefactoringThreshold,
			Description: "Rename ratio threshold to classify a tick as refactoring-heavy (0.0-1.0).",
			Flag:        "refactoring-threshold",
			Type:        core.FloatConfigurationOption,
			Default:     0.5,
		},
	}
	return options[:]
}

// Configure sets properties
func (rp *RefactoringProxy) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		rp.l = l
	}
	if val, exists := facts[ConfigRefactoringThreshold].(float64); exists {
		rp.RefactoringThreshold = val
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		rp.tickSize = val
	}
	return nil
}

// ConfigureUpstream configures upstream dependencies
func (*RefactoringProxy) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}
