package core

// Merge-tracking correctness tests over synthetic non-linear commit histories
// (PLAN.md Phase 11). They exercise Pipeline.Run()'s fork/merge machinery with
// an instrumented pipeline item and verify the invariants which every analysis
// implicitly relies on:
//
//  1. every commit is consumed exactly once across all branches;
//  2. a commit is consumed only after all of its (in-set) parents;
//  3. branch state is isolated: when a commit is consumed, the branch-local
//     state contains only ancestors of that commit — never commits from
//     sibling branches;
//  4. branch state is complete: for a non-merge commit the state holds ALL of
//     its ancestors (i.e. earlier merges combined the full lineage), and for a
//     merge commit it holds at least one parent's full lineage (the merge
//     action runs right after the merge commit is consumed);
//  5. the master branch ends up with every commit after the final merge.
//
// The commit DAGs are synthetic (makeTestCommit), so the tests do not depend
// on repository fixtures and cover shapes that are hard to pin down in a real
// history: octopus merges, criss-cross merges, multiple roots, duplicate and
// redundant merge parents.

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/test"
)

type lineageState = map[plumbing.Hash]bool

var errInvalidLineageDependencyType = errors.New("invalid lineage dependency type")

// lineageConsumeRecord captures a single Consume() call on any branch.
type lineageConsumeRecord struct {
	hash      plumbing.Hash
	index     int
	isMerge   bool
	nextMerge *object.Commit
	// state is a snapshot of the branch-local state BEFORE consuming the commit.
	state lineageState
}

// lineageRecorder is shared between the origin item and all of its forks.
type lineageRecorder struct {
	records    []lineageConsumeRecord
	forks      int
	merges     int
	hibernates int
	boots      int
}

// lineageTestItem tracks which commits its branch lineage has seen. Fork()
// deep-copies the state, Merge() unions it across the merged branches — the
// reference behavior every stateful PipelineItem must implement.
type lineageTestItem struct {
	recorder *lineageRecorder
	seen     lineageState
}

func (item *lineageTestItem) Name() string {
	return "LineageTest"
}

func (item *lineageTestItem) Provides() []string {
	return []string{}
}

func (item *lineageTestItem) Requires() []string {
	return []string{}
}

func (item *lineageTestItem) ListConfigurationOptions() []ConfigurationOption {
	return nil
}

func (item *lineageTestItem) Configure(facts map[string]any) error {
	return nil
}

func (item *lineageTestItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *lineageTestItem) Flag() string {
	return "lineage-test"
}

func (item *lineageTestItem) Description() string {
	return "Instrumented item for merge-tracking correctness tests."
}

func (item *lineageTestItem) Initialize(repository *git.Repository) error {
	item.seen = lineageState{}
	return nil
}

func (item *lineageTestItem) Consume(deps map[string]any) (map[string]any, error) {
	commit, ok := deps[DependencyCommit].(*object.Commit)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidLineageDependencyType, DependencyCommit)
	}

	index, ok := deps[DependencyIndex].(int)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidLineageDependencyType, DependencyIndex)
	}

	isMerge, ok := deps[DependencyIsMerge].(bool)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidLineageDependencyType, DependencyIsMerge)
	}

	record := lineageConsumeRecord{
		hash:    commit.Hash,
		index:   index,
		isMerge: isMerge,
		state:   copyLineageState(item.seen),
	}
	if nextMerge, exists := deps[DependencyNextMerge]; exists {
		record.nextMerge, _ = nextMerge.(*object.Commit)
	}
	item.recorder.records = append(item.recorder.records, record)
	item.seen[commit.Hash] = true
	return map[string]any{}, nil
}

func (item *lineageTestItem) Fork(n int) []PipelineItem {
	item.recorder.forks++
	clones := make([]PipelineItem, n)
	for i := range n {
		clones[i] = &lineageTestItem{
			recorder: item.recorder,
			seen:     copyLineageState(item.seen),
		}
	}
	return clones
}

func (item *lineageTestItem) Merge(branches []PipelineItem) {
	item.recorder.merges++
	union := copyLineageState(item.seen)
	for _, branch := range branches {
		for hash := range mustLineageTestItem(branch).seen {
			union[hash] = true
		}
	}
	item.seen = union
	// The PipelineItem contract requires updating all the branches, not only self.
	for _, branch := range branches {
		mustLineageTestItem(branch).seen = copyLineageState(union)
	}
}

