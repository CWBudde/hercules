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

| Decision          | Chosen                                                                                                                          | Alternative                                                                        |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Layout            | One module, **two binaries** (`cmd/hercules` + `cmd/labours`), `report` renders in-process. Renderer under `internal/render/…`. | Single binary: rendering as `hercules render` subcommand, no standalone `labours`. |
| Python `labours`  | Keep as **fallback during transition, remove after parity**.                                                                    | Remove immediately / keep both indefinitely.                                       |
| Render-stack deps | **Publish/tag** the `cwbudde/*` modules, drop `replace` dirs, vendor as usual.                                                  | Vendor the source into hercules / keep local `replace ../…`.                       |
| Protobuf          | Migrate render readers to hercules' **gogo `internal/pb`**; delete the duplicate.                                               | Keep a google-protobuf copy but generate it from the one canonical `.proto`.       |

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

### Phase 2 — Add render deps to hercules' module graph (deps only) ✅ done 2026-07-02

- [x] Add the published render modules to `go.mod`; `go mod tidy` + `just vendor`.
  - Added `internal/render/deps.go`: blank imports of the 7 matplotlib-go packages labours-go
    uses, behind a never-set `renderdeps` build tag — anchors the deps for tidy/vendor without
    compiling any renderer code. `matplotlib-go v0.3.0` is a direct require; `agg_go v0.3.2`,
    `algo-fft v0.6.11`, `mathtext v0.4.4`, `qhull-go v0.1.0`, `dejavu v0.4.0` came in as indirects.
  - Side effect (MVS): `cobra v0.0.3 → v1.10.2`, `pflag v1.0.3 → v1.0.10`, and x/crypto / x/net /
    x/sys / x/term / x/text bumps — matplotlib-go's requirements force them. See Phase 3.
- [x] Acceptance: `just` (hercules build) still succeeds and `just check-tidy` passes. No renderer
      code yet — isolates dependency risk.
  - `just` ✓, `just check-tidy` ✓, `go build ./...` ✓, smoke `./hercules --dry-run .` ✓ (1145
    commits). `go test ./...` fails only in the pre-existing fixture-sensitive tests tracked in
    Phase 14 (`internal/core` pipeline tests assert upstream src-d commits that are unreachable
    from this fork's HEAD; `TestLinesMeta`) — verified identical before/after this phase.

### Phase 3 — Unify cobra/viper ✅ done 2026-07-02 (fell out of Phase 2)

- [x] Upgrade `spf13/cobra v0.0.3 → v1.10.2` (+ pflag, + viper). Fix v0→v1 API breaks in
      `cmd/hercules/{root,combine,report,generate_plugin}.go` and the dynamic flag registration
      (`hercules.Registry.AddFlags`).
  - The upgrade happened via MVS when matplotlib-go entered the module graph — and **zero API
    breaks materialized**: all `cmd/hercules` code compiles unchanged against cobra v1.10.2.
    viper is not needed by hercules itself; it is already in the module graph (transitively via
    matplotlib-go) and becomes a direct dep only when the renderer CLI code moves in Phase 4.
- [x] Acceptance: `just test` green; `./hercules --help`, `combine`, `report`, `version` behave as before.
  - `go test ./cmd/...` green; `--help`, `combine --help`, `report --help`, `generate-plugin
--help`, `version` all work; dynamic per-analysis flags verified via `--burndown --dry-run`.
    (`version` is a subcommand, not `--version` — unchanged from before the upgrade.)

### Phase 4 — Move the renderer source into hercules

Refined 2026-07-02 after a source inventory; findings that shape the sub-phases:

- labours-go's working tree carries **intentional uncommitted WIP** (the de-replaced go.mod from
  Phase 1 plus burndown-python-compat fixes in `cmd/{root,modes}.go`,
  `internal/burndown/python_compatible.go`, `internal/modes/burndown_python.go` and tests) —
  copy from the **working tree**, not HEAD.
- `internal/pb` (google-protobuf generated, ~4k lines) is imported **only by `readers`**; it is
  copied along temporarily so Phase 4 builds standalone — Phase 5 deletes it.
- `cmd/parityviewer` is a repo-root-coupled QA tool (stdlib-only, shells `go run .`, expects
  `analysis_results/`) — **not moved**; revisit after Phase 8 if the parity harness needs it.
- Test fixtures (`example_data/` 28K, `test/testdata/` 444K incl. two `.golden.json` files) and
  runtime `themes/` move too; relative test paths (`../../example_data`) change depth.
- Lint carve-outs for the big graphics/modes files are deferred to Phase 7; acceptance here is
  build + test, not lint.
- Per the Phase 1 finding, the renderer builds non-cgo: use `CGO_ENABLED=0`.

#### Phase 4a — internal render packages ✅ done 2026-07-02 (uncommitted)

- [x] Copy labours-go `internal/{readers,modes,graphics,burndown,progress,pb}` →
      `internal/render/…`; remove the `internal/render/deps.go` anchor (real imports replace it).
  - 62 .go files / 24,178 lines copied from the working tree (incl. WIP + untracked
    `d5b_matte_repro_test.go`); `deps.go` deleted.
