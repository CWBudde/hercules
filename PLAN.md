# Hercules — Plan

Date: 2026-07-02

This is the single forward-looking plan for the repository. **Part A** (Phases 0–10) covers the
integration of the Go renderer (`labours-go`); **Part B** (Phases 11–15) carries the remaining open
items from the former `ROADMAP.md` (now deleted; completed milestones live in git history).

## Part A: Integrate the Go renderer (`labours-go`) into Hercules

## Goal

Fold the Go renderer currently living in the separate `labours-go` repo into **this** repo so that
Hercules ships a native Go visualization path and no longer depends on the Python `labours` package.
End state: a single `github.com/meko-christian/hercules` module that builds both the `hercules`
analysis binary and a drop-in `labours` renderer binary, with `hercules report` rendering
**in-process** (no subprocess, no Python).

## Why

- **The renderer we actually use is out-of-tree.** `hercules report` shells out to a `labours`
  binary (`cmd/hercules/report.go` → `resolveLaboursCommand`), and the binary we rely on is
  `labours-go` (`/mnt/projekte/Code/labours-go`), a mature Go port with all 24 modes wired. It
  produced the MeKo project/repo burndown charts.
- **The proto is hand-synced.** `labours-go/pb.proto` is a manual copy of
  `internal/pb/pb.proto`; they drift silently. Co-locating removes the sync burden.
- **Two-tool friction.** A Python dependency (`python/`, `pb-python`, `install-labours`, `uv`) is
  required just to draw charts. A native Go renderer removes that whole install path.

## Decisions (confirmed 2026-07-02)

These four were confirmed as the target. Note on protobuf: either runtime is acceptable — gogo
(`internal/pb`) is the default path, but settling on google-protobuf generated from the one
canonical `.proto` is equally fine. The requirement is that exactly **one** proto source and one
runtime remain (see Phase 5).

| Decision | Chosen | Alternative |
| --- | --- | --- |
| Layout | One module, **two binaries** (`cmd/hercules` + `cmd/labours`), `report` renders in-process. Renderer under `internal/render/…`. | Single binary: rendering as `hercules render` subcommand, no standalone `labours`. |
| Python `labours` | Keep as **fallback during transition, remove after parity**. | Remove immediately / keep both indefinitely. |
| Render-stack deps | **Publish/tag** the `cwbudde/*` modules, drop `replace` dirs, vendor as usual. | Vendor the source into hercules / keep local `replace ../…`. |
| Protobuf | Migrate render readers to hercules' **gogo `internal/pb`**; delete the duplicate. | Keep a google-protobuf copy but generate it from the one canonical `.proto`. |

## Integration challenges (context that shapes the phases)

1. **Render stack is an unpublished pure-Go matplotlib** (not gonum — the labours-go README is
   stale). It renders via `github.com/cwbudde/matplotlib-go` **v0.0.0** + `agg_go` / `algo-fft` /
   `mathtext` / `qhull-go` + dejavu fonts, all wired by local `replace ../…` directives. These must
   be resolved into hercules' vendored module graph.
2. **Two protobuf runtimes.** hercules uses **gogo/protobuf** (`internal/pb/pb.pb.go` via
   protoc-gen-gogo). labours-go uses **google.golang.org/protobuf** on its own copy. One module
   cannot regenerate the same package twice → readers migrate to the gogo types.
3. **Two cobra versions.** hercules `spf13/cobra v0.0.3`; labours-go `v1.10.2` + viper. One module
   = one version → hercules' CLI moves to cobra v1.x.
4. **Module rename.** Every `labours-go/…` import + the proto `go_package` becomes
   `github.com/meko-christian/hercules/…`.
5. **No LICENSE in labours-go.** This repo is Apache-2.0; the merged code needs headers/NOTICE.
6. **Downstream.** `ewws-statistics/scripts/generate-burndown.sh` invokes `../../labours-go/labours`
   and `hercules combine`; the `labours` binary name/interface must keep working after the merge.

## Phases

Each phase is independently buildable and testable. `just` targets refer to this repo's `justfile`.

### Phase 0 — Prep (no code moves) ✅ done 2026-07-02

