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

Affected code:

- `cmd/hercules/root.go`
- `cmd/hercules/root_test.go`
- `README.md`
- `.github/workflows/test-crosscompile.yaml`

Work:

- Replace unconditional `os.RemoveAll(cachePath)` with a managed-cache policy.
- Refuse an existing non-empty destination unless the user explicitly supplies
  `--force-cache-replace`.
- Store a Hercules-owned marker in created cache directories and require that marker before
  replacement.
- Reject filesystem roots, the current working directory, and home directories as replacement
  targets even with the force flag.
- Treat `os.Stat` errors other than `os.ErrNotExist` as hard errors.
- Clone into a temporary sibling and atomically rename it into place after a successful clone.
- If atomic replacement is unavailable on the target platform, leave the original path untouched
  and report the failure.

Tests:

- existing empty target;
- existing Hercules-managed cache;
- existing unrelated directory with sentinel contents;
- filesystem root and current working directory;
- failed clone leaves the previous cache intact;
- replacement failure is surfaced.

Acceptance criteria:

- [x] no path is recursively removed without explicit intent and a verified Hercules marker;
- [x] all safety tests run on Linux, macOS, and Windows-compatible path logic.

### SEC-01: Remove shell injection from the composite GitHub Action

Status: completed 2026-07-24

Affected code:

- `action.yml`
- new Action test workflow or shell test fixture

Work:

- Move GitHub expressions into step environment variables.
- Quote scalar values such as output paths and modes.
- Replace the free-form shell `args` contract with an argv-safe representation. Prefer a JSON
  string array parsed into a Bash array; retain a deprecated compatibility input only if it can be
  parsed without `eval`.
- Write `$GITHUB_OUTPUT` and `$GITHUB_STEP_SUMMARY` using quoted environment values.
- Make chart-generation failure fail the step unless an explicit best-effort input is set.

Tests:

- spaces and Unicode in output paths;
- semicolons, command substitutions, redirects, newlines, and quotes in every input;
- empty argument arrays;
- multiple analysis flags.

Acceptance criteria:

- [x] caller inputs are data, never executable shell source;
- [x] hostile input tests prove that no sentinel command is executed.

### SEC-02: Upgrade and continuously audit dependencies

Status: completed 2026-07-25

Verification report: [`docs/audits/2026-07-25-sec-02.md`](docs/audits/2026-07-25-sec-02.md)

Affected code:

- `go.mod`
- `go.sum`
- `.github/workflows/`

Work:

- Upgrade `github.com/go-git/go-git/v5` from `v5.5.2` to a version containing all currently
  published fixes, at minimum `v5.16.5`.
- Upgrade `go-billy` and related transport dependencies together where required.
- Run the full repository, remote-clone, Siva, submodule, authentication, and plugin tests after
  the upgrade.
- Add `govulncheck ./...` to CI for the default feature set.
- Add Dependabot or Renovate with grouped Go dependency updates.
- Document the supported dependency-update policy.

Acceptance criteria:

- [x] no reachable known vulnerability is reported for the default build;
- [x] remote HTTPS, SSH, and local/file transport behavior is covered;
- [x] future vulnerable direct dependencies fail CI.

### REL-01: Make hard CLI failures return nonzero

Status: completed 2026-07-25

Verification report: [`docs/audits/2026-07-25-rel-01.md`](docs/audits/2026-07-25-rel-01.md)

Affected code:

- `internal/render/render.go`
- `internal/render/dispatch.go`
- `cmd/labours/root.go`
- `cmd/labours/helpers.go`
- `cmd/hercules/combine.go`
- `cmd/hercules/report.go`

Work:

- Make render execution return an aggregate result containing per-mode warnings and hard errors,
  plus a top-level output-write error.
- Replace broad message matching in `isMissingAnalysisError` with
  `errors.Is(err, readers.ErrAnalysisMissing)`.
- Send diagnostics to stderr.
- Continue independent modes after a failure, then return nonzero if any hard error occurred.
- Convert `combine` to `RunE`.
- Always validate a single combine input rather than byte-copying it.
- Fail when no valid input was merged, when `--only` cannot be honored, or when output cannot be
  written.
- Merge metadata only after the corresponding analysis result succeeds.
- Preserve warnings as warnings only for explicitly typed missing optional data.

Tests:

- unwritable JSON and image outputs;
- malformed and mixed-validity combine inputs;
- a real missing-analysis warning;
- an unrelated error containing `no`, `not found`, or `missing`;
- partial multi-mode rendering;
- broken stdout pipe/write.

Acceptance criteria:

- [x] hard failures never exit zero;
- [x] optional missing analysis remains a warning;
- [x] automation can distinguish success, warning-only success, and failure without parsing text.

## Phase 2 — Restore metric correctness

Priority: P1  
Policy: until an analysis passes its end-to-end tests, remove it from default report presets or mark
it experimental in help and documentation.