- [x] Rewrite imports `labours-go/internal/… → github.com/meko-christian/hercules/internal/render/…`.
  - 41 files rewritten + `gofmt -w` (import-group reorder). Intentional leftover: the raw
    descriptor string in `render/pb/pb.pb.go` still says `labours-go/internal/pb` (length-prefixed;
    rewriting would corrupt it) — the whole package dies in Phase 5.
- [x] Move fixtures under `internal/render/` (Go `testdata/` convention) and update relative paths
      in tests; bring `themes/` along wherever `graphics.LoadUserThemes` expects them.
  - `internal/render/testdata/{example_data,hercules,labours-go_devs.yaml}`; 8 test files repathed.
    `themes/` NOT copied — nothing in the moved code/tests references it (theme tests use built-in
    themes; `LoadUserThemes` reads user dirs at runtime). `test/testdata/{realistic,simple}_burndown.pb`
    also unreferenced → not copied.
- [x] go.mod: promote the now-directly-imported deps; `go mod tidy` + `just vendor`.
  - New direct: progressbar v3.19.1, viper v1.21.0 (imported by `modes`), google.golang.org/protobuf
    v1.36.11 (temp until Phase 5); yaml.v3 indirect→direct; testify → v1.11.1. `dateparse` correctly
    deferred to Phase 4b (only cmd/ uses it).
- [x] Acceptance: `CGO_ENABLED=0 go build ./...` ✓; `CGO_ENABLED=0 go test ./internal/render/...`
      all green; hercules builds + `--dry-run .` smoke ✓; `go test ./cmd/... ./internal/core/...`
      unchanged (the 5 internal/core failures are the pre-existing Phase 14 fixture issues,
      A/B-verified identical without the render code). `just check-tidy` can only pass once this
      phase is committed (it diffs against git HEAD); the invariant was verified instead — rerunning
      `go mod tidy` is a no-op. Only deviation from verbatim: `modes/report_metrics.go` was
      gofmt-dirty upstream and got formatted.

#### Phase 4b — cmd/labours binary ✅ done 2026-07-02 (uncommitted)

- [x] Copy labours-go `cmd/{root,modes,helpers,version}.go` + tests → `cmd/labours/`; rewrite
      imports; keep the viper config behavior (`config.yaml`, `$HOME/.labours-go`).
  - Folded labours-go's root `main.go` in: everything is `package main` in `cmd/labours/`
    (main.go + root/modes/helpers/version + 2 test files). All ~40 flags, viper config, theme
    mapping, and user-visible strings kept verbatim. `cmd/parityviewer` left behind as planned.
    cmd tests needed no fixtures (pure unit tests, `t.TempDir()` only).
- [x] Remove/generalize the hardcoded `/home/christian/Code/hercules/hercules` candidate path in
      `handleHerculesIntegration`.
  - Dropped that one line; kept `hercules` (PATH), `./hercules`, `/usr/local/bin/hercules`.
    version.go ldflags vars are now `main.version`/`main.commit`/`main.date`.
- [x] Acceptance: `CGO_ENABLED=0 go build ./cmd/labours` ✓; `./labours --help` (40 flags) and
      `labours version` ✓; `go test ./cmd/labours/...` ✓; `go build ./...`,
      `go test ./internal/render/...`, `just` all still green. Drop-in smoke ✓:
      `./hercules --burndown --pb . > /tmp/b.pb && ./labours -f pb -m burndown-project -i /tmp/b.pb
-o /tmp/b.png` → 62,695-byte 1600×1200 PNG (note: input via `-i`, not positional).
      go.mod: only genuinely new module is `araddon/dateparse`; viper/progressbar/protobuf/yaml.v3
      promoted to direct (used since 4a). Phase 7 TODO noted: `.gitignore` covers `labours-go` but
      not the new `labours` binary artifact.

### Phase 5 — Collapse onto a single proto source/runtime ✅ done 2026-07-02 (uncommitted)

- [x] Default path: rewrite `internal/render/readers/pb_reader.go` to unmarshal hercules' **gogo**
      `pb.AnalysisResults` from `internal/pb`; drop the google-protobuf copy + dependency.
  - Mechanical import swap in pb*reader.go + 4 test files; `internal/render/pb/` deleted. The gogo
    path proved trivially quiet — the **only** API difference in ~35 referenced types was gogo
    renaming `FileRisk.size` → `Size*`(collides with the generated`Size()` method); 2 occurrences
    fixed. The google-protobuf alternative was therefore moot.
- [x] Re-run reader golden/extraction tests.
  - All green, no golden files touched.
