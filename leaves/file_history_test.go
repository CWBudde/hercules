package leaves

import (
	"bytes"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/internal/test/fixtures"
)

func fixtureFileHistory() *FileHistoryAnalysis {
	fh := FileHistoryAnalysis{}
	err := fh.Initialize(test.Repository)
	if err != nil {
		panic(err)
	}
	return &fh
}

func TestFileHistoryMeta(t *testing.T) {
	fh := fixtureFileHistory()
	assert.Equal(t, "FileHistoryAnalysis", fh.Name())
	assert.Empty(t, fh.Provides())
	assert.Len(t, fh.Requires(), 3)
	assert.Equal(t, items.DependencyTreeChanges, fh.Requires()[0])
	assert.Equal(t, items.DependencyLineStats, fh.Requires()[1])
	assert.Equal(t, identity.DependencyAuthor, fh.Requires()[2])
	assert.Empty(t, fh.ListConfigurationOptions())
	assert.NoError(t, fh.Configure(nil))
	logger := core.NewLogger()
	assert.NoError(t, fh.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, fh.l)
}

func TestFileHistoryRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&FileHistoryAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "FileHistoryAnalysis", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&FileHistoryAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestFileHistoryEmptyAndReinitializedLifecycle(t *testing.T) {
	fh := fixtureFileHistory()
	fh.lastCommit = &object.Commit{}
	fh.files["stale.go"] = &FileHistory{
		Hashes: []plumbing.Hash{plumbing.NewHash("1111111111111111111111111111111111111111")},
	}

	assert.NoError(t, fh.Initialize(test.Repository))
	assert.Nil(t, fh.lastCommit)
	assert.Empty(t, fh.files)
	assert.Equal(t, FileHistoryResult{Files: map[string]FileHistory{}}, fh.Finalize())
}

func TestFileHistoryConsume(t *testing.T) {
	fh := bakeFileHistoryForSerialization(t)
	validate := func() {
		assert.Len(t, fh.files, 3)
		assert.Equal(t, map[int]items.LineStats{1: ls(0, 207, 0)}, fh.files[testHerculesMainPath].People)
		assert.Equal(t, map[int]items.LineStats{1: ls(12, 0, 0)}, fh.files[testTravisPath].People)
		assert.Equal(t, map[int]items.LineStats{1: ls(628, 9, 67)}, fh.files[testAnalyserPath].People)
		assert.Len(t, fh.files[testAnalyserPath].Hashes, 2)
		assert.Equal(t, fh.files[testAnalyserPath].Hashes[0], plumbing.NewHash(
			"ffffffffffffffffffffffffffffffffffffffff",
		))
		assert.Equal(t, fh.files[testAnalyserPath].Hashes[1], plumbing.NewHash(
			"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
		))
		assert.Len(t, fh.files[testTravisPath].Hashes, 1)
		assert.Equal(t, fh.files[testTravisPath].Hashes[0], plumbing.NewHash(
			"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
		))
		assert.Len(t, fh.files[testHerculesMainPath].Hashes, 2)
		assert.Equal(t, fh.files[testHerculesMainPath].Hashes[0], plumbing.NewHash(
			"0000000000000000000000000000000000000000",
		))
		assert.Equal(t, fh.files[testHerculesMainPath].Hashes[1], plumbing.NewHash(
			"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
		))
	}
	validate()
	res := fh.Finalize().(FileHistoryResult)
	assert.Len(t, res.Files, 2)
	for key, val := range res.Files {
		assert.Equal(t, val, *fh.files[key])
	}
}

func TestFileHistoryRenameChainPreservesCompleteHistory(t *testing.T) {
	fh := fixtureFileHistory()
	firstHash := plumbing.NewHash("1111111111111111111111111111111111111111")
	secondHash := plumbing.NewHash("2222222222222222222222222222222222222222")
	thirdHash := plumbing.NewHash("3333333333333333333333333333333333333333")
	original := &FileHistory{
		Hashes: []plumbing.Hash{firstHash},
		People: map[int]items.LineStats{
			1: ls(10, 3, 2),
			2: ls(4, 1, 1),
		},
	}
	fh.files["old.go"] = original

	consumeFileHistoryRename(t, fh, "old.go", "middle.go", secondHash, 1, ls(5, 2, 3))

	assert.NotContains(t, fh.files, "old.go")
	assert.Same(t, original, fh.files["middle.go"])
	assert.Equal(t, []plumbing.Hash{firstHash, secondHash}, fh.files["middle.go"].Hashes)
	assert.Equal(t, map[int]items.LineStats{
		1: ls(15, 5, 5),
		2: ls(4, 1, 1),
	}, fh.files["middle.go"].People)

	consumeFileHistoryRename(t, fh, "middle.go", "new.go", thirdHash, 2, ls(6, 4, 2))

	assert.NotContains(t, fh.files, "middle.go")
	assert.Same(t, original, fh.files["new.go"])
	assert.Equal(t, []plumbing.Hash{firstHash, secondHash, thirdHash}, fh.files["new.go"].Hashes)
	assert.Equal(t, map[int]items.LineStats{
		1: ls(15, 5, 5),
		2: ls(10, 5, 3),
	}, fh.files["new.go"].People)
}

