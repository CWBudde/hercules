package leaves

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

func TestHotspotRiskMergeResultsSortsEqualScoresByPath(t *testing.T) {
	hra := HotspotRiskAnalysis{TopN: 2}

	result := hra.MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			Files: []FileRisk{
				{Path: "zeta.go", RiskScore: 0.5},
			},
		},
		HotspotRiskResult{
			WindowDays: 90,
			Files: []FileRisk{
				{Path: testAlphaPath, RiskScore: 0.5},
			},
		},
		nil,
		nil,
	).(HotspotRiskResult)

	require.Len(t, result.Files, 2)
	assert.Equal(t, testAlphaPath, result.Files[0].Path)
	assert.Equal(t, "zeta.go", result.Files[1].Path)
}

func TestHotspotRiskNormalizeAndScoreScalesFactors(t *testing.T) {
	hra := HotspotRiskAnalysis{
		WeightSize:      1,
		WeightChurn:     1,
		WeightCoupling:  1,
		WeightOwnership: 1,
	}
	risks := []FileRisk{
		{Path: "large.go", Size: 100, Churn: 4, CouplingDegree: 2, OwnershipGini: 0.5},
		{Path: "small.go", Size: 9, Churn: 2, CouplingDegree: 1, OwnershipGini: 0.25},
	}

	hra.normalizeAndScore(risks)

	assert.InDelta(t, 1.0, risks[0].SizeNormalized, 1e-12)
	assert.InDelta(t, 1.0, risks[0].ChurnNormalized, 1e-12)
	assert.InDelta(t, 1.0, risks[0].CouplingNormalized, 1e-12)
	assert.InDelta(t, 0.5, risks[0].OwnershipNormalized, 1e-12)
	assert.InDelta(t, 0.875, risks[0].RiskScore, 1e-12)

	assert.InDelta(t, 0.49892198580547814, risks[1].SizeNormalized, 1e-12)
	assert.InDelta(t, 0.5, risks[1].ChurnNormalized, 1e-12)
	assert.InDelta(t, 0.5, risks[1].CouplingNormalized, 1e-12)
	assert.InDelta(t, 0.25, risks[1].OwnershipNormalized, 1e-12)
	assert.InDelta(t, 0.4372304964513695, risks[1].RiskScore, 1e-12)
}

func TestHotspotRiskDisabledFactorsRemainDisabled(t *testing.T) {
	hra := HotspotRiskAnalysis{}
	require.NoError(t, hra.Configure(map[string]any{
		ConfigHotspotRiskWeightSize:      float32(1),
		ConfigHotspotRiskWeightChurn:     float32(0),
		ConfigHotspotRiskWeightCoupling:  float32(0),
		ConfigHotspotRiskWeightOwnership: float32(0),
		items.FactTickSize:               6 * time.Hour,
	}))
	require.NoError(t, hra.Initialize(nil))

	risks := []FileRisk{
		{Path: "large.go", Size: 100, Churn: 0, CouplingDegree: 0, OwnershipGini: 0},
		{Path: "small.go", Size: 9, Churn: 100, CouplingDegree: 10, OwnershipGini: 1},
	}
	hra.normalizeAndScore(risks)

	assert.InDelta(t, 1, risks[0].RiskScore, 1e-12)
	assert.InDelta(t, risks[1].SizeNormalized, risks[1].RiskScore, 1e-12)

	allDisabled := HotspotRiskAnalysis{}
	require.NoError(t, allDisabled.Configure(map[string]any{
		ConfigHotspotRiskWeightSize:      float32(0),
		ConfigHotspotRiskWeightChurn:     float32(0),
		ConfigHotspotRiskWeightCoupling:  float32(0),
		ConfigHotspotRiskWeightOwnership: float32(0),
		items.FactTickSize:               6 * time.Hour,
	}))
	require.NoError(t, allDisabled.Initialize(nil))
	allDisabled.normalizeAndScore(risks)
	assert.Zero(t, risks[0].RiskScore)
	assert.Zero(t, risks[1].RiskScore)
}

