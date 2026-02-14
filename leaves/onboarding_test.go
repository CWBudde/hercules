package leaves

import (
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestDeps creates test dependencies for Consume
func makeTestDeps(author, tick int, files map[string]int) map[string]interface{} {
	deps := map[string]interface{}{
		core.DependencyCommit:       &object.Commit{},
		identity.DependencyAuthor:   author,
		items.DependencyTick:        tick,
		items.DependencyTreeChanges: object.Changes{},
		items.DependencyLineStats:   map[object.ChangeEntry]items.LineStats{},
	}

	changes := deps[items.DependencyTreeChanges].(object.Changes)
	lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)

	for fileName, lines := range files {
		change := &object.Change{
			To: object.ChangeEntry{
				Name: fileName,
				TreeEntry: object.TreeEntry{
					Hash: plumbing.NewHash(fileName),
				},
			},
		}
		changes = append(changes, change)

		lineStats[change.To] = items.LineStats{
			Added:   lines,
			Removed: 0,
			Changed: 0,
		}
	}

	deps[items.DependencyTreeChanges] = changes
	deps[items.DependencyLineStats] = lineStats

	return deps
}

func TestOnboardingAnalysis_BasicTracking(t *testing.T) {
	oa := &OnboardingAnalysis{
		WindowDays:          []int{7, 30, 90},
		MeaningfulThreshold: 10,
		tickSize:            24 * time.Hour,
	}

	require.NoError(t, oa.Initialize(test.Repository))

	author := 0

	// Commit 1 at tick 0: 2 files, 15 lines each
	deps1 := makeTestDeps(author, 0, map[string]int{
		"file1.go": 15,
		"file2.go": 15,
	})
	_, err := oa.Consume(deps1)
	require.NoError(t, err)

	// Commit 2 at tick 3: 1 file, 20 lines
	deps2 := makeTestDeps(author, 3, map[string]int{
		"file3.go": 20,
	})
	_, err = oa.Consume(deps2)
	require.NoError(t, err)

	// Commit 3 at tick 40 (after 40 days): 1 file, 25 lines
	deps3 := makeTestDeps(author, 40, map[string]int{
		"file4.go": 25,
	})
	_, err = oa.Consume(deps3)
	require.NoError(t, err)

	// Finalize
	result := oa.Finalize().(OnboardingResult)

	// Verify author data
	require.Contains(t, result.Authors, author)
	authorData := result.Authors[author]

	assert.Equal(t, 0, authorData.FirstCommitTick)

	// Check 7-day snapshot (should capture first 2 commits)
	require.Contains(t, authorData.Snapshots, 7)
	snap7 := authorData.Snapshots[7]
	assert.Equal(t, 2, snap7.TotalCommits)
	assert.Equal(t, 3, snap7.TotalFiles)
	assert.Equal(t, 50, snap7.TotalLines) // 15+15+20
	assert.Equal(t, 2, snap7.MeaningfulCommits)

	// Check 30-day snapshot (should capture first 2 commits, commit 3 is at day 40)
	require.Contains(t, authorData.Snapshots, 30)
	snap30 := authorData.Snapshots[30]
	assert.Equal(t, 2, snap30.TotalCommits)
	assert.Equal(t, 3, snap30.TotalFiles)

	// Check 90-day snapshot (should capture all 3 commits)
	require.Contains(t, authorData.Snapshots, 90)
	snap90 := authorData.Snapshots[90]
	assert.Equal(t, 3, snap90.TotalCommits)
	assert.Equal(t, 4, snap90.TotalFiles)
	assert.Equal(t, 75, snap90.TotalLines) // 15+15+20+25
}
