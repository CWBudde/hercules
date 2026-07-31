package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

// Helper functions.
func makeRefactoringTestDeps(tick int, changes object.Changes) map[string]any {
	return map[string]any{
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

// Task 6: Basic Tests.
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

	assert.Len(t, result.Ticks, 1)
	assert.Equal(t, 4, result.TotalChanges[0])
	assert.Equal(t, 1, rp.tickMetrics[0].Renames)
	assert.InDelta(t, 0.25, result.RenameRatios[0], 0.01)
	assert.False(t, result.IsRefactoring[0])
}

// Task 7: Classification & Edge Cases.
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
			for i := range tc.renames {
				changes = append(changes, makeRename("old"+string(rune(i)), "new"+string(rune(i))))
			}
			for i := range tc.total - tc.renames {
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

	assert.Empty(t, result.Ticks)
}

// Task 8: Multiple Ticks & Serialization.
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

	assert.Len(t, result.Ticks, 3)
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

// Task B4: result merging.

const refactoringMergeBeginTime = 1556224895

func makeRefactoringResult(
	ticks []int, ratios []float64, totals []int, threshold float64, tickSize time.Duration,
) RefactoringProxyResult {
	result := RefactoringProxyResult{
		Ticks:         ticks,
		RenameRatios:  ratios,
		IsRefactoring: make([]bool, len(ticks)),
		TotalChanges:  totals,
		Threshold:     threshold,
		tickSize:      tickSize,
	}

	for i, ratio := range ratios {
		result.IsRefactoring[i] = ratio > threshold
	}

	return result
}

func TestRefactoringProxyMergeResultsRejectsWrongType(t *testing.T) {
	rp := &RefactoringProxy{}
	valid := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	err, ok := rp.MergeResults("not a result", valid, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errUnexpectedResultType)

	err, ok = rp.MergeResults(valid, 42, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errUnexpectedResultType)
}

func TestRefactoringProxyMergeResultsRejectsMismatchingTickSizes(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 22*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	err, ok := rp.MergeResults(r1, r2, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errRefactoringProxyMismatchingTickSizes)
}

func TestRefactoringProxyMergeResultsRejectsMismatchingThresholds(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.4, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	err, ok := rp.MergeResults(r1, r2, common, common).(error)
	require.True(t, ok)
	require.ErrorIs(t, err, errRefactoringProxyMismatchingThresholds)
}

// TestRefactoringProxyMergeResultsWeightsByTotalChanges pins the count-weighted mean: a naive
// unweighted average would yield 0.5 here and flip IsRefactoring to true.
func TestRefactoringProxyMergeResultsWeightsByTotalChanges(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0}, []float64{1.0}, []int{1}, 0.4, 24*time.Hour)
	r2 := makeRefactoringResult([]int{0}, []float64{0.0}, []int{99}, 0.4, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	merged, ok := rp.MergeResults(r1, r2, common, common).(RefactoringProxyResult)
	require.True(t, ok)

	assert.Equal(t, []int{0}, merged.Ticks)
	assert.Equal(t, []int{100}, merged.TotalChanges)
	assert.InDelta(t, 0.01, merged.RenameRatios[0], 1e-12)
	assert.False(t, merged.IsRefactoring[0])
	assert.InDelta(t, 0.4, merged.Threshold, 1e-12)
	assert.Equal(t, 24*time.Hour, merged.tickSize)
}

// TestRefactoringProxyMergeResultsRebasesTickAxes proves the two repositories' tick axes are
// shifted onto the earlier start. Without the offset arithmetic this collapses to two ticks.
func TestRefactoringProxyMergeResultsRebasesTickAxes(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0, 1}, []float64{0.1, 0.2}, []int{10, 10}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{0, 1}, []float64{0.3, 0.4}, []int{20, 20}, 0.5, 24*time.Hour)
	c1 := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}
	c2 := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime + 2*24*3600}

	merged, ok := rp.MergeResults(r1, r2, c1, c2).(RefactoringProxyResult)
	require.True(t, ok)
	assert.Equal(t, []int{0, 1, 2, 3}, merged.Ticks)
	assert.Equal(t, []int{10, 10, 20, 20}, merged.TotalChanges)
	assert.Equal(t, []float64{0.1, 0.2, 0.3, 0.4}, merged.RenameRatios)

	// Offsets are relative to the earlier start, not to the argument order.
	swapped, ok := rp.MergeResults(r1, r2, c2, c1).(RefactoringProxyResult)
	require.True(t, ok)
	assert.Equal(t, []int{0, 1, 2, 3}, swapped.Ticks)
	assert.Equal(t, []int{20, 20, 10, 10}, swapped.TotalChanges)
	assert.Equal(t, []float64{0.3, 0.4, 0.1, 0.2}, swapped.RenameRatios)
}

