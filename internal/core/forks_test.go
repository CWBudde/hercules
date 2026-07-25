package core

import (
	"bytes"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/test"
)

type testForkPipelineItem struct {
	NoopMerger

	Mutable   map[int]bool
	Immutable string
}

func (item *testForkPipelineItem) Name() string {
	return testItemName
}

func (item *testForkPipelineItem) Provides() []string {
	return []string{testFeatureName}
}

func (item *testForkPipelineItem) Requires() []string {
	return []string{}
}

func (item *testForkPipelineItem) Configure(facts map[string]any) error {
	return nil
}

func (item *testForkPipelineItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *testForkPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	return nil
}

func (item *testForkPipelineItem) Flag() string {
	return "mytest"
}

func (item *testForkPipelineItem) Features() []string {
	return nil
}

func (item *testForkPipelineItem) Initialize(repository *git.Repository) error {
	item.Mutable = map[int]bool{}
	return nil
}

func (item *testForkPipelineItem) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{testFeatureName: "foo"}, nil
}

func (item *testForkPipelineItem) Fork(n int) []PipelineItem {
	return ForkCopyPipelineItem(item, n)
}

func TestForkCopyPipelineItem(t *testing.T) {
	origin := &testForkPipelineItem{}
	require.NoError(t, origin.Initialize(nil))
	origin.Mutable[2] = true
	origin.Immutable = "before"
	clone, ok := origin.Fork(1)[0].(*testForkPipelineItem)
	require.True(t, ok)
	origin.Immutable = "after"
	origin.Mutable[1] = true
	assert.True(t, clone.Mutable[1])
	assert.True(t, clone.Mutable[2])
	assert.Equal(t, "before", clone.Immutable)
}

func TestInsertHibernateBoot(t *testing.T) {
	plan := []runAction{
		{runActionEmerge, nil, nil, []int{1, 2}},
		{runActionEmerge, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{1}},
		{runActionFork, nil, nil, []int{2, 4}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionDelete, nil, nil, []int{1}},
		{runActionMerge, nil, nil, []int{2, 4}},
	}
	plan = insertHibernateBoot(plan, 2)
	assert.Equal(t, []runAction{
		{runActionEmerge, nil, nil, []int{1, 2}},
		{runActionHibernate, nil, nil, []int{1, 2}},
		{runActionEmerge, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionBoot, nil, nil, []int{1}},
		{runActionCommit, nil, nil, []int{1}},
		{runActionBoot, nil, nil, []int{2}},
		{runActionFork, nil, nil, []int{2, 4}},
		{runActionHibernate, nil, nil, []int{2, 4}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionCommit, nil, nil, []int{3}},
		{runActionDelete, nil, nil, []int{1}},
		{runActionBoot, nil, nil, []int{2, 4}},
		{runActionMerge, nil, nil, []int{2, 4}},
	}, plan)
}

