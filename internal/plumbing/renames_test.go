package plumbing

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

var errTestRenameWorker = errors.New("rename worker failed")

func fixtureRenameAnalysis() *RenameAnalysis {
	ra := RenameAnalysis{SimilarityThreshold: 80}
	err := ra.Initialize(test.Repository)
	if err != nil {
		panic(err)
	}
	return &ra
}

func TestRenameAnalysisMeta(t *testing.T) {
	ra := fixtureRenameAnalysis()
	assert.Equal(t, "RenameAnalysis", ra.Name())
	assert.Len(t, ra.Provides(), 1)
	assert.Equal(t, DependencyTreeChanges, ra.Provides()[0])
	assert.Len(t, ra.Requires(), 2)
	assert.Equal(t, DependencyBlobCache, ra.Requires()[0])
	assert.Equal(t, DependencyTreeChanges, ra.Requires()[1])
	opts := ra.ListConfigurationOptions()
	assert.Len(t, opts, 2)
	assert.Equal(t, ConfigRenameAnalysisSimilarityThreshold, opts[0].Name)
	assert.Equal(t, ConfigRenameAnalysisTimeout, opts[1].Name)
	ra.SimilarityThreshold = 0

	assert.NoError(t, ra.Configure(map[string]any{
		ConfigRenameAnalysisSimilarityThreshold: 70,
		ConfigRenameAnalysisTimeout:             1000,
	}))
	assert.Equal(t, 70, ra.SimilarityThreshold)
	assert.Equal(t, time.Second, ra.Timeout)

	logger := core.NewLogger()
	assert.NoError(t, ra.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, ra.l)
	assert.Equal(t, 70, ra.SimilarityThreshold)
	assert.Equal(t, time.Second, ra.Timeout)
}

func TestRenameAnalysisRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&RenameAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "RenameAnalysis", summoned[0].Name())
	summoned = core.Registry.Summon((&RenameAnalysis{}).Provides()[0])
	assert.GreaterOrEqual(t, len(summoned), 1)
	matched := false
	for _, tp := range summoned {
		matched = matched || tp.Name() == "RenameAnalysis"
	}
	assert.True(t, matched)
}

func TestRenameAnalysisInitializeInvalidThreshold(t *testing.T) {
	ra := RenameAnalysis{SimilarityThreshold: -10}
	require.NoError(t, ra.Initialize(test.Repository))
	assert.Equal(t, RenameAnalysisDefaultThreshold, ra.SimilarityThreshold)
	ra = RenameAnalysis{SimilarityThreshold: 110}
	require.NoError(t, ra.Initialize(test.Repository))
	assert.Equal(t, RenameAnalysisDefaultThreshold, ra.SimilarityThreshold)
	ra = RenameAnalysis{SimilarityThreshold: 0}
	require.NoError(t, ra.Initialize(test.Repository))
	ra = RenameAnalysis{SimilarityThreshold: 100}
	require.NoError(t, ra.Initialize(test.Repository))
}

func TestRenameAnalysisConsume(t *testing.T) {
	ra := fixtureRenameAnalysis()
	changes := make(object.Changes, 3)
	// 2b1ed978194a94edeabbca6de7ff3b5771d4d665
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
			Name: testBurndownPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testBurndownPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2"),
			},
		},
	}
	changes[2] = &object.Change{
		From: object.ChangeEntry{
			Name: testHerculesMainPath,
			Tree: treeFrom,
			TreeEntry: object.TreeEntry{
				Name: testHerculesMainPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"),
			},
		}, To: object.ChangeEntry{
			Name: testHerculesMainPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testHerculesMainPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("f7d918ec500e2f925ecde79b51cc007bac27de72"),
			},
		},
	}
	cache := map[plumbing.Hash]*CachedBlob{}
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "f7d918ec500e2f925ecde79b51cc007bac27de72")
	deps := map[string]any{}
	deps[DependencyBlobCache] = cache
	deps[DependencyTreeChanges] = changes
	ra.SimilarityThreshold = 37
	res, err := ra.Consume(deps)
	assert.NoError(t, err)
	renamed := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, renamed, 2)
	ra.SimilarityThreshold = 39
	res, err = ra.Consume(deps)
	assert.NoError(t, err)
	renamed = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, renamed, 3)

	ra.SimilarityThreshold = 37
	ra.Timeout = time.Nanosecond
	res, err = ra.Consume(deps)
	assert.NoError(t, err)
	renamed = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, renamed, 3)
}

