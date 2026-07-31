package modes

import (
	"container/heap"
	"errors"
	"fmt"
	"slices"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// MaxDenseCouplingChartEntries is the only production boundary at which a
// sparse coupling matrix may be expanded. Larger inputs are ranked sparsely,
// then materialized as an at-most 60x60 chart.
const MaxDenseCouplingChartEntries = 60

func plotPythonCouplingHeatmap(
	title, output string,
	names []string,
	matrix readers.SparseMatrix,
	colormap string,
	optionValues ...graphics.Options,
) error {
	visuals := graphics.DefaultOptions()
	if len(optionValues) > 0 {
		visuals = optionValues[0]
	}

	if len(names) == 0 || matrix.Rows == 0 {
		return errors.New("no coupling matrix data available")
	}

	shownNames, shownMatrix := topCouplingHeatmapEntries(
		names, matrix, MaxDenseCouplingChartEntries,
	)
	data := intMatrixToFloat64(shownMatrix)

	return graphics.PlotHeatmapMatplotlib(data, shownNames, shownNames, graphics.MatplotlibHeatmapOptions{
		Title:        title,
		Output:       output,
		Colormap:     colormap,
		WidthInches:  11.52,
		HeightInches: 11.52,
		XLabelLimit:  18,
		YLabelLimit:  28,
		FontSize:     visuals.PlotFontSize(),
	})
}

func topCouplingHeatmapEntries(
	names []string, matrix readers.SparseMatrix, limit int,
) ([]string, [][]int) {
	if limit <= 0 {
		return nil, nil
	}

	if len(names) <= limit {
		indices := make([]int, len(names))
		for index := range indices {
			indices[index] = index
		}

		return append([]string(nil), names...), matrix.DenseSubset(indices)
	}

	totals := make([]int, len(names))

	matrix.ForEachNonZero(func(row, _, value int) bool {
		if row < len(totals) {
			totals[row] += value
		}

		return true
	})

	ranked := &couplingIndexHeap{}
	heap.Init(ranked)

	for index, total := range totals {
		item := rankedCouplingIndex{index: index, total: total}
		if ranked.Len() < limit {
			heap.Push(ranked, item)
		} else if betterCouplingIndex(item, (*ranked)[0]) {
			heap.Pop(ranked)
			heap.Push(ranked, item)
		}
	}

	selected := make([]rankedCouplingIndex, ranked.Len())
	// The heap keeps the weakest retained index at its root, so draining it back
	// to front yields the strongest entity first.
	for index := range slices.Backward(selected) {
		selected[index] = ranked.popRanked()
	}

	shownNames := make([]string, len(selected))

	indices := make([]int, len(selected))
	for row, selectedRow := range selected {
		shownNames[row] = names[selectedRow.index]
		indices[row] = selectedRow.index
	}

	return shownNames, matrix.DenseSubset(indices)
}

type rankedCouplingIndex struct {
	index int
	total int
}

type couplingIndexHeap []rankedCouplingIndex

func (entries *couplingIndexHeap) Len() int { return len(*entries) }
func (entries *couplingIndexHeap) Less(i, j int) bool {
	return betterCouplingIndex((*entries)[j], (*entries)[i])
}

func (entries *couplingIndexHeap) Swap(i, j int) {
	(*entries)[i], (*entries)[j] = (*entries)[j], (*entries)[i]
}

func (entries *couplingIndexHeap) Push(value any) {
	entry, ok := value.(rankedCouplingIndex)
	if !ok {
		panic(fmt.Sprintf("couplingIndexHeap: unexpected element type %T", value))
	}

	*entries = append(*entries, entry)
}

func (entries *couplingIndexHeap) Pop() any {
	old := *entries
	last := len(old) - 1
	value := old[last]
	*entries = old[:last]

	return value
}

// popRanked drains the heap root, restoring the heap invariant.
func (entries *couplingIndexHeap) popRanked() rankedCouplingIndex {
	entry, ok := heap.Pop(entries).(rankedCouplingIndex)
	if !ok {
		panic("couplingIndexHeap: corrupted heap element")
	}

	return entry
}

func betterCouplingIndex(left, right rankedCouplingIndex) bool {
	if left.total != right.total {
		return left.total > right.total
	}

	return left.index < right.index
}

func intMatrixToFloat64(matrix [][]int) [][]float64 {
	data := make([][]float64, len(matrix))
	for i, row := range matrix {
		data[i] = make([]float64, len(row))
		for j, value := range row {
			data[i][j] = float64(value)
		}
	}

	return data
}
