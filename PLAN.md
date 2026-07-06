# Hercules - Plan

Date: 2026-07-05 (compacted from the 2026-07-02 / 2026-07-04 working plan)

This is the single forward-looking plan for the repository. The former detailed execution log has
been compacted for phases that are entirely complete. Completed implementation history remains
available in git and in the merged PRs #1-#3.

**Current status:** all in-repo work in Parts A and B is done, committed, and merged to `main`.
The only open work is cross-repo follow-up in **Phase 10**:

- update `ewws-statistics` to use the in-repo `labours` binary (done on branch
  `claude/repoint-labours`, 2026-07-06; committed, awaiting push/merge — see Phase 10a);
- merge/archive the old `labours-go` repository after its pointer branch lands;
- merge/tag the upstream `matplotlib-go` stderr-noise fix, then bump this repo to the new tag.

## Part A: Integrate the Go renderer (`labours-go`) into Hercules

## Goal

Fold the Go renderer formerly living in the separate `labours-go` repo into **this** repo so that
Hercules ships a native Go visualization path and no longer depends on the Python `labours`
package.

End state, now achieved in this repo:

- one `github.com/meko-christian/hercules` module;
- two Go binaries: `cmd/hercules` and `cmd/labours`;
- `hercules report` renders in-process by default;
- the standalone `labours` binary remains a drop-in renderer for YAML/PB input;
- the renderer lives under `internal/render`;
- `internal/pb/pb.proto` is the canonical protobuf source.

## Decisions (confirmed 2026-07-02)

| Decision          | Chosen                                                                                                                          |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Layout            | One module, two binaries (`cmd/hercules` + `cmd/labours`), with `report` rendering in-process. Renderer under `internal/render`. |
| Python `labours`  | Removed after parity was verified.                                                                                              |
| Render-stack deps | Published/tagged `cwbudde/*` modules; no sibling `replace` dirs in this repo.                                                   |
| Protobuf          | Renderer readers use the Hercules gogo `internal/pb` runtime; the duplicate protobuf package was deleted.                       |

## Compact Completion Record

### Phase 0 - Prep (no code moves) - done 2026-07-02

Completed:

- confirmed no message/field drift between `labours-go/pb.proto` and `internal/pb/pb.proto`;
- chose `internal/pb/pb.proto` as the single source of truth;
- inventoried the render stack and confirmed which modules needed tags;
- confirmed licensing was acceptable because the same owner controls both codebases.

### Phase 1 - Publish/tag the render stack - done 2026-07-02

Completed:

- tagged and pushed `matplotlib-go v0.3.0`;
- cleaned stale test files in `algo-fft`, then tagged and pushed `algo-fft v0.6.12`;
- confirmed `agg_go v0.3.2`, `mathtext v0.4.4`, and `qhull-go v0.1.0` were already current;
- verified `labours-go` built and tested against published versions without sibling `replace`
  directives.

Important retained finding:

- `matplotlib-go v0.3.0` only builds cleanly in this repo's default path with `CGO_ENABLED=0`,
  `-tags purego`, or `-tags systemfreetype` with system FreeType. Hercules therefore keeps the
  renderer release path cgo-free.

### Phase 2 - Add render deps to Hercules' module graph - done 2026-07-02

Completed:

- anchored render dependencies in `go.mod`/vendor before moving renderer code;
- accepted the module-version side effect that moved Cobra and related dependencies forward;
- verified Hercules still built and smoked successfully before renderer code entered the tree.

### Phase 3 - Unify Cobra/Viper - done 2026-07-02

Completed:

- accepted the MVS upgrade to `spf13/cobra v1.10.2` and related packages;
- confirmed the existing Hercules CLI compiled unchanged;
- verified help/version/combine/report command behavior and dynamic analysis flags.

### Phase 4 - Move renderer source into Hercules - done 2026-07-02

Completed:

- copied the labours-go working-tree renderer packages into `internal/render`;
- rewrote imports from `labours-go/internal/...` to `github.com/meko-christian/hercules/internal/render/...`;
- moved required fixtures under `internal/render/testdata`;
- copied the `labours` CLI into `cmd/labours`;
- removed the hardcoded local Hercules path from the labours integration helper;
- updated module requirements and vendor state;
- verified non-cgo builds, focused renderer tests, `cmd/labours` tests, CLI help/version, and a
  drop-in burndown PNG smoke.