func TestRunActionString(t *testing.T) {
	c, _ := test.FixtureRepository().CommitObject(plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1"))
	ra := runAction{runActionCommit, c, nil, nil}
	assert.Equal(t, "cce947b", ra.String())
	ra = runAction{runActionFork, nil, nil, []int{1, 2, 5}}
	assert.Equal(t, "fork^3", ra.String())
	ra = runAction{runActionMerge, nil, nil, []int{1, 2, 5}}
	assert.Equal(t, "merge^3", ra.String())
	ra = runAction{runActionEmerge, nil, nil, nil}
	assert.Equal(t, runActionEmergeName, ra.String())
	ra = runAction{runActionDelete, nil, nil, nil}
	assert.Equal(t, "delete", ra.String())
	ra = runAction{runActionHibernate, nil, nil, nil}
	assert.Equal(t, "hibernate", ra.String())
	ra = runAction{runActionBoot, nil, nil, nil}
	assert.Equal(t, "boot", ra.String())
}

// makeTestCommit creates a fake commit with a padded hash and optional parent hashes.
func makeTestCommit(hash string, parents ...string) *object.Commit {
	pad := func(s string) plumbing.Hash {
		for len(s) < 40 {
			s += "0"
		}
		return plumbing.NewHash(s)
	}
	parentHashes := make([]plumbing.Hash, len(parents))
	for i, p := range parents {
		parentHashes[i] = pad(p)
	}
	return &object.Commit{
		Hash:         pad(hash),
		ParentHashes: parentHashes,
	}
}

func TestRunActionStringUnknown(t *testing.T) {
	ra := runAction{Action: 999, Items: []int{1}}
	assert.Empty(t, ra.String())
}

func TestOneShotMergeProcessor(t *testing.T) {
	t.Run("Initialize", func(t *testing.T) {
		proc := &OneShotMergeProcessor{}
		proc.Initialize()
		assert.NotNil(t, proc.merges)
		assert.Empty(t, proc.merges)
	})

	t.Run("regular commit always consumed", func(t *testing.T) {
		proc := &OneShotMergeProcessor{}
		proc.Initialize()
		commit := makeTestCommit("aa", "bb") // 1 parent = regular
		deps := map[string]any{DependencyCommit: commit}
		assert.True(t, proc.ShouldConsumeCommit(deps))
		assert.True(t, proc.ShouldConsumeCommit(deps))
	})

	t.Run("root commit always consumed", func(t *testing.T) {
		proc := &OneShotMergeProcessor{}
		proc.Initialize()
		commit := makeTestCommit("aa") // 0 parents = root
		deps := map[string]any{DependencyCommit: commit}
		assert.True(t, proc.ShouldConsumeCommit(deps))
		assert.True(t, proc.ShouldConsumeCommit(deps))
	})

	t.Run("merge commit consumed once", func(t *testing.T) {
		proc := &OneShotMergeProcessor{}
		proc.Initialize()
		commit := makeTestCommit("aa", "bb", "cc") // 2 parents = merge
		deps := map[string]any{DependencyCommit: commit}
		assert.True(t, proc.ShouldConsumeCommit(deps))
		assert.False(t, proc.ShouldConsumeCommit(deps))
	})

	t.Run("different merges consumed independently", func(t *testing.T) {
		proc := &OneShotMergeProcessor{}
		proc.Initialize()
		merge1 := makeTestCommit("aa", "bb", "cc")
		merge2 := makeTestCommit("dd", "ee", "ff")
		deps1 := map[string]any{DependencyCommit: merge1}
		deps2 := map[string]any{DependencyCommit: merge2}
		assert.True(t, proc.ShouldConsumeCommit(deps1))
		assert.True(t, proc.ShouldConsumeCommit(deps2))
		assert.False(t, proc.ShouldConsumeCommit(deps1))
		assert.False(t, proc.ShouldConsumeCommit(deps2))
	})
}

func TestNoopMerger(t *testing.T) {
	m := &NoopMerger{}
	m.Merge(nil)
	m.Merge([]PipelineItem{})
	m.Merge([]PipelineItem{&testForkPipelineItem{}})
}

func TestForkSamePipelineItem(t *testing.T) {
	origin := &testForkPipelineItem{Immutable: testFeatureName}

	t.Run("creates n references to same origin", func(t *testing.T) {
		clones := ForkSamePipelineItem(origin, 3)
		assert.Len(t, clones, 3)
		for _, c := range clones {
			assert.Same(t, origin, c)
		}
	})

	t.Run("zero clones", func(t *testing.T) {
		clones := ForkSamePipelineItem(origin, 0)
		assert.Empty(t, clones)
	})

	t.Run("single clone", func(t *testing.T) {
		clones := ForkSamePipelineItem(origin, 1)
		assert.Len(t, clones, 1)
		assert.Same(t, origin, clones[0])
	})
}

func TestForkCopyPipelineItemMultiple(t *testing.T) {
	origin := &testForkPipelineItem{}
	require.NoError(t, origin.Initialize(nil))
	origin.Immutable = "original"
	clones := ForkCopyPipelineItem(origin, 3)
	assert.Len(t, clones, 3)
	for i, c := range clones {
		clone, ok := c.(*testForkPipelineItem)
		require.True(t, ok)
		assert.Equal(t, "original", clone.Immutable)
		// each clone is a different pointer
		for j, other := range clones {
			if i != j {
				assert.NotSame(t, c, other)
			}
		}
	}
}

func TestForkCopyPipelineItemZero(t *testing.T) {
	origin := &testForkPipelineItem{}
	clones := ForkCopyPipelineItem(origin, 0)
	assert.Empty(t, clones)
}

func TestGetCommitParents(t *testing.T) {
	t.Run("root commit", func(t *testing.T) {
		commit := makeTestCommit("aa")
		parents := getCommitParents(commit)
		assert.Empty(t, parents)
	})

	t.Run("single parent", func(t *testing.T) {
		commit := makeTestCommit("aa", "bb")
		parents := getCommitParents(commit)
		assert.Len(t, parents, 1)
	})

	t.Run("multiple unique parents", func(t *testing.T) {
		commit := makeTestCommit("aa", "bb", "cc", "dd")
		parents := getCommitParents(commit)
		assert.Len(t, parents, 3)
	})

	t.Run("duplicate parents deduplicated", func(t *testing.T) {
		commit := makeTestCommit("aa", "bb", "bb")
		parents := getCommitParents(commit)
		assert.Len(t, parents, 1)
	})

	t.Run("mixed duplicates among unique", func(t *testing.T) {
		commit := makeTestCommit("aa", "bb", "cc", "bb")
		parents := getCommitParents(commit)
		assert.Len(t, parents, 2)
	})
}

func TestBuildDag(t *testing.T) {
	t.Run("linear chain", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "bb")

		hashes, dag := buildDag([]*object.Commit{a, b, c})
		assert.Len(t, hashes, 3)
		// A -> B
		assert.Len(t, dag[a.Hash], 1)
		assert.Equal(t, b.Hash, dag[a.Hash][0].Hash)
		// B -> C
		assert.Len(t, dag[b.Hash], 1)
		assert.Equal(t, c.Hash, dag[b.Hash][0].Hash)
		// C has no children
		assert.Empty(t, dag[c.Hash])
	})

	t.Run("fork", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "aa")

		_, dag := buildDag([]*object.Commit{a, b, c})
		assert.Len(t, dag[a.Hash], 2)
		assert.Empty(t, dag[b.Hash])
		assert.Empty(t, dag[c.Hash])
	})

	t.Run("merge", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb")
		c := makeTestCommit("cc", "aa", "bb")

		_, dag := buildDag([]*object.Commit{a, b, c})
		assert.Len(t, dag[a.Hash], 1)
		assert.Equal(t, c.Hash, dag[a.Hash][0].Hash)
		assert.Len(t, dag[b.Hash], 1)
		assert.Equal(t, c.Hash, dag[b.Hash][0].Hash)
	})

	t.Run("parent not in commit list ignored", func(t *testing.T) {
		// b's parent "xx" is not in the commit list
		b := makeTestCommit("bb", "xx")

		hashes, dag := buildDag([]*object.Commit{b})
		assert.Len(t, hashes, 1)
		assert.Empty(t, dag[b.Hash])
	})

	t.Run("duplicate parents produce single edge", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := &object.Commit{
			Hash: plumbing.NewHash("bb00000000000000000000000000000000000000"),
			ParentHashes: []plumbing.Hash{
				plumbing.NewHash("aa00000000000000000000000000000000000000"),
				plumbing.NewHash("aa00000000000000000000000000000000000000"),
			},
		}

		_, dag := buildDag([]*object.Commit{a, b})
		assert.Len(t, dag[a.Hash], 1)
	})
}

