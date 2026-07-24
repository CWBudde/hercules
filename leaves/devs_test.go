package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/internal/test/fixtures"
)

func fixtureDevs() *DevsAnalysis {
	d := DevsAnalysis{}
	d.tickSize = 24 * time.Hour
	d.Initialize(test.Repository)
	people := [...]string{"one@srcd", "two@srcd"}
	d.reversedPeopleDict = people[:]
	return &d
}

func TestDevsMeta(t *testing.T) {
	d := fixtureDevs()
	assert.Equal(t, "Devs", d.Name())
	assert.Empty(t, d.Provides())
	assert.Len(t, d.Requires(), 5)
	assert.Equal(t, identity.DependencyAuthor, d.Requires()[0])
	assert.Equal(t, items.DependencyTreeChanges, d.Requires()[1])
	assert.Equal(t, items.DependencyTick, d.Requires()[2])
	assert.Equal(t, items.DependencyLanguages, d.Requires()[3])
	assert.Equal(t, items.DependencyLineStats, d.Requires()[4])
	assert.Equal(t, "devs", d.Flag())
	assert.Len(t, d.ListConfigurationOptions(), 1)
	assert.Equal(t, ConfigDevsConsiderEmptyCommits, d.ListConfigurationOptions()[0].Name)
	assert.Equal(t, "empty-commits", d.ListConfigurationOptions()[0].Flag)
	assert.Equal(t, core.BoolConfigurationOption, d.ListConfigurationOptions()[0].Type)
	assert.Equal(t, false, d.ListConfigurationOptions()[0].Default)
	assert.NotEmpty(t, d.Description())
	logger := core.NewLogger()
	assert.NoError(t, d.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, d.l)
}

func TestDevsRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&DevsAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "Devs", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&DevsAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestDevsConfigure(t *testing.T) {
	devs := DevsAnalysis{}
	facts := map[string]any{}
	facts[ConfigDevsConsiderEmptyCommits] = true
	facts[items.FactTickSize] = 3 * time.Hour
	assert.NoError(t, devs.Configure(facts))
	assert.True(t, devs.ConsiderEmptyCommits)
	assert.Equal(t, 3*time.Hour, devs.tickSize)
}

func TestDevsInitialize(t *testing.T) {
	d := fixtureDevs()
	assert.NotNil(t, d.ticks)
	d = &DevsAnalysis{}
	assert.Error(t, d.Initialize(test.Repository))
}