- [x] Relicense the labours-go code under Apache-2.0 (add license headers / NOTICE) to match this repo. -> not needed, I'm code owner of both repos and can relicense.
- [x] `diff labours-go/pb.proto internal/pb/pb.proto`; record any drift and reconcile to
      `internal/pb/pb.proto` as the single source of truth.
  - Result: **zero message drift** — all messages, fields, and field numbers are identical.
    Only the header differs: labours-go's copy has `package pb;` +
    `option go_package = "labours-go/internal/pb";`, hercules' has neither. Wire-compatible;
    resolves itself in Phase 5 when the duplicate copy is deleted.
- [x] Inventory the render stack: confirm each `cwbudde/*` module builds standalone and decide its tag.
  - `matplotlib-go`: was ~20 commits past v0.2.0, only local `replace` for qhull-go → needed a tag.
  - `agg_go`: HEAD == published v0.3.2 (the go.mod comment "local ahead of v0.2.31" was stale) → no action.
  - `algo-fft`: ~20 commits past v0.6.11, but HEAD's test suite didn't compile → needed cleanup + tag.
  - `mathtext` v0.4.4, `qhull-go` v0.1.0: HEAD == published tag → no action.
  - GitHub note: the repos canonically live under `CWBudde/*`, so the `github.com/cwbudde/*`
    module paths are correct. The local matplotlib-go clone still pointed at the old
    `MeKo-Christian/matplotlib-go` URL (a GitHub redirect); its origin has been repointed to
    `cwbudde/matplotlib-go`. The v0.3.0 tag is on the canonical repo and resolves via the proxy.

### Phase 1 — Publish/tag the render stack ✅ done 2026-07-02

- [x] Tag real versions of `matplotlib-go` (currently v0.0.0), `agg_go`, `algo-fft`, `mathtext`,
      `qhull-go`; verify each builds with no sibling `replace`. Prerequisite for de-replacing.
  - `matplotlib-go`: dropped the redundant `replace qhull-go => ../qhull-go` (local qhull == pushed
    v0.1.0), `go mod tidy`, full build + vet + test green → tagged and pushed **v0.3.0**.
  - `algo-fft`: HEAD's tests didn't compile (a kernel-refactor renamed `dit_sizeN_*` test files to
    `dit_N_*` and removed the Mixed24 kernel family, leaving 30+ stale/duplicate test files behind).
    Removed the stale files, kept the one surviving mixed-radix test (moved its tolerance constant
    in), full suite green → tagged and pushed **v0.6.12** (test-only change; library code identical
    to v0.6.11).
  - `agg_go` v0.3.2, `mathtext` v0.4.4, `qhull-go` v0.1.0: already published and current → no action.
- [x] Acceptance: `go build` of `labours-go` succeeds against the tagged versions (temporarily
      dropping its `replace` directives).
  - labours-go go.mod: all three `replace` directives removed, `matplotlib-go v0.0.0 → v0.3.0`,
    tidied. Build green with `CGO_ENABLED=0` and with `-tags purego`; full test suite passes with
    `CGO_ENABLED=0`. (Left uncommitted — labours-go has unrelated WIP in the working tree.)
  - ⚠ Finding for Phases 2/4: matplotlib-go's default **cgo** build compiles a FreeType binding
    whose vendored FreeType (`third_party/freetype/prefix`) is git-ignored — the published module
    only builds with `CGO_ENABLED=0`, `-tags purego`, or `-tags systemfreetype` + system FreeType.
    Decision: hercules builds the renderer **non-cgo** (`CGO_ENABLED=0`, matching the cgo-free
    release goal); the justfile should pin this. If a cgo build is ever needed (e.g. matplotlib-go
    development), use `-tags systemfreetype` (system FreeType via pkg-config) — never the
    git-ignored vendored copy.

### Phase 2 — Add render deps to hercules' module graph (deps only)

- [ ] Add the published render modules to `go.mod`; `go mod tidy` + `just vendor`.
- [ ] Acceptance: `just` (hercules build) still succeeds and `just check-tidy` passes. No renderer
      code yet — isolates dependency risk.

### Phase 3 — Unify cobra/viper

- [ ] Upgrade `spf13/cobra v0.0.3 → v1.10.2` (+ pflag, + viper). Fix v0→v1 API breaks in
      `cmd/hercules/{root,combine,report,generate_plugin}.go` and the dynamic flag registration
      (`hercules.Registry.AddFlags`).
