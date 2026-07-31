package pb

import (
	"math"
	"sort"
)

func checkedInt32(value int, field string) int32 {
	if value < math.MinInt32 || value > math.MaxInt32 {
		panic(field + " exceeds the protobuf int32 range")
	}

	return int32(value)
}

func checkedUint32(value int64, field string) uint32 {
	if value < 0 || value > math.MaxUint32 {
		panic(field + " exceeds the protobuf uint32 range")
	}

	return uint32(value)
}

// ToBurndownSparseMatrix converts a rectangular integer matrix to the corresponding Protobuf object.
// It is specific to hercules.BurndownAnalysis.
func ToBurndownSparseMatrix(matrix [][]int64, name string) *BurndownSparseMatrix {
	if len(matrix) == 0 {
		panic("matrix may not be nil or empty")
	}

	result := BurndownSparseMatrix{
		Name:            name,
		NumberOfRows:    checkedInt32(len(matrix), "burndown matrix row count"),
		NumberOfColumns: checkedInt32(len(matrix[len(matrix)-1]), "burndown matrix column count"),
		Rows:            make([]*BurndownSparseMatrixRow, len(matrix)),
	}
	for rowIndex, status := range matrix {
		nnz := make([]uint32, 0, len(status))
		changed := false

		for columnOffset := range status {
			value := max(status[len(status)-1-columnOffset], 0)

			if !changed {
				changed = value != 0
			}

			if changed {
				nnz = append(nnz, checkedUint32(value, "burndown matrix value"))
			}
		}

		result.Rows[rowIndex] = &BurndownSparseMatrixRow{
			Columns: make([]uint32, len(nnz)),
		}
		for columnIndex := range nnz {
			result.Rows[rowIndex].Columns[columnIndex] = nnz[len(nnz)-1-columnIndex]
		}
	}

	return &result
}

// DenseToCompressedSparseRowMatrix takes an integer matrix and converts it to a Protobuf CSR.
// CSR format: https://en.wikipedia.org/wiki/Sparse_matrix#Compressed_sparse_row_.28CSR.2C_CRS_or_Yale_format.29
func DenseToCompressedSparseRowMatrix(matrix [][]int64) *CompressedSparseRowMatrix {
	// A zero-row matrix has no first row to measure. It is reachable: the callers guard on
	// `!= nil`, and deserializing a payload whose PeopleInteraction declares NumberOfRows == 0
	// yields a non-nil, empty matrix that passes that guard.
	if len(matrix) == 0 {
		return &CompressedSparseRowMatrix{
			Data:    make([]int64, 0),
			Indices: make([]int32, 0),
			Indptr:  make([]int64, 1),
		}
	}

	result := CompressedSparseRowMatrix{
		NumberOfRows:    checkedInt32(len(matrix), "dense matrix row count"),
		NumberOfColumns: checkedInt32(len(matrix[0]), "dense matrix column count"),
		Data:            make([]int64, 0),
		Indices:         make([]int32, 0),
		Indptr:          make([]int64, 1),
	}
	result.Indptr[0] = 0

	for _, row := range matrix {
		nnz := 0

		for x, col := range row {
			if col != 0 {
				result.Data = append(result.Data, col)
				result.Indices = append(result.Indices, checkedInt32(x, "dense matrix column index"))
				nnz++
			}
		}

		result.Indptr = append(result.Indptr, result.GetIndptr()[len(result.GetIndptr())-1]+int64(nnz))
	}

	return &result
}

// MapToCompressedSparseRowMatrix takes an integer matrix and converts it to a Protobuf CSR.
// In contrast to DenseToCompressedSparseRowMatrix, a matrix here is already in DOK format.
// CSR format: https://en.wikipedia.org/wiki/Sparse_matrix#Compressed_sparse_row_.28CSR.2C_CRS_or_Yale_format.29
func MapToCompressedSparseRowMatrix(matrix []map[int]int64) *CompressedSparseRowMatrix {
	result := CompressedSparseRowMatrix{
		NumberOfRows:    checkedInt32(len(matrix), "sparse matrix row count"),
		NumberOfColumns: checkedInt32(len(matrix), "sparse matrix column count"),
		Data:            make([]int64, 0),
		Indices:         make([]int32, 0),
		Indptr:          make([]int64, 1),
	}
	result.Indptr[0] = 0

	for _, row := range matrix {
		order := make([]int, len(row))

		columnIndex := 0
		for col := range row {
			order[columnIndex] = col
			columnIndex++
		}

		sort.Ints(order)

		for _, col := range order {
			val := row[col]
			result.Data = append(result.Data, val)
			result.Indices = append(result.Indices, checkedInt32(col, "sparse matrix column index"))
		}

		result.Indptr = append(result.Indptr, result.GetIndptr()[len(result.GetIndptr())-1]+int64(len(row)))
	}

	return &result
}