### METRIC-01: Rebuild Hotspot Risk on correct pipeline contracts

Status: completed 2026-07-25

Affected code:

- `leaves/hotspot_risk.go`
- `leaves/hotspot_risk_test.go`
- `internal/plumbing/ticks.go`
- `internal/plumbing/line_stats.go`
- renderer/report fixtures containing hotspot output

Work:

- Change `tickSize` to `time.Duration` and validate it is positive.
- Calculate the window with duration arithmetic, including sub-day and multi-day tick sizes.
- Stop constructing name-only `object.ChangeEntry` lookup keys. Use the actual old/new entry or
  publish an explicit path-keyed statistic.
- Replace net-change ownership approximation with current ownership from line history. Removed
  lines must be attributed to their previous owners, not the deleting author.
- Make zero weights remain zero so `0.0` truly disables a factor.
- Replace the all-or-nothing multiplicative score with a documented weighted normalized score, or
  explicitly handle absent/zero factors so one zero does not collapse every result.
- Define rename behavior for churn, coupling, and ownership histories.
- Version or document the metric-semantic change.

Tests:

- actual `TicksSinceStart`, `LinesStatsCalculator`, identity, and line-history dependencies;
- two authors, multiple ticks, a rename, a deletion, and a binary file;
- disabled factors;
- files with no coupling;
- hand-calculated expected scores and ranking.

Acceptance criteria:

- [x] a real pipeline fixture produces nonzero, explainable, hand-verifiable scores;
- [x] changing `WindowDays` changes the included churn exactly as documented;
- [x] no test fabricates a dependency type or key shape that differs from its producer.

### METRIC-02: Correct Onboarding time and window semantics

Status: completed 2026-07-25

Affected code:

- `leaves/onboarding.go`
- `leaves/onboarding_test.go`
- PB/YAML fixtures and renderer expectations

Work:

- Record the actual first commit timestamp for each author.
- Derive `JoinCohort` from that timestamp, not `Unix(0)+tick*tickSize`.
- Calculate windows using durations rather than truncated integer ticks-per-day.
- Validate positive tick size and define behavior for tick sizes longer than a window.
- Decide whether the closest snapshot must be at or before the requested window; encode that rule
  directly.

Tests:

- authors joining in different calendar months and time zones;
- sub-day, non-divisor, and multi-day tick sizes;
- no commit inside a requested window;
- exact boundary commits.

Acceptance criteria:

- [x] cohort keys match actual commit months;
- [x] window snapshots never include activity after their documented boundary.

### METRIC-03: Correct Bus Factor and Ownership Concentration snapshots

Status: completed 2026-07-25

Affected code:

- `leaves/bus_factor.go`
- `leaves/ownership_concentration.go`
- their tests and renderer fixtures

Work:

- Stop labeling post-next-tick state as the previous tick.
- Maintain incremental ownership totals from line-history changes, or preserve an immutable
  previous-tick state before consuming the next tick.
- Share one tested ownership snapshot component between both analyses to prevent drift.
- Use `math.Ceil` or exact integer comparison for the bus-factor threshold.
- Preserve final-tick and empty-repository semantics explicitly.

Tests:

- second tick transfers ownership and changes file size;
- small totals such as 2, 3, and 7 lines at 0.8 threshold;
- multiple commits in one tick;
- empty and single-author repositories;
- subsystem metrics match global ownership accounting.

Acceptance criteria:

- [x] each snapshot contains only commits from its labeled tick or earlier;
- [x] bus factor is the smallest author count whose exact share meets the threshold.

### METRIC-04: Preserve File History across renames

Status: completed 2026-07-25

Affected code:

- `leaves/file_history.go`
- `leaves/file_history_test.go`

Work:

- Move or merge the complete `FileHistory` state on rename, including hashes and per-author
  statistics.
- Define collision behavior when a destination path already has historical state.
- Cover rename chains and rename-plus-edit commits.

Acceptance criteria:

- [x] totals before and after a rename are conserved;
- [x] serialization and merged results preserve the same history.

### METRIC-05: Fix Couples merging

Status: completed 2026-07-25

Affected code:

- `leaves/couples.go`
- `leaves/couples_test.go`
- `internal/join/`

Work:

- Map file matrix rows through the file identity map, never the people map.
- Ensure all source and destination indices are validated before access.
- Use unrelated person and filename values in tests.
- Add merge associativity checks for three result sets.

Acceptance criteria:

- [x] merged file/people matrices match a hand-computed fixture;
- [x] merging order does not change the result.

### METRIC-06: Define and implement real `couples-shotness`

Status: completed 2026-07-25

Affected code:

- `internal/render/readers/yaml_reader.go`
- `internal/render/readers/pb_reader.go`
- `internal/render/modes/couplesShotness.go`
- mode lists and parity documentation

Work:

- Remove the current tick-index-as-entity-index calculation.
- Define coupling as a documented operation over aligned entity time series, such as co-activity,
  dot product, or normalized correlation.
