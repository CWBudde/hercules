package leaves

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv2 "gopkg.in/yaml.v2"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
)

func TestCodeChurnMeta(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.Equal(t, "CodeChurn", cc.Name())
	assert.Empty(t, cc.Provides())
	assert.Contains(t, cc.Requires(), linehistory.DependencyLineHistory)
	assert.Contains(t, cc.Requires(), identity.DependencyAuthor)
	assert.Equal(t, "codechurn", cc.Flag())
	assert.Contains(t, cc.Description(), "Experimental")
	assert.Contains(t, cc.Description(), "awareness")
	assert.Contains(t, cc.Description(), "deletion history")
	assert.NotContains(t, cc.Description(), "burndown")
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
	assert.Len(t, opts, len(burndownSharedOptions()))
}

func TestCodeChurnConfigure(t *testing.T) {
	cc := CodeChurnAnalysis{}
	facts := map[string]any{}
	facts[items.FactTickSize] = 24 * time.Hour
	facts[ConfigBurndownGranularity] = 15
	facts[ConfigBurndownSampling] = 10
	facts[ConfigBurndownTrackFiles] = true
	logger := core.NewLogger()
	facts[core.ConfigLogger] = logger

	resolver := core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	facts[core.FactIdentityResolver] = resolver

	assert.NoError(t, cc.Configure(facts))
	assert.Equal(t, 24*time.Hour, cc.tickSize)
	assert.Equal(t, 15, cc.Granularity)
	assert.Equal(t, 10, cc.Sampling)
	assert.True(t, cc.TrackFiles)
	assert.Equal(t, logger, cc.l)
	assert.Equal(t, resolver, cc.peopleResolver)
}

func TestCodeChurnConfigureDefaults(t *testing.T) {
	cc := CodeChurnAnalysis{}
	facts := map[string]any{}
	assert.NoError(t, cc.Configure(facts))
	assert.NotNil(t, cc.l)
}

func TestCodeChurnConfigureUpstream(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.NoError(t, cc.ConfigureUpstream(map[string]any{}))
}

func TestCodeChurnInitialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.NotNil(t, cc.codeChurns)
	assert.False(t, cc.hasTick)
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeWithValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 20
	cc.Sampling = 10
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.Equal(t, 20, cc.Granularity)
	assert.Equal(t, 10, cc.Sampling)
}

func TestCodeChurnInitializeSamplingGreaterThanGranularity(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 10
	cc.Sampling = 20
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.Equal(t, cc.Granularity, cc.Sampling)
}

func TestCodeChurnInitializeZeroValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = 0
	cc.Sampling = 0
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeNegativeValues(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.Granularity = -5
	cc.Sampling = -3
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.Equal(t, DefaultBurndownGranularity, cc.Granularity)
	assert.Equal(t, DefaultBurndownGranularity, cc.Sampling)
}

func TestCodeChurnInitializeWithPeopleResolver(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))
	assert.Len(t, cc.codeChurns, 2)
}

func TestCodeChurnFork(t *testing.T) {
	cc := CodeChurnAnalysis{}
	assert.NoError(t, cc.Initialize(test.Repository))

	forks := cc.Fork(2)
	assert.Len(t, forks, 2)

	cc2 := forks[0].(*CodeChurnAnalysis)
	cc3 := forks[1].(*CodeChurnAnalysis)
	assert.NotNil(t, cc2)
	assert.NotNil(t, cc3)
}

func TestCodeChurnConsumeSkipsDeletes(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

	changes := core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			core.NewLineHistoryDeletion(0, 0, 1),
		},
	}

	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestCodeChurnConsumeBasicInsert(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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

	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.NoError(t, err)
	assert.Nil(t, result)

	// Verify the author's file entry was updated
	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines)
	assert.Equal(t, int32(10), entry.ownedLines)
}

func TestCodeChurnConsumeDeleteByOther(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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
	deps1 := map[string]any{
		linehistory.DependencyLineHistory: changes1,
	}
	_, err := cc.Consume(deps1)
	assert.NoError(t, err)

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
	deps2 := map[string]any{
		linehistory.DependencyLineHistory: changes2,
	}
	_, err = cc.Consume(deps2)
	assert.NoError(t, err)

	// Alice's owned lines should have decreased
	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines) // inserted stays the same
	assert.Equal(t, int32(7), entry.ownedLines)     // 10 - 3
}