- [ ] Acceptance: `just test` green; `./hercules --help`, `combine`, `report`, `version` behave as before.

### Phase 4 — Move the renderer source into hercules

- [ ] Copy labours-go `internal/{readers,modes,graphics,burndown,progress}` → `internal/render/…`
      and `cmd/` → `cmd/labours/`.
- [ ] Rewrite imports `labours-go/… → github.com/meko-christian/hercules/internal/render/…` (and
      `.../cmd/labours`).
- [ ] Build `cmd/labours` as a second binary; port the labours-go `_test.go` files.
- [ ] Acceptance: `go build ./cmd/labours` and `go test ./...` green; `./labours --help` works.

### Phase 5 — Collapse onto a single proto source/runtime

- [ ] Default path: rewrite `internal/render/readers/pb_reader.go` to unmarshal hercules' **gogo**
      `pb.AnalysisResults` from `internal/pb`; drop the google-protobuf copy + dependency.
- [ ] Equally acceptable: standardize on **google-protobuf** instead, generated from
      `internal/pb/pb.proto` in the justfile (kills the hand-sync either way). Pick whichever
      proves less noisy — one source of truth and one runtime is the requirement.
- [ ] Re-run reader golden/extraction tests.
- [ ] Acceptance: reader tests pass; only one `.proto` remains in the tree and only one protobuf
      runtime is in `go.mod`.

### Phase 6 — Wire `hercules report` to the in-process renderer

- [ ] Change `resolveLaboursCommand` / the mode loop in `cmd/hercules/report.go` to call the render
      package directly (no subprocess) by default; keep Python fallback via `--labours-cmd`.
- [ ] Acceptance: `./hercules report --all -o ./report <repo>` renders every mode with no external
      `labours` on PATH.

### Phase 7 — Build system, CI, Docker

- [ ] justfile: add a `labours` build recipe and fold it into `default`/`test`; keep `pb-python` and
      `install-labours` behind a flag until Python removal.
- [ ] Update `Dockerfile`, `action.yml`, and `.golangci.toml` excludes for the moved graphics files;
      re-vendor.
- [ ] Acceptance: `just`, `just test`, `just check-formatted`, `just check-tidy` all pass; the
      Docker image builds and runs `hercules report`.

### Phase 8 — Parity gate

- [ ] Run the labours-go parity harness (`LABOURS_GO_VISUAL_PARITY=1`, the showcase script) from
      inside hercules on the SIVA fixture + one real repo.
- [ ] Acceptance: `hercules report --all --strict` = zero hard mode errors; RMSE within the
      labours-go `PLAN.md` matrix bounds. This gates Phase 9.

### Phase 9 — Retire Python labours + cleanup

- [ ] Delete `python/`, the `pb-python` and `install-labours` recipes, and the Python fallback path.
- [ ] Remove the stray root `labours-go` ar-archive artifact.
- [ ] Update `README.md` and `AGENTS.md` (two-tool model → one repo, Go renderer); migrate
      the labours-go parity matrix into `docs/`.
- [ ] Add a README pointer in (and archive) the old `labours-go` repo.

### Phase 10 — Downstream follow-up (tracked, not blocking the merge)

- [ ] Point `ewws-statistics/scripts/generate-burndown.sh` (`LABOURS_BIN`) and the `setup-hercules`
      / `install-labours` Justfile recipes at the hercules-built `labours` binary.

## Verification (run after Phase 6, then again after Phase 9)

1. `just` builds hercules; `go build ./cmd/labours`; `just test` / `go test ./...` green;
   `just check-tidy` + `just check-formatted` pass; `just vendor` is clean.
2. Drop-in still works:
   `./hercules --burndown --pb <repo> | ./labours -f pb -m burndown-project -o /tmp/b.png`.
3. Native path (no Python on PATH):
   `./hercules report --all --strict -o /tmp/report <repo>` completes with zero hard mode errors.
4. Parity: run the visual-parity/showcase harness on the SIVA fixture; RMSE within bounds; spot-check
   `burndown-project` and `burndown-repos-combined` visually.
