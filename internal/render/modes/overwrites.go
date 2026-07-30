package modes

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cwbudde/matplotlib-go/backends"
	"github.com/cwbudde/matplotlib-go/core"
	"github.com/cwbudde/matplotlib-go/optional"
	"github.com/cwbudde/matplotlib-go/render"
	"github.com/cwbudde/matplotlib-go/style"
	"github.com/cwbudde/matplotlib-go/ticker"

	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func OverwritesMatrix(reader readers.Reader, output string) error {
	return OverwritesMatrixWithOptions(reader, output, defaultOptions())
}

func OverwritesMatrixWithOptions(reader readers.Reader, output string, opts Options) error {
	// Step 1: Extract data from the reader
	people, matrix, err := reader.GetPeopleInteraction()
	if err != nil {
		return fmt.Errorf("failed to get people interaction data: %w", err)
	}

	fmt.Println("Processing overwrites matrix...")

	// Step 2: Process the matrix
	maxPeople := opts.MaxPeople
	people, colLabels, normalizedMatrix := processOverwritesMatrix(people, matrix, maxPeople, true)

	// Step 3: Check if JSON output is required
	if strings.HasSuffix(output, ".json") {
		return saveMatrixAsJSON(output, people, normalizedMatrix)
	}

	// Step 4: Visualize the matrix
	if err := plotOverwritesMatrixWithOptions(people, colLabels, normalizedMatrix, output, opts.Graphics); err != nil {
		return fmt.Errorf("failed to plot overwrites matrix: %w", err)
	}

	fmt.Println("Overwrites matrix generated successfully.")
	return nil
}

func processOverwritesMatrix(people []string, matrix [][]int, maxPeople int, normalize bool) ([]string, []string, [][]float64) {
	// Python labours stores column 0 as row total, column 1 as "Unidentified",
	// and developer overwrite columns at 2 + developer index.
	if len(people) > maxPeople {
		order := argsort(matrix)
		matrix = truncateOverwritesMatrix(matrix, order[:maxPeople])
		people = truncatePeople(people, order[:maxPeople])
		fmt.Fprintf(os.Stderr, "Warning: truncated people to most productive %d\n", maxPeople)
	}

	normalizedMatrix := make([][]float64, len(matrix))
	for i, row := range matrix {
		normalizedMatrix[i] = overwriteValues(row, normalize)
	}

	colLabels := append([]string{"Unidentified"}, people...)
	return people, colLabels, normalizedMatrix
}

func overwriteValues(row []int, normalize bool) []float64 {
	values := make([]float64, max(len(row)-1, 0))
	total := overwriteRowTotal(row)
	for column := 1; column < len(row); column++ {
		values[column-1] = -float64(row[column])
		if normalize && total != 0 {
			values[column-1] /= float64(total)
		}
	}
	return values
}

func overwriteRowTotal(row []int) int {
	if len(row) == 0 {
		return 0
	}
	return row[0]
}

func plotOverwritesMatrix(people, colLabels []string, matrix [][]float64, output string) error {
	return plotOverwritesMatrixWithOptions(people, colLabels, matrix, output, graphics.DefaultOptions())
}

func plotOverwritesMatrixWithOptions(
	people, colLabels []string,
	matrix [][]float64,
	output string,
	opts graphics.Options,
) error {
	people = truncateOverwriteLabels(peopleChartLabels(people))
	colLabels = truncateOverwriteLabels(peopleChartLabels(colLabels))

	if err := graphics.ValidateHeatMap(matrix, people, colLabels); err != nil {
		return err
	}

	minValue, maxValue := matrixRange(matrix)

	width, height := ownershipPlotPixelSize(
		graphics.PythonPlotDefaultWidthInches,
		graphics.PythonPlotDefaultHeightInches,
		opts.Size,
	)
	background, foreground := graphics.LaboursPlotColors(opts.Background)
	graphics.RegisterPythonLaboursHeatmapColormaps()
	fig, ax := newOverwritesFigure(width, height, background, foreground, opts.PlotFontSize())
	if ax == nil {
		return fmt.Errorf("failed to create overwrites axes")
	}

	cmap := "OrRd"
	if img := ax.MatShow(matrix, core.MatShowOptions{
		Colormap: optional.Of(cmap),
		VMin:     optional.Of(minValue),
		VMax:     optional.Of(maxValue),
	}); img == nil {
		return fmt.Errorf("failed to create overwrites matrix image")
	}
	configureOverwritesMatrixAxes(ax, people, colLabels, foreground)

	if err := saveOverwritesMatplotlibFigure(fig, output, width, height, background); err != nil {
		return fmt.Errorf("failed to save plot: %w", err)
	}
	return nil
}