func TestCommitComponents(t *testing.T) {
	t.Run("single component unchanged", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "bb")

		hashes, dag := buildDag([]*object.Commit{a, b, c})
		components := commitComponents(hashes, dag)
		require.Len(t, components, 1)
		assert.Equal(t, []plumbing.Hash{a.Hash}, components[0].Roots)
		assert.Equal(t, 3, components[0].Size)
	})

	t.Run("two components are reported in root order", func(t *testing.T) {
		// Component 1: a1 -> b1 -> c1 (3 nodes)
		a1 := makeTestCommit("a1")
		b1 := makeTestCommit("b1", "a1")
		c1 := makeTestCommit("c1", "b1")
		// Component 2: d1 -> e1 (2 nodes)
		d1 := makeTestCommit("d1")
		e1 := makeTestCommit("e1", "d1")

		hashes, dag := buildDag([]*object.Commit{a1, b1, c1, d1, e1})
		components := commitComponents(hashes, dag)
		require.Len(t, components, 2)
		assert.Equal(t, CommitComponent{Roots: []plumbing.Hash{a1.Hash}, Size: 3}, components[0])
		assert.Equal(t, CommitComponent{Roots: []plumbing.Hash{d1.Hash}, Size: 2}, components[1])
	})

	t.Run("empty dag", func(t *testing.T) {
		hashes := map[string]*object.Commit{}
		dag := map[plumbing.Hash][]*object.Commit{}
		assert.Empty(t, commitComponents(hashes, dag))
	})
}

