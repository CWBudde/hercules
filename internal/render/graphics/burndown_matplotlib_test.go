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
	err := PlotBurndownMatplotlibWithOptions(data, pngPath, false, opts)
	if err != nil {
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
	err = PlotBurndownMatplotlibWithOptions(data, svgPath, true, opts)
	if err != nil {
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

// TestNormalizedStackMatrixScalesEveryColumnToOne pins plan item T2.2: the
// --relative burndown path must normalize the per-band matrix column-wise
// instead of clipping the raw line counts to [0, 1]. Clipping left every band
// after the first at zero, which rendered as a single solid area at 1.0.
func TestNormalizedStackMatrixScalesEveryColumnToOne(t *testing.T) {
	matrix := [][]float64{
		{12345, 500, 0},
		{2345, 250, 0},
		{310, 250, 0},
	}
	original := [][]float64{
		{12345, 500, 0},
		{2345, 250, 0},
		{310, 250, 0},
	}

	got := normalizedStackMatrix(matrix)

	const epsilon = 1e-9

	for col := range 2 {
		total := 0.0
		nonZero := 0

		for _, row := range got {
			total += row[col]

			if row[col] > epsilon {
				nonZero++
			}
		}

		if diff := total - 1.0; diff > epsilon || diff < -epsilon {
			t.Fatalf("column %d sums to %v, want 1.0", col, total)
		}

		if nonZero < 2 {
			t.Fatalf("column %d has %d non-zero bands, want more than one", col, nonZero)
		}
	}

	// An all-zero column must be left alone rather than dividing by zero.
	for i, row := range got {
		if row[2] != 0 {
			t.Fatalf("zero column band %d = %v, want 0", i, row[2])
		}
	}

	// The input aliases ProcessedBurndown.Matrix and is plotted more than once,
	// so normalization must not happen in place.
	for i, row := range matrix {
		for j, v := range row {
			if v != original[i][j] {
				t.Fatalf("input matrix[%d][%d] = %v, want %v (must not be modified)", i, j, v, original[i][j])
			}
		}
	}
}

func TestLineCountYAxisTicksContainFullMagnitude(t *testing.T) {
	ticks, labels := lineCountYAxisTicks(25800 * 1.05)
	wantTicks := []float64{0, 5000, 10000, 15000, 20000, 25000}
	if len(ticks) != len(wantTicks) {
		t.Fatalf("ticks = %v, want %v", ticks, wantTicks)
	}
	for i := range wantTicks {
		if ticks[i] != wantTicks[i] {
			t.Fatalf("ticks = %v, want %v", ticks, wantTicks)
		}
	}
	wantLabels := []string{"0", "5000", "10000", "15000", "20000", "25000"}
	if strings.Join(labels, ",") != strings.Join(wantLabels, ",") {
		t.Fatalf("labels = %v, want %v", labels, wantLabels)
	}

	ticks, labels = lineCountYAxisTicks(6800 * 1.05)
	if got, want := ticks[len(ticks)-1], 6000.0; got != want {
		t.Fatalf("last tick = %v, want %v", got, want)
	}
	if got, want := labels[len(labels)-1], "6000"; got != want {
		t.Fatalf("last label = %q, want %q", got, want)
	}
}

func TestPlotBurndownLargeValuesKeepsMagnitudeOnAxis(t *testing.T) {
	data := &burndown.ProcessedBurndown{
		Name: "large",
		Matrix: [][]float64{
			{92000, 90000, 88000},
			{4000, 5000, 6000},
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

	output := filepath.Join(t.TempDir(), "burndown.svg")
	err := PlotBurndownMatplotlibWithOptions(
		data, output, false, Options{Size: "16,10", HideTitle: true},
	)
	if err != nil {
		t.Fatalf("plot large burndown: %v", err)
	}
	svgBytes, err := os.ReadFile(output) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read large burndown SVG: %v", err)
	}
	svg := string(svgBytes)
	if !strings.Contains(svg, ">80000</text>") {
		t.Fatalf("large burndown SVG does not contain a full-magnitude y tick")
	}
	if strings.Contains(svg, ">1e5</text>") || strings.Contains(svg, ">1e4</text>") {
		t.Fatalf("large burndown SVG contains detached scientific offset text")
	}
	if strings.Contains(svg, ">large 2 x 3 (granularity 30, sampling 30)</text>") {
		t.Fatalf("large burndown SVG contains a title despite HideTitle")
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
