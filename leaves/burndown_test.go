package leaves

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/internal/test/fixtures"
)

func LineHistoryAnalyser() *linehistory.LineHistoryAnalyser {
	lh := &linehistory.LineHistoryAnalyser{}
	_ = lh.Initialize(test.Repository)
	return lh
}

func AddHash(t *testing.T, cache map[plumbing.Hash]*items.CachedBlob, hash string) {
	t.Helper()
	objhash := plumbing.NewHash(hash)
	blob, err := test.Repository.BlobObject(objhash)
	assert.NoError(t, err)
	cb := &items.CachedBlob{Blob: *blob}
	err = cb.Cache()
	assert.NoError(t, err)
	cache[objhash] = cb
}

func TestBurndownMeta(t *testing.T) {
	bd := BurndownAnalysis{}
	assert.Equal(t, "Burndown", bd.Name())
	assert.Empty(t, bd.Provides())
	required := [...]string{linehistory.DependencyLineHistory, identity.DependencyAuthor}
	for _, name := range required {
		assert.Contains(t, bd.Requires(), name)
	}
	opts := bd.ListConfigurationOptions()
	matches := 0
	for _, opt := range opts {
		switch opt.Name {
		case ConfigBurndownGranularity, ConfigBurndownSampling, ConfigBurndownTrackFiles,
			ConfigBurndownTrackPeople, ConfigBurndownHibernationDisk, ConfigBurndownHibernationDir:
			matches++
		}
	}
	assert.Len(t, opts, matches)
	assert.Equal(t, "burndown", bd.Flag())
	logger := core.NewLogger()
	assert.NoError(t, bd.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, bd.l)
}

func TestBurndownConfigure(t *testing.T) {
	bd := BurndownAnalysis{}
	facts := map[string]any{}
	facts[ConfigBurndownGranularity] = 100
	facts[ConfigBurndownSampling] = 200
	facts[ConfigBurndownTrackFiles] = true
	facts[ConfigBurndownTrackPeople] = true
	facts[items.FactTickSize] = 24 * time.Hour

	people := []string{"P1", "P2", "P3", "P4", "P5"}

	facts[core.FactIdentityResolver] = core.NewIdentityResolver(people, nil)
	assert.NoError(t, bd.Configure(facts))
	assert.Equal(t, 100, bd.Granularity)
	assert.Equal(t, 200, bd.Sampling)
	assert.True(t, bd.TrackFiles)
	assert.Equal(t, 24*time.Hour, bd.tickSize)
	assert.Len(t, people, bd.peopleResolver.Count())
	assert.Len(t, people, bd.peopleResolver.MaxCount())

	facts[ConfigBurndownTrackPeople] = false
	assert.NoError(t, bd.Configure(facts))
	assert.Nil(t, bd.peopleResolver)

	facts = map[string]any{}
	bd.peopleResolver = core.NewIdentityResolver(people, nil)
	assert.NoError(t, bd.Configure(facts))
	assert.Equal(t, 100, bd.Granularity)
	assert.Equal(t, 200, bd.Sampling)
	assert.True(t, bd.TrackFiles)
	assert.NotNil(t, bd.peopleResolver)
}

func TestBurndownRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&BurndownAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "Burndown", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&BurndownAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestBurndownInitialize(t *testing.T) {
	// BurndownAnalysis.Initialize() now forces safer defaults to prevent crashes
	// from mismatched sampling/granularity values.
	bd := BurndownAnalysis{
		Sampling:    -10,
		Granularity: DefaultBurndownGranularity,
	}
	assert.NoError(t, bd.Initialize(test.Repository))
	// Initialize forces both to DefaultBurndownGranularity for safety
	assert.Equal(t, DefaultBurndownGranularity, bd.Sampling)
	assert.Equal(t, DefaultBurndownGranularity, bd.Granularity)

	// Even if we set different values, Initialize() forces defaults
	bd.Sampling = 0
	bd.Granularity = DefaultBurndownGranularity - 1
	assert.NoError(t, bd.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, bd.Sampling)
	assert.Equal(t, DefaultBurndownGranularity, bd.Granularity)

	bd.Sampling = DefaultBurndownGranularity - 1
	bd.Granularity = -10
	assert.NoError(t, bd.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, bd.Sampling)
	assert.Equal(t, DefaultBurndownGranularity, bd.Granularity)
}