func TestFileHistoryRenameCollisionSurvivesMergeAndSerialization(t *testing.T) {
	fh := fixtureFileHistory()
	destinationHash := plumbing.NewHash("1111111111111111111111111111111111111111")
	sourceHash := plumbing.NewHash("2222222222222222222222222222222222222222")
	renameHash := plumbing.NewHash("3333333333333333333333333333333333333333")
	destination := &FileHistory{
		Hashes: []plumbing.Hash{destinationHash},
		People: map[int]items.LineStats{
			1: ls(1, 2, 3),
		},
	}
	fh.files["source.go"] = &FileHistory{
		Hashes: []plumbing.Hash{sourceHash},
		People: map[int]items.LineStats{
			1: ls(4, 5, 6),
			2: ls(7, 8, 9),
		},
	}
	fh.files["destination.go"] = destination

	forks := fh.Fork(1)
	fork := forks[0].(*FileHistoryAnalysis)
	consumeFileHistoryRename(t, fork, "source.go", "destination.go", renameHash, 2, ls(10, 11, 12))
	fh.Merge(forks)

	assert.NotContains(t, fh.files, "source.go")
	assert.Same(t, destination, fh.files["destination.go"])
	assert.Equal(
		t,
		[]plumbing.Hash{destinationHash, sourceHash, renameHash},
		fh.files["destination.go"].Hashes,
	)
	assert.Equal(t, map[int]items.LineStats{
		1: ls(5, 7, 9),
		2: ls(17, 19, 21),
	}, fh.files["destination.go"].People)

	result := FileHistoryResult{Files: map[string]FileHistory{
		"destination.go": *fh.files["destination.go"],
	}}
	text := &bytes.Buffer{}
	assert.NoError(t, fh.Serialize(result, false, text))
	assert.Equal(
		t,
		"  - destination.go:\n"+
			"    commits: [\""+destinationHash.String()+"\",\""+sourceHash.String()+"\",\""+
			renameHash.String()+"\"]\n"+
			"    people: {1:[5,7,9],2:[17,19,21]}\n",
		text.String(),
	)

	binary := &bytes.Buffer{}
	assert.NoError(t, fh.Serialize(result, true, binary))
	message := pb.FileHistoryResultMessage{}
	assert.NoError(t, proto.Unmarshal(binary.Bytes(), &message))
	assert.Equal(
		t,
		[]string{
			destinationHash.String(),
			sourceHash.String(),
			renameHash.String(),
		},
		message.GetFiles()["destination.go"].GetCommits(),
	)
	assert.Equal(
		t,
		map[int32]*pb.LineStats{
			1: {Added: 5, Removed: 7, Changed: 9},
			2: {Added: 17, Removed: 19, Changed: 21},
		},
		message.GetFiles()["destination.go"].GetChangesByDeveloper(),
	)
}

func TestFileHistoryRenameCollisionUnionsOverlappingCommitsInStableOrder(t *testing.T) {
	fh := fixtureFileHistory()
	sharedHash := plumbing.NewHash("1111111111111111111111111111111111111111")
	destinationHash := plumbing.NewHash("2222222222222222222222222222222222222222")
	sourceHash := plumbing.NewHash("3333333333333333333333333333333333333333")
	renameHash := plumbing.NewHash("4444444444444444444444444444444444444444")
	fh.files["destination.go"] = &FileHistory{
		Hashes: []plumbing.Hash{sharedHash, destinationHash},
	}
	fh.files["source.go"] = &FileHistory{
		Hashes: []plumbing.Hash{sharedHash, sourceHash},
	}

	consumeFileHistoryRename(
		t, fh, "source.go", "destination.go", renameHash, 1, items.LineStats{},
	)
	// Replaying another tree-change entry for the same commit must not add the
	// commit twice after the two path histories have collided.
	fh.recordFileCommit(
		&object.Change{From: object.ChangeEntry{Name: "destination.go"}},
		merkletrie.Delete,
		renameHash,
	)

	assert.Equal(t, []plumbing.Hash{
		sharedHash,
		destinationHash,
		sourceHash,
		renameHash,
	}, fh.files["destination.go"].Hashes)
}

