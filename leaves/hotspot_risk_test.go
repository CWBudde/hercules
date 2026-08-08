package leaves

import (
	"bytes"
	"fmt"
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

// MergeResults rescales every factor against the union's maxima, so the fixtures below rank
// by the raw factors a real run carries rather than by a hand-set RiskScore, which the merge
// recomputes. OwnershipGini is the convenient one: it is already in [0,1] and passes through
// normalization unchanged, so it orders the merged list on its own.
func TestHotspotRiskMergeResultsSortsEqualScoresByPath(t *testing.T) {
	hra := HotspotRiskAnalysis{TopN: 2}

	result := hra.MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			TopN:       2,
			Files: []FileRisk{
				{Path: "zeta.go", OwnershipGini: 0.5},
			},
		},
		HotspotRiskResult{
			WindowDays: 90,
			TopN:       2,
			Files: []FileRisk{
				{Path: testAlphaPath, OwnershipGini: 0.5},
			},
		},
		nil,
		nil,
	).(HotspotRiskResult)

	require.Len(t, result.Files, 2)
	assert.Equal(t, testAlphaPath, result.Files[0].Path)
	assert.Equal(t, "zeta.go", result.Files[1].Path)
}

// TestHotspotRiskMergeResultsOnZeroValuedAnalysis exercises the `hercules combine` path:
// there the item is summoned via core.Registry.Summon (reflect.New), so neither Configure
// nor Initialize runs and TopN is 0. Truncating against the raw field emptied every merged
// result.
func TestHotspotRiskMergeResultsOnZeroValuedAnalysis(t *testing.T) {
	hra := &HotspotRiskAnalysis{}
	require.Zero(t, hra.TopN, "the combine path never initializes the item")

	result, ok := hra.MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			Files: []FileRisk{
				{Path: "mid.go", OwnershipGini: 0.5},
				{Path: "low.go", OwnershipGini: 0.1},
			},
		},
		HotspotRiskResult{
			WindowDays: 90,
			Files: []FileRisk{
				{Path: "high.go", OwnershipGini: 0.9},
			},
		},
		nil,
		nil,
	).(HotspotRiskResult)
	require.True(t, ok, "merging two valid results must not return an error")

	require.Len(t, result.Files, 3, "the merged list must not be truncated to zero")
	assert.Equal(t, []string{"high.go", "mid.go", "low.go"},
		[]string{result.Files[0].Path, result.Files[1].Path, result.Files[2].Path})
	assert.Equal(t, 90, result.WindowDays)

	// More than DefaultTopN files must still be capped at DefaultTopN.
	const fileCount = DefaultTopN + 7

	first := HotspotRiskResult{WindowDays: 90}
	second := HotspotRiskResult{WindowDays: 90}

	for i := range fileCount {
		risk := FileRisk{
			Path:          fmt.Sprintf("file%02d.go", i),
			OwnershipGini: float64(fileCount-i) / float64(fileCount),
		}
		if i%2 == 0 {
			first.Files = append(first.Files, risk)
		} else {
			second.Files = append(second.Files, risk)
		}
	}

	capped, ok := (&HotspotRiskAnalysis{}).MergeResults(first, second, nil, nil).(HotspotRiskResult)
	require.True(t, ok)
	require.Len(t, capped.Files, DefaultTopN)
	assert.Equal(t, "file00.go", capped.Files[0].Path, "the highest score must survive")
	assert.Equal(t, fmt.Sprintf("file%02d.go", DefaultTopN-1),
		capped.Files[DefaultTopN-1].Path, "the cap must keep the top scores")
}

func TestHotspotRiskMergeResultsRejectsWindowMismatch(t *testing.T) {
	hra := &HotspotRiskAnalysis{}

	merged := hra.MergeResults(
		HotspotRiskResult{WindowDays: 90, Files: []FileRisk{{Path: testAlphaPath}}},
		HotspotRiskResult{WindowDays: 30, Files: []FileRisk{{Path: testMainPath}}},
		nil,
		nil,
	)

	err, isErr := merged.(error)
	require.True(t, isErr, "combine detects failures by type-asserting the returned any to error")
	require.ErrorIs(t, err, errHotspotRiskWindowMismatch)
	assert.Contains(t, err.Error(), "r1: 90")
	assert.Contains(t, err.Error(), "r2: 30")
}

