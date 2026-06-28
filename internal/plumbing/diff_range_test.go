package plumbing

import (
	"testing"

	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// runeLines builds a synthetic line-as-rune string of N lines using consecutive
// rune codepoints starting at offset. DiffLinesToRunes uses one rune per line,
// so this matches what FileDiff.Consume passes into diffmatchpatch.
func runeLines(n int, offset rune) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = offset + rune(i)
	}
	return string(out)
}

func TestCollectSuspiciousBoundariesNone(t *testing.T) {
	// Pure equal/insert sequence with no shared prefix → no suspicious boundary.
	diffs := []diffmatchpatch.Diff{
		{Type: diffmatchpatch.DiffEqual, Text: runeLines(3, 'a')},
		{Type: diffmatchpatch.DiffInsert, Text: runeLines(2, 'A')},
		{Type: diffmatchpatch.DiffEqual, Text: runeLines(3, 'd')},
	}
	if got := collectSuspiciousBoundaries(diffs); len(got) != 0 {
		t.Fatalf("expected no boundaries, got %+v", got)
	}
}

func TestCollectSuspiciousBoundariesOne(t *testing.T) {
	prefix := runeLines(2, 'X')
	diffs := []diffmatchpatch.Diff{
		{Type: diffmatchpatch.DiffEqual, Text: runeLines(3, 'a')},           // 3 lines (0..2)
		{Type: diffmatchpatch.DiffInsert, Text: prefix + runeLines(1, 'I')}, // 3 inserted lines, shares 2-line prefix with next
		{Type: diffmatchpatch.DiffEqual, Text: prefix + runeLines(2, 'd')},
	}
	got := collectSuspiciousBoundaries(diffs)
	if len(got) != 1 {
		t.Fatalf("expected 1 boundary, got %+v", got)
	}
	if got[0].diffIndex != 1 {
		t.Fatalf("diffIndex: got %d want 1", got[0].diffIndex)
	}
	if got[0].line != 3 {
		t.Fatalf("line: got %d want 3", got[0].line)
	}
	if got[0].size != 3 {
		t.Fatalf("size: got %d want 3", got[0].size)
	}
	if got[0].matched != 2 {
		t.Fatalf("matched: got %d want 2", got[0].matched)
	}
}

func TestBoundariesToRangesEmpty(t *testing.T) {
	if got := boundariesToRanges(nil, 100); got != nil {
		t.Fatalf("expected nil for empty boundaries, got %+v", got)
	}
	if got := boundariesToRanges([]suspiciousBoundary{{}}, 0); got != nil {
		t.Fatalf("expected nil for newLOC <= 0, got %+v", got)
	}
}

func TestBoundariesToRangesSingleAndClamp(t *testing.T) {
	// boundary at line=10, size=5, matched=3 → range covers
	// 1-indexed lines [11, 18] (10+1 .. 10+5+3).
	bs := []suspiciousBoundary{{line: 10, size: 5, matched: 3}}
	got := boundariesToRanges(bs, 100)
	if len(got) != 1 || got[0] != (ast_items.LineRange{Start: 11, End: 18}) {
		t.Fatalf("unexpected range: %+v", got)
	}

	// Same boundary against a 12-line file → end clamps to 12.
	got = boundariesToRanges(bs, 12)
	if len(got) != 1 || got[0] != (ast_items.LineRange{Start: 11, End: 12}) {
		t.Fatalf("expected clamped range, got %+v", got)
	}

	// Boundary whose start is past EOF → dropped.
	got = boundariesToRanges([]suspiciousBoundary{{line: 50, size: 1, matched: 1}}, 12)
	if len(got) != 0 {
		t.Fatalf("expected no ranges for out-of-bounds boundary, got %+v", got)
	}
}

func TestBoundariesToRangesMultiple(t *testing.T) {
	bs := []suspiciousBoundary{
		{line: 0, size: 2, matched: 1},  // → [1, 3]
		{line: 50, size: 4, matched: 2}, // → [51, 56]
	}
	got := boundariesToRanges(bs, 100)
	if len(got) != 2 {
		t.Fatalf("expected 2 ranges, got %+v", got)
	}
	if got[0] != (ast_items.LineRange{Start: 1, End: 3}) {
		t.Fatalf("first range: got %+v", got[0])
	}
	if got[1] != (ast_items.LineRange{Start: 51, End: 56}) {
		t.Fatalf("second range: got %+v", got[1])
	}
}