func TestSortableChanges(t *testing.T) {
	changes := sortableChanges{
		sortableChange{
			nil, plumbing.NewHash("0000000000000000000000000000000000000000"),
		}, sortableChange{
			nil, plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
		},
	}
	assert.True(t, changes.Less(0, 1))
	assert.False(t, changes.Less(1, 0))
	assert.False(t, changes.Less(0, 0))
	changes.Swap(0, 1)
	assert.Equal(t, "ffffffffffffffffffffffffffffffffffffffff", changes[0].hash.String())
	assert.Equal(t, "0000000000000000000000000000000000000000", changes[1].hash.String())
}

// renameHashList returns hashes in ascending order. They are random rather than hand-crafted on
// purpose: across 20 uniformly distributed bytes it is almost certain that each of two hashes
// has some byte smaller than the other's, so a comparison which does not stop at the first
// differing byte calls both of them the smaller one. Contrived hashes with long zero tails
// happen to dodge that, which is how the defect stayed hidden here while breaking real blobs.
func renameHashList() []string {
	return []string{
		"210ed7d4e36453fbf7e7421c3a2353176d749f29",
		"8fd5e88021edf1480d0fb62ef3758976bb656e6c",
		"ba6aab7d50956699bb80a5d2cb4c43ada70fe81b",
		"de4816fcfc32c1d313c384f8578b3d9adb8bc01e",
		"f72fd2a7ff7b0cab15c0937416a98affcd4a8b69",
	}
}

// TestSortableChangesIsAntisymmetric pins the ordering property sort.Sort requires. The previous
// comparator scanned all 20 bytes and returned true on the first byte which happened to be
// smaller, so a greater leading byte could be outvoted by a smaller trailing one and both
// a<b and b<a held. The all-zeros/all-ff pair in TestSortableChanges is the one case that
// mistake gets right, which is why it survived.
func TestSortableChangesIsAntisymmetric(t *testing.T) {
	hashes := renameHashList()
	for i, left := range hashes {
		for j, right := range hashes {
			a := sortableChange{nil, plumbing.NewHash(left)}
			b := sortableChange{nil, plumbing.NewHash(right)}

			assert.Equal(t, i < j, a.Less(&b), "%s < %s", left, right)
			assert.Equal(t, j < i, b.Less(&a), "%s < %s", right, left)
		}
	}
}

func renameChangeEntry(name, hash string) object.ChangeEntry {
	return object.ChangeEntry{
		Name: name,
		TreeEntry: object.TreeEntry{
			Name: name,
			Mode: 0o100644,
			Hash: plumbing.NewHash(hash),
		},
	}
}

// TestRenameAnalysisConsumeDetectsExactRenames covers the case which made burndown matrices go
// negative: a commit which moves several files verbatim (git reports them as R100) while also
// adding and deleting one unrelated file each. matchExactRenames walks the added and deleted
// lists as a single merge scan, so it only pairs identical hashes when both lists are sorted
// consistently. When they are not, a pure rename is emitted as a delete plus a create, the
// deleted file's lines are charged to its age band, and the recreated ones are credited to a
// fresh band - see PLAN.md B1c.
func TestRenameAnalysisConsumeDetectsExactRenames(t *testing.T) {
	ra := fixtureRenameAnalysis()

	hashes := renameHashList()
	changes := make(object.Changes, 0, len(hashes))

	for i, hash := range hashes[:3] {
		changes = append(changes, &object.Change{
			From: renameChangeEntry(fmt.Sprintf("old/%d.txt", i), hash),
			To:   renameChangeEntry(fmt.Sprintf("new/%d.txt", i), hash),
		})
	}

	// One genuine delete and one genuine create, so the two lists hold different hash sets and
	// an inconsistent sort actually reorders them relative to each other.
	changes = append(changes, &object.Change{
		From: renameChangeEntry("gone.txt", hashes[3]),
		To:   object.ChangeEntry{},
	}, &object.Change{
		From: object.ChangeEntry{},
		To:   renameChangeEntry("fresh.txt", hashes[4]),
	})

	// Exact matches are resolved by hash alone, so only the two leftovers need to be in the
	// cache. They are deliberately under RenameAnalysisMinimumSize so the similarity pass skips
	// them and the assertion below is only about the exact stage.
	cache := map[plumbing.Hash]*CachedBlob{}
	for _, hash := range hashes[3:] {
		parsed := plumbing.NewHash(hash)
		cache[parsed] = &CachedBlob{
			Blob: object.Blob{Hash: parsed, Size: 4},
			Data: []byte("tiny"),
		}
	}

	res, err := ra.Consume(map[string]any{
		DependencyBlobCache:   cache,
		DependencyTreeChanges: changes,
	})
	require.NoError(t, err)

	renamed, ok := res[DependencyTreeChanges].(object.Changes)
	require.True(t, ok)
	require.Len(t, renamed, 5, "3 renames + 1 delete + 1 create")

	pairs := map[string]string{}

	for _, change := range renamed {
		if change.From.Name != "" && change.To.Name != "" {
			pairs[change.From.Name] = change.To.Name
		}
	}

	assert.Equal(t, map[string]string{
		"old/0.txt": "new/0.txt",
		"old/1.txt": "new/1.txt",
		"old/2.txt": "new/2.txt",
	}, pairs, "every verbatim move must survive as a single rename")
}

