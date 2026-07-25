package lang

import (
	"strings"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const javaImportsQuery = `
((import_declaration) @imp)
`

type javaExtractor struct {
	language *sitter.Language
	query    *sitter.Query
}

func newJavaExtractor() javaExtractor {
	language := grammars.JavaLanguage()

	return javaExtractor{
		language: language,
		query:    mustQuery(javaImportsQuery, language),
	}
}

func (javaExtractor) Aliases() []string { return []string{"Java"} }

func (extractor javaExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(extractor.query, root, extractor.language, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			n := c.Node

			parts := make([]string, 0, n.ChildCount())
			for i := range n.ChildCount() {
				child := n.Child(i)
				switch child.Type(extractor.language) {
				case "identifier":
					parts = append(parts, nodeTextString(child, content))
				case "asterisk":
					// Wildcard `import a.b.*;` becomes the dotted prefix
					// "a.b." (empty trailing component). Matches the
					// previous src-d/imports behavior.
					parts = append(parts, "")
				}
			}

			out = append(out, strings.Join(parts, "."))
		}
	})

	return out, nil
}