func truncateOverwriteLabels(labels []string) []string {
	for i, label := range labels {
		if len(label) > 40 {
			labels[i] = label[:37] + "..."
		}
	}
	return labels
}

func newOverwritesFigure(
	width, height int,
	background, foreground render.Color,
	fontSize float64,
) (*core.Figure, *core.Axes) {
	fig := core.NewFigure(
		width,
		height,
		style.WithFont(graphics.PythonPlotFontFamily, fontSize),
		style.WithBackground(background.R, background.G, background.B, 0),
		style.WithAxesBackground(render.Color{R: background.R, G: background.G, B: background.B, A: 0}),
		style.WithAxesEdgeColor(foreground),
		style.WithTextColor(foreground.R, foreground.G, foreground.B, foreground.A),
	)
	ax := fig.GridSpec(
		1,
		1,
		core.WithGridSpecPadding(0.105, 0.99, 0.01, 0.89),
	).Cell(0, 0).AddAxes()
	return fig, ax
}

func configureOverwritesMatrixAxes(ax *core.Axes, people, colLabels []string, foreground render.Color) {
	xTicks := integerTicks(len(colLabels))
	yTicks := integerTicks(len(people))
	xMinorTicks := halfOffsetTicks(len(colLabels))
	yMinorTicks := halfOffsetTicks(len(people))

	if ax.XAxis != nil {
		ax.XAxis.ShowTicks = false
		ax.XAxis.ShowLabels = false
		ax.XAxis.Locator = ticker.FixedLocator{TicksList: xTicks}
		ax.XAxis.Formatter = ticker.FixedFormatter{Labels: colLabels}
		ax.XAxis.MinorLocator = ticker.FixedLocator{TicksList: xMinorTicks}
	}
	top := ax.TopAxis()
	top.Locator = ticker.FixedLocator{TicksList: xTicks}
	top.Formatter = ticker.FixedFormatter{Labels: colLabels}
	top.MinorLocator = ticker.FixedLocator{TicksList: xMinorTicks}
	top.MajorLabelStyle = core.TickLabelStyle{
		Rotation:  45,
		HAlign:    core.TextAlignLeft,
		VAlign:    core.TextVAlignBottom,
		AutoAlign: false,
	}
	top.ShowTicks = true
	top.ShowLabels = true

	if ax.YAxis != nil {
		ax.YAxis.Locator = ticker.FixedLocator{TicksList: yTicks}
		ax.YAxis.Formatter = ticker.FixedFormatter{Labels: people}
		ax.YAxis.MinorLocator = ticker.FixedLocator{TicksList: yMinorTicks}
		ax.YAxis.MajorLabelStyle = core.TickLabelStyle{
			HAlign:    core.TextAlignRight,
			VAlign:    core.TextVAlignMiddle,
			AutoAlign: false,
		}
	}

	gridColor := foreground
	gridColor.A = 0.15
	xGrid := core.NewGrid(core.AxisTop)
	xGrid.Major = false
	xGrid.Minor = true
	xGrid.MinorLocator = ticker.FixedLocator{TicksList: xMinorTicks}
	xGrid.MinorColor = gridColor
	xGrid.MinorLineWidth = 1
	yGrid := core.NewGrid(core.AxisLeft)
	yGrid.Major = false
	yGrid.Minor = true
	yGrid.MinorLocator = ticker.FixedLocator{TicksList: yMinorTicks}
	yGrid.MinorColor = gridColor
	yGrid.MinorLineWidth = 1
	ax.Add(xGrid)
	ax.Add(yGrid)
}

