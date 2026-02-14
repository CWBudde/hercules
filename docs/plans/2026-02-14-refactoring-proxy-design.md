# Refactoring Proxy (Move/Rename Rate) - Design Document

**Date**: 2026-02-14
**Feature**: Task 6.6 from PLAN.md
**Author**: Claude Code

## Overview

The Refactoring Proxy analysis tracks the proportion of file renames/moves over time to distinguish refactoring phases from feature development work. When developers restructure code, commits tend to have a high ratio of renames versus other changes (additions, modifications, deletions). By tracking this ratio over time, we can identify periods where the team focused on code organization rather than adding features.

## Architecture

### Component Type
- **Type**: Leaf Pipeline Item (`core.LeafPipelineItem`)
- **Flag**: `--refactoring-proxy`
- **Dependencies**:
  - `DependencyTreeChanges` (from TreeDiff/RenameAnalysis)
  - `DependencyTick` (from TicksSinceStart)
- **Output**: `RefactoringProxyResult` containing time series data

### Key Design Decision: Rely on RenameAnalysis

The implementation assumes users enable rename detection via the `-M` flag (RenameAnalysis plumbing component). This component already provides sophisticated similarity-based rename detection using the existing `internal/plumbing/renames.go` infrastructure. When enabled, `TreeChanges` properly identifies renames as changes where `From.Name != To.Name` with similar content.

This approach:
- Reuses existing, battle-tested rename detection logic
- Keeps RefactoringProxy focused on ratio tracking and classification
- Follows the single-responsibility principle
- Aligns with the plan's note: "Rename detection already exists"

## Data Structures

### Internal State

```go
type RefactoringProxy struct {
    core.NoopMerger
    core.OneShotMergeProcessor

    // Configuration
    RefactoringThreshold float64  // Default 0.5 (50% renames = refactoring)

    // Per-tick accumulation: tick -> metrics
    tickMetrics map[int]*tickChangeMetrics
    tickSize    time.Duration

    l core.Logger
}

type tickChangeMetrics struct {
    TotalChanges int  // All additions, modifications, deletions, renames
    Renames      int  // Changes where From.Name != To.Name (both non-empty)
}
```

### Output Result

```go
type RefactoringProxyResult struct {
    // Time series data (parallel arrays, same length)
    Ticks              []int       // Tick indices
    RenameRatios       []float64   // Rename ratio per tick (0.0 to 1.0)
    IsRefactoring      []bool      // true if ratio > threshold
    TotalChanges       []int       // Total changes per tick (for context)

    // Configuration metadata
    Threshold          float64     // The threshold used for classification
    TickSize           time.Duration
}
```

## Processing Flow

### Rename Detection Logic

```go
func isRename(change *object.Change) bool {
    // A rename has both From and To set, with different names
    hasFrom := change.From.Name != ""
    hasTo := change.To.Name != ""
    differentNames := change.From.Name != change.To.Name

    return hasFrom && hasTo && differentNames
}
```

This correctly identifies:
- **Renames**: Both From and To present, different paths
- **Not renames**: Additions (only To), Deletions (only From), Modifications (same path)

### Per-Commit Processing

```go
func (rp *RefactoringProxy) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
    if !rp.ShouldConsumeCommit(deps) {
        return nil, nil
    }

    tick := deps[items.DependencyTick].(int)
    treeChanges := deps[items.DependencyTreeChanges].(object.Changes)

    if len(treeChanges) == 0 {
        return nil, nil  // Empty commit, skip
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

    return nil, nil  // Leaf item, no downstream deps
}
```

### Finalization & Classification

```go
func (rp *RefactoringProxy) Finalize() interface{} {
    // Sort ticks for ordered time series
    ticks := make([]int, 0, len(rp.tickMetrics))
    for tick := range rp.tickMetrics {
        ticks = append(ticks, tick)
    }
    sort.Ints(ticks)

    // Build parallel arrays
    result := RefactoringProxyResult{
        Ticks:        make([]int, len(ticks)),
        RenameRatios: make([]float64, len(ticks)),
        IsRefactoring: make([]bool, len(ticks)),
        TotalChanges: make([]int, len(ticks)),
        Threshold:    rp.RefactoringThreshold,
        TickSize:     rp.tickSize,
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

**Classification Logic**:
- Rename ratio = `renames / total_changes` (file count based)
- Tick is refactoring-heavy if: `ratio > threshold`
- Edge case: Zero changes → ratio = 0.0, not refactoring

## Configuration

```go
const (
    ConfigRefactoringThreshold = "RefactoringProxy.Threshold"
)