func TestBuildParents(t *testing.T) {
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")

	_, dag := buildDag([]*object.Commit{a, b, c})
	parents := buildParents(dag)

	assert.Empty(t, parents[a.Hash])
	assert.True(t, parents[b.Hash][a.Hash])
	assert.True(t, parents[c.Hash][a.Hash])
}

func TestGetMasterBranch(t *testing.T) {
	t.Run("returns smallest key", func(t *testing.T) {
		item1 := []PipelineItem{&testForkPipelineItem{Immutable: "a"}}
		item2 := []PipelineItem{&testForkPipelineItem{Immutable: "b"}}
		item3 := []PipelineItem{&testForkPipelineItem{Immutable: "c"}}

		branches := map[int][]PipelineItem{
			5: item1,
			2: item2,
			9: item3,
		}
		result := getMasterBranch(branches)
		assert.Equal(t, item2, result)
	})

	t.Run("single branch", func(t *testing.T) {
		item := []PipelineItem{&testForkPipelineItem{}}
		branches := map[int][]PipelineItem{1: item}
		result := getMasterBranch(branches)
		assert.Equal(t, item, result)
	})

	t.Run("empty map", func(t *testing.T) {
		result := getMasterBranch(map[int][]PipelineItem{})
		assert.Nil(t, result)
	})
}

func TestMergeDagSequences(t *testing.T) {
	t.Run("linear chain merged into one sequence", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "bb")

		hashes, dag := buildDag([]*object.Commit{a, b, c})
		mergedDag, mergedSeq := mergeDag(hashes, dag)

		assert.Len(t, mergedDag, 1)
		assert.Len(t, mergedSeq, 1)
		for _, seq := range mergedSeq {
			assert.Len(t, seq, 3)
		}
	})

	t.Run("fork keeps separate nodes", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "aa")

		hashes, dag := buildDag([]*object.Commit{a, b, c})
		mergedDag, mergedSeq := mergeDag(hashes, dag)

		assert.Len(t, mergedDag, 3)
		assert.Len(t, mergedSeq, 3)
	})

	t.Run("diamond pattern", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "aa")
		d := makeTestCommit("dd", "bb", "cc")

		hashes, dag := buildDag([]*object.Commit{a, b, c, d})
		mergedDag, mergedSeq := mergeDag(hashes, dag)

		// 4 separate nodes since no linear sequences can be formed
		assert.Len(t, mergedDag, 4)
		assert.Len(t, mergedSeq, 4)
	})

	t.Run("partial linear chain with fork", func(t *testing.T) {
		// a -> b -> c, b -> d (fork at b)
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "bb")
		d := makeTestCommit("dd", "bb")

		hashes, dag := buildDag([]*object.Commit{a, b, c, d})
		mergedDag, mergedSeq := mergeDag(hashes, dag)

		// a-b can be merged, c and d are separate
		// But b has 2 children so a-b can't be merged.
		// Walking up from b yields a; walking down yields [a, b].
		// c and d remain separate because b has two children.
		assert.Len(t, mergedDag, 3) // [a,b], c, d
		// The merged sequence starting at a should contain [a, b]
		if seq, ok := mergedSeq[a.Hash]; ok {
			assert.Len(t, seq, 2)
		}
	})
}

