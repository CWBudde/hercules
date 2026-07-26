package linehistory

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

const (
	transitionTextHash   = "1111111111111111111111111111111111111111"
	transitionBinaryHash = "2222222222222222222222222222222222222222"
	transitionFinalHash  = "3333333333333333333333333333333333333333"
)

func transitionBlob(data string) *items.CachedBlob {
	return &items.CachedBlob{Data: []byte(data)}
}

func transitionEntry(name string, hash plumbing.Hash) object.ChangeEntry {
	return object.ChangeEntry{
		Name: name,
		TreeEntry: object.TreeEntry{
			Name: name,
			Mode: 0o100644,
			Hash: hash,
		},
	}
}

func transitionModification(
	fromName string,
	fromHash plumbing.Hash,
	toName string,
	toHash plumbing.Hash,
) *object.Change {
	return &object.Change{
		From: transitionEntry(fromName, fromHash),
		To:   transitionEntry(toName, toHash),
	}
}

func consumeLineHistoryTransition(
	t *testing.T,
	analyser *LineHistoryAnalyser,
	author int,
	tick int,
	isMerge bool,
	cache map[plumbing.Hash]*items.CachedBlob,
	changes object.Changes,
	diffs map[string]items.FileDiffData,
) core.LineHistoryChanges {
	t.Helper()

	result, err := analyser.Consume(map[string]any{
		identity.DependencyAuthor:   author,
		items.DependencyTick:        tick,
		items.DependencyBlobCache:   cache,
		items.DependencyTreeChanges: changes,
		items.DependencyFileDiff:    diffs,
		core.DependencyIsMerge:      isMerge,
	})
	require.NoError(t, err)

	history, ok := result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)

	return history
}

func lineHistoryDelta(changes []core.LineHistoryChange) int {
	total := 0
	for _, change := range changes {
		if !change.IsDelete() {
			total += change.Delta
		}
	}

	return total
}