func TestCodeChurnConsumeDeleteBySelf(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

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
	deps1 := map[string]any{
		linehistory.DependencyLineHistory: changes1,
	}
	_, err := cc.Consume(deps1)
	require.NoError(t, err)

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
	deps2 := map[string]any{
		linehistory.DependencyLineHistory: changes2,
	}
	_, err = cc.Consume(deps2)
	require.NoError(t, err)

	entry := cc.codeChurns[0].files[core.FileId(0)]
	assert.Equal(t, int32(10), entry.insertedLines)
	assert.Equal(t, int32(6), entry.ownedLines) // 10 - 4
}

func TestCodeChurnConsumeSkipsMissingAuthor(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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
	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.NoError(t, err)
	assert.Nil(t, result)

	// No file entry should have been created for author 0 since PrevAuthor is missing
	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeSkipsZeroDelta(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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
	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.NoError(t, err)
	assert.Nil(t, result)

	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeAuthorOutOfRange(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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
	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	result, err := cc.Consume(deps)
	assert.NoError(t, err)
	assert.Nil(t, result)

	// PrevAuthor was remapped to AuthorMissing, so updateAuthor skips it
	assert.Nil(t, cc.codeChurns[0].files)
}

func TestCodeChurnConsumeMultipleFiles(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

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
	deps := map[string]any{
		linehistory.DependencyLineHistory: changes,
	}

	_, err := cc.Consume(deps)
	assert.NoError(t, err)

	assert.Equal(t, int32(10), cc.codeChurns[0].files[core.FileId(0)].insertedLines)
	assert.Equal(t, int32(20), cc.codeChurns[0].files[core.FileId(1)].insertedLines)
}

func TestCodeChurnFinalize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	assert.NoError(t, cc.Initialize(test.Repository))

	// Populate some data
	cc.codeChurns[0].files = map[core.FileId]churnFileEntry{
		0: {insertedLines: 50, ownedLines: 40},
	}
	cc.codeChurns[1].files = map[core.FileId]churnFileEntry{
		0: {insertedLines: 30, ownedLines: 25},
	}

	result := cc.Finalize().(CodeChurnResult)
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, result.GetIdentities())
	assert.Len(t, result.Authors, 2)
	assert.Equal(t, int32(50), result.Authors[0].Files["#0"].InsertedLines)
	assert.Equal(t, int32(25), result.Authors[1].Files["#0"].OwnedLines)
}

func TestCodeChurnSerialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	result := CodeChurnResult{
		Authors: []CodeChurnAuthorResult{{
			Files: map[string]CodeChurnFileResult{
				testMainPath: {
					InsertedLines: 10,
					OwnedLines:    7,
					Memorability:  0.5,
					Awareness:     0.75,
				},
			},
		}},
		people:      []string{testPersonAlice},
		tickSize:    24 * time.Hour,
		sampling:    10,
		granularity: 30,
	}
	var text bytes.Buffer
	assert.NoError(t, cc.Serialize(result, false, &text))
	assert.Contains(t, text.String(), testMainPath)

	var binary bytes.Buffer
	assert.NoError(t, cc.Serialize(result, true, &binary))
	assert.NotEmpty(t, binary.Bytes())
}

func TestCodeChurnDeserialize(t *testing.T) {
	cc := CodeChurnAnalysis{}
	original := CodeChurnResult{
		Authors: []CodeChurnAuthorResult{{
			Files: map[string]CodeChurnFileResult{
				testMainPath: {
					InsertedLines: 10,
					OwnedLines:    7,
					Memorability:  0.5,
					Awareness:     0.75,
					DeleteHistory: map[int]sparseHistory{
						1: {
							2: {deltas: map[int]int64{1: -3}},
						},
					},
				},
			},
		}},
		people:      []string{testPersonAlice, testPersonBob},
		tickSize:    24 * time.Hour,
		sampling:    10,
		granularity: 30,
	}
	var binary bytes.Buffer
	assert.NoError(t, cc.Serialize(original, true, &binary))

	result, err := cc.Deserialize(binary.Bytes())
	assert.NoError(t, err)
	decoded := result.(CodeChurnResult)
	assert.Equal(t, original.GetIdentities(), decoded.GetIdentities())
	assert.Equal(
		t,
		original.Authors[0].Files[testMainPath].InsertedLines,
		decoded.Authors[0].Files[testMainPath].InsertedLines,
	)
	assert.Equal(t, int64(-3), decoded.Authors[0].Files[testMainPath].DeleteHistory[1][2].deltas[1])
}

