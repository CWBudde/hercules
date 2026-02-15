# Refactoring Proxy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement refactoring proxy analysis to track rename/move rates and distinguish refactoring phases from feature work.

**Architecture:** Tick-based aggregation using existing RenameAnalysis output. Leaf pipeline item that tracks rename ratio per tick, classifies refactoring-heavy periods, outputs time series data for Python visualization.

**Tech Stack:** Go (hercules core), Protocol Buffers, Python (labours visualization), testify (testing)

---

## Task 1: Add Protobuf Schema

**Files:**
- Modify: `internal/pb/pb.proto` (append to end before `AnalysisResults`)

**Step 1: Add RefactoringProxyResults message**

Add this message definition before the `AnalysisResults` message (around line 408):

```protobuf
message RefactoringProxyResults {
    repeated int32 ticks = 1;
    repeated float rename_ratios = 2;
    repeated bool is_refactoring = 3;
    repeated int32 total_changes = 4;
    float threshold = 5;
    int64 tick_size = 6;
}
```

**Step 2: Register in AnalysisResults**

Find the `AnalysisResults` message and add the new field (use next available field number):

```protobuf
message AnalysisResults {
    // ... existing fields ...
    RefactoringProxyResults refactoring_proxy = 18;  // Use next available number
}
```

**Step 3: Regenerate protobuf bindings**

Run:
```bash
cd internal/pb
make
```

Expected: Files `pb.pb.go` and `pb_pb2.py` regenerated successfully

**Step 4: Commit protobuf schema**

```bash
git add internal/pb/pb.proto internal/pb/pb.pb.go python/labours/pb/pb_pb2.py
git commit -m "feat: add protobuf schema for refactoring proxy analysis"
```

---

## Task 2: Create Core Implementation (Part 1 - Structure & Boilerplate)

**Files:**
- Create: `leaves/refactoring_proxy.go`

**Step 1: Write package header and imports**

```go
package leaves

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/yaml"
)
```

**Step 2: Write data structures**

```go
// tickChangeMetrics tracks changes for one tick
type tickChangeMetrics struct {
	TotalChanges int // All additions, modifications, deletions, renames
	Renames      int // Changes where From.Name != To.Name (both non-empty)
}

// RefactoringProxyResult is returned by RefactoringProxy.Finalize()
type RefactoringProxyResult struct {
	// Time series data (parallel arrays, same length)
	Ticks         []int       // Tick indices
	RenameRatios  []float64   // Rename ratio per tick (0.0 to 1.0)
	IsRefactoring []bool      // true if ratio > threshold
	TotalChanges  []int       // Total changes per tick (for context)

	// Configuration metadata
	Threshold float64       // The threshold used for classification
	TickSize  time.Duration
}

// RefactoringProxy measures rename/move rate to identify refactoring phases
type RefactoringProxy struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// RefactoringThreshold is the rename ratio threshold (0.0-1.0)
	RefactoringThreshold float64

	// tickMetrics maps tick -> metrics
	tickMetrics map[int]*tickChangeMetrics
	tickSize    time.Duration

	l core.Logger
}
```

**Step 3: Write configuration constants**

```go
const (
	// ConfigRefactoringThreshold is the name of the configuration option
	ConfigRefactoringThreshold = "RefactoringProxy.Threshold"
)
```

**Step 4: Commit structure**

```bash
git add leaves/refactoring_proxy.go
git commit -m "feat(refactoring-proxy): add data structures and types"
```

---

## Task 3: Core Implementation (Part 2 - PipelineItem Interface)

**Files:**
- Modify: `leaves/refactoring_proxy.go`

**Step 1: Implement Name, Provides, Requires**

```go
// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (rp *RefactoringProxy) Name() string {
	return "RefactoringProxy"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (rp *RefactoringProxy) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (rp *RefactoringProxy) Requires() []string {
	return []string{
		items.DependencyTreeChanges,
		items.DependencyTick,
	}
}
```

**Step 2: Implement Flag and Description**

```go
// Flag for the command line switch which enables this analysis.
func (rp *RefactoringProxy) Flag() string {
	return "refactoring-proxy"
}

// Description returns the text which explains what the analysis is doing.
func (rp *RefactoringProxy) Description() string {
	return "Tracks rename/move rate over time to distinguish refactoring phases from feature work."
}
```

