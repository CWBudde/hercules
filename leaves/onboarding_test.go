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

func TestOnboardingAnalysis_MultipleAuthors(t *testing.T) {
	oa := &OnboardingAnalysis{
		WindowDays:          []int{7, 30},
		MeaningfulThreshold: 10,
		tickSize:            24 * time.Hour,
		reversedPeopleDict:  []string{"author0", "author1", "author2"},
	}

	require.NoError(t, oa.Initialize(test.Repository))

	// Author 0: first commit at tick 0
	deps1 := makeTestDeps(0, 0, map[string]int{"file1.go": 20})
	_, err := oa.Consume(deps1)
	require.NoError(t, err)

	// Author 1: first commit at tick 10
	deps2 := makeTestDeps(1, 10, map[string]int{"file2.go": 15})
	_, err = oa.Consume(deps2)
	require.NoError(t, err)

	// Author 2: first commit at tick 20
	deps3 := makeTestDeps(2, 20, map[string]int{"file3.go": 25})
	_, err = oa.Consume(deps3)
	require.NoError(t, err)

	// Author 0: second commit at tick 5
	deps4 := makeTestDeps(0, 5, map[string]int{"file4.go": 30})
	_, err = oa.Consume(deps4)
	require.NoError(t, err)

	// Finalize
	result := oa.Finalize().(OnboardingResult)

	// Verify all 3 authors present
	assert.Len(t, result.Authors, 3)

	// Author 0 started at tick 0
	author0 := result.Authors[0]
	assert.Equal(t, 0, author0.FirstCommitTick)
	assert.Contains(t, author0.Snapshots, 7)
	assert.Equal(t, 2, author0.Snapshots[7].TotalCommits)

	// Author 1 started at tick 10
	author1 := result.Authors[1]
	assert.Equal(t, 10, author1.FirstCommitTick)
	assert.Contains(t, author1.Snapshots, 7)
	assert.Equal(t, 1, author1.Snapshots[7].TotalCommits)

	// Author 2 started at tick 20
	author2 := result.Authors[2]
	assert.Equal(t, 20, author2.FirstCommitTick)

	// Verify cohort grouping (all same month in this test)
	assert.NotEmpty(t, result.Cohorts)
}
