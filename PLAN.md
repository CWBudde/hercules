# Hercules Pure Go Rewrite Completion Plan

## Project Overview

This plan outlines the steps to complete the Hercules pure Go rewrite, which modernizes the original `gopkg.in/src-d/hercules.v10` to use `github.com/meko-christian/hercules` with updated dependencies and improved architecture.

## Current State Analysis

### ✅ Completed Components

- [x] **Module Structure**: Successfully migrated from `gopkg.in/src-d/go-git.v4` to `github.com/go-git/go-git/v5`
- [x] **Build System**: Makefile updated to use new module path
- [x] **Core Pipeline**: Pipeline system and dependency resolution working
- [x] **Analysis Items**: All major analysis types ported (burndown, couples, devs, commits, etc.)
- [x] **Basic Functionality**: CLI interface and help system working
- [x] **Compilation Fixes**: Fixed string conversion errors in test files

### 🔄 Architectural Improvements Made

- **Burndown Refactoring**: Split monolithic `internal/burndown/file.go` into modular components:
  - `internal/burndown/matrices.go` - Matrix operations
  - `internal/linehistory/` - Line history tracking
  - `internal/join/` - Data joining operations
- **Identity Management**: Enhanced from single file to modular system:
  - `internal/plumbing/identity/people.go` - People detection
  - `internal/plumbing/identity/stories.go` - Story tracking
  - `internal/plumbing/identity/identity_shared.go` - Shared utilities
- **New Analysis Types**: Added `codechurn.go` and `linedump.go` analyses

## Completion Plan

### Phase 1: Core Infrastructure Validation ✅ COMPLETED

#### 1.1 Test Suite Completion

- [x] **Fix remaining compilation errors**
  - [x] Fixed `couples_test.go` string conversion issue
  - [x] Fixed `burndown_test.go` type mismatches (`map[string][][]int64` → `map[string]burndown.DenseHistory`)
  - [x] Fixed `burndown_legacy_test.go` type mismatches and added missing import
  - [x] Rewrote `temporal_activity_test.go` to match new dual-mode API (commits + lines)
  - [x] Fixed `TestBurndownInitialize` to match safety-first initialization
  - [x] Verify all test files compile without errors
  - [x] Run `go test ./...` successfully (except Babelfish-dependent tests)
- [x] **Validate module consistency**
  - [x] Updated Makefile to use correct module path
  - [x] All import statements use new module path
  - [x] go.mod dependencies correctly resolved

#### 1.2 Core Pipeline Testing

- [x] **Pipeline execution verification**
  - [x] Test basic pipeline functionality: `./hercules --dry-run .`
  - [x] Verify dependency resolution works correctly
  - [x] Test pipeline with multiple analysis items (`--temporal-activity`)
- [ ] **Advanced features validation** (deferred to Phase 2)
  - [ ] Test hibernation feature: `--hibernation-distance 10`
  - [ ] Verify merge tracking functionality
  - [ ] Test plugin system compatibility

### Phase 2: Analysis Feature Validation ✅ COMPLETED

#### 2.1 Burndown Analysis Testing

- [x] **New modular architecture**
  - [x] Test burndown analysis: `./hercules --burndown .`
  - [x] Test burndown with people tracking: `--burndown-people`
  - [x] Test burndown with files tracking: `--burndown-files`
  - [x] Verify line history tracking works correctly
  - [x] Test matrix operations functionality
- [ ] **Hibernation integration** (deferred - optional feature)
  - [ ] Test `--burndown-hibernation-threshold`
  - [ ] Test `--burndown-hibernation-disk` mode
  - [ ] Verify memory optimization works

#### 2.2 All Analysis Types Testing

- [x] **Core analyses**
  - [x] Burndown: `--burndown --burndown-people --burndown-files`
  - [x] Couples: `--couples`
  - [x] Devs: `--devs`
  - [x] CommitsStat: `--commits-stat`
  - [x] File history: `--file-history`
  - [x] Temporal Activity: `--temporal-activity`
  - [ ] Shotness: `--shotness` (requires Babelfish - not available)
  - [ ] Comment sentiment: `--sentiment` (requires TensorFlow - not available)