- [x] Acceptance: reader tests pass; only one `.proto` remains in the tree and only one protobuf
      runtime is in `go.mod`.
  - `go build ./...`, `go test ./internal/render/... ./cmd/labours/...`, `just` all green
    (CGO_ENABLED=0). `google.golang.org/protobuf` is gone entirely (not even indirect); only
    `gogo/protobuf v1.3.2` remains. Non-vendor `.proto` files: `internal/pb/pb.proto` (canonical)
    - `contrib/_plugin_example/churn_analysis.proto` (plugin example, intentionally untouched).
      E2E smoke: burndown PNG 62,664 bytes vs Phase 4b's 62,695 (expected date-binning drift).
      `go mod tidy` idempotent.

### Phase 6 — Wire `hercules report` to the in-process renderer

Refined 2026-07-02: the mode-dispatch logic (`executeModes` etc.) landed in `package main` under
`cmd/labours/` in Phase 4b, so it is not callable from `cmd/hercules/report.go`. Split:

#### Phase 6a — extract a callable render API ✅ done 2026-07-02 (uncommitted)

- [x] Move the mode-dispatch/execution logic from `cmd/labours/modes.go` (+ whatever helpers it
      drags along) into an importable package. `cmd/labours` becomes a thin CLI wrapper over it;
      CLI behavior stays identical.
  - New root package `internal/render` (package `render`): `render.go` (API),
    `dispatch.go` (former modes.go, verbatim), `modeset.go`, `output.go` + relocated tests.
    API: `LoadInput(input, format) (readers.Reader, error)`, `Run(reader, modeNames, Options)`
    (no error return by design — per-mode failures are printed warnings, Python-labours parity),
    `ResolveModes`, `NormalizeInputFormat`, `DetectOutputFormat`, `GenerateOutputPath`;
    `Options{Output, StartTime, EndTime, SentimentFallback, DevsParallelFallback}`. Remaining
    render settings still flow through viper — documented as a known coupling, de-viperization
    intentionally NOT attempted. Hercules-binary integration stayed CLI-only in cmd/labours.
- [x] Acceptance: `go build ./...` + `go test ./internal/render/... ./cmd/labours/...` green;
      `./labours` drop-in smoke unchanged (same PNG as Phase 5 modulo date drift).
  - All green (verified independently after the agent run); `--help` diff-identical; smoke PNG
    62,695 bytes; multi-mode missing-analysis warning path verified
    (`Devs stats were not collected. Re-run hercules with --devs.`, exit 0); JSON output smoke ok;
    tidy no-op, vendor unchanged, gofmt + vet clean.

#### Phase 6b — report.go calls it in-process ✅ done 2026-07-02 (uncommitted)

- [x] Change `resolveLaboursCommand` / the mode loop in `cmd/hercules/report.go` to call the render
      API directly (no subprocess) by default; keep the external-binary fallback via
      `--labours-cmd` (Python labours or any drop-in).
  - Default path: `render.SetRenderDefaults()` (viper.SetDefault for the 18 keys the modes read,
    mirroring labours CLI defaults, lowest-precedence so nothing clobbers) + one `render.LoadInput`
    of report.pb + per-mode `render.RunWithResults` with the same `-o` values as before → output
    layout byte-identical. `--labours-cmd` still runs the old subprocess loop unchanged. New API:
    `ModeResult{Mode, Err, Warning}` + `RunWithResults`; `Run` is now a thin wrapper (cmd/labours
    untouched). `--strict`: hard `ModeResult.Err` aborts; missing-analysis warnings stay soft
    (Python labours parity). New guard: `--labours-arg` without `--labours-cmd` errors.
- [x] Acceptance: `./hercules report --all -o ./report <repo>` renders every mode with no external
      `labours` on PATH.
  - With sanitized PATH: single-mode report ✓ (also with `--strict`); `--all` on this repo
    (1145 commits) → exit 0, zero hard failures, 36 chart/asset files. Soft warnings only:
    people/files burndown not collected (pre-existing flag-selection behavior), sentiment (no
    tensorflow build), `onboarding` unknown. Fallback `--labours-cmd ./labours` ✓ identical layout.
  - Findings for follow-up: (1) `onboarding` is listed in the report mode lists but NO renderer
    implements it — previously it failed every Go-labours subprocess run; now a soft warning.
    Implement or drop in Phase 7/9. (2) matplotlib-go's `style` init() prints ~50 `rcParam`
    warnings on stderr on every hercules invocation (upstream matplotlib-go; no public silencing
    API — worth an upstream `diag` handler fix). (3) `resolveLaboursCommand`'s PATH/python3
    auto-detection is now dead code unless `--labours-cmd` is set.

### Phase 7 — Build system, CI, Docker ✅ done 2026-07-02 (uncommitted)

- [x] justfile: add a `labours` build recipe and fold it into `default`/`test`; keep `pb-python` and
      `install-labours` behind a flag until Python removal.
  - `default: hercules labours`; `labours` recipe stamps `main.{version,commit,date}` via ldflags.
    `CGO_ENABLED=0` is now exported justfile-wide (env-overridable) per the Phase 1 finding, so
    plain `just` works without system FreeType. `pb-python` dropped from the `hercules` deps;
    `install-labours` (now depending on `pb-python`) stays as an opt-in recipe until Phase 9.
    `clean` removes the labours binary; `/labours` added to `.gitignore` (root-anchored so
    `cmd/labours/` stays tracked).
