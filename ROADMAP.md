# Hercules — Roadmap (Forward Plan)

This document is intentionally **forward-looking**: it tracks what remains, what is deferred, and why.
Completed items are listed only when they clarify the current state or unblock pending decisions.

## Goals (definition of “done”)

- A default `go build ./cmd/hercules` produces a useful binary **without legacy parser services or TensorFlow**.
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

1. **Complete larger P1/P2 milestones**
   - Scaling presets + large-repo validation.
   - Output schema contracts and compatibility checks.
   - Onboarding/hotspot/report polish.

## Milestones

### Milestone 1 — Dependency modernization (tree-sitter first) (P0)

Status: **complete**.

- [x] **Tree-sitter migration completed for structural analyses**
  - [x] `shotness` moved to tree-sitter-only implementation.
  - [x] `research/typos-dataset` moved to tree-sitter-only implementation.
  - [x] `sentiment` comment extraction moved to tree-sitter path (`-tags tensorflow` build).
  - [x] Legacy parser-specific plumbing and tests removed from the codebase.

- [x] **Modularize TensorFlow usage (keep default build light)**
  - [x] Ensure Couples and Sentiment behave sensibly when built without TensorFlow:
    - [x] Couples: still produces usable non-embedding output (or a clear “feature unavailable” message).
    - [x] Sentiment: remains behind build tag and is explicitly described as experimental.
    - [x] Non-`tensorflow` builds now provide explicit runtime/build-tag guidance when `--sentiment` is requested.
  - [x] Evaluate a pure-Go replacement only if needed (do not block the milestone on this).
    - [x] Decision: not required for Milestone 1; keep current optional TensorFlow path and revisit only if usage data shows a need.
  - [x] Acceptance: default build does not require TensorFlow and doesn’t crash when relevant flags are used.
    - [x] Verified (2026-02-15): `go build ./cmd/hercules` (default), `--couples --head` succeeds, `--sentiment --head` exits with explicit rebuild guidance (`rebuild with -tags tensorflow`).

- [x] **Finish legacy UAST surface cleanup**
  - Status: **legacy surfaces removed or replaced in this fork**.
  - [x] Legacy UAST output paths transitioned in this fork:
    - [x] Babelfish-backed `--dump-uast-changes` removed.
    - [x] Tree-sitter replacement for `--dump-uast-changes` implemented.
    - [x] `FileDiffRefiner` (UAST-based) removed from pipeline.
    - [x] Add a short migration note in docs/changelog describing removal and impact.
  - [x] Define replacement strategy for removed functionality:
    - [x] Decide whether a tree-sitter-backed commit-level AST dump mode is needed.
      - [x] Decision: yes.
    - [x] Design and implement a replacement for `--dump-uast-changes` with tests.
      - [x] Implemented via `UASTChangesSaver` tree-sitter dump mode.
    - [x] Decide whether a tree-sitter-based diff-refinement pass should be added to `FileDiff`.
      - [x] Decision: yes. Add a tree-sitter-backed refinement pass directly in `FileDiff` (enabled by default, opt-out via `--no-diff-refine`).
    - [x] Implement refinement pass and acceptance tests for human-readable diff quality.
      - [x] Implemented in `internal/plumbing/diff.go` with deterministic unit tests.
  - [x] Decide whether protobuf messages named `UAST*` should be renamed or kept for compatibility.
    - [x] Decision: removed from schema for this fork (intentional protobuf compatibility break).
  - [x] Decide whether `--shotness-xpath-*` compatibility flags should be removed or kept as ignored aliases.
    - [x] Decision: removed from CLI/runtime in this fork.
  - [x] Remove stale docs/examples that still imply XPath/UAST workflows.
  - [x] Once replacement decisions are finalized, update README migration notes with the final replacement guidance.

### Milestone 2 — Large-repo scaling & operational safety (P1)

Why: even a correct tool fails “in practice” if it OOMs or needs a handbook of flags.

- [ ] **Performance & memory validation on a large repository**
  - [ ] Run a “big repo” benchmark suite (kernel or similarly large history).
  - [ ] Record baseline runtime + peak RSS for a small set of representative analyses.
  - [ ] Validate hibernation paths (in-memory vs disk) and confirm they prevent OOM.
  - [ ] Acceptance: a documented command set completes without OOM and with reproducible results.

- [ ] **Add scaling presets (`--preset`)**
  - [ ] Implement presets with clear precedence rules (explicit flags override preset defaults).
  - [ ] Provide at least:
    - `large-repo` (first-parent + hibernation + practical defaults)
    - `quick` (fast “overview” run)
  - [ ] Acceptance: the README recommends presets and users can get a first result without tuning.

- [ ] **(Optional) Validate “advanced” pipeline features**
  - [ ] Merge tracking correctness tests.
  - [ ] Plugin compatibility smoke test.
  - [ ] Acceptance: documented as supported or explicitly marked experimental.

### Milestone 3 — Output contracts & compatibility checks (P1)

Why: stable tooling needs stable schemas; otherwise every downstream consumer is fragile.

- [ ] **Document existing YAML/PB schemas**
  - [ ] Extract the effective YAML structure from each `Serialize()` implementation and write reference docs.
  - [ ] Provide one example payload per analysis (small and readable).
  - [ ] Acceptance: a reader can write a parser without reading Go code.

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