func TestBurndownConsumeFinalize(t *testing.T) {
	deps := map[string]any{}

	// stage 1
	deps[identity.DependencyAuthor] = 0
	deps[items.DependencyTick] = 0
	cache := map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, "291286b4ac41952cbd1389fda66420ec03c1a9fe")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "dc248ba2b22048cc730c571a748e8ffcf7085ab9")
	deps[items.DependencyBlobCache] = cache
	changes := make(object.Changes, 3)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"a1eb2ea76eb7f9bfbde9b243861474421000eb96",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"994eac1cd07235bb9815e547a75c84265dea00f5",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name: testAnalyserPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testAnalyserPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("dc248ba2b22048cc730c571a748e8ffcf7085ab9"),
		},
	}, To: object.ChangeEntry{
		Name: testAnalyserPath,
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: testAnalyserPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
		},
	}}
	changes[1] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testHerculesMainPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testHerculesMainPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"),
			},
		},
	}
	changes[2] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testTravisPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testTravisPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe"),
			},
		},
	}
	deps[items.DependencyTreeChanges] = changes
	fd := fixtures.FileDiff()
	{
		result, err := fd.Consume(deps)
		assert.NoError(t, err)
		deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]
		deps[core.DependencyCommit], _ = test.Repository.CommitObject(plumbing.NewHash(
			"cce947b98a050c6d356bc6ba95030254914027b1",
		))
	}

	lh := LineHistoryAnalyser()
	{
		result, err := lh.Consume(deps)
		assert.NoError(t, err)
		deps[linehistory.DependencyLineHistory] = result[linehistory.DependencyLineHistory]
	}

	bd := BurndownAnalysis{
		Granularity:    30,
		Sampling:       30,
		TrackFiles:     true,
		peopleResolver: core.NewIdentityResolver([]string{"P1", "P2"}, nil),
	}

	totalLines := int64(0)

	{
		assert.NoError(t, bd.Initialize(test.Repository))

		result, err := bd.Consume(deps)
		assert.Nil(t, result)
		assert.NoError(t, err)

		expectedFiles := map[string]int64{
			testHerculesMainPath: 207,
			testAnalyserPath:     926,
			testTravisPath:       12,
		}

		for _, v := range expectedFiles {
			totalLines += v
		}
		assert.Len(t, bd.peopleHistories, 2)

		assert.Equal(t, bd.peopleHistories[0][0].deltas[0], totalLines)
		// assert.Equal(t, bd.peopleHistories[0][0].totalInsert, totalLines)
		// assert.Equal(t, bd.peopleHistories[0][0].totalDelete, int64(0))

		assert.Len(t, bd.globalHistory, 1)
		assert.Equal(t, bd.globalHistory[0].deltas[0], totalLines)
		// assert.Equal(t, bd.globalHistory[0].totalInsert, totalLines)
		// assert.Equal(t, bd.globalHistory[0].totalDelete, int64(0))

		assert.Len(t, bd.fileHistories, len(expectedFiles))
		// for k, v := range expectedFiles {
		//	assert.Equal(t, bd.fileHistories[k][0].totalInsert, v)
		//	assert.Equal(t, bd.fileHistories[k][0].totalDelete, int64(0))
		//}
	}

	deps[identity.DependencyAuthor] = 1

	{
		bd2 := BurndownAnalysis{
			Granularity: 30,
			Sampling:    0,
		}
		assert.NoError(t, bd2.Initialize(test.Repository))
		_, err := bd2.Consume(deps)
		assert.NoError(t, err)
		assert.Empty(t, bd2.peopleHistories)
		assert.Empty(t, bd2.fileHistories)
	}

	// stage 2
	// 2b1ed978194a94edeabbca6de7ff3b5771d4d665
	deps[items.DependencyTick] = 30
	cache = map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, "291286b4ac41952cbd1389fda66420ec03c1a9fe")
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "f7d918ec500e2f925ecde79b51cc007bac27de72")
	deps[items.DependencyBlobCache] = cache
	changes = make(object.Changes, 3)
	treeFrom, _ = test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	treeTo, _ = test.Repository.TreeObject(plumbing.NewHash(
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
		}, To: object.ChangeEntry{
			Name: testBurndownPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testBurndownPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2"),
			},
		},
	}
	changes[1] = &object.Change{
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
	changes[2] = &object.Change{
		From: object.ChangeEntry{
			Name: testTravisPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testTravisPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe"),
			},
		}, To: object.ChangeEntry{},
	}
	deps[items.DependencyTreeChanges] = changes
	{
		fd = fixtures.FileDiff()
		result, err := fd.Consume(deps)
		assert.NoError(t, err)
		deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]
	}

	{
		result, err := lh.Consume(deps)
		assert.NoError(t, err)
		deps[linehistory.DependencyLineHistory] = result[linehistory.DependencyLineHistory]
	}

	{
		result, err := bd.Consume(deps)
		assert.Nil(t, result)
		assert.NoError(t, err)
	}

	assert.Len(t, bd.peopleHistories, 2)
	assert.Equal(t, bd.peopleHistories[0][0].deltas[0], totalLines)
	// assert.Equal(t, bd.peopleHistories[0][0].totalInsert, totalLines)
	// assert.Equal(t, bd.peopleHistories[0][0].totalDelete, int64(0))

	assert.Len(t, bd.peopleHistories[0][30].deltas, 1)
	assert.Equal(t, int64(-681), bd.peopleHistories[0][30].deltas[0])
	// assert.Equal(t, bd.peopleHistories[0][30].totalInsert, int64(0))
	// assert.Equal(t, bd.peopleHistories[0][30].totalDelete, int64(-681))

	assert.Len(t, bd.peopleHistories[1][30].deltas, 1)
	assert.Equal(t, int64(369), bd.peopleHistories[1][30].deltas[30])
	// assert.Equal(t, bd.peopleHistories[1][30].totalInsert, int64(369))
	// assert.Equal(t, bd.peopleHistories[1][30].totalDelete, int64(0))

	assert.Len(t, bd.globalHistory, 2)
	assert.Len(t, bd.globalHistory[0].deltas, 1)
	assert.Equal(t, bd.globalHistory[0].deltas[0], totalLines)
	// assert.Equal(t, bd.globalHistory[0].totalInsert, totalLines)
	// assert.Equal(t, bd.globalHistory[0].totalDelete, int64(0))

	assert.Len(t, bd.globalHistory[30].deltas, 2)
	assert.Equal(t, int64(-681), bd.globalHistory[30].deltas[0])
	assert.Equal(t, int64(369), bd.globalHistory[30].deltas[30])
	// assert.Equal(t, bd.globalHistory[30].totalInsert, int64(369))
	// assert.Equal(t, bd.globalHistory[30].totalDelete, int64(-681))

	assert.Len(t, bd.fileHistories, 2)

	out := bd.Finalize().(BurndownResult)
	/*
			GlobalHistory   [][]int64
			FileHistories   map[string][][]int64
		    FileOwnership   map[string]map[int]int
			PeopleHistories [][][]int64
			PeopleMatrix    [][]int64
	*/
	assert.Len(t, out.GlobalHistory, 2)
	for i := range 2 {
		assert.Len(t, out.GlobalHistory[i], 2)
	}
	assert.Len(t, out.GlobalHistory, 2)
	assert.Equal(t, int64(1145), out.GlobalHistory[0][0])
	assert.Equal(t, int64(0), out.GlobalHistory[0][1])
	assert.Equal(t, int64(464), out.GlobalHistory[1][0])
	assert.Equal(t, int64(369), out.GlobalHistory[1][1])
	assert.Len(t, out.FileHistories, 2)
	assert.Len(t, out.FileHistories[testHerculesMainPath], 2)
	assert.Len(t, out.FileHistories[testBurndownPath], 2)
	assert.Len(t, out.FileHistories[testHerculesMainPath][0], 2)
	assert.Len(t, out.FileHistories[testBurndownPath][0], 2)
	assert.Len(t, out.FileOwnership, 2)
	assert.Equal(t, map[int]int{0: 171, 1: 119}, out.FileOwnership[testHerculesMainPath])
	assert.Equal(t, map[int]int{0: 293, 1: 250}, out.FileOwnership[testBurndownPath])
	assert.Len(t, out.PeopleMatrix, 2)
	assert.Len(t, out.PeopleMatrix[0], 4)
	assert.Len(t, out.PeopleMatrix[1], 4)
	assert.Equal(t, int64(1145), out.PeopleMatrix[0][0])
	assert.Equal(t, int64(0), out.PeopleMatrix[0][1])
	assert.Equal(t, int64(0), out.PeopleMatrix[0][2])
	assert.Equal(t, int64(-681), out.PeopleMatrix[0][3])
	assert.Equal(t, int64(369), out.PeopleMatrix[1][0])
	assert.Equal(t, int64(0), out.PeopleMatrix[1][1])
	assert.Equal(t, int64(0), out.PeopleMatrix[1][2])
	assert.Equal(t, int64(0), out.PeopleMatrix[1][3])
	assert.Len(t, out.PeopleHistories, 2)
	for i := range 2 {
		assert.Len(t, out.PeopleHistories[i], 2)
		assert.Len(t, out.PeopleHistories[i][0], 2)
		assert.Len(t, out.PeopleHistories[i][1], 2)
	}
}