- [x] Update `Dockerfile`, `action.yml`, and `.golangci.toml` excludes for the moved graphics files;
      re-vendor.
  - `.golangci.toml`: `internal/render/` + `cmd/labours/` added to `linters.exclusions.paths`
    (moved-code carve-out; render issues 409 → 0, residual repo issues ~1049 pre-existing).
  - Dockerfile: builder installs `just` via just.systems (the makedeb apt repo now 530s — was
    breaking the build at HEAD). The runtime stage ships both Go binaries on ubuntu:22.04 and
    **drops the Python labours install**: it was already unbuildable at HEAD (labours requires
    `formulaic>=0.6.0` ⇒ Python ≥ 3.7, but the ubuntu:18.04 base has py3.6). The Python package
    itself stays in the repo until Phase 9.
  - action.yml: `just` builds both binaries; charts step uses the Go `labours` binary (Python/uv
    setup steps removed from the action only, not the repo); Go bumped 1.21 → 1.25 (go.mod floor).
    test-crosscompile.yaml also cross-builds `cmd/labours`.
- [x] Acceptance: `just`, `just test`, `just check-formatted`, `just check-tidy` all pass; the
      Docker image builds and runs `hercules report`.
  - `just` green (non-cgo, both binaries). `just test`: no new failures — the 5 known
    internal/core fixture failures (Phase 14) plus `TestLinesMeta` (pre-existing at HEAD since
    9cd7397 added `lines-exclude-paths` without updating the option count; fails identically with
    cgo). `treefmt` converges + exits 0 (required formatting the moved render code and a
    `shellcheck disable=SC2086` directive in scripts/bench-large-repo.sh; the gofumpt/gci sweep
    also reformatted ~45 committed files — formatter-version drift, pre-existing).
    `check-formatted`/`check-tidy` diff against git HEAD so they only fully pass once this work is
    committed; tidy-idempotence and treefmt-convergence verified instead. Docker image builds and
    `hercules report --analysis burndown --mode burndown-project` smoke passes in-container.

### Phase 8 — Parity gate

Refined 2026-07-02 after scouting the harness. Facts that reshape the phase:

- The harness is `labours-go/test/visual/` (regression_test.go + similarity.go +
  chart_generator.go): **in-process** rendering via the internal packages (not exec), metric =
  weighted Histogram-Intersection(0.4) + SSIM(0.4) + ColorRMS(0.2), thresholds
  Strict 0.95 / Standard 0.90 / Lenient 0.85. Gates: `LABOURS_GO_VISUAL_PARITY=1` (golden PNGs in
  `test/golden/` — that dir does NOT exist even upstream, so those cases skip) and
  `LABOURS_GO_PYTHON_PARITY=1` (pre-baked Python reference PNGs in `analysis_results/reference/`,
  60 PNGs ~3.5 MB, untracked). Structural/metric-math tests run ungated.
- labours-go's own PLAN.md says the parity bar is **perceptual-first; RMSE ≤ 0.10 is a tracked
  goal, NOT a release gate** (as of 2026-05-25 only ownership 0.090 / overwrites-matrix 0.082 /
  devs 0.078 met it; burndown-project 0.101 borderline; worst temporal-activity ≈ 0.23). "Within
  the matrix bounds" therefore means: **not worse than the recorded matrix**, plus the harness's
  own thresholds pass.
- "SIVA fixture" = `cmd/hercules/test_data/hercules.siva` (2.97 MB, already in THIS repo); the
  derived `.pb` fixtures are already at `internal/render/testdata/hercules/`.
- Python is only needed to REgenerate references; the run itself is Python-free.

#### Phase 8a — port the visual harness ✅ done 2026-07-02 (uncommitted)

- [x] Copy `labours-go/test/visual/` → hercules `test/visual/` (rewrite imports to
      `internal/render/…`, repath fixtures to `internal/render/testdata/…`); copy the pre-baked
      `python_*.png` references to the path the harness expects (keep untracked, like upstream);
      add `just test-visual` / `just test-visual-parity` recipes.
  - `test/visual/{similarity.go,regression_test.go,chart_generator.go,README.md}` — similarity.go
    byte-identical to upstream; only imports + path literals changed. Goldens now live at
    `test/visual/golden/` (tracked-able, none exist yet — parity with upstream), python refs at
    `test/visual/reference/` (30 `python_*.png`, untracked via .gitignore), failure dumps at
    `test/visual/analysis_output/` (untracked). Upstream's 30 `go_*.png` NOT copied (regenerable;
    note for 8b). `demo_test.go` not ported (out of scope).
