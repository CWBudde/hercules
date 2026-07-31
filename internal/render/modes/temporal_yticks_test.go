package modes

import (
	"fmt"
	"testing"
)

// The y axis has to stay readable at every magnitude: a combined run reaches a
// few thousand commits, where a fixed step drew a hundred overlapping labels.
func TestTemporalActivityYTicksStayReadableAtEveryMagnitude(t *testing.T) {
	for _, maxValue := range []float64{1, 7, 42, 99, 350, 2194, 58000, 1200000} {
		t.Run(fmt.Sprint(maxValue), func(t *testing.T) {
			ticks := temporalActivityYTicks(maxValue)
			if len(ticks) < 2 {
				t.Fatalf("got %d ticks for max %v, want at least 2", len(ticks), maxValue)
			}

			if len(ticks) > 12 {
				t.Fatalf("got %d ticks for max %v, want a readable axis", len(ticks), maxValue)
			}

			if ticks[0] != 0 {
				t.Fatalf("first tick is %v, want the bars to sit on 0", ticks[0])
			}

			step := ticks[1] - ticks[0]
			if step < 1 {
				t.Fatalf("step %v labels a fraction of a commit", step)
			}

			for i := 1; i < len(ticks); i++ {
				if got := ticks[i] - ticks[i-1]; got != step {
					t.Fatalf("tick %d is %v after the previous one, want a uniform %v", i, got, step)
				}
			}

			if last := ticks[len(ticks)-1]; last > maxValue {
				t.Fatalf("last tick %v exceeds the data maximum %v", last, maxValue)
			}
		})
	}
}

func TestTemporalActivityYTicksHandlesAnEmptyChart(t *testing.T) {
	if got := temporalActivityYTicks(0); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("got %v, want a 0..1 axis for an empty chart", got)
	}
}
