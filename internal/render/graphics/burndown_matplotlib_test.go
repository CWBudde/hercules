package graphics

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
)

func TestPythonLaboursColorPaletteMatchesTab20Cycle(t *testing.T) {
	// Python labours applies the requested matplotlib style and then overrides
	// axes.prop_cycle with pyplot.cm.tab20.colors in plotting.import_pyplot().
	colors := PythonLaboursColorPalette(20)
	if len(colors) != 20 {
		t.Fatalf("palette length = %d, want 20", len(colors))
	}

	want := []color.Color{
		color.RGBA{R: 0x1F, G: 0x77, B: 0xB4, A: 255},
		color.RGBA{R: 0xAE, G: 0xC7, B: 0xE8, A: 255},
		color.RGBA{R: 0xFF, G: 0x7F, B: 0x0E, A: 255},
		color.RGBA{R: 0xFF, G: 0xBB, B: 0x78, A: 255},
		color.RGBA{R: 0x2C, G: 0xA0, B: 0x2C, A: 255},
		color.RGBA{R: 0x98, G: 0xDF, B: 0x8A, A: 255},
		color.RGBA{R: 0xD6, G: 0x27, B: 0x28, A: 255},
		color.RGBA{R: 0xFF, G: 0x98, B: 0x96, A: 255},
		color.RGBA{R: 0x94, G: 0x67, B: 0xBD, A: 255},
		color.RGBA{R: 0xC5, G: 0xB0, B: 0xD5, A: 255},
		color.RGBA{R: 0x8C, G: 0x56, B: 0x4B, A: 255},
		color.RGBA{R: 0xC4, G: 0x9C, B: 0x94, A: 255},
		color.RGBA{R: 0xE3, G: 0x77, B: 0xC2, A: 255},
		color.RGBA{R: 0xF7, G: 0xB6, B: 0xD2, A: 255},
		color.RGBA{R: 0x7F, G: 0x7F, B: 0x7F, A: 255},
		color.RGBA{R: 0xC7, G: 0xC7, B: 0xC7, A: 255},
		color.RGBA{R: 0xBC, G: 0xBD, B: 0x22, A: 255},
		color.RGBA{R: 0xDB, G: 0xDB, B: 0x8D, A: 255},
		color.RGBA{R: 0x17, G: 0xBE, B: 0xCF, A: 255},
		color.RGBA{R: 0x9E, G: 0xDA, B: 0xE5, A: 255},
	}
	for i := range want {
		if colors[i] != want[i] {
			t.Fatalf("color %d = %#v, want %#v", i, colors[i], want[i])
		}
	}

	// More requested series than palette entries cycles, matching matplotlib.
	wrapped := PythonLaboursColorPalette(21)
	if wrapped[20] != want[0] {
		t.Fatalf("wrap color = %#v, want %#v", wrapped[20], want[0])
	}
}

func TestPlotBurndownMatplotlibUsesBackends(t *testing.T) {
	data := &burndown.ProcessedBurndown{
		Name: "repo",
		Matrix: [][]float64{
			{4, 3, 2},
			{0, 1, 2},
		},
		DateRange: []time.Time{
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		Labels:       []string{"old", "new"},
		Granularity:  30,
		Sampling:     30,
		ResampleMode: "month",
	}

	dir := t.TempDir()
	pngPath := filepath.Join(dir, "burndown.png")
	opts := Options{Size: "2,1.5", Background: "white"}
	if err := PlotBurndownMatplotlibWithOptions(data, pngPath, false, opts); err != nil {
		t.Fatalf("plot png: %v", err)
	}
	pngFile, err := os.Open(pngPath) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("open png: %v", err)
	}
	defer func() { _ = pngFile.Close() }()
	img, err := png.Decode(pngFile)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}

	svgPath := filepath.Join(dir, "burndown.svg")
	if err := PlotBurndownMatplotlibWithOptions(data, svgPath, true, opts); err != nil {
		t.Fatalf("plot svg: %v", err)
	}
	svgBytes, err := os.ReadFile(svgPath) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read svg: %v", err)
	}
	if !strings.Contains(string(svgBytes), "<svg") {
		t.Fatalf("svg output does not contain <svg")
	}
}

func TestBurndownYAxisTicksUseScientificScale(t *testing.T) {
	ticks, labels, offset := burndownYAxisTicks(25800)
	if offset != "1e4" {
		t.Fatalf("offset = %q, want 1e4", offset)
	}
	wantTicks := []float64{0, 5000, 10000, 15000, 20000, 25000}
	if len(ticks) != len(wantTicks) {
		t.Fatalf("ticks = %v, want %v", ticks, wantTicks)
	}
	for i := range wantTicks {
		if ticks[i] != wantTicks[i] {
			t.Fatalf("ticks = %v, want %v", ticks, wantTicks)
		}
	}
	wantLabels := []string{"0", "0.5", "1", "1.5", "2", "2.5"}
	if strings.Join(labels, ",") != strings.Join(wantLabels, ",") {
		t.Fatalf("labels = %v, want %v", labels, wantLabels)
	}

	ticks, labels, offset = burndownYAxisTicks(6800)
	if offset != "1e3" {
		t.Fatalf("offset = %q, want 1e3", offset)
	}
	if got, want := ticks[len(ticks)-1], 7000.0; got != want {
		t.Fatalf("last tick = %v, want %v", got, want)
	}
	if got, want := labels[len(labels)-1], "7"; got != want {
		t.Fatalf("last label = %q, want %q", got, want)
	}
}

func TestBurndownDateTicksUseEndpointDatesForShortYearlySpan(t *testing.T) {
	dates := []time.Time{
		time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	_, labels := burndownDateTicks(dates, "year")
	want := []string{"2017-01-01", "2018-01-01"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
}
