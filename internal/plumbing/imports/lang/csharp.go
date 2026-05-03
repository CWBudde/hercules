package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// The C# grammar shipped with gotreesitter uses `identifier` for the simple
// case (`using System;`) and `qualified_name` for the dotted case
// (`using System.Collections.Generic;`). Older smacker/tree-sitter-csharp
// versions called the simple form `identifier_name`, but gotreesitter follows
// upstream grammar naming.
const csharpImportsQuery = `
(using_directive (identifier) @name)
(using_directive (qualified_name) @name)
`

var (
	csharpLang  = grammars.CSharpLanguage()
	csharpQuery = mustQuery(csharpImportsQuery, csharpLang)

	// Identifier-shaped tokens whose text contributes directly to the
	// reconstructed dotted import path.
	csharpIncludeTypes = map[string]struct{}{
		"identifier": {},
		".":          {},
	}
	// Subtrees we drop wholesale (generic argument syntax leaks into
	// using_static directives in newer C# versions).
	csharpExcludeTypes = map[string]struct{}{
		"type_argument_list": {},
		"<":                  {},
		">":                  {},
	}
)

func init() {
	RegisterLanguage(csharpExtractor{})
}

type csharpExtractor struct{}

func (csharpExtractor) Aliases() []string { return []string{"C#"} }

func (csharpExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(csharpLang, content)
	if root == nil {
		return nil, nil
	}
	var out []string
	runQuery(csharpQuery, root, csharpLang, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			out = append(out, csharpJoinName(c.Node, content))
		}
	})
	return out, nil
}

// csharpJoinName flattens a qualified_name / identifier_name subtree into
// its dotted source representation, skipping generic-argument noise.
func csharpJoinName(node *sitter.Node, content []byte) string {
	var b []byte
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		typ := n.Type(csharpLang)
		if _, ok := csharpIncludeTypes[typ]; ok {
			b = append(b, content[n.StartByte():n.EndByte()]...)
			return
		}
		if _, ok := csharpExcludeTypes[typ]; ok {
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return string(b)
}
