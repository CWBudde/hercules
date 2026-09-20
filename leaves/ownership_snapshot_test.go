package leaves

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/test"
)

// ownershipTestRun is one ownership run as a line-history tree reports it: the run starts at
// start and is owned by author since tick.
type ownershipTestRun struct {
	start  int
	author core.AuthorId
	tick   core.TickNumber
}

// ownershipTestFile is one live file of the fake resolver. ScanFile emits its runs in order and
// then the TreeEnd sentinel at length, exactly like File.ForEach does.
type ownershipTestFile struct {
	name   string
	length int
	runs   []ownershipTestRun
}

type ownershipTestResolver map[core.FileId]*ownershipTestFile

func (resolver ownershipTestResolver) NameOf(id core.FileId) string {
	if file := resolver[id]; file != nil {
		return file.name
	}

	return ""
}

func (resolver ownershipTestResolver) MergedWith(id core.FileId) (core.FileId, string, bool) {
	file, ok := resolver[id]
	if !ok {
		return id, "", false
	}

	return id, file.name, true
}

func (resolver ownershipTestResolver) ForEachFile(callback func(core.FileId, string)) bool {
	ids := make([]core.FileId, 0, len(resolver))
	for id := range resolver {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		callback(id, resolver[id].name)
	}

	return true
}

func (resolver ownershipTestResolver) ScanFile(
	id core.FileId,
	callback func(int, core.TickNumber, core.AuthorId),
) bool {
	file := resolver[id]
	if file == nil {
		return false
	}

	for _, run := range file.runs {
		callback(run.start, run.tick, run.author)
	}

	callback(file.length, linehistory.TreeMergeMark, -1)

	return true
}

// ownershipFile describes a live file by its length and ownership runs.
func ownershipFile(name string, length int, runs ...ownershipTestRun) *ownershipTestFile {
	return &ownershipTestFile{name: name, length: length, runs: runs}
}

// ownershipTouch is the only thing the snapshotter reads from a change: which file it touched.
func ownershipTouch(fileID core.FileId) core.LineHistoryChange {
	return core.LineHistoryChange{FileId: fileID, Delta: 1}
}

func ownershipChanges(files ...core.FileId) core.LineHistoryChanges {
	changes := make([]core.LineHistoryChange, len(files))
	for index, file := range files {
		changes[index] = ownershipTouch(file)
	}

	return core.LineHistoryChanges{Changes: changes}
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
	touched ...core.FileId,
) {
	t.Helper()

	changes := ownershipChanges(touched...)
	changes.Resolver = resolver

	dependencies := map[string]any{
		linehistory.DependencyLineHistory: changes,
		items.DependencyTick:              tick,
	}

	update, err := snapshotter.Consume(dependencies)
	require.NoError(t, err)

	_, err = busFactor.Consume(update)
	require.NoError(t, err)

	_, err = concentration.Consume(update)
	require.NoError(t, err)
}

