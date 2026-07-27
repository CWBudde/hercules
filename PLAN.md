# Hercules Audit Remediation Plan

Status: proposed  
Scope: findings from the July 2026 whole-repository audit  
Goal: make Hercules safe to run, trustworthy for its advertised metrics, deterministic, and
reliable in automation before expanding its feature set.

## Why this plan exists

The repository has a strong pipeline abstraction and good unit coverage in the mature core,
plumbing, and leaf packages. The audit nevertheless found several classes of problems that are
not adequately represented by the current green test suite:

- a user-supplied cache path can delete unrelated data;
- the published GitHub Action interpolates inputs into shell source;
- the direct `go-git` dependency is behind multiple security fixes;
- several advertised analyses produce incorrect results with real pipeline-shaped data;
- CLI and renderer failures can still exit successfully;
- malformed result files can panic or trigger excessive allocation;
- graph resolution and some serialized output are nondeterministic;
- important replay, merge, and empty-input paths are incomplete or unsafe;
- some large-repository paths have quadratic or worse behavior;
- CI checks new lint debt but does not adequately expose existing correctness debt.

This document converts those findings into ordered, testable work. Historical roadmap items remain
out of scope unless a task below explicitly requires them.

## Priority definitions

- **P0 — safety release blocker:** potential data loss, code execution, known reachable
  vulnerabilities, or false success in automation.
- **P1 — correctness release blocker:** output can be materially wrong, corrupted, silently
  incomplete, or nondeterministic.
- **P2 — robustness and scale:** malformed input, resource exhaustion, concurrency isolation, or
  severe performance problems.
- **P3 — maintainability and operations:** debt that increases future defect risk or makes releases
  difficult to reproduce.

## Working rules

1. Fix observable behavior before refactoring architecture.
2. Add a failing regression test before or with each fix.
3. Prefer production-shaped integration fixtures over hand-built dependency maps.
4. Return typed errors across package boundaries; do not add new `panic`, `log.Fatal`, or
   message-substring error classification.
5. Keep result schema compatibility explicit. Any semantic or wire-format change must update
   `docs/SCHEMA_CHANGELOG.md` and the schema snapshot where applicable.
6. Keep each pull request narrowly reviewable. Do not combine metric corrections with unrelated
   formatting or broad renames.
7. Preserve the supported cgo-free build throughout:

   ```bash
   CGO_ENABLED=0 go test ./...
   CGO_ENABLED=0 go vet ./...
   ```

8. Benchmark before and after performance work; do not accept a rewrite based only on asymptotic
   claims.

## Completion gates

The remediation is complete when all of the following are true:

- no ordinary CLI argument can recursively delete an unrelated existing directory;
- reusable Action inputs cannot alter shell syntax;
- `govulncheck ./...` reports no reachable known vulnerability in supported default features;
- all advertised default analyses have end-to-end semantic tests using real upstream pipeline
  items;
- repeated test runs produce identical plans, metadata, serialized results, and artifact names;
- hard analysis, render, merge, parse, and output failures return a nonzero process status;
- malformed YAML/PB input returns bounded, descriptive errors rather than panicking;
- a representative large-repository benchmark stays within documented memory and time budgets;
- normal pull requests run unit, race-targeted, schema, lint, security, and representative visual
  checks;
- the full supported test suite passes at least 20 consecutive times.

## Phase 0 — Establish a reproducible baseline

Priority: P0  
Purpose: make later changes measurable and prevent the known flaky test from hiding new failures.

### BASE-01: Capture current behavior

Status: in progress

Baseline report: [`docs/audits/2026-07-25-base-01.md`](docs/audits/2026-07-25-base-01.md)

- Record the current commit, Go version, operating system, and cgo setting in the remediation PR.
- Save baseline results for:

  ```bash
  CGO_ENABLED=0 go test -count=1 ./...
  CGO_ENABLED=0 go test -count=20 ./internal/toposort
  CGO_ENABLED=0 go test -race ./internal/core ./internal/plumbing/... ./leaves/...
  CGO_ENABLED=0 go vet ./...
  CGO_ENABLED=0 golangci-lint run --config ./.golangci.toml --modules-download-mode=mod
  ```

- Capture package coverage for core, plumbing, leaves, CLI, readers, and renderer modes.
- Add small integration fixtures that use the actual pipeline lifecycle:
  `Configure → ConfigureUpstream → Initialize → Consume → Finalize → Serialize`.

Acceptance criteria:

- [ ] baseline output is attached to or summarized in the first remediation PR;
- [x] known failures are isolated in named tests and are not converted into blanket skips;
- [x] all later phases can compare against a stable benchmark and test command set.

## Phase 1 — Eliminate immediate safety and automation hazards

Priority: P0

### SAFE-01: Make remote cache handling non-destructive

Status: completed 2026-07-25
Verification: [`docs/audits/2026-07-25-safe-01.md`](docs/audits/2026-07-25-safe-01.md)

