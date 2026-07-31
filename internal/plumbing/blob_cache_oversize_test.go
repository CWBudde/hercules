package plumbing

import (
	"fmt"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

// recordingLogger captures the warnings emitted by BlobCache so the once-per-run contract can be
// asserted directly.
type recordingLogger struct {
	mutex    sync.Mutex
	warnings []string
	errors   []string
}

func (l *recordingLogger) Warnf(format string, args ...any) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.warnings = append(l.warnings, sprintfLike(format, args))
}

func (l *recordingLogger) Warn(args ...any) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.warnings = append(l.warnings, sprintfLike("%v", args))
}

func (l *recordingLogger) Errorf(format string, args ...any) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.errors = append(l.errors, sprintfLike(format, args))
}

func (l *recordingLogger) Error(args ...any) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.errors = append(l.errors, sprintfLike("%v", args))
}

func (l *recordingLogger) Info(...any)          {}
func (l *recordingLogger) Infof(string, ...any) {}
func (l *recordingLogger) Critical(...any)      {}

func (l *recordingLogger) Criticalf(string, ...any) {}

func (l *recordingLogger) warningsSnapshot() []string {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return append([]string(nil), l.warnings...)
}

func sprintfLike(format string, args []any) string {
	return fmt.Sprintf(format, args...)
}

// oversizeModifyChange is the labours.py modification used throughout the blob cache tests:
// the previous blob is 8969 bytes and the new one is 9481 bytes.
func oversizeModifyChange(t *testing.T) (*object.Commit, object.Changes) {
	t.Helper()

	commit, err := test.Repository.CommitObject(plumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	require.NoError(t, err)
	treeFrom, err := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	require.NoError(t, err)
	treeTo, err := test.Repository.TreeObject(plumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	require.NoError(t, err)

	return commit, object.Changes{{From: object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("1cacfc1bf0f048eb2f31973750983ae5d8de647a"),
		},
	}, To: object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742"),
		},
	}}}
}

// oversizeInsertDeleteChanges deletes a 26446 byte blob and then inserts a 5576 byte one, in that
// order, so the second change can be used to prove that skipping the first consumed no budget.
func oversizeInsertDeleteChanges(t *testing.T) (*object.Commit, object.Changes) {
	t.Helper()

	commit, err := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	require.NoError(t, err)
	treeFrom, err := test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	require.NoError(t, err)
	treeTo, err := test.Repository.TreeObject(plumbing.NewHash(
		"251f2094d7b523d5bcc60e663b6cf38151bf8844",
	))
	require.NoError(t, err)

	return commit, object.Changes{
		{
			From: object.ChangeEntry{
				Name: testAnalyserPath,
				Tree: treeFrom,
				TreeEntry: object.TreeEntry{
					Name: testAnalyserPath,
					Mode: 0o100644,
					Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
				},
			}, To: object.ChangeEntry{},
		},
		{
			From: object.ChangeEntry{}, To: object.ChangeEntry{
				Name: testPipelinePath,
				Tree: treeTo,
				TreeEntry: object.TreeEntry{
					Name: testPipelinePath,
					Mode: 0o100644,
					Hash: plumbing.NewHash("db99e1890f581ad69e1527fe8302978c661eb473"),
				},
			},
		},
	}
}

func consumeChanges(
	t *testing.T, cache *BlobCache, commit *object.Commit, changes object.Changes,
) (map[plumbing.Hash]*CachedBlob, error) {
	t.Helper()

	result, err := cache.Consume(map[string]any{
		core.DependencyCommit: commit,
		DependencyTreeChanges: changes,
	})
	if err != nil {
		return nil, err
	}

	return result[DependencyBlobCache].(map[plumbing.Hash]*CachedBlob), nil
}

func requireOversizedPlaceholder(t *testing.T, blob *CachedBlob, originalSize int64) {
	t.Helper()

	require.NotNil(t, blob, "the placeholder must keep the map key: consumers index and dereference")
	assert.True(t, blob.Oversized)
	assert.Nil(t, blob.Data)
	// Size stays 0 so RenameAnalysis cannot mistake an empty payload for a large file.
	assert.Equal(t, int64(0), blob.Size)
	assert.Equal(t, originalSize, blob.OriginalSize)

	lines, err := blob.CountLines()
	require.ErrorIs(t, err, ErrBinary)
	assert.Equal(t, 0, lines)
}

func TestBlobCacheSkipsBlobOverPerBlobLimit(t *testing.T) {
	commit, changes := oversizeModifyChange(t)
	cache := fixtureBlobCache()
	cache.l = &recordingLogger{}
	cache.MaxBlobSize = 1024

	blobs, err := consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)
	assert.Len(t, blobs, 2)

	requireOversizedPlaceholder(
		t, blobs[plumbing.NewHash("1cacfc1bf0f048eb2f31973750983ae5d8de647a")], 8969,
	)
	requireOversizedPlaceholder(
		t, blobs[plumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742")], 9481,
	)
}

