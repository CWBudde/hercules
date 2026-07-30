package linehistory

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
)

const (
	mergeTestAuthor = core.AuthorId(3)
	mergeTestTick   = core.TickNumber(7)
	mergeTestLines  = 10
)

// newMergePendingAnalyser reproduces the state left behind by a merge commit: its edits were
// written with TreeMergeMark, which makes File.updateTime suppress every updater, so Consume()
// emitted nothing for them. Merge() is what later resolves the marks.
func newMergePendingAnalyser(t *testing.T) *LineHistoryAnalyser {
	t.Helper()

	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Configure(map[string]any{core.ConfigLogger: core.NewLogger()}))
	require.NoError(t, analyser.Initialize(test.Repository))

	analyser.newFile("merged.go", mergeTestAuthor, TreeMergeMark, mergeTestLines)
	require.Empty(t, analyser.changes, "a merge-marked insertion must not emit changes yet")

	analyser.tick = mergeTestTick
	analyser.mergedAuthor = mergeTestAuthor
	analyser.mergePending = true

	return analyser
}

func totalDelta(changes []core.LineHistoryChange) int {
	total := 0
	for _, change := range changes {
		total += change.Delta
	}

	return total
}

func emptyConsumeDeps(tick int) map[string]any {
	return map[string]any{
		identity.DependencyAuthor:   int(mergeTestAuthor),
		items.DependencyTick:        tick,
		items.DependencyBlobCache:   map[plumbing.Hash]*items.CachedBlob{},
		items.DependencyTreeChanges: object.Changes{},
		items.DependencyFileDiff:    map[string]items.FileDiffData{},
	}
}

// TestLinesMergeResolutionDeltasSurvive guards the B1 regression: Merge() used to compute the
// deltas for the lines it resolved and then drop them on the floor. Those lines were never added
// to any consumer's history, yet their later removal still emitted a negative delta - which drove
// the project burndown matrix below zero.
func TestLinesMergeResolutionDeltasSurvive(t *testing.T) {
	analyser := newMergePendingAnalyser(t)

	analyser.Merge(nil)

	pending := FileIdResolver{analyser}.PendingChanges()
	require.NotEmpty(t, pending, "merge resolution deltas must not be discarded")
	assert.Equal(t, mergeTestLines, totalDelta(pending))

	for _, change := range pending {
		assert.Equal(t, mergeTestTick, change.PrevTick)
		assert.Equal(t, mergeTestTick, change.CurrTick)
		assert.Equal(t, mergeTestAuthor, change.PrevAuthor)
		assert.Positive(t, change.Delta)
	}
}

// TestLinesMergeResolutionDeltasEmittedOnNextCommit checks the normal delivery path: the buffered
// deltas ride along with the next commit the branch consumes, and are not emitted twice.
func TestLinesMergeResolutionDeltasEmittedOnNextCommit(t *testing.T) {
	analyser := newMergePendingAnalyser(t)
	analyser.Merge(nil)

	result, err := analyser.Consume(emptyConsumeDeps(int(mergeTestTick) + 1))
	require.NoError(t, err)

	changes, ok := result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)
	assert.Equal(t, mergeTestLines, totalDelta(changes.Changes))

	assert.Empty(t, FileIdResolver{analyser}.PendingChanges(),
		"delivered deltas must be cleared")

	result, err = analyser.Consume(emptyConsumeDeps(int(mergeTestTick) + 2))
	require.NoError(t, err)

	changes, ok = result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)
	assert.Empty(t, changes.Changes, "merge deltas must not be emitted a second time")
}

// TestLinesMergeResolutionDeltasNotForkedIntoClones covers the double-counting hazard: the pipeline
// keeps the origin on its branch slot after a fork, so only the origin may carry the buffer.
func TestLinesMergeResolutionDeltasNotForkedIntoClones(t *testing.T) {
	analyser := newMergePendingAnalyser(t)
	analyser.Merge(nil)

	require.NotEmpty(t, analyser.pendingChanges)

	for _, clone := range analyser.Fork(2) {
		assert.Empty(t, mustLineHistoryAnalyser(clone).pendingChanges)
	}

	assert.Equal(t, mergeTestLines, totalDelta(analyser.pendingChanges),
		"the origin keeps the buffer")
}

// TestLinesPendingChangesIgnoresForeignResolver keeps the helper safe for the leaves, which hold a
// core.FileIdResolver that is not necessarily backed by a LineHistoryAnalyser.
func TestLinesPendingChangesIgnoresForeignResolver(t *testing.T) {
	assert.Nil(t, PendingChanges(nil))
	assert.Nil(t, PendingChanges(FileIdResolver{}))
}
