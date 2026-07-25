package lang

import (
	"strings"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var _ = RegisterLanguage(newPyExtractor())

type pyExtractor struct {
	language      *sitter.Language
	staticQueries []*sitter.Query
	dynamicQuery  *sitter.Query
}

func newPyExtractor() pyExtractor {
	language := grammars.PythonLanguage()
	querySources := []string{
		`((import_statement name: (dotted_name (identifier)) @name))`,
		`((import_statement name: (aliased_import name: (dotted_name (identifier)) @name)))`,
		`((import_from_statement module_name: (dotted_name (identifier)) @name))`,
		`((import_from_statement module_name: (relative_import (import_prefix)) @name))`,
	}

	staticQueries := make([]*sitter.Query, len(querySources))
	for index, source := range querySources {
		staticQueries[index] = mustQuery(source, language)
	}

	return pyExtractor{
		language:      language,
		staticQueries: staticQueries,
		dynamicQuery: mustQuery(
			`((call function: (identifier) @fn arguments: (argument_list (string) @arg)))`,
			language,
		),
	}
}

func (pyExtractor) Aliases() []string { return []string{"Python"} }

func (extractor pyExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(extractor.language, content)
	if root == nil {
		return nil, nil
	}

	out := make([]string, 0)

	for _, query := range extractor.staticQueries {
		runQuery(query, root, extractor.language, content, func(captures []sitter.QueryCapture) {
			if len(captures) != 1 {
				return
			}

			out = append(out, nodeTextString(captures[0].Node, content))
		})
	}

	runQuery(extractor.dynamicQuery, root, extractor.language, content, func(captures []sitter.QueryCapture) {
		if len(captures) != 2 {
			return
		}

		functionName := nodeTextString(captures[0].Node, content)
		if functionName != "__import__" && functionName != "import_file" {
			return
		}

		arg := nodeTextString(captures[1].Node, content)
		out = append(out, strings.Trim(arg, `'"`))
	})

	return out, nil
}
