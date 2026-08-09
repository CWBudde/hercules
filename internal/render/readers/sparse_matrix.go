package readers

import (
	"errors"
	"fmt"
	"sort"
)

var (
	errNegativeSparseDimensions = errors.New("negative sparse matrix dimensions")
	errSparseCellOutOfRange     = errors.New("sparse matrix cell is out of range")
	errSparseValueOverflow      = errors.New("sparse matrix value overflows int")
)

// SparseEntry is one non-zero cell in a SparseMatrix.
type SparseEntry struct {
	Row    int
	Column int
	Value  int
}

// SparseMatrix stores an integer matrix in compressed sparse row (CSR) form.
//
// RowOffsets always has Rows+1 entries. ColumnIndices and Values have one
// entry per non-zero cell and are ordered by row, then column.
type SparseMatrix struct {
	Rows          int
	Columns       int
	RowOffsets    []int
	ColumnIndices []int
	Values        []int
}

// NewSparseMatrix constructs a canonical CSR matrix. Duplicate cells are
// summed and zero-valued results are omitted.
func NewSparseMatrix(rows, columns int, entries []SparseEntry) (SparseMatrix, error) {
	if rows < 0 || columns < 0 {
		return SparseMatrix{}, fmt.Errorf("%w %dx%d", errNegativeSparseDimensions, rows, columns)
	}

	sorted := append([]SparseEntry(nil), entries...)

	err := validateSparseEntries(rows, columns, sorted)
	if err != nil {
		return SparseMatrix{}, err
	}

	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Row == sorted[j].Row {
			return sorted[i].Column < sorted[j].Column
		}

		return sorted[i].Row < sorted[j].Row
	})

	matrix := SparseMatrix{
		Rows:       rows,
		Columns:    columns,
		RowOffsets: make([]int, rows+1),
	}

	err = appendCanonicalSparseEntries(&matrix, sorted)
	if err != nil {
		return SparseMatrix{}, err
	}

	for row := range rows {
		matrix.RowOffsets[row+1] += matrix.RowOffsets[row]
	}

	return matrix, nil
}

func validateSparseEntries(rows, columns int, entries []SparseEntry) error {
	for _, entry := range entries {
		if entry.Row < 0 || entry.Row >= rows || entry.Column < 0 || entry.Column >= columns {
			return fmt.Errorf(
				"%w: cell (%d, %d) is outside %dx%d",
				errSparseCellOutOfRange, entry.Row, entry.Column, rows, columns,
			)
		}
	}

	return nil
}

func appendCanonicalSparseEntries(matrix *SparseMatrix, sorted []SparseEntry) error {
	for index := 0; index < len(sorted); {
		entry := sorted[index]
		value := entry.Value

		index++
		for index < len(sorted) && sameSparseCell(entry, sorted[index]) {
			var err error

			value, err = addSparseValues(value, sorted[index].Value)
			if err != nil {
				return fmt.Errorf("%w: cell (%d, %d)", errSparseValueOverflow, entry.Row, entry.Column)
			}

			index++
		}

		if value == 0 {
			continue
		}

		matrix.ColumnIndices = append(matrix.ColumnIndices, entry.Column)
		matrix.Values = append(matrix.Values, value)
		matrix.RowOffsets[entry.Row+1]++
	}

	return nil
}

func sameSparseCell(left, right SparseEntry) bool {
	return left.Row == right.Row && left.Column == right.Column
}

func addSparseValues(value, addend int) (int, error) {
	maxInt := int(^uint(0) >> 1)

	minInt := -maxInt - 1
	if addend > 0 && value > maxInt-addend {
		return 0, fmt.Errorf("%w: positive", errSparseValueOverflow)
	}

	if addend < 0 && value < minInt-addend {
		return 0, fmt.Errorf("%w: negative", errSparseValueOverflow)
	}

	return value + addend, nil
}

// SparseMatrixFromDense converts a dense matrix. It is intended for small
// fixtures and compatibility boundaries, not coupling readers or modes.
func SparseMatrixFromDense(dense [][]int) SparseMatrix {
	columns := 0
	for _, row := range dense {
		columns = max(columns, len(row))
	}

	entries := make([]SparseEntry, 0)

	for row, values := range dense {
		for column, value := range values {
			if value != 0 {
				entries = append(entries, SparseEntry{Row: row, Column: column, Value: value})
			}
		}
	}

	matrix, _ := NewSparseMatrix(len(dense), columns, entries)

	return matrix
}

// NonZeroCount returns the number of stored cells.
func (matrix SparseMatrix) NonZeroCount() int {
	return len(matrix.Values)
}

// At returns a cell value, or zero for an absent or out-of-range cell.
func (matrix SparseMatrix) At(row, column int) int {
	if row < 0 || row >= matrix.Rows || column < 0 || column >= matrix.Columns ||
		len(matrix.RowOffsets) != matrix.Rows+1 {
		return 0
	}

	start, end := matrix.RowOffsets[row], matrix.RowOffsets[row+1]

	position := sort.Search(end-start, func(index int) bool {
		return matrix.ColumnIndices[start+index] >= column
	})
	if start+position < end && matrix.ColumnIndices[start+position] == column {
		return matrix.Values[start+position]
	}

	return 0
}

// Row returns the canonical column and value slices for a row. The returned
// slices alias the matrix and must not be modified.
func (matrix SparseMatrix) Row(row int) ([]int, []int) {
	if row < 0 || row >= matrix.Rows || len(matrix.RowOffsets) != matrix.Rows+1 {
		return nil, nil
	}

	start, end := matrix.RowOffsets[row], matrix.RowOffsets[row+1]

	return matrix.ColumnIndices[start:end], matrix.Values[start:end]
}

// ForEachNonZero visits stored cells in row-major order.
func (matrix SparseMatrix) ForEachNonZero(visit func(row, column, value int) bool) {
	for row := range matrix.Rows {
		columns, values := matrix.Row(row)
		for index, column := range columns {
			if !visit(row, column, values[index]) {
				return
			}
		}
	}
}

// DenseSubset materializes the square submatrix selected by source indices.
// Coupling modes call this only after enforcing their documented chart limit.
func (matrix SparseMatrix) DenseSubset(indices []int) [][]int {
	dense := make([][]int, len(indices))

	selected := make(map[int]int, len(indices))
	for destination, source := range indices {
		selected[source] = destination
		dense[destination] = make([]int, len(indices))
	}

	for destinationRow, sourceRow := range indices {
		columns, values := matrix.Row(sourceRow)
		for index, sourceColumn := range columns {
			if destinationColumn, ok := selected[sourceColumn]; ok {
				dense[destinationRow][destinationColumn] = values[index]
			}
		}
	}

	return dense
}

// Dense materializes the complete matrix. It is provided for small tests and
// non-scalability-sensitive compatibility code.
func (matrix SparseMatrix) Dense() [][]int {
	dense := make([][]int, matrix.Rows)
	for row := range dense {
		dense[row] = make([]int, matrix.Columns)
	}

	matrix.ForEachNonZero(func(row, column, value int) bool {
		if column < matrix.Columns {
			dense[row][column] = value
		}

		return true
	})

	return dense
}
