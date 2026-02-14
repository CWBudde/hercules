package leaves

import (
	"bytes"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions
func makeRefactoringTestDeps(tick int, changes object.Changes) map[string]interface{} {
	return map[string]interface{}{
		core.DependencyCommit:       &object.Commit{},
		items.DependencyTick:        tick,
		items.DependencyTreeChanges: changes,
	}
}

func makeRename(fromName, toName string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: fromName},
		To:   object.ChangeEntry{Name: toName},
	}
}

func makeAddition(name string) *object.Change {
	return &object.Change{
		To: object.ChangeEntry{Name: name},
	}
}

func makeDeletion(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: name},
	}
}

func makeModification(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: name},
		To:   object.ChangeEntry{Name: name},
	}
}

// Task 6: Basic Tests
func TestRefactoringProxy_RenameDetection(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	changes := object.Changes{
		makeRename("old/file.go", "new/file.go"),
		makeAddition("added.go"),
		makeDeletion("deleted.go"),
		makeModification("modified.go"),
	}

	deps := makeRefactoringTestDeps(0, changes)
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize().(RefactoringProxyResult)

	assert.Equal(t, 1, len(result.Ticks))
	assert.Equal(t, 4, result.TotalChanges[0])
	assert.Equal(t, 1, rp.tickMetrics[0].Renames)
	assert.InDelta(t, 0.25, result.RenameRatios[0], 0.01)
	assert.False(t, result.IsRefactoring[0])
}

// Task 7: Classification & Edge Cases
func TestRefactoringProxy_Classification(t *testing.T) {
	testCases := []struct {
		name        string
		threshold   float64
		renames     int
		total       int
		expectRefac bool
	}{
		{"below threshold", 0.5, 2, 10, false},
		{"at threshold", 0.5, 5, 10, false},
		{"above threshold", 0.5, 6, 10, true},
		{"all renames", 0.5, 10, 10, true},
		{"high threshold", 0.9, 8, 10, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rp := &RefactoringProxy{RefactoringThreshold: tc.threshold}
			require.NoError(t, rp.Initialize(nil))

			changes := object.Changes{}
			for i := 0; i < tc.renames; i++ {
				changes = append(changes, makeRename("old"+string(rune(i)), "new"+string(rune(i))))
			}
			for i := 0; i < tc.total-tc.renames; i++ {
				changes = append(changes, makeAddition("added"+string(rune(i))))
			}

			deps := makeRefactoringTestDeps(0, changes)
			_, err := rp.Consume(deps)
			require.NoError(t, err)

			result := rp.Finalize().(RefactoringProxyResult)

			assert.Equal(t, tc.expectRefac, result.IsRefactoring[0])
		})
	}
}

func TestRefactoringProxy_EmptyCommits(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	deps := makeRefactoringTestDeps(0, object.Changes{})
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize().(RefactoringProxyResult)

	assert.Equal(t, 0, len(result.Ticks))
}

// Task 8: Multiple Ticks & Serialization
func TestRefactoringProxy_MultipleTicks(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	// Tick 0: Low rename ratio (0.2)
	deps0 := makeRefactoringTestDeps(0, object.Changes{
		makeRename("a", "b"),
		makeAddition("c"),
		makeAddition("d"),
		makeAddition("e"),
		makeAddition("f"),
	})
	_, err := rp.Consume(deps0)
	require.NoError(t, err)

	// Tick 1: High rename ratio (0.8)
	deps1 := makeRefactoringTestDeps(1, object.Changes{
		makeRename("g", "h"),
		makeRename("i", "j"),
		makeRename("k", "l"),
		makeRename("m", "n"),
		makeAddition("o"),
	})
	_, err = rp.Consume(deps1)
	require.NoError(t, err)

	// Tick 2: Medium rename ratio (0.5)
	deps2 := makeRefactoringTestDeps(2, object.Changes{
		makeRename("p", "q"),
		makeAddition("r"),
	})
	_, err = rp.Consume(deps2)
	require.NoError(t, err)

	result := rp.Finalize().(RefactoringProxyResult)

	assert.Equal(t, 3, len(result.Ticks))
	assert.Equal(t, []int{0, 1, 2}, result.Ticks)
	assert.InDelta(t, 0.2, result.RenameRatios[0], 0.01)
	assert.InDelta(t, 0.8, result.RenameRatios[1], 0.01)
	assert.InDelta(t, 0.5, result.RenameRatios[2], 0.01)
	assert.False(t, result.IsRefactoring[0])
	assert.True(t, result.IsRefactoring[1])
	assert.False(t, result.IsRefactoring[2])
}

func TestRefactoringProxy_Serialization(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	deps := makeRefactoringTestDeps(0, object.Changes{
		makeRename("old.go", "new.go"),
		makeRename("foo.go", "bar.go"),
		makeAddition("baz.go"),
	})
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize()

	// Test binary serialization
	buf := new(bytes.Buffer)
	err = rp.Serialize(result, true, buf)
	require.NoError(t, err)
	assert.NotEmpty(t, buf.Bytes())

	// Test text serialization
	textBuf := new(bytes.Buffer)
	err = rp.Serialize(result, false, textBuf)
	require.NoError(t, err)
	assert.Contains(t, textBuf.String(), "refactoring_proxy:")
	assert.Contains(t, textBuf.String(), "threshold:")
}