func integerTicks(count int) []float64 {
	ticks := make([]float64, count)
	for i := range ticks {
		ticks[i] = float64(i)
	}
	return ticks
}

func halfOffsetTicks(count int) []float64 {
	ticks := make([]float64, count)
	for i := range ticks {
		ticks[i] = float64(i) + 0.5
	}
	return ticks
}

func saveOverwritesMatplotlibFigure(fig *core.Figure, output string, width, height int, background render.Color) error {
	if output == "" {
		output = "overwrites.png"
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return fmt.Errorf("failed to create output directory for %s: %w", output, err)
	}

	transparentBackground := background
	transparentBackground.A = 0
	config := backends.Config{
		Width:       width,
		Height:      height,
		Background:  transparentBackground,
		DPI:         100,
		Transparent: transparentBackground.A == 0,
	}
	switch strings.ToLower(filepath.Ext(output)) {
	case ".svg":
		renderer, _, err := backends.NewRenderer("svg", config, nil)
		if err != nil {
			return fmt.Errorf("failed to create SVG renderer: %w", err)
		}
		return core.SaveSVG(fig, renderer, output)
	default:
		renderer, _, err := backends.NewRenderer("agg", config, backends.TextCapabilities)
		if err != nil {
			return fmt.Errorf("failed to create AGG renderer: %w", err)
		}
		if err := core.SavePNG(fig, renderer, output); err != nil {
			return err
		}
		if transparentBackground.A == 0 {
			return graphics.SetTransparentPNGRGB(output, transparentBackground)
		}
		return nil
	}
}

func matrixRange(matrix [][]float64) (minValue, maxValue float64) {
	minValue = math.Inf(1)
	maxValue = math.Inf(-1)
	for _, row := range matrix {
		for _, value := range row {
			minValue = math.Min(minValue, value)
			maxValue = math.Max(maxValue, value)
		}
	}
	if math.IsInf(minValue, 0) {
		return 0, 1
	}
	if minValue == maxValue {
		return minValue - 0.5, maxValue + 0.5
	}
	return minValue, maxValue
}

func saveMatrixAsJSON(output string, people []string, matrix [][]float64) error {
	data := struct {
		Type   string      `json:"type"`
		People []string    `json:"people"`
		Matrix [][]float64 `json:"matrix"`
	}{
		Type:   "overwrites_matrix",
		People: people,
		Matrix: matrix,
	}

	file, err := os.Create(output) // #nosec G304 - output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %w", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func truncateOverwritesMatrix(matrix [][]int, indices []int) [][]int {
	truncated := make([][]int, len(indices))
	for i, idx := range indices {
		if idx >= len(matrix) {
			continue
		}
		cols := append([]int{0, 1}, addOffset(indices, 2)...)
		truncated[i] = make([]int, len(cols))
		for j, col := range cols {
			if col >= 0 && col < len(matrix[idx]) {
				truncated[i][j] = matrix[idx][col]
			}
		}
	}
	return truncated
}

func addOffset(values []int, offset int) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = value + offset
	}
	return result
}

func truncatePeople(people []string, indices []int) []string {
	truncated := make([]string, len(indices))
	for i, idx := range indices {
		truncated[i] = people[idx]
	}
	return truncated
}

func argsort(matrix [][]int) []int {
	scores := make([]int, len(matrix))
	for i, row := range matrix {
		if len(row) > 0 {
			scores[i] = row[0]
		}
	}

	indices := make([]int, len(scores))
	for i := range indices {
		indices[i] = i
	}

	sort.Slice(indices, func(i, j int) bool {
		return scores[indices[i]] > scores[indices[j]]
	})

	return indices
}
