package leaves

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/test"
)

type ownershipTestResolver map[core.FileId]string

func (resolver ownershipTestResolver) NameOf(id core.FileId) string {
	return resolver[id]
}

func (resolver ownershipTestResolver) MergedWith(id core.FileId) (core.FileId, string, bool) {
	name, ok := resolver[id]

	return id, name, ok
}

func (resolver ownershipTestResolver) ForEachFile(callback func(core.FileId, string)) bool {
	panic("incremental ownership snapshots must not enumerate live files")
}

func (ownershipTestResolver) ScanFile(
	core.FileId,
	func(int, core.TickNumber, core.AuthorId),
) bool {
	panic("incremental ownership snapshots must not rescan live lines")
}

func ownershipChange(
	fileID core.FileId,
	delta int,
	previousAuthor core.AuthorId,
	currentAuthor core.AuthorId,
	tick core.TickNumber,
) core.LineHistoryChange {
	return core.LineHistoryChange{
		FileId:     fileID,
		CurrTick:   tick,
		PrevTick:   tick,
		CurrAuthor: currentAuthor,
		PrevAuthor: previousAuthor,
		Delta:      delta,
	}
}

func TestOwnershipSnapshotterMetaAndRegistration(t *testing.T) {
	snapshotter := &ownershipSnapshotter{}
	assert.Equal(t, "OwnershipSnapshot", snapshotter.Name())
	assert.Equal(t, []string{dependencyOwnershipSnapshot}, snapshotter.Provides())
	assert.ElementsMatch(t, []string{
		linehistory.DependencyLineHistory,
		items.DependencyTick,
	}, snapshotter.Requires())

	summoned := core.Registry.Summon(dependencyOwnershipSnapshot)
	require.Len(t, summoned, 1)
	assert.IsType(t, &ownershipSnapshotter{}, summoned[0])
}

func TestOwnershipAnalysesDeployOneSharedSnapshotter(t *testing.T) {
	pipeline := core.NewPipeline(test.Repository)
	pipeline.DeployItem(&BusFactorAnalysis{})
	afterBusFactor := pipeline.Len()

	pipeline.DeployItem(&OwnershipConcentrationAnalysis{})

	assert.Equal(t, afterBusFactor+1, pipeline.Len())
}

func consumeOwnershipAnalyses(
	t *testing.T,
	snapshotter *ownershipSnapshotter,
	busFactor *BusFactorAnalysis,
	concentration *OwnershipConcentrationAnalysis,
	tick int,
	resolver core.FileIdResolver,
	changes ...core.LineHistoryChange,
) {
	t.Helper()

	dependencies := map[string]any{
		linehistory.DependencyLineHistory: core.LineHistoryChanges{
			Changes:  changes,
			Resolver: resolver,
		},
		items.DependencyTick: tick,
	}

	update, err := snapshotter.Consume(dependencies)
	require.NoError(t, err)

	_, err = busFactor.Consume(update)
	require.NoError(t, err)

	_, err = concentration.Consume(update)
	require.NoError(t, err)
}

