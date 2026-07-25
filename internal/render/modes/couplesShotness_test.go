package modes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeShotnessCouplingUsesHeatmapMatrixForRankedPairs(t *testing.T) {
	names := []string{"a.go:alpha", "b.go:beta", "c.go:gamma"}
	matrix := [][]int{
		{10, 6, 1},
		{6, 29, 20},
		{1, 20, 17},
	}

	analysis := analyzeShotnessCoupling(names, matrix)

	require.Equal(t, names, analysis.EntityNames)
	require.Equal(t, matrix, analysis.CouplingMatrix)
	require.Equal(t, []ShotnessCouplingPair{
		{
			Entity1:          "b.go:beta",
			Entity2:          "c.go:gamma",
			CouplingScore:    20,
			CooccuranceCount: 20,
		},
		{
			Entity1:          "a.go:alpha",
			Entity2:          "b.go:beta",
			CouplingScore:    6,
			CooccuranceCount: 6,
		},
		{
			Entity1:          "a.go:alpha",
			Entity2:          "c.go:gamma",
			CouplingScore:    1,
			CooccuranceCount: 1,
		},
	}, analysis.TopCoupling)

	for _, pair := range analysis.TopCoupling {
		i := indexOfEntity(t, names, pair.Entity1)
		j := indexOfEntity(t, names, pair.Entity2)
		require.Equal(t, matrix[i][j], pair.CooccuranceCount)
		require.Equal(t, float64(matrix[i][j]), pair.CouplingScore)
	}
}

func TestAnalyzeShotnessCouplingSingleEntityHasNoPairStatistics(t *testing.T) {
	analysis := analyzeShotnessCoupling([]string{"a.go:alpha"}, [][]int{{9}})

	require.Empty(t, analysis.TopCoupling)
	require.Equal(t, 1, analysis.Statistics.TotalEntities)
	require.Zero(t, analysis.Statistics.TotalCouplings)
	require.Zero(t, analysis.Statistics.AverageCoupling)
	require.Zero(t, analysis.Statistics.MaxCoupling)
	require.Zero(t, analysis.Statistics.MinCoupling)
}

func indexOfEntity(t *testing.T, names []string, name string) int {
	t.Helper()
	for i, candidate := range names {
		if candidate == name {
			return i
		}
	}
	t.Fatalf("entity %q is not present in heatmap labels", name)
	return -1
}