- Compute a symmetric entity-by-entity matrix with a meaningful diagonal policy.
- Remove the mode from `all` until the replacement passes a hand-calculated fixture.

Acceptance criteria:

- [x] every displayed row and column represents the labeled entity;
- [x] top pairs and heatmap cells agree with the same source matrix;
- [x] PB and YAML readers produce identical matrices.

### METRIC-07: Preserve distinct structural entities in Shotness

Status: completed 2026-07-25

Affected code:

- `leaves/shotness.go`
- tree-sitter node model and shotness tests

Work:

- Key nodes by stable identity: qualified name/receiver, kind, and source identity rather than bare
  name.
- Define behavior for moved and renamed structural entities.
- Add fixtures with same-named methods on different receivers and nested scopes.

Acceptance criteria:

- [x] distinct same-named entities never overwrite one another;
- [x] counters and coupling remain stable across serialization round trips.

### METRIC-08: Correct filter-boundary transitions

Status: completed 2026-07-25

Affected code:

- `internal/plumbing/tree_diff.go`
- `internal/plumbing/tree_diff_test.go`

Work:

- Evaluate old-path and new-path inclusion separately.
- Translate included→excluded transitions to deletion.
- Translate excluded→included transitions to insertion.
- Preserve modify only when both sides remain in scope.
- Return language-detection errors rather than dropping them.

Acceptance criteria:

- [x] stateful analyses exactly match the filtered repository view after cross-boundary renames;
- [x] vendor, blacklist, and language filters share the same transition rules.

### METRIC-09: Finish and validate Code Churn semantics

Status: completed 2026-07-25

Affected code:

- `leaves/codechurn.go`
- `leaves/codechurn_test.go`
- `docs/SCHEMAS.md`

Work:

- Replace the copied line-burndown description with an accurate description of Code Churn,
  awareness, memorability, and delete-history output.
- Specify the awareness and memorability equations, their units, bounds, decay interval, and
  intended treatment of self-deletions versus deletions by other authors.
- Either define and implement the currently constant reinforcement factor or remove it and the
  speculative formula comments. Do not leave a tunable-looking constant whose value is always
  `1.0`.
- Add hand-calculated, multi-tick fixtures for reinforcement, decay, insertions, self-deletions,
  other-author deletions, and a serialization round trip.
- Keep the analysis explicitly experimental until those fixtures establish stable semantics.

Acceptance criteria:

- [x] `--help` and schema documentation describe the metric actually emitted;
- [x] every factor in the awareness/memorability calculation has a documented meaning and a test;
- [x] the implementation contains no placeholder formula or unexplained neutral factor.

### METRIC-10: Find and eliminate negative burndown balances

Status: completed 2026-07-25

Affected code:

- `internal/yaml/utils.go`
- `internal/burndown/`
- `leaves/burndown.go`
- `leaves/burndown_legacy.go`
- burndown merge, rename, deletion, and serialization tests

Work:

- Instrument the matrix update and merge paths so the first transition that creates a negative
  balance reports the file, tick, age band, and operation.
- Reproduce the issue with focused fixtures covering merges, rename chains, deletions, filter
  boundaries, and text/binary transitions.
- Fix the producing state transition rather than treating the YAML serializer's `fixNegative`
  clamp as the correction.
- Retain the clamp only as an explicitly documented compatibility safeguard, or replace it with a
  typed serialization error after proving normal histories cannot reach it.
- Assert non-negative global, file, person, and repository matrices before serialization.

Acceptance criteria:

- [x] valid histories never depend on output-time clamping;
- [x] a regression fixture demonstrates the original negative balance and the corrected transition;
- [x] invalid internal histories fail with a useful diagnostic instead of silently changing data.

## Phase 3 — Make the core deterministic and lifecycle-safe

Priority: P1

### CORE-01: Define deterministic graph semantics

Affected code:

- `internal/toposort/toposort.go`
- `internal/toposort/toposort_test.go`
- `internal/core/pipeline.go`

Work:

- Decide and document whether `BreadthSort.Level` means shortest distance, longest dependency
  depth, or another pipeline-specific rank.
- Sort roots and outgoing neighbors with the graph ordering policy.
- Mark nodes when enqueued so a later parent cannot overwrite their level accidentally.
- Make `AddEdge` and `RemoveEdge` update counts only when the edge set changes.
- Require both endpoints to exist or return an explicit error.
- Test final pipeline plans, not only intermediate graph positions.
- Remove the duplicate ordering TODO at the `Pipeline.resolveAmbiguous` call site once the graph
  contract is implemented.

Acceptance criteria:

- [ ] `go test -count=100 ./internal/toposort ./internal/core` passes;
- [ ] randomized insertion and map order produce byte-identical plans.

### CORE-02: Replace lossy identity joining

Affected code:

- `internal/join/join.go`
- all result mergers using `LiteralIdentities` or `PeopleIdentities`