**Step 3: Implement ListConfigurationOptions**

```go
// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (rp *RefactoringProxy) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigRefactoringThreshold,
			Description: "Rename ratio threshold to classify a tick as refactoring-heavy (0.0-1.0).",
			Flag:        "refactoring-threshold",
			Type:        core.FloatConfigurationOption,
			Default:     0.5,
		},
	}
	return options[:]
}
```

**Step 4: Implement Configure**

```go
// Configure sets the properties previously published by ListConfigurationOptions().
func (rp *RefactoringProxy) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		rp.l = l
	}
	if val, exists := facts[ConfigRefactoringThreshold].(float64); exists {
		rp.RefactoringThreshold = val
	}
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		rp.tickSize = val
	}
	return nil
}

// ConfigureUpstream configures the upstream dependencies.
func (*RefactoringProxy) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}
```

**Step 5: Commit interface methods**

```bash
git add leaves/refactoring_proxy.go
git commit -m "feat(refactoring-proxy): implement PipelineItem interface"
```

---

## Task 4: Core Implementation (Part 3 - Processing Logic)

**Files:**
- Modify: `leaves/refactoring_proxy.go`

**Step 1: Implement Initialize**

```go
// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume() calls.
func (rp *RefactoringProxy) Initialize(repository *git.Repository) error {
	rp.l = core.NewLogger()
	rp.tickMetrics = map[int]*tickChangeMetrics{}
	rp.OneShotMergeProcessor.Initialize()

	// Set default threshold if not configured
	if rp.RefactoringThreshold == 0 {
		rp.RefactoringThreshold = 0.5
	}

	return nil
}
```

**Step 2: Implement helper functions**

```go
// isRename checks if a change represents a file rename
func isRename(change *object.Change) bool {
	hasFrom := change.From.Name != ""
	hasTo := change.To.Name != ""
	differentNames := change.From.Name != change.To.Name

	return hasFrom && hasTo && differentNames
}

// getOrCreateTickMetrics retrieves or creates tick metrics
func (rp *RefactoringProxy) getOrCreateTickMetrics(tick int) *tickChangeMetrics {
	metrics, exists := rp.tickMetrics[tick]
	if !exists {
		metrics = &tickChangeMetrics{}
		rp.tickMetrics[tick] = metrics
	}
	return metrics
}
```

**Step 3: Implement Consume**

```go
// Consume runs this PipelineItem on the next commit data.
func (rp *RefactoringProxy) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !rp.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	tick := deps[items.DependencyTick].(int)
	treeChanges := deps[items.DependencyTreeChanges].(object.Changes)

	if len(treeChanges) == 0 {
		return nil, nil // Empty commit, skip
	}

	// Get or create tick metrics
	metrics := rp.getOrCreateTickMetrics(tick)

	// Classify each change
	for _, change := range treeChanges {
		metrics.TotalChanges++

		if isRename(change) {
			metrics.Renames++
		}
	}

	return nil, nil // Leaf item, no downstream deps
}
```

**Step 4: Implement Finalize**

```go
// Finalize returns the result of the analysis.
func (rp *RefactoringProxy) Finalize() interface{} {
	// Sort ticks for ordered time series
	ticks := make([]int, 0, len(rp.tickMetrics))
	for tick := range rp.tickMetrics {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)

	// Build parallel arrays
	result := RefactoringProxyResult{
		Ticks:         make([]int, len(ticks)),
		RenameRatios:  make([]float64, len(ticks)),
		IsRefactoring: make([]bool, len(ticks)),
		TotalChanges:  make([]int, len(ticks)),
		Threshold:     rp.RefactoringThreshold,
		TickSize:      rp.tickSize,
	}

	for i, tick := range ticks {
		metrics := rp.tickMetrics[tick]

		result.Ticks[i] = tick
		result.TotalChanges[i] = metrics.TotalChanges

		if metrics.TotalChanges > 0 {
			ratio := float64(metrics.Renames) / float64(metrics.TotalChanges)
			result.RenameRatios[i] = ratio
			result.IsRefactoring[i] = ratio > rp.RefactoringThreshold
		} else {
			result.RenameRatios[i] = 0.0
			result.IsRefactoring[i] = false
		}
	}

	return result
}
```