func TestOwnershipAnalysesSnapshotBeforeNextTick(t *testing.T) {
	resolver := ownershipTestResolver{
		1: "src/main.go",
		2: "docs/guide.md",
	}

	busFactor := &BusFactorAnalysis{Threshold: 0.8}
	concentration := &OwnershipConcentrationAnalysis{}
	snapshotter := &ownershipSnapshotter{}
	require.NoError(t, snapshotter.Initialize(test.Repository))
	require.NoError(t, busFactor.Initialize(test.Repository))
	require.NoError(t, concentration.Initialize(test.Repository))

	// Two commits in tick 0 establish 4 Alice lines and 1 Bob line.
	consumeOwnershipAnalyses(
		t, snapshotter, busFactor, concentration, 0, resolver,
		ownershipChange(1, 4, 0, 0, 0),
	)
	consumeOwnershipAnalyses(
		t, snapshotter, busFactor, concentration, 0, resolver,
		ownershipChange(2, 1, 1, 1, 0),
	)

	// The first tick-1 commit transfers two lines from Alice to Bob and replaces them with
	// three Bob lines. Tick 0 must be closed before these changes are applied.
	consumeOwnershipAnalyses(
		t, snapshotter, busFactor, concentration, 1, resolver,
		ownershipChange(1, -2, 0, 1, 1),
		ownershipChange(1, 3, 1, 1, 1),
	)

	require.Contains(t, busFactor.snapshots, 0)
	assert.Equal(t, int64(5), busFactor.snapshots[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, busFactor.snapshots[0].AuthorLines)
	assert.Equal(t, 1, busFactor.snapshots[0].BusFactor)

	require.Contains(t, concentration.snapshots, 0)
	assert.Equal(t, int64(5), concentration.snapshots[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, concentration.snapshots[0].AuthorLines)
	assert.InDelta(t, 0.3, concentration.snapshots[0].Gini, 1e-9)
	assert.InDelta(t, 0.68, concentration.snapshots[0].HHI, 1e-9)

	// A second commit in tick 1 grows the file. The immutable tick-0 snapshot must not move.
	consumeOwnershipAnalyses(
		t, snapshotter, busFactor, concentration, 1, resolver,
		ownershipChange(1, 1, 1, 1, 1),
	)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, busFactor.snapshots[0].AuthorLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, concentration.snapshots[0].AuthorLines)

	busResult := busFactor.Finalize().(BusFactorResult)
	concentrationResult := concentration.Finalize().(OwnershipConcentrationResult)

	require.Contains(t, busResult.Snapshots, 1)
	assert.Equal(t, int64(7), busResult.Snapshots[1].TotalLines)
	assert.Equal(t, map[int]int64{0: 2, 1: 5}, busResult.Snapshots[1].AuthorLines)
	assert.Equal(t, 2, busResult.Snapshots[1].BusFactor)
	assert.Equal(t, map[string]int{"src": 2, "docs": 1}, busResult.SubsystemBusFactor)

	require.Contains(t, concentrationResult.Snapshots, 1)
	assert.Equal(t, int64(7), concentrationResult.Snapshots[1].TotalLines)
	assert.Equal(t, map[int]int64{0: 2, 1: 5}, concentrationResult.Snapshots[1].AuthorLines)
	assert.InDelta(t, 3.0/14.0, concentrationResult.Snapshots[1].Gini, 1e-9)
	assert.InDelta(t, 29.0/49.0, concentrationResult.Snapshots[1].HHI, 1e-9)

	assert.InDelta(
		t,
		1.0/6.0,
		concentrationResult.SubsystemConcentration["src"].Gini,
		1e-9,
	)
	assert.InDelta(
		t,
		5.0/9.0,
		concentrationResult.SubsystemConcentration["src"].HHI,
		1e-9,
	)
	assert.InDelta(t, 0, concentrationResult.SubsystemConcentration["docs"].Gini, 1e-9)
	assert.InDelta(t, 1, concentrationResult.SubsystemConcentration["docs"].HHI, 1e-9)

	subsystems := snapshotter.ownership.subsystemOwnership()
	var subsystemTotal int64
	for _, authors := range subsystems {
		for _, lines := range authors {
			subsystemTotal += lines
		}
	}
	assert.Equal(t, busResult.Snapshots[1].TotalLines, subsystemTotal)
}

func TestOwnershipAnalysesPipelineSnapshotsUseLabeledTickState(t *testing.T) {
	repository, commits := newHotspotRiskPipelineFixture(t)
	pipeline := core.NewPipeline(repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	busFactor := pipeline.DeployItem(&BusFactorAnalysis{}).(*BusFactorAnalysis)
	concentration := pipeline.DeployItem(
		&OwnershipConcentrationAnalysis{},
	).(*OwnershipConcentrationAnalysis)

	require.NoError(t, pipeline.Initialize(map[string]any{
		core.ConfigPipelineCommits:          commits,
		items.ConfigTicksSinceStartTickSize: 24,
		ConfigBusFactorThreshold:            float32(0.8),
	}))

	results, err := pipeline.Run(commits)
	require.NoError(t, err)
	busResult, ok := results[busFactor].(BusFactorResult)
	require.True(t, ok)
	concentrationResult, ok := results[concentration].(OwnershipConcentrationResult)
	require.True(t, ok)

	// The first two real commits are both in tick 0. The tick-1 edit must not leak into this
	// snapshot: Alice owns 6 lines and Bob owns 3.
	require.Contains(t, busResult.Snapshots, 0)
	assert.Equal(t, int64(9), busResult.Snapshots[0].TotalLines)
	assert.ElementsMatch(t, []int64{6, 3}, ownershipLineCounts(busResult.Snapshots[0].AuthorLines))
	assert.Equal(t, 2, busResult.Snapshots[0].BusFactor)

	require.Contains(t, concentrationResult.Snapshots, 0)
	assert.Equal(t, int64(9), concentrationResult.Snapshots[0].TotalLines)
	assert.ElementsMatch(
		t,
		[]int64{6, 3},
		ownershipLineCounts(concentrationResult.Snapshots[0].AuthorLines),
	)
	assert.InDelta(t, 1.0/6.0, concentrationResult.Snapshots[0].Gini, 1e-9)
	assert.InDelta(t, 5.0/9.0, concentrationResult.Snapshots[0].HHI, 1e-9)

	// Tick 1 contains the ownership transfer and file growth. Tick 2 is the explicit final
	// snapshot after the later deletion.
	require.Contains(t, busResult.Snapshots, 1)
	assert.Equal(t, int64(10), busResult.Snapshots[1].TotalLines)
	assert.ElementsMatch(t, []int64{7, 3}, ownershipLineCounts(busResult.Snapshots[1].AuthorLines))
	require.Contains(t, busResult.Snapshots, 2)
	assert.Equal(t, int64(8), busResult.Snapshots[2].TotalLines)
	assert.ElementsMatch(t, []int64{5, 3}, ownershipLineCounts(busResult.Snapshots[2].AuthorLines))

	require.Contains(t, concentrationResult.Snapshots, 1)
	assert.Equal(t, int64(10), concentrationResult.Snapshots[1].TotalLines)
	assert.ElementsMatch(
		t,
		[]int64{7, 3},
		ownershipLineCounts(concentrationResult.Snapshots[1].AuthorLines),
	)
	require.Contains(t, concentrationResult.Snapshots, 2)
	assert.Equal(t, int64(8), concentrationResult.Snapshots[2].TotalLines)
	assert.ElementsMatch(
		t,
		[]int64{5, 3},
		ownershipLineCounts(concentrationResult.Snapshots[2].AuthorLines),
	)

	assert.Equal(t, busResult.Snapshots[2].BusFactor, busResult.SubsystemBusFactor["/"])
	assert.InDelta(
		t,
		concentrationResult.Snapshots[2].Gini,
		concentrationResult.SubsystemConcentration["/"].Gini,
		1e-9,
	)
	assert.InDelta(
		t,
		concentrationResult.Snapshots[2].HHI,
		concentrationResult.SubsystemConcentration["/"].HHI,
		1e-9,
	)
}

func ownershipLineCounts(authorLines map[int]int64) []int64 {
	counts := make([]int64, 0, len(authorLines))
	for _, lines := range authorLines {
		counts = append(counts, lines)
	}

	return counts
}

func TestOwnershipAnalysesEmptyAndSingleAuthorRepositories(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		busFactor := &BusFactorAnalysis{}
		concentration := &OwnershipConcentrationAnalysis{}
		require.NoError(t, busFactor.Initialize(test.Repository))
		require.NoError(t, concentration.Initialize(test.Repository))

		busResult := busFactor.Finalize().(BusFactorResult)
		concentrationResult := concentration.Finalize().(OwnershipConcentrationResult)

		assert.Empty(t, busResult.Snapshots)
		assert.Nil(t, busResult.SubsystemBusFactor)
		assert.Empty(t, concentrationResult.Snapshots)
		assert.Nil(t, concentrationResult.SubsystemConcentration)
	})

	t.Run("single author final tick", func(t *testing.T) {
		resolver := ownershipTestResolver{1: "main.go"}
		busFactor := &BusFactorAnalysis{Threshold: 0.8}
		concentration := &OwnershipConcentrationAnalysis{}
		snapshotter := &ownershipSnapshotter{}
		require.NoError(t, snapshotter.Initialize(test.Repository))
		require.NoError(t, busFactor.Initialize(test.Repository))
		require.NoError(t, concentration.Initialize(test.Repository))

		consumeOwnershipAnalyses(
			t, snapshotter, busFactor, concentration, 7, resolver,
			ownershipChange(1, 3, 0, 0, 7),
		)

		busResult := busFactor.Finalize().(BusFactorResult)
		concentrationResult := concentration.Finalize().(OwnershipConcentrationResult)

		require.Contains(t, busResult.Snapshots, 7)
		assert.Equal(t, 1, busResult.Snapshots[7].BusFactor)
		assert.Equal(t, int64(3), busResult.Snapshots[7].TotalLines)
		assert.Equal(t, 1, busResult.SubsystemBusFactor["/"])

		require.Contains(t, concentrationResult.Snapshots, 7)
		assert.Zero(t, concentrationResult.Snapshots[7].Gini)
		assert.InDelta(t, 1.0, concentrationResult.Snapshots[7].HHI, 1e-9)
		assert.Zero(t, concentrationResult.SubsystemConcentration["/"].Gini)
		assert.InDelta(t, 1.0, concentrationResult.SubsystemConcentration["/"].HHI, 1e-9)
	})
}

func TestOwnershipSnapshotAccumulatorRejectsUnderflow(t *testing.T) {
	accumulator := ownershipSnapshotAccumulator{}
	accumulator.reset()

	_, _, err := accumulator.consume(0, core.LineHistoryChanges{
		Changes: []core.LineHistoryChange{
			ownershipChange(1, -1, 0, 1, 0),
		},
	})

	require.ErrorIs(t, err, errOwnershipUnderflow)
}