Work:

- Deduplicate literal identities before assigning final indexes.
- Replace one-index-per-token traversal with union-find over every identity vertex and alias token.
- Produce a mapping for every original record, including duplicate and transitive overlaps.
- Define deterministic canonical names and output order.

Tests:

- duplicates within one input;
- duplicates across inputs;
- three-way transitive overlap;
- name-only, email-only, and mixed identities;
- merge associativity.

Acceptance criteria:

- [ ] no valid identity is omitted;
- [ ] no two unrelated identities receive the same final index;
- [ ] output is deterministic.

### CORE-03: Reject empty, duplicate, and disconnected commit input cleanly

Affected code:

- `internal/core/pipeline.go`
- `internal/core/forks.go`
- CLI commit-file handling and tests

Work:

- Return typed `ErrNoCommits`/`ErrNoReferences` errors for empty repositories and plans.
- Reject duplicate hashes in explicit commit input.
- Reject disconnected commit components with a diagnostic listing component roots and sizes.
- If a future mode intentionally permits component selection, expose it explicitly and compute
  metadata from the retained plan.
- Check all scanner errors when reading commit files.

Acceptance criteria:

- [ ] no empty or malformed input path panics;
- [ ] metadata commit counts exactly equal consumed commits;
- [ ] component selection is never implicit or map-order-dependent.

### CORE-04: Complete Line History replay

Affected code:

- `internal/linehistory/line_loader.go`
- `internal/linehistory/line_loader_test.go`
- public resolver contracts

Work:

- Keep parsed file metadata immutable across `Initialize`.
- Implement file line-state reconstruction and `ScanFile`, or revise the public feature contract
  and disable consumers that require unavailable state.
- Reset only iteration state during initialization.
- Preserve configured loggers.
- Validate loaded file/tick/author ranges and reject truncated input.
- Remove tests that expect a panic or erased state.

Acceptance criteria:

- [ ] an exported history can be loaded and consumed through the normal pipeline lifecycle;
- [ ] `NameOf`, `ForEachFile`, and `ScanFile` work after initialization;
- [ ] export→load→export is semantically equivalent.

### CORE-05: Remove process termination and leaked global state

Affected code:

- `internal/linehistory/line_history.go`
- `leaves/burndown_legacy.go`
- `internal/core/pipeline.go`
- hibernation code

Work:

- Validate packed author/tick capacity before running.
- Replace `log.Fatalf`/panic limits with returned typed errors; consider widening the packed
  representation.
- Save and restore `debug.SetGCPercent` around the run.
- Defer idempotent disposal for every unique branch item, including failure paths.
- Remove failed hibernation temporary files.
- Preserve injected loggers across `Initialize`.

Acceptance criteria:

- [ ] library use never terminates the host process for data/configuration limits;
- [ ] failed runs leave no temporary artifacts and do not change process GC settings.

### CORE-06: Harden foundational boundary behavior

Affected code:

- `internal/rbtree/rbtree.go`
- `internal/math.go`
- `internal/core/pipeline.go`
- `leaves/commits.go`

Work:

- Use `io.ReadFull` for fixed-size serialized allocator data and validate all lengths before
  allocating.
- Add integrity/version information to hibernated allocator files if they can survive beyond a
  single process operation.
- Define and test `Abs64(math.MinInt64)` rather than allowing signed overflow to return a negative
  result.
- Check `scanner.Err()` after commit-list parsing.
- Reset `CommitsAnalysis` state during repeated initialization.
- Sort file statistics before appending or serializing commit results.
- Remove the stale RB-tree insertion TODO: `doInsert` already delays allocation until it finds an
  empty insertion slot. Add an allocator-count regression test if the existing tests do not prove
  that duplicate inserts allocate nothing.

Acceptance criteria:

- [ ] truncated or oversized allocator data returns an error;
- [ ] numerical boundary behavior is explicit;
- [ ] repeated analysis initialization and serialization are deterministic.

### CORE-07: Define line-history behavior for binary transitions and branch deletions

Affected code:

- `internal/linehistory/line_history.go`
- `internal/linehistory/line_history_test.go`
- `leaves/burndown_legacy.go`
- line-history consumers that interpret insertion and deletion events

Work:

- Define whether a tracked path keeps its file identity and history across
  text→binary→text transitions.
- Replace the current text→binary shortcut through ordinary file deletion with transition-specific
  behavior that preserves the chosen identity and event semantics.
- Ensure binary→text reconstruction neither loses prior ownership unexpectedly nor double-counts
  the file as both deleted and newly inserted.
- Correct merge handling when one branch deletes a file while another independently edits it.
- Make legacy-burndown rename-chain cleanup delete map entries rather than leaving empty-string
  tombstones, and prove that an empty path cannot enter rename traversal.

Tests:

- text→binary, binary→text, and text→binary→text;
- each transition combined with a rename;
- deletion on one branch and modification on another before merge;
- rename cycles and deletion after a rename chain;
- emitted file IDs, ownership deltas, and downstream burndown/churn totals.

Acceptance criteria:

- [ ] transition and merge behavior is documented and identical across line-history consumers;
- [ ] no valid sequence loses a live branch's state or creates a synthetic empty filename;
- [ ] file IDs and line totals satisfy the documented invariants after every transition.

## Phase 4 — Harden readers, formats, and report generation

Priority: P1/P2

### DATA-01: Validate YAML and PB before allocation or indexing

Affected code:

- `internal/render/readers/yaml_reader.go`
- `internal/render/readers/pb_reader.go`
- `internal/render/readers/helpers.go`
- leaf `Deserialize` implementations

Work:

- Introduce typed `ErrAnalysisMalformed`, `ErrAnalysisTooLarge`, and
  `ErrAnalysisVersionUnsupported`.
- Validate nonnegative matrix dimensions and indices.
- Validate rectangular dense matrices.
- Validate CSR pointer monotonicity, terminal pointer, index bounds, and data/indices lengths.
- Validate all parallel-array lengths before indexing.
- Apply configurable limits to input bytes, decoded cells, rows, columns, and nested records.
- Use `io.LimitReader` for file/stdin input.
- Centralize validators so leaves and renderer use the same rules.

Tests:

- negative, overflowed, ragged, truncated, and inconsistent structures;
- extremely large declared dimensions with tiny payloads;
- fuzzing for YAML, PB envelopes, CSR matrices, and each leaf deserializer.

Acceptance criteria:

- [ ] malformed input returns a bounded error without panic or excessive allocation;
- [ ] valid historical fixtures continue to load.

### DATA-02: Enforce schema compatibility

Affected code:

- `internal/pb/version.go`
- readers
- `cmd/hercules/combine.go`
- schema documentation

Work:

- Require a valid metadata header.
- Reject newer breaking versions and explicitly migrate supported older versions.
- Require compatible input versions before combine.
- Never relabel an unvalidated old payload as the current schema.
- Apply equivalent validation to YAML headers.

Acceptance criteria:

- [ ] supported old, current, newer, missing, and malformed versions have explicit tests;
- [ ] combine never emits current-version metadata for unvalidated contents.

### DATA-03: Make report output transactional and collision-free

Affected code:

- `cmd/hercules/report.go`
- `internal/render/output.go`
- fan-out modes such as burndown person/file/repository

Work:

- Render into a fresh staging directory.
- Record an explicit manifest of files produced in the current run.
- Replace or publish the report directory atomically after required work succeeds.
- Do not recursively include stale assets from previous runs.
- Generate rune-safe slugs plus a stable short hash of the original identity.
- Detect duplicate planned paths before rendering.
- Include warnings and failures in the report summary.

Acceptance criteria:

- [ ] rerunning with fewer modes cannot retain stale charts;
- [ ] colliding paths and long Unicode names produce unique stable filenames;
- [ ] strict mode leaves the previous complete report intact on failure.

### DATA-04: Correct `labours --from-repo`

Affected code:

- `cmd/labours/helpers.go`
- `cmd/labours/root.go`

Work:

- Maintain one tested mode→required-analysis dependency table.
- Run Hercules once with the union of required analyses.
- Render the exact resolved mode list rather than a hardcoded substitute.
- Discover the Hercules executable with `exec.LookPath`.
- Support worktrees and bare repositories through Git-aware repository discovery.
- Return aggregate errors.

Acceptance criteria:

- [ ] every valid mode either renders that exact mode or returns a precise unsupported error;
- [ ] temporal, bus-factor, hotspot, couples-people, and burndown-file integration tests pass.

### DATA-05: Make plugin and protobuf generation reproducible

Affected code:

- `cmd/hercules/generate_plugin.go`
- `PLUGINS.md`
- `justfile`
- schema-generation CI

Work:

- Validate plugin names before indexing split results.
- Check template execution, file write, close, and required-flag errors.
- Generate the build file documented to users.
- Pin `protoc-gen-gogo`; do not install `@latest`.
- Regenerate protobuf code in CI and fail on a diff from committed output.
- Update broken and deprecated plugin documentation.

Acceptance criteria:

- [ ] a generated example plugin builds using only documented commands;
- [ ] generation is byte-reproducible with pinned tooling.

### DATA-06: Harden repository-supplied text and configuration

Affected code:

- `internal/plumbing/identity/mailmap.go`
- `internal/plumbing/identity/people.go`
- `internal/plumbing/identity/stories.go`
- `internal/plumbing/tree_diff.go`

Work:

- Bounds-check every `.mailmap` delimiter before slicing; skip or report malformed lines without
  panic.
- Increase scanner token limits where long identity records are supported and always check
  `scanner.Err()`.
- Reject conflicting aliases rather than silently overwriting them.
- Replace `regexp.MustCompile` on user configuration with `regexp.Compile` and a returned
  configuration error.