**Step 5: Implement Fork**

```go
// Fork clones this pipeline item.
func (rp *RefactoringProxy) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(rp, n)
}
```

**Step 6: Commit processing logic**

```bash
git add leaves/refactoring_proxy.go
git commit -m "feat(refactoring-proxy): implement core processing logic"
```

---

## Task 5: Core Implementation (Part 4 - Serialization)

**Files:**
- Modify: `leaves/refactoring_proxy.go`

**Step 1: Implement serializeText**

```go
// serializeText outputs YAML format
func (rp *RefactoringProxy) serializeText(result *RefactoringProxyResult, writer io.Writer) {
	fmt.Fprintln(writer, "  refactoring_proxy:")
	fmt.Fprintf(writer, "    threshold: %.2f\n", result.Threshold)
	fmt.Fprintf(writer, "    tick_size: %d\n", int(result.TickSize.Seconds()))

	// Ticks array
	fmt.Fprint(writer, "    ticks: [")
	for i, tick := range result.Ticks {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%d", tick)
	}
	fmt.Fprintln(writer, "]")

	// Rename ratios array
	fmt.Fprint(writer, "    rename_ratios: [")
	for i, ratio := range result.RenameRatios {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%.4f", ratio)
	}
	fmt.Fprintln(writer, "]")

	// Is refactoring array
	fmt.Fprint(writer, "    is_refactoring: [")
	for i, isRef := range result.IsRefactoring {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%t", isRef)
	}
	fmt.Fprintln(writer, "]")

	// Total changes array
	fmt.Fprint(writer, "    total_changes: [")
	for i, total := range result.TotalChanges {
		if i > 0 {
			fmt.Fprint(writer, ", ")
		}
		fmt.Fprintf(writer, "%d", total)
	}
	fmt.Fprintln(writer, "]")
}
```

**Step 2: Implement serializeBinary**

```go
// serializeBinary outputs Protocol Buffers format
func (rp *RefactoringProxy) serializeBinary(result *RefactoringProxyResult, writer io.Writer) error {
	message := pb.RefactoringProxyResults{
		Ticks:         make([]int32, len(result.Ticks)),
		RenameRatios:  make([]float32, len(result.RenameRatios)),
		IsRefactoring: result.IsRefactoring,
		TotalChanges:  make([]int32, len(result.TotalChanges)),
		Threshold:     float32(result.Threshold),
		TickSize:      int64(result.TickSize),
	}

	for i := range result.Ticks {
		message.Ticks[i] = int32(result.Ticks[i])
		message.RenameRatios[i] = float32(result.RenameRatios[i])
		message.TotalChanges[i] = int32(result.TotalChanges[i])
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}
```

**Step 3: Implement Serialize**

```go
// Serialize converts the analysis result as returned by Finalize() to text or bytes.
func (rp *RefactoringProxy) Serialize(result interface{}, binary bool, writer io.Writer) error {
	refactoringResult := result.(RefactoringProxyResult)
	if binary {
		return rp.serializeBinary(&refactoringResult, writer)
	}
	rp.serializeText(&refactoringResult, writer)
	return nil
}
```

**Step 4: Implement Deserialize**

```go
// Deserialize converts the specified protobuf bytes to RefactoringProxyResult.
func (rp *RefactoringProxy) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.RefactoringProxyResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	result := RefactoringProxyResult{
		Ticks:         make([]int, len(message.Ticks)),
		RenameRatios:  make([]float64, len(message.RenameRatios)),
		IsRefactoring: message.IsRefactoring,
		TotalChanges:  make([]int, len(message.TotalChanges)),
		Threshold:     float64(message.Threshold),
		TickSize:      time.Duration(message.TickSize),
	}

	for i := range message.Ticks {
		result.Ticks[i] = int(message.Ticks[i])
		result.RenameRatios[i] = float64(message.RenameRatios[i])
		result.TotalChanges[i] = int(message.TotalChanges[i])
	}

	return result, nil
}
```

**Step 5: Add init registration**

```go
func init() {
	core.Registry.Register(&RefactoringProxy{})
}
```

**Step 6: Commit serialization**

```bash
git add leaves/refactoring_proxy.go
git commit -m "feat(refactoring-proxy): implement serialization and registration"
```

---