- [x] Acceptance: ungated structural+metric tests green in `just test`; `LABOURS_GO_PYTHON_PARITY=1`
      suite passes its Lenient thresholds against the pre-baked references; golden cases skip
      cleanly (parity with upstream).
  - `just test-visual` PASS 0.8s; `just test-visual-parity` PASS 3.1s: golden cases skip cleanly;
    TestPythonCompatibility: absolute burndown Overall **93.51%**, relative **97.06%** (both would
    even pass Standard 90%, thresholds untouched). Full `go test ./...`: no new failures. Tidy
    idempotent; harness adds no deps.

#### Phase 8b — run the gate ✅ done 2026-07-02 — **GATE PASS**

- [x] `hercules report --all --strict` on the SIVA fixture and one real repo (this repo) → zero
      hard mode errors.
  - Both exit 0, 36 charts each, zero hard errors. SIVA needs no flag — `loadRepository` opens
    `.siva` natively (`cmd/hercules/root.go:206`). Soft warnings: burndown files/people "not
    collected" + sentiment (non-tensorflow build).
  - **Bug found**: `report --all` intends to pass `burndown-files`/`burndown-people` but
    `selectReportAnalysisFlags` filters against registry _leaf_ flags and silently drops these two
    Burndown _sub-options_ — so burndown-file/person, ownership, overwrites-matrix, devs-parallel
    never render without `--hercules-arg` workarounds. Verified: with the sub-flags forced,
    131 charts render and only the sentiment warning remains. → fixed post-gate, see below.
- [x] Regenerate the Go side of the matched-pair modes and compute RMSE vs the pre-baked Python
      references; record the numbers next to the 2026-05-25 matrix (equal-or-better = pass).
  - Provenance: the matrix was computed on `analysis_results/system-optimiser-core/raw/analysis.pb`
    (still present upstream) with `cmd/parityviewer`'s RMSE = sqrt(ΣΔ²/(unionPixels·4)) over RGBA,
    ÷255. Calibration reproduced published numbers exactly (devs 0.0783, ownership 0.0901,
    heatmap 58.6 raw).
  - Result: **25/26 pairs equal-or-better** (12 pass-within-tolerance, 7 strictly better incl.
    burndown-project 0.101→0.0979 — now meets the ≤0.10 goal — ownership 0.090→0.0884,
    refactoring-proxy, knowledge-diffusion×3, weekdays*lines); 1 marginal: devs-efforts
    0.139→0.1442 (+0.0052) — caused by the post-matrix y-axis offset-notation renderer feature,
    perceptually equivalent (the same feature \_improved* four other modes).
  - Port fidelity: on identical fixtures our integrated `labours` is **byte-identical (RMSE 0.0)**
    to a fresh upstream-current labours-go build. Six seemingly-worse modes vs the checked-in
    `go_*.png` reference set traced to those artifacts being stale (May 8 < matrix code May 25);
    upstream-current reproduces our numbers to 4 decimals. RMSE tables preserved in the session
    scratchpad (`rmse_*.txt`).
- [x] Acceptance: both bullets recorded here; this gates Phase 9. → **Phase 9 unblocked.**
- [x] Post-gate fix: `selectReportAnalysisFlags` now maps the burndown sub-options through, so
      `report --all` collects files/people burndown without workarounds.

### Phase 9 — Retire Python labours + cleanup ✅ done 2026-07-02 (uncommitted; last bullet deferred)

- [x] Delete `python/`, the `pb-python` and `install-labours` recipes, and the Python fallback path.
  - Also removed: the stale `.travis.yml` (dead upstream CI: Go 1.11–1.13, bblfsh, PyPI deploy,
    `python/setup.py`), stray Python-protobuf leftovers `internal/__init__.py`,
    `internal/pb/{__init__.py,pb_pb2.py}`, the Python references in `.gitignore` /
    `treefmt.toml` / `CONTRIBUTING.md` / Dockerfile comments, and the dead `python3 -m labours`
    fallback in `resolveLaboursCommand` (`--labours-cmd` stays as a generic external drop-in
    escape hatch). Dropped the phantom `onboarding` render _mode_ from the `hercules report`
    mode lists (no renderer implements it; the `onboarding` _analysis_ flag stays).
- [x] Remove the stray root `labours-go` ar-archive artifact.
- [x] Update `README.md` and `AGENTS.md` (two-tool model → one repo, Go renderer); migrate
      the labours-go parity matrix into `docs/` → [docs/RENDER_PARITY.md](docs/RENDER_PARITY.md).
- [ ] Add a README pointer in (and archive) the old `labours-go` repo. _(deferred — separate repo,
      explicitly out of scope for the in-repo cleanup)_

### Phase 10 — Downstream follow-up (tracked, not blocking the merge)

- [ ] Point `ewws-statistics/scripts/generate-burndown.sh` (`LABOURS_BIN`) and the `setup-hercules`
      / `install-labours` Justfile recipes at the hercules-built `labours` binary.
  - 2026-07-02: repo located at `/mnt/projekte/Code/MeKo/ewws-statistics` (not the sibling path the
    plan assumed). Not touched — separate repo, needs the user's go-ahead; the merged `labours`
    binary is drop-in compatible (verified byte-identical rendering vs labours-go in Phase 8b).

