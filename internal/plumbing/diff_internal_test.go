package plumbing

import (
	"testing"

	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/stretchr/testify/assert"
)

func TestRefineDiffByNodeDensityNormalizesAdjacentInserts(t *testing.T) {
	original := FileDiffData{
		OldLinesOfCode: 4,
		NewLinesOfCode: 5,
		Diffs: []diffmatchpatch.Diff{
			{Type: diffmatchpatch.DiffEqual, Text: "A"},
			{Type: diffmatchpatch.DiffInsert, Text: "BC"},
			{Type: diffmatchpatch.DiffEqual, Text: "B"},
			{Type: diffmatchpatch.DiffInsert, Text: "X"},
			{Type: diffmatchpatch.DiffEqual, Text: "Y"},
		},
	}
	refined := refineDiffByNodeDensity(original, [][]ast_items.Node{
		{},
		{{ID: "n1"}},
		{{ID: "n2"}},
		{},
		{},
	})
	refined.Diffs = normalizeDiffs(refined.Diffs)
	if assert.Len(t, refined.Diffs, 3) {
		assert.Equal(t, diffmatchpatch.DiffEqual, refined.Diffs[0].Type)
		assert.Equal(t, "AB", refined.Diffs[0].Text)
		assert.Equal(t, diffmatchpatch.DiffInsert, refined.Diffs[1].Type)
		assert.Equal(t, "CBX", refined.Diffs[1].Text)
		assert.Equal(t, diffmatchpatch.DiffEqual, refined.Diffs[2].Type)
		assert.Equal(t, "Y", refined.Diffs[2].Text)
	}
}
