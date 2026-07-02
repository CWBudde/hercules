package modes

import (
	"testing"

	"github.com/meko-christian/hercules/internal/render/readers"
)

// TestRepositoryBands verifies that each repository becomes its own stacked
// band holding the repository's total surviving lines of code per sample
// (sum over age bands), aligned to the widest sample grid.
func TestRepositoryBands(t *testing.T) {
	matrix, labels := repositoryBands([]readers.RepositoryBurndown{
		{
			Repository: "repo-a",
			Matrix: [][]int{
				{1, 2},
				{3, 4},
			},
		},
		{
			Repository: "repo-b",
			Matrix: [][]int{
				{10},
				{20, 30},
				{40, 50},
			},
		},
	})

	wantLabels := []string{"repo-a", "repo-b"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("labels = %v, want %v", labels, wantLabels)
	}
	for i := range wantLabels {
		if labels[i] != wantLabels[i] {
			t.Fatalf("labels[%d] = %q, want %q", i, labels[i], wantLabels[i])
		}
	}

	// repo-a per-sample totals: col0 = 1+3 = 4, col1 = 2+4 = 6.
	// repo-b per-sample totals: col0 = 10+20+40 = 70, col1 = 0+30+50 = 80.
	want := [][]float64{
		{4, 6},
		{70, 80},
	}
	if len(matrix) != len(want) {
		t.Fatalf("matrix rows = %d, want %d", len(matrix), len(want))
	}
	for i := range want {
		if len(matrix[i]) != len(want[i]) {
			t.Fatalf("matrix row %d columns = %d, want %d", i, len(matrix[i]), len(want[i]))
		}
		for j := range want[i] {
			if matrix[i][j] != want[i][j] {
				t.Fatalf("matrix[%d][%d] = %v, want %v", i, j, matrix[i][j], want[i][j])
			}
		}
	}
}