func TestCodeChurnMergeResults(t *testing.T) {
	cc := CodeChurnAnalysis{}
	r1 := CodeChurnResult{
		Authors: []CodeChurnAuthorResult{{
			Files: map[string]CodeChurnFileResult{
				testMainPath: {InsertedLines: 10, OwnedLines: 7},
			},
		}},
		people:      []string{testPersonAlice},
		tickSize:    24 * time.Hour,
		sampling:    10,
		granularity: 30,
	}
	r2 := CodeChurnResult{
		Authors: []CodeChurnAuthorResult{{
			Files: map[string]CodeChurnFileResult{
				testMainPath: {InsertedLines: 4, OwnedLines: 2},
				"util.go":    {InsertedLines: 6, OwnedLines: 6},
			},
		}, {
			Files: map[string]CodeChurnFileResult{
				testMainPath: {InsertedLines: 3, OwnedLines: 1},
			},
		}},
		people:      []string{testPersonAlice, testPersonBob},
		tickSize:    24 * time.Hour,
		sampling:    15,
		granularity: 40,
	}
	result := cc.MergeResults(r1, r2, nil, nil).(CodeChurnResult)
	assert.Equal(t, []string{testPersonAlice, testPersonBob}, result.GetIdentities())
	assert.Equal(t, int32(14), result.Authors[0].Files[testMainPath].InsertedLines)
	assert.Equal(t, int32(6), result.Authors[0].Files["util.go"].OwnedLines)
	assert.Equal(t, int32(3), result.Authors[1].Files[testMainPath].InsertedLines)
	assert.Equal(t, 15, result.sampling)
	assert.Equal(t, 40, result.granularity)
}

func TestCodeChurnMergeRejectsCounterOverflow(t *testing.T) {
	cc := CodeChurnAnalysis{}
	result := func(lines int32) CodeChurnResult {
		return CodeChurnResult{
			Authors: []CodeChurnAuthorResult{{
				Files: map[string]CodeChurnFileResult{
					testMainPath: {InsertedLines: lines, OwnedLines: lines},
				},
			}},
			people:   []string{testPersonAlice},
			tickSize: 24 * time.Hour,
		}
	}

	merged := cc.MergeResults(result(math.MaxInt32), result(1), nil, nil)
	err, ok := merged.(error)
	require.True(t, ok)
	assert.ErrorIs(t, err, errCodeChurnCounterOverflow)
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

func TestCodeChurnScoreDecay(t *testing.T) {
	assert.InDelta(t, 1.0, decayCodeChurnScore(1, 0, 30), 0.000001)
	assert.InDelta(t, 0.5, decayCodeChurnScore(1, 30, 30), 0.000001)
	assert.InDelta(t, 0.25, decayCodeChurnScore(1, 60, 30), 0.000001)
	assert.InDelta(t, 1.0, decayCodeChurnScore(2, 0, 30), 0)
	assert.InDelta(t, 0.0, decayCodeChurnScore(-1, 0, 30), 0)
}

func TestCodeChurnUpdateKnowledge(t *testing.T) {
	cc := CodeChurnAnalysis{}

	t.Run("initial insertion establishes full scores", func(t *testing.T) {
		entry := churnFileEntry{}
		cc.updateKnowledge(&entry, 0, churnLines{inserted: 10})
		assert.InDelta(t, 1, entry.awareness, 0)
		assert.InDelta(t, 1, entry.memorability, 0)
	})

	t.Run("self deletion reinforces after decay", func(t *testing.T) {
		entry := churnFileEntry{
			ownedLines:    10,
			awareness:     1,
			memorability:  1,
			lastScoreTick: 0,
			hasScore:      true,
		}
		cc.updateKnowledge(&entry, 30, churnLines{deletedBySelf: 2})
		assert.InDelta(t, 0.6, entry.awareness, 0.000001)
		assert.InDelta(t, 0.9127189745, entry.memorability, 0.000001)
	})

	t.Run("other deletion disrupts without reinforcement", func(t *testing.T) {
		entry := churnFileEntry{
			ownedLines:    8,
			awareness:     0.6,
			memorability:  0.9127189745,
			lastScoreTick: 30,
			hasScore:      true,
		}
		cc.updateKnowledge(&entry, 60, churnLines{deletedByOthers: 4})
		assert.InDelta(t, 0.15, entry.awareness, 0.000001)
		assert.InDelta(t, 0.4065700822, entry.memorability, 0.000001)
	})
}

func TestCodeChurnClassifiesKnowledgeEvents(t *testing.T) {
	assert.Equal(
		t,
		churnLines{inserted: 5},
		churnLinesForChange(core.LineHistoryChange{PrevAuthor: 0, CurrAuthor: 0}, 5),
	)
	assert.Equal(
		t,
		churnLines{deletedBySelf: 4},
		churnLinesForChange(core.LineHistoryChange{PrevAuthor: 0, CurrAuthor: 0}, -4),
	)
	assert.Equal(
		t,
		churnLines{deletedByOthers: 3},
		churnLinesForChange(core.LineHistoryChange{PrevAuthor: 0, CurrAuthor: 1}, -3),
	)
}

func TestCodeChurnSameTickChangesUseEmittedOrder(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{
			FileId: 0, CurrTick: 0, PrevTick: 0,
			CurrAuthor: 0, PrevAuthor: 0, Delta: 10,
		},
	)

	_, err := cc.Consume(map[string]any{
		linehistory.DependencyLineHistory: core.LineHistoryChanges{
			Changes: []core.LineHistoryChange{
				{
					FileId: 0, CurrTick: 30, PrevTick: 0,
					CurrAuthor: 0, PrevAuthor: 0, Delta: 2,
				},
				{
					FileId: 0, CurrTick: 30, PrevTick: 0,
					CurrAuthor: 1, PrevAuthor: 0, Delta: -3,
				},
			},
		},
	})
	require.NoError(t, err)

	// At tick 30 awareness first decays to .5. Inserting 2/12 reinforces it to
	// 7/12, then deleting 3/12 of the now-owned lines reduces it to 7/16.
	result := cc.Finalize().(CodeChurnResult)
	alice := result.Authors[0].Files["#0"]
	assert.Equal(t, int32(12), alice.InsertedLines)
	assert.Equal(t, int32(9), alice.OwnedLines)
	assert.InDelta(t, 0.4375, alice.Awareness, 0.000001)
	assert.InDelta(t, 0.6818116988, alice.Memorability, 0.000001)
}

