package graphics

import (
	"image/color"
	"strconv"
	"testing"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
)

// A stacked chart of categorical series - repositories, developers, languages -
// is only readable while every band wears its own color. tab20 has twenty
// entries, so the interesting sizes are the ones around and past it.
func TestDistinctSeriesColorsGivesEverySeriesItsOwnColor(t *testing.T) {
	for _, n := range []int{1, 2, 8, 19, 20, 21, 32, 41, 60} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			colors := DistinctSeriesColors(n)
			if len(colors) != n {
				t.Fatalf("got %d colors, want %d", len(colors), n)
			}

			seen := make(map[color.Color]int, n)

			for i, c := range colors {
				key := color.RGBAModel.Convert(c)
				if first, dup := seen[key]; dup {
					t.Fatalf("series %d repeats the color of series %d: %#v", i, first, key)
				}

				seen[key] = i
			}
		})
	}
}

func TestDistinctSeriesColorsHandlesEmptySeries(t *testing.T) {
	if colors := DistinctSeriesColors(0); colors != nil {
		t.Fatalf("expected no colors for an empty series set, got %v", colors)
	}

	if colors := DistinctSeriesColors(-3); colors != nil {
		t.Fatalf("expected no colors for a negative count, got %v", colors)
	}
}

// Past the palette size an entry has to be reused, but the repeat must stay
// visibly apart from the series it repeats - that is the whole point of the
// scheme, and what makes a 32-repository legend readable.
func TestDistinctSeriesColorsDarkensRepeatedPaletteEntries(t *testing.T) {
	const paletteSize = 20

	colors := DistinctSeriesColors(paletteSize * 2)
	for i := range paletteSize {
		firstR, firstG, firstB, _ := colors[i].RGBA()
		repeatR, repeatG, repeatB, _ := colors[i+paletteSize].RGBA()

		if repeatR >= firstR || repeatG >= firstG || repeatB >= firstB {
			t.Fatalf("series %d (%#v) is not darker than series %d (%#v)",
				i+paletteSize, colors[i+paletteSize], i, colors[i])
		}
	}
}

// Age-band burndowns keep the Python palette: their layers are ordered time
// buckets and their coloring is pinned to the original. Categorical layers -
// the combined repository chart - take the distinct scheme instead.
func TestBurndownStackColorsFollowLayerKind(t *testing.T) {
	const layers = 24

	data := &burndown.ProcessedBurndown{
		Matrix:    make([][]float64, layers),
		DateRange: []time.Time{time.Unix(0, 0), time.Unix(3600, 0)},
	}
	for i := range data.Matrix {
		data.Matrix[i] = []float64{1, 1}
	}

	_, ageBand, _ := prepareBurndownStackData(data, false)
	if ageBand[0] != ageBand[20] {
		t.Errorf("age bands no longer cycle the Python palette: %#v vs %#v", ageBand[0], ageBand[20])
	}

	_, categorical, _ := prepareBurndownStackData(data, true)
	if categorical[0] == categorical[20] {
		t.Errorf("categorical layer %d wears the color of layer 0: %#v", 20, categorical[20])
	}
}