func TestBindOrderNodes(t *testing.T) {
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "bb")

	hashes, dag := buildDag([]*object.Commit{a, b, c})
	mergedDag, _ := mergeDag(hashes, dag)
	orderNodes := bindOrderNodes(mergedDag)

	order := orderNodes(false, false)
	assert.NotEmpty(t, order)

	reverseOrder := orderNodes(true, false)
	assert.NotEmpty(t, reverseOrder)
	// reversed order should be the reverse
	assert.Len(t, reverseOrder, len(order))
	for i := range order {
		assert.Equal(t, order[i], reverseOrder[len(reverseOrder)-1-i])
	}
}

func TestCloneItems(t *testing.T) {
	item1 := &testPipelineItem{}
	require.NoError(t, item1.Initialize(nil))
	item2 := &testPipelineItem{}
	require.NoError(t, item2.Initialize(nil))

	origin := []PipelineItem{item1, item2}
	clones := cloneItems(origin, 3)

	assert.Len(t, clones, 3)
	for _, clone := range clones {
		assert.Len(t, clone, 2)
	}
	assert.True(t, item1.Forked)
	assert.True(t, item2.Forked)
}

func TestMergeItems(t *testing.T) {
	item1 := &testPipelineItem{}
	require.NoError(t, item1.Initialize(nil))
	item2 := &testPipelineItem{}
	require.NoError(t, item2.Initialize(nil))

	item3 := &testPipelineItem{}
	require.NoError(t, item3.Initialize(nil))
	item4 := &testPipelineItem{}
	require.NoError(t, item4.Initialize(nil))

	branches := [][]PipelineItem{
		{item1, item2},
		{item3, item4},
	}

	mergeItems(branches)
	assert.True(t, *item1.Merged)
	assert.True(t, *item2.Merged)
}