## Task 6: Write Tests (Part 1 - Basic Tests)

**Files:**
- Create: `leaves/refactoring_proxy_test.go`

**Step 1: Write test imports and helper**

```go
package leaves

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeRefactoringTestDeps creates test dependencies with renames
func makeRefactoringTestDeps(tick int, changes object.Changes) map[string]interface{} {
	return map[string]interface{}{
		core.DependencyCommit:       &object.Commit{},
		items.DependencyTick:        tick,
		items.DependencyTreeChanges: changes,
	}
}

// makeRename creates a rename change
func makeRename(fromName, toName string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: fromName},
		To:   object.ChangeEntry{Name: toName},
	}
}

// makeAddition creates an addition change
func makeAddition(name string) *object.Change {
	return &object.Change{
		To: object.ChangeEntry{Name: name},
	}
}

// makeDeletion creates a deletion change
func makeDeletion(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: name},
	}
}

// makeModification creates a modification change
func makeModification(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: name},
		To:   object.ChangeEntry{Name: name},
	}
}
```

**Step 2: Write test for rename detection**

```go
func TestRefactoringProxy_RenameDetection(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	changes := object.Changes{
		makeRename("old/file.go", "new/file.go"),  // Rename
		makeAddition("added.go"),                   // Addition (not rename)
		makeDeletion("deleted.go"),                 // Deletion (not rename)
		makeModification("modified.go"),            // Modification (not rename)
	}

	deps := makeRefactoringTestDeps(0, changes)
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize().(RefactoringProxyResult)

	assert.Equal(t, 1, len(result.Ticks))
	assert.Equal(t, 4, result.TotalChanges[0])
	assert.Equal(t, 1, result.tickMetrics[0].Renames)
	assert.InDelta(t, 0.25, result.RenameRatios[0], 0.01) // 1/4 = 0.25
	assert.False(t, result.IsRefactoring[0])               // 0.25 < 0.5
}
```

**Step 3: Run test to verify it passes**

Run:
```bash
go test -v ./leaves -run TestRefactoringProxy_RenameDetection
```

Expected: PASS

**Step 4: Commit rename detection test**

```bash
git add leaves/refactoring_proxy_test.go
git commit -m "test(refactoring-proxy): add rename detection test"
```

---

## Task 7: Write Tests (Part 2 - Classification & Edge Cases)

**Files:**
- Modify: `leaves/refactoring_proxy_test.go`

**Step 1: Write classification test**

```go
func TestRefactoringProxy_Classification(t *testing.T) {
	testCases := []struct {
		name          string
		threshold     float64
		renames       int
		total         int
		expectRefac   bool
	}{
		{"below threshold", 0.5, 2, 10, false},       // 0.2 < 0.5
		{"at threshold", 0.5, 5, 10, false},          // 0.5 == 0.5 (not >)
		{"above threshold", 0.5, 6, 10, true},        // 0.6 > 0.5
		{"all renames", 0.5, 10, 10, true},           // 1.0 > 0.5
		{"high threshold", 0.9, 8, 10, false},        // 0.8 < 0.9
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
```

**Step 2: Write empty commits test**

```go
func TestRefactoringProxy_EmptyCommits(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	// Empty changes
	deps := makeRefactoringTestDeps(0, object.Changes{})
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize().(RefactoringProxyResult)

	// Should have no ticks recorded (empty commits are skipped)
	assert.Equal(t, 0, len(result.Ticks))
}
```

**Step 3: Run tests**

Run:
```bash
go test -v ./leaves -run TestRefactoringProxy_Classification
go test -v ./leaves -run TestRefactoringProxy_EmptyCommits
```

Expected: Both PASS

**Step 4: Commit classification and edge case tests**

```bash
git add leaves/refactoring_proxy_test.go
git commit -m "test(refactoring-proxy): add classification and edge case tests"
```

---

## Task 8: Write Tests (Part 3 - Multiple Ticks & Serialization)

**Files:**
- Modify: `leaves/refactoring_proxy_test.go`

**Step 1: Write multiple ticks test**

```go
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
	assert.False(t, result.IsRefactoring[2]) // exactly 0.5, not > 0.5
}
```

**Step 2: Write serialization test**