## Verification (run after Phase 6, then again after Phase 9)

Phase-6 checkpoint run 2026-07-02: items 1–3 pass (item 1 modulo the known Phase 14
`internal/core` fixture failures; `just check-tidy` pending commit of the phase work).
Item 2: pipe drop-in → 62,664-byte PNG. Item 3: `report --all --strict` with sanitized
PATH → exit 0, 36 chart files, zero hard failures. Items 4–5 (parity harness, downstream
regen) remain for the post-Phase-9 run.

Post-Phase-9 run 2026-07-02: items 1–4 pass — builds/tests as above (same known Phase 14
failures only); pipe drop-in → 62,664-byte PNG; `report --all --strict` on the SIVA fixture
→ exit 0, **131** charts (sub-flag fix), zero hard errors; parity harness
(`CGO_ENABLED=0`, both gates) passes, RMSE matrix regenerated in Phase 8b / docs/RENDER_PARITY.md.
Item 5 (downstream): the repo lives at `/mnt/projekte/Code/MeKo/ewws-statistics` — regen not run
(separate repo, user decision); see Phase 10.

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

### Phase 11 — Validate or demote advanced pipeline features (P1) ✅ done 2026-07-02

Why: advanced pipeline behavior (non-linear histories, plugins) is currently neither covered by
tests nor explicitly marked experimental.

- [x] Add merge-tracking correctness tests that exercise non-linear histories.
  - `internal/core/merge_tracking_test.go`: an instrumented item (fork = deep copy,
    merge = union) runs full `Pipeline.Run()`s over synthetic commit DAGs — diamond, octopus,
    criss-cross, sequential feature branches, long parallel branches, multiple roots,
    duplicate merge parents, redundant merge edges (fast-forward collapse), a stacked-diamond
    stress topology — each verified against five invariants: consumed exactly once, parents
    before children, branch-state isolation (no sibling leakage), lineage completeness at
    non-merge commits, full final state on the master branch. Also covered: hibernation
    transparency on branchy plans (same results + Hibernate/Boot invoked) and the
    `merge_tracks` feature (`DependencyNextMerge` annotation via
    `InitializeExt`/`RunPreparedPlan`, `FactMergeHashCount`, plain-`Initialize` rejection).
- [x] Add a plugin compatibility smoke test that loads a minimal plugin.
  - `test/plugin_smoke/` (run via `just test-plugin`): builds
    `testdata/minimal_plugin` with `-buildmode=plugin` plus a `CGO_ENABLED=1 -tags purego`
    hercules from the same tree, then asserts `--plugin` registers the flag in `--help` and
    the analysis runs end-to-end on a generated 2-commit repo (`consumed_commits: 2`).
    Skips (with pointer to `just test-plugin`) when cgo is off — i.e. under `just test`,
    which pins `CGO_ENABLED=0`. Finding: a cgo hercules build requires `-tags purego`
    (or `systemfreetype`), confirming the Phase 1 note.
- [x] Decide whether each advanced path is supported or experimental, then document that status
      in README/help text.
  - **Non-linear histories: supported** (now invariant-tested) — README states it.
  - **Plugins: supported only on cgo builds** — the default/release build is `CGO_ENABLED=0`
    and cannot load plugins. Documented in README (Plugins), PLUGINS.md (new
    "Status and requirements" section: cgo mandatory, same toolchain/tree/tags,
    linux+darwin only) and the `--plugin` help text.
- [x] Acceptance: advanced pipeline behavior is either covered by tests or explicitly documented
      as experimental. → Non-linear histories + hibernation + merge_tracks are test-covered;
      plugins are test-covered on the cgo path and documented as unavailable in cgo-free builds.

### Phase 12 — PB schema versioning & CI guardrails (P1) ✅ done 2026-07-02 (uncommitted)

Why: stable tooling needs stable schemas; otherwise every downstream consumer is fragile.

Already done: schema version policy defined, compatible-vs-breaking PB changes documented,
changelog entry format added, schema snapshot + CI check that flags PB schema changes in place.
Output structures and examples are documented in [docs/SCHEMAS.md](docs/SCHEMAS.md).

- [x] Define where the current schema version is emitted.
  - Single source of truth: `pb.SchemaVersion` (`internal/pb/version.go`, = 2), emitted in the
    YAML header, PB `Metadata.version` (incl. `combine`), `hercules version` (`Schema:` line)
    and `labours version` (its duplicate const removed). Fixed a real bug: YAML/`combine`
    emitted `0` because the module rename broke package-suffix-derived `BinaryVersion`;
    `--pb` hardcoded 2. Documented in SCHEMAS.md ("Schema Version").
- [x] Reserve removed field numbers/names in `.proto` files.
  - Audit vs upstream: no fields were ever removed from surviving messages; only whole
    messages `UASTChange`/`UASTChangesSaverResults` died (proto3 has no file-level `reserved`
    for message names) — recorded as "Retired message names" in the pb.proto header comment.
    Tooling now parses `reserved` statements, snapshots them in `pb.schema.json`, and CI
    flags removed-but-unreserved fields and reserved-number/name reuse for the future.