5. Downstream: re-run `ewws-statistics` `just burndown-clean` against the merged `labours`; confirm
   the MeKo project & repo burndown charts regenerate.

## Critical files

- This repo: `go.mod`/`go.sum`, `justfile`, `Dockerfile`, `action.yml`, `.golangci.toml`,
  `cmd/hercules/{root,report,combine,generate_plugin}.go`, `internal/pb/`, `README.md`,
  `AGENTS.md`; new `internal/render/…`, `cmd/labours/…`.
- Source to move (`/mnt/projekte/Code/labours-go`): `cmd/`,
  `internal/{readers,modes,graphics,burndown,progress}`, `pb.proto`, `PLAN.md` (parity matrix).
- Render stack: `../matplotlib-go`, `../agg_go`, `../algo-fft`, `../mathtext`, `../qhull-go`.
- Downstream: `ewws-statistics/scripts/generate-burndown.sh`, `ewws-statistics/Justfile`.

## Part B: Remaining roadmap work (carried over from ROADMAP.md)

These phases are independent of the renderer integration and can be worked in any order.
Priorities follow the former roadmap model: **P1** = scale & contracts, **P2** = finish partial
features, **P3** = nice-to-have.

### Phase 11 — Validate or demote advanced pipeline features (P1)

Why: advanced pipeline behavior (non-linear histories, plugins) is currently neither covered by
tests nor explicitly marked experimental.

- [ ] Add merge-tracking correctness tests that exercise non-linear histories.
- [ ] Add a plugin compatibility smoke test that loads a minimal plugin.
- [ ] Decide whether each advanced path is supported or experimental, then document that status
      in README/help text.
- [ ] Acceptance: advanced pipeline behavior is either covered by tests or explicitly documented
      as experimental.

### Phase 12 — PB schema versioning & CI guardrails (P1)

Why: stable tooling needs stable schemas; otherwise every downstream consumer is fragile.

Already done: schema version policy defined, compatible-vs-breaking PB changes documented,
changelog entry format added, schema snapshot + CI check that flags PB schema changes in place.
Output structures and examples are documented in [docs/SCHEMAS.md](docs/SCHEMAS.md).

- [ ] Define where the current schema version is emitted.
- [ ] Reserve removed field numbers/names in `.proto` files.
- [ ] Require an explicit version bump and changelog entry for breaking changes in CI.
- [ ] Acceptance: PB changes can be reviewed against the written compatibility policy, and
      accidental breaking PB changes fail CI while intentional changes point to the
      version/changelog decision.

Note: coordinate with **Phase 5** — versioning applies to whichever single proto source survives.

### Phase 13 — Decide on JSON export mode (P1)

- [ ] Confirm whether downstream users need native JSON or whether YAML/PB is enough.
- [ ] If needed: add `--json` output for direct consumption (not via labours) and provide JSON
      Schemas per analysis.
- [ ] Acceptance: either JSON is documented and stable, or this plan records why it is deferred.

### Phase 14 — Fix broad `just test` fixture failures (P2)

Release hygiene is otherwise complete, but the broad `just test` gate still fails in existing
fixture-sensitive tests:

- [ ] `internal/core` pipeline fixture expectations.
- [ ] `internal/linehistory/TestLinesMeta`.
- [ ] Acceptance: `just test` passes clean; the pre-tag caveat recorded during release prep is
      removed.

### Phase 15 — Sentiment: mark as experimental everywhere (P3)

`--help` already includes a caveat. Remaining:

- [ ] CLI: prefix sentiment outputs with `[EXPERIMENTAL]`.
- [ ] Renderer: add a subtitle warning on sentiment charts (in the Go renderer once Part A
      Phase 9 lands; until then, Python labours).

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

- **Code review metrics**: requires GitHub/GitLab API integration; Git history alone is
  insufficient. Revisit when an `internal/platform/` abstraction exists.
- **Remote repository cloning support (HTTPS/SSH)**: nice to have, but not core to correctness;
  can be handled externally by cloning locally. Revisit after scaling presets and schema
  contracts are solid.
- **Caching**: still deferred until repeated real-world workloads show a stable bottleneck;
  premature caching risks wrong-by-default behavior. Revisit after schema contracts and release
  hygiene are in place.
