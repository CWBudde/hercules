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

type csharpExtractor struct {
	language *sitter.Language
	query    *sitter.Query
}

func newCSharpExtractor() csharpExtractor {
	language := grammars.CSharpLanguage()

	return csharpExtractor{
		language: language,
		query:    mustQuery(csharpImportsQuery, language),
	}
}

func (csharpExtractor) Aliases() []string { return []string{"C#"} }

func (extractor csharpExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(extractor.query, root, extractor.language, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			out = append(out, csharpJoinName(c.Node, extractor.language, content))
		}
	})

	return out, nil
}

// csharpJoinName flattens a qualified_name / identifier_name subtree into
// its dotted source representation, skipping generic-argument noise.
func csharpJoinName(
	node *sitter.Node,
	language *sitter.Language,
	content []byte,
) string {
	var joinedName []byte
	var walk func(currentNode *sitter.Node)
	walk = func(currentNode *sitter.Node) {
		nodeType := currentNode.Type(language)
		if nodeType == "identifier" || nodeType == "." {
			joinedName = append(joinedName, content[currentNode.StartByte():currentNode.EndByte()]...)

			return
		}

		// Drop generic argument syntax wholesale; it leaks into using_static
		// directives in newer C# grammar versions.
		if nodeType == "type_argument_list" || nodeType == "<" || nodeType == ">" {
			return
		}

		for i := range currentNode.ChildCount() {
			walk(currentNode.Child(i))
		}
	}
	walk(node)

	return string(joinedName)
}
