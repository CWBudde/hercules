package plumbing

import (
	"errors"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
)

// LinesStatsCalculator measures line statistics for each text file in the commit.
type LinesStatsCalculator struct {
	core.NoopMerger

	l core.Logger
}

// LineStats holds the numbers of inserted, deleted and changed lines.
type LineStats struct {
	// Added is the number of added lines by a particular developer in a particular day.
	Added int
	// Removed is the number of removed lines by a particular developer in a particular day.
	Removed int
	// Changed is the number of changed lines by a particular developer in a particular day.
	Changed int
}

const (
	// DependencyLineStats is the identifier of the data provided by LinesStatsCalculator - line
	// statistics for each file in the commit.
	DependencyLineStats = "line_stats"
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (lsc *LinesStatsCalculator) Name() string {
	return "LinesStats"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (lsc *LinesStatsCalculator) Provides() []string {
	return []string{DependencyLineStats}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (lsc *LinesStatsCalculator) Requires() []string {
	return []string{DependencyTreeChanges, DependencyBlobCache, DependencyFileDiff}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (lsc *LinesStatsCalculator) ListConfigurationOptions() []core.ConfigurationOption {
	return nil
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (lsc *LinesStatsCalculator) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		lsc.l = l
	}

	return nil
}

func (*LinesStatsCalculator) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (lsc *LinesStatsCalculator) Initialize(repository *git.Repository) error {
	lsc.l = core.NewLogger()
	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (lsc *LinesStatsCalculator) Consume(deps map[string]any) (map[string]any, error) {
	result := map[object.ChangeEntry]LineStats{}
	treeDiff := deps[DependencyTreeChanges].(object.Changes)
	cache := deps[DependencyBlobCache].(map[plumbing.Hash]*CachedBlob)
	fileDiffs := deps[DependencyFileDiff].(map[string]FileDiffData)

	for _, change := range treeDiff {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		// Skip binary files completely; they do not contribute line counts.
		if action == merkletrie.Modify {
			binary, err := modificationIsBinary(change, cache)
			if err != nil {
				return nil, err
			}

			if binary {
				continue
			}
		}

		switch action {
		case merkletrie.Insert:
			blob := cache[change.To.TreeEntry.Hash]

			lines, err := blob.CountLines()
			if err != nil {
				// binary
				continue
			}

			result[change.To] = LineStats{
				Added:   lines,
				Removed: 0,
				Changed: 0,
			}
		case merkletrie.Delete:
			blob := cache[change.From.TreeEntry.Hash]

			lines, err := blob.CountLines()
			if err != nil {
				// binary
				continue
			}

			result[change.From] = LineStats{
				Added:   0,
				Removed: lines,
				Changed: 0,
			}
		case merkletrie.Modify:
			result[change.To] = countModifiedLines(fileDiffs[change.To.Name])
		}
	}

	return map[string]any{DependencyLineStats: result}, nil
}

func modificationIsBinary(
	change *object.Change, cache map[plumbing.Hash]*CachedBlob,
) (bool, error) {
	for _, hash := range []plumbing.Hash{change.From.TreeEntry.Hash, change.To.TreeEntry.Hash} {
		_, err := cache[hash].CountLines()
		if errors.Is(err, ErrBinary) {
			return true, nil
		}

		if err != nil {
			return false, err
		}
	}

	return false, nil
}

func countModifiedLines(fileDiff FileDiffData) LineStats {
	var stats LineStats
	removedPending := 0

	for _, edit := range fileDiff.Diffs {
		switch edit.Type {
		case diffmatchpatch.DiffEqual:
			stats.Removed += removedPending
			removedPending = 0
		case diffmatchpatch.DiffInsert:
			delta := utf8.RuneCountInString(edit.Text)
			stats.Changed += min(removedPending, delta)
			stats.Removed += max(removedPending-delta, 0)
			stats.Added += max(delta-removedPending, 0)
			removedPending = 0
		case diffmatchpatch.DiffDelete:
			removedPending = utf8.RuneCountInString(edit.Text)
		}
	}

	stats.Removed += removedPending

	return stats
}

// Fork clones this PipelineItem.
func (lsc *LinesStatsCalculator) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(lsc, n)
}

func init() {
	core.Registry.Register(&LinesStatsCalculator{})
}
