package ast

import (
	"testing"

	sitter "github.com/odvcencio/gotreesitter"
)

func TestTreeSitterExtractorUnsupported(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	nodes, err := extractor.Extract("README.md", []byte("# title"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected no nodes for unsupported extension, got %d", len(nodes))
	}
}

func TestTreeSitterExtractorGo(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	source := []byte(`package demo

func Alpha() int {
	return 1
}

type T struct{}

func (t T) Beta() int {
	return 2
}
`)
	nodes, err := extractor.Extract("demo.go", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "Alpha" || nodes[0].StartLine != 3 || nodes[0].EndLine != 5 {
		t.Fatalf("unexpected first node: %+v", nodes[0])
	}
	if nodes[1].Name != "Beta" || nodes[1].StartLine != 9 || nodes[1].EndLine != 11 {
		t.Fatalf("unexpected second node: %+v", nodes[1])
	}
}

func TestTreeSitterExtractorPython(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	source := []byte(`def alpha():
    return 1

class T:
    def beta(self):
        return 2
`)
	nodes, err := extractor.Extract("demo.py", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "alpha" || nodes[0].StartLine != 1 || nodes[0].EndLine != 2 {
		t.Fatalf("unexpected first node: %+v", nodes[0])
	}
	if nodes[1].Name != "beta" || nodes[1].StartLine != 5 || nodes[1].EndLine != 6 {
		t.Fatalf("unexpected second node: %+v", nodes[1])
	}
}

func TestTreeSitterExtractorJavaScriptAndTypeScript(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	jsSource := []byte(`function alpha() {
  return 1
}
class T {
  beta() {
    return 2
  }
}
`)
	jsNodes, err := extractor.Extract("demo.js", jsSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jsNodes) != 2 {
		t.Fatalf("expected 2 JS nodes, got %d", len(jsNodes))
	}
	if jsNodes[0].Name != "alpha" || jsNodes[0].StartLine != 1 || jsNodes[0].EndLine != 3 {
		t.Fatalf("unexpected JS first node: %+v", jsNodes[0])
	}
	if jsNodes[1].Name != "beta" || jsNodes[1].StartLine != 5 || jsNodes[1].EndLine != 7 {
		t.Fatalf("unexpected JS second node: %+v", jsNodes[1])
	}

	tsSource := []byte(`function gamma(): number {
  return 3
}
`)
	tsNodes, err := extractor.Extract("demo.ts", tsSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tsNodes) != 1 {
		t.Fatalf("expected 1 TS node, got %d", len(tsNodes))
	}
	if tsNodes[0].Name != "gamma" || tsNodes[0].StartLine != 1 || tsNodes[0].EndLine != 3 {
		t.Fatalf("unexpected TS node: %+v", tsNodes[0])
	}
}

func TestTreeSitterExtractorIdentifiers(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	source := []byte(`package demo

func alpha() int {
	var cnt = 1
	return cnt
}
`)
	nodes, err := extractor.ExtractIdentifiers("demo.go", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected identifier nodes")
	}
	found := false
	for _, node := range nodes {
		if node.Name == "cnt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find identifier 'cnt' in %+v", nodes)
	}
}

func TestTreeSitterExtractorComments(t *testing.T) {
	extractor := NewTreeSitterExtractor()
	source := []byte(`package demo
// hello world
func alpha() int {
	return 1 // trailing
}
`)
	nodes, err := extractor.ExtractComments("demo.go", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 comment nodes, got %d", len(nodes))
	}
}

func TestExtractNamedNodes(t *testing.T) {
	source := []byte(`package demo

func alpha() int {
	return 1
}
`)
	nodes, err := ExtractNamedNodes("demo.go", source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected named nodes for Go source")
	}
	foundFunction := false
	for _, node := range nodes {
		if node.Type == "ast:function_declaration" && node.StartLine == 3 && node.EndLine == 5 {
			foundFunction = true
			break
		}
	}
	if !foundFunction {
		t.Fatalf("expected function_declaration node, got %+v", nodes)
	}
}

func TestExtractNamedNodesInRangesUnsupported(t *testing.T) {
	nodes, err := ExtractNamedNodesInRanges("README.md", []byte("# title"), []LineRange{{Start: 1, End: 1}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodes != nil {
		t.Fatalf("expected nil nodes for unsupported extension, got %+v", nodes)
	}
}

func TestExtractNamedNodesInRangesEmptyInputs(t *testing.T) {
	cases := map[string]struct {
		source []byte
		ranges []LineRange
	}{
		"empty source":              {source: nil, ranges: []LineRange{{Start: 1, End: 1}}},
		"empty ranges":              {source: []byte("package demo\n"), ranges: nil},
		"all ranges out of bounds":  {source: []byte("package demo\n"), ranges: []LineRange{{Start: 99, End: 100}}},
		"empty interval after clamp": {source: []byte("package demo\n"), ranges: []LineRange{{Start: 5, End: 2}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			nodes, err := ExtractNamedNodesInRanges("demo.go", tc.source, tc.ranges)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if nodes != nil {
				t.Fatalf("expected nil nodes, got %+v", nodes)
			}
		})
	}
}

// TestExtractNamedNodesInRangesEquivalence verifies that the range-limited
// extractor returns the same nodes (within the requested interval) that a
// full-file parse would surface.
func TestExtractNamedNodesInRangesEquivalence(t *testing.T) {
	source := []byte(`package demo

func alpha() int {
	return 1
}

func beta() int {
	return 2
}

func gamma() int {
	return 3
}
`)
	full, err := ExtractNamedNodes("demo.go", source)
	if err != nil {
		t.Fatalf("ExtractNamedNodes failed: %v", err)
	}
	// Restrict to the line range covering beta() (lines 7..9 in 1-indexed terms).
	ranges := []LineRange{{Start: 7, End: 9}}
	limited, err := ExtractNamedNodesInRanges("demo.go", source, ranges)
	if err != nil {
		t.Fatalf("ExtractNamedNodesInRanges failed: %v", err)
	}
	if len(limited) == 0 {
		t.Fatal("expected non-empty node list for restricted parse")
	}
	// Every node returned by the limited parse must overlap the requested range.
	// Function-level node IDs must also be present in the full-file output —
	// the synthetic root container (source_file) is excluded because tree-sitter
	// reports its extents based on the included ranges, not the whole file.
	fullByID := map[string]Node{}
	for _, n := range full {
		fullByID[n.ID] = n
	}
	for _, n := range limited {
		if n.EndLine < 7 || n.StartLine > 9 {
			t.Fatalf("limited parse returned node outside requested range: %+v", n)
		}
		if n.Type == "ast:source_file" {
			continue
		}
		if _, ok := fullByID[n.ID]; !ok {
			t.Fatalf("limited parse produced node missing from full parse: %+v", n)
		}
	}
	// The full parse should at least cover every node the limited parse found.
	// (The reverse need not hold — the partial syntax tree may resynchronise
	// differently around uncovered regions and yield extra named nodes.)
	beta := false
	for _, n := range limited {
		if n.Type == "ast:function_declaration" && n.StartLine == 7 && n.EndLine == 9 {
			beta = true
			break
		}
	}
	if !beta {
		t.Fatalf("expected function_declaration for beta() in limited parse, got %+v", limited)
	}
}

func TestComputeLineStarts(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   []int
	}{
		{"empty", "", []int{0}},
		{"no newline", "abc", []int{0, 3}},
		{"single newline", "ab\n", []int{0, 3}},
		{"trailing content", "ab\ncd", []int{0, 3, 5}},
		{"multiple lines", "a\nb\nc\n", []int{0, 2, 4, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeLineStarts([]byte(tc.source))
			if len(got) != len(tc.want) {
				t.Fatalf("length mismatch: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("offset %d: got %v, want %v", i, got, tc.want)
				}
			}
		})
	}
}

func TestBuildSitterRangesMergesAndSorts(t *testing.T) {
	source := []byte("a\nb\nc\nd\ne\nf\n")
	lineStarts := computeLineStarts(source)
	numLines := len(lineStarts) - 1
	// Lines 1..2 and 2..3 overlap and should merge to 1..3. Lines 5..6 are
	// separated from 1..3 by a gap (line 4) and must remain a second range.
	ranges := []LineRange{
		{Start: 5, End: 6},
		{Start: 1, End: 2},
		{Start: 2, End: 3},
	}
	got := buildSitterRanges(ranges, lineStarts, numLines)
	if len(got) != 2 {
		t.Fatalf("expected 2 merged ranges, got %d: %+v", len(got), got)
	}
	if got[0].StartByte != 0 || got[0].EndByte != 6 {
		t.Fatalf("first range bytes wrong: %+v", got[0])
	}
	if got[1].StartByte != 8 || got[1].EndByte != 12 {
		t.Fatalf("second range bytes wrong: %+v", got[1])
	}
	// Tree-sitter requires sorted, non-overlapping ranges.
	for i := 1; i < len(got); i++ {
		if got[i].StartByte < got[i-1].EndByte {
			t.Fatalf("ranges out of order: %+v", got)
		}
	}
	// Points must be consistent with bytes (Row/Column derived from offsets).
	for _, r := range got {
		want := pointAt(int(r.StartByte), lineStarts)
		if r.StartPoint != want {
			t.Fatalf("StartPoint mismatch for %+v: want %+v", r, want)
		}
		want = pointAt(int(r.EndByte), lineStarts)
		if r.EndPoint != want {
			t.Fatalf("EndPoint mismatch for %+v: want %+v", r, want)
		}
	}
}

func TestBuildSitterRangesAdjacentMerge(t *testing.T) {
	source := []byte("a\nb\nc\nd\n")
	lineStarts := computeLineStarts(source)
	numLines := len(lineStarts) - 1
	// Adjacent ranges (no gap) should merge.
	ranges := []LineRange{
		{Start: 1, End: 2},
		{Start: 3, End: 4},
	}
	got := buildSitterRanges(ranges, lineStarts, numLines)
	if len(got) != 1 {
		t.Fatalf("expected adjacent ranges to merge, got %d: %+v", len(got), got)
	}
	if got[0].StartByte != 0 || got[0].EndByte != 8 {
		t.Fatalf("merged range bytes wrong: %+v", got[0])
	}
}

var _ = sitter.Point{} // ensure sitter import survives even if test bodies change

func TestBuildSitterRangesClampsAndDrops(t *testing.T) {
	source := []byte("a\nb\nc\n")
	lineStarts := computeLineStarts(source)
	numLines := len(lineStarts) - 1
	ranges := []LineRange{
		{Start: -3, End: 1},  // clamps to 1..1
		{Start: 99, End: 100}, // dropped
		{Start: 5, End: 2},    // empty after clamp → dropped
	}
	got := buildSitterRanges(ranges, lineStarts, numLines)
	if len(got) != 1 {
		t.Fatalf("expected 1 range, got %d: %+v", len(got), got)
	}
	if got[0].StartByte != 0 || got[0].EndByte != 2 {
		t.Fatalf("clamped range wrong: %+v", got[0])
	}
}