func prepareBDForSerialization(t *testing.T, firstAuthor, secondAuthor int) BurndownResult {
	t.Helper()
	bd := BurndownAnalysis{
		Granularity:    30,
		Sampling:       30,
		TrackFiles:     true,
		peopleResolver: core.NewIdentityResolver([]string{"P1", "P2"}, nil),
		tickSize:       24 * time.Hour,
	}
	assert.NoError(t, bd.Initialize(test.Repository))
	deps := map[string]any{}
	// stage 1
	deps[identity.DependencyAuthor] = firstAuthor
	deps[items.DependencyTick] = 0
	cache := map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, "291286b4ac41952cbd1389fda66420ec03c1a9fe")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "dc248ba2b22048cc730c571a748e8ffcf7085ab9")
	deps[items.DependencyBlobCache] = cache
	changes := make(object.Changes, 3)
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"a1eb2ea76eb7f9bfbde9b243861474421000eb96",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"994eac1cd07235bb9815e547a75c84265dea00f5",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name: testAnalyserPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testAnalyserPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("dc248ba2b22048cc730c571a748e8ffcf7085ab9"),
		},
	}, To: object.ChangeEntry{
		Name: testAnalyserPath,
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: testAnalyserPath,
			Mode: 0o100644,
			Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
		},
	}}
	changes[1] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testHerculesMainPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testHerculesMainPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"),
			},
		},
	}
	changes[2] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: testTravisPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testTravisPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe"),
			},
		},
	}
	deps[items.DependencyTreeChanges] = changes
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))

	fd := fixtures.FileDiff()
	{
		result, err := fd.Consume(deps)
		assert.NoError(t, err)
		deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]
	}

	lh := LineHistoryAnalyser()
	{
		result, err := lh.Consume(deps)
		assert.NoError(t, err)
		deps[linehistory.DependencyLineHistory] = result[linehistory.DependencyLineHistory]
	}

	{
		_, err := bd.Consume(deps)
		assert.NoError(t, err)
	}

	// stage 2
	// 2b1ed978194a94edeabbca6de7ff3b5771d4d665
	deps[identity.DependencyAuthor] = secondAuthor
	deps[items.DependencyTick] = 30
	cache = map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, "291286b4ac41952cbd1389fda66420ec03c1a9fe")
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "f7d918ec500e2f925ecde79b51cc007bac27de72")
	deps[items.DependencyBlobCache] = cache
	changes = make(object.Changes, 3)
	treeFrom, _ = test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	treeTo, _ = test.Repository.TreeObject(plumbing.NewHash(
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
		}, To: object.ChangeEntry{
			Name: testBurndownPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testBurndownPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2"),
			},
		},
	}
	changes[1] = &object.Change{
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
	changes[2] = &object.Change{
		From: object.ChangeEntry{
			Name: testTravisPath,
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: testTravisPath,
				Mode: 0o100644,
				Hash: plumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe"),
			},
		}, To: object.ChangeEntry{},
	}
	deps[items.DependencyTreeChanges] = changes

	{
		result, err := fd.Consume(deps)
		assert.NoError(t, err)
		deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]
	}

	{
		result, err := lh.Consume(deps)
		assert.NoError(t, err)
		deps[linehistory.DependencyLineHistory] = result[linehistory.DependencyLineHistory]
	}

	{
		bd.peopleResolver = core.NewIdentityResolver([]string{testPersonOneSource, testPersonTwoSource}, nil)
		_, err := bd.Consume(deps)
		assert.NoError(t, err)
	}

	out := bd.Finalize().(BurndownResult)
	return out
}

func TestBurndownSerialize(t *testing.T) {
	out := prepareBDForSerialization(t, 0, 1)
	bd := &BurndownAnalysis{}

	buffer := &bytes.Buffer{}
	assert.NoError(t, bd.Serialize(out, false, buffer))
	assert.Equal(t, `  granularity: 30
  sampling: 30
  tick_size: 86400
  "project": |-
    1145    0
     464  369
  files:
    "burndown.go": |-
      926   0
      293 250
    "cmd/hercules/main.go": |-
      207   0
      171 119
  files_ownership:
    - 0: 293
      1: 250
    - 0: 171
      1: 119
  people_sequence:
    - "one@srcd"
    - "two@srcd"
  people:
    "one@srcd": |-
      1145    0
       464    0
    "two@srcd": |-
      0     0
        0 369
  people_interaction: |-
    1145    0    0 -681
     369    0    0    0
`, buffer.String())
	buffer = &bytes.Buffer{}
	assert.NoError(t, bd.Serialize(out, true, buffer))
	msg := pb.BurndownAnalysisResults{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Equal(t, msg.GetTickSize(), int64(24*time.Hour))
	assert.Equal(t, int32(30), msg.GetGranularity())
	assert.Equal(t, int32(30), msg.GetSampling())
	assert.Equal(t, "project", msg.GetProject().GetName())
	assert.Equal(t, int32(2), msg.GetProject().GetNumberOfRows())
	assert.Equal(t, int32(2), msg.GetProject().GetNumberOfColumns())
	assert.Len(t, msg.GetProject().GetRows(), 2)
	assert.Len(t, msg.GetProject().GetRows()[0].GetColumns(), 1)
	assert.Equal(t, uint32(1145), msg.GetProject().GetRows()[0].GetColumns()[0])
	assert.Len(t, msg.GetProject().GetRows()[1].GetColumns(), 2)
	assert.Equal(t, uint32(464), msg.GetProject().GetRows()[1].GetColumns()[0])
	assert.Equal(t, uint32(369), msg.GetProject().GetRows()[1].GetColumns()[1])
	assert.Len(t, msg.GetFiles(), 2)
	assert.Equal(t, testBurndownPath, msg.GetFiles()[0].GetName())
	assert.Equal(t, testHerculesMainPath, msg.GetFiles()[1].GetName())
	assert.Len(t, msg.GetFiles()[0].GetRows(), 2)
	assert.Len(t, msg.GetFiles()[0].GetRows()[0].GetColumns(), 1)
	assert.Equal(t, uint32(926), msg.GetFiles()[0].GetRows()[0].GetColumns()[0])
	assert.Len(t, msg.GetFiles()[0].GetRows()[1].GetColumns(), 2)
	assert.Equal(t, uint32(293), msg.GetFiles()[0].GetRows()[1].GetColumns()[0])
	assert.Equal(t, uint32(250), msg.GetFiles()[0].GetRows()[1].GetColumns()[1])
	assert.Len(t, msg.GetFilesOwnership(), 2)
	assert.Equal(t, map[int32]int32{0: 293, 1: 250}, msg.GetFilesOwnership()[0].GetValue())
	assert.Equal(t, map[int32]int32{0: 171, 1: 119}, msg.GetFilesOwnership()[1].GetValue())
	assert.Len(t, msg.GetPeople(), 2)
	assert.Equal(t, testPersonOneSource, msg.GetPeople()[0].GetName())
	assert.Equal(t, testPersonTwoSource, msg.GetPeople()[1].GetName())
	assert.Len(t, msg.GetPeople()[0].GetRows(), 2)
	assert.Len(t, msg.GetPeople()[0].GetRows()[0].GetColumns(), 1)
	assert.Len(t, msg.GetPeople()[0].GetRows()[1].GetColumns(), 1)
	assert.Equal(t, uint32(1145), msg.GetPeople()[0].GetRows()[0].GetColumns()[0])
	assert.Equal(t, uint32(464), msg.GetPeople()[0].GetRows()[1].GetColumns()[0])
	assert.Len(t, msg.GetPeople()[1].GetRows(), 2)
	assert.Empty(t, msg.GetPeople()[1].GetRows()[0].GetColumns())
	assert.Len(t, msg.GetPeople()[1].GetRows()[1].GetColumns(), 2)
	assert.Equal(t, uint32(0), msg.GetPeople()[1].GetRows()[1].GetColumns()[0])
	assert.Equal(t, uint32(369), msg.GetPeople()[1].GetRows()[1].GetColumns()[1])
	assert.Equal(t, int32(2), msg.GetPeopleInteraction().GetNumberOfRows())
	assert.Equal(t, int32(4), msg.GetPeopleInteraction().GetNumberOfColumns())
	data := [...]int64{1145, -681, 369}
	assert.Equal(t, msg.GetPeopleInteraction().GetData(), data[:])
	indices := [...]int32{0, 3, 0}
	assert.Equal(t, msg.GetPeopleInteraction().GetIndices(), indices[:])
	indptr := [...]int64{0, 2, 3}
	assert.Equal(t, msg.GetPeopleInteraction().GetIndptr(), indptr[:])
}

func TestBurndownSerializeRejectsNegativeBalances(t *testing.T) {
	tests := map[string]struct {
		result BurndownResult
		scope  string
		name   string
	}{
		"global": {
			result: BurndownResult{GlobalHistory: burndown.DenseHistory{{0}, {0, 0, -3}}},
			scope:  "project",
			name:   "project",
		},
		"file": {
			result: BurndownResult{
				GlobalHistory: burndown.DenseHistory{{0}},
				FileHistories: map[string]burndown.DenseHistory{
					"src/renamed.go": {{0}, {0, 0, -3}},
				},
			},
			scope: "file",
			name:  "src/renamed.go",
		},
		"person": {
			result: BurndownResult{
				GlobalHistory:      burndown.DenseHistory{{0}},
				PeopleHistories:    []burndown.DenseHistory{{{0}, {0, 0, -3}}},
				reversedPeopleDict: []string{"alice"},
			},
			scope: "person",
			name:  "alice",
		},
		"repository": {
			result: BurndownResult{
				GlobalHistory:          burndown.DenseHistory{{0}},
				RepositoryHistories:    []burndown.DenseHistory{{{0}, {0, 0, -3}}},
				ReversedRepositoryDict: []string{"example/repository"},
			},
			scope: "repository",
			name:  "example/repository",
		},
	}

	for name, testCase := range tests {
		for _, binary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/binary=%t", name, binary), func(t *testing.T) {
				testCase.result.sampling = 5
				testCase.result.granularity = 7
				buffer := &bytes.Buffer{}

				err := (&BurndownAnalysis{}).Serialize(testCase.result, binary, buffer)
				assert.ErrorIs(t, err, errNegativeBurndownBalance)
				assert.Empty(t, buffer.Bytes())

				var balanceError *negativeBurndownBalanceError
				require.ErrorAs(t, err, &balanceError)
				assert.Equal(t, testCase.scope, balanceError.Scope)
				assert.Equal(t, testCase.name, balanceError.Name)
				assert.Equal(t, 5, balanceError.Tick)
				assert.Equal(t, 14, balanceError.AgeBand)
				assert.Equal(t, "serialization", balanceError.Operation)
				assert.Equal(t, int64(-3), balanceError.Value)
			})
		}
	}
}