func TestRefactoringProxyMergeResultsKeepsSingleSourceTicksVerbatim(t *testing.T) {
	rp := &RefactoringProxy{}
	// 1/3 is not representable; (r*t)/t may differ by an ulp, so a lone source must be copied.
	oneThird := 1.0 / 3.0
	r1 := makeRefactoringResult([]int{0, 5}, []float64{oneThird, 0.0}, []int{3, 0}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{9}, []float64{0.75}, []int{4}, 0.5, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	merged, ok := rp.MergeResults(r1, r2, common, common).(RefactoringProxyResult)
	require.True(t, ok)
	assert.Equal(t, []int{0, 5, 9}, merged.Ticks)
	assert.Equal(t, []float64{oneThird, 0.0, 0.75}, merged.RenameRatios)
	assert.Equal(t, []int{3, 0, 4}, merged.TotalChanges)
	assert.Equal(t, []bool{false, false, true}, merged.IsRefactoring)
}

func TestRefactoringProxyMergeResultsSortsTicks(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{7, 1, 4}, []float64{0.1, 0.2, 0.3}, []int{1, 2, 3}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{5, 0, 2}, []float64{0.4, 0.5, 0.6}, []int{4, 5, 6}, 0.5, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	merged, ok := rp.MergeResults(r1, r2, common, common).(RefactoringProxyResult)
	require.True(t, ok)
	assert.Equal(t, []int{0, 1, 2, 4, 5, 7}, merged.Ticks)
	assert.Equal(t, []int{5, 2, 6, 3, 4, 1}, merged.TotalChanges)
}

func TestRefactoringProxyMergeResultsWithoutCommons(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0, 1}, []float64{0.5, 0.5}, []int{2, 2}, 0.5, 24*time.Hour)
	r2 := makeRefactoringResult([]int{1, 2}, []float64{0.5, 0.5}, []int{2, 2}, 0.5, 24*time.Hour)

	require.NotPanics(t, func() {
		merged, ok := rp.MergeResults(r1, r2, nil, nil).(RefactoringProxyResult)
		require.True(t, ok)
		assert.Equal(t, []int{0, 1, 2}, merged.Ticks)
		assert.Equal(t, []int{2, 4, 2}, merged.TotalChanges)
	})

	// A zero tick size must not divide by zero either.
	z1 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 0)
	z2 := makeRefactoringResult([]int{0}, []float64{0.5}, []int{2}, 0.5, 0)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	require.NotPanics(t, func() {
		merged, ok := rp.MergeResults(z1, z2, common, common).(RefactoringProxyResult)
		require.True(t, ok)
		assert.Equal(t, []int{0}, merged.Ticks)
		assert.Equal(t, []int{4}, merged.TotalChanges)
	})
}

func TestRefactoringProxyMergeResultsRoundTrip(t *testing.T) {
	rp := &RefactoringProxy{}
	r1 := makeRefactoringResult([]int{0, 1}, []float64{1.0, 0.25}, []int{1, 4}, 0.4, 24*time.Hour)
	r2 := makeRefactoringResult([]int{0, 2}, []float64{0.0, 0.8}, []int{99, 5}, 0.4, 24*time.Hour)
	common := &core.CommonAnalysisResult{BeginTime: refactoringMergeBeginTime}

	merged, ok := rp.MergeResults(r1, r2, common, common).(RefactoringProxyResult)
	require.True(t, ok)

	buffer := &bytes.Buffer{}
	require.NoError(t, rp.Serialize(merged, true, buffer))

	rawRestored, err := rp.Deserialize(buffer.Bytes())
	require.NoError(t, err)

	restored, ok := rawRestored.(RefactoringProxyResult)
	require.True(t, ok)

	assert.Equal(t, merged.Ticks, restored.Ticks)
	assert.Equal(t, merged.TotalChanges, restored.TotalChanges)
	assert.Equal(t, merged.IsRefactoring, restored.IsRefactoring)
	assert.Equal(t, merged.tickSize, restored.tickSize)
	assert.InDelta(t, merged.Threshold, restored.Threshold, 1e-6)
	require.Len(t, restored.RenameRatios, len(merged.RenameRatios))

	for i, ratio := range merged.RenameRatios {
		assert.InDelta(t, ratio, restored.RenameRatios[i], 1e-6)
	}
}