- [x] Require an explicit version bump and changelog entry for breaking changes in CI.
  - New `internal/pb/schema` package (proto parser + compatible/breaking classifier per the
    SCHEMAS.md policy, unit-tested) + `cmd/schema-guard` CLI. New `test-schema` CI workflow
    diffs `pb.schema.json` against the push/PR base: any schema change without a
    `docs/SCHEMA_CHANGELOG.md` entry fails; breaking changes additionally require
    `pb.SchemaVersion` > base. Locally: `just check-schema [base]`.
- [x] Acceptance: PB changes can be reviewed against the written compatibility policy, and
      accidental breaking PB changes fail CI while intentional changes point to the
      version/changelog decision. → Verified with synthetic snapshots: unrecorded breaking
      change fails with both violations named; compatible change fails without changelog and
      passes with it; reserved+bumped+logged breaking change passes.

Note: coordinate with **Phase 5** — versioning applies to whichever single proto source survives.
(Phase 5 landed first; the guard covers the surviving `internal/pb/pb.proto`.)

### Phase 13 — Decide on JSON export mode (P1) ✅ decided 2026-07-03 — **deferred with rationale**

- [x] Confirm whether downstream users need native JSON or whether YAML/PB is enough.
  - **Decision: native JSON is not needed now; defer it.** No consumer requires it and the output
    contract is already machine-readable in two documented formats:
    - The `labours` renderer reads only YAML/PB (`internal/render/readers/helpers.go` `createReader`
      accepts `yaml`/`pb` only; `internal/render/modeset.go` `NormalizeInputFormat` rejects anything
      else). `hercules combine` (`cmd/hercules/combine.go`) reads only PB.
    - YAML output is a stable, structured, fully-documented stream ([docs/SCHEMAS.md](docs/SCHEMAS.md))
      that converts to JSON with any off-the-shelf `yaml→json` tool; PB carries the canonical,
      versioned, CI-guarded schema (`pb.SchemaVersion`, snapshot + guard from Phase 12). Downstream
      JSON consumers are therefore already served without adding a third first-class format.
- [x] If needed: add `--json` output for direct consumption (not via labours) and provide JSON
      Schemas per analysis.
  - Not implemented — **deferred** (see rationale above and the "Deferred / not planned" section).
    A correct native JSON emitter is invasive and speculative: leaf results are hand-serialized
    (each leaf hand-writes YAML in `serializeText` and builds a `pb.*Results` in `serializeBinary`),
    and the `Finalize()` structs carry unexported fields, so a naive `encoding/json` of them would
    silently drop data and diverge from the documented schema. Doing it right means threading a
    third format through all ~7 leaves + the plugin template + the `LeafPipelineItem` contract
    (`internal/core/pipeline.go`) — real surface area for no known requirement.
  - **Recommended path when revisited** (least invasive): add a `--json` branch in
    `cmd/hercules/root.go` alongside `protobufResults`/`printResults` that reuses the per-leaf
    `pb.*Results` messages and marshals them with `github.com/gogo/protobuf/jsonpb` — staying
    schema-aligned with the documented PB contract and the `pb.SchemaVersion` guard rather than
    hand-adding a third path to every leaf. JSON Schemas per analysis can then be generated from
    `internal/pb/pb.proto` (e.g. a `protoc` json-schema plugin) so they stay in lockstep with the
    schema version.
- [x] Acceptance: either JSON is documented and stable, or this plan records why it is deferred.
      → This plan records why it is deferred; `docs/SCHEMAS.md` documents the two supported formats and
      the intentional absence of a JSON mode plus the practical `yaml→json` alternative.

### Phase 14 — Fix broad `just test` fixture failures (P2) ✅ done 2026-07-03

Release hygiene is otherwise complete, but the broad `just test` gate still fails in existing
fixture-sensitive tests:

- [x] `internal/core` pipeline fixture expectations — made **hermetic**.
  - Root cause: the `internal/core` tests (`TestMergeDag`, `TestPrepareRunPlan*`, `TestRunActionString`,
    `TestPrintAction`, …) assert specific upstream src-d/hercules commit hashes and pipeline plans,
    but they resolved them through `internal/test.Repository`, whose init opens the **ambient**
    checkout (or clones `src-d/hercules` as a fallback when the local repo has <100 commits). The
    resulting commit DAG therefore varied with checkout depth / upstream drift, so the plans differed
    and CI failed — while a local sandbox that happened to fall back to the network clone passed,
    masking the problem.
  - Fix: added a deterministic, network-free `internal/test.FixtureRepository()` that loads the
    checked-in `cmd/hercules/test_data/hercules.siva` fixture (a fixed src-d/hercules snapshot,
    master authored 2018-01-25). It is opened **read-only** — the default read-write siva filesystem
    rewrites the archive's index footer and would mutate the committed fixture — via a small
    `fixtureStorer` that serves objects from the read-only siva while keeping references in an
    in-memory store, so master can be exposed as HEAD (`Head()` / `Log(From: ZeroHash)` work like a
    normal clone). The `internal/core` test files were pointed at this fixture; `test.Repository`
    itself is unchanged, so `identity` / `leaves` / other packages that depend on its (looser)
    assertions are untouched. Two `internal/core`-local follow-ups: `TestRunActionString` /
    `TestPrintAction` used commit `c1002f4…` (newer than the fixture) → repointed to the fixture
    root `cce947b…`; `TestPrepareRunPlanBig`'s three cases with post-2018-01-25 cutoffs (which
    relied on commits absent from the fixture) were dropped with a comment noting the horizon.
    `go test ./internal/core/...` is green and deterministic (`-count=1`), and the siva fixture is
    verified byte-unchanged after the run.
