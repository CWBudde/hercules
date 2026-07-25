package lang

import (
	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const jsImportsQuery = `
(import_statement source: (string) @path)
`

var (
	jsLang  = grammars.JavascriptLanguage()
	jsQuery = mustQuery(jsImportsQuery, jsLang)
)

var _ = RegisterLanguage(jsExtractor{})

type jsExtractor struct{}

func (jsExtractor) Aliases() []string { return []string{"JavaScript"} }

func (jsExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(jsLang, content)
	if root == nil {
		return nil, nil
	}
	var out []string

	runQuery(jsQuery, root, jsLang, content, func(captures []sitter.QueryCapture) {
		for _, c := range captures {
			if c.Node.EndByte()-c.Node.StartByte() < 2 {
				continue
			}

			out = append(out, string(content[c.Node.StartByte()+1:c.Node.EndByte()-1]))
		}
	})

	return out, nil
}
