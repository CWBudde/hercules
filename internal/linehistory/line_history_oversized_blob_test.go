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
)

const oversizedBlobHash = "4444444444444444444444444444444444444444"

// TestLineHistoryOversizedBlobPlaceholderKeepsFileAlive is the burndown-facing contract of the
// skip-and-warn policy for blobs which exceed the cache limits. The placeholder BlobCache stores
// declares itself binary, so a text file which turns into one keeps its identity and its file ID
// and merely loses its visible lines, instead of being torn down as a deletion.
func TestLineHistoryOversizedBlobPlaceholderKeepsFileAlive(t *testing.T) {
	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Initialize(nil))

	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(transitionTextHash): transitionBlob("one\ntwo\nthree\n"),
		// Exactly what internal/plumbing.BlobCache installs for a blob it refused to read.
		plumbing.NewHash(oversizedBlobHash):   {Oversized: true, OriginalSize: 1 << 30},
		plumbing.NewHash(transitionFinalHash): transitionBlob("four\nfive\n"),
	}

	inserted := consumeLineHistoryTransition(
		t, analyser, 0, 0, false, cache,
		object.Changes{&object.Change{
			To: transitionEntry("huge.txt", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)
	require.Len(t, inserted.Changes, 1)
	fileID := inserted.Changes[0].FileId
	assert.Equal(t, 3, lineHistoryDelta(inserted.Changes))

	// The next revision of the same path is too large to cache.
	skipped := consumeLineHistoryTransition(
		t, analyser, 1, 1, false, cache,
		object.Changes{transitionModification(
			"huge.txt", plumbing.NewHash(transitionTextHash),
			"huge.txt", plumbing.NewHash(oversizedBlobHash),
		)},
		map[string]items.FileDiffData{},
	)

	assert.Equal(t, -3, lineHistoryDelta(skipped.Changes))

	for _, change := range skipped.Changes {
		assert.Equal(t, fileID, change.FileId)
		assert.False(t, change.IsDelete(), "an unreadable revision is not a file deletion")
	}

	require.Contains(t, analyser.files, "huge.txt")
	assert.Equal(t, fileID, analyser.files["huge.txt"].Id)
	assert.Zero(t, analyser.files["huge.txt"].Len())
	assert.Equal(t, "huge.txt", skipped.Resolver.NameOf(fileID))

	// Raising the limit again, or simply a smaller next revision, restores the visible lines
	// under the original identity.
	restored := consumeLineHistoryTransition(
		t, analyser, 2, 2, false, cache,
		object.Changes{transitionModification(
			"huge.txt", plumbing.NewHash(oversizedBlobHash),
			"huge.txt", plumbing.NewHash(transitionFinalHash),
		)},
		map[string]items.FileDiffData{},
	)
	assert.Equal(t, 2, lineHistoryDelta(restored.Changes))
	assert.Equal(t, fileID, analyser.files["huge.txt"].Id)
	assert.Equal(t, 2, analyser.files["huge.txt"].Len())
}

// TestLineHistoryEmptyPlaceholderIsNotAViableSubstitute records why the placeholder must declare
// itself binary rather than reuse the empty dummy blob used for missing submodules: an empty blob
// counts as text with zero lines, so the modification is routed through the ordinary diff path and
// trips the source integrity check instead of being recognised as opaque.
func TestLineHistoryEmptyPlaceholderIsNotAViableSubstitute(t *testing.T) {
	analyser := &LineHistoryAnalyser{}
	require.NoError(t, analyser.Initialize(nil))

	emptyHash := plumbing.NewHash(oversizedBlobHash)
	cache := map[plumbing.Hash]*items.CachedBlob{
		plumbing.NewHash(transitionTextHash): transitionBlob("one\ntwo\nthree\n"),
		emptyHash:                            {},
	}

	lines, err := cache[emptyHash].CountLines()
	require.NoError(t, err, "an empty blob reads as text, which is the whole problem")
	assert.Equal(t, 0, lines)

	consumeLineHistoryTransition(
		t, analyser, 0, 0, false, cache,
		object.Changes{&object.Change{
			To: transitionEntry("huge.txt", plumbing.NewHash(transitionTextHash)),
		}},
		map[string]items.FileDiffData{},
	)

	_, err = analyser.Consume(map[string]any{
		identity.DependencyAuthor: 1,
		items.DependencyTick:      1,
		items.DependencyBlobCache: cache,
		items.DependencyTreeChanges: object.Changes{transitionModification(
			"huge.txt", plumbing.NewHash(transitionTextHash),
			"huge.txt", emptyHash,
		)},
		items.DependencyFileDiff: map[string]items.FileDiffData{},
		core.DependencyIsMerge:   false,
	})
	require.Error(t, err)
}