Implemented marker-verified managed caches, explicit forced replacement, protected-path rejection,
and temporary-sibling cloning with atomic installation. Cross-platform tests cover managed,
unrelated, protected, failed-clone, and failed-replacement targets; unrelated data is never
recursively removed.

### SEC-01: Remove shell injection from the composite GitHub Action

Status: completed 2026-07-24

Moved Action inputs through quoted environment values and argv-safe JSON array parsing, without
`eval`; chart failures now fail the step unless best-effort behavior is explicitly requested.
Hostile-input fixtures cover shell metacharacters, substitutions, redirects, newlines, Unicode,
empty arrays, and multiple flags without executing caller-controlled syntax.

### SEC-02: Upgrade and continuously audit dependencies

Status: completed 2026-07-25

Verification report: [`docs/audits/2026-07-25-sec-02.md`](docs/audits/2026-07-25-sec-02.md)

Upgraded `go-git`, `go-billy`, and related transport dependencies to patched versions; added
default-build `govulncheck` CI, grouped dependency updates, and a documented update policy.
Transport, remote-clone, Siva, submodule, authentication, and plugin coverage passes, with no
reachable known vulnerability in the supported default build.

### REL-01: Make hard CLI failures return nonzero

Status: completed 2026-07-25

Verification report: [`docs/audits/2026-07-25-rel-01.md`](docs/audits/2026-07-25-rel-01.md)

Render/report/combine paths now aggregate typed warnings and hard errors, write diagnostics to
stderr, validate every input, preserve only explicitly optional missing analyses as warnings, and
return nonzero for hard or output failures. Tests cover partial multi-mode rendering, malformed
and mixed inputs, unwritable outputs, and broken pipes so automation can distinguish success,
warning-only success, and failure.

## Phase 2 — Restore metric correctness

Priority: P1  
Policy: until an analysis passes its end-to-end tests, remove it from default report presets or mark
it experimental in help and documentation.

### METRIC-01: Rebuild Hotspot Risk on correct pipeline contracts

Status: completed 2026-07-25

Hotspot Risk now uses validated duration windows, real path-keyed upstream statistics, current
line-history ownership, explicit rename behavior, and a documented weighted normalized score in
which zero weights disable factors. Production-shaped fixtures verify hand-calculated rankings
across multiple authors/ticks, renames, deletions, binary files, absent coupling, and disabled
factors.

### METRIC-02: Correct Onboarding time and window semantics

Status: completed 2026-07-25

Onboarding now derives cohorts from each author's actual first commit timestamp and selects
duration-based snapshots only at or before the documented window boundary. Tests cover calendar
months, time zones, sub-day/non-divisor/multi-day ticks, missing in-window commits, exact
boundaries, and PB/YAML rendering parity.

### METRIC-03: Correct Bus Factor and Ownership Concentration snapshots

Status: completed 2026-07-25

Bus Factor and Ownership Concentration now share immutable tick-aligned ownership snapshots and
use exact threshold comparisons; final-tick and empty-repository behavior is explicit. Fixtures
cover ownership transfers, file-size changes, small totals, same-tick commits, empty/single-author
repositories, and subsystem/global accounting.

### METRIC-04: Preserve File History across renames

Status: completed 2026-07-25

Renames now move complete file history—deduplicated destination-first hashes and per-author line
statistics—with deterministic collision merging. Rename chains, rename-plus-edit, collisions,
fork/merge, empty state, and YAML/PB round trips conserve the same history.

### METRIC-05: Fix Couples merging

Status: completed 2026-07-25

Couples merging now maps file rows through file identities, validates every matrix/index shape with
typed integrity errors, and preserves empty/deserialized merge identity. Hand-computed unrelated
people/file fixtures and three-way associativity tests prove deterministic file and people
matrices.

### METRIC-06: Define and implement real `couples-shotness`

Status: completed 2026-07-25

`couples-shotness` now computes a symmetric entity matrix as the dot product of aligned
entity-indexed co-occurrence profiles, with squared profile norms on the diagonal and positive
distinct pairs in rankings. Heatmaps and top pairs share one validated matrix; hand-calculated,
empty, single-entity, malformed, overflow, ambiguous-label, and YAML/PB parity fixtures pass.

### METRIC-07: Preserve distinct structural entities in Shotness

Status: completed 2026-07-25

Shotness identities now combine normalized source path, AST kind, and qualified lexical name
(including receiver/enclosing scope), excluding coordinates. Line moves and whole-file renames
preserve identity, while structural renames and individual cross-file moves start new identities;
same-named/nested entities and YAML/PB round trips retain distinct counters and coupling.

### METRIC-08: Correct filter-boundary transitions

Status: completed 2026-07-25

TreeDiff now filters old and new paths independently: included→excluded becomes deletion,
excluded→included insertion, included→included modification, and excluded→excluded omission.
Blacklist, vendor, regex, and language filters share the rule; language errors are returned
without advancing state, and initial-tree/retry behavior is covered.