func TestBurndownSerializeAuthorMissing(t *testing.T) {
	out := prepareBDForSerialization(t, 0, core.AuthorMissing)
	bd := &BurndownAnalysis{}

	buffer := &bytes.Buffer{}
	assert.NoError(t, bd.Serialize(out, false, buffer))
	assert.Equal(t, `  granularity: 30
  sampling: 30
  tick_size: 86400
  "project": |-
    1145    0
     464  369
  files:
    "burndown.go": |-
      926   0
      293 250
    "cmd/hercules/main.go": |-
      207   0
      171 119
  files_ownership:
    - 0: 293
      -1: 250
    - 0: 171
      -1: 119
  people_sequence:
    - "one@srcd"
    - "two@srcd"
  people:
    "one@srcd": |-
      1145    0
       464    0
    "two@srcd": |-
      0 0
      0 0
  people_interaction: |-
    1145 -681    0    0
       0    0    0    0
`, buffer.String())
	buffer = &bytes.Buffer{}
	assert.NoError(t, bd.Serialize(out, true, buffer))
	msg := pb.BurndownAnalysisResults{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Equal(t, int32(30), msg.GetGranularity())
	assert.Equal(t, int32(30), msg.GetSampling())
	assert.Equal(t, "project", msg.GetProject().GetName())
	assert.Equal(t, int32(2), msg.GetProject().GetNumberOfRows())
	assert.Equal(t, int32(2), msg.GetProject().GetNumberOfColumns())
	assert.Len(t, msg.GetProject().GetRows(), 2)
	assert.Len(t, msg.GetProject().GetRows()[0].GetColumns(), 1)
	assert.Equal(t, uint32(1145), msg.GetProject().GetRows()[0].GetColumns()[0])
	assert.Len(t, msg.GetProject().GetRows()[1].GetColumns(), 2)
	assert.Equal(t, uint32(464), msg.GetProject().GetRows()[1].GetColumns()[0])
	assert.Equal(t, uint32(369), msg.GetProject().GetRows()[1].GetColumns()[1])
	assert.Len(t, msg.GetFiles(), 2)
	assert.Equal(t, testBurndownPath, msg.GetFiles()[0].GetName())
	assert.Equal(t, testHerculesMainPath, msg.GetFiles()[1].GetName())
	assert.Len(t, msg.GetFiles()[0].GetRows(), 2)
	assert.Len(t, msg.GetFiles()[0].GetRows()[0].GetColumns(), 1)
	assert.Equal(t, uint32(926), msg.GetFiles()[0].GetRows()[0].GetColumns()[0])
	assert.Len(t, msg.GetFiles()[0].GetRows()[1].GetColumns(), 2)
	assert.Equal(t, uint32(293), msg.GetFiles()[0].GetRows()[1].GetColumns()[0])
	assert.Equal(t, uint32(250), msg.GetFiles()[0].GetRows()[1].GetColumns()[1])
	assert.Len(t, msg.GetFilesOwnership(), 2)
	assert.Equal(t, map[int32]int32{0: 293, -1: 250}, msg.GetFilesOwnership()[0].GetValue())
	assert.Equal(t, map[int32]int32{0: 171, -1: 119}, msg.GetFilesOwnership()[1].GetValue())
	assert.Len(t, msg.GetPeople(), 2)
	assert.Equal(t, testPersonOneSource, msg.GetPeople()[0].GetName())
	assert.Equal(t, testPersonTwoSource, msg.GetPeople()[1].GetName())
	assert.Len(t, msg.GetPeople()[0].GetRows(), 2)
	assert.Len(t, msg.GetPeople()[0].GetRows()[0].GetColumns(), 1)
	assert.Len(t, msg.GetPeople()[0].GetRows()[1].GetColumns(), 1)
	assert.Equal(t, uint32(1145), msg.GetPeople()[0].GetRows()[0].GetColumns()[0])
	assert.Equal(t, uint32(464), msg.GetPeople()[0].GetRows()[1].GetColumns()[0])
	assert.Len(t, msg.GetPeople()[1].GetRows(), 2)
	assert.Empty(t, msg.GetPeople()[1].GetRows()[0].GetColumns())
	assert.Empty(t, msg.GetPeople()[1].GetRows()[1].GetColumns())
	assert.Equal(t, int32(2), msg.GetPeopleInteraction().GetNumberOfRows())
	assert.Equal(t, int32(4), msg.GetPeopleInteraction().GetNumberOfColumns())
	data := [...]int64{1145, -681}
	assert.Equal(t, msg.GetPeopleInteraction().GetData(), data[:])
	indices := [...]int32{0, 1}
	assert.Equal(t, msg.GetPeopleInteraction().GetIndices(), indices[:])
	indptr := [...]int64{0, 2, 2}
	assert.Equal(t, msg.GetPeopleInteraction().GetIndptr(), indptr[:])
}

func TestBurndownMergeGlobalHistory(t *testing.T) {
	people1 := [...]string{testPersonOne, testPersonTwo}
	res1 := BurndownResult{
		GlobalHistory:      [][]int64{},
		FileHistories:      map[string]burndown.DenseHistory{},
		PeopleHistories:    []burndown.DenseHistory{},
		PeopleMatrix:       [][]int64{},
		reversedPeopleDict: people1[:],
		sampling:           15,
		granularity:        20,
		tickSize:           24 * time.Hour,
	}
	c1 := core.CommonAnalysisResult{
		BeginTime:     600566400, // 1989 Jan 12
		EndTime:       604713600, // 1989 March 1
		CommitsNumber: 10,
		RunTime:       100000,
	}
	// 48 days
	res1.GlobalHistory = make([][]int64, 48/15+1 /* 4 samples */)
	for i := range res1.GlobalHistory {
		res1.GlobalHistory[i] = make([]int64, 48/20+1 /* 3 bands */)
		switch i {
		case 0:
			res1.GlobalHistory[i][0] = 1000
		case 1:
			res1.GlobalHistory[i][0] = 1100
			res1.GlobalHistory[i][1] = 400
		case 2:
			res1.GlobalHistory[i][0] = 900
			res1.GlobalHistory[i][1] = 750
			res1.GlobalHistory[i][2] = 100
		case 3:
			res1.GlobalHistory[i][0] = 850
			res1.GlobalHistory[i][1] = 700
			res1.GlobalHistory[i][2] = 150
		}
	}
	res1.PeopleHistories = append(res1.PeopleHistories, res1.GlobalHistory)
	res1.PeopleHistories = append(res1.PeopleHistories, res1.GlobalHistory)
	res1.PeopleMatrix = append(res1.PeopleMatrix, make([]int64, 4))
	res1.PeopleMatrix = append(res1.PeopleMatrix, make([]int64, 4))
	res1.PeopleMatrix[0][0] = 10
	res1.PeopleMatrix[0][1] = 20
	res1.PeopleMatrix[0][2] = 30
	res1.PeopleMatrix[0][3] = 40
	res1.PeopleMatrix[1][0] = 50
	res1.PeopleMatrix[1][1] = 60
	res1.PeopleMatrix[1][2] = 70
	res1.PeopleMatrix[1][3] = 80
	people2 := [...]string{testPersonTwo, testPersonThree}
	res2 := BurndownResult{
		GlobalHistory:      nil,
		FileHistories:      map[string]burndown.DenseHistory{},
		PeopleHistories:    nil,
		PeopleMatrix:       nil,
		tickSize:           24 * time.Hour,
		reversedPeopleDict: people2[:],
		sampling:           14,
		granularity:        19,
	}
	c2 := core.CommonAnalysisResult{
		BeginTime:     601084800, // 1989 Jan 18
		EndTime:       605923200, // 1989 March 15
		CommitsNumber: 10,
		RunTime:       100000,
	}
	// 56 days
	res2.GlobalHistory = make([][]int64, 56/14 /* 4 samples */)
	for i := range res2.GlobalHistory {
		res2.GlobalHistory[i] = make([]int64, 56/19+1 /* 3 bands */)
		switch i {
		case 0:
			res2.GlobalHistory[i][0] = 900
		case 1:
			res2.GlobalHistory[i][0] = 1100
			res2.GlobalHistory[i][1] = 400
		case 2:
			res2.GlobalHistory[i][0] = 900
			res2.GlobalHistory[i][1] = 750
			res2.GlobalHistory[i][2] = 100
		case 3:
			res2.GlobalHistory[i][0] = 800
			res2.GlobalHistory[i][1] = 600
			res2.GlobalHistory[i][2] = 600
		}
	}
	res2.PeopleHistories = append(res2.PeopleHistories, res2.GlobalHistory)
	res2.PeopleHistories = append(res2.PeopleHistories, res2.GlobalHistory)
	res2.PeopleMatrix = append(res2.PeopleMatrix, make([]int64, 4))
	res2.PeopleMatrix = append(res2.PeopleMatrix, make([]int64, 4))
	res2.PeopleMatrix[0][0] = 100
	res2.PeopleMatrix[0][1] = 200
	res2.PeopleMatrix[0][2] = 300
	res2.PeopleMatrix[0][3] = 400
	res2.PeopleMatrix[1][0] = 500
	res2.PeopleMatrix[1][1] = 600
	res2.PeopleMatrix[1][2] = 700
	res2.PeopleMatrix[1][3] = 800
	bd := BurndownAnalysis{
		tickSize: 24 * time.Hour,
	}
	merged := bd.MergeResults(res1, res2, &c1, &c2).(BurndownResult)
	assert.Equal(t, 19, merged.granularity)
	assert.Equal(t, 14, merged.sampling)
	assert.Equal(t, 24*time.Hour, merged.tickSize)
	assert.Len(t, merged.GlobalHistory, 5)
	for _, row := range merged.GlobalHistory {
		assert.Len(t, row, 4)
	}
	assert.Nil(t, merged.FileHistories)
	assert.Len(t, merged.reversedPeopleDict, 3)
	assert.NotEqual(t, merged.PeopleHistories[0], res1.GlobalHistory)
	assert.Equal(t, merged.PeopleHistories[1], merged.GlobalHistory)
	assert.NotEqual(t, merged.PeopleHistories[2], res2.GlobalHistory)
	assert.Len(t, merged.PeopleMatrix, 3)
	for _, row := range merged.PeopleMatrix {
		assert.Len(t, row, 5)
	}
	assert.Equal(t, int64(10), merged.PeopleMatrix[0][0])
	assert.Equal(t, int64(20), merged.PeopleMatrix[0][1])
	assert.Equal(t, int64(30), merged.PeopleMatrix[0][2])
	assert.Equal(t, int64(40), merged.PeopleMatrix[0][3])
	assert.Equal(t, int64(0), merged.PeopleMatrix[0][4])

	assert.Equal(t, int64(150), merged.PeopleMatrix[1][0])
	assert.Equal(t, int64(260), merged.PeopleMatrix[1][1])
	assert.Equal(t, int64(70), merged.PeopleMatrix[1][2])
	assert.Equal(t, int64(380), merged.PeopleMatrix[1][3])
	assert.Equal(t, int64(400), merged.PeopleMatrix[1][4])

	assert.Equal(t, int64(500), merged.PeopleMatrix[2][0])
	assert.Equal(t, int64(600), merged.PeopleMatrix[2][1])
	assert.Equal(t, int64(0), merged.PeopleMatrix[2][2])
	assert.Equal(t, int64(700), merged.PeopleMatrix[2][3])
	assert.Equal(t, int64(800), merged.PeopleMatrix[2][4])
	assert.NoError(t, bd.serializeBinary(&merged, io.Discard))
}

func TestBurndownMergeGlobalHistory_withDifferentTickSizes(t *testing.T) {
	res1 := BurndownResult{
		tickSize: 13 * time.Hour,
	}
	c1 := core.CommonAnalysisResult{
		BeginTime:     600566400, // 1989 Jan 12
		EndTime:       604713600, // 1989 March 1
		CommitsNumber: 10,
		RunTime:       100000,
	}
	res2 := BurndownResult{
		tickSize: 24 * time.Hour,
	}
	c2 := core.CommonAnalysisResult{
		BeginTime:     601084800, // 1989 Jan 18
		EndTime:       605923200, // 1989 March 15
		CommitsNumber: 10,
		RunTime:       100000,
	}
	bd := BurndownAnalysis{
		tickSize: 24 * time.Hour,
	}
	merged := bd.MergeResults(res1, res2, &c1, &c2)
	mergedErr, ok := merged.(error)
	assert.True(t, ok)
	assert.ErrorIs(t, mergedErr, errBurndownMismatchingTickSizes)
	assert.Contains(t, mergedErr.Error(), "mismatching tick sizes")
}

func TestBurndownLifecycleTransitionsRemainNonNegative(t *testing.T) {
	treeFrom, err := test.Repository.TreeObject(plumbing.NewHash(
		"a1eb2ea76eb7f9bfbde9b243861474421000eb96",
	))
	require.NoError(t, err)
	treeTo, err := test.Repository.TreeObject(plumbing.NewHash(
		"994eac1cd07235bb9815e547a75c84265dea00f5",
	))
	require.NoError(t, err)

	entry := func(name, hash string, tree *object.Tree) object.ChangeEntry {
		return object.ChangeEntry{
			Name: name,
			Tree: tree,
			TreeEntry: object.TreeEntry{
				Name: name,
				Mode: 0o100644,
				Hash: plumbing.NewHash(hash),
			},
		}
	}
	insert := func(name, hash string) *object.Change {
		return &object.Change{To: entry(name, hash, treeTo)}
	}
	remove := func(name, hash string) *object.Change {
		return &object.Change{From: entry(name, hash, treeFrom)}
	}
	modify := func(fromName, fromHash, toName, toHash string) *object.Change {
		return &object.Change{
			From: entry(fromName, fromHash, treeFrom),
			To:   entry(toName, toHash, treeTo),
		}
	}

	type lifecycleStep struct {
		tick    int
		author  int
		changes object.Changes
	}
	tests := []struct {
		name              string
		steps             []lifecycleStep
		survivingPath     string
		expectedFinalSize int64
	}{
		{
			name: "rename chain ending in deletion",
			steps: []lifecycleStep{
				{tick: 0, author: 0, changes: object.Changes{
					insert(lifecycleBaselinePath, lifecycleBaselineHash),
					insert("original.txt", lifecycleTextHash),
				}},
				{tick: 30, author: 1, changes: object.Changes{
					modify("original.txt", lifecycleTextHash, "middle.txt", lifecycleTextHash),
				}},
				{tick: 60, author: 1, changes: object.Changes{
					modify("middle.txt", lifecycleTextHash, "final.txt", lifecycleTextHash),
				}},
				{tick: 90, author: 1, changes: object.Changes{
					remove("final.txt", lifecycleTextHash),
				}},
			},
			expectedFinalSize: 12,
		},
		{
			name: "filter boundary delete and reinsert",
			steps: []lifecycleStep{
				{tick: 0, author: 0, changes: object.Changes{
					insert(lifecycleBaselinePath, lifecycleBaselineHash),
					insert("boundary.txt", lifecycleTextHash),
				}},
				// TreeDiff represents included -> excluded as a deletion and
				// excluded -> included as an insertion. The TreeDiff transition
				// table tests filters; downstream they share this lifecycle invariant.
				{tick: 30, author: 1, changes: object.Changes{
					remove("boundary.txt", lifecycleTextHash),
				}},
				{tick: 60, author: 1, changes: object.Changes{
					insert("boundary.txt", lifecycleTextHash),
				}},
			},
			survivingPath:     "boundary.txt",
			expectedFinalSize: 219,
		},
		{
			name: "text to binary and back to text",
			steps: []lifecycleStep{
				{tick: 0, author: 0, changes: object.Changes{
					insert(lifecycleBaselinePath, lifecycleBaselineHash),
					insert("format.txt", lifecycleTextHash),
				}},
				{tick: 30, author: 1, changes: object.Changes{
					modify("format.txt", lifecycleTextHash, "format.txt", lifecycleBinaryHash),
				}},
				{tick: 60, author: 1, changes: object.Changes{
					modify("format.txt", lifecycleBinaryHash, "format.txt", lifecycleTextHash2),
				}},
			},
			survivingPath:     "format.txt",
			expectedFinalSize: 302,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fd := fixtures.FileDiff()
			lh := LineHistoryAnalyser()
			bd := BurndownAnalysis{
				TrackFiles:     true,
				peopleResolver: core.NewIdentityResolver([]string{"P1", "P2"}, nil),
				repositoryName: tc.name,
			}
			require.NoError(t, bd.Initialize(test.Repository))

			for _, step := range tc.steps {
				cache := map[plumbing.Hash]*items.CachedBlob{}
				for _, change := range step.changes {
					for _, hash := range []plumbing.Hash{
						change.From.TreeEntry.Hash,
						change.To.TreeEntry.Hash,
					} {
						if hash != plumbing.ZeroHash {
							AddHash(t, cache, hash.String())
						}
					}
				}

				deps := map[string]any{
					identity.DependencyAuthor:   step.author,
					items.DependencyTick:        step.tick,
					items.DependencyBlobCache:   cache,
					items.DependencyTreeChanges: step.changes,
				}
				diffResult, err := fd.Consume(deps)
				require.NoError(t, err)
				deps[items.DependencyFileDiff] = diffResult[items.DependencyFileDiff]

				lineResult, err := lh.Consume(deps)
				require.NoError(t, err)
				deps[linehistory.DependencyLineHistory] = lineResult[linehistory.DependencyLineHistory]

				_, err = bd.Consume(deps)
				require.NoError(t, err)
			}

			finalized := bd.Finalize()
			result, ok := finalized.(BurndownResult)
			require.Truef(t, ok, "finalization returned %T: %v", finalized, finalized)
			require.NoError(t, validateBurndownResultBalances(
				&result, "lifecycle transition test",
			))

			require.NotEmpty(t, result.GlobalHistory)
			require.Contains(t, result.FileHistories, lifecycleBaselinePath)
			require.Len(t, result.PeopleHistories, 2)
			require.Len(t, result.RepositoryHistories, 1)

			assertBurndownHistoryNonNegative(t, "global history", result.GlobalHistory)
			for name, history := range result.FileHistories {
				assertBurndownHistoryNonNegative(t, "file history "+name, history)
			}
			for index, history := range result.PeopleHistories {
				assertBurndownHistoryNonNegative(
					t, fmt.Sprintf("person history %d", index), history,
				)
			}
			for index, history := range result.RepositoryHistories {
				assertBurndownHistoryNonNegative(
					t, fmt.Sprintf("repository history %d", index), history,
				)
			}

			lastRow := result.GlobalHistory[len(result.GlobalHistory)-1]
			var finalSize int64
			for _, value := range lastRow {
				finalSize += value
			}
			assert.Equal(t, tc.expectedFinalSize, finalSize)
			if tc.survivingPath == "" {
				assert.NotContains(t, result.FileHistories, "final.txt")
			} else {
				assert.Contains(t, result.FileHistories, tc.survivingPath)
			}
		})
	}
}

