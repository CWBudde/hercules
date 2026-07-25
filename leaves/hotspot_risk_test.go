package leaves

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
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
	assert.InDelta(t, 0.5, risks[0].RiskScore, 1e-12)

	assert.InDelta(t, 0.49892198580547814, risks[1].SizeNormalized, 1e-12)
	assert.InDelta(t, 0.5, risks[1].ChurnNormalized, 1e-12)
	assert.InDelta(t, 0.5, risks[1].CouplingNormalized, 1e-12)
	assert.InDelta(t, 0.25, risks[1].OwnershipNormalized, 1e-12)
	assert.InDelta(t, 0.031182624112842384, risks[1].RiskScore, 1e-12)
}

func TestHotspotRiskFinalizeAppliesWindowAndTopN(t *testing.T) {
	repo, head := newHotspotRiskFixtureRepository(t, map[string]string{
		testAlphaPath: lines(20),
		testBetaPath:  lines(10),
		testGammaPath: lines(5),
	})

	hra := HotspotRiskAnalysis{TopN: 2, WindowDays: 2}
	require.NoError(t, hra.Configure(map[string]any{items.FactTickSize: int64(24 * 60 * 60)}))
	require.NoError(t, hra.Initialize(repo))

	consumeHotspotRiskCommit(t, &hra, head, 0, 0, []string{testAlphaPath, testBetaPath}, map[string]items.LineStats{
		testAlphaPath: {Added: 10},
		testBetaPath:  {Added: 10},
	})
	consumeHotspotRiskCommit(t, &hra, head, 1, 1, []string{testAlphaPath, testGammaPath}, map[string]items.LineStats{
		testAlphaPath: {Added: 5},
		testGammaPath: {Added: 5},
	})
	consumeHotspotRiskCommit(
		t,
		&hra,
		head,
		2,
		4,
		[]string{testAlphaPath, testBetaPath, testGammaPath},
		map[string]items.LineStats{
			testAlphaPath: {Added: 5},
			testBetaPath:  {Added: 5},
			testGammaPath: {Added: 5},
		},
	)

	result := hra.Finalize().(HotspotRiskResult)

	require.Len(t, result.Files, 2)
	assert.Equal(t, 2, result.WindowDays)

	alpha := result.Files[0]
	assert.Equal(t, testAlphaPath, alpha.Path)
	assert.Equal(t, 20, alpha.Size)
	assert.Equal(t, 1, alpha.Churn, "only the tick inside the two-day window should count")
	assert.Equal(t, 2, alpha.CouplingDegree)
	assert.InDelta(t, 0.16666666666666674, alpha.OwnershipGini, 1e-12)
	assert.InDelta(t, 1.0, alpha.SizeNormalized, 1e-12)
	assert.InDelta(t, 1.0, alpha.ChurnNormalized, 1e-12)
	assert.InDelta(t, 1.0, alpha.CouplingNormalized, 1e-12)
	assert.InDelta(t, 0.16666666666666674, alpha.OwnershipNormalized, 1e-12)
	assert.InDelta(t, 0.16666666666666674, alpha.RiskScore, 1e-12)

	assert.Equal(t, testBetaPath, result.Files[1].Path)
	assert.Equal(t, 1, result.Files[1].Churn)
	assert.InDelta(t, 0.13126827616087608, result.Files[1].RiskScore, 1e-12)
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

func newHotspotRiskFixtureRepository(t *testing.T, files map[string]string) (*git.Repository, *object.Commit) {
	t.Helper()
	storage := memory.NewStorage()
	fs := memfs.New()
	repo, err := git.Init(storage, fs)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	for name, content := range files {
		writeFixtureFile(t, fs, name, content)
		_, err := worktree.Add(name)
		require.NoError(t, err)
	}
	hash, err := worktree.Commit("fixture", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "tester@example.com"},
	})
	require.NoError(t, err)
	commit, err := repo.CommitObject(hash)
	require.NoError(t, err)
	return repo, commit
}

func writeFixtureFile(t *testing.T, fs billy.Filesystem, name, content string) {
	t.Helper()
	file, err := fs.Create(name)
	require.NoError(t, err)
	_, err = file.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func lines(count int) string {
	var buf bytes.Buffer
	for i := range count {
		fmt.Fprintf(&buf, "line %d\n", i)
	}
	return buf.String()
}

func consumeHotspotRiskCommit(
	t *testing.T,
	hra *HotspotRiskAnalysis,
	commit *object.Commit,
	author int,
	tick int,
	paths []string,
	statsByPath map[string]items.LineStats,
) {
	t.Helper()
	changes := make(object.Changes, len(paths))
	lineStats := make(map[object.ChangeEntry]items.LineStats, len(statsByPath))
	for i, path := range paths {
		changes[i] = makeHotspotModifyChange(path)
		if stats, ok := statsByPath[path]; ok {
			lineStats[object.ChangeEntry{Name: path}] = stats
		}
	}
	result, err := hra.Consume(map[string]any{
		core.DependencyCommit:       commit,
		core.DependencyIsMerge:      false,
		items.DependencyTreeChanges: changes,
		items.DependencyLineStats:   lineStats,
		identity.DependencyAuthor:   author,
		items.DependencyTick:        tick,
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func makeHotspotModifyChange(path string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      path,
			TreeEntry: object.TreeEntry{Name: path, Hash: plumbing.NewHash("1111111111111111111111111111111111111111")},
		},
		To: object.ChangeEntry{
			Name:      path,
			TreeEntry: object.TreeEntry{Name: path, Hash: plumbing.NewHash("2222222222222222222222222222222222222222")},
		},
	}
}