### METRIC-09: Finish and validate Code Churn semantics

Status: completed 2026-07-25

Code Churn now has an explicit experimental contract for bounded awareness/memorability,
30/180-tick decay, reinforcement, self/other deletion effects, same-tick ordering, and deletion
horizon behavior in help and `docs/SCHEMAS.md`. Hand-calculated multi-tick fixtures, deterministic
delete history, overflow validation, and YAML/PB round trips replace the placeholder semantics.

### METRIC-10: Find and eliminate negative burndown balances

Status: completed 2026-07-25

Burndown interpolation now proportionally lowers existing cohorts when an aggregate decreases
inside a forming age band, eliminating the source of negative balances. Global/file/person/
repository matrices are validated with typed contextual errors at finalization, merge, and
serialization; lifecycle fixtures cover branch merges, rename chains, deletions, filter
boundaries, and text/binary transitions without relying on YAML clamping.

## Phase 3 — Make the core deterministic and lifecycle-safe

Priority: P1

### CORE-01: Define deterministic graph semantics

Status: implementation completed 2026-07-25; full baseline gate remains open

`BreadthSort.Level` is now the shortest root distance and `Index` is deterministic ordered-BFS
discovery. Roots, children, cycles, debug output, and pipeline ambiguity resolution follow the
graph ordering policy; nodes are marked when enqueued. Edge mutations validate both endpoints,
deduplicate count changes, and propagate planner errors, while insertion ordering remains stable
after removals.

Randomized graph construction and pipeline item insertion produce identical final plans across
100 runs. The exact combined gate remains blocked by pre-existing `internal/core` failures in
merge tracking and pipeline result/configuration tests; the same count-one failures were
reproduced from an untouched `HEAD` worktree.

Acceptance criteria:

- [ ] `go test -count=100 ./internal/toposort ./internal/core` passes;
- [x] randomized insertion and map order produce byte-identical plans.

### CORE-02: Replace lossy identity joining

Status: completed 2026-07-25

Identity joining now returns a destination for every source record and uses union-find across all
alias tokens, preserving duplicates and transitive overlaps without colliding unrelated people.
Literal identities are deduplicated in first-seen order; people components use stable first-record
order and canonical name-before-email lexical ordering. Developers, Couples, and Burndown mergers
aggregate every mapped source row. Duplicate, transitive, mixed-token, unrelated, deterministic,
and associative fixtures pass.

### CORE-03: Reject empty, duplicate, and disconnected commit input cleanly

Status: completed 2026-07-25

Empty repositories, commit files, and plans now return exported typed sentinel errors instead of
panicking. Explicit input rejects malformed, nil, zero-hash, and duplicate commits; disconnected
components return stable structured diagnostics with sorted roots and sizes rather than silently
selecting one component. Commit-file scanner errors propagate, and execution verifies that
metadata counts exactly match the commits consumed by the accepted plan. Core and CLI hostile-input
fixtures cover each path and deterministic diagnostics.

### CORE-04: Complete Line History replay

Status: completed 2026-07-25

LineDumper replay now preserves immutable metadata and configured loggers across initialization,
reconstructs deterministic live-line ownership for `NameOf`, `ForEachFile`, and `ScanFile`, and
lets configuration-provided commits enter normal pipeline planning. Typed validation rejects
malformed hashes, ranges, inconsistent ownership deltas, truncation, and trailing documents
transactionally. The resolver contract documents synthetic replay indices because the aggregate
wire format omits edit positions; a lifecycle fixture proves export→load→export equivalence.

### CORE-05: Remove process termination and leaked global state

Status: completed 2026-07-26

Line History and legacy Burndown now reject packed author/tick overflow with typed errors, including
pre-run commit-span validation, instead of terminating the host process. Hibernating runs serialize
and restore the process GC setting, dispose every unique branch instance on success or failure, and
remove disk state transactionally and idempotently across Line History and both Burndown analyzers.
Initialization preserves injected loggers. Repeated, race, cleanup, failure-path, lint, and vet
fixtures cover the lifecycle contract.

### CORE-06: Harden foundational boundary behavior

Status: completed 2026-07-26

Allocator hibernation now uses a versioned, checksummed format, validates every declared size before
allocation, reads fixed payloads fully, and commits deserialized state only after complete
validation. `Abs64` explicitly saturates `math.MinInt64` to `math.MaxInt64`. Commit-list scanner
errors propagate; repeated Commits initialization resets collected state while preserving injected
loggers, and file statistics are totally ordered during collection and non-mutating text/binary
serialization. Duplicate RB-tree inserts are regression-tested to allocate no nodes, and the stale
marker is removed. Truncation, oversizing, version, integrity, numerical-boundary, initialization,
and deterministic-output fixtures cover the contract.

