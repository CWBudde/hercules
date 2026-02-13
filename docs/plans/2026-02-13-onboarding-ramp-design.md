# Onboarding Ramp Analysis Design

**Date:** 2026-02-13
**Feature:** Task 6.4 - Onboarding Ramp Analysis
**Status:** Approved

## Overview

The onboarding ramp analysis measures how quickly new contributors ramp up in a repository. It tracks time-to-first-change, breadth-of-files touched in the first N days, and convergence to stable contribution patterns. This analysis helps identify onboarding friction, effective mentorship, and cohort-based trends.

## Goals

- Track per-author progression: first commit, cumulative commits, files touched, lines changed
- Capture snapshots at configurable milestones (default: 7, 30, 90 days after first commit)
- Group authors into cohorts by join month and compute cohort averages
- Distinguish between all activity and "meaningful" contributions (configurable line threshold)
- Provide data for cohort heatmaps and per-author ramp curves

## Architecture

### Pipeline Integration

**Component:** `OnboardingAnalysis` in `leaves/onboarding.go`

- Implements: `core.LeafPipelineItem`
- Flag: `--onboarding`
- Provides: Nothing (leaf node)
- Requires:
  - `identity.DependencyAuthor` - Author identity
  - `items.DependencyTick` - Timestamp bucket
  - `items.DependencyLineStats` - Line change statistics
  - `items.DependencyTreeChanges` - File tree changes
- Embeddings:
  - `core.NoopMerger` - No branch merging support
  - `core.OneShotMergeProcessor` - Standard merge handling

### Configuration Options

1. `--onboarding-windows` (default: `"7,30,90"`)
   - Comma-separated list of days for snapshot milestones
   - Example: `"7,14,30,60,90"` for more granular tracking

2. `--onboarding-meaningful-threshold` (default: `10`)
   - Minimum lines changed for a commit to count as "meaningful"
   - Filters trivial commits (typo fixes, formatting) from ramp metrics

### Key Design Decisions

- **Join time:** First commit timestamp defines when an author "joins"
- **Snapshot metrics:** Record cumulative values at milestone points (7, 30, 90 days)
- **Dual tracking:** Maintain both filtered (meaningful) and unfiltered (all commits) metrics
- **Cohort grouping:** Month-level granularity (YYYY-MM format)

## Data Structures

### Internal State (Consume Phase)

```go
type OnboardingAnalysis struct {
    core.NoopMerger
    core.OneShotMergeProcessor

    WindowDays              []int
    MeaningfulThreshold     int

    // author -> tick -> metrics
    authorTimeline          map[int]map[int]*onboardingTickMetrics

    reversedPeopleDict      []string
    tickSize                time.Duration
    l                       core.Logger
}

type onboardingTickMetrics struct {
    Commits          int
    Files            map[string]bool  // set of unique files
    LinesAdded       int
    LinesRemoved     int
    LinesChanged     int

    // Filtered metrics (meaningful commits only)
    MeaningfulCommits      int
    MeaningfulFiles        map[string]bool
    MeaningfulLinesAdded   int
    MeaningfulLinesRemoved int
    MeaningfulLinesChanged int
}
```

### Result Structures (Finalize Phase)

```go
type OnboardingResult struct {
    Authors             map[int]*AuthorOnboardingData
    Cohorts             map[string]*CohortStats
    WindowDays          []int
    MeaningfulThreshold int
    reversedPeopleDict  []string
    tickSize            time.Duration
}

type AuthorOnboardingData struct {
    FirstCommitTick int
    JoinCohort      string  // "YYYY-MM"

    // Indexed by window days (e.g., 7, 30, 90)
    Snapshots       map[int]*OnboardingSnapshot
}

type OnboardingSnapshot struct {
    DaysSinceJoin     int

    // All commits
    TotalCommits      int
    TotalFiles        int
    TotalLines        int

    // Meaningful commits only
    MeaningfulCommits int
    MeaningfulFiles   int
    MeaningfulLines   int
}

type CohortStats struct {
    Cohort            string  // "YYYY-MM"
    AuthorCount       int

    // Averaged across all authors in cohort
    AverageSnapshots  map[int]*OnboardingSnapshot
}
```

## Processing Logic

### Consume() Phase

```
1. Extract dependencies from deps map
2. Initialize author's timeline if first commit
3. Get or create tick metrics for this author+tick
4. Process each changed file:
   - Add to Files set
   - Accumulate line stats
   - If total lines >= MeaningfulThreshold:
     - Add to MeaningfulFiles set
     - Accumulate meaningful line stats
5. Increment commit counters (all and meaningful)
6. Return nil, nil (no downstream dependencies)
```

### Finalize() Phase

```
1. Initialize result structures

2. For each author:
   a. Find first commit tick (minimum tick in timeline)
   b. Convert tick to timestamp, extract join cohort (YYYY-MM)
   c. Build cumulative timeline:
      - Sort ticks chronologically
      - Accumulate metrics tick-by-tick
   d. Compute window snapshots:
      - For each window day (e.g., 7, 30, 90):
        - Calculate target tick = first + (days × ticks/day)
        - Find closest actual tick ≤ target
        - Extract cumulative metrics
        - Store as OnboardingSnapshot
   e. Group into cohort

3. Compute cohort aggregates:
   - For each cohort:
     - Count authors
     - Average snapshots across authors
     - Store as CohortStats

4. Return OnboardingResult
```

### Implementation Notes

