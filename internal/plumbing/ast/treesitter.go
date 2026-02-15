package ast

import (
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	golang "github.com/smacker/go-tree-sitter/golang"
	javascript "github.com/smacker/go-tree-sitter/javascript"
	python "github.com/smacker/go-tree-sitter/python"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"
)

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
		language: golang.GetLanguage(),
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
		language: python.GetLanguage(),
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
		language: javascript.GetLanguage(),
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
		language: javascript.GetLanguage(),
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
		language: javascript.GetLanguage(),
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
		language: javascript.GetLanguage(),
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
		language: typescript.GetLanguage(),
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
		language: typescript.GetLanguage(),
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
	root := sitter.Parse(source, spec.language)
	if root == nil || root.IsNull() {
		return nil, fmt.Errorf("tree-sitter failed to parse %s", path)
	}
	nodes := make([]Node, 0, 32)
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil || node.IsNull() {
			return
		}
		if _, ok := nodeTypes[node.Type()]; ok {
			nameNode := node.ChildByFieldName("name")
			if nameNode == nil || nameNode.IsNull() {
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(i)
					switch child.Type() {
					case "identifier", "field_identifier", "property_identifier",
						"private_property_identifier":
						nameNode = child
					}
					if nameNode != nil && !nameNode.IsNull() {
						break
					}
				}
			}
			name := ""
			if nameNode != nil && !nameNode.IsNull() {
				name = strings.TrimSpace(nameNode.Content(source))
			}
			if name == "" {
				name = strings.TrimSpace(node.Content(source))
			}
			if requireName && name == "" {
				goto recurse
			}
			startLine := int(node.StartPoint().Row) + 1
			endLine := int(node.EndPoint().Row) + 1
			if endLine < startLine {
				endLine = startLine
			}
			nodes = append(nodes, Node{
				ID:        fmt.Sprintf("%d:%d:%s:%s", startLine, endLine, node.Type(), name),
				Type:      "ast:" + node.Type(),
				Name:      name,
				Text:      strings.TrimSpace(node.Content(source)),
				StartLine: startLine,
				StartCol:  int(node.StartPoint().Column),
				EndLine:   endLine,
				EndCol:    int(node.EndPoint().Column),
			})
		}
	recurse:
		if namedOnlyWalk {
			for i := 0; i < int(node.NamedChildCount()); i++ {
				walk(node.NamedChild(i))
			}
			return
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)
	return nodes, nil
}
