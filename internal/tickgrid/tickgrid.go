// Package tickgrid holds the rule that turns wall-clock time into hercules'
// tick grid.
//
// It exists as its own package because both sides of the product need the same
// rule and neither can reasonably depend on the other: the analysis pipeline
// (internal/plumbing) mints tick indices with it, and the renderer
// (internal/render) has to convert them back into dates. Pulling
// internal/plumbing into the renderer for this would link go-git, tree-sitter
// and enry into the labours binary for the sake of three lines, and a second
// copy of the rule is what put the two grids out of step in the first place.
package tickgrid

import "time"

// FloorTime is the missing implementation of time.Time.Floor() - round to the
// nearest instant less than or equal to t.
//
// The grid it produces is anchored on Go's zero time (January 1 of year 1, UTC),
// because that is what time.Time.Round measures from. That anchor is load
// bearing: a grid floored on the Unix epoch instead agrees with this one only
// when the tick size divides the offset between the two, which holds for 24h
// and its divisors but not for, say, a 5-, 7- or 36-hour tick. Flooring a
// timestamp on the wrong grid shifts every date derived from it and can move a
// date filter's boundary onto the neighbouring tick.
func FloorTime(t time.Time, d time.Duration) time.Time {
	// We have check if the regular rounding resulted in Floor() + d.
	result := t.Round(d)
	if result.After(t) {
		result = result.Add(-d)
	}

	return result
}