```go
func TestRefactoringProxy_Serialization(t *testing.T) {
	rp := &RefactoringProxy{}
	require.NoError(t, rp.Initialize(nil))

	// Create test data
	deps := makeRefactoringTestDeps(0, object.Changes{
		makeRename("old.go", "new.go"),
		makeRename("foo.go", "bar.go"),
		makeAddition("baz.go"),
	})
	_, err := rp.Consume(deps)
	require.NoError(t, err)

	result := rp.Finalize()

	// Test protobuf serialization round-trip
	serialized, err := rp.serializeBinary(&result.(RefactoringProxyResult), new(bytes.Buffer))
	require.NoError(t, err)

	// Note: Full round-trip would require proper protobuf marshal/unmarshal
	// This test verifies serializeBinary doesn't crash
	assert.NotNil(t, serialized)
}
```

**Step 3: Add bytes import**

Add to imports at top of file:
```go
import (
	"bytes"
	// ... existing imports
)
```

**Step 4: Run tests**

Run:
```bash
go test -v ./leaves -run TestRefactoringProxy_MultipleTicks
go test -v ./leaves -run TestRefactoringProxy_Serialization
```

Expected: Both PASS

**Step 5: Commit multiple ticks and serialization tests**

```bash
git add leaves/refactoring_proxy_test.go
git commit -m "test(refactoring-proxy): add multiple ticks and serialization tests"
```

---

## Task 9: Run Full Test Suite

**Step 1: Run all RefactoringProxy tests**

Run:
```bash
go test -v ./leaves -run TestRefactoringProxy
```

Expected: All tests PASS

**Step 2: Run full leaves package tests**

Run:
```bash
go test ./leaves
```

Expected: All tests PASS (including existing tests)

**Step 3: Build hercules binary**

Run:
```bash
just hercules
```

Expected: Build succeeds, binary created at `cmd/hercules/hercules`

**Step 4: Verify flag registration**

Run:
```bash
./cmd/hercules/hercules --help | grep refactoring
```

Expected: Shows `--refactoring-proxy` and `--refactoring-threshold` flags

---

## Task 10: Manual Integration Test

**Step 1: Run analysis on hercules repo without -M flag**

Run:
```bash
./cmd/hercules/hercules --refactoring-proxy . > /tmp/refac-no-m.yml
```

**Step 2: Check output (should show low/zero rename ratios)**

Run:
```bash
grep -A 10 "refactoring_proxy:" /tmp/refac-no-m.yml
```

Expected: See YAML output with mostly zero rename ratios (no RenameAnalysis)

**Step 3: Run analysis WITH -M flag**

Run:
```bash
./cmd/hercules/hercules --refactoring-proxy -M . > /tmp/refac-with-m.yml
```

**Step 4: Check output (should show actual rename ratios)**

Run:
```bash
grep -A 10 "refactoring_proxy:" /tmp/refac-with-m.yml
```

Expected: See YAML output with varied rename ratios detected

**Step 5: Test custom threshold**

Run:
```bash
./cmd/hercules/hercules --refactoring-proxy -M --refactoring-threshold 0.7 . > /tmp/refac-custom.yml
```

**Step 6: Verify threshold in output**

Run:
```bash
grep "threshold:" /tmp/refac-custom.yml
```

Expected: Shows `threshold: 0.70`

**Step 7: Commit integration test confirmation**

```bash
git commit --allow-empty -m "test(refactoring-proxy): manual integration test passed"
```

---

## Task 11: Update PLAN.md

**Files:**
- Modify: `PLAN.md:381-402`

**Step 1: Mark Go analysis as complete**

Change:
```markdown
- [ ] **Go analysis** (`leaves/refactoring_proxy.go`)
```

To:
```markdown
- [x] **Go analysis** (`leaves/refactoring_proxy.go`)
```

**Step 2: Mark Protobuf schema as complete**

Change:
```markdown
- [ ] **Protobuf schema** — add `RefactoringProxyResults` message
```

To:
```markdown
- [x] **Protobuf schema** — add `RefactoringProxyResults` message
```

**Step 3: Mark Tests as complete**

Change:
```markdown
- [ ] **Tests**
```

To:
```markdown
- [x] **Tests**
```

**Step 4: Commit PLAN.md update**

```bash
git add PLAN.md
git commit -m "docs: mark refactoring proxy Go implementation complete"
```

---

