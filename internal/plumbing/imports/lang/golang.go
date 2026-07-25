package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const goImportsQuery = `
(import_spec (interpreted_string_literal) @name)
`

type goExtractor struct {
	language *sitter.Language
	query    *sitter.Query
}

func newGoExtractor() goExtractor {
	language := grammars.GoLanguage()

	return goExtractor{
		language: language,
		query:    mustQuery(goImportsQuery, language),
	}
}

func (goExtractor) Aliases() []string { return []string{"Go"} }

func (extractor goExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(extractor.query, root, extractor.language, content, func(captures []sitter.QueryCapture) {
		for _, capture := range captures {
			// `interpreted_string_literal` includes the surrounding quotes;
			// strip them to match the smacker-era behavior.
			if capture.Node.EndByte()-capture.Node.StartByte() < 2 {
				continue
			}

			out = append(out, string(content[capture.Node.StartByte()+1:capture.Node.EndByte()-1]))
		}
	})

	return out, nil
}

// mustQuery panics on compile failure because invalid grammar queries are a
// programmer error.
func mustQuery(src string, lang *sitter.Language) *sitter.Query {
	q, err := sitter.NewQuery(src, lang)
	if err != nil {
		panic(err)
	}

	return q
}