func (rp *RefactoringProxy) ListConfigurationOptions() []core.ConfigurationOption {
    return []core.ConfigurationOption{
        {
            Name:        ConfigRefactoringThreshold,
            Description: "Rename ratio threshold to classify a tick as refactoring-heavy (0.0-1.0).",
            Flag:        "refactoring-threshold",
            Type:        core.FloatConfigurationOption,
            Default:     0.5,
        },
    }
}
```

**Usage**:
```bash
hercules --refactoring-proxy -M /path/to/repo > analysis.yml
hercules --refactoring-proxy -M --refactoring-threshold 0.7 /path/to/repo > analysis.yml
```

Note: `-M` flag enables RenameAnalysis for proper rename detection.

## Serialization

### YAML Format

```yaml
refactoring_proxy:
  threshold: 0.5
  tick_size: 86400  # seconds
  ticks: [0, 1, 2, 3, 4, 5]
  rename_ratios: [0.1, 0.3, 0.7, 0.2, 0.15, 0.8]
  is_refactoring: [false, false, true, false, false, true]
  total_changes: [50, 40, 30, 45, 60, 25]
```

### Protobuf Schema

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

### Implementation

- `serializeText()`: Output YAML with parallel arrays
- `serializeBinary()`: Output protobuf for efficient storage
- `Deserialize()`: Read protobuf back into `RefactoringProxyResult`
- Follows same pattern as `OnboardingAnalysis`

## Python Visualization

### 1. Standalone Time Series Plot

**File**: `python/labours/modes/refactoring_proxy.py`

```python
def plot_refactoring_timeline(data, output_path):
    """
    Creates a time series visualization of rename rate over time.

    Features:
    - Line plot: rename_ratios vs time (converted from ticks)
    - Shaded regions: refactoring-heavy periods (is_refactoring == true)
    - Threshold line: horizontal dashed line at threshold value
    - Color scheme: Orange/amber for refactoring phases
    """
```

**Output**: `refactoring_timeline.png`

### 2. Burndown Chart Overlay

**Enhancement**: Modify `python/labours/modes/burndown.py`

```python
def plot_burndown(..., refactoring_events=None):
    """
    Existing burndown plot with optional refactoring event markers.

    If refactoring_events provided:
    - Add vertical bands or annotations where is_refactoring == true
    - Shows correlation between refactoring and code evolution
    """
```

**Output**: `burndown_with_refactoring.png`

### Text Summary

Console output showing:
- Total refactoring-heavy ticks
- Percentage of project lifetime spent refactoring
- Top refactoring periods (date ranges with highest rename ratios)

## Testing Strategy

### Unit Tests (Go)

**File**: `leaves/refactoring_proxy_test.go`

```go
// Test cases:
TestRefactoringProxy_BasicTracking          // Single tick, mixed changes
TestRefactoringProxy_Classification         // Threshold boundary conditions
TestRefactoringProxy_EmptyCommits           // Zero-division handling
TestRefactoringProxy_RenameDetection        // Rename vs add/delete/modify
TestRefactoringProxy_MultipleTicks          // Tick aggregation correctness
TestRefactoringProxy_Configuration          // Custom threshold values
TestRefactoringProxy_Serialization          // Round-trip YAML/Protobuf
```

**Test Repository Pattern**: Use `testRepository` helper (like `onboarding_test.go`) to create synthetic git histories with controlled rename scenarios.

### Integration Testing

- Run on hercules repository to validate real-world behavior
- Compare output with/without `-M` flag
- Verify no crashes on edge cases (empty repos, single commit)

## Implementation Complexity

**Effort**: Medium

**Components**:
1. **Go analysis** (`leaves/refactoring_proxy.go`) - ~400 lines
   - Core logic: ~150 lines
   - Serialization: ~150 lines
   - Boilerplate: ~100 lines
2. **Protobuf schema** (`internal/pb/pb.proto`) - ~10 lines
3. **Python visualization** (`python/labours/modes/refactoring_proxy.py`) - ~200 lines
4. **Tests** (`leaves/refactoring_proxy_test.go`) - ~300 lines

**Total**: ~900 lines of code

**Dependencies**: Reuses existing RenameAnalysis infrastructure, no new external dependencies.

## Key Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Rename detection | Rely on RenameAnalysis (`-M` flag) | Reuses existing, proven logic; keeps component focused |
| Ratio calculation | File count based | Simpler, more intuitive than line-weighted |
| Time granularity | Tick-based aggregation | Consistent with other analyses; reduces noise |
| Classification | Fixed threshold (configurable) | Pragmatic; avoids statistical complexity |
| Output format | Time series arrays | Clean for visualization; follows established patterns |

## Expected Outcomes

Users will be able to:
1. Identify when major refactoring efforts occurred
2. Distinguish restructuring work from feature development
3. Correlate refactoring phases with code evolution metrics
4. Quantify the proportion of effort spent on code organization
5. Make informed decisions about when to refactor vs. when to add features

The analysis provides both quantitative metrics (rename ratios, refactoring percentages) and visual insights (time series, event markers) to support project health assessment.
