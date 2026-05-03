# Hercules — Roadmap (Forward Plan)

This document is intentionally **forward-looking**: it tracks what remains, what is deferred, and why.
Completed items are listed only when they clarify the current state or unblock pending decisions.

## Goals (definition of “done”)

- A default `go build ./cmd/hercules` produces a useful binary **without legacy parser services, TensorFlow, or cgo**.
- The tool scales to large repositories with documented presets and verified memory behavior.
- Outputs are stable and documented (YAML/PB now; optional JSON later) and can be consumed by automation.
- Remaining “partial” analyses are completed to a shippable level (tests + labours UX).
- Documentation matches reality: install, flags, build tags, and limitations are clear.

## Priority model

- **P0 (Release blockers)**: buildability, dependency modernization, correctness regressions, broken-by-default features.
- **P1 (Scale & contracts)**: large-repo usability, output schemas, compatibility checks.
- **P2 (Finish partial features)**: onboarding labours, hotspot risk tests, report UX polish.
- **P3 (Nice-to-have / research)**: new heuristics, platform integrations, optional optimizations.

## Current remaining focus (short list)

1. ~~**Milestone 1 — Dependency modernization (drop cgo)**~~ — done. The default
  build is now cgo-free: pure-Go LZ4 is the default, `gotreesitter` replaced
  the old bindings, `src-d/imports` was removed, and CI/docs cover
  `CGO_ENABLED=0` builds and cross-compiles.
2. **Complete larger P1/P2 milestones**
   - Scaling presets + large-repo validation (Milestone 2).
   - Output schema contracts and compatibility checks (Milestone 3).
   - Onboarding/hotspot/report polish (Milestone 4).

## Milestones

### Milestone 1 — Dependency modernization (tree-sitter first) (P0)

Status: **complete**.

Milestone 1 is now here only to record the resulting baseline.

- [x] **cgo-free default build**: `CGO_ENABLED=0 go build ./cmd/hercules` succeeds, cross-compiles are verified for linux/{amd64,arm64}, windows/amd64, and darwin/arm64, and CI runs both the matrix and a `CGO_ENABLED=0` test job.
- [x] **Legacy parser/UAST cleanup**: structural analyses are tree-sitter-only, Babelfish-backed surfaces are gone, and `--dump-uast-changes` plus diff refinement now use the replacement tree-sitter implementations.
- [x] **Dependency simplification**: `internal/rbtree` uses pure-Go LZ4 by default (`-tags cgo_lz4` remains opt-in), `internal/plumbing/ast` uses `github.com/odvcencio/gotreesitter`, and `src-d/imports` was replaced by the in-tree implementation under `internal/plumbing/imports/lang`.
- [x] **Optional features remain optional**: TensorFlow-backed sentiment stays behind the `tensorflow` build tag, and non-`tensorflow` builds return explicit guidance instead of failing ambiguously.

### Milestone 2 — Large-repo scaling & operational safety (P1)

Why: even a correct tool fails “in practice” if it OOMs or needs a handbook of flags.

- [ ] **Performance & memory validation on a large repository**
  - [ ] Run a "big repo" benchmark suite (kernel or similarly large history).
  - [ ] Record baseline runtime + peak RSS for a small set of representative analyses.
  - [x] Validate hibernation paths (in-memory vs disk) and confirm they prevent OOM.
    - [x] BurndownAnalysis now implements `HibernateablePipelineItem` (gob+flate serialization with optional disk spill).
    - [x] LineHistory hibernation already supported; `large-repo` preset enables it by default.
  - [ ] Acceptance: a documented command set completes without OOM and with reproducible results.

