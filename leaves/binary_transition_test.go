package leaves

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

const (
	consumerTextHash   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	consumerBinaryHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	consumerFinalHash  = "cccccccccccccccccccccccccccccccccccccccc"
)

func consumerBlob(data string) *items.CachedBlob {
	return &items.CachedBlob{Data: []byte(data)}
}

func consumerEntry(name string, hash plumbing.Hash) object.ChangeEntry {
	return object.ChangeEntry{
		Name: name,
		TreeEntry: object.TreeEntry{
			Name: name,
			Mode: 0o100644,
			Hash: hash,
		},
	}
}

func consumeTransitionForConsumers(
	t *testing.T,
	analyser *linehistory.LineHistoryAnalyser,
	author int,
	tick int,
	cache map[plumbing.Hash]*items.CachedBlob,
	change *object.Change,
) core.LineHistoryChanges {
	t.Helper()

	result, err := analyser.Consume(map[string]any{
		identity.DependencyAuthor:   author,
		items.DependencyTick:        tick,
		items.DependencyBlobCache:   cache,
		items.DependencyTreeChanges: object.Changes{change},
		items.DependencyFileDiff:    map[string]items.FileDiffData{},
		core.DependencyIsMerge:      false,
	})
	require.NoError(t, err)

	changes, ok := result[linehistory.DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)

	return changes
}

func sparseHistoryBalance(history sparseHistory) int64 {
	var balance int64
	for _, entry := range history {
		for _, delta := range entry.deltas {
			balance += delta
		}
	}

	return balance
}

func TestBinaryTransitionsKeepBurndownAndChurnTotalsConsistent(t *testing.T) {
	lines := &linehistory.LineHistoryAnalyser{}
	require.NoError(t, lines.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(consumerTextHash):   consumerBlob("one\ntwo\nthree\n"),
		plumbing.NewHash(consumerBinaryHash): consumerBlob("opaque\x00contents"),
		plumbing.NewHash(consumerFinalHash):  consumerBlob("four\nfive\n"),
	}

	resolver := core.NewIdentityResolver([]string{"first", "second", "third"}, nil)
	burndown := &BurndownAnalysis{
		TrackFiles:     true,
		Granularity:    1,
		Sampling:       1,
		peopleResolver: resolver,
	}
	require.NoError(t, burndown.Initialize(nil))

	churn := &CodeChurnAnalysis{
		Granularity:    1,
		Sampling:       1,
		peopleResolver: resolver,
	}
	require.NoError(t, churn.Initialize(nil))

	consumeDownstream := func(changes core.LineHistoryChanges) {
		t.Helper()

		deps := map[string]any{linehistory.DependencyLineHistory: changes}
		_, err := burndown.Consume(deps)
		require.NoError(t, err)
		_, err = churn.Consume(deps)
		require.NoError(t, err)
	}

	inserted := consumeTransitionForConsumers(
		t,
		lines,
		0,
		0,
		cache,
		&object.Change{To: consumerEntry("source.txt", plumbing.NewHash(consumerTextHash))},
	)
	require.Len(t, inserted.Changes, 1)
	fileID := inserted.Changes[0].FileId
	consumeDownstream(inserted)

	binary := consumeTransitionForConsumers(
		t,
		lines,
		1,
		1,
		cache,
		&object.Change{
			From: consumerEntry("source.txt", plumbing.NewHash(consumerTextHash)),
			To:   consumerEntry("binary.dat", plumbing.NewHash(consumerBinaryHash)),
		},
	)
	consumeDownstream(binary)

	restored := consumeTransitionForConsumers(
		t,
		lines,
		2,
		2,
		cache,
		&object.Change{
			From: consumerEntry("binary.dat", plumbing.NewHash(consumerBinaryHash)),
			To:   consumerEntry("restored.txt", plumbing.NewHash(consumerFinalHash)),
		},
	)
	consumeDownstream(restored)

	assert.Equal(t, int64(2), sparseHistoryBalance(burndown.globalHistory))
	require.Len(t, burndown.fileHistories, 1)
	assert.Equal(t, int64(2), sparseHistoryBalance(burndown.fileHistories[fileID]))
	assert.Empty(t, burndown.deletedFileHistories)

	first := churn.codeChurns[0].files[fileID]
	assert.Equal(t, int32(3), first.insertedLines)
	assert.Zero(t, first.ownedLines)
	require.Contains(t, first.deleteHistory, core.AuthorId(1))

	third := churn.codeChurns[2].files[fileID]
	assert.Equal(t, int32(2), third.insertedLines)
	assert.Equal(t, int32(2), third.ownedLines)
	assert.NotContains(t, churn.codeChurns[1].files, fileID)
}

func TestBurndownRestoresFileHistoryAfterParallelBranchDeletion(t *testing.T) {
	resolver := ownershipTestResolver{7: "file.txt"}
	burndown := &BurndownAnalysis{
		TrackFiles:      true,
		Granularity:     1,
		Sampling:        1,
		peopleResolver:  core.NewIdentityResolver([]string{"first", "second"}, nil),
		primaryResolver: resolver,
		fileResolver:    resolver,
	}
	require.NoError(t, burndown.Initialize(nil))

	consume := func(changes ...core.LineHistoryChange) {
		t.Helper()
		_, err := burndown.Consume(map[string]any{
			linehistory.DependencyLineHistory: core.LineHistoryChanges{
				Changes:  changes,
				Resolver: resolver,
			},
		})
		require.NoError(t, err)
	}

	consume(core.LineHistoryChange{
		FileId: 7, CurrAuthor: 0, PrevAuthor: 0, CurrTick: 0, PrevTick: 0, Delta: 2,
	})
	consume(
		core.LineHistoryChange{
			FileId: 7, CurrAuthor: 1, PrevAuthor: 0, CurrTick: 1, PrevTick: 0, Delta: -2,
		},
		core.NewLineHistoryDeletion(7, 1, 1),
	)
	assert.NotContains(t, burndown.fileHistories, core.FileId(7))
	assert.Contains(t, burndown.deletedFileHistories, core.FileId(7))

	require.NoError(t, burndown.Hibernate())
	require.NoError(t, burndown.Boot())
	assert.Contains(t, burndown.deletedFileHistories, core.FileId(7))

	consume(
		core.LineHistoryChange{
			FileId: 7, CurrAuthor: 1, PrevAuthor: 0, CurrTick: 2, PrevTick: 0, Delta: -1,
		},
		core.LineHistoryChange{
			FileId: 7, CurrAuthor: 1, PrevAuthor: 1, CurrTick: 2, PrevTick: 2, Delta: 1,
		},
	)
	assert.Contains(t, burndown.fileHistories, core.FileId(7))
	assert.NotContains(t, burndown.deletedFileHistories, core.FileId(7))

	assert.Contains(t, burndown.finalizeFileHistories(2), "file.txt")
}
