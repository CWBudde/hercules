package plumbing

import (
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureBlobCache() *BlobCache {
	cache := &BlobCache{}
	err := cache.Initialize(test.Repository)
	if err != nil {
		panic(err)
	}
	return cache
}

func AddHash(t *testing.T, cache map[plumbing.Hash]*CachedBlob, hash string) {
	t.Helper()
	objhash := plumbing.NewHash(hash)
	blob, err := test.Repository.BlobObject(objhash)
	require.NoError(t, err)
	cb := &CachedBlob{Blob: *blob}
	err = cb.Cache()
	require.NoError(t, err)
	cache[objhash] = cb
}

func TestBlobCacheConfigureInitialize(t *testing.T) {
	cache := fixtureBlobCache()
	assert.Equal(t, test.Repository, cache.repository)
	assert.False(t, cache.FailOnMissingSubmodules)
	assert.Equal(t, DefaultBlobCacheMaxBlobSize, cache.MaxBlobSize)
	assert.Equal(t, DefaultBlobCacheMaxCommitSize, cache.MaxCommitSize)
	facts := map[string]any{}
	facts[ConfigBlobCacheFailOnMissingSubmodules] = true
	facts[ConfigBlobCacheMaxBlobSize] = 1024
	facts[ConfigBlobCacheMaxCommitSize] = 4096
	require.NoError(t, cache.Configure(facts))
	assert.True(t, cache.FailOnMissingSubmodules)
	assert.Equal(t, int64(1024), cache.MaxBlobSize)
	assert.Equal(t, int64(4096), cache.MaxCommitSize)
	facts = map[string]any{}
	require.NoError(t, cache.Configure(facts))
	assert.True(t, cache.FailOnMissingSubmodules)
	assert.Equal(t, DefaultBlobCacheMaxBlobSize, cache.MaxBlobSize)
	assert.Equal(t, DefaultBlobCacheMaxCommitSize, cache.MaxCommitSize)
}

func TestBlobCacheMetadata(t *testing.T) {
	cache := fixtureBlobCache()
	assert.Equal(t, "BlobCache", cache.Name())
	assert.Len(t, cache.Provides(), 1)
	assert.Equal(t, DependencyBlobCache, cache.Provides()[0])
	assert.Len(t, cache.Requires(), 1)
	changes := &TreeDiff{}
	assert.Equal(t, cache.Requires()[0], changes.Provides()[0])
	opts := cache.ListConfigurationOptions()
	assert.Len(t, opts, 3)
	assert.Equal(t, ConfigBlobCacheFailOnMissingSubmodules, opts[0].Name)
	assert.Equal(t, ConfigBlobCacheMaxBlobSize, opts[1].Name)
	assert.Equal(t, int(DefaultBlobCacheMaxBlobSize), opts[1].Default)
	assert.Equal(t, ConfigBlobCacheMaxCommitSize, opts[2].Name)
	assert.Equal(t, int(DefaultBlobCacheMaxCommitSize), opts[2].Default)
}

func TestCachedBlobCacheWithLimitRejectsDeclaredOversize(t *testing.T) {
	blob, err := test.Repository.BlobObject(plumbing.NewHash(
		"c872b8d2291a5224e2c9f6edd7f46039b96b4742",
	))
	require.NoError(t, err)

	cached := &CachedBlob{Blob: *blob}
	err = cached.CacheWithLimit(blob.Size - 1)
	require.ErrorIs(t, err, ErrBlobTooLarge)
	assert.Nil(t, cached.Data)
}

func TestReadExactBlobRejectsDeclaredSizeMismatch(t *testing.T) {
	hash := plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff")

	data, err := readExactBlob(strings.NewReader("abc"), 4, hash)
	require.ErrorIs(t, err, errIncompleteBlobRead)
	assert.Nil(t, data)

	data, err = readExactBlob(strings.NewReader("abcde"), 4, hash)
	require.ErrorIs(t, err, errIncompleteBlobRead)
	assert.Nil(t, data)

	data, err = readExactBlob(strings.NewReader("abcd"), 4, hash)
	require.NoError(t, err)
	assert.Equal(t, []byte("abcd"), data)
}

func TestBlobCacheCommitBudgetIsAggregateUpperBound(t *testing.T) {
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

	change := &object.Change{From: object.ChangeEntry{
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
	}}
	cache := fixtureBlobCache()
	cache.MaxCommitSize = 10_000

	result, err := cache.Consume(map[string]any{
		core.DependencyCommit: commit,
		DependencyTreeChanges: object.Changes{
			change,
		},
	})
	require.ErrorIs(t, err, ErrBlobCacheBudgetExceeded)
	assert.Nil(t, result)
}

func TestBlobCacheRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&BlobCache{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "BlobCache", summoned[0].Name())
	summoned = core.Registry.Summon((&BlobCache{}).Provides()[0])
	assert.Len(t, summoned, 1)
	assert.Equal(t, "BlobCache", summoned[0].Name())
}