- [x] **New analyses**
  - [x] Code churn: `--codechurn`
  - [x] Line dump: `--linedump`
  - [x] Verified integration with pipeline

#### 2.3 Output Format Validation

- [x] **YAML output testing**
  - [x] Verify YAML format is generated correctly
  - [ ] Test output parsing with `labours` Python tools (deferred to Phase 3)
- [x] **Protocol Buffers testing**
  - [x] Test `--pb` flag functionality
  - [x] Verified binary format is generated correctly
  - [x] Test `hercules combine` command for merging results

### Phase 3: Integration & Compatibility Testing 🔗

#### 3.1 CLI Interface Validation ✅ COMPLETED

- [x] **Command line flags**
  - [x] Test all documented flags from `--help`
  - [x] Verify flag combinations work correctly
  - [x] Test edge cases and error handling
- [x] **Repository handling**
  - [x] Test with local repositories (current dir, absolute path)
  - [x] Test with --commits flag (custom commit history)
  - [x] Test with different repository sizes
  - [ ] Test with remote repositories (HTTPS/SSH) - deferred
  - [ ] Test caching functionality - deferred

#### 3.2 Python Integration Testing ✅ COMPLETED

- [x] **Labours compatibility**
  - [x] Install Python requirements: Used `uv` for installation (numpy 1.26.4 compatibility)
  - [x] Test basic plotting: `./hercules --burndown --quiet . > out.yml && labours -i out.yml -m burndown-project`
  - [x] Test Protocol Buffers mode: `./hercules --burndown --pb --quiet . > out.pb && labours -f pb -i out.pb -m burndown-project`
  - [x] Test temporal-activity plotting: Generated all 10 plots (commits + lines for weekdays, hours, months, weeks, heatmap)

#### 3.3 Performance & Memory Testing

- [ ] **Large repository testing**
  - [ ] Test with Linux kernel or similar large repo
  - [ ] Monitor memory usage during analysis
  - [ ] Verify hibernation prevents OOM errors
  - [ ] Compare performance with original implementation

### Phase 4: Output Validation & Comparison 📊

#### 4.1 Results Accuracy Testing

- [ ] **Side-by-side comparison**
  - [ ] Run same analysis on both versions
  - [ ] Compare YAML outputs (allowing for minor formatting differences)
  - [ ] Verify numerical results match
  - [ ] Document any intentional differences

#### 4.2 Edge Case Testing

- [ ] **Repository edge cases**
  - [ ] Empty repositories
  - [ ] Single commit repositories
  - [ ] Repositories with complex merge histories
  - [ ] Repositories with renames and moves
  - [ ] Repositories with binary files

### Phase 5: Documentation & Polish 📚

#### 5.1 Documentation Updates

- [ ] **README.md updates**
  - [ ] Update installation instructions
  - [ ] Update module path references
  - [ ] Update Go version requirements (1.18+)
  - [ ] Update example commands
- [ ] **Code documentation**
  - [ ] Update package documentation
  - [ ] Add documentation for new modular architecture
  - [ ] Document new analysis types

#### 5.2 Final cleanup

- [ ] **Code quality**
  - [ ] Run `go fmt` on all packages
  - [ ] Run `go vet` and fix warnings
  - [ ] Remove any dead code
  - [ ] Optimize imports

- [ ] **Release preparation**
  - [ ] Update version information
  - [ ] Update CLAUDE.md with final architecture
  - [ ] Create migration guide from original

## Testing Strategy

### Automated Testing

```bash
# Core test suite
just test

# Individual package testing
go test ./internal/core
go test ./internal/plumbing
go test ./leaves
go test ./internal/burndown
go test ./internal/linehistory

# Integration testing
./hercules --dry-run .
./hercules --burndown --dry-run .
```

### Manual Testing Commands

```bash
# Basic functionality
./hercules --help
./hercules --version

# Core analyses
./hercules --burndown .
./hercules --burndown --burndown-people .
./hercules --couples .
./hercules --devs .

# With Python plotting
./hercules --burndown . | labours -m burndown-project
./hercules --burndown --pb . | labours -f pb -m burndown-project

# Performance testing
./hercules --burndown --hibernation-distance 10 /path/to/large/repo
```

### Validation Criteria

