package hercules_test

import (
	"slices"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hercules "github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/test"
)

const (
	defaultGitFeature = "git.commits"
	gitStubFeature    = "git.stub"
	mergeTrackFeature = "merge_tracks"
)

func TestRegisteredDefaultItemsConformToLifecycle(t *testing.T) {
	repository := test.FixtureRepository()
	commits := lifecycleCommits(t, repository)
	items := append(
		[]hercules.PipelineItem{},
		hercules.Registry.GetPlumbingItems()...,
	)
	for _, leaf := range hercules.Registry.GetLeaves() {
		items = append(items, leaf)
	}

	tested := map[string]struct{}{}
	for _, item := range items {
		if !isDefaultGitItem(item) {
			continue
		}
		if _, duplicate := tested[item.Name()]; duplicate {
			continue
		}
		tested[item.Name()] = struct{}{}

		t.Run(item.Name(), func(t *testing.T) {
			assertPipelineItemLifecycle(t, item, repository, commits, items)
		})
	}

	assert.NotEmpty(t, tested)
	for _, item := range items {
		if isDefaultGitItem(item) {
			assert.Contains(t, tested, item.Name())
		}
	}
}

func assertPipelineItemLifecycle(
	t *testing.T,
	item hercules.PipelineItem,
	repository *git.Repository,
	commits []*object.Commit,
	registered []hercules.PipelineItem,
) {
	t.Helper()

	pipeline := hercules.NewPipeline(repository)
	pipeline.SetFeature(defaultGitFeature)
	pipeline.DeployItem(item)
	outputDirectory := t.TempDir()

	newFacts := func() map[string]any {
		facts := lifecycleFacts(commits, registered, outputDirectory)
		return facts
	}

	// A second initialization without a run must discard only the first run's
	// state; configuration and topology remain reusable.
	firstInitializeError := pipeline.Initialize(newFacts())
	if firstInitializeError != nil {
		// Optional build-time integrations remain registered so their CLI flags
		// can explain the missing capability. They must still fail repeatedly
		// and deterministically without retaining partial run state.
		require.Contains(t, firstInitializeError.Error(), "unavailable in this build")
		secondInitializeError := pipeline.Initialize(newFacts())
		require.EqualError(t, secondInitializeError, firstInitializeError.Error())
		return
	}
	require.NoError(t, pipeline.Initialize(newFacts()))

	first, err := pipeline.Run(commits)
	require.NoError(t, err)

	require.NoError(t, pipeline.Initialize(newFacts()))
	second, err := pipeline.Run(commits)
	require.NoError(t, err)

	firstLeaf, isLeaf := item.(hercules.LeafPipelineItem)
	if !isLeaf {
		return
	}

	assert.Equal(t, first[firstLeaf], second[firstLeaf])
}

func lifecycleFacts(
	commits []*object.Commit,
	registered []hercules.PipelineItem,
	outputDirectory string,
) map[string]any {
	facts := map[string]any{
		hercules.ConfigPipelineCommits: commits,
	}
	for _, item := range registered {
		for _, option := range item.ListConfigurationOptions() {
			if _, exists := facts[option.Name]; exists {
				continue
			}

			switch value := option.Default.(type) {
			case []string:
				facts[option.Name] = slices.Clone(value)
			default:
				facts[option.Name] = value
			}
		}
	}

	// The UAST saver defaults to the process working directory. Lifecycle
	// fixtures isolate all generated files in the test's temporary directory.
	facts["ChangesSaver.OutputPath"] = outputDirectory

	return facts
}

func lifecycleCommits(t *testing.T, repository *git.Repository) []*object.Commit {
	t.Helper()

	hashes := []string{
		"6db8065cdb9bb0758f36a7e75fc72ab95f9e8145",
		"f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee",
		"8a03b5620b1caa72ec9cb847ea88332621e2950a",
		"dd9dd084d5851d7dc4399fc7dbf3d8292831ebc5",
		"f4ed0405b14f006c0744029d87ddb3245607587a",
	}
	commits := make([]*object.Commit, len(hashes))
	for index, hash := range hashes {
		var err error
		commits[index], err = repository.CommitObject(plumbing.NewHash(hash))
		require.NoError(t, err, "load lifecycle commit "+hash)
	}

	return commits
}

func isDefaultGitItem(item hercules.PipelineItem) bool {
	featured, ok := item.(hercules.FeaturedPipelineItem)
	if !ok {
		return true
	}

	for _, feature := range featured.Features() {
		if feature == gitStubFeature || feature == mergeTrackFeature {
			return false
		}
		if feature != defaultGitFeature {
			return false
		}
	}

	return true
}