func TestBurndownMergeRejectsNegativeInput(t *testing.T) {
	first := BurndownResult{
		GlobalHistory: burndown.DenseHistory{{1}, {-2}},
		tickSize:      24 * time.Hour,
		sampling:      2,
		granularity:   3,
	}
	second := BurndownResult{
		GlobalHistory: burndown.DenseHistory{{1}},
		tickSize:      24 * time.Hour,
		sampling:      2,
		granularity:   3,
	}
	common := &core.CommonAnalysisResult{BeginTime: 0, EndTime: 2 * 86400}

	merged := (&BurndownAnalysis{}).MergeResults(first, second, common, common)
	err, ok := merged.(error)
	require.True(t, ok)
	assert.ErrorIs(t, err, errNegativeBurndownBalance)
	assert.ErrorContains(t, err, "tick 2")
	assert.ErrorContains(t, err, "age band 0")
	assert.ErrorContains(t, err, "merge input 1")
}

func TestBurndownMergeNils(t *testing.T) {
	res1 := BurndownResult{
		GlobalHistory:      nil,
		FileHistories:      map[string]burndown.DenseHistory{},
		PeopleHistories:    nil,
		PeopleMatrix:       nil,
		tickSize:           24 * time.Hour,
		reversedPeopleDict: nil,
		sampling:           15,
		granularity:        20,
	}
	c1 := core.CommonAnalysisResult{
		BeginTime:     600566400, // 1989 Jan 12
		EndTime:       604713600, // 1989 March 1
		CommitsNumber: 10,
		RunTime:       100000,
	}
	res2 := BurndownResult{
		GlobalHistory:      nil,
		FileHistories:      nil,
		PeopleHistories:    nil,
		PeopleMatrix:       nil,
		tickSize:           24 * time.Hour,
		reversedPeopleDict: nil,
		sampling:           14,
		granularity:        19,
	}
	c2 := core.CommonAnalysisResult{
		BeginTime:     601084800, // 1989 Jan 18
		EndTime:       605923200, // 1989 March 15
		CommitsNumber: 10,
		RunTime:       100000,
	}
	bd := BurndownAnalysis{
		tickSize: 24 * time.Hour,
	}
	merged := bd.MergeResults(res1, res2, &c1, &c2).(BurndownResult)
	assert.Equal(t, 19, merged.granularity)
	assert.Equal(t, 14, merged.sampling)
	assert.Equal(t, 24*time.Hour, merged.tickSize)
	assert.Nil(t, merged.GlobalHistory)
	assert.Nil(t, merged.FileHistories)
	assert.Nil(t, merged.PeopleHistories)
	assert.Nil(t, merged.PeopleMatrix)
	assert.NoError(t, bd.serializeBinary(&merged, io.Discard))

	res2.GlobalHistory = [][]int64{
		{900, 0, 0},
		{1100, 400, 0},
		{900, 750, 100},
		{800, 600, 600},
	}
	res2.FileHistories = map[string]burndown.DenseHistory{"test": res2.GlobalHistory}
	people1 := [...]string{testPersonOne, testPersonTwo}
	res1.reversedPeopleDict = people1[:]
	res1.PeopleMatrix = append(res1.PeopleMatrix, make([]int64, 4))
	res1.PeopleMatrix = append(res1.PeopleMatrix, make([]int64, 4))
	res1.PeopleMatrix[0][0] = 10
	res1.PeopleMatrix[0][1] = 20
	res1.PeopleMatrix[0][2] = 30
	res1.PeopleMatrix[0][3] = 40
	res1.PeopleMatrix[1][0] = 50
	res1.PeopleMatrix[1][1] = 60
	res1.PeopleMatrix[1][2] = 70
	res1.PeopleMatrix[1][3] = 80
	people2 := [...]string{testPersonTwo, testPersonThree}
	res2.reversedPeopleDict = people2[:]
	merged = bd.MergeResults(res1, res2, &c1, &c2).(BurndownResult)
	// calculated in a spreadsheet
	mgh := burndown.DenseHistory{
		{514, 0, 0, 0},
		{808, 506, 0, 0},
		{674, 889, 177, 0},
		{576, 720, 595, 0},
		{547, 663, 610, 178},
	}
	assert.Equal(t, mgh, merged.GlobalHistory)
	assert.Nil(t, merged.FileHistories)
	assert.Nil(t, merged.PeopleHistories)
	assert.Len(t, merged.PeopleMatrix, 3)
	for _, row := range merged.PeopleMatrix {
		assert.Len(t, row, 5)
	}
	assert.Equal(t, int64(10), merged.PeopleMatrix[0][0])
	assert.Equal(t, int64(20), merged.PeopleMatrix[0][1])
	assert.Equal(t, int64(30), merged.PeopleMatrix[0][2])
	assert.Equal(t, int64(40), merged.PeopleMatrix[0][3])
	assert.Equal(t, int64(0), merged.PeopleMatrix[0][4])

	assert.Equal(t, int64(50), merged.PeopleMatrix[1][0])
	assert.Equal(t, int64(60), merged.PeopleMatrix[1][1])
	assert.Equal(t, int64(70), merged.PeopleMatrix[1][2])
	assert.Equal(t, int64(80), merged.PeopleMatrix[1][3])
	assert.Equal(t, int64(0), merged.PeopleMatrix[1][4])

	assert.Equal(t, int64(0), merged.PeopleMatrix[2][0])
	assert.Equal(t, int64(0), merged.PeopleMatrix[2][1])
	assert.Equal(t, int64(0), merged.PeopleMatrix[2][2])
	assert.Equal(t, int64(0), merged.PeopleMatrix[2][3])
	assert.Equal(t, int64(0), merged.PeopleMatrix[2][4])
	assert.NoError(t, bd.serializeBinary(&merged, io.Discard))
}