func TestSortableBlobs(t *testing.T) {
	blobs := sortableBlobs{
		sortableBlob{
			nil, int64(0),
		}, sortableBlob{
			nil, int64(1),
		},
	}
	assert.True(t, blobs.Less(0, 1))
	assert.False(t, blobs.Less(1, 0))
	assert.False(t, blobs.Less(0, 0))
	blobs.Swap(0, 1)
	assert.Equal(t, int64(1), blobs[0].size)
	assert.Equal(t, int64(0), blobs[1].size)
}

func TestRenameAnalysisFork(t *testing.T) {
	ra1 := fixtureRenameAnalysis()
	clones := ra1.Fork(1)
	assert.Len(t, clones, 1)
	ra2 := clones[0].(*RenameAnalysis)
	assert.Same(t, ra1, ra2)
	ra1.Merge([]core.PipelineItem{ra2})
}

func TestRenameAnalysisSizesAreClose(t *testing.T) {
	ra := fixtureRenameAnalysis()
	assert.True(t, ra.sizesAreClose(941, 963))
	assert.True(t, ra.sizesAreClose(941, 1150))
	assert.True(t, ra.sizesAreClose(941, 803))
	assert.False(t, ra.sizesAreClose(1320, 1668))
}

func TestRunRenameWorkersSimultaneousErrorsDoNotDeadlock(t *testing.T) {
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	worker := func(context.Context) renameMatchResult {
		ready <- struct{}{}
		<-release

		return renameMatchResult{err: errTestRenameWorker}
	}

	done := make(chan [2]renameMatchResult, 1)
	go func() {
		first, second := runRenameWorkers(context.Background(), worker, worker)
		done <- [2]renameMatchResult{first, second}
	}()

	<-ready
	<-ready
	close(release)

	select {
	case results := <-done:
		require.ErrorIs(t, results[0].err, errTestRenameWorker)
		require.ErrorIs(t, results[1].err, errTestRenameWorker)
	case <-time.After(time.Second):
		t.Fatal("simultaneous rename worker errors deadlocked")
	}
}

func TestRenameComparisonHonorsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ra := fixtureRenameAnalysis()
	blob := &CachedBlob{Data: []byte("hello\n")}
	blob.Size = int64(len(blob.Data))
	_, err := ra.blobsAreCloseContext(ctx, blob, blob)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRenameAnalysisSortRenameCandidates(t *testing.T) {
	candidates := []int{0, 1, 2, 3}
	sortRenameCandidates(candidates, "test_regression.py", func(i int) string {
		return []string{"gather_nd_op.h", "test.py", "test_file_system.cc", "regression.py"}[i]
	})
	assert.Equal(t, 3, candidates[0])
	assert.Equal(t, 1, candidates[1])
}

