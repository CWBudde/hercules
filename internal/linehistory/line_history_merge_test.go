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

	file := analyser.newFile("merged.go", mergeTestAuthor, TreeMergeMark, mergeTestLines)
	require.NotNil(t, file)
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

const (
	// deleteTestBlob is the fixture blob for .travis.yml, which CountLines() reports as 12.
	deleteTestBlob  = "291286b4ac41952cbd1389fda66420ec03c1a9fe"
	deleteTestLines = 12
	deleteTestPath  = ".travis.yml"
	deleteTestBorn  = core.TickNumber(5)
)

// deleteChange builds the tree change a commit which removes deleteTestPath produces.
func deleteChange() *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name: deleteTestPath,
			TreeEntry: object.TreeEntry{
				Name: deleteTestPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash(deleteTestBlob),
			},
		},
		To: object.ChangeEntry{},
	}
}

// newDeletionAnalyser holds a single file born at deleteTestBorn, ready to be removed.
func newDeletionAnalyser(t *testing.T) (*LineHistoryAnalyser, map[plumbing.Hash]*items.CachedBlob) {
	t.Helper()

	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Configure(map[string]any{core.ConfigLogger: core.NewLogger()}))
	require.NoError(t, analyser.Initialize(test.Repository))

	analyser.tick = deleteTestBorn
	analyser.commitTick = deleteTestBorn

	file := analyser.newFile(deleteTestPath, mergeTestAuthor, deleteTestBorn, deleteTestLines)
	require.NotNil(t, file)
	require.Equal(t, deleteTestLines, totalDelta(analyser.changes))

	analyser.changes = nil

	cache := map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, deleteTestBlob)

	return analyser, cache
}

// TestLinesDeletionInsideMergeCommitIsAccounted guards B1b: handleDeletion used to run the removal
// under TreeMergeMark, which suppresses every updater, and to skip the deletion marker as well.
// The file was dropped regardless, so its lines stayed in every consumer's history forever - line
// totals drifted upwards with no negative value for the burndown balance check to catch.
func TestLinesDeletionInsideMergeCommitIsAccounted(t *testing.T) {
	analyser, cache := newDeletionAnalyser(t)

	// The state Consume() leaves behind while a merge commit is processed.
	analyser.commitTick = deleteTestBorn + 3
	analyser.tick = TreeMergeMark
	analyser.mergedAuthor = mergeTestAuthor
	analyser.mergePending = true

	require.NoError(t, analyser.handleDeletion(deleteChange(), mergeTestAuthor, cache))

	var removed, markers int

	for _, change := range analyser.changes {
		if change.IsDelete() {
			markers++

			assert.Equal(t, deleteTestBorn+3, change.CurrTick)

			continue
		}

		removed += change.Delta

		assert.Equal(t, deleteTestBorn, change.PrevTick, "removed lines keep the tick they were born on")
		assert.Equal(t, deleteTestBorn+3, change.CurrTick, "the removal belongs to the merge commit's tick")
	}

	assert.Equal(t, -deleteTestLines, removed)
	assert.Equal(t, 1, markers, "consumers must be told the file is gone")
	assert.NotContains(t, analyser.files, deleteTestPath)
}

// TestLinesDeletionOutsideMergeCommitUnchanged pins the ordinary path, which was already correct.
func TestLinesDeletionOutsideMergeCommitUnchanged(t *testing.T) {
	analyser, cache := newDeletionAnalyser(t)
	analyser.tick = deleteTestBorn + 3
	analyser.commitTick = analyser.tick

	require.NoError(t, analyser.handleDeletion(deleteChange(), mergeTestAuthor, cache))

	removed := 0

	for _, change := range analyser.changes {
		if !change.IsDelete() {
			removed += change.Delta
		}
	}

	assert.Equal(t, -deleteTestLines, removed)
}

// TestLinesDeletionOfUnresolvedMergeLinesDefers covers the one case that must still be suppressed:
// the removed lines are themselves unresolved marks, which updateTime() refuses to touch. They were
// never added to any history either, so dropping them keeps the books balanced.
func TestLinesDeletionOfUnresolvedMergeLinesDefers(t *testing.T) {
	analyser, cache := newDeletionAnalyser(t)

	analyser.tick = TreeMergeMark
	analyser.commitTick = deleteTestBorn + 3

	// Overwrite every line with the marker the merge commit's own edits carry.
	analyser.files[deleteTestPath].Update(packChangePersonWithTick(mergeTestAuthor, TreeMergeMark),
		0, deleteTestLines, deleteTestLines)
	require.True(t, analyser.files[deleteTestPath].hasMergeMarks())

	analyser.changes = nil

	require.NoError(t, analyser.handleDeletion(deleteChange(), mergeTestAuthor, cache))
	assert.Empty(t, analyser.changes)
}