func mustLineageTestItem(item PipelineItem) *lineageTestItem {
	lineage, ok := item.(*lineageTestItem)
	if !ok {
		panic("pipeline item is not a lineage test item")
	}

	return lineage
}

func (item *lineageTestItem) Hibernate() error {
	item.recorder.hibernates++
	return nil
}

func (item *lineageTestItem) Boot() error {
	item.recorder.boots++
	return nil
}

func (item *lineageTestItem) Finalize() any {
	return copyLineageState(item.seen)
}

func (item *lineageTestItem) Serialize(result any, binary bool, writer io.Writer) error {
	return nil
}

func copyLineageState(state lineageState) lineageState {
	clone := make(lineageState, len(state))
	for hash := range state {
		clone[hash] = true
	}
	return clone
}

// uniqueInSetParents returns the deduplicated parents of a commit which are
// present in the analyzed commit set.
func uniqueInSetParents(commit *object.Commit, inSet map[plumbing.Hash]*object.Commit) []plumbing.Hash {
	var parents []plumbing.Hash
	visited := map[plumbing.Hash]bool{}
	for _, parent := range commit.ParentHashes {
		if visited[parent] {
			continue
		}
		visited[parent] = true
		if _, exists := inSet[parent]; exists {
			parents = append(parents, parent)
		}
	}
	return parents
}

// properAncestors computes, for each commit, the set of its ancestors within
// the analyzed commit set (excluding the commit itself).
func properAncestors(commits []*object.Commit) map[plumbing.Hash]lineageState {
	inSet := map[plumbing.Hash]*object.Commit{}
	for _, commit := range commits {
		inSet[commit.Hash] = commit
	}
	memo := map[plumbing.Hash]lineageState{}
	var visit func(hash plumbing.Hash) lineageState
	visit = func(hash plumbing.Hash) lineageState {
		if ancestors, exists := memo[hash]; exists {
			return ancestors
		}
		ancestors := lineageState{}
		for _, parent := range uniqueInSetParents(inSet[hash], inSet) {
			ancestors[parent] = true
			for ancestor := range visit(parent) {
				ancestors[ancestor] = true
			}
		}
		memo[hash] = ancestors
		return ancestors
	}
	for _, commit := range commits {
		visit(commit.Hash)
	}
	return memo
}

// runLineagePipeline executes a full Pipeline over the given synthetic commits
// and returns the shared recorder plus the master branch's final lineage state.
func runLineagePipeline(
	t *testing.T, commits []*object.Commit, hibernationDistance int,
) (*lineageRecorder, lineageState) {
	t.Helper()
	pipeline := NewPipeline(test.FixtureRepository())
	item := &lineageTestItem{recorder: &lineageRecorder{}}
	pipeline.AddItem(item)
	facts := map[string]any{ConfigPipelineCommits: commits}
	if hibernationDistance > 0 {
		facts[ConfigPipelineHibernationDistance] = hibernationDistance
	}
	require.NoError(t, pipeline.Initialize(facts))
	result, err := pipeline.Run(commits)
	require.NoError(t, err)
	finalState, ok := result[item].(lineageState)
	require.True(t, ok, "master branch did not produce a lineage state")
	return item.recorder, finalState
}

// verifyLineageInvariants asserts the merge-tracking correctness invariants
// (see the file comment) for a completed run.
func verifyLineageInvariants(
	t *testing.T, commits []*object.Commit, recorder *lineageRecorder, finalState lineageState,
) {
	t.Helper()
	inSet := map[plumbing.Hash]*object.Commit{}
	for _, commit := range commits {
		inSet[commit.Hash] = commit
	}
	ancestors := properAncestors(commits)

	// 1. Every commit is consumed exactly once.
	require.Len(t, recorder.records, len(commits),
		"number of Consume() calls must match the number of commits")
	consumedAt := map[plumbing.Hash]int{}
	for i, record := range recorder.records {
		_, known := inSet[record.hash]
		require.True(t, known, "consumed unknown commit %s", record.hash)
		_, duplicate := consumedAt[record.hash]
		require.False(t, duplicate, "commit %s was consumed twice", record.hash)
		consumedAt[record.hash] = i
	}

	for _, record := range recorder.records {
		parents := uniqueInSetParents(inSet[record.hash], inSet)

		// 2. Parents are always consumed before their children.
		for _, parent := range parents {
			assert.Less(t, consumedAt[parent], consumedAt[record.hash],
				"commit %s was consumed before its parent %s", record.hash, parent)
		}

		// 3. Branch state isolation: only ancestors may be visible.
		expected := ancestors[record.hash]
		for hash := range record.state {
			assert.True(t, expected[hash],
				"while consuming %s the branch state leaked non-ancestor %s",
				record.hash, hash)
		}

		if len(parents) <= 1 {
			// 4a. Non-merge commits must see their complete ancestry.
			assert.Len(t, record.state, len(expected),
				"while consuming non-merge commit %s the branch state missed ancestors",
				record.hash)
		} else {
			// 4b. Merge commits are consumed on one parent branch before the
			// item-level Merge() runs, so at least one parent's full lineage
			// must be present.
			complete := false
			for _, parent := range parents {
				full := record.state[parent]
				for ancestor := range ancestors[parent] {
					if !record.state[ancestor] {
						full = false
						break
					}
				}
				if full {
					complete = true
					break
				}
			}
			assert.True(t, complete,
				"while consuming merge commit %s no parent lineage was complete",
				record.hash)
		}
	}

	// 5. The master branch saw everything.
	assert.Len(t, finalState, len(commits), "final master state is incomplete")
	for _, commit := range commits {
		assert.True(t, finalState[commit.Hash],
			"final master state misses commit %s", commit.Hash)
	}
}

