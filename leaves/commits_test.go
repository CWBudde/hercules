package leaves

import (
	"bytes"
	"sort"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/internal/test/fixtures"
)

func TestCommitsMeta(t *testing.T) {
	ca := CommitsAnalysis{}
	assert.Equal(t, "CommitsStat", ca.Name())
	assert.Empty(t, ca.Provides())
	required := [...]string{identity.DependencyAuthor, items.DependencyLanguages, items.DependencyLineStats}
	for _, name := range required {
		assert.Contains(t, ca.Requires(), name)
	}
	opts := ca.ListConfigurationOptions()
	assert.Empty(t, opts)
	assert.Equal(t, "commits-stat", ca.Flag())
	logger := core.NewLogger()
	assert.NoError(t, ca.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, ca.l)
}

func TestCommitsRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&CommitsAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "CommitsStat", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&CommitsAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestCommitsConfigure(t *testing.T) {
	ca := CommitsAnalysis{}
	facts := map[string]any{}
	facts[identity.FactIdentityDetectorReversedPeopleDict] = ca.Requires()
	assert.NoError(t, ca.Configure(facts))
	assert.Equal(t, ca.reversedPeopleDict, ca.Requires())
}

func TestCommitsConsume(t *testing.T) {
	ca := CommitsAnalysis{}
	assert.NoError(t, ca.Initialize(test.Repository))
	deps := map[string]any{}

	// stage 1
	deps[identity.DependencyAuthor] = 0
	cache := map[plumbing.Hash]*items.CachedBlob{}
	AddHash(t, cache, "291286b4ac41952cbd1389fda66420ec03c1a9fe")
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	AddHash(t, cache, "dc248ba2b22048cc730c571a748e8ffcf7085ab9")
	deps[items.DependencyBlobCache] = cache
	deps[items.DependencyLanguages] = map[plumbing.Hash]string{
		plumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe"): "Go",
		plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"): "Go",
		plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"): "Go",
		plumbing.NewHash("dc248ba2b22048cc730c571a748e8ffcf7085ab9"): "Go",
	}
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
	result, err := fd.Consume(deps)
	assert.NoError(t, err)
	deps[items.DependencyFileDiff] = result[items.DependencyFileDiff]
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	lsc := &items.LinesStatsCalculator{}
	lscres, err := lsc.Consume(deps)
	assert.NoError(t, err)
	deps[items.DependencyLineStats] = lscres[items.DependencyLineStats]

	result, err = ca.Consume(deps)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Len(t, ca.commits, 1)
	c := ca.commits[0]
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", c.Hash)
	assert.Equal(t, int64(1481563829), c.When)
	assert.Equal(t, 0, c.Author)
	assert.Len(t, c.Files, 3)
	sort.Slice(c.Files, func(i, j int) bool { return c.Files[i].Name < c.Files[j].Name })
	assert.Equal(t, testTravisPath, c.Files[0].Name)
	assert.Equal(t, "Go", c.Files[0].Language)
	assert.Equal(t, 12, c.Files[0].Added)
	assert.Equal(t, 0, c.Files[0].Removed)
	assert.Equal(t, 0, c.Files[0].Changed)
	assert.Equal(t, testAnalyserPath, c.Files[1].Name)
	assert.Equal(t, "Go", c.Files[1].Language)
	assert.Equal(t, 628, c.Files[1].Added)
	assert.Equal(t, 9, c.Files[1].Removed)
	assert.Equal(t, 67, c.Files[1].Changed)
	assert.Equal(t, testHerculesMainPath, c.Files[2].Name)
	assert.Equal(t, "Go", c.Files[2].Language)
	assert.Equal(t, 207, c.Files[2].Added)
	assert.Equal(t, 0, c.Files[2].Removed)
	assert.Equal(t, 0, c.Files[2].Changed)
}

