package modes

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/meko-christian/hercules/internal/render/readers"
)

type refactoringProxyTestReader struct {
	*NoDataReader
}

func (r *refactoringProxyTestReader) GetRefactoringProxy() (*readers.RefactoringProxyData, error) {
	return &readers.RefactoringProxyData{
		Threshold: 0.3,
		Ticks: []readers.RefactoringProxyTick{
			{RefactoringRate: 0.1, TotalChanges: 10},
			{RefactoringRate: 0.5, IsRefactoring: true, TotalChanges: 20},
		},
	}, nil
}

func TestRefactoringProxyWritesChart(t *testing.T) {
	output := filepath.Join(t.TempDir(), "refactoring-proxy.png")
	err := RefactoringProxy(&refactoringProxyTestReader{NoDataReader: &NoDataReader{}}, output)
	if err != nil {
		t.Fatalf("RefactoringProxy() unexpected error: %v", err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("Expected refactoring proxy chart at %s: %v", output, err)
	}
}

func TestRefactoringProxyMatchesPythonDimensions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "refactoring-proxy.png")
	if err := RefactoringProxy(&refactoringProxyTestReader{NoDataReader: &NoDataReader{}}, output); err != nil {
		t.Fatalf("RefactoringProxy() unexpected error: %v", err)
	}

	img := readPNG(t, output)
	bounds := img.Bounds()
	if bounds.Dx() != 1600 || bounds.Dy() != 600 {
		t.Fatalf("image size = %dx%d, want 1600x600", bounds.Dx(), bounds.Dy())
	}
}

func TestRefactoringProxyPreservesTransparentBackground(t *testing.T) {
	output := filepath.Join(t.TempDir(), "refactoring-proxy.png")
	if err := RefactoringProxy(&refactoringProxyTestReader{NoDataReader: &NoDataReader{}}, output); err != nil {
		t.Fatalf("RefactoringProxy() unexpected error: %v", err)
	}

	img := readPNG(t, output)
	if !hasTransparentPixel(img) {
		t.Fatal("expected refactoring proxy chart to preserve transparent background")
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()

	file, err := os.Open(path) // #nosec G304 - test reads a temp output path.
	if err != nil {
		t.Fatalf("open PNG: %v", err)
	}
	defer func() { _ = file.Close() }()

	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	return img
}

func hasTransparentPixel(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xffff {
				return true
			}
		}
	}
	return false
}