// assertIsMergeFlags checks that DependencyIsMerge was true exactly for the
// given commits. Only usable for topologies without redundant merge edges
// (those are collapsed by the planner on purpose).
func assertIsMergeFlags(t *testing.T, recorder *lineageRecorder, merges ...*object.Commit) {
	t.Helper()
	expected := lineageState{}
	for _, commit := range merges {
		expected[commit.Hash] = true
	}
	for _, record := range recorder.records {
		assert.Equal(t, expected[record.hash], record.isMerge,
			"DependencyIsMerge mismatch for %s", record.hash)
	}
}

func TestMergeTrackingLinearHistory(t *testing.T) {
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "bb")
	commits := []*object.Commit{a, b, c}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder)
	assert.Equal(t, 0, recorder.merges, "linear history must not trigger Merge()")
}

func TestMergeTrackingDiamond(t *testing.T) {
	//   a
	//  / \
	// b   c
	//  \ /
	//   d
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	d := makeTestCommit("dd", "bb", "cc")
	commits := []*object.Commit{a, b, c, d}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, d)
	assert.Equal(t, 1, recorder.merges)
	// The state isolation invariant already guarantees that b and c never see
	// each other, but spell the crucial case out for clarity.
	for _, record := range recorder.records {
		switch record.hash {
		case b.Hash:
			assert.False(t, record.state[c.Hash], "branch of b leaked commit c")
		case c.Hash:
			assert.False(t, record.state[b.Hash], "branch of c leaked commit b")
		}
	}
}

func TestMergeTrackingOctopus(t *testing.T) {
	// a forks into b, c, d; e merges all three at once.
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	d := makeTestCommit("dd", "aa")
	e := makeTestCommit("ee", "bb", "cc", "dd")
	commits := []*object.Commit{a, b, c, d, e}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, e)
	assert.Equal(t, 1, recorder.merges, "an octopus merge is a single Merge() call")
}

func TestMergeTrackingCrissCross(t *testing.T) {
	//    a
	//   / \
	//  b   c
	//  |\ /|
	//  | X |
	//  |/ \|
	//  d   e     (d and e both merge b and c)
	//   \ /
	//    f
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	d := makeTestCommit("dd", "bb", "cc")
	e := makeTestCommit("ee", "cc", "bb")
	f := makeTestCommit("ff", "dd", "ee")
	commits := []*object.Commit{a, b, c, d, e, f}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, d, e, f)
	assert.Equal(t, 3, recorder.merges)
}

func TestMergeTrackingSequentialFeatureBranches(t *testing.T) {
	// Two feature branches merged back one after another:
	// a - b -- m1 -- m2
	//  \ /    /
	//   c    /
	//    \  /
	//     d-e
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	m1 := makeTestCommit("dd", "bb", "cc")
	d := makeTestCommit("ee", "aa")
	e := makeTestCommit("ff", "ee")
	m2 := makeTestCommit("0f", "dd", "ff")
	commits := []*object.Commit{a, b, c, m1, d, e, m2}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, m1, m2)
	assert.Equal(t, 2, recorder.merges)
}

