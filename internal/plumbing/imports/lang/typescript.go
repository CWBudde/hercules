package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var tsLang = grammars.TypescriptLanguage()

func init() {
	RegisterLanguage(tsExtractor{})
}

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
	eachNodeOfTypes(root, tsLang, func(n *sitter.Node) bool {
		if n.Type(tsLang) == "export_statement" {
			for i := 0; i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child.Type(tsLang) != "string" {
					continue
				}
				out = append(out, stripStringQuotes(child, content))
			}
			return false
		}
		// import_statement
		for i := 0; i < n.ChildCount(); i++ {
			child := n.Child(i)
			switch child.Type(tsLang) {
			case "string":
				out = append(out, stripStringQuotes(child, content))
			case "import_require_clause":
				for j := 0; j < child.ChildCount(); j++ {
					inner := child.Child(j)
					if inner.Type(tsLang) != "string" {
						continue
					}
					out = append(out, stripStringQuotes(inner, content))
				}
			}
		}
		return false
	}, "import_statement", "export_statement")
	return out, nil
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
