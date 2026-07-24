package lang

import (
	"strings"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var (
	pyLang = grammars.PythonLanguage()

	// Static imports: each query targets one of the AST shapes Python uses
	// for `import X`, `import X as Y`, `from X import ...`, and relative
	// imports (`from . import ...`).
	pyStaticQuerySources = []string{
		`((import_statement name: (dotted_name (identifier)) @name))`,
		`((import_statement name: (aliased_import name: (dotted_name (identifier)) @name)))`,
		`((import_from_statement module_name: (dotted_name (identifier)) @name))`,
		`((import_from_statement module_name: (relative_import (import_prefix)) @name))`,
	}
	pyStaticQueries []*sitter.Query

	// Dynamic imports: detect calls to __import__ / import_file with a
	// string literal argument.
	pyDynamicQuery = `((call function: (identifier) @fn arguments: (argument_list (string) @arg)))`
	pyDynamicQ     *sitter.Query
	pyDynamicFns   = map[string]struct{}{
		"__import__":  {},
		"import_file": {},
	}
)

func init() {
	RegisterLanguage(pyExtractor{})

	pyStaticQueries = make([]*sitter.Query, len(pyStaticQuerySources))
	for i, src := range pyStaticQuerySources {
		pyStaticQueries[i] = mustQuery(src, pyLang)
	}

	pyDynamicQ = mustQuery(pyDynamicQuery, pyLang)
}

type pyExtractor struct{}

func (pyExtractor) Aliases() []string { return []string{"Python"} }

func (pyExtractor) Imports(content []byte) ([]string, error) {
	root := parseFull(pyLang, content)
	if root == nil {
		return nil, nil
	}

	out := make([]string, 0)

	for _, q := range pyStaticQueries {
		runQuery(q, root, pyLang, content, func(captures []sitter.QueryCapture) {
			if len(captures) != 1 {
				return
			}

			out = append(out, nodeTextString(captures[0].Node, content))
		})
	}

	runQuery(pyDynamicQ, root, pyLang, content, func(captures []sitter.QueryCapture) {
		if len(captures) != 2 {
			return
		}

		fn := nodeTextString(captures[0].Node, content)
		if _, ok := pyDynamicFns[fn]; !ok {
			return
		}

		arg := nodeTextString(captures[1].Node, content)
		out = append(out, strings.Trim(arg, `'"`))
	})

	return out, nil
}