func TestBlobCacheStrictModeAbortsOnBlobOverPerBlobLimit(t *testing.T) {
	commit, changes := oversizeModifyChange(t)
	cache := fixtureBlobCache()
	cache.MaxBlobSize = 1024
	cache.FailOnOversizedBlobs = true

	blobs, err := consumeChanges(t, cache, commit, changes)
	require.ErrorIs(t, err, ErrBlobTooLarge)
	assert.Nil(t, blobs)
}

func TestBlobCacheSkipsBlobOverCommitBudget(t *testing.T) {
	commit, changes := oversizeModifyChange(t)
	cache := fixtureBlobCache()
	cache.l = &recordingLogger{}
	cache.MaxCommitSize = 10_000

	blobs, err := consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)
	assert.Len(t, blobs, 2)

	// The new blob is cached first and fits; the previous one no longer does.
	newBlob := blobs[plumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742")]
	require.NotNil(t, newBlob)
	assert.False(t, newBlob.Oversized)
	assert.Equal(t, int64(9481), newBlob.Size)

	requireOversizedPlaceholder(
		t, blobs[plumbing.NewHash("1cacfc1bf0f048eb2f31973750983ae5d8de647a")], 8969,
	)
}

// TestBlobCacheSkippedBlobLeavesBudgetForLaterOnes proves that a skipped blob consumes no budget:
// the 26446 byte deletion does not fit 10000 bytes, yet the 5576 byte insertion after it does.
func TestBlobCacheSkippedBlobLeavesBudgetForLaterOnes(t *testing.T) {
	commit, changes := oversizeInsertDeleteChanges(t)
	cache := fixtureBlobCache()
	cache.l = &recordingLogger{}
	cache.MaxCommitSize = 10_000

	blobs, err := consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)
	assert.Len(t, blobs, 2)

	requireOversizedPlaceholder(
		t, blobs[plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1")], 26446,
	)

	later := blobs[plumbing.NewHash("db99e1890f581ad69e1527fe8302978c661eb473")]
	require.NotNil(t, later)
	assert.False(t, later.Oversized)
	assert.Equal(t, int64(5576), later.Size)
	assert.Len(t, later.Data, 5576)
}

func TestBlobCacheWarnsOnceAndSummarisesOnDispose(t *testing.T) {
	logger := &recordingLogger{}
	cache := fixtureBlobCache()
	cache.l = logger
	cache.MaxBlobSize = 1024

	commit, changes := oversizeModifyChange(t)
	_, err := consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)

	commit, changes = oversizeInsertDeleteChanges(t)
	_, err = consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)

	warnings := logger.warningsSnapshot()
	require.Len(t, warnings, 1, "four skipped blobs must produce exactly one warning")
	assert.Contains(t, warnings[0], testLaboursPath)
	assert.Contains(t, warnings[0], "9481")
	assert.Contains(t, warnings[0], "--blob-cache-max-blob-size")
	assert.Contains(t, warnings[0], "binary")
	assert.Contains(t, warnings[0], "--fail-on-oversized-blobs")

	cache.Dispose()

	warnings = logger.warningsSnapshot()
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[1], "4 blobs")

	// Dispose is idempotent and clones share the guard, so no second summary appears.
	cache.Dispose()
	clone := cache.Fork(1)[0].(*BlobCache)
	clone.Dispose()
	assert.Len(t, logger.warningsSnapshot(), 2)
}

func TestBlobCacheForkSharesTheOversizeReport(t *testing.T) {
	logger := &recordingLogger{}
	cache := fixtureBlobCache()
	cache.l = logger
	cache.MaxBlobSize = 1024

	clone := cache.Fork(1)[0].(*BlobCache)
	require.NotNil(t, clone.l)
	assert.Same(t, cache.oversize, clone.oversize)
	assert.Equal(t, cache.FailOnOversizedBlobs, clone.FailOnOversizedBlobs)

	commit, changes := oversizeModifyChange(t)
	_, err := consumeChanges(t, cache, commit, changes)
	require.NoError(t, err)

	commit, changes = oversizeInsertDeleteChanges(t)
	_, err = consumeChanges(t, clone, commit, changes)
	require.NoError(t, err)

	assert.Len(t, logger.warningsSnapshot(), 1, "branches share one counter")

	clone.Dispose()
	warnings := logger.warningsSnapshot()
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[1], "4 blobs")
}

func TestBlobCacheDisposeStaysSilentWithoutSkips(t *testing.T) {
	logger := &recordingLogger{}
	cache := fixtureBlobCache()
	cache.l = logger
	cache.Dispose()
	assert.Empty(t, logger.warningsSnapshot())

	// A single skip is already fully described by its warning; no summary is added.
	cache.MaxBlobSize = 1024
	cache.oversize.record(logger, "x", plumbing.ZeroHash, 1, ErrBlobTooLarge)
	cache.Dispose()
	assert.Len(t, logger.warningsSnapshot(), 1)
}

func TestBlobCacheIsDisposable(t *testing.T) {
	var _ core.DisposablePipelineItem = &BlobCache{}
}