func TestMergeTrackingLongParallelBranches(t *testing.T) {
	// Multi-commit sequences on both sides of the fork exercise the planner's
	// sequence collapsing (mergeDag).
	a := makeTestCommit("aa")
	b1 := makeTestCommit("b1", "aa")
	b2 := makeTestCommit("b2", "b1")
	b3 := makeTestCommit("b3", "b2")
	c1 := makeTestCommit("c1", "aa")
	c2 := makeTestCommit("c2", "c1")
	c3 := makeTestCommit("c3", "c2")
	m := makeTestCommit("dd", "b3", "c3")
	tail := makeTestCommit("ee", "dd")
	commits := []*object.Commit{a, b1, b2, b3, c1, c2, c3, m, tail}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, m)
	assert.Equal(t, 1, recorder.merges)
}

func TestMergeTrackingMultipleRoots(t *testing.T) {
	// Two independent root commits joined by a merge — e.g. a subtree import.
	r1 := makeTestCommit("aa")
	r2 := makeTestCommit("bb")
	m := makeTestCommit("cc", "aa", "bb")
	tail := makeTestCommit("dd", "cc")
	commits := []*object.Commit{r1, r2, m, tail}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder, m)
	assert.Equal(t, 1, recorder.merges)
}

func TestMergeTrackingDuplicateMergeParents(t *testing.T) {
	// Merge commits with duplicated parent hashes exist in the wild and used
	// to crash Hercules (see getCommitParents). The duplicate must be ignored,
	// turning the "merge" into a regular commit.
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa", "aa")
	commits := []*object.Commit{a, b}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assertIsMergeFlags(t, recorder)
	assert.Equal(t, 0, recorder.merges)
}

func TestMergeTrackingRedundantMergeEdge(t *testing.T) {
	// The second parent of m is an ancestor of the first parent — a redundant
	// merge edge which collapseFastForwards() removes from the plan. The run
	// must still consume every commit exactly once with correct lineage.
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "bb")
	m := makeTestCommit("dd", "cc", "aa")
	commits := []*object.Commit{a, b, c, m}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	// No assertIsMergeFlags here: the planner may legitimately treat m as a
	// plain commit after collapsing the redundant edge.
}

func TestMergeTrackingHibernation(t *testing.T) {
	// Hibernation must be transparent: same invariants and the same final
	// state as the plain run, with Hibernate()/Boot() actually invoked.
	a := makeTestCommit("aa")
	b1 := makeTestCommit("b1", "aa")
	b2 := makeTestCommit("b2", "b1")
	b3 := makeTestCommit("b3", "b2")
	c1 := makeTestCommit("c1", "aa")
	c2 := makeTestCommit("c2", "c1")
	c3 := makeTestCommit("c3", "c2")
	m := makeTestCommit("dd", "b3", "c3")
	commits := []*object.Commit{a, b1, b2, b3, c1, c2, c3, m}

	plainRecorder, plainState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, plainRecorder, plainState)

	hibRecorder, hibState := runLineagePipeline(t, commits, 1)
	verifyLineageInvariants(t, commits, hibRecorder, hibState)
	assert.Positive(t, hibRecorder.hibernates, "expected Hibernate() to be invoked")
	assert.Positive(t, hibRecorder.boots, "expected Boot() to be invoked")
	assert.Equal(t, plainState, hibState,
		"hibernation changed the analysis result")

	plainOrder := make([]plumbing.Hash, len(plainRecorder.records))
	hibOrder := make([]plumbing.Hash, len(hibRecorder.records))
	for i := range plainRecorder.records {
		plainOrder[i] = plainRecorder.records[i].hash
		hibOrder[i] = hibRecorder.records[i].hash
	}
	assert.Equal(t, plainOrder, hibOrder,
		"hibernation changed the commit consumption order")
}

func TestMergeTrackingNextMerge(t *testing.T) {
	// The merge_tracks feature (InitializeExt + RunPreparedPlan) annotates
	// every commit with the next downstream merge commit of its branch.
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	d := makeTestCommit("dd", "bb", "cc")
	commits := []*object.Commit{a, b, c, d}

	pipeline := NewPipeline(test.FixtureRepository())
	item := &lineageTestItem{recorder: &lineageRecorder{}}
	pipeline.AddItem(item)
	pipeline.SetFeature(FeatureMergeTracks)
	facts := map[string]any{ConfigPipelineCommits: commits}
	require.NoError(t, pipeline.InitializeExt(facts,
		func(items []PipelineItem) PipelineItem { return items[0] }, true))
	assert.Equal(t, 1, facts[FactMergeHashCount])

	result, err := pipeline.RunPreparedPlan()
	require.NoError(t, err)
	finalState, ok := result[item].(lineageState)
	require.True(t, ok)
	verifyLineageInvariants(t, commits, item.recorder, finalState)

	for _, record := range item.recorder.records {
		require.NotNil(t, record.nextMerge,
			"commit %s has no NextMerge annotation", record.hash)
		assert.Equal(t, d.Hash, record.nextMerge.Hash,
			"commit %s must point at merge %s", record.hash, d.Hash)
	}

	// A second RunPreparedPlan() must fail - the plan is consumed.
	_, err = pipeline.RunPreparedPlan()
	assert.Error(t, err)
}