Retained note:

- `cmd/parityviewer` stayed in `labours-go`; the later parity gate did not require moving it.

### Phase 5 - Collapse onto a single proto source/runtime - done 2026-07-02

Completed:

- rewrote render PB readers to unmarshal Hercules gogo `pb.AnalysisResults`;
- deleted the temporary google-protobuf generated package;
- removed `google.golang.org/protobuf` from the dependency graph;
- verified reader tests, renderer tests, builds, and a PB-to-PNG smoke.

### Phase 6 - Wire `hercules report` to the in-process renderer - done 2026-07-02

Completed:

- extracted an importable `internal/render` API from the labours mode-dispatch code;
- kept `cmd/labours` as a thin CLI wrapper over the render API;
- changed `cmd/hercules/report.go` to render in-process by default;
- kept `--labours-cmd` as an external drop-in escape hatch;
- added result reporting so strict mode still aborts on hard render errors while missing-analysis
  warnings remain soft;
- verified `hercules report --all` with no external `labours` on `PATH`.

Retained findings:

- the phantom `onboarding` report mode was later removed in Phase 9;
- upstream `matplotlib-go` emitted init-time rcParam warnings, now tracked in Phase 10.

### Phase 7 - Build system, CI, Docker - done 2026-07-02

Completed:

- made `just` build both `hercules` and `labours`;
- pinned the default build path to `CGO_ENABLED=0`;
- removed Python labours setup from Docker/action paths;
- updated Dockerfile, action metadata, cross-compile workflow, `.golangci.toml`, `.gitignore`, and
  vendor state;
- verified local builds/tests as far as possible before the then-known fixture failures were fixed
  in Phase 14.

### Phase 8 - Parity gate - done 2026-07-02 - GATE PASS

Completed:

- ported the visual parity harness to `test/visual`;
- added `just test-visual` and `just test-visual-parity`;
- verified ungated structural tests and Python-reference parity tests;
- ran `hercules report --all --strict` on the SIVA fixture and this repo with zero hard mode
  errors;
- regenerated matched-pair RMSE comparisons against the pre-baked Python references.

Gate result:

- 25/26 chart pairs were equal or better than the recorded matrix;
- the one marginal regression (`devs-efforts`, +0.0052 RMSE) was traced to an axis-offset renderer
  feature and was perceptually equivalent;
- the integrated `labours` binary was byte-identical to an upstream-current labours-go build on the
  same fixtures;
- the `report --all` burndown sub-option filtering bug found during the gate was fixed.

### Phase 9 - Retire Python labours + cleanup - done 2026-07-02

Completed:

- deleted `python/`, Python protobuf leftovers, `pb-python`, `install-labours`, and the Python
  fallback path;
- removed stale `.travis.yml` and Python references in docs/config;
- removed the phantom `onboarding` render mode from report mode lists;
- removed the stray root `labours-go` archive artifact;
- updated `README.md`, `AGENTS.md`, and [docs/RENDER_PARITY.md](docs/RENDER_PARITY.md).

Carried forward:

- the old `labours-go` repo pointer/archive work moved to Phase 10 because it lives outside this
  repository.

### Phase 10 - Cross-repo follow-ups (open, not blocking this repo)

Everything in this phase requires a repository other than Hercules. None of it is executable from a
Hercules-only checkout. All in-repo Hercules work is complete.

#### Phase 10a - Repoint `ewws-statistics` to the merged `labours`

