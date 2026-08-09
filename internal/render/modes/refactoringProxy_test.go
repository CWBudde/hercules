package modes

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/matplotlib-go/dates"

	"github.com/cwbudde/hercules/internal/render/readers"
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
	_, err = os.Stat(output)
	if err != nil {
		t.Fatalf("Expected refactoring proxy chart at %s: %v", output, err)
	}
}

func TestRefactoringProxyMatchesPythonDimensions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "refactoring-proxy.png")
	err := RefactoringProxy(&refactoringProxyTestReader{NoDataReader: &NoDataReader{}}, output)
	if err != nil {
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
	err := RefactoringProxy(&refactoringProxyTestReader{NoDataReader: &NoDataReader{}}, output)
	if err != nil {
		t.Fatalf("RefactoringProxy() unexpected error: %v", err)
	}

	img := readPNG(t, output)
	if !hasTransparentPixel(img) {
		t.Fatal("expected refactoring proxy chart to preserve transparent background")
	}
}

// TestRefactoringProxyKeepsThresholdOnTheDataDateRange pins plan item T2.1: the
// threshold line and the shaded refactoring spans are drawn from raw float64 x
// values while the rate series is plotted from []time.Time, so both have to be
// expressed in matplotlib-go date units. Feeding unix seconds instead stretched
// the axis to ~1.7e9 days and collapsed the whole series into a spike at the
// left edge, with a tick label in the year -242464.
func TestRefactoringProxyKeepsThresholdOnTheDataDateRange(t *testing.T) {
	ticks := []readers.RefactoringProxyTick{
		{Timestamp: time.Date(2021, 2, 8, 0, 0, 0, 0, time.UTC).Unix(), RefactoringRate: 0.1},
		{Timestamp: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC).Unix(), RefactoringRate: 0.5, IsRefactoring: true},
		{Timestamp: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC).Unix(), RefactoringRate: 0.2},
	}

	timestamps, x := refactoringProxyDateNumbers(ticks)
	if len(x) != len(ticks) {
		t.Fatalf("len(x) = %d, want %d", len(x), len(ticks))
	}

	tolerance := 24 * time.Hour
	for i := range x {
		got := dates.Num2Date(x[i], time.UTC)
		if diff := got.Sub(timestamps[i]); diff > tolerance || diff < -tolerance {
			t.Fatalf("x[%d] round-trips to %s, want within a day of %s", i, got, timestamps[i])
		}
	}

	// The overlays derive their coordinates from the same slice, so checking the
	// span endpoints guards the AxVSpan call sites too.
	regions := refactoringProxyRegions(ticks, 0.3, x)
	if len(regions) == 0 {
		t.Fatal("expected at least one refactoring region")
	}

	first, last := timestamps[0], timestamps[len(timestamps)-1]
	for _, region := range regions {
		for _, edge := range []float64{region.Start, region.End} {
			got := dates.Num2Date(edge, time.UTC)
			if got.Before(first.Add(-tolerance)) || got.After(last.Add(tolerance)) {
				t.Fatalf("region edge round-trips to %s, want within [%s, %s]", got, first, last)
			}
		}
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
