package linehistory_test

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/leaves"
)

func replayFixture() string {
	return fmt.Sprintf(`LineDumper:
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |-
      1 0 1 0 1 3
      2 0 1 0 1 2
    bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb: |-
      1 0 1 1 2 -2
      1 1 2 1 2 1
      2 0 1 1 2 -2
      2 262143 2 1 2 %d
  file_sequence:
    1: "main.go"
  author_sequence:
    - "Alice"
    - "Bob"
`, math.MinInt)
}

func TestLineHistoryExportLoadExportLifecycle(t *testing.T) {
	first := runLoadedLineDump(t, replayFixture())
	second := runLoadedLineDump(t, first)

	require.Equal(t, first, second)
}

func runLoadedLineDump(t *testing.T, input string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "history.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0o600))

	repository, err := git.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)

	pipeline := core.NewPipeline(repository)
	dumper := &leaves.LineDumper{}
	pipeline.SetFeature(core.FeatureGitStub)
	pipeline.DeployItem(dumper)

	facts := map[string]any{linehistory.ConfigLinesLoadFrom: path}
	err = pipeline.InitializeExt(facts, func(items []core.PipelineItem) core.PipelineItem {
		return items[0]
	}, true)
	require.NoError(t, err)

	results, err := pipeline.RunPreparedPlan()
	require.NoError(t, err)

	var output bytes.Buffer
	output.WriteString("LineDumper:\n")
	require.NoError(t, dumper.Serialize(results[dumper], false, &output))

	return output.String()
}