- Make merge-dictionary identity assignment deterministic rather than dependent on map iteration.
- Add table and fuzz tests for malformed `.mailmap`, people dictionaries, merge dictionaries, and
  regular expressions.

Acceptance criteria:

- [ ] committed repository text cannot panic configuration or identity detection;
- [ ] truncated input is never accepted as a complete identity map;
- [ ] identical input produces identical identity IDs.

### DATA-07: Complete burndown survival-analysis parity

Affected code:

- `internal/render/burndown/python_compatible.go`
- `internal/render/graphics/burndown_matplotlib.go`
- `internal/render/modes/burndown.go`
- `internal/render/modes/burndown_python.go`
- renderer parity fixtures

Work:

- Replace the ignored `reportSurvival` branch and placeholder printer with one tested
  Kaplan–Meier implementation matching the historical Python renderer.
- Route native and Python-compatible burndown modes through the same survival calculation; remove
  the current ad-hoc column-ratio implementations if they are not mathematically equivalent.
- Define censoring, zero-line, empty-matrix, sampling, and granularity behavior.
- Make survival rows deterministic and send diagnostics to the appropriate output stream.
- Replace the basic duration truncation in `FloorDateTime` if calendar/tick boundary parity tests
  show it differs from the Python behavior.

Acceptance criteria:

- [ ] golden fixtures match the historical Python survival output for raw and resampled matrices;
- [ ] toggling `reportSurvival` has observable, tested behavior;
- [ ] survival output order is stable across repeated runs;
- [ ] no placeholder survival path remains.

## Phase 5 — Bound resource use and improve large-repository scaling

Priority: P2

### PERF-01: Replace pathological coupling ranking

- Preserve sparse coupling matrices through readers and modes.
- Compute top pairs with a bounded min-heap instead of materializing and bubble-sorting all pairs.
- Avoid dense `N×N` expansion unless the requested chart size is below a documented threshold.
- Add benchmarks for 1,000 and 10,000 files/entities.

Acceptance criteria:

- [ ] top-K ranking is `O(nonzero × log K)`;
- [ ] memory is proportional to sparse nonzero entries plus bounded rendering data.

### PERF-02: Stream burndown merges

- Replace duration-squared full intermediate matrices with row/band streaming or sparse chunked
  accumulation.
- Remove explicit `runtime.GC()` calls from algorithmic code.
- Add allocation and peak-RSS benchmarks for multi-year daily histories.

Acceptance criteria:

- [ ] output remains numerically equivalent within documented rounding tolerance;
- [ ] peak memory grows sub-quadratically with history duration.

### PERF-03: Incremental ownership snapshots

- Share the incremental ownership accumulator created in `METRIC-03`.
- Avoid rescanning all live files and lines for every occupied tick.
- Benchmark repositories with many ticks and stable large files.

Acceptance criteria:

- [ ] work scales primarily with changed ownership runs plus snapshot output size.

### PERF-04: Bound blob and rename processing

Affected code:

- `internal/plumbing/blob_cache.go`
- `internal/plumbing/renames.go`
- import extraction worker lifecycle

Work:

- Introduce maximum blob size and total per-commit cache budgets with configurable behavior.
- Avoid pre-growing buffers directly from untrusted declared sizes without a limit.
- Give rename comparisons a shared context/deadline.
- Replace the hardcoded one-hour diff timeout.
- Buffer the worker error channel and cancel peer work on error.
- Ensure import workers are closed on every return path; consider a reusable bounded pool.

Acceptance criteria:

- [ ] configured time and memory limits are real upper bounds;
- [ ] rename errors cannot deadlock;
- [ ] worker-leak tests pass under `-race`.

## Phase 6 — Isolate configuration and simplify architecture

Priority: P2/P3

### ARCH-01: Introduce an instance-scoped renderer

Affected code:

- `internal/render/render.go`
- `internal/render/dispatch.go`
- graphics/theme configuration
- mode implementations

Work:

- Add a `Renderer` type carrying immutable options, theme, output policy, and fallback behavior.
- Pass configuration into handlers instead of reading global Viper state.
- Keep Viper only in the CLI adapter.
- Remove package-global fallback booleans.
- Make concurrent renders independent and race-free.

Acceptance criteria:

- [ ] parallel renders with opposite options do not leak configuration;
- [ ] `go test -race ./internal/render/... ./cmd/labours` passes.

### ARCH-02: Replace unsafe flag fact aliasing

Affected code:

- `internal/core/registry.go`
- public flag/fact facade

Work:

- Retain normal typed flag pointers.
- After parsing, build a plain typed configuration snapshot without rewriting interface data
  words via `unsafe.Pointer`.
- Introduce typed getters or configuration structs for frequently shared facts such as tick size,
  identities, repository metadata, and output paths.
- Migrate incrementally; do not break external plugins without a compatibility adapter.