func fixtureCommits(t *testing.T) *CommitsAnalysis {
	t.Helper()
	ca := CommitsAnalysis{}
	assert.NoError(t, ca.Initialize(test.Repository))
	ca.commits = []*CommitStat{
		{
			Hash:   "cce947b98a050c6d356bc6ba95030254914027b1",
			When:   1481563829,
			Author: 0,
			Files: []FileStat{
				{
					Name:     testTravisPath,
					Language: "Yaml",
					LineStats: items.LineStats{
						Added:   12,
						Removed: 0,
						Changed: 0,
					},
				},
				{
					Name:     testAnalyserPath,
					Language: "Go",
					LineStats: items.LineStats{
						Added:   628,
						Removed: 9,
						Changed: 67,
					},
				},
			},
		},
		{
			Hash:   "c29112dbd697ad9b401333b80c18a63951bc18d9",
			When:   1481563999,
			Author: 1,
			Files: []FileStat{
				{
					Name:     testHerculesMainPath,
					Language: "Go",
					LineStats: items.LineStats{
						Added:   1,
						Removed: 0,
						Changed: 0,
					},
				},
			},
		},
	}
	people := [...]string{testPersonOneSource, testPersonTwoSource}
	ca.reversedPeopleDict = people[:]

	return &ca
}

func TestCommitsFinalize(t *testing.T) {
	ca := fixtureCommits(t)
	x := ca.Finalize().(CommitsResult)
	assert.Equal(t, x.Commits, ca.commits)
	assert.Equal(t, x.reversedPeopleDict, ca.reversedPeopleDict)
}

func TestCommitsSerialize(t *testing.T) {
	ca := fixtureCommits(t)
	res := ca.Finalize().(CommitsResult)
	buffer := &bytes.Buffer{}
	err := ca.Serialize(res, false, buffer)
	assert.NoError(t, err)
	assert.Equal(t, `  commits:
    - hash: cce947b98a050c6d356bc6ba95030254914027b1
      when: 1481563829
      author: 0
      files:
       - name: .travis.yml
         language: Yaml
         stat: [12, 0, 0]
       - name: analyser.go
         language: Go
         stat: [628, 67, 9]
    - hash: c29112dbd697ad9b401333b80c18a63951bc18d9
      when: 1481563999
      author: 1
      files:
       - name: cmd/hercules/main.go
         language: Go
         stat: [1, 0, 0]
  people:
  - "one@srcd"
  - "two@srcd"
`, buffer.String())

	buffer = &bytes.Buffer{}
	err = ca.Serialize(res, true, buffer)
	assert.NoError(t, err)
	msg := pb.CommitsAnalysisResults{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Equal(t, msg.GetAuthorIndex(), ca.reversedPeopleDict)
	assert.Len(t, msg.GetCommits(), 2)
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", msg.GetCommits()[0].GetHash())
	assert.Equal(t, int64(1481563829), msg.GetCommits()[0].GetWhenUnixTime())
	assert.Equal(t, int32(0), msg.GetCommits()[0].GetAuthor())
	assert.Len(t, msg.GetCommits()[0].GetFiles(), 2)
	assert.Equal(t, &pb.CommitFile{
		Name:     testTravisPath,
		Stats:    &pb.LineStats{Added: 12, Removed: 0, Changed: 0},
		Language: "Yaml",
	}, msg.GetCommits()[0].GetFiles()[0])
	assert.Equal(t, &pb.CommitFile{
		Name:     testAnalyserPath,
		Stats:    &pb.LineStats{Added: 628, Removed: 9, Changed: 67},
		Language: "Go",
	}, msg.GetCommits()[0].GetFiles()[1])
	assert.Equal(t, "c29112dbd697ad9b401333b80c18a63951bc18d9", msg.GetCommits()[1].GetHash())
	assert.Equal(t, int64(1481563999), msg.GetCommits()[1].GetWhenUnixTime())
	assert.Equal(t, int32(1), msg.GetCommits()[1].GetAuthor())
	assert.Len(t, msg.GetCommits()[1].GetFiles(), 1)
	assert.Equal(t, &pb.CommitFile{
		Name:     testHerculesMainPath,
		Stats:    &pb.LineStats{Added: 1, Removed: 0, Changed: 0},
		Language: "Go",
	}, msg.GetCommits()[1].GetFiles()[0])
}