func TestBlobCacheConsumeModification(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
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
	}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	cacheIface, exists := result[DependencyBlobCache]
	assert.True(t, exists)
	cache := cacheIface.(map[plumbing.Hash]*CachedBlob)
	assert.Len(t, cache, 2)
	blobFrom, exists := cache[plumbing.NewHash("1cacfc1bf0f048eb2f31973750983ae5d8de647a")]
	assert.True(t, exists)
	blobTo, exists := cache[plumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742")]
	assert.True(t, exists)
	assert.Equal(t, int64(8969), blobFrom.Size)
	assert.Equal(t, int64(9481), blobTo.Size)
}

func TestBlobCacheConsumeInsertionDeletion(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	changes := make(object.Changes, 2)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"251f2094d7b523d5bcc60e663b6cf38151bf8844",
	))
	changes[0] = &object.Change{
		From: object.ChangeEntry{
			Name: testAnalyserPath,
			Tree: treeFrom,
			TreeEntry: object.TreeEntry{
				Name: testAnalyserPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
			},
		}, To: object.ChangeEntry{},
	}
	changes[1] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testPipelinePath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testPipelinePath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("db99e1890f581ad69e1527fe8302978c661eb473"),
			},
		},
	}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	cacheIface, exists := result[DependencyBlobCache]
	assert.True(t, exists)
	cache := cacheIface.(map[plumbing.Hash]*CachedBlob)
	assert.Len(t, cache, 2)
	blobFrom, exists := cache[plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1")]
	assert.True(t, exists)
	blobTo, exists := cache[plumbing.NewHash("db99e1890f581ad69e1527fe8302978c661eb473")]
	assert.True(t, exists)
	assert.Equal(t, int64(26446), blobFrom.Size)
	assert.Equal(t, int64(5576), blobTo.Size)
}

func TestBlobCacheConsumeNoAction(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{}, To: object.ChangeEntry{}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	assert.Nil(t, result)
	require.Error(t, err)
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeFrom,
		TreeEntry: object.TreeEntry{},
	}, To: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeTo,
		TreeEntry: object.TreeEntry{},
	}}
	result, err = fixtureBlobCache().Consume(deps)
	assert.Nil(t, result)
	require.Error(t, err)
}

func TestBlobCacheConsumeBadHashes(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeFrom,
		TreeEntry: object.TreeEntry{},
	}, To: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeTo,
		TreeEntry: object.TreeEntry{},
	}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	assert.Nil(t, result)
	require.Error(t, err)
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeFrom,
		TreeEntry: object.TreeEntry{},
	}, To: object.ChangeEntry{}}
	result, err = fixtureBlobCache().Consume(deps)
	// Deleting a missing blob is fine
	assert.NotNil(t, result)
	require.NoError(t, err)
	changes[0] = &object.Change{
		From: object.ChangeEntry{},
		To: object.ChangeEntry{
			Name:      testLaboursPath,
			Tree:      treeTo,
			TreeEntry: object.TreeEntry{},
		},
	}
	result, err = fixtureBlobCache().Consume(deps)
	assert.Nil(t, result)
	require.Error(t, err)
}

func TestBlobCacheConsumeInvalidHash(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
		},
	}, To: object.ChangeEntry{
		Name:      testLaboursPath,
		Tree:      treeTo,
		TreeEntry: object.TreeEntry{},
	}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	assert.Nil(t, result)
	require.Error(t, err)
}

func TestBlobCacheGetBlob(t *testing.T) {
	cache := fixtureBlobCache()
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	entry := object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("80fe25955b8e725feee25c08ea5759d74f8b670d"),
		},
	}
	getter := func(path string) (*object.File, error) {
		assert.Equal(t, ".gitmodules", path)
		commit, _ := test.Repository.CommitObject(plumbing.NewHash(
			"13272b66c55e1ba1237a34104f30b84d7f6e4082",
		))
		return commit.File("test_data/gitmodules")
	}
	blob, err := cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.ErrorIs(t, err, plumbing.ErrObjectNotFound)
	getter = func(path string) (*object.File, error) {
		assert.Equal(t, ".gitmodules", path)
		commit, _ := test.Repository.CommitObject(plumbing.NewHash(
			"13272b66c55e1ba1237a34104f30b84d7f6e4082",
		))
		return commit.File("test_data/gitmodules_empty")
	}
	blob, err = cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.ErrorIs(t, err, plumbing.ErrObjectNotFound)
}

