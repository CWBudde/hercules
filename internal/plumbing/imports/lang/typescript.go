package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const tsStringNodeType = "string"

type tsExtractor struct {
	language *sitter.Language
}

func newTSExtractor() tsExtractor {
	return tsExtractor{language: grammars.TypescriptLanguage()}
}

func (tsExtractor) Aliases() []string { return []string{"TypeScript"} }

// Imports walks every import_statement / export_statement in the tree and
// pulls module specifiers out. The TS grammar mixes the same surface for
// re-exports (`export { x } from "y"`) and CommonJS-style
// `import x = require("y")`, so we handle both shapes here rather than via
// a single S-expression query.
func (extractor tsExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	eachNodeOfTypes(root, extractor.language, func(node *sitter.Node) bool {
		out = append(out, tsModuleSpecifiers(node, extractor.language, content)...)

		return false
	}, "import_statement", "export_statement")

	return out, nil
}

func tsModuleSpecifiers(
	statement *sitter.Node,
	language *sitter.Language,
	content []byte,
) []string {
	if statement.Type(language) == "export_statement" {
		return tsStringChildren(statement, language, content)
	}

	var specifiers []string

	for i := range statement.ChildCount() {
		child := statement.Child(i)
		switch child.Type(language) {
		case tsStringNodeType:
			specifiers = append(specifiers, stripStringQuotes(child, content))
		case "import_require_clause":
			specifiers = append(specifiers, tsStringChildren(child, language, content)...)
		}
	}

	return specifiers
}

func tsStringChildren(
	node *sitter.Node,
	language *sitter.Language,
	content []byte,
) []string {
	var strings []string

	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Type(language) == tsStringNodeType {
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