func TestMergeTrackingNextMergeAfterLastMerge(t *testing.T) {
	// Commits after the last merge have no downstream merge -> nil NextMerge.
	a := makeTestCommit("aa")
	b := makeTestCommit("bb", "aa")
	c := makeTestCommit("cc", "aa")
	d := makeTestCommit("dd", "bb", "cc")
	tail := makeTestCommit("ee", "dd")
	commits := []*object.Commit{a, b, c, d, tail}

	pipeline := NewPipeline(test.FixtureRepository())
	item := &lineageTestItem{recorder: &lineageRecorder{}}
	pipeline.AddItem(item)
	pipeline.SetFeature(FeatureMergeTracks)
	facts := map[string]any{ConfigPipelineCommits: commits}
	require.NoError(t, pipeline.InitializeExt(facts,
		func(items []PipelineItem) PipelineItem { return items[0] }, true))

	result, err := pipeline.RunPreparedPlan()
	require.NoError(t, err)
	finalState, ok := result[item].(lineageState)
	require.True(t, ok)
	verifyLineageInvariants(t, commits, item.recorder, finalState)

	for _, record := range item.recorder.records {
		if record.hash == tail.Hash {
			assert.Nil(t, record.nextMerge,
				"commit after the last merge must have no NextMerge")
		} else {
			require.NotNil(t, record.nextMerge,
				"commit %s has no NextMerge annotation", record.hash)
			assert.Equal(t, d.Hash, record.nextMerge.Hash)
		}
	}
}

func TestMergeTracksRequiresPreparedPlan(t *testing.T) {
	// Plain Initialize() must reject the merge_tracks feature: it only works
	// with a prepared plan (InitializeExt + RunPreparedPlan).
	a := makeTestCommit("aa")
	commits := []*object.Commit{a}

	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&lineageTestItem{recorder: &lineageRecorder{}})
	pipeline.SetFeature(FeatureMergeTracks)
	err := pipeline.Initialize(map[string]any{ConfigPipelineCommits: commits})
	assert.Error(t, err)
}

// TestMergeTrackingStress runs the invariant suite over a wider generated
// topology: a chain of diamonds with varying branch lengths and an octopus in
// the middle. This is the kind of history where branch bookkeeping errors
// (reused branch indices, missed deletes) historically surfaced.
func TestMergeTrackingStress(t *testing.T) {
	var commits []*object.Commit
	newCommit := func(id int, parents ...string) string {
		hash := fmt.Sprintf("%08x", id+1) // zero hash is not a valid commit
		commit := makeTestCommit(hash, parents...)
		commits = append(commits, commit)
		return hash
	}

	id := 0
	nextID := func() int { id++; return id }

	tip := newCommit(nextID())
	// Five stacked diamonds with growing branch lengths.
	for width := 1; width <= 5; width++ {
		heads := make([]string, 0, 2)
		for range 2 {
			parent := tip
			for range width {
				parent = newCommit(nextID(), parent)
			}
			heads = append(heads, parent)
		}
		tip = newCommit(nextID(), heads...)
	}
	// One octopus across four branches.
	heads := make([]string, 0, 4)
	for range 4 {
		head := newCommit(nextID(), tip)
		heads = append(heads, head)
	}
	tip = newCommit(nextID(), heads...)
	// Linear tail.
	for range 3 {
		tip = newCommit(nextID(), tip)
	}

	recorder, finalState := runLineagePipeline(t, commits, 0)
	verifyLineageInvariants(t, commits, recorder, finalState)
	assert.Equal(t, 6, recorder.merges, "expected 5 diamond merges + 1 octopus merge")

	// And once more with hibernation enabled.
	hibRecorder, hibState := runLineagePipeline(t, commits, 2)
	verifyLineageInvariants(t, commits, hibRecorder, hibState)
	assert.Equal(t, finalState, hibState)
}