### CORE-07: Define line-history behavior for binary transitions and branch deletions

Status: completed 2026-07-26

Tracked text files now retain one file ID as a zero-line placeholder while binary: text→binary
removes visible ownership without a deletion sentinel, and binary→text reconstructs visible lines
under the transition author without allocating a second identity. Renames preserve that contract.
Merge commits stage line edits with merge markers, reuse abandoned IDs, resolve ownership from live
parents, and synchronize every branch, including deletion-versus-edit histories. Modern and legacy
Burndown retain restorable per-file histories across branch-local deletion; Code Churn receives only
the documented ownership deltas. Legacy rename cleanup deletes graph entries cycle-safely and
rejects empty-path traversal. Transition, rename, binary deletion, merge, downstream-total,
hibernation-state, cycle, and rename-chain deletion fixtures cover the invariants.

## Phase 4 — Harden readers, formats, and report generation

Priority: P1/P2

### DATA-01: Validate YAML and PB before allocation or indexing

Status: completed 2026-07-26

Renderer readers and every leaf protobuf deserializer now share typed malformed, oversized, and
unsupported-version errors plus configurable limits for input bytes, decoded cells, dimensions,
and nested records. File and stdin reads use `io.LimitReader`; dense YAML matrices must be
rectangular, sparse indices nonnegative, protobuf dimensions bounded before allocation, CSR
pointers/data/indices internally consistent, and index-coupled arrays length-compatible before
access. Historical unknown-author and sparse-author shapes remain supported. Hostile fixtures cover
negative, overflowed, ragged, truncated, inconsistent, and tiny-payload/huge-dimension inputs;
short fuzz runs cover YAML, protobuf envelopes, burndown/CSR messages, and all leaf deserializers,
while the historical renderer fixtures continue to load.

### DATA-02: Enforce schema compatibility

Status: completed 2026-07-26.

Readers now require valid metadata and enforce a documented format-specific
compatibility matrix. PB schema 1 is decoded and migrated content-by-content
(including the incompatible Couples wrapper and UAST representation) before
being marked schema 2; legacy YAML `version: 0` is normalized only after full
schema-2 validation. Missing, malformed, unsupported, and future versions are
rejected. Combine uses the same bounded migration/validation path and has a
regression test proving that unknown legacy contents cannot be relabelled as
current output. Explicit old/current/newer/missing/malformed cases cover both
reader formats.

### DATA-03: Make report output transactional and collision-free

Status: completed 2026-07-26.

Report generation now writes exclusively to a fresh sibling staging directory, records the
current files plus warnings and failures in `manifest.json` and `index.html`, and atomically
installs or exchanges the completed directory. A strict render failure discards staging without
touching the previous report; successful reruns replace the entire old tree, so stale charts
cannot survive. File, person, and repository fan-outs preflight every destination and use
rune-safe bounded slugs plus a stable SHA-256-derived short hash. Regression tests cover stale
asset removal, failed/abandoned publication, manifest outcomes, duplicate plans, sanitization
collisions, and long Unicode identities.

### DATA-04: Correct `labours --from-repo`

Status: completed 2026-07-26.

`labours --from-repo` now resolves a tested mode→analysis dependency table, invokes the
`exec.LookPath`-resolved Hercules executable once with the sorted union of required flags, and
passes the exact requested modes to one aggregate renderer run. Git-based discovery accepts
nested paths, linked worktrees, and bare repositories. Every concrete mode has either an explicit
analysis mapping or a precise unsupported reason for single-repository input; integration tests
cover temporal activity, bus factor, hotspot risk, people coupling, file burndown, aggregated
render errors, and repository discovery.

### DATA-05: Make plugin and protobuf generation reproducible

Status: completed 2026-07-26.

Plugin generation now validates names and overrides before deriving artifacts, propagates
required-flag, template, write, close, and `protoc` failures, and emits a reproducible Makefile.
The documented `make` workflow builds a generated example with the required cgo/pure-Go settings;
GoGo Protobuf is pinned to v1.3.2 and `protoc` to 3.20.3 in CI. Schema CI regenerates committed
protobuf output, rejects diffs, generates the same plugin twice byte-for-byte, and builds it.

### DATA-06: Harden repository-supplied text and configuration

Status: completed 2026-07-26.

Mailmap parsing now validates every delimiter and skips malformed or conflicting records without
panicking. People and merge dictionaries accept bounded long records, reject empty/conflicting
aliases, check scanner errors, and install parsed state only after the complete input succeeds;
oversized/truncated input therefore cannot masquerade as a complete map. Merge identities and
mailmap-derived IDs are assigned deterministically. Invalid user whitelist expressions return
configuration errors instead of panicking. Table, atomic-state, repeated-run, and fuzz tests cover
malformed mailmaps, both dictionary formats, oversized records, duplicate hashes, and arbitrary
regular expressions.

### DATA-07: Complete burndown survival-analysis parity