func TestBurndownDeserialize(t *testing.T) {
	allBuffer, err := os.ReadFile(path.Join("..", "internal", "test_data", "burndown.pb"))
	assert.NoError(t, err)
	bd := BurndownAnalysis{}
	iresult, err := bd.Deserialize(allBuffer)
	assert.NoError(t, err)
	result := iresult.(BurndownResult)
	assert.NotEmpty(t, result.GlobalHistory)
	assert.NotEmpty(t, result.FileHistories)
	assert.Len(t, result.FileHistories, len(result.FileOwnership))
	assert.NotEmpty(t, result.reversedPeopleDict)
	assert.NotEmpty(t, result.PeopleHistories)
	assert.NotEmpty(t, result.PeopleMatrix)
	assert.Equal(t, 30, result.granularity)
	assert.Equal(t, 30, result.sampling)
	assert.Equal(t, 24*time.Hour, result.tickSize)
}

func TestBurndownEmptyFileHistory(t *testing.T) {
	bd := &BurndownAnalysis{
		Sampling:       30,
		Granularity:    30,
		globalHistory:  sparseHistory{0: sparseHistoryEntry{deltas: map[int]int64{0: 10}}},
		fileHistories:  map[core.FileId]sparseHistory{1: {}},
		peopleResolver: core.NewIdentityResolver(nil, nil),
	}
	res := bd.Finalize().(BurndownResult)
	assert.Len(t, res.GlobalHistory, 1)
	assert.Empty(t, res.FileHistories)
	assert.NotNil(t, res.FileHistories)
	assert.Empty(t, res.PeopleHistories)
	assert.NotNil(t, res.PeopleHistories)
}

