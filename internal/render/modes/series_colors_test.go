package modes

import (
	"strconv"
	"testing"
)

// A stacked bar chart is only readable while neighbouring series differ, so the
// interesting sizes are the ones around and past the palette size - that is
// where the old spread formula started rounding two developers onto one color.
func TestSampledTab20ColorsGivesEverySeriesItsOwnColor(t *testing.T) {
	for _, n := range []int{1, 2, 8, 19, 20, 21, 25, 41, 60} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			colors := sampledTab20Colors(n)
			if len(colors) != n {
				t.Fatalf("got %d colors, want %d", len(colors), n)
			}

			seen := make(map[[4]float64]int, n)
			for i, c := range colors {
				key := [4]float64{c.R, c.G, c.B, c.A}
				if first, dup := seen[key]; dup {
					t.Fatalf("series %d repeats the color of series %d: %v", i, first, key)
				}

				seen[key] = i
			}
		})
	}
}

func TestSampledTab20ColorsHandlesEmptySeries(t *testing.T) {
	if colors := sampledTab20Colors(0); colors != nil {
		t.Fatalf("expected no colors for an empty series set, got %v", colors)
	}

	if colors := sampledTab20Colors(-3); colors != nil {
		t.Fatalf("expected no colors for a negative count, got %v", colors)
	}
}

// The palette is reused once there are more series than entries, but a reused
// entry must stay visibly apart from the one it repeats.
func TestSampledTab20ColorsDarkensRepeatedPaletteEntries(t *testing.T) {
	const paletteSize = 20

	colors := sampledTab20Colors(paletteSize * 2)
	for i := range paletteSize {
		first, repeat := colors[i], colors[i+paletteSize]
		if repeat.R >= first.R || repeat.G >= first.G || repeat.B >= first.B {
			t.Fatalf("series %d (%v) is not darker than series %d (%v)", i+paletteSize, repeat, i, first)
		}
	}
}