// TestHotspotRiskMergeResultsHonoursConfiguredTopN pins T1.2(a): combine summons the item
// zero-valued, so before the results carried TopN a run configured with
// --hotspot-risk-top=200 was still truncated to DefaultTopN.
func TestHotspotRiskMergeResultsHonoursConfiguredTopN(t *testing.T) {
	const configuredTopN = DefaultTopN + 13

	weights := HotspotRiskWeights{Size: 1, Churn: 1, Coupling: 1, Ownership: 1}
	first := HotspotRiskResult{WindowDays: 90, TopN: configuredTopN, Weights: weights}
	second := HotspotRiskResult{WindowDays: 90, TopN: configuredTopN, Weights: weights}

	for i := range configuredTopN + 5 {
		risk := FileRisk{
			Path:          fmt.Sprintf("file%02d.go", i),
			OwnershipGini: float64(configuredTopN+5-i) / float64(configuredTopN+5),
		}
		if i%2 == 0 {
			first.Files = append(first.Files, risk)
		} else {
			second.Files = append(second.Files, risk)
		}
	}

	merged, ok := (&HotspotRiskAnalysis{}).MergeResults(first, second, nil, nil).(HotspotRiskResult)
	require.True(t, ok)
	assert.Len(t, merged.Files, configuredTopN,
		"the merge must truncate to the configured length, not to DefaultTopN")
	assert.Equal(t, configuredTopN, merged.TopN, "the merged result carries the setting on")
}

// TestHotspotRiskMergeResultsDeduplicatesEqualPaths pins T1.2(b). Paths are repository
// qualified before the merge (QualifyPaths), so an equal path denotes the same file; the
// org-wide report showed README.md twice because nothing collapsed them.
func TestHotspotRiskMergeResultsDeduplicatesEqualPaths(t *testing.T) {
	weights := HotspotRiskWeights{Size: 1, Churn: 1, Coupling: 1, Ownership: 1}

	merged, ok := (&HotspotRiskAnalysis{}).MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			Weights:    weights,
			Files: []FileRisk{
				{Path: testAlphaPath, Size: 10, OwnershipGini: 0.2},
				{Path: testBetaPath, Size: 5, OwnershipGini: 0.1},
			},
		},
		HotspotRiskResult{
			WindowDays: 90,
			Weights:    weights,
			Files: []FileRisk{
				{Path: testAlphaPath, Size: 40, OwnershipGini: 0.9},
			},
		},
		nil,
		nil,
	).(HotspotRiskResult)
	require.True(t, ok)

	require.Len(t, merged.Files, 2, "the two alpha.go rows must collapse into one")
	assert.Equal(t, testAlphaPath, merged.Files[0].Path)
	assert.Equal(t, 40, merged.Files[0].Size, "the higher-scoring entry wins")
	assert.Equal(t, testBetaPath, merged.Files[1].Path)
}

// TestHotspotRiskMergeResultsRescalesAcrossRuns pins T1.2(c). Each run normalizes against
// its own maxima, so on the 231-repository merge every surviving RiskScore was exactly 1.0.
// The merge must rescale against the union before ranking.
func TestHotspotRiskMergeResultsRescalesAcrossRuns(t *testing.T) {
	weights := HotspotRiskWeights{Size: 1, Churn: 1, Coupling: 1, Ownership: 1}
	small := FileRisk{Path: "small-repo-top.go", Size: 100, Churn: 10, RiskScore: 1}
	large := FileRisk{Path: "large-repo-top.go", Size: 10000, Churn: 1000, RiskScore: 1}

	merged, ok := (&HotspotRiskAnalysis{}).MergeResults(
		HotspotRiskResult{WindowDays: 90, Weights: weights, Files: []FileRisk{small}},
		HotspotRiskResult{WindowDays: 90, Weights: weights, Files: []FileRisk{large}},
		nil,
		nil,
	).(HotspotRiskResult)
	require.True(t, ok)

	require.Len(t, merged.Files, 2)
	assert.Equal(t, large.Path, merged.Files[0].Path,
		"the larger repository's file outranks the smaller one's after rescaling")
	assert.Greater(t, merged.Files[0].RiskScore, merged.Files[1].RiskScore,
		"both files came in at 1.0; rescaling must separate them")
	assert.InDelta(t, 1.0, merged.Files[0].ChurnNormalized, 1e-12,
		"the union maximum defines the top of the scale")
	assert.InDelta(t, 0.01, merged.Files[1].ChurnNormalized, 1e-12)
}

func TestHotspotRiskMergeResultsRejectsWeightMismatch(t *testing.T) {
	merged := (&HotspotRiskAnalysis{}).MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			Weights:    HotspotRiskWeights{Size: 1, Churn: 1, Coupling: 1, Ownership: 1},
		},
		HotspotRiskResult{
			WindowDays: 90,
			Weights:    HotspotRiskWeights{Size: 1, Churn: 1, Coupling: 0, Ownership: 1},
		},
		nil,
		nil,
	)

	err, isErr := merged.(error)
	require.True(t, isErr, "combine detects failures by type-asserting the returned any to error")
	require.ErrorIs(t, err, errHotspotRiskWeightMismatch)
}