#### Functionality ✅

- [ ] All CLI commands work without errors
- [ ] All analysis types produce expected output
- [ ] Python integration works correctly
- [ ] Protocol Buffers format is compatible

#### Performance ✅

- [ ] Memory usage is reasonable (hibernation works)
- [ ] Analysis speed is comparable to original
- [ ] Large repositories can be processed

#### Compatibility ✅

- [ ] Output format matches original (YAML/PB)
- [ ] Labours plotting works with new output
- [ ] Result merging works correctly

## Risk Mitigation

### High Risk Items

1. **Burndown Architecture Changes**: The split from monolithic to modular design
   - **Mitigation**: Thorough side-by-side testing with original
2. **Module Path Migration**: Potential import path issues
   - **Mitigation**: Systematic verification of all imports

3. **Protocol Buffers Compatibility**: Schema changes
   - **Mitigation**: Binary compatibility testing

### Medium Risk Items

1. **Hibernation Feature**: Complex memory management
   - **Mitigation**: Memory profiling and stress testing
2. **New Analysis Types**: Code churn and line dump
   - **Mitigation**: Unit testing and validation

## Success Metrics

- [ ] **100% test suite passing**
- [ ] **All documented CLI flags working**
- [ ] **Python labours integration working**
- [ ] **Memory usage within acceptable bounds**
- [ ] **Output format compatibility maintained**
- [ ] **Performance within 10% of original**

### Phase 6: New Analysis Features (from "Dig the Diff" presentation)

These feature ideas were collected from the "Fehlende Metriken" and "Priorisierte
Verbesserungen" sections of the presentation analysis. Each feature is a new
`core.LeafPipelineItem` registered via `core.Registry.Register()` in `init()`.
Reference implementation pattern: `leaves/temporal_activity.go`.

#### 6.1 Bus-Factor@80% (Repo & per Subsystem)

Compute the smallest k such that the top-k owners cover ≥ 80% of living lines.
Ownership data is already tracked by `BurndownAnalysis` with `--burndown-people`;
this feature post-processes that data into an aggregate KPI.