- [x] **Tree-sitter memory reduction (follow-up to `--diff-refine-max-file-size`)**
  - [x] **Phase 1 – Line-count threshold** (quick win): add `--diff-refine-max-lines` alongside
        the existing byte-size gate (`--diff-refine-max-file-size`). Files exceeding N lines
        (suggested default: 5 000) skip refinement. Handles minified/generated files where byte
        size alone is insufficient. Change is ~10 lines in `internal/plumbing/diff.go`.
  - [x] **Phase 2 – Range-limited parsing**: `ExtractNamedNodesInRanges` (added in
        `internal/plumbing/ast/treesitter.go`) drives a `*sitter.Parser` with
        `SetIncludedRanges()` so the parse only touches the line intervals consulted
        by the node-density heuristic. Routing in `extractNodesForRefinement`
        (`internal/plumbing/diff.go`) selects the strategy per-file:
    - **Size gate** (`refineRangeMinLines = 1000`): files with fewer than 1 000
      new-file lines always full-parse — no perf gain to chase, and avoids any
      possibility of drift on small files.
    - **Coverage threshold** (`refineCoverageThreshold = 0.5`): if padded ranges
      cover ≥ 50 % of the new file, fall back to full parse.
    - **Range padding** (`refineRangePadding = 4`): each computed range is
      extended by 4 lines on either side before being handed to tree-sitter,
      giving the grammar enough surrounding tokens to resolve context-sensitive
      constructs the same way it would in a full-file parse.
    - **Mode flag** (`--diff-refine-mode=auto|full|range`, default `auto`):
      `full` restores bit-identical pre-Phase-2 output; `range` always uses
      included-ranges parsing when at least one range exists; `auto` applies the
      heuristics above.
  - **Benchmark** (5 000-line synthetic Go file, single 10-line edit, amd64 i7-1255U):
    - Full-file parse: 40.8 ms · 17.3 MB · 158 k allocs
    - Range-limited (10 lines): 0.16 ms · 162 KB · 373 allocs
    - ≈ 250× faster, ≈ 107× less memory, ≈ 420× fewer allocations.
  - **Output stability**: with the size gate + padding active, a
    `hercules --burndown --first-parent` smoke run on this repo is
    bit-identical to the pre-Phase-2 baseline (only `hash` and `run_time`
    metadata fields differ). End-to-end runtime drops from 17.8 s to 3.3 s.
  - [x] Acceptance: peak RSS on files exercised by the new path is measurably lower
        without `--no-diff-refine` (benchmark above). Big-repo RSS validation is
        tracked under "Performance & memory validation on a large repository" above.

- [x] **Add scaling presets (`--preset`)**
  - [x] Implement presets with clear precedence rules (explicit flags override preset defaults).
  - [x] Provide at least:
    - `large-repo` (first-parent + hibernation threshold 200k + disk spill + granularity/sampling 30)
    - `quick` (fast "overview" run via `--head`)
  - [ ] Acceptance: the README recommends presets and users can get a first result without tuning.

- [ ] **(Optional) Validate “advanced” pipeline features**
  - [ ] Merge tracking correctness tests.
  - [ ] Plugin compatibility smoke test.
  - [ ] Acceptance: documented as supported or explicitly marked experimental.

### Milestone 3 — Output contracts & compatibility checks (P1)

Why: stable tooling needs stable schemas; otherwise every downstream consumer is fragile.

- [x] **Document existing YAML/PB schemas**
  - [x] Extract the effective YAML structure from each `Serialize()` implementation and write reference docs.
  - [x] Provide one example payload per analysis (small and readable).
  - [x] Acceptance: a reader can write a parser without reading Go code.
  - [x] Reference: `docs/SCHEMAS.md`.

- [ ] **Freeze and version the PB schema**
  - [ ] Introduce a schema version policy (semantic versioning).
  - [ ] Use `reserved` fields for removals.
  - [ ] Acceptance: PB changes are intentional and reviewed as compatibility changes.

- [ ] **Add CI guardrails for schema changes**
  - [ ] Add a check that flags incompatible PB changes.
  - [ ] Acceptance: breaking changes require an explicit version bump + changelog entry.

- [ ] **(Optional) JSON export mode**
  - [ ] Add `--json` output for direct consumption (not via labours).
  - [ ] Provide JSON Schemas per analysis.
  - [ ] Acceptance: JSON output is documented and stable.

