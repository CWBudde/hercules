package core

import (
	"errors"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/test"
)

var (
	errHibernationTestHibernate = errors.New("hibernate failed")
	errHibernationTestBoot      = errors.New("boot failed")
)

// hibernationRecorder is shared by every clone of a hibernationTestItem, so the assertions can
// see what happened across all branches without the item itself having to be shared.
type hibernationRecorder struct {
	hibernates int
	boots      int
	// asleep tracks the sleep state per instance, which is what the B11 regression turns on:
	// consuming a commit on a hibernated instance is the panic being guarded against.
	asleep map[*hibernationTestItem]bool
	// unbalanced records any Boot() that arrived without a matching Hibernate(), or any
	// consume that landed on a sleeping instance.
	violations []string

	raiseHibernateError bool
	raiseBootError      bool
}

// hibernationTestItem clones per branch, the way LineHistoryAnalyser does.
type hibernationTestItem struct {
	NoopMerger

	recorder *hibernationRecorder
	shared   bool
}

func (item *hibernationTestItem) Name() string { return "HibernationTest" }

func (item *hibernationTestItem) Provides() []string { return []string{"hibernation_test"} }

func (item *hibernationTestItem) Requires() []string { return nil }

func (item *hibernationTestItem) ListConfigurationOptions() []ConfigurationOption { return nil }

func (item *hibernationTestItem) Configure(map[string]any) error { return nil }

func (item *hibernationTestItem) ConfigureUpstream(map[string]any) error { return nil }

func (item *hibernationTestItem) Initialize(*git.Repository) error { return nil }

func (item *hibernationTestItem) Consume(map[string]any) (map[string]any, error) {
	if item.recorder.asleep[item] {
		// The real analysers panic here (internal/linehistory/line_history.go). Recording it
		// keeps the failure readable and lets the test report every occurrence.
		item.recorder.violations = append(item.recorder.violations,
			"Consume() ran on a hibernated instance")
	}

	return map[string]any{"hibernation_test": item}, nil
}

func (item *hibernationTestItem) Fork(n int) []PipelineItem {
	if item.shared {
		return ForkSamePipelineItem(item, n)
	}

	clones := make([]PipelineItem, n)
	for i := range clones {
		clones[i] = &hibernationTestItem{recorder: item.recorder}
	}

	return clones
}

func (item *hibernationTestItem) Hibernate() error {
	item.recorder.hibernates++

	if item.recorder.asleep[item] {
		item.recorder.violations = append(item.recorder.violations,
			"Hibernate() ran on an already hibernated instance")
	}

	item.recorder.asleep[item] = true

	if item.recorder.raiseHibernateError {
		return errHibernationTestHibernate
	}

	return nil
}

func (item *hibernationTestItem) Boot() error {
	item.recorder.boots++

	if !item.recorder.asleep[item] {
		item.recorder.violations = append(item.recorder.violations,
			"Boot() ran without a matching Hibernate()")
	}

	item.recorder.asleep[item] = false

	if item.recorder.raiseBootError {
		return errHibernationTestBoot
	}

	return nil
}

func (item *hibernationTestItem) Finalize() any { return nil }

// hibernationFixtureCommits returns a history with two branches that both live long enough for
// the idle-gap detector to fire at HibernationDistance = 1.
func hibernationFixtureCommits(t *testing.T) []*object.Commit {
	t.Helper()

	hashes := []string{
		"0183e08978007c746468fca9f68e6e2fbf32100c",
		"b467a682f680a4dcfd74869480a52f8be3a4fdf0",
		"31c9f752f9ce103e85523442fa3f05b1ff4ea546",
		"6530890fcd02fb5e6e85ce2951fdd5c555f2c714",
		"feb2d230777cbb492ecbc27dea380dc1e7b8f437",
		"9b30d2abc043ab59aa7ec7b50970c65c90b98853",
	}

	commits := make([]*object.Commit, len(hashes))

	for i, hash := range hashes {
		commit, err := test.FixtureRepository().CommitObject(plumbing.NewHash(hash))
		require.NoError(t, err)

		commits[i] = commit
	}

	return commits
}

func runHibernationPipeline(t *testing.T, recorder *hibernationRecorder, shared bool) error {
	t.Helper()

	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.HibernationDistance = 1
	pipeline.AddItem(&hibernationTestItem{recorder: recorder, shared: shared})
	require.NoError(t, pipeline.Initialize(map[string]any{}))

	_, err := pipeline.Run(hibernationFixtureCommits(t))

	return err
}

func newHibernationRecorder() *hibernationRecorder {
	return &hibernationRecorder{asleep: map[*hibernationTestItem]bool{}}
}

// TestHibernationNeverSleepsAnInstanceAnotherBranchIsUsing is the PLAN.md B11 regression.
//
// Hibernation is scheduled per branch index, but ForkSamePipelineItem hands the same pointer to
// every branch. The plan routinely hibernates the idle side of a fork and then commits on its
// sibling in the very next step, which used to nil out the shared instance's state underneath
// the running branch - `panic: assignment to entry in nil map` in burndown.
func TestHibernationNeverSleepsAnInstanceAnotherBranchIsUsing(t *testing.T) {
	recorder := newHibernationRecorder()
	require.NoError(t, runHibernationPipeline(t, recorder, true))

	assert.Empty(t, recorder.violations)
	assert.Zero(t, recorder.hibernates,
		"a branch-shared instance is held by every live branch, so it can never sleep")
	assert.Zero(t, recorder.boots)
}

// TestHibernationStillSleepsPerBranchInstances is the other half of the contract: the fix must
// not disable hibernation for the items that actually own their state per branch - which is
// where the memory it saves comes from.
func TestHibernationStillSleepsPerBranchInstances(t *testing.T) {
	recorder := newHibernationRecorder()
	require.NoError(t, runHibernationPipeline(t, recorder, false))

	assert.Empty(t, recorder.violations)
	assert.Positive(t, recorder.hibernates, "expected Hibernate() to be invoked")
	assert.Equal(t, recorder.hibernates, recorder.boots,
		"every Hibernate() must be paired with exactly one Boot()")

	for item, asleep := range recorder.asleep {
		assert.False(t, asleep, "instance %p was left hibernated at the end of the run", item)
	}
}

func TestHibernationErrorsPropagate(t *testing.T) {
	recorder := newHibernationRecorder()
	recorder.raiseHibernateError = true
	require.ErrorIs(t, runHibernationPipeline(t, recorder, false), errHibernationTestHibernate)

	recorder = newHibernationRecorder()
	recorder.raiseBootError = true
	require.ErrorIs(t, runHibernationPipeline(t, recorder, false), errHibernationTestBoot)
}