func TestBlobCacheDeleteInvalidBlob(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	changes[0] = &object.Change{
		From: object.ChangeEntry{
			Name: testAnalyserPath,
			Tree: treeFrom,
			TreeEntry: object.TreeEntry{
				Name: testAnalyserPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
			},
		}, To: object.ChangeEntry{},
	}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	cacheIface, exists := result[DependencyBlobCache]
	assert.True(t, exists)
	cache := cacheIface.(map[plumbing.Hash]*CachedBlob)
	assert.Len(t, cache, 1)
	blobFrom, exists := cache[plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff")]
	assert.True(t, exists)
	assert.Equal(t, int64(0), blobFrom.Size)
}

func TestBlobCacheInsertInvalidBlob(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	changes := make(object.Changes, 1)
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"251f2094d7b523d5bcc60e663b6cf38151bf8844",
	))
	changes[0] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testPipelinePath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testPipelinePath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
			},
		},
	}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	result, err := fixtureBlobCache().Consume(deps)
	require.Error(t, err)
	assert.Empty(t, result)
}

func TestBlobCacheGetBlobIgnoreMissing(t *testing.T) {
	cache := fixtureBlobCache()
	cache.FailOnMissingSubmodules = false
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	entry := object.ChangeEntry{
		Name: core.DependencyCommit,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: core.DependencyCommit,
			Mode: 0o160000,
			Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
		},
	}
	getter := func(path string) (*object.File, error) {
		return nil, plumbing.ErrObjectNotFound
	}
	blob, err := cache.getBlob(&entry, getter)
	assert.NotNil(t, blob)
	require.NoError(t, err)
	assert.Equal(t, int64(0), blob.Size)
	cache.FailOnMissingSubmodules = true
	getter = func(path string) (*object.File, error) {
		assert.Equal(t, ".gitmodules", path)
		commit, _ := test.Repository.CommitObject(plumbing.NewHash(
			"13272b66c55e1ba1237a34104f30b84d7f6e4082",
		))
		return commit.File("test_data/gitmodules")
	}
	blob, err = cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.Error(t, err)
}

func TestBlobCacheGetBlobGitModulesErrors(t *testing.T) {
	cache := fixtureBlobCache()
	cache.FailOnMissingSubmodules = true
	entry := object.ChangeEntry{
		Name: testLaboursPath,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o160000,
			Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
		},
	}
	getter := func(path string) (*object.File, error) {
		return nil, plumbing.ErrInvalidType
	}
	blob, err := cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.ErrorIs(t, err, plumbing.ErrInvalidType)
	getter = func(path string) (*object.File, error) {
		blob, _ := internal.CreateDummyBlob(
			plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"), true,
		)
		return &object.File{Name: "fake", Blob: *blob}, nil
	}
	blob, err = cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.Error(t, err)
	assert.ErrorContains(t, err, "dummy failure")
	getter = func(path string) (*object.File, error) {
		blob, _ := test.Repository.BlobObject(plumbing.NewHash(
			"4434197c2b0509d990f09d53a3cabb910bfd34b7",
		))
		return &object.File{Name: ".gitmodules", Blob: *blob}, nil
	}
	blob, err = cache.getBlob(&entry, getter)
	assert.Nil(t, blob)
	require.Error(t, err)
	assert.NotEqual(t, err.Error(), plumbing.ErrObjectNotFound.Error())
}

func TestBlobCacheFork(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	changes := make(object.Changes, 1)
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"251f2094d7b523d5bcc60e663b6cf38151bf8844",
	))
	hash := plumbing.NewHash("db99e1890f581ad69e1527fe8302978c661eb473")
	changes[0] = &object.Change{From: object.ChangeEntry{}, To: object.ChangeEntry{
		Name: testPipelinePath,
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: testPipelinePath,
			Mode: 0o100644,
			Hash: hash,
		},
	}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[DependencyTreeChanges] = changes
	cache1 := fixtureBlobCache()
	cache1.FailOnMissingSubmodules = true
	cache1.MaxBlobSize = 12345
	cache1.MaxCommitSize = 23456
	_, err := cache1.Consume(deps)
	require.NoError(t, err)
	clones := cache1.Fork(1)
	assert.Len(t, clones, 1)
	cache2 := clones[0].(*BlobCache)
	assert.True(t, cache2.FailOnMissingSubmodules)
	assert.Equal(t, int64(12345), cache2.MaxBlobSize)
	assert.Equal(t, int64(23456), cache2.MaxCommitSize)
	assert.Equal(t, cache1.repository, cache2.repository)
	cache1.cache[plumbing.ZeroHash] = nil
	assert.Len(t, cache1.cache, 2)
	assert.Len(t, cache2.cache, 1)
	assert.Equal(t, cache1.cache[hash].Size, cache2.cache[hash].Size)
	// just for the sake of it
	cache1.Merge([]core.PipelineItem{cache2})
}