### Milestone 4 — Close “partial” features (P2)

- [ ] **Onboarding ramp: labours visualization**
  - [ ] Define the chart input mapping from `OnboardingResults`.
  - [ ] Implement `python/labours/modes/onboarding.py` (cohort heatmap).
  - [ ] Add per-author ramp plot (overlay or small multiples).
  - [ ] Register mode so `labours -m onboarding` works.
  - [ ] Add one usage example + one screenshot in docs.
  - [ ] Acceptance: one command produces a clear onboarding chart for a real repo.

- [ ] **Hotspot risk score: add deterministic Go tests**
  - [ ] Add `leaves/hotspot_risk_test.go` with fixed fixture data.
  - [ ] Verify ranking (top-N and tie-handling), factor scaling, and windowing.
  - [ ] Verify YAML/PB serialization shape.
  - [ ] Acceptance: `go test ./leaves -run HotspotRisk` is stable and meaningful.

- [ ] **One-command reports: finish the “easy path”**
  - [ ] Add a `just report` recipe if it still adds value alongside `hercules report`.
  - [ ] Acceptance: first-time users can generate a report without knowing labours flags.

### Milestone 5 — Identity correctness & auditability (P2/P3)

Why: identity errors silently corrupt multiple downstream metrics.

- [ ] **Additional heuristics**
  - [ ] GitHub username resolution via commit trailers (`Co-authored-by:`).
  - [ ] Fuzzy matching for name variants (Levenshtein or Jaro-Winkler).
  - [ ] Configurable confidence threshold for automatic merges.

- [ ] **Identity audit report (`--identity-audit`)**
  - [ ] Emit all detected identities and merge decisions with confidence.
  - [ ] Flag ambiguous cases for manual review.
  - [ ] Output format: JSON (preferred) plus optional table.
  - [ ] Acceptance: users can find and fix suspicious merges without reading code.

- [ ] **Generate `people-dict` template**
  - [ ] Produce a template file from detected identities for manual refinement.
  - [ ] Acceptance: identity refinement becomes an explicit workflow step.

### Milestone 6 — Documentation & release hygiene (P2)

- [ ] **Update README / docs to match reality**
  - [ ] Installation steps (including optional build tags).
  - [ ] Go version requirements.
  - [ ] Example commands updated to include presets.
  - [ ] Limitations for experimental/optional analyses (Sentiment, embeddings).

- [ ] **Code quality gates**
  - [ ] `go fmt ./...`
  - [ ] `go vet ./...` (fix only relevant warnings)
  - [ ] Trim dead code and stale docs.

- [ ] **Release preparation**
  - [ ] Confirm `--version` and version policy.
  - [ ] Add a short migration guide for users of the old upstream.
  - [ ] Acceptance: a tagged release is buildable and documented.

## Test & validation matrix (what to run while working)

```bash
# Unit tests
just test

# Focused packages (run while iterating)
go test ./internal/core
go test ./internal/plumbing
go test ./leaves

# Smoke runs
./hercules --dry-run .
./hercules --preset quick .

# Scaling smoke (use an actual large repo path)
./hercules --preset large-repo /path/to/large/repo
```

## Deferred / not planned (with rationale)

- **Code review metrics**: requires GitHub/GitLab API integration; Git history alone is insufficient.
  - Revisit when an `internal/platform/` abstraction exists.

- **Remote repository cloning support (HTTPS/SSH)**: nice to have, but not core to correctness; can be handled externally by cloning locally.
  - Revisit after scaling presets and schema contracts are solid.

- **Caching**: postpone until real-world perf numbers exist; premature caching risks wrong-by-default behavior.
  - Revisit after Milestone 2 benchmarks.

## Low-effort correctness/UX fixes (small wins)

- [ ] **Sentiment: mark as experimental everywhere**
  - [ ] CLI: prefix outputs with `[EXPERIMENTAL]`.
  - [ ] `--help`: include a caveat.
  - [ ] Labours: add subtitle warning on charts.