func TestHotspotRiskPipelineProducesExplainableScores(t *testing.T) {
	repository, commits := newHotspotRiskPipelineFixture(t)

	oneDay := runHotspotRiskPipeline(t, repository, commits, 1, 10)
	require.Len(t, oneDay.Files, 3, "the binary file must not be scored")

	alpha := oneDay.Files[0]
	assert.Equal(t, testAlphaPath, alpha.Path)
	assert.Equal(t, 3, alpha.Size)
	assert.Equal(t, 2, alpha.Churn, "only the final deletion is inside the one-day window")
	assert.Equal(t, 1, alpha.CouplingDegree)
	assert.InDelta(t, 1.0/6.0, alpha.OwnershipGini, 1e-12)
	assert.InDelta(t, 1, alpha.SizeNormalized, 1e-12)
	assert.InDelta(t, 1, alpha.ChurnNormalized, 1e-12)
	assert.InDelta(t, 1, alpha.CouplingNormalized, 1e-12)
	assert.InDelta(t, 1.0/6.0, alpha.OwnershipNormalized, 1e-12)
	assert.InDelta(t, 19.0/24.0, alpha.RiskScore, 1e-12)

	renamed := oneDay.Files[1]
	assert.Equal(t, "renamed.go", renamed.Path)
	assert.Equal(t, 2, renamed.Size)
	assert.Zero(t, renamed.Churn)
	assert.Equal(t, 1, renamed.CouplingDegree)
	assert.InDelta(t, 1, renamed.OwnershipGini, 1e-12)
	assert.InDelta(t, (math.Log(3)/math.Log(4)+2)/4, renamed.RiskScore, 1e-12)

	solo := oneDay.Files[2]
	assert.Equal(t, "solo.go", solo.Path)
	assert.Zero(t, solo.Churn)
	assert.Zero(t, solo.CouplingDegree, "a file changed alone has no coupling")
	assert.InDelta(t, 0.5, solo.RiskScore, 1e-12)

	twoDays := runHotspotRiskPipeline(t, repository, commits, 2, 10)
	require.Len(t, twoDays.Files, 3)
	assert.Equal(t, testAlphaPath, twoDays.Files[0].Path)
	assert.Equal(t, 6, twoDays.Files[0].Churn,
		"the two-day window includes the edit 36 hours before the final tick")

	multiDayTicks := runHotspotRiskPipeline(t, repository, commits, 1, 72)
	risksByPath := map[string]FileRisk{}
	for _, risk := range multiDayTicks.Files {
		risksByPath[risk.Path] = risk
	}
	assert.Equal(t, 6, risksByPath[testAlphaPath].Churn,
		"the current multi-day tick is complete even though its older edit is outside one day")
	assert.Zero(t, risksByPath["renamed.go"].Churn,
		"the preceding multi-day tick is outside the one-day window")

	fullHistory := runHotspotRiskPipeline(t, repository, commits, 3, 10)
	risksByPath = map[string]FileRisk{}
	for _, risk := range fullHistory.Files {
		risksByPath[risk.Path] = risk
	}
	assert.Equal(t, 2, risksByPath["renamed.go"].Churn,
		"rename moves the original path's churn history to the new path")
}

func TestHotspotRiskRejectsInvalidDurationAndWeights(t *testing.T) {
	hra := HotspotRiskAnalysis{}
	require.Error(t, hra.Configure(map[string]any{items.FactTickSize: time.Duration(0)}))
	require.Error(t, hra.Configure(map[string]any{items.FactTickSize: -time.Hour}))
	require.Error(t, hra.Configure(map[string]any{ConfigHotspotRiskWindow: 0}))
	require.Error(t, hra.Configure(map[string]any{ConfigHotspotRiskWeightSize: float32(-1)}))
}

