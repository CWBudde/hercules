package modes

import (
	"fmt"
	"sort"

	"github.com/cwbudde/hercules/internal/render/graphics"
)

const maxPythonCouplingHeatmapEntries = 60

func plotPythonCouplingHeatmap(title, output string, names []string, matrix [][]int, colormap string) error {
	if len(names) == 0 || len(matrix) == 0 {
		return fmt.Errorf("no coupling matrix data available")
	}

	shownNames, shownMatrix := topCouplingHeatmapEntries(names, matrix, maxPythonCouplingHeatmapEntries)
	data := intMatrixToFloat64(shownMatrix)

	return graphics.PlotHeatmapMatplotlib(data, shownNames, shownNames, graphics.MatplotlibHeatmapOptions{
		Title:        title,
		Output:       output,
		Colormap:     colormap,
		WidthInches:  11.52,
		HeightInches: 11.52,
		XLabelLimit:  18,
		YLabelLimit:  28,
	})
}

func topCouplingHeatmapEntries(names []string, matrix [][]int, limit int) ([]string, [][]int) {
	if limit <= 0 || len(names) <= limit {
		return append([]string(nil), names...), cloneIntMatrix(matrix)
	}

	type rankedIndex struct {
		index int
		total int
	}
	ranked := make([]rankedIndex, len(names))
	for i := range names {
		total := 0
		if i < len(matrix) {
			for _, value := range matrix[i] {
				total += value
			}
		}
		ranked[i] = rankedIndex{index: i, total: total}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].total == ranked[j].total {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].total > ranked[j].total
	})

	selected := ranked[:limit]
	shownNames := make([]string, len(selected))
	shownMatrix := make([][]int, len(selected))
	for row, selectedRow := range selected {
		shownNames[row] = names[selectedRow.index]
		shownMatrix[row] = make([]int, len(selected))
		if selectedRow.index >= len(matrix) {
			continue
		}
		for col, selectedCol := range selected {
			if selectedCol.index < len(matrix[selectedRow.index]) {
				shownMatrix[row][col] = matrix[selectedRow.index][selectedCol.index]
			}
		}
	}
	return shownNames, shownMatrix
}

func cloneIntMatrix(matrix [][]int) [][]int {
	clone := make([][]int, len(matrix))
	for i, row := range matrix {
		clone[i] = append([]int(nil), row...)
	}
	return clone
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
