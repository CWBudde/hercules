package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const goImportsQuery = `
(import_spec (interpreted_string_literal) @name)
`

var (
	goLang  = grammars.GoLanguage()
	goQuery = mustQuery(goImportsQuery, goLang)
)

type goExtractor struct{}

func (goExtractor) Aliases() []string { return []string{"Go"} }

func (goExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(goLang, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(goQuery, root, goLang, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			// `interpreted_string_literal` includes the surrounding quotes;
			// strip them to match the smacker-era behavior.
			if c.Node.EndByte()-c.Node.StartByte() < 2 {
				continue
			}

			out = append(out, string(content[c.Node.StartByte()+1:c.Node.EndByte()-1]))
		}
	})

	return out, nil
}

// mustQuery panics on compile failure — invalid grammar queries are a
// programmer error caught at init time, not a runtime condition.
func mustQuery(src string, lang *sitter.Language) *sitter.Query {
	q, err := sitter.NewQuery(src, lang)
	if err != nil {
		panic(err)
	}

	return q
}
