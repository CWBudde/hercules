package leaves

import (
	"testing"
	"time"

	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/linehistory"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestCodeChurnMeta(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Equal(t, "CodeChurn", cc.Name())
	assert.Len(t, cc.Provides(), 0)
	assert.Contains(t, cc.Requires(), linehistory.DependencyLineHistory)
	assert.Contains(t, cc.Requires(), identity.DependencyAuthor)
	assert.Equal(t, "codechurn", cc.Flag())
	assert.NotEmpty(t, cc.Description())
}

func TestCodeChurnRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&CodeChurnAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "CodeChurn", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&CodeChurnAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestCodeChurnListConfigurationOptions(t *testing.T) {
	cc := CodeChurnAnalysis{}
	opts := cc.ListConfigurationOptions()
	assert.Equal(t, len(BurndownSharedOptions), len(opts))
}

func TestCodeChurnConfigure(t *testing.T) {
	cc := CodeChurnAnalysis{}
	facts := map[string]interface{}{}
	facts[items.FactTickSize] = 24 * time.Hour
	facts[ConfigBurndownGranularity] = 15
	facts[ConfigBurndownSampling] = 10
	facts[ConfigBurndownTrackFiles] = true
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	resolver := core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	facts[core.FactIdentityResolver] = resolver

	assert.Nil(t, cc.Configure(facts))
	assert.Equal(t, 24*time.Hour, cc.tickSize)
	assert.Equal(t, 15, cc.Granularity)
	assert.Equal(t, 10, cc.Sampling)
	assert.True(t, cc.TrackFiles)
	assert.Equal(t, logger, cc.l)
	assert.Equal(t, resolver, cc.peopleResolver)
}

func TestCodeChurnConfigureDefaults(t *testing.T) {
	cc := CodeChurnAnalysis{}
	facts := map[string]interface{}{}
	assert.Nil(t, cc.Configure(facts))
	assert.NotNil(t, cc.l)
}

func TestCodeChurnConfigureUpstream(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Nil(t, cc.ConfigureUpstream(map[string]interface{}{}))
}

func TestCodeChurnInitialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.NotNil(t, cc.codeChurns)
	assert.NotNil(t, cc.churnDeltas)
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeWithValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 20
	cc.Sampling = 10
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.Equal(t, 20, cc.Granularity)
	assert.Equal(t, 10, cc.Sampling)
}

func TestCodeChurnInitializeSamplingGreaterThanGranularity(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 10
	cc.Sampling = 20
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.Equal(t, cc.Granularity, cc.Sampling)
}

func TestCodeChurnInitializeZeroValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 0
	cc.Sampling = 0
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeNegativeValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = -5
	cc.Sampling = -3
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeWithPeopleResolver(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))
	assert.Len(t, cc.codeChurns, 2)
}

func TestCodeChurnFork(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Nil(t, cc.Initialize(test.Repository))

	forks := cc.Fork(2)
	assert.Len(t, forks, 2)

	cc2 := forks[0].(*CodeChurnAnalysis)
	cc3 := forks[1].(*CodeChurnAnalysis)
	assert.NotNil(t, cc2)
	assert.NotNil(t, cc3)
}

func TestCodeChurnConsumeSkipsDeletes(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			core.NewLineHistoryDeletion(0, 0, 1),
		},
	}

	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)
}

func TestCodeChurnConsumeBasicInsert(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      10,
			},
		},
	}

	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	// Verify the author's file entry was updated
	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines)
	assert.Equal(t, int32(10), entry.ownedLines)
}

func TestCodeChurnConsumeDeleteByOther(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// First: Alice inserts lines
	changes1 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      10,
			},
		},
	}
	deps1 := map[string]interface{}{
		linehistory.DependencyLineHistory: changes1,
	}
	_, err := cc.Consume(deps1)
	assert.Nil(t, err)

	// Then: Bob deletes some of Alice's lines
	changes2 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   2,
				PrevTick:   1,
				CurrAuthor: 1, // Bob
				PrevAuthor: 0, // Alice's lines
				Delta:      -3,
			},
		},
	}
	deps2 := map[string]interface{}{
		linehistory.DependencyLineHistory: changes2,
	}
	_, err = cc.Consume(deps2)
	assert.Nil(t, err)

	// Alice's owned lines should have decreased
	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines) // inserted stays the same
	assert.Equal(t, int32(7), entry.ownedLines)      // 10 - 3
}

func TestCodeChurnConsumeDeleteBySelf(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// Alice inserts lines
	changes1 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      10,
			},
		},
	}
	deps1 := map[string]interface{}{
		linehistory.DependencyLineHistory: changes1,
	}
	_, err := cc.Consume(deps1)
	assert.Nil(t, err)

	// Alice deletes some of her own lines
	changes2 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   2,
				PrevTick:   1,
				CurrAuthor: 0, // Alice
				PrevAuthor: 0, // Alice's lines
				Delta:      -4,
			},
		},
	}
	deps2 := map[string]interface{}{
		linehistory.DependencyLineHistory: changes2,
	}
	_, err = cc.Consume(deps2)
	assert.Nil(t, err)

	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines)
	assert.Equal(t, int32(6), entry.ownedLines) // 10 - 4
}