func TestBurndownMergePeopleHistories(t *testing.T) {
	h1 := [][]int64{
		{50, 0, 0},
		{40, 80, 0},
		{30, 50, 70},
	}
	h2 := [][]int64{
		{900, 0, 0},
		{1100, 400, 0},
		{900, 750, 100},
		{800, 600, 600},
	}
	res1 := BurndownResult{
		GlobalHistory:      h1,
		FileHistories:      map[string]burndown.DenseHistory{},
		PeopleHistories:    []burndown.DenseHistory{h1, h1},
		PeopleMatrix:       nil,
		tickSize:           24 * time.Hour,
		reversedPeopleDict: []string{testPersonOne, testPersonThree},
		sampling:           15, // 3
		granularity:        20, // 3
	}
	c1 := core.CommonAnalysisResult{
		BeginTime:     600566400, // 1989 Jan 12
		EndTime:       604540800, // 1989 February 27
		CommitsNumber: 10,
		RunTime:       100000,
	}
	res2 := BurndownResult{
		GlobalHistory:      h2,
		FileHistories:      nil,
		PeopleHistories:    []burndown.DenseHistory{h2, h2},
		PeopleMatrix:       nil,
		tickSize:           24 * time.Hour,
		reversedPeopleDict: []string{testPersonOne, testPersonTwo},
		sampling:           14,
		granularity:        19,
	}
	c2 := core.CommonAnalysisResult{
		BeginTime:     601084800, // 1989 Jan 18
		EndTime:       605923200, // 1989 March 15
		CommitsNumber: 10,
		RunTime:       100000,
	}
	bd := BurndownAnalysis{
		tickSize: 24 * time.Hour,
	}
	merged := bd.MergeResults(res1, res2, &c1, &c2).(BurndownResult)
	mh := burndown.DenseHistory{
		{560, 0, 0, 0},
		{851, 572, 0, 0},
		{704, 995, 217, 0},
		{605, 767, 670, 0},
		{575, 709, 685, 178},
	}
	assert.Equal(t, []string{testPersonOne, testPersonThree, testPersonTwo}, merged.reversedPeopleDict)
	assert.Equal(t, mh, merged.PeopleHistories[0])
	mh = burndown.DenseHistory{
		{46, 0, 0, 0},
		{43, 66, 0, 0},
		{30, 106, 39, 0},
		{28, 46, 75, 0},
		{28, 46, 75, 0},
	}
	assert.Equal(t, mh, merged.PeopleHistories[1])
	mh = burndown.DenseHistory{
		{514, 0, 0, 0},
		{808, 506, 0, 0},
		{674, 889, 177, 0},
		{576, 720, 595, 0},
		{547, 663, 610, 178},
	}
	assert.Equal(t, mh, merged.PeopleHistories[2])
	assert.Nil(t, merged.PeopleMatrix)
	assert.NoError(t, bd.serializeBinary(&merged, io.Discard))
}

