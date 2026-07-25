package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var tsLang = grammars.TypescriptLanguage()

const tsStringNodeType = "string"

var _ = RegisterLanguage(tsExtractor{})

type tsExtractor struct{}

func (tsExtractor) Aliases() []string { return []string{"TypeScript"} }

// Imports walks every import_statement / export_statement in the tree and
// pulls module specifiers out. The TS grammar mixes the same surface for
// re-exports (`export { x } from "y"`) and CommonJS-style
// `import x = require("y")`, so we handle both shapes here rather than via
// a single S-expression query.
func (tsExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(tsLang, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	eachNodeOfTypes(root, tsLang, func(node *sitter.Node) bool {
		out = append(out, tsModuleSpecifiers(node, content)...)

		return false
	}, "import_statement", "export_statement")

	return out, nil
}

func tsModuleSpecifiers(statement *sitter.Node, content []byte) []string {
	if statement.Type(tsLang) == "export_statement" {
		return tsStringChildren(statement, content)
	}

	var specifiers []string

	for i := range statement.ChildCount() {
		child := statement.Child(i)
		switch child.Type(tsLang) {
		case tsStringNodeType:
			specifiers = append(specifiers, stripStringQuotes(child, content))
		case "import_require_clause":
			specifiers = append(specifiers, tsStringChildren(child, content)...)
		}
	}

	return specifiers
}

func tsStringChildren(node *sitter.Node, content []byte) []string {
	var strings []string

	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Type(tsLang) == tsStringNodeType {
			strings = append(strings, stripStringQuotes(child, content))
		}
	}

	return strings
}

// stripStringQuotes returns the contents of a `string` AST node minus the
// surrounding quote characters. Tree-sitter includes both quotes inside
// the node's byte range.
func stripStringQuotes(node *sitter.Node, content []byte) string {
	if node.EndByte()-node.StartByte() < 2 {
		return ""
	}

	return string(content[node.StartByte()+1 : node.EndByte()-1])
}
