package ast

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// LineRange describes a 1-indexed, inclusive line interval in a source file.
// Used by ExtractNamedNodesInRanges to scope tree-sitter parsing to specific lines.
type LineRange struct {
	Start int
	End   int
}

// Node is the minimal structural unit needed by Shotness.
type Node struct {
	ID        string
	Type      string
	Name      string
	Text      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

// Extractor describes the parser backend needed by Shotness.
type Extractor interface {
	Extract(path string, source []byte) ([]Node, error)
}

type languageSpec struct {
	language            *sitter.Language
	functionNodeTypes   map[string]struct{}
	identifierNodeTypes map[string]struct{}
	commentNodeTypes    map[string]struct{}
}

var languageByExtension = map[string]languageSpec{
	".go": {
		language: grammars.GoLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_declaration": {},
			"method_declaration":   {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":       {},
			"field_identifier": {},
			"type_identifier":  {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".py": {
		language: grammars.PythonLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_definition": {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".js": {
		language: grammars.JavascriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".jsx": {
		language: grammars.JavascriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".mjs": {
		language: grammars.JavascriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".cjs": {
		language: grammars.JavascriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".ts": {
		language: grammars.TypescriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function":                       {},
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
			"type_identifier":             {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".tsx": {
		language: grammars.TypescriptLanguage(),
		functionNodeTypes: map[string]struct{}{
			"function":                       {},
			"function_declaration":           {},
			"generator_function_declaration": {},
			"method_definition":              {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier":                  {},
			"property_identifier":         {},
			"private_property_identifier": {},
			"type_identifier":             {},
		},
		commentNodeTypes: map[string]struct{}{
			"comment": {},
		},
	},
	".java": {
		language: grammars.JavaLanguage(),
		functionNodeTypes: map[string]struct{}{
			"method_declaration":      {},
			"constructor_declaration": {},
		},
		identifierNodeTypes: map[string]struct{}{
			"identifier": {},
		},
		commentNodeTypes: map[string]struct{}{
			"line_comment":  {},
			"block_comment": {},
		},
	},
}

// TreeSitterExtractor implements Extractor with tree-sitter grammars.
type TreeSitterExtractor struct{}

// NewTreeSitterExtractor initializes the default parser backend.
func NewTreeSitterExtractor() *TreeSitterExtractor {
	return &TreeSitterExtractor{}
}

// Extract returns function-like nodes for supported languages.
func (*TreeSitterExtractor) Extract(path string, source []byte) ([]Node, error) {
	return extractByTypes(path, source, func(spec languageSpec) map[string]struct{} {
		return spec.functionNodeTypes
	}, true, true)
}

// ExtractIdentifiers returns identifier-like nodes for supported languages.
func (*TreeSitterExtractor) ExtractIdentifiers(path string, source []byte) ([]Node, error) {
	return extractByTypes(path, source, func(spec languageSpec) map[string]struct{} {
		return spec.identifierNodeTypes
	}, false, true)
}

// ExtractComments returns comment nodes for supported languages.
func (*TreeSitterExtractor) ExtractComments(path string, source []byte) ([]Node, error) {
	return extractByTypes(path, source, func(spec languageSpec) map[string]struct{} {
		return spec.commentNodeTypes
	}, false, false)
}

// parseFull parses an entire source file with the given language.
func parseFull(source []byte, lang *sitter.Language) (*sitter.Node, error) {
	parser := sitter.NewParser(lang)

	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}

	if tree == nil {
		return nil, errors.New("tree-sitter returned nil tree")
	}

	root := tree.RootNode()
	if root == nil {
		return nil, errors.New("tree-sitter returned nil root")
	}

	return root, nil
}

// ExtractNamedNodes returns all named syntax nodes for supported languages.
// This is intended for line-level structural heuristics where broad node coverage is needed.
func ExtractNamedNodes(path string, source []byte) ([]Node, error) {
	spec, ok := languageByExtension[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil
	}

	root, err := parseFull(source, spec.language)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter failed to parse %s: %w", path, err)
	}

	return collectNamedNodes(root, spec.language), nil
}

// ExtractNamedNodesInRanges is the range-limited counterpart of ExtractNamedNodes.
// It instructs tree-sitter to only parse the line intervals listed in `ranges`,
// reducing per-call allocation from O(file_size) to O(diff_size) for large files
// with small diffs. Ranges may overlap or appear out of order; they are clamped
// to [1, numLines], sorted, and merged before being passed to tree-sitter (which
// requires non-overlapping, sorted ranges). Returns (nil, nil) for unsupported
// extensions or when the resulting range set is empty.
func ExtractNamedNodesInRanges(path string, source []byte, ranges []LineRange) ([]Node, error) {
	spec, ok := languageByExtension[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil
	}

	if len(source) == 0 || len(ranges) == 0 {
		return nil, nil
	}

	lineStarts := computeLineStarts(source)
	numLines := len(lineStarts) - 1

	sitterRanges := buildSitterRanges(ranges, lineStarts, numLines)
	if len(sitterRanges) == 0 {
		return nil, nil
	}

	parser := sitter.NewParser(spec.language)
	parser.SetIncludedRanges(sitterRanges)

	tree, err := parser.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter failed to parse %s: %w", path, err)
	}

	if tree == nil {
		return nil, fmt.Errorf("tree-sitter returned nil tree for %s", path)
	}

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("tree-sitter returned nil root for %s", path)
	}

	return collectNamedNodes(root, spec.language), nil
}

// collectNamedNodes is the shared walker used by ExtractNamedNodes and
// ExtractNamedNodesInRanges. It descends through unnamed nodes and emits one
// entry per named node, capturing its line/column extents.
func collectNamedNodes(root *sitter.Node, lang *sitter.Language) []Node {
	nodes := make([]Node, 0, 128)
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}

		if node.IsNamed() {
			startLine := int(node.StartPoint().Row) + 1

			endLine := max(int(node.EndPoint().Row)+1, startLine)

			nodes = append(nodes, Node{
				ID:        fmt.Sprintf("%d:%d:%s:%d:%d", startLine, endLine, node.Type(lang), node.StartPoint().Column, node.EndPoint().Column),
				Type:      "ast:" + node.Type(lang),
				Name:      "",
				Text:      "",
				StartLine: startLine,
				StartCol:  int(node.StartPoint().Column),
				EndLine:   endLine,
				EndCol:    int(node.EndPoint().Column),
			})
			for i := range node.NamedChildCount() {
				walk(node.NamedChild(i))
			}

			return
		}

		for i := range node.ChildCount() {
			walk(node.Child(i))
		}
	}
	walk(root)

	return nodes
}

// computeLineStarts returns a slice s where s[i] is the byte offset of the
// (i+1)-th 1-indexed line, plus a trailing entry equal to len(source). It is
// safe to call on empty input (returns []int{0}).
func computeLineStarts(source []byte) []int {
	starts := make([]int, 1, len(source)/40+2)
	starts[0] = 0

	for i, b := range source {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}

	if starts[len(starts)-1] != len(source) {
		starts = append(starts, len(source))
	}

	return starts
}

// buildSitterRanges normalizes raw LineRanges into a slice of sitter.Range
// suitable for Parser.SetIncludedRanges (sorted, non-overlapping, non-empty).
// Out-of-bounds inputs are clamped; empty intervals are dropped.
func buildSitterRanges(ranges []LineRange, lineStarts []int, numLines int) []sitter.Range {
	if numLines <= 0 {
		return nil
	}

	clamped := make([]LineRange, 0, len(ranges))
	for _, r := range ranges {
		a, b := r.Start, r.End
		if a < 1 {
			a = 1
		}

		if b > numLines {
			b = numLines
		}

		if a > b {
			continue
		}

		clamped = append(clamped, LineRange{Start: a, End: b})
	}

	if len(clamped) == 0 {
		return nil
	}

	sort.Slice(clamped, func(i, j int) bool {
		return clamped[i].Start < clamped[j].Start
	})

	merged := clamped[:1]
	for _, r := range clamped[1:] {
		last := &merged[len(merged)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}

			continue
		}

		merged = append(merged, r)
	}

	out := make([]sitter.Range, 0, len(merged))
	for _, r := range merged {
		startByte := lineStarts[r.Start-1]
		endByte := lineStarts[r.End]
		out = append(out, sitter.Range{
			StartPoint: pointAt(startByte, lineStarts),
			EndPoint:   pointAt(endByte, lineStarts),
			StartByte:  uint32(startByte),
			EndByte:    uint32(endByte),
		})
	}

	return out
}

// pointAt converts a byte offset into a tree-sitter Point using the precomputed
// line offset table.
func pointAt(offset int, lineStarts []int) sitter.Point {
	r := max(sort.SearchInts(lineStarts, offset+1)-1, 0)

	return sitter.Point{
		Row:    uint32(r),
		Column: uint32(offset - lineStarts[r]),
	}
}

func extractByTypes(
	path string,
	source []byte,
	typeSelector func(spec languageSpec) map[string]struct{},
	requireName bool,
	namedOnlyWalk bool,
) ([]Node, error) {
	spec, ok := languageByExtension[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil
	}

	nodeTypes := typeSelector(spec)

	root, err := parseFull(source, spec.language)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter failed to parse %s: %w", path, err)
	}

	nodes := make([]Node, 0, 32)
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}

		if _, ok := nodeTypes[node.Type(spec.language)]; ok {
			nameNode := node.ChildByFieldName("name", spec.language)
			if nameNode == nil {
				for i := range node.NamedChildCount() {
					child := node.NamedChild(i)
					switch child.Type(spec.language) {
					case "identifier", "field_identifier", "property_identifier",
						"private_property_identifier":
						nameNode = child
					}

					if nameNode != nil {
						break
					}
				}
			}

			name := ""
			if nameNode != nil {
				name = strings.TrimSpace(nameNode.Text(source))
			}

			if name == "" {
				name = strings.TrimSpace(node.Text(source))
			}

			if requireName && name == "" {
				goto recurse
			}

			startLine := int(node.StartPoint().Row) + 1

			endLine := max(int(node.EndPoint().Row)+1, startLine)

			nodes = append(nodes, Node{
				ID:        fmt.Sprintf("%d:%d:%s:%s", startLine, endLine, node.Type(spec.language), name),
				Type:      "ast:" + node.Type(spec.language),
				Name:      name,
				Text:      strings.TrimSpace(node.Text(source)),
				StartLine: startLine,
				StartCol:  int(node.StartPoint().Column),
				EndLine:   endLine,
				EndCol:    int(node.EndPoint().Column),
			})
		}

	recurse:
		if namedOnlyWalk {
			for i := range node.NamedChildCount() {
				walk(node.NamedChild(i))
			}

			return
		}

		for i := range node.ChildCount() {
			walk(node.Child(i))
		}
	}
	walk(root)

	return nodes, nil
}