func TestOwnershipAnalysesSnapshotBeforeNextTick(t *testing.T) {
	const (
		mainFile  core.FileId = 1
		guideFile core.FileId = 2
	)

	resolver := ownershipTestResolver{}

	busFactor := &BusFactorAnalysis{Threshold: 0.8}
	concentration := &OwnershipConcentrationAnalysis{}
	snapshotter := &ownershipSnapshotter{}
	require.NoError(t, snapshotter.Initialize(test.Repository))
	require.NoError(t, busFactor.Initialize(test.Repository))
	require.NoError(t, concentration.Initialize(test.Repository))

	// Two commits in tick 0 establish 4 Alice lines and 1 Bob line.
	resolver[mainFile] = ownershipFile("src/main.go", 4, ownershipTestRun{0, 0, 0})
	consumeOwnershipAnalyses(t, snapshotter, busFactor, concentration, 0, resolver, mainFile)

	resolver[guideFile] = ownershipFile("docs/guide.md", 1, ownershipTestRun{0, 1, 0})
	consumeOwnershipAnalyses(t, snapshotter, busFactor, concentration, 0, resolver, guideFile)

	// The first tick-1 commit replaces two of Alice's lines with three Bob lines. Tick 0 must be
	// closed before the tree is re-read.
	resolver[mainFile] = ownershipFile(
		"src/main.go", 5, ownershipTestRun{0, 0, 0}, ownershipTestRun{2, 1, 1},
	)
	consumeOwnershipAnalyses(t, snapshotter, busFactor, concentration, 1, resolver, mainFile)

	closed := snapshotter.ownership.closedSnapshots()
	require.Contains(t, closed, 0)
	assert.Equal(t, int64(5), closed[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, closed[0].AuthorLines)

	// A second commit in tick 1 grows the file. The immutable tick-0 snapshot must not move.
	resolver[mainFile] = ownershipFile(
		"src/main.go", 6, ownershipTestRun{0, 0, 0}, ownershipTestRun{2, 1, 1},
	)
	consumeOwnershipAnalyses(t, snapshotter, busFactor, concentration, 1, resolver, mainFile)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, closed[0].AuthorLines)

	busResult := busFactor.Finalize().(BusFactorResult)
	concentrationResult := concentration.Finalize().(OwnershipConcentrationResult)

	require.Contains(t, busResult.Snapshots, 0)
	assert.Equal(t, int64(5), busResult.Snapshots[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, busResult.Snapshots[0].AuthorLines)
	assert.Equal(t, 1, busResult.Snapshots[0].BusFactor)

	require.Contains(t, concentrationResult.Snapshots, 0)
	assert.Equal(t, int64(5), concentrationResult.Snapshots[0].TotalLines)
	assert.Equal(t, map[int]int64{0: 4, 1: 1}, concentrationResult.Snapshots[0].AuthorLines)
	assert.InDelta(t, 0.3, concentrationResult.Snapshots[0].Gini, 1e-9)
	assert.InDelta(t, 0.68, concentrationResult.Snapshots[0].HHI, 1e-9)

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
		resolver := ownershipTestResolver{1: ownershipFile("main.go", 3, ownershipTestRun{0, 0, 7})}
		busFactor := &BusFactorAnalysis{Threshold: 0.8}
		concentration := &OwnershipConcentrationAnalysis{}
		snapshotter := &ownershipSnapshotter{}
		require.NoError(t, snapshotter.Initialize(test.Repository))
		require.NoError(t, busFactor.Initialize(test.Repository))
		require.NoError(t, concentration.Initialize(test.Repository))

		consumeOwnershipAnalyses(t, snapshotter, busFactor, concentration, 7, resolver, 1)

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

// TestScanFileOwnershipCountsRunsByLength pins the reading of ScanFile's output: callbacks are
// run boundaries, so a run's length is the distance to the next one and belongs to the previous
// callback's author, and the unmatched identity as well as the end sentinel own nothing.
func TestScanFileOwnershipCountsRunsByLength(t *testing.T) {
	resolver := ownershipTestResolver{1: ownershipFile(
		"main.go", 9,
		ownershipTestRun{0, 0, 0},
		ownershipTestRun{3, 1, 1},
		ownershipTestRun{5, core.AuthorMissing, 2},
	)}

	lines, live, err := scanFileOwnership(resolver, 1)
	require.NoError(t, err)
	assert.True(t, live)
	assert.Equal(t, map[int]int64{0: 3, 1: 2}, lines)

	_, live, err = scanFileOwnership(resolver, 2)
	require.NoError(t, err)
	assert.False(t, live, "an unknown id is a deleted file")
}

// TestScanFileOwnershipRejectsUnresolvedMergeLines: a line still carrying TreeMergeMark decodes to
// author 0, so counting it would credit somebody at random. The scan reports it instead.
func TestScanFileOwnershipRejectsUnresolvedMergeLines(t *testing.T) {
	resolver := ownershipTestResolver{1: ownershipFile(
		"main.go", 4, ownershipTestRun{0, 0, linehistory.TreeMergeMark},
	)}

	_, _, err := scanFileOwnership(resolver, 1)
	require.ErrorIs(t, err, errOwnershipUnresolvedMerge)

	snapshotter := &ownershipSnapshotter{}
	require.NoError(t, snapshotter.Initialize(test.Repository))

	changes := ownershipChanges(1)
	changes.Resolver = resolver
	_, err = snapshotter.Consume(map[string]any{
		linehistory.DependencyLineHistory: changes,
		items.DependencyTick:              0,
	})
	require.ErrorIs(t, err, errOwnershipUnresolvedMerge)
}

// TestOwnershipSnapshotterForkIsolatesBranches: a fork gets its own table, so a branch which
// rewrites a file does not disturb its sibling's counts, and the closed snapshots are shared.
func TestOwnershipSnapshotterForkIsolatesBranches(t *testing.T) {
	resolver := ownershipTestResolver{1: ownershipFile("main.go", 10, ownershipTestRun{0, 0, 0})}
	origin := &ownershipSnapshotter{}
	require.NoError(t, origin.Initialize(test.Repository))

	changes := ownershipChanges(1)
	changes.Resolver = resolver
	require.NoError(t, origin.ownership.consume(0, changes))

	forks := origin.Fork(2)
	require.Len(t, forks, 2)
	left, ok := forks[0].(*ownershipSnapshotter)
	require.True(t, ok)
	assert.NotSame(t, origin, left)

	// The left branch removes half of the file in tick 1; the origin does not see it.
	branchResolver := ownershipTestResolver{1: ownershipFile("main.go", 5, ownershipTestRun{0, 0, 0})}
	branchChanges := ownershipChanges(1)
	branchChanges.Resolver = branchResolver
	require.NoError(t, left.ownership.consume(1, branchChanges))

	_, leftFinal := left.ownership.finalSnapshot()
	_, originFinal := origin.ownership.finalSnapshot()
	assert.Equal(t, int64(5), leftFinal.TotalLines)
	assert.Equal(t, int64(10), originFinal.TotalLines)
	assert.Equal(t, map[int]int64{0: 10}, origin.ownership.fileLines[1])

	require.Contains(t, left.ownership.closedSnapshots(), 0)
	assert.Equal(t, int64(10), left.ownership.closedSnapshots()[0].TotalLines)
	assert.NotContains(t, origin.ownership.closedSnapshots(), 0)
}

// TestOwnershipSnapshotterMergeRebuildsFromTrees: after LineHistoryAnalyser.Merge() the trees
// are the merged state and the table is not, so Merge() rebuilds the receiver and its siblings
// from scratch - re-keyed ids leave, new files arrive.
func TestOwnershipSnapshotterMergeRebuildsFromTrees(t *testing.T) {
	before := ownershipTestResolver{
		1: ownershipFile("a.go", 10, ownershipTestRun{0, 0, 0}),
		2: ownershipFile("b.go", 4, ownershipTestRun{0, 1, 0}),
	}
	receiver := &ownershipSnapshotter{}
	require.NoError(t, receiver.Initialize(test.Repository))

	changes := ownershipChanges(1, 2)
	changes.Resolver = before
	require.NoError(t, receiver.ownership.consume(0, changes))

	sibling, ok := receiver.Fork(1)[0].(*ownershipSnapshotter)
	require.True(t, ok)

	// The merge re-keyed b.go onto id 3, resolved two more lines of a.go to Bob and created c.go.
	// Both branches now see the same merged trees, as synchronizeLineHistoryBranch guarantees.
	after := ownershipTestResolver{
		1: ownershipFile("a.go", 12, ownershipTestRun{0, 0, 0}, ownershipTestRun{10, 1, 3}),
		3: ownershipFile("b.go", 4, ownershipTestRun{0, 1, 0}),
		4: ownershipFile("c.go", 2, ownershipTestRun{0, 2, 3}),
	}
	receiver.ownership.resolver = after
	sibling.ownership.resolver = after

	receiver.Merge([]core.PipelineItem{sibling})

	for _, branch := range []*ownershipSnapshotter{receiver, sibling} {
		_, final := branch.ownership.finalSnapshot()
		require.NotNil(t, final)
		assert.Equal(t, int64(18), final.TotalLines)
		assert.Equal(t, map[int]int64{0: 10, 1: 6, 2: 2}, final.AuthorLines)
		assert.NotContains(t, branch.ownership.fileLines, core.FileId(2))
		assert.Equal(t, map[string]map[int]int64{
			"/": {0: 10, 1: 6, 2: 2},
		}, branch.ownership.subsystemOwnership())
	}
}

// TestOwnershipLeavesIgnoreReplicaState: a merge commit is replayed once per parent, and only the
// first sighting runs on the branch which survives the merge. The leaves must keep that one.
func TestOwnershipLeavesIgnoreReplicaState(t *testing.T) {
	authoritative := &ownershipSnapshotAccumulator{}
	replica := &ownershipSnapshotAccumulator{}
	busFactor := &BusFactorAnalysis{}
	concentration := &OwnershipConcentrationAnalysis{}
	require.NoError(t, busFactor.Initialize(test.Repository))
	require.NoError(t, concentration.Initialize(test.Repository))

	_, err := busFactor.Consume(map[string]any{
		dependencyOwnershipSnapshot: ownershipSnapshotUpdate{State: authoritative},
	})
	require.NoError(t, err)
	_, err = concentration.Consume(map[string]any{
		dependencyOwnershipSnapshot: ownershipSnapshotUpdate{State: authoritative},
	})
	require.NoError(t, err)

	replicaDeps := map[string]any{
		dependencyOwnershipSnapshot:   ownershipSnapshotUpdate{State: replica},
		core.DependencyIsMergeReplica: true,
	}
	_, err = busFactor.Consume(replicaDeps)
	require.NoError(t, err)
	_, err = concentration.Consume(replicaDeps)
	require.NoError(t, err)

	assert.Same(t, authoritative, busFactor.ownership)
	assert.Same(t, authoritative, concentration.ownership)
}