func TestBurndownResultGetters(t *testing.T) {
	br := BurndownResult{tickSize: time.Hour, reversedPeopleDict: []string{testPersonOne, testPersonTwo}}
	assert.Equal(t, br.tickSize, br.GetTickSize())
	assert.Equal(t, br.GetIdentities(), br.reversedPeopleDict)
}

func TestBurndownHibernateBoot(t *testing.T) {
	bd := BurndownAnalysis{}
	assert.NoError(t, bd.Initialize(test.Repository))

	// Populate with some data
	bd.globalHistory.updateDelta(0, 0, 50)
	bd.globalHistory.updateDelta(0, 10, -10)
	bd.globalHistory.updateDelta(10, 10, 20)

	bd.fileHistories[1] = sparseHistory{}
	bd.fileHistories[1].updateDelta(0, 0, 30)

	// Hibernate
	assert.NoError(t, bd.Hibernate())
	// Maps should be nil after hibernation
	assert.Nil(t, bd.globalHistory)
	assert.Nil(t, bd.fileHistories)
	assert.Nil(t, bd.peopleHistories)
	assert.Nil(t, bd.matrix)

	// Boot
	assert.NoError(t, bd.Boot())
	// Data should be restored
	assert.Equal(t, int64(50), bd.globalHistory[0].deltas[0])
	assert.Equal(t, int64(-10), bd.globalHistory[10].deltas[0])
	assert.Equal(t, int64(20), bd.globalHistory[10].deltas[10])
	assert.Equal(t, int64(30), bd.fileHistories[1][0].deltas[0])
}

func TestBurndownHibernateBootDisk(t *testing.T) {
	bd := BurndownAnalysis{}
	assert.NoError(t, bd.Initialize(test.Repository))
	bd.HibernationToDisk = true

	bd.globalHistory.updateDelta(0, 5, 100)

	assert.NoError(t, bd.Hibernate())
	assert.NotEmpty(t, bd.hibernatedFileName)
	assert.Nil(t, bd.globalHistory)

	assert.NoError(t, bd.Boot())
	assert.Empty(t, bd.hibernatedFileName)
	assert.Equal(t, int64(100), bd.globalHistory[5].deltas[0])
}