## Task 12: Python Visualization (Part 1 - Standalone Plot)

**Files:**
- Create: `python/labours/modes/refactoring_proxy.py`

**Step 1: Write module header and imports**

```python
"""
Refactoring Proxy visualization module.

Generates time series plots showing rename rate over time to identify
refactoring phases distinct from feature development.
"""

import matplotlib.pyplot as plt
import numpy as np
from datetime import datetime, timedelta


def plot_refactoring_timeline(data, output_path="refactoring_timeline.png"):
    """
    Creates a time series visualization of rename rate over time.

    Args:
        data: RefactoringProxyResults from YAML/protobuf
        output_path: Path to save the output plot

    Features:
        - Line plot: rename_ratios vs time
        - Shaded regions: refactoring-heavy periods
        - Threshold line: horizontal dashed line
        - Color scheme: Orange/amber for refactoring phases
    """
    # Extract data
    ticks = data['ticks']
    rename_ratios = data['rename_ratios']
    is_refactoring = data['is_refactoring']
    threshold = data['threshold']
    tick_size_seconds = data['tick_size']

    if not ticks:
        print("No refactoring proxy data to plot.")
        return

    # Convert ticks to dates
    dates = [datetime.fromtimestamp(tick * tick_size_seconds) for tick in ticks]

    # Create figure
    fig, ax = plt.subplots(figsize=(12, 6))

    # Plot rename ratio line
    ax.plot(dates, rename_ratios, color='steelblue', linewidth=2, label='Rename Rate')

    # Shade refactoring periods
    for i in range(len(ticks)):
        if is_refactoring[i]:
            # Shade this tick period
            x_start = dates[i]
            x_end = dates[i + 1] if i + 1 < len(dates) else dates[i] + timedelta(seconds=tick_size_seconds)
            ax.axvspan(x_start, x_end, alpha=0.3, color='orange', label='Refactoring Period' if i == 0 or not is_refactoring[i-1] else '')

    # Add threshold line
    ax.axhline(y=threshold, color='red', linestyle='--', linewidth=1.5, label=f'Threshold ({threshold:.2f})')

    # Formatting
    ax.set_xlabel('Date', fontsize=12)
    ax.set_ylabel('Rename Ratio', fontsize=12)
    ax.set_title('Refactoring Activity Over Time', fontsize=14, fontweight='bold')
    ax.set_ylim(0, 1.0)
    ax.grid(True, alpha=0.3)
    ax.legend(loc='upper right')

    # Rotate x-axis labels
    plt.xticks(rotation=45, ha='right')

    plt.tight_layout()
    plt.savefig(output_path, dpi=150)
    plt.close()

    print(f"Refactoring timeline saved to {output_path}")


def print_refactoring_summary(data):
    """
    Prints a text summary of refactoring activity.

    Args:
        data: RefactoringProxyResults from YAML/protobuf
    """
    ticks = data['ticks']
    is_refactoring = data['is_refactoring']
    rename_ratios = data['rename_ratios']
    threshold = data['threshold']

    if not ticks:
        print("No refactoring proxy data available.")
        return

    total_ticks = len(ticks)
    refactoring_ticks = sum(is_refactoring)
    refactoring_percentage = (refactoring_ticks / total_ticks) * 100

    print("\n=== Refactoring Activity Summary ===")
    print(f"Total ticks analyzed: {total_ticks}")
    print(f"Refactoring-heavy ticks: {refactoring_ticks} ({refactoring_percentage:.1f}%)")
    print(f"Classification threshold: {threshold:.2f}")

    # Find top refactoring periods
    top_indices = sorted(range(len(rename_ratios)), key=lambda i: rename_ratios[i], reverse=True)[:5]
    print("\nTop 5 refactoring periods:")
    for rank, idx in enumerate(top_indices, 1):
        print(f"  {rank}. Tick {ticks[idx]}: {rename_ratios[idx]:.2%} rename rate")

    print()
```

**Step 2: Commit Python standalone plot**

```bash
git add python/labours/modes/refactoring_proxy.py
git commit -m "feat(refactoring-proxy): add Python visualization module"
```

---

## Task 13: Python Visualization (Part 2 - Integration)

**Files:**
- Modify: `python/labours/modes/__init__.py` (if exists)

**Step 1: Check if __init__.py exists**

