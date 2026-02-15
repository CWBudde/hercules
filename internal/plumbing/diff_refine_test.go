package plumbing

import (
	"testing"

	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/stretchr/testify/assert"
)

func TestRefineDiffByNodeDensityShiftRight(t *testing.T) {
	original := FileDiffData{
		OldLinesOfCode: 3,
		NewLinesOfCode: 4,
		Diffs: []diffmatchpatch.Diff{
			{Type: diffmatchpatch.DiffInsert, Text: "ax"},
			{Type: diffmatchpatch.DiffEqual, Text: "ab"},
		},
	}
	line2node := [][]ast_items.Node{
		{{ID: "A"}, {ID: "B"}},
		{{ID: "A"}},
		{{ID: "A"}},
		{},
	}
	refined := refineDiffByNodeDensity(original, line2node)
	assert.Equal(t, original.OldLinesOfCode, refined.OldLinesOfCode)
	assert.Equal(t, original.NewLinesOfCode, refined.NewLinesOfCode)
	assert.Len(t, refined.Diffs, 3)
	assert.Equal(t, diffmatchpatch.DiffEqual, refined.Diffs[0].Type)
	assert.Equal(t, "a", refined.Diffs[0].Text)
	assert.Equal(t, diffmatchpatch.DiffInsert, refined.Diffs[1].Type)
	assert.Equal(t, "xa", refined.Diffs[1].Text)
	assert.Equal(t, diffmatchpatch.DiffEqual, refined.Diffs[2].Type)
	assert.Equal(t, "b", refined.Diffs[2].Text)
}

func TestRefineDiffByNodeDensityKeepsOriginal(t *testing.T) {
	original := FileDiffData{
		OldLinesOfCode: 3,
		NewLinesOfCode: 4,
		Diffs: []diffmatchpatch.Diff{
			{Type: diffmatchpatch.DiffInsert, Text: "ax"},
			{Type: diffmatchpatch.DiffEqual, Text: "ab"},
		},
	}
	line2node := [][]ast_items.Node{
		{{ID: "A"}},
		{{ID: "A"}},
		{{ID: "A"}, {ID: "B"}},
		{},
	}
	refined := refineDiffByNodeDensity(original, line2node)
	assert.Equal(t, original, refined)
}