func TestCodeChurnConsumeRejectsCounterOverflow(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{
			FileId: 0, CurrTick: 0, PrevTick: 0,
			CurrAuthor: 0, PrevAuthor: 0, Delta: math.MaxInt32,
		},
	)

	_, err := cc.Consume(map[string]any{
		linehistory.DependencyLineHistory: core.LineHistoryChanges{
			Changes: []core.LineHistoryChange{{
				FileId: 0, CurrTick: 1, PrevTick: 0,
				CurrAuthor: 0, PrevAuthor: 0, Delta: 1,
			}},
		},
	})
	require.ErrorIs(t, err, errCodeChurnCounterOverflow)

	entry := cc.codeChurns[0].files[0]
	assert.Equal(t, int32(math.MaxInt32), entry.insertedLines)
	assert.Equal(t, int32(math.MaxInt32), entry.ownedLines)
}

func TestCodeChurnConsumeRejectsUnrepresentableDeletionMagnitude(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

	_, err := cc.Consume(map[string]any{
		linehistory.DependencyLineHistory: core.LineHistoryChanges{
			Changes: []core.LineHistoryChange{{
				FileId: 0, CurrTick: 1, PrevTick: 0,
				CurrAuthor: 0, PrevAuthor: 0, Delta: math.MinInt32,
			}},
		},
	})
	require.ErrorIs(t, err, errCodeChurnCounterOverflow)
	assert.Empty(t, cc.codeChurns[0].files)
}

func TestCodeChurnFinalizeDecaysInactiveFilesToLastTick(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 0, CurrTick: 0, PrevTick: 0, CurrAuthor: 0, PrevAuthor: 0, Delta: 10},
	)
	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 1, CurrTick: 30, PrevTick: 0, CurrAuthor: 1, PrevAuthor: 1, Delta: 5},
	)

	result := cc.Finalize().(CodeChurnResult)
	alice := result.Authors[0].Files["#0"]
	assert.InDelta(t, 0.5, alice.Awareness, 0.000001)
	assert.InDelta(t, 0.8908987181, alice.Memorability, 0.000001)
}

