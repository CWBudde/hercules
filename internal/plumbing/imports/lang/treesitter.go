package lang

import (
	"slices"

	sitter "github.com/odvcencio/gotreesitter"
)

// parseFull parses content with the given language and returns the root
// node. Returns nil on failure (callers treat this as "no imports").
func parseFull(lang *sitter.Language, content []byte) *sitter.Node {
	parser := sitter.NewParser(lang)

	tree, err := parser.Parse(content)
	if err != nil || tree == nil {
		return nil
	}

	return tree.RootNode()
}

// runQuery executes a precompiled query against root and yields each match's
// captures via the visit callback. Errors during cursor iteration are
// suppressed; a malformed query is a programmer error caught at init time.
func runQuery(
	q *sitter.Query,
	root *sitter.Node,
	lang *sitter.Language,
	content []byte,
	visit func(captures []sitter.QueryCapture),
) {
	if q == nil || root == nil {
		return
	}

	cursor := q.Exec(root, lang, content)
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}

		visit(m.Captures)
	}
}

// nodeText returns the byte slice of content covered by node.
func nodeText(node *sitter.Node, content []byte) []byte {
	if node == nil {
		return nil
	}

	return content[node.StartByte():node.EndByte()]
}

// nodeTextString is the string-typed convenience over nodeText.
func nodeTextString(node *sitter.Node, content []byte) string {
	return string(nodeText(node, content))
}

// eachNode walks every node (named and unnamed) in pre-order, calling fnc.
// If fnc returns false, descent into the current node is skipped.
func eachNode(root *sitter.Node, lang *sitter.Language, fnc func(n *sitter.Node) bool) {
	if root == nil {
		return
	}

	if !fnc(root) {
		return
	}

	for i := range root.ChildCount() {
		eachNode(root.Child(i), lang, fnc)
	}
}

// eachNodeOfTypes walks the tree and calls fnc only for nodes whose Type
// matches one of the supplied names. Returning false from fnc skips descent
// into that node's subtree.
func eachNodeOfTypes(root *sitter.Node, lang *sitter.Language, fnc func(n *sitter.Node) bool, types ...string) {
	eachNode(root, lang, func(n *sitter.Node) bool {
		typ := n.Type(lang)
		if typ == "" {
			return true
		}

		if slices.Contains(types, typ) {
			return fnc(n)
		}

		return true
	})
}