Acceptance criteria:

- [ ] registry code contains no interface-layout pointer arithmetic;
- [ ] invalid fact types fail configuration with a descriptive error rather than silently falling back.

### ARCH-03: Make lifecycle and cleanup contracts explicit

- Document which fields survive `Configure`, `Initialize`, `Fork`, `Merge`, `Hibernate`, `Boot`,
  and `Dispose`.
- Add reusable lifecycle conformance tests for pipeline items.
- Separate immutable configuration from per-run state.
- Split the largest multi-purpose files after behavior is covered, especially renderer report
  metrics, core pipeline resolution/execution, and legacy burndown.

Acceptance criteria:

- [ ] repeated initialization is idempotent and clears only run state;
- [ ] every item with resources is disposed on success and failure;
- [ ] lifecycle tests cover all registered default items.

## Phase 7 — Strengthen CI, release, and documentation

Priority: P3

### CI-01: Run meaningful checks on pull requests

Affected code:

- `.github/workflows/test.yaml`
- reusable workflows

Work:

- Add `pull_request` alongside `push`.
- Add a repeated deterministic core test.
- Add targeted race jobs for core, plumbing, leaves, and renderer.
- Add fuzz smoke runs with fixed short budgets.
- Add `govulncheck`.
- Add end-to-end CLI tests for cache safety, combine, report, `--from-repo`, and exit codes.

Acceptance criteria:

- [ ] fork pull requests receive the same required correctness and security gates as pushes.

### CI-02: Make visual parity a real regression gate

- Commit or reliably fetch a small, license-safe representative golden set.
- Fail the job when required references are absent instead of skipping.
- Cover one chart from each major rendering family.
- Keep the full historical parity suite available as an extended job.

Acceptance criteria:

- [ ] layout, artifact-set, and major color/label regressions fail CI.

### CI-03: Pay down and enforce lint debt

- Save a reviewed lint baseline or fix high-signal existing findings first:
  `nilerr`, `nilnesserr`, `errcheck`, `errorlint`, `staticcheck`, `govet`, `gosec`, and `unused`.
- Stop excluding all of `internal/render` and `cmd/labours`; replace broad exclusions with narrow,
  justified rules.
- Keep style-only cleanup in separate pull requests.

Acceptance criteria:

- [ ] high-signal correctness/security linters run repository-wide;
- [ ] no production error return is silently ignored without an explicit justification.

### OPS-01: Align build documentation and actual build behavior

- Make the pure-Go renderer selection independent of ambient cgo where feasible.
- Otherwise include `CGO_ENABLED=0` consistently in README, release, and developer commands.
- Test every documented build command in CI.
- Pin container base images and downloaded tools by version/digest and checksum.
- Remove `apt-get upgrade`, unpinned install scripts, obsolete ports/environment variables, and
  root runtime where unnecessary.
- Add multi-architecture Docker builds.

Acceptance criteria:

- [ ] a clean supported machine can follow documentation verbatim;
- [ ] container builds are reproducible and architecture-correct.

### DOC-01: Correct user-visible contracts

- Remove or correct nonexistent/stale CLI flags.
- Document warning versus failure exit semantics.
- Document metric definitions and known compatibility changes.
- Document cache replacement safety and schema-version behavior.
- Update output manifests for every renderer mode.

Acceptance criteria:

- [ ] automated help/README examples execute successfully in CI;
- [ ] each default analysis links to a precise metric definition.

### DOC-02: Make the burndown y-axis magnitude self-contained

Burndown charts label the y-axis with bare numbers (`2`, `4`, `6`, `8`) and put the magnitude in a
separate matplotlib-style offset text, `1e4`, in the top-left corner above the axes box. A reader
who misses that corner reads the chart as single-digit line counts instead of tens of thousands.

The offset text is also physically detached from the axis it belongs to, so it is trivially lost.
`just burndown-chart` used to crop the top 40 px to drop the chart title, and that same band
carries the `1e4`; the chart published on pcjv.de showed 2/4/6/8 with no magnitude anywhere until
2026-07-25. The recipe now blanks out only the title's x-range (`rectangle 300,0 1350,35`, clear of
the axes frame at y=38) and keeps the offset text, so the immediate symptom is gone — but the
underlying fragility is the offset notation itself, and the next consumer will hit it again.

The switch into offset notation is also sensitive to a rounding-level change in the data, which makes
it unpredictable for anyone embedding a chart. `-m ownership` on this repository renders full `80000`
tick labels when the stack peaks at ~92k, and flips to `1e5` with `0.0`-`1.0` labels when it peaks at
~96k, because `maxOwnershipStackY(...)*1.05` crosses 1e5 and the formatter changes mode. Two
regenerations of the same chart weeks apart can therefore disagree about how the y-axis is labelled.

- Render the magnitude where it cannot be separated from the data: either format the ticks in full
  (`20000`, `40000`) or fold the unit into the axis label (`Lines of code (x10^4)`), rather than
  relying on detached offset text.
