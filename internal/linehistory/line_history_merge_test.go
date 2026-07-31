package linehistory

import (
	"slices"
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

const (
	// adoptTestPath is contributed by a sibling branch only; the merge branch never held it.
	adoptTestPath = testFileOnePath
	adoptTestBorn = core.TickNumber(4)
)

// newAdoptionBranches reproduces the shape of a merge commit which brings in a path the
// authoritative branch never held: the sibling branch created it under its own FileId and emitted
// the insertion, while the merge branch mints a fresh id whose lines all carry TreeMergeMark - so
// File.updateTime suppressed every updater and nothing was ever emitted for that id.
func newAdoptionBranches(t *testing.T) (
	lead, sibling *LineHistoryAnalyser, siblingChanges []core.LineHistoryChange,
) {
	t.Helper()

	lead = &LineHistoryAnalyser{}
	require.NoError(t, lead.Configure(map[string]any{core.ConfigLogger: core.NewLogger()}))
	require.NoError(t, lead.Initialize(test.Repository))

	forks := lead.Fork(1)
	require.Len(t, forks, 1)

	sibling = mustLineHistoryAnalyser(forks[0])

	// The branch which really adds the path: an ordinary insertion, emitted under its own id.
	sibling.tick = adoptTestBorn
	sibling.commitTick = adoptTestBorn
	require.NotNil(t, sibling.newFile(adoptTestPath, mergeTestAuthor, adoptTestBorn, mergeTestLines))
	require.Equal(t, mergeTestLines, totalDelta(sibling.changes))

	siblingChanges = sibling.changes

	// The merge branch replays the merge commit: the path is new to it, so newFile() mints a fresh
	// id, and TreeMergeMark keeps the insertion from being emitted for it.
	require.NotNil(t, lead.newFile(adoptTestPath, mergeTestAuthor, TreeMergeMark, mergeTestLines))
	require.Empty(t, lead.changes, "a merge-marked insertion must not emit changes yet")
	require.NotEqual(t, sibling.files[adoptTestPath].Id, lead.files[adoptTestPath].Id,
		"the fixture is only meaningful while the two branches disagree on the id")

	lead.tick = mergeTestTick
	lead.commitTick = mergeTestTick
	lead.mergedAuthor = mergeTestAuthor
	lead.mergePending = true

	return lead, sibling, siblingChanges
}

// deltasByFileId sums the deltas of the non-marker changes per file id.
func deltasByFileId(changes []core.LineHistoryChange) map[FileId]int {
	totals := map[FileId]int{}

	for _, change := range changes {
		if change.IsDelete() {
			continue
		}

		totals[change.FileId] += change.Delta
	}

	return totals
}

// TestLinesMergeAdoptsCreatingBranchFileId pins the id adoption: a merge commit which brings in a
// path the merge branch never held used to leave the file on the freshly minted id, while the
// branch that created the path had already emitted its insertion under its own. The two halves of
// the accounting then lived on different keys.
func TestLinesMergeAdoptsCreatingBranchFileId(t *testing.T) {
	lead, sibling, _ := newAdoptionBranches(t)

	adopted := sibling.files[adoptTestPath].Id
	minted := lead.files[adoptTestPath].Id

	lead.Merge([]core.PipelineItem{sibling})

	merged := lead.files[adoptTestPath]
	require.NotNil(t, merged)
	assert.Equal(t, adopted, merged.Id,
		"the merged file must keep the id the branch which created the path used")
	assert.Equal(t, adoptTestPath, lead.fileNames[adopted])
	assert.NotContains(t, lead.fileNames, minted,
		"the vacated id must not stay in the name map")
	assert.NotContains(t, lead.fileAbandonedNames, minted,
		"the vacated id holds no accounting and must not be resurrectable")
}

// TestLinesMergeAdoptedFileIdKeepsRemovalPaired covers the observable consequence: the removal
// which eventually arrives must cancel the insertion the creating branch emitted. Without
// adoption it lands on the merge-minted id, whose insertion was never sent, and that file id ends
// up net negative - a negative burndown cell.
func TestLinesMergeAdoptedFileIdKeepsRemovalPaired(t *testing.T) {
	lead, sibling, siblingChanges := newAdoptionBranches(t)

	lead.Merge([]core.PipelineItem{sibling})

	// The lines the merge resolved plus whatever the branches emitted before it.
	changes := append(slices.Clone(siblingChanges), FileIdResolver{lead}.PendingChanges()...)

	// A later commit deletes the path again.
	lead.tick = mergeTestTick + 1
	lead.commitTick = lead.tick
	lead.files[adoptTestPath].Update(
		packChangePersonWithTick(mergeTestAuthor, lead.tick), 0, 0, mergeTestLines)

	changes = append(changes, lead.changes...)

	totals := deltasByFileId(changes)
	assert.Len(t, totals, 1, "the path's accounting must live on a single file id: %v", totals)
	assert.Zero(t, totalDelta(changes), "the removal must cancel the insertion")

	for id, total := range totals {
		assert.Zero(t, total, "file id %d must not end up net negative", id)
	}
}
