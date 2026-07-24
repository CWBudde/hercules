package internal_test

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
	"github.com/cwbudde/hercules/leaves"
)

func TestPipelineSerialize(t *testing.T) {
	pipeline := core.NewPipeline(test.Repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	pipeline.DeployItem(&leaves.LegacyBurndownAnalysis{})
	facts := map[string]any{}
	facts[core.ConfigPipelineDryRun] = true
	tmpdir, _ := os.MkdirTemp("", "hercules-")
	defer func() { _ = os.RemoveAll(tmpdir) }()
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