Status: done in `ewws-statistics` on branch `claude/repoint-labours` (2026-07-06). Committed, not
pushed (awaiting the user's go-ahead to push/merge). See "Results" below.

Known context:

- Repo location found 2026-07-02: `/mnt/projekte/Code/MeKo/ewws-statistics`.
- The original plan assumed a sibling path; that was wrong.
- The relevant paths are expected to be:
  - `ewws-statistics/scripts/generate-burndown.sh`;
  - `ewws-statistics/Justfile`.
- The script currently points `LABOURS_BIN` at the old `../../labours-go/labours` binary.
- The Justfile has `setup-hercules` / `install-labours` recipes that should point at the
  Hercules-built `labours` binary instead.
- The merged `labours` binary is drop-in compatible. Phase 8b verified byte-identical rendering
  against upstream-current labours-go on identical fixtures.

Subtasks:

- [x] Get explicit approval to edit `/mnt/projekte/Code/MeKo/ewws-statistics`.
- [x] Inspect current `scripts/generate-burndown.sh` and `Justfile` before changing them.
- [x] Replace `LABOURS_BIN` default with a path to the Hercules-built `labours` binary, preserving
  any existing environment override behavior.
- [x] Update `setup-hercules` / `install-labours` recipes so they build or install from the merged
  Hercules repo instead of the old `labours-go` repo.
- [x] Run the smallest available smoke that exercises burndown generation.
- [ ] If practical, run the full `just burndown-clean` regeneration and compare the MeKo project and
  repo burndown outputs. (Deferred: heavy — re-runs Hercules across all auto-filtered repos. The
  exact `labours` render invocations were smoked directly instead; see Results.)
- [x] Record the exact command results in this phase before marking it complete.

Results (2026-07-06, branch `claude/repoint-labours`):

- Edited `scripts/generate-burndown.sh`, `Justfile`, `README.md`. No `labours-go` references remain
  in load-bearing files (`grep -rn 'labours-go' scripts/ Justfile README.md` → none). Doc-only
  convention mentions in `usage-heatmap/` and `.gitignore` were intentionally left.
- Discovery: `/home/christian/anaconda3/bin/labours` is the **retired Python labours** (broken:
  fails on `import labours.__main__`) and shadows PATH. So the script now prefers the Hercules-built
  Go binary (`$ROOT/../../hercules/labours`) and only falls back to a `labours` on PATH; `LABOURS_BIN`
  override still wins. Resolution verified to select the Go ELF binary at
  `/mnt/projekte/Code/hercules/labours`.
- `just labours` built the renderer; `labours --help` exposes every flag the script uses
  (`-f/--input-format`, `-i/--input`, `-m/--modes`, `-o/--output`, `--resample`, `--max-repos`).
- Smoke: rendered both chart modes from the existing `output/data/burndown-pb/all_combined.pb` with
  the exact script invocations — `-m burndown-project` → `/tmp/smoke_project.svg` (2.9 MB) and
  `-m burndown-repos-combined --max-repos 25` → `/tmp/smoke_repos.svg` (106 KB), both exit 0, valid
  SVG. (The runs still emit the Phase 10c matplotlib-go rcParam stderr noise; unrelated to 10a.)
- `just --list` parses the edited `Justfile`; `shellcheck` shows no new findings in the edited region.

Acceptance:

- [x] `ewws-statistics` no longer depends on `../../labours-go/labours`;
- [x] local burndown generation works with `cmd/labours` from this repository (both modes rendered);
- [x] environment overrides for custom `LABOURS_BIN` values still work (verified override is honored).

Note: the stale, broken `~/anaconda3/bin/labours` (Python) is an environment leftover worth removing
so it no longer shadows the Go binary on PATH; that is a local cleanup, outside the repo edits.

#### Phase 10b - Archive the old `labours-go` repo

Status: partly done. Pointer branch is pushed; owner-side merge/archive actions remain.

Known context:

- Branch: `claude/archive-pointer`.
- Commit: `33943c9`.
- Completed on that branch:
  - README starts with an `[!IMPORTANT]` notice explaining that the renderer is superseded;
  - the notice points to Hercules `internal/render` and `cmd/labours`;
  - the notice says parity was verified and issues/PRs should go to Hercules;
  - labours-go `PLAN.md` has a one-line closing note.

Subtasks:

- [ ] Merge `labours-go` branch `claude/archive-pointer`.
- [ ] Confirm the default branch README renders the superseded notice correctly on GitHub.
- [ ] Confirm the old repo has no open release branch that still needs the renderer code active.
- [ ] Flip the repository's GitHub archive/read-only flag.
- [ ] Record the merge commit or archive confirmation here.

Acceptance:

- the old `labours-go` repository clearly points users to Hercules;
- the old repository is archived/read-only;
- new renderer issues and PRs are directed to this repository.

#### Phase 10c - Merge/tag upstream `matplotlib-go` stderr-noise fix and bump Hercules

Status: upstream fix branch is pushed; owner-side merge/tag and the Hercules dependency bump remain.

Problem:

- `matplotlib-go/style` emitted roughly 50 `rcParam "..." is ... not parsed by matplotlib-go`
  warnings to stderr during package `init()`;
- this happened on every `hercules`/`labours` invocation, even commands like `hercules version`;
- the warnings fired before Hercules code could run, so this repository could not intercept them
  with `log.SetOutput` or handler tricks;
- the available handler lived in `matplotlib-go/internal/diag`, which was unreachable from
  Hercules.

Known upstream state from 2026-07-04:

- Latest published tag at investigation time: `matplotlib-go v0.3.0`.
- Fix branch: `claude/silence-bundled-style-warnings`.
- Commit: `39f7164`.
- Implemented on that branch:
  - bundled stylesheets parse under a silenced handler during init;
  - warning dedup state resets afterward so user-supplied stylesheets still warn;
  - new public package `github.com/cwbudde/matplotlib-go/diag`;
  - public `diag.SetHandler(fn func(string)) (restore func())`, with nil silencing;
  - public handler delegates to the existing internal handler.
- Upstream validation already performed:
  - full `CGO_ENABLED=0` suite had zero new failures versus a clean v0.3.0 baseline;
  - a temporary Hercules `replace` changed `hercules version` stderr from 50 lines to 0.

Subtasks:

- [ ] Merge `matplotlib-go` branch `claude/silence-bundled-style-warnings`.
- [ ] Tag the merge as `v0.3.1`.
- [ ] Confirm `go list -m -versions github.com/cwbudde/matplotlib-go` sees `v0.3.1`.
- [ ] In this repository, bump `github.com/cwbudde/matplotlib-go` from `v0.3.0` to `v0.3.1`.
- [ ] Run `go mod tidy` and `just vendor`.
- [ ] Verify `CGO_ENABLED=0 go test ./internal/render/... ./cmd/labours/...`.
- [ ] Verify `./hercules version` and `./labours version` produce no rcParam stderr noise.
- [ ] Run the standard repo verification subset listed below.
- [ ] Record the exact tag, dependency diff, and verification results here.

Acceptance:

- Hercules depends on `github.com/cwbudde/matplotlib-go v0.3.1` or newer;
- the init-time bundled-style rcParam warnings are gone without local workarounds;
- user stylesheet warnings still work upstream.

## Verification

Historical verification:

- Phase-6 checkpoint, 2026-07-02: in-process rendering worked; external `--labours-cmd` fallback
  worked; report generation completed with zero hard failures.
- Post-Phase-9, 2026-07-02: builds and parity checks passed; `report --all --strict` on the SIVA
  fixture produced 131 charts with zero hard errors; downstream `ewws-statistics` regeneration was
  not run because it is a separate repo and is tracked in Phase 10.
- Phase 14 later fixed the broad `just test` fixture failures that existed during early renderer
  integration.

Standard verification subset for remaining Phase 10 work:

1. `just`
2. `just test`
3. `just check-tidy`
4. `just check-formatted`
5. `just test-visual`
6. `just test-visual-parity`
7. `./hercules --burndown --pb <repo> | ./labours -f pb -m burndown-project -o /tmp/b.png`
8. `./hercules report --all --strict -o /tmp/report <repo>`

For Phase 10c, also check:

1. `./hercules version 2>/tmp/hercules-version.err`
2. `./labours version 2>/tmp/labours-version.err`
3. `wc -l /tmp/hercules-version.err /tmp/labours-version.err`

Both stderr files should have zero rcParam warning lines.

## Critical Files

This repo:

- `go.mod`
- `go.sum`
- `vendor/`
- `justfile`
- `Dockerfile`
- `action.yml`
- `.golangci.toml`
- `cmd/hercules/{root,report,combine,generate_plugin}.go`
- `cmd/labours/`
- `internal/pb/`
- `internal/render/`
- `README.md`
- `AGENTS.md`
- `docs/RENDER_PARITY.md`
- `docs/SCHEMAS.md`

Cross-repo:

- `/mnt/projekte/Code/MeKo/ewws-statistics/scripts/generate-burndown.sh`
- `/mnt/projekte/Code/MeKo/ewws-statistics/Justfile`
- old `labours-go` repository, branch `claude/archive-pointer`
- `github.com/cwbudde/matplotlib-go`, branch `claude/silence-bundled-style-warnings`

## Part B: Remaining roadmap work (carried over from ROADMAP.md)

These phases are independent of the renderer integration. They are complete in this repository.

### Phase 11 - Validate or demote advanced pipeline features (P1) - done 2026-07-02

Completed:

- added merge-tracking correctness tests for non-linear histories;
- covered branch isolation, lineage completeness, hibernation transparency, and merge-track
  dependency annotations;
- added a cgo-only plugin smoke test via `just test-plugin`;
- documented non-linear histories as supported;
- documented plugins as supported only on cgo builds with matching toolchain/tree/tags.

### Phase 12 - PB schema versioning & CI guardrails (P1) - done 2026-07-02

Completed:

- made `pb.SchemaVersion` the single schema-version source of truth;
- emitted the schema version in YAML, PB metadata, `combine`, `hercules version`, and
  `labours version`;
- documented schema compatibility policy in [docs/SCHEMAS.md](docs/SCHEMAS.md);
- added schema snapshots and `cmd/schema-guard`;
- added CI/local guardrails requiring changelog entries and version bumps for schema changes.

### Phase 13 - Decide on JSON export mode (P1) - decided 2026-07-03

Decision: native JSON export remains deferred.

Rationale:

- no current downstream consumer needs first-class JSON;
- YAML and PB are already documented machine-readable contracts;
- the renderer and `hercules combine` consume YAML/PB only;
- a correct JSON emitter would touch all leaf serialization paths and the plugin contract.

Recommended revisit path:

- if a concrete JSON consumer appears, add a `--json` branch that marshals the existing per-leaf
  PB messages with `github.com/gogo/protobuf/jsonpb`;
- generate JSON Schemas from `internal/pb/pb.proto` so schema validation stays aligned with
  `pb.SchemaVersion`.

### Phase 14 - Fix broad `just test` fixture failures (P2) - done 2026-07-03

Completed:

- made `internal/core` fixture-sensitive tests deterministic and network-free by using the checked-in
  `cmd/hercules/test_data/hercules.siva` fixture through a read-only fixture storer;
- repointed fixture-incompatible commit references and removed post-fixture-horizon cases with a
  comment;
- fixed `internal/linehistory/TestLinesMeta` after the `lines-exclude-paths` option was added;
- committed missing render reader `.golden.json` fixtures by adding an explicit `.gitignore`
  negation.

Retained caveat:

- `cmd/hercules/TestLoadGitRepositoryWithCreds` can pass unexpectedly in this sandbox because the
  outbound HTTPS proxy accepts bogus credentials for public GitHub repos. The test is left intact
  because normal GitHub Actions network behavior returns 401 and preserves the intended coverage.

### Phase 15 - Sentiment: mark as experimental everywhere (P3) - done 2026-07-04

Completed:

- kept the YAML/PB content key as `Sentiment` for compatibility;
- added a YAML comment warning for tensorflow sentiment output;
- prefixed the renderer sentiment summary with `[EXPERIMENTAL]`;
- added subtitle support to the relevant plot option structs;
- rendered an experimental warning subtitle on sentiment charts.

## Test & Validation Matrix

Use this while working on future changes:

```bash
# Unit tests
just test

# Focused packages
go test ./internal/core
go test ./internal/plumbing
go test ./leaves

# Smoke runs
./hercules --dry-run .
./hercules --preset quick .

# Scaling smoke (use an actual large repo path)
./hercules --preset large-repo /path/to/large/repo
```

## Deferred / Not Planned

- **Code review metrics:** requires GitHub/GitLab API integration; Git history alone is
  insufficient. Revisit when an `internal/platform/` abstraction exists.
- **Remote repository cloning support (HTTPS/SSH):** nice to have, but not core to correctness; can
  be handled externally by cloning locally. Revisit after scaling presets and schema contracts are
  solid.
- **Caching:** still deferred until repeated real-world workloads show a stable bottleneck;
  premature caching risks wrong-by-default behavior.
- **Native JSON export mode:** deferred in Phase 13. Revisit only when a concrete downstream JSON
  consumer or schema-validation need appears.