func runHotspotRiskPipeline(
	t *testing.T,
	repository *git.Repository,
	commits []*object.Commit,
	windowDays int,
	tickSizeHours int,
) HotspotRiskResult {
	t.Helper()

	pipeline := core.NewPipeline(repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	analysis := pipeline.DeployItem(&HotspotRiskAnalysis{}).(*HotspotRiskAnalysis)
	require.NoError(t, pipeline.Initialize(map[string]any{
		core.ConfigPipelineCommits:          commits,
		items.ConfigTicksSinceStartTickSize: tickSizeHours,
		ConfigHotspotRiskWindow:             windowDays,
	}))

	results, err := pipeline.Run(commits)
	require.NoError(t, err)
	result, ok := results[analysis].(HotspotRiskResult)
	require.True(t, ok)

	var serialized bytes.Buffer
	require.NoError(t, analysis.Serialize(result, false, &serialized))
	assert.Contains(t, serialized.String(), "risk_score:")

	return result
}

func newHotspotRiskPipelineFixture(t *testing.T) (*git.Repository, []*object.Commit) {
	t.Helper()

	storage := memory.NewStorage()
	fs := memfs.New()
	repository, err := git.Init(storage, fs)
	require.NoError(t, err)
	worktree, err := repository.Worktree()
	require.NoError(t, err)

	alice := object.Signature{
		Name: "Alice", Email: "alice@example.com",
		When: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	bob := object.Signature{Name: "Bob", Email: "bob@example.com"}

	writeFixtureFile(t, fs, testAlphaPath, "a0\na1\na2\na3\n")
	writeFixtureFile(t, fs, testBetaPath, "p0\np1\n")
	writeFixtureFile(t, fs, "image.bin", "\x00\x01\x02")
	addFixturePaths(t, worktree, testAlphaPath, testBetaPath, "image.bin")
	commits := make([]*object.Commit, 0, 4)
	commits = append(commits, commitHotspotFixture(t, repository, worktree, "initial files", alice))

	bob.When = alice.When.Add(6 * time.Hour)
	writeFixtureFile(t, fs, "solo.go", "s0\ns1\ns2\n")
	addFixturePaths(t, worktree, "solo.go")
	commits = append(commits, commitHotspotFixture(t, repository, worktree, "uncoupled file", bob))

	bob.When = alice.When.Add(24 * time.Hour)
	writeFixtureFile(t, fs, testAlphaPath, "a0\nb1\nb2\nb3\nb4\n")
	addFixturePaths(t, worktree, testAlphaPath)
	_, err = worktree.Move(testBetaPath, "renamed.go")
	require.NoError(t, err)
	commits = append(commits, commitHotspotFixture(t, repository, worktree, "edit and rename", bob))

	alice.When = alice.When.Add(60 * time.Hour)
	writeFixtureFile(t, fs, testAlphaPath, "a0\nb1\nb2\n")
	writeFixtureFile(t, fs, "image.bin", "\x00\x03\x04")
	addFixturePaths(t, worktree, testAlphaPath, "image.bin")
	commits = append(commits, commitHotspotFixture(t, repository, worktree, "delete lines", alice))

	return repository, commits
}

func addFixturePaths(t *testing.T, worktree *git.Worktree, paths ...string) {
	t.Helper()

	for _, path := range paths {
		_, err := worktree.Add(path)
		require.NoError(t, err)
	}
}

func commitHotspotFixture(
	t *testing.T,
	repository *git.Repository,
	worktree *git.Worktree,
	message string,
	signature object.Signature,
) *object.Commit {
	t.Helper()

	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &signature, Committer: &signature,
	})
	require.NoError(t, err)
	commit, err := repository.CommitObject(hash)
	require.NoError(t, err)

	return commit
}

func TestHotspotRiskSerializationShapes(t *testing.T) {
	hra := HotspotRiskAnalysis{}
	result := HotspotRiskResult{
		WindowDays: 30,
		Files: []FileRisk{
			{
				Path:                testMainPath,
				RiskScore:           0.25,
				Size:                12,
				Churn:               3,
				CouplingDegree:      2,
				OwnershipGini:       0.75,
				SizeNormalized:      1,
				ChurnNormalized:     0.5,
				CouplingNormalized:  0.25,
				OwnershipNormalized: 0.75,
			},
		},
	}

	var text bytes.Buffer
	require.NoError(t, hra.Serialize(result, false, &text))
	assert.Contains(t, text.String(), "  window_days: 30\n")
	assert.Contains(t, text.String(), "    - path: \"main.go\"\n")
	assert.Contains(t, text.String(), "      risk_score: 0.250000\n")
	assert.Contains(t, text.String(), "      normalized:\n")
	assert.Contains(t, text.String(), "        ownership: 0.750000\n")

	var binary bytes.Buffer
	require.NoError(t, hra.Serialize(result, true, &binary))
	message := pb.HotspotRiskResults{}
	require.NoError(t, proto.Unmarshal(binary.Bytes(), &message))
	assert.Equal(t, int32(30), message.GetWindowDays())
	require.Len(t, message.GetFiles(), 1)
	assert.Equal(t, testMainPath, message.GetFiles()[0].GetPath())
	assert.InDelta(t, 0.25, message.GetFiles()[0].GetRiskScore(), 0.00001)
	assert.Equal(t, int32(12), message.GetFiles()[0].GetSize_())
	assert.Equal(t, int32(3), message.GetFiles()[0].GetChurn())
	assert.Equal(t, int32(2), message.GetFiles()[0].GetCouplingDegree())
	assert.InDelta(t, 0.75, message.GetFiles()[0].GetOwnershipGini(), 0.00001)
	assert.InDelta(t, 1.0, message.GetFiles()[0].GetSizeNormalized(), 0.00001)
}

func writeFixtureFile(t *testing.T, fs billy.Filesystem, name, content string) {
	t.Helper()
	file, err := fs.Create(name)
	require.NoError(t, err)
	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