Status: completed 2026-07-26.

Native and Python-compatible burndown modes now share the historical weighted Kaplan–Meier
calculation over the raw age-band matrix, with explicit entry, death, final-sample censoring,
empty/zero-line, sampling, and granularity semantics. `reportSurvival` controls the returned curve,
and a writer-based deterministic formatter reproduces Python's sampled survival table without
mixing it with plotting state. Raw and resampled golden fixtures, repeated-run, censoring, invalid
matrix, and flag tests cover the contract. Unix-epoch flooring now matches Python for calendar,
non-calendar, fractional, and timezone-sensitive tick boundaries.

## Phase 5 — Bound resource use and improve large-repository scaling

Priority: P2

### PERF-01: Replace pathological coupling ranking

Status: completed 2026-07-26.

Coupling data now stays in canonical CSR form through protobuf/YAML readers, shotness profile
construction, ranking, developer modes, and streamed projector output. Ranked pairs and heatmap
entity selection use bounded min-heaps with deterministic ties; only the selected at-most `60×60`
heatmap is materialized densely. Sparse validation limits stored entries rather than logical
`N×N` cells. Regression tests cover exact top-K/statistics, 10,000-entity CSR retention and bounded
rendering, streamed normalized embeddings, and sparse resource limits. The 1,000/10,000-entity
benchmark demonstrates entity-count-independent retained allocation for ranking.

### PERF-02: Stream burndown merges

Status: completed 2026-07-26.

Burndown result merging now interpolates one source-sampling chunk per age band and finalizes one
output row at a time, replacing the duration-squared daily float workspace with linear auxiliary
state. The legacy analyser shares the same implementation and algorithmic code no longer forces
garbage collection. Existing interpolation invariants plus a mixed-resolution/start-offset golden
test preserve the documented one-line float32 rounding tolerance. Allocation and Linux current-RSS
benchmarks cover two-, five-, and ten-year daily histories and report interpolation workspace
separately from the unavoidable dense result size.

### PERF-03: Incremental ownership snapshots

Status: completed 2026-07-26.

Bus Factor and Ownership Concentration now depend on one registered incremental ownership stage,
so every changed ownership run is applied once and immutable tick maps are shared by both metrics.
Tick boundaries never enumerate files or scan living lines; final totals and subsystem ownership
are cached across both leaves. A stable-large-file benchmark compares this path with the former
full-rescan shape and reports changed runs, snapshot entries, stable files, allocations, and time.

### PERF-04: Bound blob and rename processing

Status: completed 2026-07-26.

Blob caching now rejects declared sizes before allocation, enforces configurable 100 MiB per-blob
and 512 MiB per-commit defaults, reads into exact bounded storage, and fails closed with typed
errors. Both rename directions share the configured commit deadline; nested diffs use its remaining
time, peer completion cancels the other direction, and a two-result buffered join prevents
simultaneous errors from deadlocking. Import workers have one deferred close-and-join lifecycle on
all returns. Hostile-size, aggregate-budget, deadline, simultaneous-error, and worker-exit
regressions pass under the race detector.

## Phase 6 — Isolate configuration and simplify architecture

Priority: P2/P3

### ARCH-01: Introduce an instance-scoped renderer

Status: completed 2026-07-26.

`Renderer` now snapshots the complete typed options set, time bounds, theme palette, output backend,
and fallback policy. Dispatch and mode handlers receive that configuration explicitly; renderer,
graphics, mode, and reader packages no longer import Viper, while the labours CLI is the sole Viper
adapter. Legacy entry points retain deterministic defaults without mutable fallback switches.
Parallel renderers with opposite fallback, limit, theme, and backend settings are synchronized in a
regression test and remain isolated under the race detector.

### ARCH-02: Replace unsafe flag fact aliasing

Status: completed 2026-07-27.

Registry flags now retain ordinary typed pflag pointers and produce detached facts through an
explicit post-parse `FlagConfiguration.Snapshot`. The legacy `AddFlags` API remains source- and
behavior-compatible through safe value synchronization, without interface-layout pointer
arithmetic. Public typed fact getters and centralized configuration/shared-fact validation report
descriptive `FactTypeError` failures for invalid option, tick, identity, repository metadata, and
output-path types before configuration can silently fall back.

### ARCH-03: Make lifecycle and cleanup contracts explicit

Status: completed 2026-07-27

The pipeline-item contract now defines configuration and run-state ownership across configuration,
initialization, branching, hibernation, finalization, and idempotent disposal. Pipeline cycle state
tracks prepared plans and active resources separately, cleans replaced or partially initialized
items on validation errors and panics, and retains the existing success/failure branch-clone cleanup.
A reusable registry-wide fixture initializes and executes every default Git item twice across a real
branch and merge; it exposed and fixed stale `TreeDiff` commit state and Line History abandonment
metadata. Pipeline lifecycle/configuration, DAG resolution, execution, renderer JSON extraction,
and legacy Burndown lifecycle/results/change processing now have focused source files.

