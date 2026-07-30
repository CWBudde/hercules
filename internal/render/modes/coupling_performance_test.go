package modes

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestAnalyzeCouplingPairsKeepsDeterministicTopK(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	matrix := readers.SparseMatrixFromDense([][]int{
		{1, 5, 9, 0, 0},
		{5, 1, 9, 0, 0},
		{9, 9, 1, 7, 0},
		{0, 0, 7, 1, 9},
		{0, 0, 0, 9, 1},
	})
	want := []commonCouplingPair{
		{Name1: "a", Name2: "c", Score: 9, Count: 9},
		{Name1: "b", Name2: "c", Score: 9, Count: 9},
		{Name1: "d", Name2: "e", Score: 9, Count: 9},
	}

	for range 100 {
		pairs, stats := analyzeCouplingPairs(names, matrix, 3)
		require.Equal(t, want, pairs)
		require.Equal(t, commonCouplingStats{
			Total: 39, Average: 7.8, Max: 9, Min: 5,
		}, stats)
	}
}

func TestTopCouplingHeatmapEntriesBoundsDenseRenderingAtTenThousandEntities(t *testing.T) {
	const entities = 10_000
	names := make([]string, entities)
	entries := make([]readers.SparseEntry, entities)
	for index := range names {
		names[index] = fmt.Sprintf("entity-%05d", index)
		entries[index] = readers.SparseEntry{
			Row: index, Column: index, Value: index + 1,
		}
	}
	matrix, err := readers.NewSparseMatrix(entities, entities, entries)
	require.NoError(t, err)

	shownNames, dense := topCouplingHeatmapEntries(
		names, matrix, MaxDenseCouplingChartEntries,
	)

	require.Len(t, shownNames, MaxDenseCouplingChartEntries)
	require.Len(t, dense, MaxDenseCouplingChartEntries)
	require.Equal(t, "entity-09999", shownNames[0])
	require.Equal(t, "entity-09940", shownNames[len(shownNames)-1])
	for row := range dense {
		require.Len(t, dense[row], MaxDenseCouplingChartEntries)
		require.Equal(t, entities-row, dense[row][row])
	}
}

func BenchmarkAnalyzeCouplingPairsSparse(b *testing.B) {
	for _, entities := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("entities_%d", entities), func(b *testing.B) {
			names := make([]string, entities)
			for index := range names {
				names[index] = fmt.Sprintf("entity-%d", index)
			}
			matrix := benchmarkSparseCoupling(entities, 4)
			b.ReportAllocs()
			b.ReportMetric(float64(matrix.NonZeroCount()), "nonzero")
			b.ResetTimer()
			for range b.N {
				pairs, _ := analyzeCouplingPairs(names, matrix, 20)
				if len(pairs) != 20 {
					b.Fatalf("got %d pairs, want 20", len(pairs))
				}
			}
		})
	}
}

func benchmarkSparseCoupling(entities, degree int) readers.SparseMatrix {
	entries := make([]readers.SparseEntry, 0, 2*entities*degree)
	for row := range entities {
		for offset := 1; offset <= degree && row+offset < entities; offset++ {
			column := row + offset
			value := 1 + (row+column)%100
			entries = append(
				entries,
				readers.SparseEntry{Row: row, Column: column, Value: value},
				readers.SparseEntry{Row: column, Column: row, Value: value},
			)
		}
	}
	matrix, err := readers.NewSparseMatrix(entities, entities, entries)
	if err != nil {
		panic(err)
	}
	return matrix
}