- Apply the same treatment to every mode that stacks line counts, not just `burndown-project`.
- Consider a flag to suppress the chart title, so downstream users stop cropping the figure by
  hand to remove it and taking the offset text with it.

Acceptance criteria:

- [ ] a rendered burndown chart states its magnitude within the axes box, verifiable by cropping the
      figure to the plot area alone;
- [ ] a visual test covers a data set large enough to trigger offset notation.

### DOC-03: People-based charts leak raw identity strings into labels

`devs`, `devs-efforts`, `burndown-person`, `ownership` and `overwrites-matrix` label each series with
the identity string as the detector built it — `vadim markovtsev|gmarkhor@gmail.com|vadim@athenian.co`
— so every rendered chart publishes its contributors' email addresses. On the hercules self-analysis
that is 29 addresses, and the same person appears several times over when their commits span more
than one address, which also splits their totals.

`--people-dict` is the existing escape hatch (`scripts/self-analysis-people.txt` is the merged dict
that `just devs-chart` passes), but it has to be maintained by hand, and the leak is the default
behaviour rather than an opt-in.

- Label series with the canonical name only; keep the full identity string for tooltips/JSON output.
- Consider making `--people-anonymity` composable with rendering, so a chart can be published
  without maintaining a dict at all.

Acceptance criteria:

- [ ] rendering a people-based mode without `--people-dict` produces labels containing no `@`;
- [ ] a test asserts the label for a multi-address identity is a single name.

### DOC-04: devs-efforts wastes ~40% of the canvas and dips below zero

`labours -m devs-efforts` on the hercules self-analysis renders the stacked effort area across the
top ~60% of the figure and leaves the bottom ~40% empty: the y-limits are computed from downward
spikes that reach roughly -5e4, but the axis extends to about -3.5e5, so most of the plot area is
blank. The spikes themselves are the second half of the problem — a cumulative "changed lines of
code" series should not go negative at all, which points at the delta accumulation rather than the
axis code.

Reproduce with:

```sh
just hercules labours
./hercules --burndown --burndown-people --devs --pb . > /tmp/pd.pb
./labours -i /tmp/pd.pb -m devs-efforts --max-people 8 -o /tmp/efforts.png
```

- Find why per-developer effort accumulates negative values, and fix the accumulation or clamp it.
- Derive the y-limits from the rendered data extent, not from a value that ignores it.

Acceptance criteria:

- [ ] the plotted area fills the axes box, verifiable by comparing the data extent to the axis limits;
- [ ] a visual test covers a repository whose history triggers the negative spikes.

### DOC-05: burndown-person emits one figure per developer with an unreadable legend

`labours -m burndown-person -o chart.png` does not write `chart.png`; it writes one file per
contributor (`chart_vadim markovtsev_gmarkhor@gmail_com.png`, 29 files for this repository), with
spaces and mangled addresses in the filenames. Each figure carries the full 40-band quarterly legend
whether or not that developer touched those quarters, so a contributor with a few hundred surviving
lines gets a chart that is roughly 80% legend and 20% data, on an axis spanning the whole project
history rather than their own.

- Document the fan-out in `--help` (the single `-o` path reads as a single output file), and derive
  filenames that are safe to serve.
- Drop empty bands from the per-person legend, and consider defaulting the x range to the
  developer's own activity window.
- Offer a combined per-developer view for the common case of "who owns what": a single figure
  stacking developers, which is what `-m ownership` already approximates.

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
| Text/binary conversion in `internal/linehistory/line_history.go`                  | Correct transition semantics and downstream invariants are covered by `CORE-07` and `METRIC-10`.                                       |
| Code Churn description and constant reinforcement factor in `leaves/codechurn.go` | Resolved by `METRIC-09`; the experimental equations and output contract are documented and hand-tested.                                |
| Empty-string rename tombstones in `leaves/burndown_legacy.go`                     | Covered by `CORE-07`; test rename chains and cycles before changing the map behavior.                                                  |
| Ignored survival flag and placeholder Kaplan–Meier output in the renderer         | One parity gap; covered by `DATA-07`.                                                                                                  |
| RB-tree “delay creating” comment in `internal/rbtree/rbtree.go`                   | Behavior is already implemented by `doInsert`; remove the stale marker and prove duplicate inserts do not allocate under `CORE-06`.    |
| Plugin-template description text                                                  | Intentional generated-code customization point, not Hercules backlog. Keep it unless `DATA-05` replaces it with a generator parameter. |
| `LineHistory` loader's `not implemented` panic                                    | Related unfinished work already covered by `CORE-04`.                                                                                  |
| Packed tick/author “hack” comments                                                | The capacity and host-process failure risk is covered by `CORE-05`.                                                                    |
| Parallel branch deletion comments in line history and legacy burndown             | Folded into the merge cases in `CORE-07`.                                                                                              |
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
