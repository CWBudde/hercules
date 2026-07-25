package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const jsImportsQuery = `
(import_statement source: (string) @path)
`

type jsExtractor struct {
	language *sitter.Language
	query    *sitter.Query
}

func newJSExtractor() jsExtractor {
	language := grammars.JavascriptLanguage()

	return jsExtractor{
		language: language,
		query:    mustQuery(jsImportsQuery, language),
	}
}

func (jsExtractor) Aliases() []string { return []string{"JavaScript"} }

func (extractor jsExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(extractor.query, root, extractor.language, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			if c.Node.EndByte()-c.Node.StartByte() < 2 {
				continue
			}

			out = append(out, string(content[c.Node.StartByte()+1:c.Node.EndByte()-1]))
		}
	})

	return out, nil
}
