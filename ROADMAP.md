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

1. **Finish the remaining P1 contract work**: PB schema versioning and CI
   compatibility checks (Milestone 3).
2. **Close P2 usability gaps**: onboarding labours mode, hotspot-risk tests, and
   report easy path (Milestone 4).
3. **Prepare release hygiene**: documentation, quality gates, version policy, and
   migration notes (Milestone 6).
4. **Defer or explicitly mark experimental work**: advanced pipeline support,
   identity audit flows, JSON export, and sentiment caveats.

## Milestones

### Milestone 1 — Dependency modernization (tree-sitter first) (P0)

Status: **complete**.

Baseline now in place: the default build is cgo-free, structural analyses are
tree-sitter-only, legacy Babelfish/UAST surfaces are gone, `src-d/imports` was
replaced by in-tree import handling, pure-Go LZ4 is the default, and
TensorFlow-backed sentiment remains behind the `tensorflow` build tag.

### Milestone 2 — Large-repo scaling & operational safety (P1)

Status: **core complete; optional validation remains**.

Why: even a correct tool fails “in practice” if it OOMs or needs a handbook of flags.

Completed baseline:

- Large-repo benchmark workflow exists as `scripts/bench-large-repo.sh` and
  `just bench-large-repo <REPO_PATH>`, with CPython baseline results documented
  in [docs/SCALING.md](docs/SCALING.md).
- Hibernation support covers LineHistory and BurndownAnalysis, and the
  `large-repo` preset enables conservative spill behavior by default.
- Diff refinement now has byte, line-count, and range-limited tree-sitter gates;
  smoke output stayed stable while synthetic large-file parsing became
  materially cheaper.
- `--preset quick` and `--preset large-repo` provide documented starting points,
  with explicit flags overriding preset defaults.

Remaining:

- [ ] **Validate or demote advanced pipeline features**
  - [ ] Add merge-tracking correctness tests that exercise non-linear histories.
  - [ ] Add a plugin compatibility smoke test that loads a minimal plugin.
  - [ ] Decide whether each advanced path is supported or experimental, then
        document that status in README/help text.
  - [ ] Acceptance: advanced pipeline behavior is either covered by tests or
        explicitly documented as experimental.

### Milestone 3 — Output contracts & compatibility checks (P1)

Status: **open**.

Why: stable tooling needs stable schemas; otherwise every downstream consumer is fragile.

Completed baseline: existing YAML/PB output structures and examples are
documented in [docs/SCHEMAS.md](docs/SCHEMAS.md).

- [ ] **Freeze and version the PB schema**
  - [x] Define the schema version policy.
  - [ ] Define where the current schema version is emitted.
  - [x] Document compatible vs. breaking PB changes.
  - [ ] Reserve removed field numbers/names in `.proto` files.
  - [x] Add a changelog entry format for schema changes.
  - [ ] Acceptance: PB changes can be reviewed against a written compatibility policy.

- [ ] **Add CI guardrails for schema changes**
  - [x] Add a generated descriptor or equivalent schema snapshot.
  - [x] Add a CI check that flags PB schema changes.
  - [ ] Require an explicit version bump and changelog entry for breaking changes.
  - [ ] Acceptance: accidental breaking PB changes fail CI and intentional changes
        point to the version/changelog decision.

- [ ] **Decide on JSON export mode**
  - [ ] Confirm whether downstream users need native JSON or whether YAML/PB is enough.
  - [ ] Add `--json` output for direct consumption (not via labours).
  - [ ] Provide JSON Schemas per analysis.
  - [ ] Acceptance: either JSON is documented and stable, or the roadmap records why it is deferred.

### Milestone 4 — Close “partial” features (P2)

Status: **open**.

- [x] **Onboarding ramp: labours visualization**
  - [x] Define the chart input mapping from `OnboardingResults`.
  - [x] Implement `python/labours/modes/onboarding.py` with a cohort heatmap.
  - [x] Add a per-author ramp plot.
  - [x] Register mode so `labours -m onboarding` works.
  - [x] Add one usage example and one screenshot in docs.
  - [x] Acceptance: one command produces a clear onboarding chart for a real repo.

- [x] **Hotspot risk score: add deterministic Go tests**
  - [x] Add `leaves/hotspot_risk_test.go` with fixed fixture data.
  - [x] Verify ranking (top-N and tie-handling), factor scaling, and windowing.
  - [x] Verify YAML/PB serialization shape.
  - [x] Acceptance: `go test ./leaves -run HotspotRisk` is stable and meaningful.

- [x] **One-command reports: finish the “easy path”**
  - [x] Audit `hercules report` and current labours invocation flow.
  - [x] Add a `just report` recipe only if it removes real first-run friction.
  - [x] Document the recommended report command in README.
  - [x] Acceptance: first-time users can generate a report without knowing labours flags.

### Milestone 5 — Identity correctness & auditability (P2/P3)

Status: **complete**.

Why: identity errors silently corrupt multiple downstream metrics.

- [x] **Improve identity merge heuristics**
  - [x] GitHub username resolution via commit trailers (`Co-authored-by:`).
  - [x] Fuzzy matching for name variants (Levenshtein or Jaro-Winkler).
  - [x] Configurable confidence threshold for automatic merges.
  - [x] Acceptance: heuristic decisions are deterministic and covered by fixtures.

- [x] **Identity audit report (`--identity-audit`)**
  - [x] Emit all detected identities and merge decisions with confidence.
  - [x] Flag ambiguous cases for manual review.
  - [x] Output format: JSON (preferred); table output remains unnecessary unless users ask for it.
  - [x] Acceptance: users can find and fix suspicious merges without reading code.

- [x] **Generate `people-dict` template**
  - [x] Produce a template file from detected identities for manual refinement.
  - [x] Acceptance: identity refinement becomes an explicit workflow step.

### Milestone 6 — Documentation & release hygiene (P2)

Status: **open**.

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
  - [ ] Confirm release artifacts build with the default cgo-free configuration.
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

- **Caching**: still deferred until repeated real-world workloads show a stable
  bottleneck; premature caching risks wrong-by-default behavior.
  - Revisit after schema contracts and release hygiene are in place.

## Low-effort correctness/UX fixes (small wins)

- [ ] **Sentiment: mark as experimental everywhere**
  - [ ] CLI: prefix outputs with `[EXPERIMENTAL]`.
  - [ ] `--help`: include a caveat.
  - [ ] Labours: add subtitle warning on charts.
