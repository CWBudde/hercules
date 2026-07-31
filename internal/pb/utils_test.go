package pb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDenseToCompressedSparseRowMatrixEmpty pins the regression where a non-nil but
// zero-row matrix panicked on matrix[0]. It is reachable because the burndown
// serializers guard on `!= nil`, and deserializing a payload whose PeopleInteraction
// declares NumberOfRows == 0 produces exactly such a matrix.
func TestDenseToCompressedSparseRowMatrixEmpty(t *testing.T) {
	var result *CompressedSparseRowMatrix

	require.NotPanics(t, func() {
		result = DenseToCompressedSparseRowMatrix([][]int64{})
	})

	require.NotNil(t, result)
	assert.Equal(t, int32(0), result.GetNumberOfRows())
	assert.Equal(t, int32(0), result.GetNumberOfColumns())
	assert.Empty(t, result.GetData())
	assert.Empty(t, result.GetIndices())
	assert.Equal(t, []int64{0}, result.GetIndptr())
}

// TestDenseToCompressedSparseRowMatrixKeepsNonEmptyBehaviour guards the fast path from
// the empty-matrix guard above.
func TestDenseToCompressedSparseRowMatrixKeepsNonEmptyBehaviour(t *testing.T) {
	result := DenseToCompressedSparseRowMatrix([][]int64{
		{1, 0, 2},
		{0, 0, 0},
		{0, 3, 0},
	})

	require.NotNil(t, result)
	assert.Equal(t, int32(3), result.GetNumberOfRows())
	assert.Equal(t, int32(3), result.GetNumberOfColumns())
	assert.Equal(t, []int64{1, 2, 3}, result.GetData())
	assert.Equal(t, []int32{0, 2, 1}, result.GetIndices())
	assert.Equal(t, []int64{0, 2, 2, 3}, result.GetIndptr())
}
