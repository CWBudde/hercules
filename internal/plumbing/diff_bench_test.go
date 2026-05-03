package plumbing

import (
	"fmt"
	"strings"
	"testing"

	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
)

// buildLargeGoSource synthesizes a Go file with `funcs` short functions, totalling
// roughly 5*funcs lines. Used to compare full-file vs range-limited tree-sitter
// parsing under the same workload.
func buildLargeGoSource(funcs int) []byte {
	var b strings.Builder
	b.WriteString("package bench\n\n")
	for i := 0; i < funcs; i++ {
		fmt.Fprintf(&b, "func Fn%d(x int) int {\n\ty := x + %d\n\treturn y * %d\n}\n\n", i, i, i+1)
	}
	return []byte(b.String())
}

func BenchmarkExtractNamedNodesFullFile(b *testing.B) {
	source := buildLargeGoSource(1000) // ~5000 lines
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		nodes, err := ast_items.ExtractNamedNodes("bench.go", source)
		if err != nil {
			b.Fatalf("ExtractNamedNodes: %v", err)
		}
		if len(nodes) == 0 {
			b.Fatal("expected nodes")
		}
	}
}

func BenchmarkExtractNamedNodesInRangesSmallDiff(b *testing.B) {
	source := buildLargeGoSource(1000)
	// Mimic the typical refinement workload: a single ~10-line interval somewhere
	// in the middle of a 5000-line file.
	ranges := []ast_items.LineRange{{Start: 2500, End: 2510}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		nodes, err := ast_items.ExtractNamedNodesInRanges("bench.go", source, ranges)
		if err != nil {
			b.Fatalf("ExtractNamedNodesInRanges: %v", err)
		}
		if len(nodes) == 0 {
			b.Fatal("expected nodes")
		}
	}
}

func BenchmarkExtractNamedNodesInRangesLargeFraction(b *testing.B) {
	source := buildLargeGoSource(1000)
	// 50% coverage as a sanity check — the routing in
	// extractNodesForRefinement would normally fall back to the full parse here.
	ranges := []ast_items.LineRange{{Start: 1, End: 2500}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		nodes, err := ast_items.ExtractNamedNodesInRanges("bench.go", source, ranges)
		if err != nil {
			b.Fatalf("ExtractNamedNodesInRanges: %v", err)
		}
		if len(nodes) == 0 {
			b.Fatal("expected nodes")
		}
	}
}