func TestCoverageRatio(t *testing.T) {
	cases := []struct {
		name    string
		ranges  []ast_items.LineRange
		newLOC  int
		want    float64
		epsilon float64
	}{
		{"empty", nil, 100, 0, 0},
		{"zero LOC", []ast_items.LineRange{{Start: 1, End: 5}}, 0, 0, 0},
		{"single range", []ast_items.LineRange{{Start: 1, End: 10}}, 100, 0.10, 1e-9},
		{"two disjoint", []ast_items.LineRange{{Start: 1, End: 5}, {Start: 10, End: 14}}, 100, 0.10, 1e-9},
		{"overlapping", []ast_items.LineRange{{Start: 1, End: 10}, {Start: 5, End: 15}}, 100, 0.15, 1e-9},
		{"adjacent merge", []ast_items.LineRange{{Start: 1, End: 5}, {Start: 6, End: 10}}, 100, 0.10, 1e-9},
		{"clamped to 1.0", []ast_items.LineRange{{Start: 1, End: 200}}, 100, 1.0, 1e-9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coverageRatio(tc.ranges, tc.newLOC)
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > tc.epsilon {
				t.Fatalf("coverageRatio = %v, want %v", got, tc.want)
			}
		})
	}
}

// routingDecision mirrors extractNodesForRefinement's branching so we can
// assert which extractor would be picked without running tree-sitter.
func routingDecision(ranges []ast_items.LineRange, newLOC int, mode string) string {
	switch mode {
	case RefineModeFull:
		return "full"
	case RefineModeRange:
		if len(ranges) == 0 {
			return "full"
		}
		return "range"
	default:
		if len(ranges) == 0 || newLOC < refineRangeMinLines {
			return "full"
		}
		padded := padRanges(ranges, newLOC, refineRangePadding)
		if coverageRatio(padded, newLOC) >= refineCoverageThreshold {
			return "full"
		}
		return "range"
	}
}

func TestRefineRoutingAuto(t *testing.T) {
	cases := []struct {
		name   string
		ranges []ast_items.LineRange
		newLOC int
		want   string
	}{
		{"no ranges → full", nil, 5000, "full"},
		{"small file → full (size gate)", []ast_items.LineRange{{Start: 1, End: 5}}, 500, "full"},
		{"large file, tiny diff → range", []ast_items.LineRange{{Start: 1, End: 5}}, 5000, "range"},
		{"large file, half coverage → full (threshold)", []ast_items.LineRange{{Start: 1, End: 3000}}, 5000, "full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := routingDecision(tc.ranges, tc.newLOC, RefineModeAuto); got != tc.want {
				t.Fatalf("auto routing: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRefineRoutingForcedModes(t *testing.T) {
	ranges := []ast_items.LineRange{{Start: 1, End: 5}}
	// RefineModeFull always picks full, regardless of size or coverage.
	for _, newLOC := range []int{50, 5000, 100000} {
		if got := routingDecision(ranges, newLOC, RefineModeFull); got != "full" {
			t.Fatalf("RefineModeFull@%d: got %q, want full", newLOC, got)
		}
	}
	// RefineModeRange always picks range when ranges are present, even on tiny
	// files where auto would have bailed out.
	if got := routingDecision(ranges, 50, RefineModeRange); got != "range" {
		t.Fatalf("RefineModeRange tiny file: got %q, want range", got)
	}
	// RefineModeRange still falls back to full when the heuristic produced no
	// ranges to parse.
	if got := routingDecision(nil, 5000, RefineModeRange); got != "full" {
		t.Fatalf("RefineModeRange empty ranges: got %q, want full", got)
	}
}

func TestPadRanges(t *testing.T) {
	cases := []struct {
		name   string
		input  []ast_items.LineRange
		newLOC int
		pad    int
		want   []ast_items.LineRange
	}{
		{"zero pad is identity", []ast_items.LineRange{{Start: 5, End: 10}}, 100, 0,
			[]ast_items.LineRange{{Start: 5, End: 10}}},
		{"negative pad is identity", []ast_items.LineRange{{Start: 5, End: 10}}, 100, -3,
			[]ast_items.LineRange{{Start: 5, End: 10}}},
		{"empty input", nil, 100, 5, nil},
		{"interior pad", []ast_items.LineRange{{Start: 50, End: 60}}, 200, 5,
			[]ast_items.LineRange{{Start: 45, End: 65}}},
		{"clamps low side", []ast_items.LineRange{{Start: 3, End: 7}}, 200, 10,
			[]ast_items.LineRange{{Start: 1, End: 17}}},
		{"clamps high side", []ast_items.LineRange{{Start: 90, End: 100}}, 100, 10,
			[]ast_items.LineRange{{Start: 80, End: 100}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := padRanges(tc.input, tc.newLOC, tc.pad)
			if len(got) != len(tc.want) {
				t.Fatalf("length mismatch: got %+v want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("range %d: got %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