- [x] **Go analysis** (`leaves/bus_factor.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "bus-factor"`
  - Require `identity.DependencyAuthor` and `linehistory.DependencyLineHistory`
  - In `Consume()`: accumulate per-file, per-author alive-line counts
  - In `Finalize()`: sort authors by ownership share, find smallest k where
    `Sum(Top-k) ≥ threshold` (default 80%, configurable via `--bus-factor-threshold`)
  - Emit per-tick time series (bus-factor over project lifetime)
  - Optional: per-directory/subsystem granularity using file path prefixes
- [x] **Protobuf schema** — add `BusFactorResults` message to `internal/pb/pb.proto`
  - Fields: `repeated int32 bus_factor_per_tick`, `map<string, int32> bus_factor_per_subsystem`
- [x] **YAML serialization** in `Serialize()`
- [x] **Python visualization** (`python/labours/modes/bus_factor.py`)
  - Gauge chart (current value) + time series plot
  - Register as labours mode `-m bus-factor`
- [x] **Tests** — table-driven tests in `leaves/bus_factor_test.go`

#### 6.2 Ownership Concentration Index (Gini / HHI)

Quantify how concentrated or distributed code ownership is, tracked over time.
Complementary to Bus-Factor: Gini=0 means perfectly equal, Gini=1 means one person
owns everything.

- [x] **Go analysis** (`leaves/ownership_concentration.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "ownership-concentration"`
  - Require same dependencies as Bus-Factor (6.1)
  - In `Finalize()`: compute Gini coefficient and/or HHI per tick
    - Gini: `1 - 2 * integral(Lorenz curve)`
    - HHI: `Sum(share_i²)` for all authors with share > 0
  - Configurable via `--concentration-metric gini|hhi|both`
- [x] **Protobuf schema** — add `OwnershipConcentrationResults` message
- [x] **Python visualization** — time series of Gini/HHI with change-point markers
- [x] **Tests**
- [ ] **Effort**: Low–Medium — same data sources as 6.1, math is straightforward

#### 6.3 Knowledge Diffusion

Track unique editors per file over time to identify single-contributor risk areas
and knowledge silos.

- [x] **Go analysis** (`leaves/knowledge_diffusion.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "knowledge-diffusion"`
  - Require `identity.DependencyAuthor`, `items.DependencyTreeChanges`,
    `items.DependencyTick`
  - In `Consume()`: for each changed file, record the set of unique authors
  - In `Finalize()`: per file emit `unique_editors_count`, `unique_editors_over_time`,
    `last_N_months_editors`; aggregate as distribution (histogram of files by editor count)
- [x] **Protobuf schema** — add `KnowledgeDiffusionResults` message
- [x] **Python visualization**
  - Lorenz curve of editor distribution across files
  - Distribution histogram of files by editor count
  - Top-N "knowledge silos" (files with fewest unique editors)
- [x] **Tests**
- [ ] **Effort**: Medium — needs per-file author tracking, moderate state

#### 6.4 Onboarding Ramp

Measure how quickly new contributors ramp up: time-to-first-change,
breadth-of-files in first N days, convergence to stable contribution patterns.

- [x] **Go analysis** (`leaves/onboarding.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "onboarding"`
  - Require `identity.DependencyAuthor`, `items.DependencyTick`,
    `items.DependencyLineStats`, `items.DependencyTreeChanges`
  - In `Consume()`: track per author: first commit tick, cumulative commits,
    cumulative files touched, cumulative lines changed; bucket into configurable
    windows (default: 7/30/90 days from first commit)
  - In `Finalize()`: per author emit onboarding metrics; compute cohort averages
    (group by join month)
  - Configurable: `--onboarding-windows "7,30,90"` (days), `--onboarding-meaningful-threshold 10`
    (min lines to count as "meaningful change")
- [x] **Protobuf schema** — add `OnboardingResults` message
- [ ] **Python visualization**
  - Cohort heatmap: rows = join month, columns = days since first commit, cells = cumulative activity
  - Per-author ramp curves (overlay or small multiples)
- [x] **Tests**
- [x] **Effort**: Medium — new per-author temporal tracking, cohort aggregation logic

#### 6.5 Hotspot Risk Score ✅

Combined per-file risk metric: `log(size) × churn × coupling_degree × ownership_concentration`.
Implemented as a self-contained real-time pipeline item that tracks all metrics incrementally.

- [x] **Go analysis** (`leaves/hotspot_risk.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "hotspot-risk"`
  - Require `items.DependencyLineStats`, `items.DependencyTreeChanges`,
    `identity.DependencyAuthor`, `items.DependencyTick`
  - In `Consume()`: per file track: current size (lines), change count (churn),
    set of co-changed files (coupling degree), set of authors (ownership concentration)
  - In `Finalize()`: compute composite score per file, rank, emit top-N
  - Configurable: `--hotspot-risk-top 20` (how many files to report),
    `--hotspot-risk-window 90` (days for churn window),
    weights for each factor via `--hotspot-risk-weights`
- [x] **Protobuf schema** — add `HotspotRiskResults` and `FileRisk` messages
- [x] **Python visualization** (`python/labours/modes/hotspot_risk.py`)
  - Bubble chart: x=churn, y=coupling, size=file size, color=ownership Gini
  - Ranked bar chart showing top-N files with component breakdown
  - Text summary table in console output
- [ ] **Tests** — Not yet implemented
- [x] **Effort**: Medium–High — Self-contained implementation with normalized scoring

#### 6.6 Refactoring Proxy (Move/Rename Rate)

Track the proportion of commits dominated by file renames/moves to distinguish
refactoring phases from feature work. Rename detection already exists in
`internal/plumbing/renames.go`.

- [ ] **Go analysis** (`leaves/refactoring_proxy.go`)
  - Implement `core.LeafPipelineItem` with `Flag() = "refactoring-proxy"`
  - Require `items.DependencyTreeChanges`, `items.DependencyTick`
  - Use existing `TreeDiff` output which already detects renames via go-git's
    `DiffTree` (similarity-based rename detection)
  - In `Consume()`: per commit, count renames/moves vs. total changes;
    classify commits as "refactoring-heavy" if rename ratio > threshold
  - In `Finalize()`: time series of rename rate, event markers for spikes
  - Configurable: `--refactoring-threshold 0.5` (rename ratio to classify as refactoring)
- [ ] **Protobuf schema** — add `RefactoringProxyResults` message
- [ ] **Python visualization**
  - Time series of rename rate overlaid with "Added vs Changed" for correlation
  - Event markers on burndown chart for major refactoring phases
- [ ] **Tests**
- [ ] **Effort**: Medium — rename detection exists, but interpretation logic is new

#### 6.7 Code Review Metrics (requires external API — deferred)

Cycle time, review latency, rework-after-review. These metrics are **not available
from Git alone** and require GitHub/GitLab API integration. Deferred until a
platform integration layer exists.

- [ ] **Design**: define API abstraction (`internal/platform/`) for GitHub/GitLab
  - Interface: `ListPullRequests()`, `GetReviews()`, `GetComments()`
- [ ] **Go analysis** (`leaves/code_review.go`)
  - Correlate PR merge commits with review metadata
  - Compute: time-to-first-review, review-to-merge latency, rework commits after approval
- [ ] **Protobuf schema** — add `CodeReviewResults` message
- [ ] **Python visualization** — cycle time distribution, review latency trends
- [ ] **Effort**: High — new external dependency, API rate limiting, auth handling
- [ ] **Status**: Deferred — design only until platform layer is available

### Phase 7: Tool & UX Improvements (from "Dig the Diff" analysis)

#### 7.1 Metrics Contract: Stable Output Schemas

Currently, JSON plot format is unspecified and depends on the Python implementation.
YAML output structure is implicit in each analysis's `Serialize()` method.
For automation, teaching, and third-party tooling, stable documented schemas are needed.

- [ ] **Document existing schemas** — extract current YAML/PB structure from each
      `Serialize()` implementation in `leaves/*.go` and write as reference docs
- [ ] **Freeze PB schema** — version `internal/pb/pb.proto` with semantic versioning;
      add `reserved` fields for removed items
- [ ] **JSON export mode** — add `--json` flag to `hercules` CLI that emits structured
      JSON directly (bypassing labours), with a documented JSON Schema per analysis
- [ ] **Schema validation** — add CI check that PB schema changes are backwards-compatible
- [ ] **Priority**: High | **Effort**: Medium

#### 7.2 Large-Repo Scaling Presets

The README documents several workarounds for large repos (disk backend, blacklisting,
language filter, hibernation, `--first-parent`) but users must discover and combine
them manually.

- [ ] **Implement `--preset` flag** in `cmd/hercules/root.go`
  - `--preset large-repo`: sets `--first-parent`, `--hibernation-distance 10`,
    `--burndown-hibernation-threshold 100`, `--burndown-hibernation-disk`,
    enables language filter for common generated files
  - `--preset quick`: disables couples/shotness, uses `--first-parent`,
    coarse granularity
  - Presets are overridable: explicit flags take precedence
- [ ] **Document scaling guide** — table of repo size thresholds (commits, files, branches)
      with recommended preset and expected memory/time
- [ ] **Priority**: High | **Effort**: Low

#### 7.3 One-Command Report Generation

The current workflow requires piping `hercules` into `labours` with format flags
and mode selection. For first-time users and teaching, a single command that
produces a complete report would lower the barrier significantly.

- [x] **Implement `hercules report` subcommand** in `cmd/hercules/`
  - Runs all enabled analyses, invokes labours internally, produces output directory
  - `hercules report --all -o ./report/ <repo>` generates all charts + summary
  - Output: directory with PNGs/SVGs + an `index.html` that embeds all charts
- [ ] **Alternative: `just report` recipe** in Justfile as a simpler first step
  - Shell script that chains hercules + labours with sensible defaults
  - Less integration effort, immediately usable
- [ ] **Priority**: Medium | **Effort**: Medium (subcommand) or Low (just recipe)

#### 7.4 Dependency Modernization

Babelfish (required for Shotness/UAST parsing) is abandoned. Tensorflow (required
for Couples embeddings and Sentiment) is heavy and complicates builds.

- [ ] **Replace Babelfish** for Shotness analysis
  - Evaluate tree-sitter as alternative AST parser (wide language support, active community)
  - Implement `internal/plumbing/uast_treesitter.go` behind same interface
  - Keep Babelfish as fallback via build tag `babelfish`
- [ ] **Modularize Tensorflow**
  - Couples embeddings: evaluate pure-Go alternatives (e.g., Gorgonia, or custom
    Swivel implementation without TF)
  - Sentiment: already behind `tensorflow` build tag — document this more prominently
  - Goal: `go build` without any tags produces a fully functional binary
    (couples without embeddings, no sentiment)
- [ ] **Priority**: Medium | **Effort**: High

#### 7.5 Improved Identity Resolution

Identity is the foundation for People Burndown, Ownership, Overwrites, and
Onboarding metrics. Current matching (`internal/plumbing/identity/people.go`)
uses name/email opportunistic matching + `.mailmap`. Misattributions silently
corrupt multiple downstream metrics.

- [ ] **Additional heuristics**
  - GitHub username resolution via commit trailers (`Co-authored-by:`)
  - Levenshtein/Jaro-Winkler fuzzy matching for name variations
  - Configurable confidence threshold for automatic merges
- [ ] **Identity audit report** (`--identity-audit`)
  - Output: list of all detected identities, merge decisions with confidence,
    ambiguous cases flagged for manual review
  - Format: table or JSON for programmatic consumption
- [ ] **Interactive identity editor** — generate `people-dict` template from
      auto-detected identities for manual refinement
- [ ] **Priority**: Medium | **Effort**: Medium

#### 7.6 Sentiment: Mark as Experimental

Sentiment analysis uses a general-purpose BiDiSentiment model on code comments,
which the README itself warns about ("don't expect too much"). Making this
limitation visible in the tool prevents misinterpretation.

- [ ] **CLI output** — prefix Sentiment results with `[EXPERIMENTAL]` marker
- [ ] **`--help` text** — add caveat to `--sentiment` flag description
- [ ] **Labours plot** — add subtitle "Experimental — general-purpose model, not
      validated for code comments" to sentiment charts
- [ ] **Priority**: Low | **Effort**: Low

## Timeline Estimate

- **Phase 1**: 1-2 days (Core infrastructure) ✅
- **Phase 2**: 2-3 days (Analysis validation) ✅
- **Phase 3**: 1-2 days (Integration testing)
- **Phase 4**: 1 day (Comparison testing)
- **Phase 5**: 1 day (Documentation)
- **Phase 6**: New analysis features (per feature, see effort estimates above)
  - 6.1 Bus-Factor: 1-2 days
  - 6.2 Ownership Concentration: 1-2 days
  - 6.3 Knowledge Diffusion: 2-3 days
  - 6.4 Onboarding Ramp: 3-4 days
  - 6.5 Hotspot Risk Score: 4-5 days
  - 6.6 Refactoring Proxy: 2-3 days
  - 6.7 Code Review Metrics: deferred
- **Phase 7**: Tool & UX improvements
  - 7.1 Metrics Contract: 3-4 days
  - 7.2 Scaling Presets: 1 day
  - 7.3 Report Generation: 1-2 days (just recipe) or 3-4 days (subcommand)
  - 7.4 Dependency Modernization: 5+ days
  - 7.5 Identity Resolution: 3-4 days
  - 7.6 Sentiment Labeling: < 1 day

## Progress Summary

### Completed Phases

- ✅ **Phase 1**: Core Infrastructure Validation (100%)
- ✅ **Phase 2**: Analysis Feature Validation (100%)
- 🔄 **Phase 3**: Integration & Compatibility Testing (67% - 3.1 & 3.2 completed)
  - ✅ 3.1 CLI Interface Validation
  - ✅ 3.2 Python Integration Testing
  - ⏳ 3.3 Performance & Memory Testing (next)

### Next Steps

1. ✅ ~~Phase 1.1: Fix remaining compilation errors~~
2. ✅ ~~Complete test suite validation~~
3. ✅ ~~Phase 2: Analysis feature validation~~
4. ✅ ~~Phase 3.1: CLI interface validation~~
5. ✅ ~~Phase 3.2: Python Integration Testing~~
6. **Current**: Phase 3.3: Performance & Memory Testing
7. Document any issues or deviations found
8. Create final migration documentation