func TestDevsConsumeFinalize(t *testing.T) {
	devs := fixtureDevs()
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
		Name: "analyser.go",
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: "analyser.go",
			Mode: 0o100644,
			Hash: plumbing.NewHash("dc248ba2b22048cc730c571a748e8ffcf7085ab9"),
		},
	}, To: object.ChangeEntry{
		Name: "analyser.go",
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: "analyser.go",
			Mode: 0o100644,
			Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
		},
	}}
	changes[1] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: "cmd/hercules/main.go",
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: "cmd/hercules/main.go",
				Mode: 0o100644,
				Hash: plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"),
			},
		},
	}
	changes[2] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: ".travis.yml",
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: ".travis.yml",
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

	result, err = devs.Consume(deps)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Len(t, devs.ticks, 1)
	day := devs.ticks[0]
	assert.Len(t, day, 1)
	dev := day[0]
	assert.Equal(t, 1, dev.Commits)
	assert.Equal(t, 847, dev.Added)
	assert.Equal(t, 9, dev.Removed)
	assert.Equal(t, 67, dev.Changed)
	assert.Equal(t, 847, dev.Languages["Go"].Added)
	assert.Equal(t, 9, dev.Languages["Go"].Removed)
	assert.Equal(t, 67, dev.Languages["Go"].Changed)

	deps[identity.DependencyAuthor] = 1
	lscres, err = lsc.Consume(deps)
	assert.NoError(t, err)
	deps[items.DependencyLineStats] = lscres[items.DependencyLineStats]
	result, err = devs.Consume(deps)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Len(t, devs.ticks, 1)
	day = devs.ticks[0]
	assert.Len(t, day, 2)
	for i := range 2 {
		dev = day[i]
		assert.Equal(t, 1, dev.Commits)
		assert.Equal(t, 847, dev.Added)
		assert.Equal(t, 9, dev.Removed)
		assert.Equal(t, 67, dev.Changed)
		assert.Equal(t, 847, dev.Languages["Go"].Added)
		assert.Equal(t, 9, dev.Languages["Go"].Removed)
		assert.Equal(t, 67, dev.Languages["Go"].Changed)
	}

	result, err = devs.Consume(deps)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Len(t, devs.ticks, 1)
	day = devs.ticks[0]
	assert.Len(t, day, 2)
	dev = day[0]
	assert.Equal(t, 1, dev.Commits)
	assert.Equal(t, 847, dev.Added)
	assert.Equal(t, 9, dev.Removed)
	assert.Equal(t, 67, dev.Changed)
	assert.Equal(t, 847, dev.Languages["Go"].Added)
	assert.Equal(t, 9, dev.Languages["Go"].Removed)
	assert.Equal(t, 67, dev.Languages["Go"].Changed)
	dev = day[1]
	assert.Equal(t, 2, dev.Commits)
	assert.Equal(t, 847*2, dev.Added)
	assert.Equal(t, 9*2, dev.Removed)
	assert.Equal(t, 67*2, dev.Changed)
	assert.Equal(t, 847*2, dev.Languages["Go"].Added)
	assert.Equal(t, 9*2, dev.Languages["Go"].Removed)
	assert.Equal(t, 67*2, dev.Languages["Go"].Changed)

	deps[items.DependencyTick] = 1
	result, err = devs.Consume(deps)
	assert.Nil(t, result)
	assert.NoError(t, err)
	assert.Len(t, devs.ticks, 2)
	day = devs.ticks[0]
	assert.Len(t, day, 2)
	dev = day[0]
	assert.Equal(t, 1, dev.Commits)
	assert.Equal(t, 847, dev.Added)
	assert.Equal(t, 9, dev.Removed)
	assert.Equal(t, 67, dev.Changed)
	assert.Equal(t, 847, dev.Languages["Go"].Added)
	assert.Equal(t, 9, dev.Languages["Go"].Removed)
	assert.Equal(t, 67, dev.Languages["Go"].Changed)
	dev = day[1]
	assert.Equal(t, 2, dev.Commits)
	assert.Equal(t, 847*2, dev.Added)
	assert.Equal(t, 9*2, dev.Removed)
	assert.Equal(t, 67*2, dev.Changed)
	assert.Equal(t, 847*2, dev.Languages["Go"].Added)
	assert.Equal(t, 9*2, dev.Languages["Go"].Removed)
	assert.Equal(t, 67*2, dev.Languages["Go"].Changed)
	day = devs.ticks[1]
	assert.Len(t, day, 1)
	dev = day[1]
	assert.Equal(t, 1, dev.Commits)
	assert.Equal(t, 847, dev.Added)
	assert.Equal(t, 9, dev.Removed)
	assert.Equal(t, 67, dev.Changed)
	assert.Equal(t, 847, dev.Languages["Go"].Added)
	assert.Equal(t, 9, dev.Languages["Go"].Removed)
	assert.Equal(t, 67, dev.Languages["Go"].Changed)
}

func ls(added, removed, changed int) items.LineStats {
	return items.LineStats{Added: added, Removed: removed, Changed: changed}
}

func TestDevsFinalize(t *testing.T) {
	devs := fixtureDevs()
	devs.ticks[1] = map[int]*DevTick{}
	devs.ticks[1][1] = &DevTick{10, ls(20, 30, 40), nil}
	x := devs.Finalize().(DevsResult)
	assert.Equal(t, x.Ticks, devs.ticks)
	assert.Equal(t, x.reversedPeopleDict, devs.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, devs.tickSize)
}

func TestDevsFork(t *testing.T) {
	devs := fixtureDevs()
	clone := devs.Fork(1)[0].(*DevsAnalysis)
	assert.Same(t, devs, clone)
}