func TestBlobsAreCloseFlakyBug(t *testing.T) {
	gitBlob1, err := test.Repository.BlobObject(plumbing.NewHash(
		"29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2",
	))
	if err != nil {
		t.Fatalf("get 29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2 %v", err)
	}
	gitBlob2, err := test.Repository.BlobObject(plumbing.NewHash(
		"baa64828831d174f40140e4b3cfa77d1e917a2c1",
	))
	if err != nil {
		t.Fatalf("get baa64828831d174f40140e4b3cfa77d1e917a2c1 %v", err)
	}
	blob1 := &CachedBlob{Blob: *gitBlob1}
	blob2 := &CachedBlob{Blob: *gitBlob2}
	err = blob1.Cache()
	if err != nil {
		t.Fatalf("read 29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2 %v", err)
	}
	err = blob2.Cache()
	if err != nil {
		t.Fatalf("read baa64828831d174f40140e4b3cfa77d1e917a2c1 %v", err)
	}
	wg := sync.WaitGroup{}
	gr := 10 // number of concurrent goroutines
	wg.Add(gr)
	for range gr {
		go func() {
			ra := fixtureRenameAnalysis()
			ra.SimilarityThreshold = 37
			result, err := ra.blobsAreClose(blob1, blob2)
			assert.NoError(t, err)
			assert.True(t, result)
			result, err = ra.blobsAreClose(blob2, blob1)
			assert.NoError(t, err)
			assert.True(t, result)

			ra.SimilarityThreshold = 39
			result, err = ra.blobsAreClose(blob1, blob2)
			assert.NoError(t, err)
			assert.False(t, result)
			result, err = ra.blobsAreClose(blob2, blob1)
			assert.NoError(t, err)
			assert.False(t, result)
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestBlobsAreCloseText(t *testing.T) {
	blob1 := &CachedBlob{Data: []byte("hello, world!")}
	blob2 := &CachedBlob{Data: []byte("hello, world?")}
	blob1.Size = int64(len(blob1.Data))
	blob2.Size = int64(len(blob2.Data))
	ra := fixtureRenameAnalysis()
	result, err := ra.blobsAreClose(blob1, blob2)
	require.NoError(t, err)
	assert.True(t, result)

	blob1.Data = []byte("hello, mloncode")
	blob1.Size = int64(len(blob1.Data))
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.False(t, result)
}

func TestBlobsAreCloseBinary(t *testing.T) {
	blob1 := &CachedBlob{}
	blob2 := &CachedBlob{}
	ra := fixtureRenameAnalysis()
	result, err := ra.blobsAreClose(blob1, blob2)
	require.NoError(t, err)
	assert.True(t, result)

	blob1.Data = make([]byte, 100)
	blob2.Data = blob1.Data
	blob1.Size = 100
	blob2.Size = 100
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.True(t, result)

	blob1.Data = bytes.Repeat([]byte{1}, 100)
	blob2.Data = blob1.Data
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.True(t, result)

	for i := range 100 {
		blob1.Data[i] = byte(i)
	}
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.True(t, result)

	blob2.Data = make([]byte, 100)
	for i := range 80 {
		blob2.Data[i] = byte(i)
	}
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.True(t, result)

	blob2.Data[79] = 0
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.False(t, result)

	blob1.Data = []byte("hello, world!")
	blob1.Size = int64(len(blob2.Data))
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.False(t, result)

	blob1.Data, blob2.Data = blob2.Data, blob1.Data
	blob1.Size, blob2.Size = blob2.Size, blob1.Size
	result, err = ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.False(t, result)
}

func loadData(t *testing.T, name string) []byte {
	t.Helper()
	gzsource, err := os.Open(path.Join("..", "test_data", name))
	if err != nil {
		t.Errorf("open ../test_data/%s: %v", name, err)
	}
	defer func() { _ = gzsource.Close() }()
	gzreader, err := gzip.NewReader(gzsource)
	if err != nil {
		t.Errorf("gzip ../test_data/%s: %v", name, err)
	}
	data, err := io.ReadAll(gzreader)
	if err != nil {
		t.Errorf("gzip ../test_data/%s: %v", name, err)
	}
	return data
}

func TestBlobsAreCloseBug1(t *testing.T) {
	data := loadData(t, "rename_bug1.xml.gz")
	blob1 := &CachedBlob{Data: data}
	blob2 := &CachedBlob{Data: data}
	blob1.Size = int64(len(data))
	blob2.Size = int64(len(data))
	ra := fixtureRenameAnalysis()
	result, err := ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.True(t, result)
}

func TestBlobsAreCloseBug2(t *testing.T) {
	data1 := loadData(t, "rename_bug2.base.gz")
	data2 := loadData(t, "rename_bug2.head.gz")
	blob1 := &CachedBlob{Data: data1}
	blob2 := &CachedBlob{Data: data2}
	blob1.Size = int64(len(data1))
	blob2.Size = int64(len(data2))
	ra := fixtureRenameAnalysis()
	// This large fixture protects the similarity result, not the deadline
	// behavior covered by the focused context tests. Leave enough headroom for
	// package-parallel and repeated test runs on loaded builders.
	ra.Timeout = 2 * time.Minute
	result, err := ra.blobsAreClose(blob1, blob2)
	assert.NoError(t, err)
	assert.False(t, result)
}