func TestCodeChurnConsumeSkipsMissingAuthor(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// Change from AuthorMissing (should be skipped in updateAuthor)
	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: core.AuthorMissing,
				Delta:      5,
			},
		},
	}
	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	// No file entry should have been created for author 0 since PrevAuthor is missing
	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeSkipsZeroDelta(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      0,
			},
		},
	}
	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeAuthorOutOfRange(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// PrevAuthor is out of range (not AuthorMissing), should be remapped to AuthorMissing
	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 999, // out of range for 1-person resolver
				Delta:      5,
			},
		},
	}
	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.Nil(t, err)
	assert.Nil(t, result)

	// PrevAuthor was remapped to AuthorMissing, so updateAuthor skips it
	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeMultipleFiles(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{
				FileId:     0,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      10,
			},
			{
				FileId:     1,
				CurrTick:   1,
				PrevTick:   0,
				CurrAuthor: 0,
				PrevAuthor: 0,
				Delta:      20,
			},
		},
	}
	deps := map[string]interface{}{
		linehistory.DependencyLineHistory: changes,
	}

	_, err := cc.Consume(deps)
	assert.Nil(t, err)

	assert.Equal(t, int32(10), cc.codeChurns[0].files[core.FileId(0)].insertedLines)
	assert.Equal(t, int32(20), cc.codeChurns[0].files[core.FileId(1)].insertedLines)
}

func TestCodeChurnFinalize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// Populate some data
	cc.codeChurns[0].files = map[core.FileId]churnFileEntry{
		0: {insertedLines: 50, ownedLines: 40},
	}
	cc.codeChurns[1].files = map[core.FileId]churnFileEntry{
		0: {insertedLines: 30, ownedLines: 25},
	}

	result := cc.Finalize()
	assert.Nil(t, result) // Finalize returns nil currently
}

func TestCodeChurnSerialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Nil(t, cc.Serialize(nil, false, nil))
	assert.Nil(t, cc.Serialize(nil, true, nil))
}

func TestCodeChurnDeserialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	result, err := cc.Deserialize(nil)
	assert.Nil(t, err)
	assert.Nil(t, result)
}

func TestCodeChurnMergeResults(t *testing.T) {
	cc := CodeChurnAnalysis{}
	result := cc.MergeResults(nil, nil, nil, nil)
	assert.Nil(t, result)
}

func TestPersonChurnStatsGetFileEntry(t *testing.T) {
	t.Run("nil files map", func(t *testing.T) {
		p := personChurnStats{}
		entry := p.getFileEntry(0)
		assert.NotNil(t, entry.deleteHistory)
		assert.NotNil(t, p.files)
	})

	t.Run("existing files map, new file", func(t *testing.T) {
		p := personChurnStats{
			files: map[core.FileId]churnFileEntry{},
		}
		entry := p.getFileEntry(1)
		assert.NotNil(t, entry.deleteHistory)
	})

	t.Run("existing entry with deleteHistory", func(t *testing.T) {
		existing := churnFileEntry{
			insertedLines: 10,
			ownedLines:    5,
			deleteHistory: map[core.AuthorId]sparseHistory{},
		}
		p := personChurnStats{
			files: map[core.FileId]churnFileEntry{0: existing},
		}
		entry := p.getFileEntry(0)
		assert.Equal(t, int32(10), entry.insertedLines)
		assert.Equal(t, int32(5), entry.ownedLines)
	})

	t.Run("existing entry without deleteHistory", func(t *testing.T) {
		existing := churnFileEntry{
			insertedLines: 10,
			ownedLines:    5,
		}
		p := personChurnStats{
			files: map[core.FileId]churnFileEntry{0: existing},
		}
		entry := p.getFileEntry(0)
		assert.NotNil(t, entry.deleteHistory)
	})
}

func TestCodeChurnMemoryLoss(t *testing.T) {
	cc := CodeChurnAnalysis{}

	tests := []struct {
		name string
		x    float64
	}{
		{"zero", 0.0},
		{"at half life", 30.0},
		{"large value", 100.0},
		{"small value", 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cc.memoryLoss(tt.x)
			assert.True(t, result >= 0.0 && result <= 1.0,
				"memoryLoss(%f) = %f, should be in [0, 1]", tt.x, result)
		})
	}

	// memoryLoss is a sigmoid, should be monotonically decreasing
	assert.Greater(t, cc.memoryLoss(0), cc.memoryLoss(30))
	assert.Greater(t, cc.memoryLoss(30), cc.memoryLoss(100))

	// At x=0, sigmoid is 1/(1+exp(-30)) ≈ 1.0
	assert.InDelta(t, 1.0, cc.memoryLoss(0), 0.001)
	// At x=30 (halfLossPeriod), sigmoid is 1/(1+exp(0)) = 0.5
	assert.InDelta(t, 0.5, cc.memoryLoss(30), 0.001)
}

