package graphics

import (
	"fmt"
	"sync"

	matcolor "github.com/cwbudde/matplotlib-go/color"
	"github.com/cwbudde/matplotlib-go/render"
)

var registerHeatmapColormapsOnce sync.Once

// RegisterPythonLaboursHeatmapColormaps registers colormaps used by Python labours
// but not provided by matplotlib-go's default registry.
func RegisterPythonLaboursHeatmapColormaps() {
	registerHeatmapColormapsOnce.Do(func() {
		registerLaboursColormap("Reds", []render.Color{
			{R: 1.000, G: 0.961, B: 0.941, A: 1},
			{R: 0.996, G: 0.878, B: 0.824, A: 1},
			{R: 0.988, G: 0.733, B: 0.631, A: 1},
			{R: 0.988, G: 0.573, B: 0.447, A: 1},
			{R: 0.984, G: 0.416, B: 0.290, A: 1},
			{R: 0.937, G: 0.231, B: 0.173, A: 1},
			{R: 0.796, G: 0.094, B: 0.114, A: 1},
			{R: 0.647, G: 0.059, B: 0.082, A: 1},
			{R: 0.404, G: 0.000, B: 0.051, A: 1},
		})
		registerLaboursColormap("Greens", []render.Color{
			{R: 0.969, G: 0.988, B: 0.961, A: 1},
			{R: 0.898, G: 0.961, B: 0.878, A: 1},
			{R: 0.780, G: 0.914, B: 0.753, A: 1},
			{R: 0.631, G: 0.851, B: 0.608, A: 1},
			{R: 0.455, G: 0.769, B: 0.463, A: 1},
			{R: 0.255, G: 0.671, B: 0.365, A: 1},
			{R: 0.137, G: 0.545, B: 0.271, A: 1},
			{R: 0.000, G: 0.427, B: 0.173, A: 1},
			{R: 0.000, G: 0.267, B: 0.106, A: 1},
		})
		registerLaboursColormap("OrRd", []render.Color{
			{R: 1.000, G: 0.969, B: 0.925, A: 1},
			{R: 0.996, G: 0.910, B: 0.784, A: 1},
			{R: 0.992, G: 0.831, B: 0.620, A: 1},
			{R: 0.992, G: 0.733, B: 0.518, A: 1},
			{R: 0.988, G: 0.553, B: 0.349, A: 1},
			{R: 0.937, G: 0.396, B: 0.282, A: 1},
			{R: 0.843, G: 0.188, B: 0.122, A: 1},
			{R: 0.702, G: 0.000, B: 0.000, A: 1},
			{R: 0.498, G: 0.000, B: 0.000, A: 1},
		})
		registerLaboursColormap("YlOrRd", []render.Color{
			{R: 1.000, G: 1.000, B: 0.800, A: 1},
			{R: 1.000, G: 0.929, B: 0.627, A: 1},
			{R: 0.996, G: 0.851, B: 0.463, A: 1},
			{R: 0.996, G: 0.698, B: 0.298, A: 1},
			{R: 0.992, G: 0.553, B: 0.235, A: 1},
			{R: 0.988, G: 0.306, B: 0.165, A: 1},
			{R: 0.890, G: 0.102, B: 0.110, A: 1},
			{R: 0.741, G: 0.000, B: 0.149, A: 1},
			{R: 0.502, G: 0.000, B: 0.149, A: 1},
		})
	})
}

func registerLaboursColormap(name string, colors []render.Color) {
	stops := make([]matcolor.ColorStop, len(colors))
	for i, clr := range colors {
		stops[i] = matcolor.ColorStop{Pos: float64(i) / float64(len(colors)-1), Color: clr}
	}
	matcolor.RegisterColormap(name, matcolor.NewColormap(name, stops))
}

func ValidateHeatMap(matrix [][]float64, rows, cols []string) error {
	if len(matrix) != len(rows) {
		return fmt.Errorf("heatmap row count mismatch: matrix has %d rows, labels have %d", len(matrix), len(rows))
	}
	for i, row := range matrix {
		if len(row) != len(cols) {
			return fmt.Errorf("heatmap column count mismatch on row %d: matrix has %d columns, labels have %d", i, len(row), len(cols))
		}
	}
	return nil
}