- Use `items.FloorTime(timestamp, tickSize)` for tick/timestamp conversion
- Handle sparse timelines (not every author commits every tick)
- Use linear search through sorted ticks for snapshot extraction (simple, fast)
- Extract cohort via `timestamp.Format("2006-01")`

## Protobuf Schema

Add to `internal/pb/pb.proto`:

```protobuf
message OnboardingSnapshot {
    int32 days_since_join = 1;

    int32 total_commits = 2;
    int32 total_files = 3;
    int32 total_lines = 4;

    int32 meaningful_commits = 5;
    int32 meaningful_files = 6;
    int32 meaningful_lines = 7;
}

message AuthorOnboardingData {
    int32 first_commit_tick = 1;
    string join_cohort = 2;
    map<int32, OnboardingSnapshot> snapshots = 3;
}

message CohortStats {
    string cohort = 1;
    int32 author_count = 2;
    map<int32, OnboardingSnapshot> average_snapshots = 3;
}

message OnboardingResults {
    map<int32, AuthorOnboardingData> authors = 1;
    map<string, CohortStats> cohorts = 2;
    repeated int32 window_days = 3;
    int32 meaningful_threshold = 4;
    repeated string dev_index = 5;
    int64 tick_size = 6;
}
```

### YAML Format

```yaml
onboarding:
  window_days: [7, 30, 90]
  meaningful_threshold: 10
  authors:
    0:
      first_commit_tick: 42
      join_cohort: "2024-01"
      snapshots:
        7: {days: 7, commits: 3, files: 5, lines: 120, meaningful_commits: 2, ...}
        30: {days: 30, commits: 15, files: 12, lines: 450, ...}
        90: {days: 90, commits: 48, files: 25, lines: 1200, ...}
  cohorts:
    "2024-01":
      author_count: 5
      average_snapshots:
        7: {days: 7, commits: 2.4, files: 3.8, lines: 89.2, ...}
        30: {days: 30, commits: 12.1, files: 9.5, lines: 378.3, ...}
  people:
    - developer1@example.com
    - developer2@example.com
  tick_size: 86400
```

## Testing Strategy

### Test Cases

**File:** `leaves/onboarding_test.go`

1. **TestOnboardingAnalysis_BasicTracking**
   - Single author, 3 commits across different ticks
   - Verify cumulative metrics increase correctly
   - Check window snapshots at boundaries

2. **TestOnboardingAnalysis_MultipleAuthors**
   - 3 authors joining at different times
   - Verify independent timelines
   - Check cohort grouping

3. **TestOnboardingAnalysis_MeaningfulThreshold**
   - Mix of small (<10 lines) and large (>10 lines) commits
   - Verify meaningful metrics filter correctly
   - Verify all-commits metrics count everything

4. **TestOnboardingAnalysis_WindowBoundaries**
   - Commits at exactly 7, 30, 90 days
   - Commits with gaps near boundaries
   - Verify snapshot selection (closest tick ≤ target)

5. **TestOnboardingAnalysis_CohortAggregation**
   - Multiple authors in same cohort
   - Verify average calculations
   - Test single-author cohort edge case

6. **TestOnboardingAnalysis_Configuration**
   - Custom windows: [14, 60]
   - Custom threshold: 50
   - Verify configuration applies

7. **TestOnboardingAnalysis_Serialization**
   - YAML round-trip
   - Protobuf round-trip
   - Verify consistency

### Test Utilities

- `makeTestCommit(author, tick, files, linesPerFile)` - Generate test commits
- `assertSnapshot(t, snapshot, expected)` - Compare snapshots
- Follow patterns from `devs_test.go` and `knowledge_diffusion_test.go`

### Coverage Goal

Target 80%+ code coverage with focus on edge cases.

## Python Visualization

**File:** `python/labours/modes/onboarding.py`

### Cohort Heatmap

- **Rows:** Join month (YYYY-MM)
- **Columns:** Days since first commit (7, 30, 90)
- **Cells:** Average cumulative commits/files/lines
- **Color:** Intensity shows ramp speed

### Per-Author Ramp Curves

- **X-axis:** Days since join
- **Y-axis:** Cumulative metric (commits, files, or lines)
- **Lines:** One per author, optionally colored by cohort
- **Options:** Overlay all authors or small multiples by cohort

Both visualizations support filtering by meaningful vs. all commits.

## Implementation Approach

Use in-memory timeline with post-processing:

1. **Consume():** Store per-author per-tick metrics in timeline map
2. **Finalize():** Process timelines to compute window snapshots and cohort aggregates

**Advantages:**
- Follows established Hercules patterns (DevsAnalysis, KnowledgeDiffusionAnalysis)
- Simple, testable logic with clear separation of concerns
- Flexible for adding metrics or changing windows
- Memory efficient (aggregates only, not individual commits)

## Risks and Mitigations

**Risk:** Window computation may miss exact day boundaries if tick granularity is coarse (e.g., weekly ticks).

**Mitigation:** Use "closest tick ≤ target" logic to approximate. Document behavior for users with coarse tick sizes.

**Risk:** Large repositories with thousands of contributors may have high memory usage.

**Mitigation:** Timeline stores only aggregates (not commit objects). Even 10,000 authors × 1,000 ticks = ~100MB maximum.

## Success Criteria

- Analysis successfully tracks onboarding progression for all authors
- Window snapshots accurately reflect cumulative metrics at milestone days
- Cohort averages help identify trends across time periods
- YAML and protobuf serialization work correctly
- Tests achieve 80%+ coverage
- Python visualization generates cohort heatmaps and ramp curves