func TestDevsSerialize(t *testing.T) {
	devs := fixtureDevs()
	devs.ticks[1] = map[int]*DevTick{}
	devs.ticks[1][0] = &DevTick{10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(2, 3, 4)}}
	devs.ticks[1][1] = &DevTick{1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(25, 35, 45)}}
	devs.ticks[10] = map[int]*DevTick{}
	devs.ticks[10][0] = &DevTick{11, ls(21, 31, 41), map[string]items.LineStats{"": ls(12, 13, 14)}}
	devs.ticks[10][core.AuthorMissing] = &DevTick{
		100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(32, 33, 34)},
	}
	res := devs.Finalize().(DevsResult)
	buffer := &bytes.Buffer{}
	err := devs.Serialize(res, false, buffer)
	assert.NoError(t, err)
	assert.Equal(t, `  ticks:
    1:
      0: [10, 20, 30, 40, {Go: [2, 3, 4]}]
      1: [1, 2, 3, 4, {Go: [25, 35, 45]}]
    10:
      0: [11, 21, 31, 41, {none: [12, 13, 14]}]
      -1: [100, 200, 300, 400, {Go: [32, 33, 34]}]
  people:
  - "one@srcd"
  - "two@srcd"
  tick_size: 86400
`, buffer.String())

	buffer = &bytes.Buffer{}
	err = devs.Serialize(res, true, buffer)
	assert.NoError(t, err)
	msg := pb.DevsAnalysisResults{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Equal(t, msg.GetDevIndex(), devs.reversedPeopleDict)
	assert.Equal(t, int64(24*time.Hour), msg.GetTickSize())
	assert.Len(t, msg.GetTicks(), 2)
	assert.Len(t, msg.GetTicks()[1].GetDevs(), 2)
	assert.Equal(t, &pb.DevTick{
		Commits: 10, Stats: &pb.LineStats{Added: 20, Removed: 30, Changed: 40},
		Languages: map[string]*pb.LineStats{"Go": {Added: 2, Removed: 3, Changed: 4}},
	}, msg.GetTicks()[1].GetDevs()[0])
	assert.Equal(t, &pb.DevTick{
		Commits: 1, Stats: &pb.LineStats{Added: 2, Removed: 3, Changed: 4},
		Languages: map[string]*pb.LineStats{"Go": {Added: 25, Removed: 35, Changed: 45}},
	}, msg.GetTicks()[1].GetDevs()[1])
	assert.Len(t, msg.GetTicks()[10].GetDevs(), 2)
	assert.Equal(t, &pb.DevTick{
		Commits: 11, Stats: &pb.LineStats{Added: 21, Removed: 31, Changed: 41},
		Languages: map[string]*pb.LineStats{"": {Added: 12, Removed: 13, Changed: 14}},
	}, msg.GetTicks()[10].GetDevs()[0])
	assert.Equal(t, &pb.DevTick{
		Commits: 100, Stats: &pb.LineStats{Added: 200, Removed: 300, Changed: 400},
		Languages: map[string]*pb.LineStats{"Go": {Added: 32, Removed: 33, Changed: 34}},
	}, msg.GetTicks()[10].GetDevs()[-1])
}

func TestDevsDeserialize(t *testing.T) {
	devs := fixtureDevs()
	devs.ticks[1] = map[int]*DevTick{}
	devs.ticks[1][0] = &DevTick{10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}}
	devs.ticks[1][1] = &DevTick{1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}}
	devs.ticks[10] = map[int]*DevTick{}
	devs.ticks[10][0] = &DevTick{11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(32, 33, 34)}}
	devs.ticks[10][core.AuthorMissing] = &DevTick{
		100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(42, 43, 44)},
	}
	res := devs.Finalize().(DevsResult)
	buffer := &bytes.Buffer{}
	err := devs.Serialize(res, true, buffer)
	assert.NoError(t, err)
	rawres2, err := devs.Deserialize(buffer.Bytes())
	assert.NoError(t, err)
	res2 := rawres2.(DevsResult)
	assert.Equal(t, res, res2)
}