// TestHotspotRiskMergeResultsAdoptsWeightsFromCarryingSide covers the upgrade path: a `.pb`
// written before the weights were serialized decodes as all-zero, which means "unset", not
// "every factor disabled". Erroring on it would make results from two hercules versions
// impossible to combine.
func TestHotspotRiskMergeResultsAdoptsWeightsFromCarryingSide(t *testing.T) {
	configured := HotspotRiskWeights{Size: 1, Churn: 0, Coupling: 0, Ownership: 0}

	merged, ok := (&HotspotRiskAnalysis{}).MergeResults(
		HotspotRiskResult{
			WindowDays: 90,
			Files:      []FileRisk{{Path: testAlphaPath, Size: 100}},
		},
		HotspotRiskResult{
			WindowDays: 90,
			Weights:    configured,
			Files:      []FileRisk{{Path: testBetaPath, Size: 10}},
		},
		nil,
		nil,
	).(HotspotRiskResult)
	require.True(t, ok, "an unset side must not be a merge error")

	assert.Equal(t, configured, merged.Weights)
	assert.InDelta(t, merged.Files[0].SizeNormalized, merged.Files[0].RiskScore, 1e-12,
		"size is the only enabled factor, so it is the whole score")
}

// TestHotspotRiskMergeResultsDefaultsWeightsWhenNeitherSideCarriesThem covers two
// pre-upgrade inputs: with no weights anywhere the merge must fall back to DefaultWeight
// rather than score every file zero.
func TestHotspotRiskMergeResultsDefaultsWeightsWhenNeitherSideCarriesThem(t *testing.T) {
	merged, ok := (&HotspotRiskAnalysis{}).MergeResults(
		HotspotRiskResult{WindowDays: 90, Files: []FileRisk{{Path: testAlphaPath, Size: 100}}},
		HotspotRiskResult{WindowDays: 90, Files: []FileRisk{{Path: testBetaPath, Size: 10}}},
		nil,
		nil,
	).(HotspotRiskResult)
	require.True(t, ok)

	assert.Equal(t, HotspotRiskWeights{
		Size: DefaultWeight, Churn: DefaultWeight,
		Coupling: DefaultWeight, Ownership: DefaultWeight,
	}, merged.Weights)
	assert.Positive(t, merged.Files[0].RiskScore, "defaults must produce a real ranking")
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

	normalizeAndScore(risks, hra.weights())

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
	normalizeAndScore(risks, hra.weights())

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
	normalizeAndScore(risks, allDisabled.weights())
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
		TopN:       50,
		Weights: HotspotRiskWeights{
			Size:      1,
			Churn:     2,
			Coupling:  0,
			Ownership: 0.5,
		},
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
	assert.Contains(t, text.String(), "  top_n: 50\n")
	assert.Contains(t, text.String(), "    churn: 2.000000\n")
	assert.Contains(t, text.String(), "    ownership: 0.500000\n")
	assert.Contains(t, text.String(), "    - path: \"main.go\"\n")
	assert.Contains(t, text.String(), "      risk_score: 0.250000\n")
	assert.Contains(t, text.String(), "      normalized:\n")
	assert.Contains(t, text.String(), "        ownership: 0.750000\n")

	var binary bytes.Buffer
	require.NoError(t, hra.Serialize(result, true, &binary))
	message := pb.HotspotRiskResults{}
	require.NoError(t, proto.Unmarshal(binary.Bytes(), &message))
	assert.Equal(t, int32(30), message.GetWindowDays())
	assert.Equal(t, int32(50), message.GetTopN())
	assert.InDelta(t, 1.0, message.GetWeightSize(), 0.00001)
	assert.InDelta(t, 2.0, message.GetWeightChurn(), 0.00001)
	assert.Zero(t, message.GetWeightCoupling())
	assert.InDelta(t, 0.5, message.GetWeightOwnership(), 0.00001)
	require.Len(t, message.GetFiles(), 1)
	assert.Equal(t, testMainPath, message.GetFiles()[0].GetPath())
	assert.InDelta(t, 0.25, message.GetFiles()[0].GetRiskScore(), 0.00001)
	assert.Equal(t, int32(12), message.GetFiles()[0].GetSize_())
	assert.Equal(t, int32(3), message.GetFiles()[0].GetChurn())
	assert.Equal(t, int32(2), message.GetFiles()[0].GetCouplingDegree())
	assert.InDelta(t, 0.75, message.GetFiles()[0].GetOwnershipGini(), 0.00001)
	assert.InDelta(t, 1.0, message.GetFiles()[0].GetSizeNormalized(), 0.00001)

	roundTripped, err := hra.Deserialize(binary.Bytes())
	require.NoError(t, err)
	assert.Equal(t, result, roundTripped,
		"the configuration MergeResults reads must survive the protobuf round trip")
}

func writeFixtureFile(t *testing.T, fs billy.Filesystem, name, content string) {
	t.Helper()
	file, err := fs.Create(name)
	require.NoError(t, err)
	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
