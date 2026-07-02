package modes

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessOverwritesMatrixUsesPythonColumnSemantics(t *testing.T) {
	people := []string{"low", "high"}
	matrix := [][]int{
		{10, -2, -5, -3},
		{20, -4, -1, -10},
	}

	rows, cols, normalized := processOverwritesMatrix(people, matrix, 1, true)
	if len(rows) != 1 || rows[0] != "high" {
		t.Fatalf("rows = %#v, want only high", rows)
	}
	wantCols := []string{"Unidentified", "high"}
	if len(cols) != len(wantCols) {
		t.Fatalf("cols = %#v, want %#v", cols, wantCols)
	}
	for i := range wantCols {
		if cols[i] != wantCols[i] {
			t.Fatalf("cols = %#v, want %#v", cols, wantCols)
		}
	}
	if len(normalized) != 1 || len(normalized[0]) != 2 {
		t.Fatalf("normalized shape = %#v, want 1x2", normalized)
	}
	if normalized[0][0] != 0.2 || normalized[0][1] != 0.5 {
		t.Fatalf("normalized = %#v, want [[0.2 0.5]]", normalized)
	}
}

func TestPlotOverwritesMatrixWritesOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "overwrites.png")
	err := plotOverwritesMatrix(
		[]string{"Alice", "Bob"},
		[]string{"Unidentified", "Alice", "Bob"},
		[][]float64{{-0.1, -0.2, -0.3}, {-0.4, -0.5, -0.6}},
		output,
	)
	if err != nil {
		t.Fatalf("plotOverwritesMatrix() error = %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("expected output file: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty output file")
	}

	file, err := os.Open(output) // #nosec G304 - test output path is under t.TempDir.
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("decoded output type = %T, want *image.NRGBA", img)
	}
	offset := nrgba.PixOffset(0, 0)
	if got := nrgba.Pix[offset : offset+4]; got[0] != 255 || got[1] != 255 || got[2] != 255 || got[3] != 0 {
		t.Fatalf("corner RGBA = %#v, want transparent white", got)
	}
}

func TestMatrixRangeUsesActualProcessedValues(t *testing.T) {
	minValue, maxValue := matrixRange([][]float64{
		{-0.2, 0.5},
		{0.1, -0.7},
	})

	if minValue != -0.7 || maxValue != 0.5 {
		t.Fatalf("matrixRange() = (%v, %v), want (-0.7, 0.5)", minValue, maxValue)
	}
}