func TestLineHistoryBinaryTransitionsPreserveIdentityAcrossRenames(t *testing.T) {
	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(transitionTextHash):   transitionBlob("one\ntwo\nthree\n"),
		plumbing.NewHash(transitionBinaryHash): transitionBlob("opaque\x00contents"),
		plumbing.NewHash(transitionFinalHash):  transitionBlob("four\nfive\n"),
	}

	inserted := consumeLineHistoryTransition(
		t,
		analyser,
		0,
		0,
		false,
		cache,
		object.Changes{&object.Change{
			To: transitionEntry("source.txt", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)
	require.Len(t, inserted.Changes, 1)
	fileID := inserted.Changes[0].FileId
	assert.Equal(t, 3, lineHistoryDelta(inserted.Changes))

	binary := consumeLineHistoryTransition(
		t,
		analyser,
		1,
		1,
		false,
		cache,
		object.Changes{transitionModification(
			"source.txt",
			plumbing.NewHash(transitionTextHash),
			"binary.dat",
			plumbing.NewHash(transitionBinaryHash),
		)},
		map[string]items.FileDiffData{},
	)
	assert.Equal(t, -3, lineHistoryDelta(binary.Changes))
	for _, change := range binary.Changes {
		assert.Equal(t, fileID, change.FileId)
		assert.False(t, change.IsDelete())
	}
	require.Contains(t, analyser.files, "binary.dat")
	assert.Equal(t, fileID, analyser.files["binary.dat"].Id)
	assert.Zero(t, analyser.files["binary.dat"].Len())

	resolvedID, name, present := binary.Resolver.MergedWith(fileID)
	assert.Equal(t, fileID, resolvedID)
	assert.Equal(t, "binary.dat", name)
	assert.True(t, present)

	text := consumeLineHistoryTransition(
		t,
		analyser,
		2,
		2,
		false,
		cache,
		object.Changes{transitionModification(
			"binary.dat",
			plumbing.NewHash(transitionBinaryHash),
			"restored.txt",
			plumbing.NewHash(transitionFinalHash),
		)},
		map[string]items.FileDiffData{},
	)
	assert.Equal(t, 2, lineHistoryDelta(text.Changes))
	for _, change := range text.Changes {
		assert.Equal(t, fileID, change.FileId)
		assert.False(t, change.IsDelete())
	}
	require.Contains(t, analyser.files, "restored.txt")
	assert.Equal(t, fileID, analyser.files["restored.txt"].Id)
	assert.Equal(t, 2, analyser.files["restored.txt"].Len())
	assert.Equal(t, "restored.txt", text.Resolver.NameOf(fileID))
	assert.NotContains(t, analyser.files, "")
}

func TestLineHistoryDeletingBinaryPlaceholderAbandonsSameIdentity(t *testing.T) {
	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(transitionTextHash):   transitionBlob("one\n"),
		plumbing.NewHash(transitionBinaryHash): transitionBlob("opaque\x00contents"),
	}

	inserted := consumeLineHistoryTransition(
		t,
		analyser,
		0,
		0,
		false,
		cache,
		object.Changes{&object.Change{
			To: transitionEntry("file", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)
	fileID := inserted.Changes[0].FileId

	consumeLineHistoryTransition(
		t,
		analyser,
		1,
		1,
		false,
		cache,
		object.Changes{transitionModification(
			"file",
			plumbing.NewHash(transitionTextHash),
			"file",
			plumbing.NewHash(transitionBinaryHash),
		)},
		map[string]items.FileDiffData{},
	)

	deleted := consumeLineHistoryTransition(
		t,
		analyser,
		2,
		2,
		false,
		cache,
		object.Changes{&object.Change{
			From: transitionEntry("file", plumbing.NewHash(transitionBinaryHash)),
		}},
		map[string]items.FileDiffData{},
	)
	require.Len(t, deleted.Changes, 1)
	assert.True(t, deleted.Changes[0].IsDelete())
	assert.Equal(t, fileID, deleted.Changes[0].FileId)
	assert.NotContains(t, analyser.files, "file")

	resolvedID, name, present := deleted.Resolver.MergedWith(fileID)
	assert.Zero(t, resolvedID)
	assert.Equal(t, "file", name)
	assert.False(t, present)
}

func TestLineHistoryMergeKeepsEditedBranchAfterParallelDeletion(t *testing.T) {
	primary := &LineHistoryAnalyser{}
	require.NoError(t, primary.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(transitionTextHash):  transitionBlob("one\ntwo\n"),
		plumbing.NewHash(transitionFinalHash): transitionBlob("one\nchanged\n"),
	}

	inserted := consumeLineHistoryTransition(
		t,
		primary,
		0,
		0,
		false,
		cache,
		object.Changes{&object.Change{
			To: transitionEntry("file.txt", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)
	fileID := inserted.Changes[0].FileId

	edited := mustLineHistoryAnalyser(primary.Fork(1)[0])
	deleted := consumeLineHistoryTransition(
		t,
		primary,
		1,
		1,
		false,
		cache,
		object.Changes{&object.Change{
			From: transitionEntry("file.txt", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)
	assert.Equal(t, -2, lineHistoryDelta(deleted.Changes))
	require.Len(t, deleted.Changes, 2)
	assert.True(t, deleted.Changes[1].IsDelete())

	branchEdit := consumeLineHistoryTransition(
		t,
		edited,
		2,
		2,
		false,
		cache,
		object.Changes{transitionModification(
			"file.txt",
			plumbing.NewHash(transitionTextHash),
			"file.txt",
			plumbing.NewHash(transitionFinalHash),
		)},
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
	)
	assert.Zero(t, lineHistoryDelta(branchEdit.Changes))
	for _, change := range branchEdit.Changes {
		assert.Equal(t, fileID, change.FileId)
	}

	mergeCommit := consumeLineHistoryTransition(
		t,
		primary,
		3,
		3,
		true,
		cache,
		object.Changes{&object.Change{
			To: transitionEntry("file.txt", plumbing.NewHash(transitionFinalHash)),
		}},
		map[string]items.FileDiffData{},
	)
	assert.Empty(t, mergeCommit.Changes)
	require.Contains(t, primary.files, "file.txt")
	assert.Equal(t, fileID, primary.files["file.txt"].Id)

	primary.Merge([]core.PipelineItem{edited})

	require.Contains(t, primary.files, "file.txt")
	require.Contains(t, edited.files, "file.txt")
	assert.Equal(t, fileID, primary.files["file.txt"].Id)
	assert.Equal(t, fileID, edited.files["file.txt"].Id)
	assert.Equal(t, 2, primary.files["file.txt"].Len())
	assert.Equal(t, primary.files["file.txt"].Dump(), edited.files["file.txt"].Dump())
	assert.NotContains(t, primary.files, "")
	assert.NotContains(t, edited.files, "")

	owners := []core.AuthorId{}
	primary.files["file.txt"].ForEach(func(_, value int) {
		if value != -1 {
			author, _ := unpackPersonWithTick(value)
			owners = append(owners, author)
		}
	})
	assert.Equal(t, []core.AuthorId{0, 2}, owners)
}
