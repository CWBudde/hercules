package modes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/render/readers"
)

const knowledgeSiloTestTickSize = int64(24 * time.Hour)

func knowledgeSiloPaths(files []knowledgeFileSummary) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}

	return paths
}

// The defect T3.2 records: when almost every file has one editor, the alphabetical tie-break is
// the ranking, so a trivial config file outranks the file the sole owner actually holds hostage.
func TestRankedKnowledgeFilesRanksRiskAboveAlphabet(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		".devcontainer/devcontainer.json": {
			UniqueEditors: 1, RecentEditors: 1,
			Lines: 12, Churn: 14, TicksSinceLastEdit: 400,
		},
		"server/pricing.go": {
			UniqueEditors: 1, RecentEditors: 1,
			Lines: 1800, Churn: 9000, TicksSinceLastEdit: 2,
		},
		"web/widget.tsx": {
			UniqueEditors: 4, RecentEditors: 2,
			Lines: 1600, Churn: 8000, TicksSinceLastEdit: 2,
		},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, knowledgeSiloTestTickSize)

	assert.True(t, wasRanked)
	assert.Equal(
		t,
		[]string{"server/pricing.go", "web/widget.tsx", ".devcontainer/devcontainer.json"},
		knowledgeSiloPaths(ranked),
	)
}

// Sole ownership is the point of the chart: two files which are otherwise identical must be
// separated by their editor count.
func TestRankedKnowledgeFilesPrefersSoleOwnership(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		"a_shared.go": {UniqueEditors: 4, Lines: 500, Churn: 500, TicksSinceLastEdit: 1},
		"b_siloed.go": {UniqueEditors: 1, Lines: 500, Churn: 500, TicksSinceLastEdit: 1},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, knowledgeSiloTestTickSize)

	assert.True(t, wasRanked)
	assert.Equal(t, []string{"b_siloed.go", "a_shared.go"}, knowledgeSiloPaths(ranked))
}

// Recency decays rather than cutting off, so a stale file still ranks - just below an equally
// sized one somebody is working in today.
func TestRankedKnowledgeFilesPrefersRecentActivity(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		"a_stale.go":  {UniqueEditors: 1, Lines: 400, Churn: 400, TicksSinceLastEdit: 900},
		"b_active.go": {UniqueEditors: 1, Lines: 400, Churn: 400, TicksSinceLastEdit: 1},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, knowledgeSiloTestTickSize)

	assert.True(t, wasRanked)
	assert.Equal(t, []string{"b_active.go", "a_stale.go"}, knowledgeSiloPaths(ranked))
	assert.Positive(t, ranked[1].SiloRisk, "a decayed recency factor must not zero the score")
}

// A file which no longer exists at HEAD carries no lines and is not a silo risk.
func TestRankedKnowledgeFilesScoresDeletedFilesZero(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		"live.go": {UniqueEditors: 1, Lines: 100, Churn: 100, TicksSinceLastEdit: 1},
		"gone.go": {UniqueEditors: 1, Lines: 0, Churn: 5000, TicksSinceLastEdit: 1},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, knowledgeSiloTestTickSize)

	assert.True(t, wasRanked)
	assert.Equal(t, []string{"live.go", "gone.go"}, knowledgeSiloPaths(ranked))
	assert.Zero(t, ranked[1].SiloRisk)
}

// A .pb written before the leaf collected these factors carries no lines and no churn. Such a
// file must keep the ordering it always had rather than get an arbitrary one.
func TestRankedKnowledgeFilesFallsBackWithoutFactors(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		"zebra.go": {UniqueEditors: 1, RecentEditors: 1},
		"alpha.go": {UniqueEditors: 3, RecentEditors: 2},
		"beta.go":  {UniqueEditors: 1, RecentEditors: 1},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, knowledgeSiloTestTickSize)

	assert.False(t, wasRanked)
	assert.Equal(t, []string{"beta.go", "zebra.go", "alpha.go"}, knowledgeSiloPaths(ranked))
}

// An unknown tick size must neutralise the recency factor, not divide by zero.
func TestRankedKnowledgeFilesToleratesUnknownTickSize(t *testing.T) {
	files := map[string]readers.KnowledgeDiffusionFile{
		"a.go": {UniqueEditors: 1, Lines: 100, Churn: 100, TicksSinceLastEdit: 900},
		"b.go": {UniqueEditors: 1, Lines: 400, Churn: 400, TicksSinceLastEdit: 1},
	}

	ranked, wasRanked := rankedKnowledgeFiles(files, 6, 0)

	require.Len(t, ranked, 2)
	assert.True(t, wasRanked)
	// Without a tick size the ages are ignored, so size and churn decide.
	assert.Equal(t, []string{"b.go", "a.go"}, knowledgeSiloPaths(ranked))
	assert.Positive(t, ranked[1].SiloRisk)
}

// The ranking has to survive the path from KnowledgeDiffusionData to the plot, which is where
// WindowMonths and TickSize are handed over.
func TestPlotKnowledgeSilosRendersRankedData(t *testing.T) {
	// The caller hands plotKnowledgeSilos the sibling path, not the base output.
	output := siblingOutputPath(
		filepath.Join(t.TempDir(), "knowledge-diffusion.png"), "knowledge-diffusion.png", "silos",
	)
	data := &readers.KnowledgeDiffusionData{
		Files: map[string]readers.KnowledgeDiffusionFile{
			"server/pricing.go": {
				UniqueEditors: 1, RecentEditors: 1,
				Lines: 1800, Churn: 9000, TicksSinceLastEdit: 2,
			},
			".devcontainer/devcontainer.json": {
				UniqueEditors: 1, RecentEditors: 0,
				Lines: 12, Churn: 14, TicksSinceLastEdit: 400,
			},
		},
		Distribution: map[int]int{1: 2},
		WindowMonths: 6,
		TickSize:     knowledgeSiloTestTickSize,
	}

	require.NoError(t, plotKnowledgeSilos("repo", data, output))

	_, err := os.Stat(output)
	require.NoError(t, err)
}

func TestKnowledgeSiloWindowTicks(t *testing.T) {
	// 6 months x 30.44 days, matching the leaf's own conversion.
	assert.InDelta(t, 182, knowledgeSiloWindowTicks(6, knowledgeSiloTestTickSize), 2)
	assert.Zero(t, knowledgeSiloWindowTicks(6, 0))
	assert.Zero(t, knowledgeSiloWindowTicks(0, knowledgeSiloTestTickSize))
}

// The title carries the selection rule, because nobody looking at the PNG later can tell a
// ranked chart from a fallback one otherwise.
func TestKnowledgeSiloTitleNamesTheSelection(t *testing.T) {
	assert.Equal(t, "Knowledge Silos (ranked by risk)", knowledgeSiloTitle("", true))
	assert.Equal(t, "repo - Knowledge Silos (ranked by risk)", knowledgeSiloTitle("repo", true))
	assert.Equal(t, "repo - Knowledge Silos (fewest editors first)", knowledgeSiloTitle("repo", false))
}