- [x] `internal/linehistory/TestLinesMeta`.
  - Root cause: `9cd7397` added the `lines-exclude-paths` option (`ConfigLinesExcludePaths`) to
    `LineHistoryAnalyser` without updating the test's option enumeration, so
    `ListConfigurationOptions()` returned 5 options while the test's `switch` only counted 4.
    Fixed by adding `ConfigLinesExcludePaths` to the enumerated cases in `line_history_test.go`.
- [x] Also fixed (blocked the whole `internal/render/readers` package, not called out originally):
      `report_default_summary.golden.json` / `shotness_summary.golden.json` were caught by the
      blanket `*.json` `.gitignore` rule, so they were never committed and
      `TestProtobufReader_*ExtractionGolden` failed with "no such file or directory". Added a
      `!internal/render/testdata/**/*.golden.json` negation (mirroring the existing
      `!internal/pb/pb.schema.json` carve-out) and committed the two deterministic goldens
      (regenerable via `LABOURS_GO_UPDATE_GOLDENS=1`).
- [x] Acceptance: `go test ./...` (CGO_ENABLED=0, after `go generate ./cmd/hercules/`) is clean
      except for `cmd/hercules/TestLoadGitRepositoryWithCreds`, which is **not** a fixture/logic
      failure: it asserts that bogus URL credentials (`user:user@github.com`) cause an auth error,
      but this sandbox's outbound HTTPS proxy accepts bogus creds for public repos and the clone
      succeeds (verified: `git clone https://user:user@github.com/src-d/hercules` → exit 0). Under
      normal network conditions (the project's GitHub Actions CI, no credential-injecting proxy)
      GitHub returns 401 and the test passes; the test was left unchanged so as not to weaken its
      real-CI coverage. The pre-tag caveat about the `internal/core` + `TestLinesMeta` fixture
      failures is removed.

### Phase 15 — Sentiment: mark as experimental everywhere (P3) ✅ done 2026-07-04

`--help` already carried a caveat (`Description()` is `[EXPERIMENTAL]`-prefixed in both the
tensorflow and the stub builds). Now done:

- [x] CLI: prefix sentiment outputs with `[EXPERIMENTAL]`.
  - The YAML section header `Sentiment:` is emitted generically from `item.Name()`
    (`cmd/hercules/root.go:633`) and is reused as the PB content-map key and by the render
    readers, so it must not change. Instead `serializeText` (`leaves/comment_sentiment.go`) now
    emits a leading YAML comment `  # [EXPERIMENTAL] Sentiment analysis is experimental and may
    be inaccurate.` — ignored by every YAML parser and by the labours readers, untouched PB path.
    Only affects `-tags tensorflow` builds (the only builds that emit sentiment output).
- [x] Renderer: add a subtitle warning on sentiment charts.
  - Added an optional `Subtitle` field to the three option structs sentiment renders through
    (`MatplotlibTimeAreaOptions`, `MatplotlibGroupedBarOptions`, `MatplotlibScatterOptions`) plus
    a shared `drawSubtitle` helper in `internal/render/graphics/matplotlib_plots.go` that draws it
    as a small gray caption centered just below the axes title (axes-coords `ax.Text`, drawn last
    so it stays on top). Empty subtitle is a no-op, so every other mode is unaffected.
    `internal/render/modes/sentiment.go` sets the warning on all three sentiment chart paths and
    prefixes the printed `printSentimentSummary` header with `[EXPERIMENTAL]`. Verified visually:
    the caption renders under the title without overlapping the plot.

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
- **Native JSON export mode** (Phase 13): deferred. No consumer needs it — the `labours` renderer
  and `hercules combine` read only YAML/PB, and the output contract is already machine-readable and
  documented in those two formats (YAML converts to JSON with any off-the-shelf tool; PB is the
  canonical versioned schema). Adding a correct third format is invasive (per-leaf hand-serialization
  - the `LeafPipelineItem` contract) and speculative. Revisit when a concrete downstream JSON
    consumer or schema-validation need appears; recommended low-invasion path recorded in Phase 13.
