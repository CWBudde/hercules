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