func TestCollectGarbage(t *testing.T) {
	t.Run("no garbage returns original plan", func(t *testing.T) {
		c1 := makeTestCommit("aa")
		plan := []runAction{
			{Action: runActionEmerge, Items: []int{1}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
		}
		result := collectGarbage(plan)
		assert.Equal(t, plan, result)
	})

	t.Run("inserts delete for unused branches", func(t *testing.T) {
		c1 := makeTestCommit("aa")
		c2 := makeTestCommit("bb")
		plan := []runAction{
			{Action: runActionEmerge, Items: []int{1}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
			{Action: runActionEmerge, Items: []int{2}},
			{Action: runActionCommit, Commit: c2, Items: []int{2}},
		}
		result := collectGarbage(plan)
		deleteFound := false
		for _, a := range result {
			if a.Action == runActionDelete && a.Items[0] == 1 {
				deleteFound = true
			}
		}
		assert.True(t, deleteFound, "expected delete action for branch 1")
	})

	t.Run("empty plan", func(t *testing.T) {
		result := collectGarbage([]runAction{})
		assert.Empty(t, result)
	})

	t.Run("fork and merge garbage", func(t *testing.T) {
		c1 := makeTestCommit("aa")
		c2 := makeTestCommit("bb")
		plan := []runAction{
			{Action: runActionEmerge, Items: []int{1}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
			{Action: runActionFork, Items: []int{1, 2}},
			{Action: runActionCommit, Commit: c2, Items: []int{2}},
			{Action: runActionMerge, Items: []int{1, 2}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
		}
		result := collectGarbage(plan)
		// Branch 2 last used at merge (index 4), not at end (index 5)
		deleteFound := false
		for _, a := range result {
			if a.Action == runActionDelete && a.Items[0] == 2 {
				deleteFound = true
			}
		}
		assert.True(t, deleteFound, "expected delete action for branch 2")
	})
}

func TestTracebackMerges(t *testing.T) {
	t.Run("sets NextMerge on commits", func(t *testing.T) {
		mergeCommit := makeTestCommit("mm", "aa", "bb")
		c1 := makeTestCommit("c1")
		c2 := makeTestCommit("c2")
		c3 := makeTestCommit("c3")

		plan := []runAction{
			{Action: runActionEmerge, Items: []int{1}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
			{Action: runActionFork, Items: []int{1, 2}},
			{Action: runActionCommit, Commit: c2, Items: []int{2}},
			{Action: runActionCommit, Commit: c3, Items: []int{1}},
			{Action: runActionMerge, Commit: mergeCommit, Items: []int{1, 2}},
		}

		count := tracebackMerges(plan)
		assert.Equal(t, 1, count)
		assert.Equal(t, mergeCommit, plan[1].NextMerge)
		assert.Equal(t, mergeCommit, plan[3].NextMerge)
		assert.Equal(t, mergeCommit, plan[4].NextMerge)
	})

	t.Run("nil merge commit not counted", func(t *testing.T) {
		plan := []runAction{
			{Action: runActionMerge, Commit: nil, Items: []int{1, 2}},
		}
		count := tracebackMerges(plan)
		assert.Equal(t, 0, count)
	})

	t.Run("emerge clears tracked merges for earlier commits", func(t *testing.T) {
		mergeCommit := makeTestCommit("mm", "aa", "bb")
		c0 := makeTestCommit("c0")
		c1 := makeTestCommit("c1")

		// Synthetic plan: c0 is before the emerge on the same branch.
		// The backward walk should clear branch 1 at the emerge,
		// so c0 (before emerge) gets no NextMerge.
		plan := []runAction{
			{Action: runActionCommit, Commit: c0, Items: []int{1}},
			{Action: runActionEmerge, Items: []int{1}},
			{Action: runActionCommit, Commit: c1, Items: []int{1}},
			{Action: runActionMerge, Commit: mergeCommit, Items: []int{1, 2}},
		}

		tracebackMerges(plan)
		assert.Nil(t, plan[0].NextMerge)                // before emerge: cleared
		assert.Equal(t, mergeCommit, plan[2].NextMerge) // after emerge: set
	})

	t.Run("empty plan", func(t *testing.T) {
		count := tracebackMerges([]runAction{})
		assert.Equal(t, 0, count)
	})
}

func TestPrintAction(t *testing.T) {
	var buf bytes.Buffer

	commit, _ := test.FixtureRepository().CommitObject(plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1"))

	tests := []struct {
		name     string
		action   runAction
		contains string
	}{
		{"commit", runAction{Action: runActionCommit, Commit: commit, Items: []int{1}}, "C 1"},
		{"fork", runAction{Action: runActionFork, Items: []int{1, 2}}, "F"},
		{"merge", runAction{Action: runActionMerge, Commit: commit, Items: []int{1, 2}}, "M"},
		{runActionEmergeName, runAction{Action: runActionEmerge, Items: []int{1}}, "E"},
		{"delete", runAction{Action: runActionDelete, Items: []int{1}}, "D"},
		{"hibernate", runAction{Action: runActionHibernate, Items: []int{1}}, "H"},
		{"boot", runAction{Action: runActionBoot, Items: []int{1}}, "B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			printAction(&buf, tt.action)
			assert.Contains(t, buf.String(), tt.contains)
		})
	}
}

func TestPrepareRunPlan(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		plan, mergeCount, err := prepareRunPlan(nil, 0, false)
		require.ErrorIs(t, err, ErrNoCommits)
		assert.Nil(t, plan)
		assert.Zero(t, mergeCount)
	})

	t.Run("nil commit", func(t *testing.T) {
		_, _, err := prepareRunPlan([]*object.Commit{nil}, 0, false)
		require.ErrorIs(t, err, ErrInvalidCommit)
	})

	t.Run("duplicate hash", func(t *testing.T) {
		a := makeTestCommit("aa")
		_, _, err := prepareRunPlan([]*object.Commit{a, a}, 0, false)
		require.ErrorIs(t, err, ErrDuplicateCommits)

		var duplicate *DuplicateCommitError
		require.ErrorAs(t, err, &duplicate)
		assert.Equal(t, a.Hash, duplicate.Hash)
		assert.Equal(t, 0, duplicate.FirstPosition)
		assert.Equal(t, 1, duplicate.SecondPosition)
	})

	t.Run("disconnected input is deterministic", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc")
		d := makeTestCommit("dd", "cc")
		e := makeTestCommit("ee", "dd")

		_, _, firstErr := prepareRunPlan([]*object.Commit{e, a, d, b, c}, 0, false)
		_, _, secondErr := prepareRunPlan([]*object.Commit{c, b, d, a, e}, 0, false)
		require.ErrorIs(t, firstErr, ErrDisconnectedCommits)
		require.EqualError(t, secondErr, firstErr.Error())

		var disconnected *DisconnectedCommitsError
		require.ErrorAs(t, firstErr, &disconnected)
		require.Equal(t, []CommitComponent{
			{Roots: []plumbing.Hash{a.Hash}, Size: 2},
			{Roots: []plumbing.Hash{c.Hash}, Size: 3},
		}, disconnected.Components)
	})

	t.Run("linear commits", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "bb")

		plan, mergeCount, err := prepareRunPlan([]*object.Commit{a, b, c}, 0, false)
		require.NoError(t, err)
		assert.NotEmpty(t, plan)
		assert.Equal(t, 0, mergeCount)

		assert.Equal(t, runActionEmerge, plan[0].Action)

		commitCount := 0
		for _, p := range plan {
			if p.Action == runActionCommit {
				commitCount++
			}
		}
		assert.Equal(t, 3, commitCount)
	})

	t.Run("with traceback", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "aa")
		d := makeTestCommit("dd", "bb", "cc")

		_, mergeCount, err := prepareRunPlan([]*object.Commit{a, b, c, d}, 0, true)
		require.NoError(t, err)
		assert.Positive(t, mergeCount)
	})

	t.Run("with hibernation", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")

		plan, _, err := prepareRunPlan([]*object.Commit{a, b}, 1, false)
		require.NoError(t, err)
		assert.NotEmpty(t, plan)
	})

	t.Run("diamond produces fork and merge", func(t *testing.T) {
		a := makeTestCommit("aa")
		b := makeTestCommit("bb", "aa")
		c := makeTestCommit("cc", "aa")
		d := makeTestCommit("dd", "bb", "cc")

		plan, _, err := prepareRunPlan([]*object.Commit{a, b, c, d}, 0, false)
		require.NoError(t, err)

		hasFork := false
		hasMerge := false
		for _, p := range plan {
			if p.Action == runActionFork {
				hasFork = true
			}
			if p.Action == runActionMerge {
				hasMerge = true
			}
		}
		assert.True(t, hasFork, "expected fork action in diamond")
		assert.True(t, hasMerge, "expected merge action in diamond")
	})
}

func TestInsertHibernateBootNoOp(t *testing.T) {
	c1 := makeTestCommit("aa")
	// All branches used consecutively - no hibernation needed
	plan := []runAction{
		{Action: runActionEmerge, Commit: nil, Items: []int{1}},
		{Action: runActionCommit, Commit: c1, Items: []int{1}},
	}
	result := insertHibernateBoot(plan, 10)
	assert.Equal(t, plan, result)
}