## Phase 7 — Strengthen CI, release, and documentation

Priority: P3

### CI-01: Run meaningful checks on pull requests

Status: completed 2026-07-27

Pushes and pull requests, including forks, now receive the same read-only unit, deterministic,
targeted race, schema, lint, vulnerability, cross-compilation, fuzz-smoke, and CLI end-to-end
gates. Repeated graph/pipeline tests exercise deterministic ordering; short fixed-budget fuzzing
covers analysis input, plumbing, identity, leaf, and renderer-reader boundaries. Binary-level
tests cover managed-cache replacement and refusal, combine, report publication, `--from-repo`,
and success/failure exit codes. The new fuzz gate also exposed and fixed a malformed Couples
payload panic caused by `people_files`/`files_lines` without their corresponding index metadata.

### CI-02: Make visual parity a real regression gate

Status: completed 2026-07-27

Every push and pull request now renders a committed, license-safe golden set from the repository's
small fixtures. The deterministic gate covers stacked and annotated time-area charts, a heatmap,
and a ranked bar chart; it enforces both the exact multi-file artifact contract and PNG contents,
so missing references, layout, palette, and label changes fail instead of skipping. A manual
checksum-pinned extended workflow retains the historical Python-reference comparisons without
making external assets part of the fork-safe pull-request gate.

### CI-03: Pay down and enforce lint debt

Status: completed 2026-07-27

CI now runs `nilerr`, `nilnesserr`, `errcheck`, `errorlint`, `staticcheck`, `govet`, `gosec`,
and `unused` across the full repository before the separate new-issue style gate. The renderer
directory exclusions were removed, hidden correctness findings and ignored error returns were
fixed or made explicit, and the reviewed `unused` baseline names only the retained dormant
renderer compatibility helpers.

### OPS-01: Align build documentation and actual build behavior

Status: completed 2026-07-27

Supported source, release, Action, and developer builds now select the pure-Go renderer explicitly
with `CGO_ENABLED=0`; intentional cgo profiles pair their feature tag with `purego`. CI executes the
documented `just` and direct builds and builds a Linux amd64/arm64 OCI index. The container uses a
Go 1.26.5 multi-architecture digest, checksum-locked modules, deterministic build metadata defaults,
and a scratch non-root runtime with only both binaries and CA roots. Package upgrades, install
scripts, obsolete server settings, and root runtime were removed. The remaining downloaded
developer binary, treefmt v2.1.1, is selected per platform and verified against its pinned SHA-256.

### DOC-01: Correct user-visible contracts

Status: completed 2026-07-27

README and hibernation guidance now name only supported flags and describe cache replacement,
schema compatibility, semantic changes, and warning versus hard-failure exit behavior. Every
default report analysis links to a precise metric definition, and every concrete renderer mode has
a tested output-asset manifest. CLI end-to-end coverage validates documented command flags against
live help, exercises the local README analysis/render workflow, and smoke-tests root and subcommand
help; the latter exposed and fixed a custom-usage panic on ordinary subcommands.

### DOC-02: Make the burndown y-axis magnitude self-contained

Status: completed 2026-07-27

Burndown, ownership, language-evolution, and developer-effort line-count axes now use fixed full
decimal labels such as `80000`, with no detached scientific offset or threshold-dependent formatter
switch. Burndown layouts reserve room for the wider labels, `--no-burndown-title` removes burndown
and ownership titles without image cropping, and `just burndown-chart` uses that option instead of
ImageMagick post-processing. Large-value SVG regressions verify that the magnitude remains on the
axis and that no `1e4`/`1e5` offset text is emitted.

### DOC-03: People-based charts leak raw identity strings into labels

Status: completed 2026-07-28

All people-based renderers now pass raw identity records through a shared public-label boundary:
`devs`, `devs-efforts`, `burndown-person`, `ownership`, and `overwrites-matrix` display only the
canonical non-email name, including exact-signature `Name <email>` inputs, while email-only records
fall back to `Contributor`. Full identity strings remain available to analysis and JSON output, and
`--people-anonymity` labels pass through unchanged. Regression coverage verifies that a multi-address
identity renders as one name with no `@` and that JSON retains the complete identity record.

### DOC-04: devs-efforts wastes ~40% of the canvas and dips below zero

Status: completed 2026-07-28

Developer-effort charts now render only the meaningful non-negative cumulative stack; the inherited
negated and rescaled instantaneous mirror that produced the downward spikes and empty lower canvas
was removed. Invalid negative legacy totals are clamped at both matrix construction and rendering,
and the y-axis is derived from the rendered stack extent with only 2% headroom. Extent regression
coverage verifies that the data fills at least 98% of the axis, while a visual render test uses the
checked-in Hercules history fixture that previously produced large negative spikes.

### DOC-05: burndown-person emits one figure per developer with an unreadable legend