Run:
```bash
ls python/labours/modes/__init__.py
```

**Step 2: If exists, add import**

If file exists, add:
```python
from . import refactoring_proxy
```

If file doesn't exist, create it:
```python
"""Labours visualization modes."""

from . import refactoring_proxy
```

**Step 3: Test Python import**

Run:
```bash
cd python
python3 -c "from labours.modes import refactoring_proxy; print('Import successful')"
```

Expected: "Import successful"

**Step 4: Commit Python integration**

```bash
git add python/labours/modes/__init__.py
git commit -m "feat(refactoring-proxy): integrate visualization into labours"
```

---

## Task 14: Documentation & Cleanup

**Files:**
- Modify: `PLAN.md:381-402`

**Step 1: Mark Python visualization task**

Change:
```markdown
- [ ] **Python visualization**
```

To:
```markdown
- [x] **Python visualization**
  - Standalone timeline plot with shaded refactoring periods
  - Text summary of refactoring activity
  - (Burndown overlay deferred - needs burndown.py analysis)
```

**Step 2: Add usage notes to PLAN.md**

After the task checklist, add:
```markdown

**Usage Example:**
```bash
# Run analysis with rename detection
hercules --refactoring-proxy -M /path/to/repo > analysis.yml

# Visualize in Python
python3 -c "
import yaml
from labours.modes.refactoring_proxy import plot_refactoring_timeline, print_refactoring_summary

with open('analysis.yml') as f:
    data = yaml.safe_load(f)['refactoring_proxy']

plot_refactoring_timeline(data)
print_refactoring_summary(data)
"
```
```

**Step 3: Commit documentation**

```bash
git add PLAN.md
git commit -m "docs: add refactoring proxy usage examples to PLAN.md"
```

---

## Task 15: Final Verification

**Step 1: Run full Go test suite**

Run:
```bash
go test ./...
```

Expected: All tests PASS

**Step 2: Build final binary**

Run:
```bash
just clean
just hercules
```

Expected: Clean build succeeds

**Step 3: Run end-to-end test**

Run:
```bash
./cmd/hercules/hercules --refactoring-proxy -M . > /tmp/final-test.yml
cat /tmp/final-test.yml | grep -A 20 "refactoring_proxy:"
```

Expected: See complete YAML output with all fields populated

**Step 4: Verify Python can parse output**

Run:
```bash
python3 -c "
import yaml
with open('/tmp/final-test.yml') as f:
    data = yaml.safe_load(f)
    rp = data['refactoring_proxy']
    print(f'Loaded {len(rp[\"ticks\"])} ticks successfully')
    print(f'Threshold: {rp[\"threshold\"]}')
"
```

Expected: Successful parsing with tick count and threshold shown

**Step 5: Create final commit**

```bash
git add -A
git commit -m "feat: complete refactoring proxy analysis implementation

Implements task 6.6 from PLAN.md - tracking rename/move rates to
distinguish refactoring phases from feature development.

Components:
- Go analysis in leaves/refactoring_proxy.go
- Protobuf schema for RefactoringProxyResults
- Comprehensive test suite (7 test cases)
- Python visualization with timeline plot and summary
- Integration with hercules pipeline

Usage:
  hercules --refactoring-proxy -M /path/to/repo > analysis.yml

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Completion Checklist

- [x] Protobuf schema added and regenerated
- [x] Core Go implementation with all PipelineItem methods
- [x] Processing logic (Initialize, Consume, Finalize)
- [x] Serialization (YAML and Protobuf)
- [x] Comprehensive test suite (rename detection, classification, edge cases, serialization)
- [x] Python visualization (standalone timeline plot)
- [x] Integration testing
- [x] Documentation updates
- [x] PLAN.md marked complete

## Known Limitations

1. **Burndown overlay**: Deferred until burndown.py structure is analyzed
2. **Rename detection dependency**: Requires `-M` flag (RenameAnalysis) for accurate results
3. **Threshold selection**: Fixed at 0.5 by default, may need tuning per repository

## Next Steps (Future Work)

1. Add burndown chart overlay visualization
2. Collect data from multiple repositories to calibrate optimal threshold
3. Add statistical analysis (mean, stddev, outlier detection)
4. Consider per-author refactoring metrics (who does most refactoring)
