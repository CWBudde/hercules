package ast

import "testing"

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
