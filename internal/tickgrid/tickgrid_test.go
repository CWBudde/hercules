package tickgrid

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var floorTimeTicks = []time.Duration{5 * time.Hour, 7 * time.Hour, 24 * time.Hour, 36 * time.Hour}

func floorTimeInstants(tick time.Duration) []time.Time {
	anchor := time.Time{} // January 1 of year 1, 00:00:00 UTC

	return []time.Time{
		anchor,
		anchor.Add(12345 * tick),                              // exactly on the grid
		anchor.Add(12345*tick + time.Nanosecond),              // just past a boundary
		anchor.Add(12346*tick - time.Nanosecond),              // just before the next boundary
		time.Date(1930, time.June, 15, 13, 7, 0, 0, time.UTC), // pre-1970
		time.Date(1969, time.December, 31, 23, 59, 59, 999999999, time.UTC),
		time.Unix(0, 0).UTC(), // the Unix epoch
		time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC),  // midnight
		time.Date(2024, time.February, 29, 12, 0, 0, 0, time.UTC), // noon
		time.Date(2024, time.February, 29, 23, 59, 59, 999999999, time.UTC),
	}
}

func TestFloorTimeContract(t *testing.T) {
	for _, tick := range floorTimeTicks {
		for _, instant := range floorTimeInstants(tick) {
			floored := FloorTime(instant, tick)

			assert.Falsef(t, floored.After(instant), "%v: floor(%v) = %v exceeds the instant", tick, instant, floored)
			assert.Truef(t, floored.After(instant.Add(-tick)),
				"%v: floor(%v) = %v is more than one tick below the instant", tick, instant, floored)
			assert.Truef(t, floored.Equal(FloorTime(floored, tick)),
				"%v: floor(%v) = %v is not a fixed point", tick, instant, floored)
			assert.Truef(t, floored.Equal(instant.Truncate(tick)),
				"%v: floor(%v) = %v differs from Truncate, which rounds down from the same zero-time anchor",
				tick, instant, floored)
		}
	}
}

// TestFloorTimeIsAnchoredOnYearOne pins the grid's anchor: FloorTime measures
// from Go's zero time like time.Time.Round and time.Time.Truncate do, not from
// the Unix epoch. The two grids only coincide when the tick divides the offset
// between the anchors (719162 days), which 24h does and 5h, 7h and 36h do not.
func TestFloorTimeIsAnchoredOnYearOne(t *testing.T) {
	instant := time.Date(2024, time.March, 10, 5, 0, 0, 0, time.UTC)
	agreesWithEpoch := map[time.Duration]bool{
		5 * time.Hour:  false,
		7 * time.Hour:  false,
		24 * time.Hour: true,
		36 * time.Hour: false,
	}

	for _, tick := range floorTimeTicks {
		floored := FloorTime(instant, tick)

		// Round semantics: the nearest grid point, stepped back one tick when it lies ahead.
		expected := instant.Round(tick)
		if expected.After(instant) {
			expected = expected.Add(-tick)
		}

		assert.Truef(t, floored.Equal(expected), "%v: got %v, want %v", tick, floored, expected)

		tickSeconds := int64(tick / time.Second)
		epochFloored := time.Unix(instant.Unix()-instant.Unix()%tickSeconds, 0).UTC()
		assert.Equalf(t, agreesWithEpoch[tick], floored.Equal(epochFloored),
			"%v: year-1 floor %v vs Unix-epoch floor %v", tick, floored, epochFloored)
	}
}