Status: completed 2026-07-28

`burndown-person` now documents its file fan-out in CLI help and emits bounded, rune-safe filenames
whose readable slug contains only the canonical contributor name while the complete identity
contributes a stable collision-resistant hash. Each person chart removes age bands that contributor
never occupied and trims the default x-axis to their own activity window. Help and the People guide
also direct the common “who owns what” use case to the existing single-file `ownership` developer
stack. Regression coverage protects private filenames, compacted bands and dates, and the
fan-out/combined-view help contract.

Acceptance criteria:

- [ ] `--help` states that `burndown-person` writes one file per developer;
- [ ] a per-person figure's legend lists only bands with data.

## TODO-marker audit

Audit date: 2026-07-25  
Scope: tracked source, tests, templates, and documentation; generated protobuf and vendored LZ4
sources were classified separately.

| Marker or related unfinished behavior                                             | Disposition                                                                                                                            |
| --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Graph ordering in `internal/toposort/toposort.go` and `internal/core/pipeline.go` | One issue; covered by `CORE-01`.                                                                                                       |
| Negative-value clamping in `internal/yaml/utils.go`                               | Resolved by `METRIC-10`; valid burndown histories are checked before serialization and no longer request clamping.                     |
| Text/binary conversion in `internal/linehistory/line_history.go`                  | Resolved by `CORE-07`; tracked files retain identity through an explicit zero-line binary state.                                       |
| Code Churn description and constant reinforcement factor in `leaves/codechurn.go` | Resolved by `METRIC-09`; the experimental equations and output contract are documented and hand-tested.                                |
| Empty-string rename tombstones in `leaves/burndown_legacy.go`                     | Resolved by `CORE-07`; cycle-safe cleanup deletes entries and excludes empty paths from traversal.                                     |
| Ignored survival flag and placeholder Kaplan–Meier output in the renderer         | One parity gap; covered by `DATA-07`.                                                                                                  |
| RB-tree “delay creating” comment in `internal/rbtree/rbtree.go`                   | Resolved by `CORE-06`; the stale marker is removed and duplicate inserts are proven not to allocate.                                   |
| Plugin-template description text                                                  | Intentional generated-code customization point, not Hercules backlog. Keep it unless `DATA-05` replaces it with a generator parameter. |
| `LineHistory` loader replay gap                                                   | Resolved by `CORE-04`; replay reconstructs live ownership and `ScanFile` is lifecycle-tested.                                          |
| Packed tick/author “hack” comments                                                | Resolved by `CORE-05`; configured capacity is checked before execution and custom inputs return typed errors without process exit.     |
| Parallel branch deletion comments in line history and legacy burndown             | Resolved by `CORE-07`; merge markers preserve the live edited branch and synchronize all parents.                                      |
| Basic `FloorDateTime` implementation                                              | Include in the historical-renderer comparison under `DATA-07`.                                                                         |
| `XXX_*` protobuf members and LZ4 `DEBUGLOG` macros                                | Generated/vendored API names, not task markers; do not edit them by hand.                                                              |

When the referenced work item is completed, remove its TODO/placeholder wording in the same change.
Do not close a marker merely by deleting the comment: the acceptance criteria above are the closure
conditions.

## Recommended pull request sequence

The phases above describe dependencies; the following PR slices keep review scope manageable:

1. Cache safety and destructive-path tests (`SAFE-01`).
2. GitHub Action quoting and hostile-input tests (`SEC-01`).
3. `go-git` upgrade, `govulncheck`, dependency automation (`SEC-02`).
4. Renderer/CLI typed error propagation and combine semantics (`REL-01`).
5. Hotspot Risk contract and scoring correction (`METRIC-01`).
6. Onboarding, bus-factor, and ownership tick semantics (`METRIC-02`, `METRIC-03`).
7. File History, Couples merge, and Shotness identity fixes (`METRIC-04`, `METRIC-05`,
   `METRIC-07`).
8. Replace or temporarily disable `couples-shotness` (`METRIC-06`).
9. Tree-filter boundary transitions (`METRIC-08`).
10. Code Churn semantics and negative-burndown diagnosis (`METRIC-09`, `METRIC-10`).
11. Deterministic graph and identity joining (`CORE-01`, `CORE-02`).
12. Empty/disconnected input handling and line-history replay (`CORE-03`, `CORE-04`).
13. Lifecycle cleanup, capacity errors, foundational boundaries, and binary/branch transitions
    (`CORE-05`, `CORE-06`, `CORE-07`, `ARCH-03`).
14. Reader bounds, schema validation, repository-text hardening, and fuzz tests (`DATA-01`,
    `DATA-02`, `DATA-06`).
