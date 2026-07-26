package readers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

func TestSparseMatrixCanonicalizesEntriesAndSelectsDenseSubset(t *testing.T) {
	matrix, err := NewSparseMatrix(4, 4, []SparseEntry{
		{Row: 2, Column: 1, Value: 5},
		{Row: 0, Column: 3, Value: 7},
		{Row: 2, Column: 1, Value: -2},
		{Row: 1, Column: 1, Value: 0},
	})
	require.NoError(t, err)
	require.Equal(t, 2, matrix.NonZeroCount())
	require.Equal(t, 3, matrix.At(2, 1))
	require.Zero(t, matrix.At(3, 3))
	require.Equal(t, [][]int{
		{0, 3, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 7},
		{0, 0, 0, 0},
	}, matrix.DenseSubset([]int{2, 1, 0, 3}))
}

func TestCompressedSparseCouplingMatrixKeepsTenThousandRowsSparse(t *testing.T) {
	const size = 10_000
	indptr := make([]int64, size+1)
	indices := make([]int32, size)
	values := make([]int64, size)
	for index := 0; index < size; index++ {
		indptr[index+1] = int64(index + 1)
		indices[index] = int32(index)
		values[index] = int64(index + 1)
	}

	matrix, err := parseCompressedSparseCouplingMatrix(&pb.CompressedSparseRowMatrix{
		NumberOfRows:    size,
		NumberOfColumns: size,
		Indptr:          indptr,
		Indices:         indices,
		Data:            values,
	})
	require.NoError(t, err)
	require.Equal(t, size, matrix.Rows)
	require.Equal(t, size, matrix.Columns)
	require.Equal(t, size, matrix.NonZeroCount())
	require.Len(t, matrix.RowOffsets, size+1)
	require.Equal(t, size, matrix.At(size-1, size-1))
}

func TestSparseYAMLCouplingRowsRetainEmptyTrailingColumns(t *testing.T) {
	rows := []interface{}{
		map[string]interface{}{"0": 2, "3": 4},
		map[string]interface{}{},
		map[string]interface{}{},
		map[string]interface{}{"0": 4},
	}
	matrix, err := parseSparseCooccurrenceRows(rows, 4, analysisio.DefaultLimits())
	require.NoError(t, err)
	require.Equal(t, 4, matrix.Rows)
	require.Equal(t, 4, matrix.Columns)
	require.Equal(t, 3, matrix.NonZeroCount())
	require.Equal(t, 4, matrix.At(3, 0))
}

func TestSparseYAMLValidationUsesStoredEntryLimit(t *testing.T) {
	rows := make([]interface{}, 10_000)
	for index := range rows {
		rows[index] = map[string]interface{}{}
	}
	rows[0] = map[string]interface{}{"9999": 1}
	limits := analysisio.Limits{
		MaxDecodedCells: 1,
		MaxRows:         10_000,
		MaxColumns:      10_000,
	}
	require.NoError(t, validateYAMLSparseRows("large sparse YAML", rows, limits))

	rows[1] = map[string]interface{}{"9998": 1}
	require.ErrorIs(
		t,
		validateYAMLSparseRows("too many sparse YAML entries", rows, limits),
		ErrAnalysisTooLarge,
	)
}