func TestCodeChurnCalculateAwareness(t *testing.T) {
	cc := CodeChurnAnalysis{}

	t.Run("zero insertedLines returns initial values", func(t *testing.T) {
		entry := churnFileEntry{insertedLines: 0}
		change := core.LineHistoryChange{CurrTick: 5}
		awareness, memorability := cc.calculateAwareness(entry, change, 0, churnLines{})
		assert.Equal(t, 0.0, awareness)
		assert.Equal(t, 0.5, memorability)
	})

	t.Run("lastTouch >= CurrTick returns current values", func(t *testing.T) {
		entry := churnFileEntry{
			insertedLines: 10,
			awareness:     0.8,
			memorability:  0.7,
		}
		change := core.LineHistoryChange{CurrTick: 5}
		awareness, memorability := cc.calculateAwareness(entry, change, 5, churnLines{})
		assert.InDelta(t, 0.8, awareness, 0.001)
		assert.InDelta(t, 0.7, memorability, 0.001)
	})

	t.Run("lastTouch >= CurrTick also for future", func(t *testing.T) {
		entry := churnFileEntry{
			insertedLines: 10,
			awareness:     0.8,
			memorability:  0.7,
		}
		change := core.LineHistoryChange{CurrTick: 5}
		awareness, memorability := cc.calculateAwareness(entry, change, 10, churnLines{})
		assert.InDelta(t, 0.8, awareness, 0.001)
		assert.InDelta(t, 0.7, memorability, 0.001)
	})
}

func TestCodeChurnUpdateAwareness(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	t.Run("new delta key is created", func(t *testing.T) {
		entry := churnFileEntry{
			deleteHistory: map[core.AuthorId]sparseHistory{},
		}
		change := core.LineHistoryChange{
			FileId:     0,
			CurrTick:   1,
			PrevTick:   0,
			CurrAuthor: 0,
			PrevAuthor: 0,
			Delta:      10,
		}
		cc.updateAwareness(change, &entry)
		key := churnDeltaKey{0, 0}
		delta, exists := cc.churnDeltas[key]
		assert.True(t, exists)
		assert.Equal(t, core.TickNumber(1), delta.lastTouch)
		assert.Equal(t, int32(10), delta.inserted)
	})

	t.Run("delete by others tracks deletedByOthers", func(t *testing.T) {
		cc2 := CodeChurnAnalysis{}
		cc2.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
		assert.Nil(t, cc2.Initialize(test.Repository))

		entry := churnFileEntry{
			deleteHistory: map[core.AuthorId]sparseHistory{},
		}
		change := core.LineHistoryChange{
			FileId:     0,
			CurrTick:   1,
			PrevTick:   0,
			CurrAuthor: 1, // Bob
			PrevAuthor: 0, // Alice's lines
			Delta:      -5,
		}
		cc2.updateAwareness(change, &entry)
		key := churnDeltaKey{0, 0}
		delta, exists := cc2.churnDeltas[key]
		assert.True(t, exists)
		assert.Equal(t, int32(5), delta.deletedByOthers)
	})
}

func TestCodeChurnConsumeIntegration(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{"Alice", "Bob"}, nil)
	assert.Nil(t, cc.Initialize(test.Repository))

	// Simulate a series of changes across ticks
	// Tick 1: Alice inserts 20 lines in file 0
	changes1 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{FileId: 0, CurrTick: 1, PrevTick: 0, CurrAuthor: 0, PrevAuthor: 0, Delta: 20},
		},
	}
	_, err := cc.Consume(map[string]interface{}{linehistory.DependencyLineHistory: changes1})
	assert.Nil(t, err)

	// Tick 2: Bob inserts 15 lines in file 0 and deletes 5 of Alice's lines
	changes2 := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			{FileId: 0, CurrTick: 2, PrevTick: 1, CurrAuthor: 1, PrevAuthor: 1, Delta: 15},
			{FileId: 0, CurrTick: 2, PrevTick: 1, CurrAuthor: 1, PrevAuthor: 0, Delta: -5},
		},
	}
	_, err = cc.Consume(map[string]interface{}{linehistory.DependencyLineHistory: changes2})
	assert.Nil(t, err)

	// Check Alice's stats
	aliceEntry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(20), aliceEntry.insertedLines)
	assert.Equal(t, int32(15), aliceEntry.ownedLines) // 20 - 5

	// Check Bob's stats
	bobEntry := cc.codeChurns[1].files[core.FileId(0)]
	assert.Equal(t, int32(15), bobEntry.insertedLines)
	assert.Equal(t, int32(15), bobEntry.ownedLines)
}
