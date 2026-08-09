package modes

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
)

// nanosecondsPerSecond converts the int64 nanosecond tick sizes the reader
// structs carry (pb.proto stores Go's time.Duration verbatim) into the seconds
// the burndown date helpers work in.
const nanosecondsPerSecond = float64(time.Second)

// tickAxis maps tick indices onto real dates.
//
// Tick 0 does **not** start at the header's begin time. GetHeader reports the
// raw committer timestamp of the first analysed commit, while ticks are floored
// onto a UTC-anchored grid of tick-size multiples (internal/plumbing/ticks.go,
// FloorTime). The two differ by up to one full tick, so anchoring on the begin
// time directly - as report_temporal_activity.go used to - shifts every point
// late by that much and can push a filter boundary onto the wrong tick.
type tickAxis struct {
	// start is the instant tick 0 begins, in UTC.
	start time.Time
	// step is how long one tick lasts.
	step time.Duration
}

// newTickAxis builds the axis from a header begin time in unix seconds and a
// tick size in nanoseconds. It reports false when either input is missing, which
// is the caller's signal to keep plotting bare tick indices: a zero header would
// otherwise anchor the whole series at 1970, which is worse than an ugly axis.
func newTickAxis(beginUnix, tickSizeNanos int64) (tickAxis, bool) {
	if beginUnix <= 0 || tickSizeNanos <= 0 {
		return tickAxis{}, false
	}

	tickSizeSeconds := float64(tickSizeNanos) / nanosecondsPerSecond

	return tickAxis{
		start: burndown.FloorDateTime(time.Unix(beginUnix, 0).UTC(), tickSizeSeconds),
		step:  time.Duration(tickSizeNanos),
	}, true
}

// dateOf returns the instant the given tick begins.
func (a tickAxis) dateOf(tick int) time.Time {
	return a.start.Add(time.Duration(tick) * a.step)
}

// datesOf converts a whole series of tick indices in one pass.
func (a tickAxis) datesOf(ticks []int) []time.Time {
	converted := make([]time.Time, len(ticks))
	for i, tick := range ticks {
		converted[i] = a.dateOf(tick)
	}

	return converted
}

// tickOf returns the index of the tick containing t. It floors, so an instant
// anywhere inside a tick maps to that tick, and instants before tick 0 yield
// negative indices rather than clamping - a caller filtering a range wants to
// know that its lower bound sits before the repository starts.
func (a tickAxis) tickOf(t time.Time) int {
	// Only the zero value has no step, and callers are meant to keep the tick
	// axis in that case rather than ask it questions.
	if a.step <= 0 {
		return 0
	}

	elapsed := t.Sub(a.start)
	tick := elapsed / a.step

	// Integer division truncates towards zero, so a negative remainder has to
	// be rounded down by hand or the tick before tick 0 comes out as 0.
	if elapsed < 0 && elapsed%a.step != 0 {
		tick--
	}

	return int(tick)
}

// tickRange is an inclusive window of tick indices.
type tickRange struct {
	first int
	last  int
}

// contains reports whether a tick falls inside the window.
func (r tickRange) contains(tick int) bool {
	return tick >= r.first && tick <= r.last
}

// tickRangeFor converts a requested date range into an inclusive tick window.
// Either boundary may be nil, in which case that end is left open. It reports
// false when no boundary was requested at all, so callers skip filtering
// entirely rather than walking their data to no effect.
//
// Whole ticks are selected, never clipped: a tick that merely overlaps the
// requested range is kept in full. That matches how temporal-activity has always
// filtered, and a partially counted tick would be a silently wrong data point
// rather than a coarse one.
func tickRangeFor(axis tickAxis, startTime, endTime *time.Time) (tickRange, bool) {
	if startTime == nil && endTime == nil {
		return tickRange{}, false
	}

	window := tickRange{first: math.MinInt, last: math.MaxInt}
	if startTime != nil {
		window.first = axis.tickOf(*startTime)
	}

	if endTime != nil {
		window.last = axis.tickOf(*endTime)
	}

	return window, true
}

// ticksInRange keeps the sorted tick indices that fall inside the window.
func ticksInRange(ticks []int, window tickRange) []int {
	kept := make([]int, 0, len(ticks))

	for _, tick := range ticks {
		if window.contains(tick) {
			kept = append(kept, tick)
		}
	}

	return kept
}

// tickIndexSeries pairs tick indices with their values for the fallback axis,
// where the x coordinate is the bare tick number.
func tickIndexSeries(ticks []int, values []float64) xySeries {
	series := make(xySeries, len(ticks))
	for i, tick := range ticks {
		series[i] = xyPoint{X: float64(tick), Y: values[i]}
	}

	return series
}

// selectReportTicks applies a requested --start-date/--end-date window to a
// sorted list of tick indices.
//
// When the axis could not be built the range cannot be mapped onto ticks at all,
// and the chart is rendered unfiltered - but it says so. Quietly ignoring a flag
// the user typed is the same failure mode as labelling a date axis "Tick".
func selectReportTicks(
	ticks []int,
	axis tickAxis,
	dated bool,
	startTime, endTime *time.Time,
	chart string,
) []int {
	if startTime == nil && endTime == nil {
		return ticks
	}

	// The unusable-axis check has to come before the window is built: without a
	// tick size there is nothing to divide by.
	if !dated {
		fmt.Fprintf(os.Stderr,
			"Warning: %s cannot honour --start-date/--end-date - the input carries no "+
				"begin time or tick size, so ticks cannot be dated\n", chart)

		return ticks
	}

	window, requested := tickRangeFor(axis, startTime, endTime)
	if !requested {
		return ticks
	}

	return ticksInRange(ticks, window)
}