func consumeFileHistoryRename(
	t *testing.T,
	fh *FileHistoryAnalysis,
	source string,
	destination string,
	commit plumbing.Hash,
	author int,
	stats items.LineStats,
) {
	t.Helper()
	change := &object.Change{
		From: object.ChangeEntry{Name: source},
		To:   object.ChangeEntry{Name: destination},
	}
	result, err := fh.Consume(map[string]any{
		core.DependencyCommit:       &object.Commit{Hash: commit},
		items.DependencyTreeChanges: object.Changes{change},
		items.DependencyLineStats: map[object.ChangeEntry]items.LineStats{
			change.To: stats,
		},
		identity.DependencyAuthor: author,
	})
	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestFileHistoryFork(t *testing.T) {
	fh1 := fixtureFileHistory()
	clones := fh1.Fork(1)
	assert.Len(t, clones, 1)
	fh2 := clones[0].(*FileHistoryAnalysis)
	assert.Same(t, fh1, fh2)
	fh1.Merge([]core.PipelineItem{fh2})
}

func TestFileHistorySerializeText(t *testing.T) {
	fh := bakeFileHistoryForSerialization(t)
	res := fh.Finalize().(FileHistoryResult)
	buffer := &bytes.Buffer{}
	assert.NoError(t, fh.Serialize(res, false, buffer))
	assert.Equal(t, `  - .travis.yml:
    commits: ["2b1ed978194a94edeabbca6de7ff3b5771d4d665"]
    people: {1:[12,0,0]}
  - cmd/hercules/main.go:
    commits: ["0000000000000000000000000000000000000000","2b1ed978194a94edeabbca6de7ff3b5771d4d665"]
    people: {1:[0,207,0]}
`, buffer.String())
}

func TestFileHistorySerializeBinary(t *testing.T) {
	fh := bakeFileHistoryForSerialization(t)
	res := fh.Finalize().(FileHistoryResult)
	buffer := &bytes.Buffer{}
	assert.NoError(t, fh.Serialize(res, true, buffer))
	msg := pb.FileHistoryResultMessage{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Len(t, msg.GetFiles(), 2)
	assert.Len(t, msg.GetFiles()[testTravisPath].GetCommits(), 1)
	assert.Equal(t, "2b1ed978194a94edeabbca6de7ff3b5771d4d665", msg.GetFiles()[testTravisPath].GetCommits()[0])
	assert.Len(t, msg.GetFiles()[testHerculesMainPath].GetCommits(), 2)
	assert.Equal(t, "0000000000000000000000000000000000000000", msg.GetFiles()[testHerculesMainPath].GetCommits()[0])
	assert.Equal(t, "2b1ed978194a94edeabbca6de7ff3b5771d4d665", msg.GetFiles()[testHerculesMainPath].GetCommits()[1])
	assert.Equal(
		t,
		map[int32]*pb.LineStats{1: {Added: 12, Removed: 0, Changed: 0}},
		msg.GetFiles()[testTravisPath].GetChangesByDeveloper(),
	)
	assert.Equal(
		t,
		map[int32]*pb.LineStats{1: {Added: 0, Removed: 207, Changed: 0}},
		msg.GetFiles()[testHerculesMainPath].GetChangesByDeveloper(),
	)
}

func bakeFileHistoryForSerialization(t *testing.T) *FileHistoryAnalysis {
	t.Helper()
	fh := fixtureFileHistory()
	deps := map[string]any{}
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
		To: object.ChangeEntry{}, From: object.ChangeEntry{
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
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	deps[core.DependencyCommit] = commit
	deps[identity.DependencyAuthor] = 1
	fd := fixtures.FileDiff()
	result, err := fd.Consume(deps)
	assert.NoError(t, err)
	deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]

	lineStats, err := (&items.LinesStatsCalculator{}).Consume(deps)
	assert.NoError(t, err)
	deps[items.DependencyLineStats] = lineStats[items.DependencyLineStats]

	fh.files[testHerculesMainPath] = &FileHistory{Hashes: []plumbing.Hash{plumbing.NewHash(
		"0000000000000000000000000000000000000000",
	)}}
	fh.files[testAnalyserPath] = &FileHistory{Hashes: []plumbing.Hash{plumbing.NewHash(
		"ffffffffffffffffffffffffffffffffffffffff",
	)}}
	cres, err := fh.Consume(deps)
	assert.Nil(t, cres)
	assert.NoError(t, err)
	return fh
}
