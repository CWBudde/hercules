package leaves

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

func TestLegacyBurndownBinaryTransitionsPreserveFileAcrossRenames(t *testing.T) {
	analyser := &LegacyBurndownAnalysis{
		TrackFiles:   true,
		PeopleNumber: 3,
		Granularity:  1,
		Sampling:     1,
	}
	require.NoError(t, analyser.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(consumerTextHash):   consumerBlob("one\ntwo\nthree\n"),
		plumbing.NewHash(consumerBinaryHash): consumerBlob("opaque\x00contents"),
		plumbing.NewHash(consumerFinalHash):  consumerBlob("four\nfive\n"),
	}

	analyser.tick = 0
	require.NoError(t, analyser.handleInsertion(
		&object.Change{To: consumerEntry("source.txt", plumbing.NewHash(consumerTextHash))},
		0,
		cache,
	))
	file := analyser.files["source.txt"]
	require.NotNil(t, file)
	assert.Equal(t, 3, file.Len())

	analyser.tick = 1
	require.NoError(t, analyser.handleModification(
		&object.Change{
			From: consumerEntry("source.txt", plumbing.NewHash(consumerTextHash)),
			To:   consumerEntry("binary.dat", plumbing.NewHash(consumerBinaryHash)),
		},
		1,
		cache,
		map[string]items.FileDiffData{},
	))
	assert.Same(t, file, analyser.files["binary.dat"])
	assert.Zero(t, file.Len())
	assert.NotContains(t, analyser.files, "source.txt")

	analyser.tick = 2
	require.NoError(t, analyser.handleModification(
		&object.Change{
			From: consumerEntry("binary.dat", plumbing.NewHash(consumerBinaryHash)),
			To:   consumerEntry("restored.txt", plumbing.NewHash(consumerFinalHash)),
		},
		2,
		cache,
		map[string]items.FileDiffData{},
	))
	assert.Same(t, file, analyser.files["restored.txt"])
	assert.Equal(t, 2, file.Len())
	assert.Equal(t, int64(2), sparseHistoryBalance(analyser.globalHistory))
	assert.Contains(t, analyser.fileHistories, "restored.txt")
	assert.NotContains(t, analyser.renames, "")

	analyser.tick = 3
	require.NoError(t, analyser.handleDeletion(
		&object.Change{From: consumerEntry("restored.txt", plumbing.NewHash(consumerFinalHash))},
		2,
		cache,
	))
	assert.NotContains(t, analyser.files, "restored.txt")
	assert.Empty(t, analyser.renames)
	assert.NotContains(t, analyser.fileHistories, "restored.txt")
	assert.Contains(t, analyser.deletedFileHistories, "restored.txt")
}

func TestLegacyBurndownClearRenameChainDeletesCyclesWithoutEmptyTombstones(t *testing.T) {
	analyser := &LegacyBurndownAnalysis{
		renames: map[string]string{
			"a": "b",
			"b": "c",
			"c": "a",
			"":  "a",
			"x": "unrelated",
		},
	}

	analyser.clearRenameChain("c")

	assert.Equal(t, map[string]string{"x": "unrelated"}, analyser.renames)
	assert.NotContains(t, analyser.renames, "")
	assert.Empty(t, analyser.futureHistoryName("", map[string]bool{}))
}

func TestLegacyBurndownMergeKeepsEditedBranchAfterParallelDeletion(t *testing.T) {
	primary := &LegacyBurndownAnalysis{
		TrackFiles:   true,
		PeopleNumber: 3,
		Granularity:  1,
		Sampling:     1,
	}
	require.NoError(t, primary.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(consumerTextHash):  consumerBlob("one\ntwo\n"),
		plumbing.NewHash(consumerFinalHash): consumerBlob("one\nchanged\n"),
	}

	primary.tick = 0
	require.NoError(t, primary.handleInsertion(
		&object.Change{To: consumerEntry("file.txt", plumbing.NewHash(consumerTextHash))},
		0,
		cache,
	))
	edited := primary.Fork(1)[0].(*LegacyBurndownAnalysis)

	primary.tick = 1
	require.NoError(t, primary.handleDeletion(
		&object.Change{From: consumerEntry("file.txt", plumbing.NewHash(consumerTextHash))},
		1,
		cache,
	))

	edited.tick = 2
	require.NoError(t, edited.handleModification(
		&object.Change{
			From: consumerEntry("file.txt", plumbing.NewHash(consumerTextHash)),
			To:   consumerEntry("file.txt", plumbing.NewHash(consumerFinalHash)),
		},
		2,
		cache,
		map[string]items.FileDiffData{
			"file.txt": {
				OldLinesOfCode: 2,
				NewLinesOfCode: 2,
				Diffs: []diffmatchpatch.Diff{
					{Type: diffmatchpatch.DiffEqual, Text: "x"},
					{Type: diffmatchpatch.DiffDelete, Text: "x"},
					{Type: diffmatchpatch.DiffInsert, Text: "x"},
				},
			},
		},
	))

	_, err := primary.Consume(map[string]any{
		identity.DependencyAuthor: 2,
		items.DependencyTick:      3,
		items.DependencyBlobCache: cache,
		items.DependencyTreeChanges: object.Changes{
			&object.Change{To: consumerEntry("file.txt", plumbing.NewHash(consumerFinalHash))},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{},
		core.DependencyIsMerge:   true,
	})
	require.NoError(t, err)
	primary.Merge([]core.PipelineItem{edited})

	require.Contains(t, primary.files, "file.txt")
	require.Contains(t, edited.files, "file.txt")
	assert.Equal(t, 2, primary.files["file.txt"].Len())
	assert.Equal(t, primary.files["file.txt"].Dump(), edited.files["file.txt"].Dump())
	assert.Contains(t, primary.fileHistories, "file.txt")
	assert.NotContains(t, primary.deletedFileHistories, "file.txt")
}