func TestDevsMergeResults(t *testing.T) {
	people1 := [...]string{"1@srcd", "2@srcd"}
	people2 := [...]string{"3@srcd", "1@srcd"}
	r1 := DevsResult{
		Ticks:              map[int]map[int]*DevTick{},
		reversedPeopleDict: people1[:],
		tickSize:           24 * time.Hour,
	}
	r1.Ticks[1] = map[int]*DevTick{}
	r1.Ticks[1][0] = &DevTick{10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}}
	r1.Ticks[1][1] = &DevTick{1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}}
	r1.Ticks[10] = map[int]*DevTick{}
	r1.Ticks[10][0] = &DevTick{11, ls(21, 31, 41), nil}
	r1.Ticks[10][core.AuthorMissing] = &DevTick{
		100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(32, 33, 34)},
	}
	r1.Ticks[11] = map[int]*DevTick{}
	r1.Ticks[11][1] = &DevTick{10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(42, 43, 44)}}
	r2 := DevsResult{
		Ticks:              map[int]map[int]*DevTick{},
		reversedPeopleDict: people2[:],
		tickSize:           22 * time.Hour,
	}
	r2.Ticks[1] = map[int]*DevTick{}
	r2.Ticks[1][0] = &DevTick{10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}}
	r2.Ticks[1][1] = &DevTick{1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}}
	r2.Ticks[2] = map[int]*DevTick{}
	r2.Ticks[2][0] = &DevTick{11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(32, 33, 34)}}
	r2.Ticks[2][core.AuthorMissing] = &DevTick{
		100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(42, 43, 44)},
	}
	r2.Ticks[10] = map[int]*DevTick{}
	r2.Ticks[10][0] = &DevTick{11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(52, 53, 54)}}
	r2.Ticks[10][core.AuthorMissing] = &DevTick{
		100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(62, 63, 64)},
	}

	devs := fixtureDevs()
	c1 := core.CommonAnalysisResult{BeginTime: 1556224895}
	mergeErr, ok := devs.MergeResults(r1, r2, &c1, &c1).(error)
	require.True(t, ok)
	require.Error(t, mergeErr)
	r2.tickSize = r1.tickSize
	rm := devs.MergeResults(r1, r2, &c1, &c1).(DevsResult)
	peoplerm := [...]string{"1@srcd", "2@srcd", "3@srcd"}
	assert.Equal(t, rm.reversedPeopleDict, peoplerm[:])
	assert.Len(t, rm.Ticks, 4)
	assert.Equal(t, map[int]*DevTick{
		1: {10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(42, 43, 44)}},
	}, rm.Ticks[11])
	assert.Equal(t, map[int]*DevTick{
		core.AuthorMissing: {100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(42, 43, 44)}},
		2:                  {11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(32, 33, 34)}},
	}, rm.Ticks[2])
	assert.Equal(t, map[int]*DevTick{
		0: {11, ls(22, 33, 44), map[string]items.LineStats{"Go": ls(34, 36, 38)}},
		1: {1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}},
		2: {10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}},
	}, rm.Ticks[1])
	assert.Equal(t, map[int]*DevTick{
		0: {11, ls(21, 31, 41), map[string]items.LineStats{}},
		2: {11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(52, 53, 54)}},
		core.AuthorMissing: {
			100 * 2, ls(200*2, 300*2, 400*2), map[string]items.LineStats{"Go": ls(94, 96, 98)},
		},
	}, rm.Ticks[10])

	c2 := core.CommonAnalysisResult{BeginTime: 1556224895 + 24*3600}
	rm = devs.MergeResults(r1, r2, &c1, &c2).(DevsResult)
	assert.Len(t, rm.Ticks, 5)
	assert.Equal(t, map[int]*DevTick{
		0: {10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}},
		1: {1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}},
	}, rm.Ticks[1])
	assert.Equal(t, map[int]*DevTick{
		2: {10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(12, 13, 14)}},
		0: {1, ls(2, 3, 4), map[string]items.LineStats{"Go": ls(22, 23, 24)}},
	}, rm.Ticks[2])
	assert.Equal(t, map[int]*DevTick{
		2:                  {11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(32, 33, 34)}},
		core.AuthorMissing: {100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(42, 43, 44)}},
	}, rm.Ticks[3])
	assert.Equal(t, map[int]*DevTick{
		0:                  {11, ls(21, 31, 41), map[string]items.LineStats{}},
		core.AuthorMissing: {100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(32, 33, 34)}},
	}, rm.Ticks[10])
	assert.Equal(t, map[int]*DevTick{
		1:                  {10, ls(20, 30, 40), map[string]items.LineStats{"Go": ls(42, 43, 44)}},
		2:                  {11, ls(21, 31, 41), map[string]items.LineStats{"Go": ls(52, 53, 54)}},
		core.AuthorMissing: {100, ls(200, 300, 400), map[string]items.LineStats{"Go": ls(62, 63, 64)}},
	}, rm.Ticks[11])
}

func TestDevsResultGetters(t *testing.T) {
	dr := DevsResult{tickSize: time.Hour, reversedPeopleDict: []string{"one", "two"}}
	assert.Equal(t, dr.tickSize, dr.GetTickSize())
	assert.Equal(t, dr.GetIdentities(), dr.reversedPeopleDict)
}
