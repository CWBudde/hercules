package internal_test

import (
	"bytes"
	"os"
	"path"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/leaves"
)

func TestPipelineSerialize(t *testing.T) {
	pipeline := core.NewPipeline(test.Repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	pipeline.DeployItem(&leaves.LegacyBurndownAnalysis{})
	facts := map[string]any{}
	facts[core.ConfigPipelineDryRun] = true
	tmpdir := t.TempDir()
	dotpath := path.Join(tmpdir, "graph.dot")
	facts[core.ConfigPipelineDAGPath] = dotpath
	_ = pipeline.Initialize(facts)
	bdot, _ := os.ReadFile(dotpath)
	dot := string(bdot)
	assert.Equal(t, `digraph Hercules {
  "5 BlobCache_1" -> "6 [blob_cache]"
  "8 [changes]" -> "9 FileDiff_1"
  "8 [changes]" -> "11 LegacyBurndown_1"
  "6 [blob_cache]" -> "9 FileDiff_1"
  "6 [blob_cache]" -> "11 LegacyBurndown_1"
  "6 [blob_cache]" -> "7 RenameAnalysis_1"
  "9 FileDiff_1" -> "10 [file_diff]"
  "10 [file_diff]" -> "11 LegacyBurndown_1"
  "4 [tick]" -> "11 LegacyBurndown_1"
  "3 [author]" -> "11 LegacyBurndown_1"
  "0 PeopleDetector_1" -> "3 [author]"
  "7 RenameAnalysis_1" -> "8 [changes]"
  "1 TicksSinceStart_1" -> "4 [tick]"
  "2 TreeDiff_1" -> "5 BlobCache_1"
  "2 TreeDiff_1" -> "7 RenameAnalysis_1"
}`, dot)
}

func TestPipelineResolveIntegration(t *testing.T) {
	pipeline := core.NewPipeline(test.Repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	pipeline.DeployItem(&leaves.LegacyBurndownAnalysis{})
	pipeline.DeployItem(&leaves.CouplesAnalysis{})
	assert.NoError(t, pipeline.Initialize(map[string]any{}))
}

func TestPipelineLifecycleCommitsIntegration(t *testing.T) {
	repository := test.FixtureRepository()
	commit, err := repository.CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	require.NoError(t, err)

	pipeline := core.NewPipeline(repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	analysis := pipeline.DeployItem(&leaves.CommitsAnalysis{}).(*leaves.CommitsAnalysis)
	commits := []*object.Commit{commit}
	require.NoError(t, pipeline.Initialize(map[string]any{
		core.ConfigPipelineCommits: commits,
	}))

	results, err := pipeline.Run(commits)
	require.NoError(t, err)
	result, exists := results[analysis]
	require.True(t, exists)
	commitsResult, ok := result.(leaves.CommitsResult)
	require.True(t, ok)
	require.Len(t, commitsResult.Commits, 1)
	assert.Equal(t, commit.Hash.String(), commitsResult.Commits[0].Hash)

	var yaml bytes.Buffer
	require.NoError(t, analysis.Serialize(result, false, &yaml))
	assert.Contains(t, yaml.String(), "hash: "+commit.Hash.String())

	var protobuf bytes.Buffer
	require.NoError(t, analysis.Serialize(result, true, &protobuf))
	var message pb.CommitsAnalysisResults
	require.NoError(t, proto.Unmarshal(protobuf.Bytes(), &message))
	require.Len(t, message.GetCommits(), 1)
	assert.Equal(t, commit.Hash.String(), message.GetCommits()[0].GetHash())
}