func TestCodeChurnFileDeletionAdvancesFinalDecayTick(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice}, nil)
	require.NoError(t, cc.Initialize(test.Repository))

	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{
			FileId: 0, CurrTick: 0, PrevTick: 0,
			CurrAuthor: 0, PrevAuthor: 0, Delta: 10,
		},
	)
	consumeCodeChurnChange(t, &cc, core.NewLineHistoryDeletion(1, 0, 30))

	result := cc.Finalize().(CodeChurnResult)
	alice := result.Authors[0].Files["#0"]
	assert.InDelta(t, 0.5, alice.Awareness, 0.000001)
	assert.InDelta(t, 0.8908987181, alice.Memorability, 0.000001)
}

func TestCodeChurnHandCalculatedMultiTickAndSerialization(t *testing.T) {
	cc := CodeChurnAnalysis{}
	cc.peopleResolver = core.NewIdentityResolver([]string{testPersonAlice, testPersonBob}, nil)
	require.NoError(t, cc.Initialize(test.Repository))
	cc.tickSize = 24 * time.Hour

	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 0, CurrTick: 0, PrevTick: 0, CurrAuthor: 0, PrevAuthor: 0, Delta: 10},
	)
	// Tick 30: A=1*2^-1=.5, M=1*2^(-30/180)=.8908987181.
	// Self-deleting 2/10 lines reinforces both: score'=score+(1-score)*.2.
	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 0, CurrTick: 30, PrevTick: 0, CurrAuthor: 0, PrevAuthor: 0, Delta: -2},
	)
	// Tick 60: decay another 30 ticks, then Bob deletes 4/8 owned lines, so both scores halve.
	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 0, CurrTick: 60, PrevTick: 30, CurrAuthor: 1, PrevAuthor: 0, Delta: -4},
	)
	// Tick 90: decay another 30 ticks; inserting 4 into an 8-line post-insert population
	// reinforces both scores by r=4/(4+4)=.5.
	consumeCodeChurnChange(
		t, &cc,
		core.LineHistoryChange{FileId: 0, CurrTick: 90, PrevTick: 60, CurrAuthor: 0, PrevAuthor: 0, Delta: 4},
	)

	result := cc.Finalize().(CodeChurnResult)
	alice := result.Authors[0].Files["#0"]
	assert.Equal(t, int32(14), alice.InsertedLines)
	assert.Equal(t, int32(8), alice.OwnedLines)
	assert.InDelta(t, 0.5375, alice.Awareness, 0.000001)
	assert.InDelta(t, 0.6811063825, alice.Memorability, 0.000001)
	assert.Equal(t, int64(-2), alice.DeleteHistory[0][30].deltas[0])
	assert.Equal(t, int64(-4), alice.DeleteHistory[1][60].deltas[30])

	var text bytes.Buffer
	require.NoError(t, cc.Serialize(result, false, &text))
	assert.Contains(t, text.String(), "          awareness: 0.537500\n")
	assert.Contains(t, text.String(), "          memorability: 0.681106\n")
	assert.Contains(t, text.String(), "          delete_history:\n")
	assert.Contains(t, text.String(), "            0:\n              30:\n                0: -2\n")
	assert.Contains(t, text.String(), "            1:\n              60:\n                30: -4\n")
	var yamlDocument map[any]any
	require.NoError(t, yamlv2.Unmarshal([]byte("CodeChurn:\n"+text.String()), &yamlDocument))
	yamlRoundTrip, err := yamlv2.Marshal(yamlDocument)
	require.NoError(t, err)
	var decodedYAML map[any]any
	require.NoError(t, yamlv2.Unmarshal(yamlRoundTrip, &decodedYAML))
	assert.Equal(t, yamlDocument, decodedYAML)

	var binary bytes.Buffer
	require.NoError(t, cc.Serialize(result, true, &binary))
	decodedResult, err := cc.Deserialize(binary.Bytes())
	require.NoError(t, err)
	decoded := decodedResult.(CodeChurnResult)
	assert.Equal(t, result.people, decoded.people)
	assert.Equal(t, result.tickSize, decoded.tickSize)
	assert.Equal(t, result.sampling, decoded.sampling)
	assert.Equal(t, result.granularity, decoded.granularity)
	assert.Equal(t, alice, decoded.Authors[0].Files["#0"])
}

func consumeCodeChurnChange(t *testing.T, cc *CodeChurnAnalysis, change core.LineHistoryChange) {
	t.Helper()

	result, err := cc.Consume(map[string]any{
		linehistory.DependencyLineHistory: core.LineHistoryChanges{
			Changes: []core.LineHistoryChange{change},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, result)
}