15. Transactional report generation and exact `--from-repo` behavior (`DATA-03`, `DATA-04`).
16. Complete and parity-test burndown survival output (`DATA-07`).
17. Coupling/burndown/blob/rename performance work with benchmarks (`PERF-*`).
18. Renderer isolation and typed registry configuration (`ARCH-01`, `ARCH-02`).
19. CI, visual, build, container, plugin, and documentation hardening (`CI-*`, `OPS-01`,
    `DOC-01`, `DATA-05`).

Each PR must leave the supported cgo-free test suite green. If a metric correction intentionally
changes a golden result, the PR must include a hand-calculated explanation rather than merely
updating the golden.

## Verification matrix

### Every pull request

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 golangci-lint run --config ./.golangci.toml --modules-download-mode=mod
```

### Core and lifecycle changes

```bash
CGO_ENABLED=0 go test -count=100 ./internal/toposort ./internal/core
CGO_ENABLED=0 go test -race ./internal/core ./internal/linehistory ./internal/rbtree
```

### Analysis changes

```bash
CGO_ENABLED=0 go test -race ./internal/plumbing/... ./leaves/...
CGO_ENABLED=0 go test ./test/plugin_smoke
```

### Renderer and format changes

```bash
CGO_ENABLED=0 go test -race ./internal/render/... ./cmd/labours ./cmd/hercules
just test-visual
just test-visual-parity
```

### Security and release candidates

```bash
govulncheck ./...
just test
just test-plugin
```

Run the large-repository benchmark suite for any change touching commit planning, blob caching,
diffing, line history, burndown merging, coupling, ownership, or rendering:

```bash
just bench-large-repo /path/to/representative/repository
```

## Finding-to-work mapping

| Audit finding                                                             | Work item       |
| ------------------------------------------------------------------------- | --------------- |
| Existing cache path recursively deleted                                   | `SAFE-01`       |
| GitHub Action shell injection                                             | `SEC-01`        |
| Vulnerable `go-git` dependency                                            | `SEC-02`        |
| Hard render/combine failures exit zero                                    | `REL-01`        |
| Hotspot Risk tick/key/ownership/scoring defects                           | `METRIC-01`     |
| Onboarding cohorts based on 1970                                          | `METRIC-02`     |
| Bus-factor/ownership off-by-one snapshots                                 | `METRIC-03`     |
| Bus-factor threshold truncation                                           | `METRIC-03`     |
| File History loses people on rename                                       | `METRIC-04`     |
| Couples merge uses people index for files                                 | `METRIC-05`     |
| `couples-shotness` labels ticks as entities                               | `METRIC-06`     |
| Shotness collapses same-named entities                                    | `METRIC-07`     |
| Filter-crossing renames corrupt state                                     | `METRIC-08`     |
| Code Churn has placeholder semantics and an inert reinforcement factor    | `METRIC-09`     |
| Burndown serialization silently clamps unexplained negative balances      | `METRIC-10`     |
| Nondeterministic `BreadthSort` and edge counts                            | `CORE-01`       |
| Identity joining loses duplicates/overlaps                                | `CORE-02`       |
| Empty/disconnected/duplicate commits panic or disappear                   | `CORE-03`       |
| Line History loader erases state and panics                               | `CORE-04`       |
| Packed limits terminate process; cleanup/global GC leaks                  | `CORE-05`       |
| Truncated allocator/commit input and numerical boundaries                 | `CORE-06`       |
| Binary transitions and branch deletions lose line-history state           | `CORE-07`       |
| Malformed YAML/PB panics or allocates excessively                         | `DATA-01`       |
| Schema versions emitted but not enforced                                  | `DATA-02`       |
| Stale/colliding report artifacts                                          | `DATA-03`       |
| `labours --from-repo` renders substitute modes                            | `DATA-04`       |
| Plugin/protobuf generation drift                                          | `DATA-05`       |
| Malformed `.mailmap`, dictionaries, or regex configuration panic/truncate | `DATA-06`       |
| Survival flag ignored and Kaplan–Meier output is a placeholder            | `DATA-07`       |
| Coupling ranking up to `O(N^4)`                                           | `PERF-01`       |
| Burndown merge quadratic memory and forced GC                             | `PERF-02`       |
| Ownership rescanned for every tick                                        | `PERF-03`       |
| Unbounded blobs and ineffective rename timeout                            | `PERF-04`       |
| Global renderer/Viper state                                               | `ARCH-01`       |
| Unsafe interface-layout flag storage                                      | `ARCH-02`       |
| Implicit lifecycle contracts                                              | `ARCH-03`       |
| Push-only CI, opt-in visuals, broad lint exclusions                       | `CI-01`–`CI-03` |
| cgo/documentation/container drift                                         | `OPS-01`        |
| Stale CLI and output documentation                                        | `DOC-01`        |

## Definition of done

This plan is done only when the completion gates are met and the finding-to-work table has no open
row. A green unit test suite alone is not sufficient: corrected metrics must have hand-verifiable
integration fixtures, safety changes must have hostile-input tests, and scalability changes must
have before/after benchmark evidence.
