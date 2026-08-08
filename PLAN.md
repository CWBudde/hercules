# PLAN

**Open work, in execution order:**

| phase                                                 | items                | state                                      |
| ----------------------------------------------------- | -------------------- | ------------------------------------------ |
| [Phase 1 — combine correctness](#phase-1)             | T1.1–T1.3 (M7/M6/M2) | T1.1 + T1.2 landed; T1.3 open              |
| [Phase 2 — rendering defects](#phase-2)               | T2.1–T2.3 (M4/M5/M3) | not started, small and visible             |
| [Phase 3 — unexposed analyses](#phase-3)              | T3.1–T3.3 (M1/M8/M9) | not started, largest new work              |
| [Phase 4 — burndown residue (B1c/P5)](#phase-4)       | T4.1–T4.3            | **on hold — recommendation: do not start** |
| [Phase 5 — recorded, deliberately not done](#phase-5) | —                    | reference only                             |

Everything else filed since the 0.2.0 regression sweep — B1–B13, B1c phases P0–P4 and P6, and the
corpus regression suite — is **done**; see the [appendix](#appendix-a) for the one-line record.

Two facts that keep being re-derived, so they are stated once:

- **`082bf15` is not a correctness baseline.** Until `3e31507` both serializers clamped negatives
  away. Rebuilt with the clamp off, the old binary's project matrix is already negative
  (`ewws-wiki` 22 cells / min −227, `render-pdf` 53 cells / min −6912). 0.2.0 did not start
  producing negatives, it started reporting them.
- **Negatives no longer abort a run.** They warn once; `--strict-burndown-balances` restores the
  hard failure. B1c is a numbers-are-wrong item, not an availability item.

**Filed:** 2026-07-30 against `b330adf` (tag `0.2.0`); the M items 2026-08-06.
**Corpus:** `/mnt/projekte/Code/MeKo/*`, the repositories in `ewws-statistics/.core-repos`.

---

<a id="phase-1"></a>

## Phase 1 — combine correctness

The org-wide corpus (231 non-archived MeKo-Tech repositories, 10.9 M lines, 31 508 commits, 38
identity groups, merged into one `.pb`) exposes three defects in `hercules combine`. None is an
accounting defect; these are things hercules collects and then merges wrongly or drops silently.

### T1.1 — qualify path keys with the repository (M7) 🟢 landed, corpus check open

Root cause behind T1.2(b) and the collapse of the org-wide coupling charts. **One change at one
merge site unlocks both.**

`leaves/couples.go:413` merges the file index with `join.LiteralIdentities(cr1.Files, cr2.Files)`,
which keys `destinations map[string]int` on the raw string (`internal/join/join.go:17-29`, `:35`)
and sums co-occurrence via `mergeCouplesMatrices` (`leaves/couples.go:424`). No prefix, no tag.
Measured: 31 077 distinct files reduce to **20 coupling relationships**, led by `manifest.json`,
`CHANGELOG.md`↔`package.json` and `go.mod`↔`go.sum`. The charts are not noisy — they answer a
question nobody asked ("do many repositories have a `package.json`?").

The repository identity **is already in the combine pipeline** and is simply never threaded into a
path key: it reaches `Burndown` as a separate axis (`cmd/hercules/combine.go:182,187-188` —
`ReversedRepositoryDict`, `RepositoryHistories`) and the header (`:197,243`).

- [x] Thread the repository qualifier (`repo:path`) into path keys at merge time. **Done
      2026-08-08.** `core.RepositoryQualifiablePipelineItem` (`internal/core/pipeline.go`) adds one
      optional method, `QualifyPaths(result, repository)`; `qualifyRepositoryPaths`
      (`cmd/hercules/combine.go`) applies it to every loaded result before the merge, next to
      `initializeBurndownRepository`. The prefix is built by `core.QualifyRepositoryPath` with
      `core.RepositoryPathSeparator = ":"`.
- [x] Apply to the other bare-path keys. Implemented for `Couples.Files`, `FileRisk.Path`,
      `CodeChurnAuthorResult.Files`, and — same defect, not in the original list —
      `KnowledgeDiffusionResult.Files`, `BusFactorResult.SubsystemBusFactor` and
      `OwnershipConcentrationResult.SubsystemConcentration` (the last two key on a directory
      prefix, so `cmd` from 231 repositories was collapsing into one worst-case reading).
      `FileHistoryResult.Files` is **not** qualified: `FileHistoryAnalysis` is non-mergeable, so
      combine never deserializes it (see T1.3); it needs qualification only if that changes.
      `BurndownResult.FileHistories`/`FileOwnership` are likewise untouched — burndown's merge
      drops them (Phase 5, combined file burndown).
- [ ] Verify on the 231-repo corpus that coupling relationships rise from 20 to something with
      per-repository structure. **Not run — no per-repository `.pb` corpus on disk, and rebuilding
      231 of them is hours.** Verified instead on a real two-repository sample (`ewws-auth` +
      `ewws-wiki`, `--couples --pb --diff-timeout=300000`): merged index 572 files = 342 + 230 with
      no fusion, 54 650 coupling relationships, **0 cross-repository**. The two repositories share
      11 identical paths (`.gitignore`, `.github/workflows/*`, `README.md`) which previously fused
      into 11 keys with summed co-occurrence — that is the org-wide `manifest.json` /
      `go.mod`↔`go.sum` result in miniature.

Two decisions worth keeping:

- **Re-combining is guarded by an explicit header flag**, `Metadata.qualified_paths`
  (`internal/pb/pb.proto` field 9, added compatibly — no `SchemaVersion` bump). `hercules combine`
  sets it on its output and skips qualification for any input which carries it. It is deliberately
  **not** inferred from the repository string: a combine of one input has a header naming one
  repository and is indistinguishable from a plain analysis, while files written before the flag
  have unqualified paths whatever their header says. Both directions are covered by
  `TestRunCombineLeavesCombinedInputQualifiedOnce` and
  `TestRunCombineQualifiesFilesWrittenBeforeTheHeaderFlag`.
- **An empty header repository leaves paths bare**, so a result of unknown origin does not gain a
  leading `:`.

### T1.2 — combined `hotspot-risk` is unsound in three independent ways (M6) 🟢 landed 2026-08-08

Rendering it on the 231-repo merge succeeded and produced something worse than an error: 20 rows,
alphabetically ordered, **every `RiskScore` exactly 1.0**, `README.md` present twice as two rows.

- [x] **(a) Truncated to 20 regardless of configuration.** `cmd/hercules/combine.go` summons the
      item via `Registry.Summon` and calls neither `Configure` nor `Initialize`, so `TopN` was
      zero-valued and `effectiveTopN()` fell back to `DefaultTopN = 20`. A user who ran every
      repository with `TopN=200` still got 20.
- [x] **(b) No dedup** — `MergeResults` concatenated and re-sorted. Its doc comment justified this
      by _"the same path in two different repositories denotes two different files, and `FileRisk`
      carries no repository qualifier."_ T1.1 removed that justification.
- [x] **(c) No cross-run rescaling.** `normalizeAndScore` was called only from `Finalize`, never
      from `MergeResults`, so each repository's factors were normalised against its own maxima and
      the merged ranking compared incomparable numbers — which is why every surviving score was 1.0.

**The fix follows B13's pattern**: `MergeResults` now reads **no receiver state at all**, the way
`BusFactorAnalysis` and `OwnershipConcentrationAnalysis` do. `HotspotRiskResult` carries the
configuration the run was produced with — `TopN` and a `HotspotRiskWeights` value — round-tripped
through `HotspotRiskResults.top_n` and `weight_*` (`internal/pb/pb.proto` fields 3–7, compatible
additions, **no `SchemaVersion` bump**, recorded in `docs/SCHEMA_CHANGELOG.md`). All-zero is read as
_unset_ and falls back to `DefaultTopN`/`DefaultWeight`, so pre-upgrade `.pb` files still merge; a
side which carries weights lends them to a side which does not, and two sides which carry
_disagreeing_ weights are a merge error (`errHotspotRiskWeightMismatch`), mirroring the existing
churn-window check.

The merge order is load-bearing: **concatenate → rescale → dedup → rank → truncate**. Rescaling
must precede dedup, because the incoming scores were normalised against each run's own maxima, so
picking a winner by score before rescaling would compare exactly the incomparable numbers the
rescaling exists to remove. `normalizeAndScore` became a free function taking the weights, shared
with `Finalize`; it derives everything from the **raw** `Size`/`Churn`/`CouplingDegree`/
`OwnershipGini` fields, which makes it idempotent under combine's pairwise reduce.

**One correction to this file's own diagnosis:** (c) claimed the fix "requires carrying the raw
normalization bounds in the `FileRisk` schema". It does not — `FileRisk` always carried the raw
factors. Only the weights and `TopN` were missing, because they live on the analysis item.

Two accepted approximations, both recorded at `MergeResults`:

- Each input was already truncated to its own `TopN` in `Finalize`, so the merge renormalises
  against the maxima of the **survivors**, not of every file that ever existed.
- combine reduces pairwise and truncates at every step, so which files survive can depend on merge
  order. Pre-existing; not made worse.

**Verified** on `ewws-auth` + `ewws-wiki` (`--hotspot-risk --hotspot-risk-top=50 --pb
--diff-timeout=300000`, neither run truncated). Before: **20** rows, every score a verbatim copy of
its per-run value, and **`ewws-wiki` absent entirely** — its files could not compete against
`ewws-auth`'s own-run-normalised scores. After: **50** rows drawn from both repositories, all paths
distinct, and the ranking genuinely changed (`PLAN.md` #1 → #2; `server/components/main_templ.go`,
the largest file at 1307 lines, out of the top 8). Unit coverage in `leaves/hotspot_risk_test.go`
per defect plus the upgrade path, and `TestRunCombineHotspotRiskIsSoundAcrossRepositories` end to
end in `cmd/hercules/combine_test.go`.

### T1.3 — non-mergeable leaves are dropped silently (M2) 🟡

`cmd/hercules/combine.go:305-315`: a failed `ResultMergeablePipelineItem` assertion appends the
analysis to `loaded.skipped` with `"; not merged"` and carries on — surfaced only when the user
named it via `--only`. So `hercules report` (which enables `--onboarding` by default,
`report.go:61`) followed by `hercules combine` loses the data without saying so.

Current non-mergeable set (all embed `core.NoopMerger`, none define `MergeResults`): `Onboarding`,
`Shotness`, `CommitsStat` (`leaves/commits.go:22`), `FileHistoryAnalysis`
(`leaves/file_history.go:24`), `ImportsPerDeveloper` (`leaves/imports_printer.go:31`). `CodeChurn`
is the exception — real merge at `leaves/codechurn.go:413`.

- [ ] **(a)** Always warn on a dropped analysis, not only under `--only`.
- [ ] **(b)** Reconcile `reportDefaultAnalysisFlags` (`report.go:52-65`) with `reportDefaultModes`
      (`:84-101`) — today the former computes `onboarding` and the latter has no mode for it.
      Land this together with T3.1.

---

<a id="phase-2"></a>

## Phase 2 — rendering defects

T2.1 and T2.2 are outright bugs and should be fixed before anyone trusts either chart. All three
reproduce on a **single** repository, so none is scope- or combine-dependent.

### T2.1 — `refactoring-proxy` renders one spike at year −242464 (M4) 🔴

The data is correct: `labours -m refactoring-proxy -o out.json` on the 231-repo merge gives 1526
ticks, `Timestamp` min 1612793078 (2021-02-08), max 1785938678 (2026-08-05), **zero** timestamps
below 1e9, with `TickSizeDays: 1`, `StartDate` and `EndDate` populated.

The chart is not: both the combined and the `ewws-auth` single-repo SVG plot **one** vertical spike
at the extreme left (93 % and 18 % respectively) against a single x-tick reading `-242464`/
`-242460`, with the whole five-year series collapsed into it. The axis is labelled `"Date"`
(`internal/render/modes/refactoringProxy.go:112`) and the conversion at `:41-51`
(`timestamps[i] = time.Unix(tick.Timestamp, 0).UTC()`) is right, so the loss sits **between that
`[]time.Time` and the plotter's x values** — a year-−242464 label is what an epoch-relative
`time.Time` produces after a wrong unit conversion.

- [ ] Reproduce: `labours -f pb -i output/data/hercules-pb/ewws-auth-burndown.pb -m
  refactoring-proxy -o /tmp/x.svg`
- [ ] Fix the unit conversion between `[]time.Time` and the plotter's x values.

### T2.2 — `--relative` collapses `burndown-project` to a single band (M5) 🔴

`labours -m burndown-project --relative` renders one solid colour filling the plot at a constant
1.0 across the whole history, with a full and correct legend beside it (23 bands on the merge, 11
on `ewws-auth`). Only the first band is drawn; the rest sit at zero. Without `--relative` the same
input renders correctly.

This matters because the relative view is the _right_ one at org-wide scale — the absolute burndown
is dominated by corpus growth. **`--relative` on `ownership` works correctly** and produces the best
code-ownership chart in the set, which localises the defect to the burndown normalisation path
rather than to `--relative` itself.

- [ ] Fix the burndown normalisation path; use the working `ownership` path as the reference.

### T2.3 — three charts label an axis "Tick" while the data holds real dates (M3) 🟡

`oldVsNew` proves the renderer can do it: `internal/render/modes/oldVsNew.go:64` takes
`reader.GetHeader()` (`internal/render/readers/pb_reader.go:73-79`, begin/end unix), then
`timeSeriesCalendarRange` (`oldVsNew.go:183`) and `dates[i] = start.AddDate(0, 0, i)` (`:171-174`).

These three plot a bare integer index instead:

| mode                       | x assignment                                      | axis label       |
| -------------------------- | ------------------------------------------------- | ---------------- |
| `busFactor`                | `internal/render/modes/report_metrics.go:670-671` | `"Tick"` `:676`  |
| `ownershipConcentration`   | `report_metrics.go:996,998`                       | `"Tick"` `:1010` |
| `knowledgeDiffusion` trend | `report_metrics.go:2094-2103`                     | `"Tick"` `:2103` |

The inputs are already there — `readers.BusFactorData.TickSize` (`readers/reader.go:168-174`),
`OwnershipConcentrationData.TickSize` (`:183-189`), `KnowledgeDiffusionData.TickSize` (`:198-204`),
plus `reader.GetHeader()`. `date = begin + tick × tickSize` is the whole fix.

- [ ] Convert the x axis in all three modes and relabel.
- [ ] Same pass: `busFactor`, `ownershipConcentration`, `hotspotRisk` and `refactoringProxy` discard
      the `startTime`/`endTime` dispatch parameters — note the `_, _ *time.Time` signatures at
      `internal/render/dispatch.go:362`, `:366`, `:374`, `:382`, against `oldVsNew` at `:342` and
      `temporalActivity` at `:349`. None of them can honour `--start-date`/`--end-date`.

**Downstream value:** the bus-factor timeline is otherwise the best unused chart in the corpus
(1 → 9 over five years, with a dip to 3 held from 2021-08 to 2024-04), and the tick axis is the only
thing keeping it off a slide unexplained.

---

<a id="phase-3"></a>

## Phase 3 — analyses collected but never surfaced

### T3.1 — `--onboarding` is computed on every default report run and then discarded (M1) 🔴

The single biggest gap, and the largest payoff. `OnboardingAnalysis` measures
time-to-first-meaningful-change, per-author ramp-up snapshots at fixed day windows, and join
cohorts — for a 38-person organisation with a steady AZUBI intake, that is the one question none of
the existing charts answer.

What exists:

- Leaf `leaves/onboarding.go:86`, `Name()` `:117`, `Flag()` `"onboarding"` `:229`, registered `:889`.
- `OnboardingResult` (`:76-84`): `Authors map[int]*AuthorOnboardingData`,
  `Cohorts map[string]*CohortStats`, `WindowDays []int`, `MeaningfulThreshold int`.
  `AuthorOnboardingData` (`:58-64`) carries `FirstCommitTick`, `JoinCohort` (`"YYYY-MM"`) and
  `Snapshots` keyed by window; `OnboardingSnapshot` (`:43-56`) separates total from _meaningful_
  commits/files/lines.
- Protobuf exists: `OnboardingResults` at `internal/pb/pb.proto:371`, emitted at `:793`.
- It is in `reportDefaultAnalysisFlags` (`cmd/hercules/report.go:61`) — **every** default
  `hercules report` pays for it.

What is missing: no `"onboarding"` key in `internal/render/dispatch.go:18-43`, no entry in
`cmd/labours/helpers.go:87-112`, no reader in `internal/render/readers/`. `grep -ri onboarding
cmd/labours/ internal/render/` is empty — the charts only ever existed in the retired Python
renderer.

- [ ] `.pb` reader in `internal/render/readers/`.
- [ ] `"onboarding"` mode handler in `internal/render/dispatch.go`.
- [ ] Mode→analyses entry in `cmd/labours/helpers.go`.
- [ ] Chart 1: ramp-up curve per join cohort (meaningful lines against days-since-join).
- [ ] Chart 2: time-to-first-meaningful-commit distribution.
- [ ] **Org-wide is blocked on T1.3** — `OnboardingAnalysis` embeds `core.NoopMerger` (`:87`) and
      defines no `MergeResults`, so it fails the assertion at `cmd/hercules/combine.go:305` and is
      dropped. Per repository it works today. Either write a `MergeResults` or ship per-repository
      only, with T1.3(a) making the drop loud.

### T3.2 — `knowledge-diffusion` has the silo data and sorts it alphabetically (M8) 🟡

`knowledge_diffusion_silos` looks like the actionable companion to the headline number (78 % of all
files have exactly one editor) and is useless: at org-wide scale it lists `.claude/`, `.cursor/` and
`.devcontainer/` config files in alphabetical order, every one with one editor.

The inputs for a real ranking are already collected — lines per file and editor count, with the tick
axis supplying recency and churn. Small, self-contained, high value.

- [ ] Rank by `lines × single-editor × recency × churn` instead of alphabetically, so the chart
      answers _which_ silos are dangerous.

### T3.3 — analyses with no mode at all (M9) 🟢

| leaf                        | `Name()`                    | `Flag()`                | reader                       | mode | mergeable       |
| --------------------------- | --------------------------- | ----------------------- | ---------------------------- | ---- | --------------- |
| `leaves/codechurn.go`       | `CodeChurn` `:116`          | `codechurn` `:175`      | no                           | no   | **yes**, `:413` |
| `leaves/commits.go`         | `CommitsStat` `:58`         | `commits-stat` `:101`   | yes, `pb_reader.go:1152`     | no¹  | no              |
| `leaves/file_history.go`    | `FileHistoryAnalysis` `:47` | `file-history` `:71`    | yes, `pb_reader.go:633,1167` | no   | no              |
| `leaves/imports_printer.go` | `ImportsPerDeveloper` `:54` | `imports-per-dev` `:78` | no                           | no   | no              |

¹ `commits-stat` is not _entirely_ unrendered — `cmd/labours/helpers.go:106` maps `"run-times"` onto
it — but nothing surfaces the per-commit/per-file line stats it actually carries.

- [ ] **`CodeChurn` is the pick of these**: the only mergeable one, so it works org-wide today, and
      `CodeChurnFileResult` (`leaves/codechurn.go:83-89`) carries `InsertedLines` against
      `OwnedLines` per author per file — lifetime contribution against surviving contribution, a
      genuinely different statement from anything the burndown charts make. Caveats: it merges by
      bare path (T1.1) and panics on differing tick sizes (`:429`).
- [ ] The other three: decide per leaf whether a mode is wanted at all before building readers.

---

<a id="phase-4"></a>

## Phase 4 — B1c: the same lines removed twice across sibling branches 🔴

**On hold. The premise is measured and confirmed; the recommendation is not to start, on price.**

### 4.0 The defect

Two sibling branches each delete the same lines from their own copy of a file. All five leaves that
consume `DependencyLineHistory` fork with `core.ForkSamePipelineItem`, so both removals land in
**one shared accumulator**, while the branches feeding it do not share state. The merge sees both
parents with the lines already gone and has nothing to re-add. The negative is permanent — residual,
not transient, and it rides through every later sampled row as a constant offset because burndown
rows are cumulative.

Traced on `render-pdf` (before the comparator fix, which is why this instance is gone):

```
TRACE 0090e98b file=395 prev=304 curr=304 delta= 7413
TRACE bc3aea9f file=395 prev=304 curr=416 delta=-7396   <- sibling of 52ca72b7
TRACE 52ca72b7 file=395 prev=304 curr=416 delta=-7388   <- sibling of bc3aea9f
TRACE bc3aea9f file=510 prev=416 curr=416 delta= 7412
```

Band 304 receives +7413 −7396 −7388 = **−7371** and stays there. Note the milder second half: the
lines are re-credited to band 416 instead of keeping band 304, so the _positive_ bands are wrong
too. Downstream never sees that, because the protobuf serializer clamps only negatives.

**Only line identity can close it.** `updateGlobal` calls
`globalHistory.updateDelta(PrevTick, CurrTick, Delta)` with **no `FileId`**, so no amount of
file-identity work can reach the project or people matrices. (That is why P3, which repairs
per-file histories, did not close B1c — see [appendix](#appendix-a).)

### 4.1 Current magnitude

From `test/corpus/baseline.json` (commit `b97f549`, flags `--burndown --burndown-people --devs
--couples --bus-factor --ownership-concentration --hotspot-risk --skip-blacklist --granularity=30
--sampling=30`). "Residual" = cells whose age band is still negative in the final sampled row.

| repository                                                             | negative cells | residual |
| ---------------------------------------------------------------------- | -------------: | -------: |
| `mekorp-backend`                                                       |          1 748 |       67 |
| `mekorp-webclient`                                                     |          1 447 |       45 |
| `meko-etl-tool`                                                        |            553 |       12 |
| `backend-for-microscope`                                               |            137 |        7 |
| `process-manager`                                                      |            114 |        6 |
| `ewws-wiki`                                                            |             83 |        3 |
| `personio-ipoffice-sync`                                               |             34 |        2 |
| `ShelfStockScanner`                                                    |              2 |        1 |
| `MKTools`, `ewws-render`, `ewws-tailscale`, `go-clients`, `render-pdf` |              0 |        0 |

`mekorp-backend` carries the overwhelming share of the mass and is the case to drive from. These
counts exclude `people_interaction:` — a signed interaction matrix whose negatives are correct by
construction; counting it inflated every figure in the older tables by roughly an order of
magnitude. **Older tables in git history are not comparable to these.**

After the rename fix and P3, what is left is **327 residual file cells and ~4 100 project cells
corpus-wide**, concentrated in one repository, on a metric that now warns rather than fails.

### 4.2 What this costs in the people matrix, measured against `git blame`

**Measured 2026-08-01 against `ca66fff`.** B2's magnitude ("person matrices have _always_ contained
negatives") was only ever stated as a cell count. It is worse than a count suggests: on the
worst-affected repositories **whole authors change places**. The check is independent of hercules —
`git blame --line-porcelain` over every tracked file at HEAD, aggregated through
`ewws-statistics/ewws-developers.identities`, against the bus-factor gauge's latest snapshot for the
same `.pb`.

`mekorp-webclient` (551 448 lines blamed, hercules reports 704 393):

| person       | git blame |  hercules |                                       |
| ------------ | --------: | --------: | ------------------------------------- |
| Christian    |     22.4% |     19.2% | ok                                    |
| **Fabienne** | **21.4%** |     **—** | 2nd largest owner, not in top 8       |
| Hannes       |     16.3% |     13.6% | ok                                    |
| Emil         |     10.4% |      5.1% | halved                                |
| Rüdiger      |      7.7% |      4.8% | understated                           |
| Lukas N.     |      5.3% |      5.2% | ok                                    |
| Katharina    |      4.1% |         — | absent                                |
| **Ionut**    |  **2.6%** | **40.8%** | credited ~287 k lines he does not own |

The shares missing from Fabienne, Katharina, Emil and Rüdiger sum to roughly Ionut's excess, so this
reads as several authors' lines collapsing onto one — not noise spread thinly. Fabienne's ownership
is not marginal: 38 159 lines of `src/__generated__/apiTypes.ts` and 7 341 of `src/gql/graphql.ts`
are hers by blame, and those two generated files alone are 248 k of the repository's 580 k lines.

**It is not systematic, and that is the useful part.** The same comparison on `ewws-auth` is
essentially exact — Christian 67.8 % vs 66 %, Lukas N. 28.5 % vs 30 %, Emil 2.1 % vs 2 %, totals
within 0.1 % (85 154 vs 85 236). `ewws-auth` has **0** negative burndown cells; `mekorp-webclient`
has 1 447. The distortion tracks B1c damage rather than repository size or age, which is what makes
it B1c's cost rather than a separate defect.

**Consequences.** Any downstream reading of ownership on an affected repository is wrong at the
level of _who_, not just how much: bus factor, the ownership gauge, `code_ownership.svg`, and
anything built on them. B1c cannot be judged closed on negative-cell counts alone — **this
comparison is the acceptance test that actually reflects what users see**, and it is cheap to run on
any repository in the corpus.

### 4.3 The premise, measured — the double removal is real and is the right size

Measured 2026-07-31 with throwaway instrumentation (mirror `map[FileId]map[PrevTick]int64` of the
running line count, hooked into `updateGlobal` at `burndown.go:989` and reported at `Finalize`; it
records the part of each removal the band could not cover, then clamps at zero so one over-removal
does not cascade). The instrumented binary reproduces `baseline.json` cell for cell, so it is not
perturbing what it measures. Corpus flags, `--diff-timeout=300000`, neither run truncated.

| repository       | over-removed (file, band) pairs | events | **excess mass** | project residual mass | all-matrix residual mass |
| ---------------- | ------------------------------: | -----: | --------------: | --------------------: | -----------------------: |
| `mekorp-backend` |                             624 |  2 739 |     **123 611** |               107 392 |                  217 766 |
| `meko-etl-tool`  |                             283 |  1 176 |       **3 937** |                   984 |                    2 269 |

Compare against the **residual** mass — the final sampled row — not total negative mass, because
burndown rows are cumulative and one over-removal shows up in every later row. On the case to drive
from, over-removal accounts for **115 % of the project residue**; on the cross-check, 4×. Excess
exceeding the residue is what the mechanism predicts: some over-removed lines are later re-inserted
and the cell recovers, so the excess is an upper bound. The people matrices' residual mass (110 374)
tracks the project's (107 392) — the same residue counted per author, not a second one.

**Verdict: P5 is aimed at the right mechanism.** Caveat: the mirror detects over-removal of _any_
origin, not specifically two siblings doing it. But over-removal at this scale, in an accumulator
shared across branches by `ForkSamePipelineItem` and reconciled by nothing
(`burndown.go:308-318`), is that mechanism's signature, and no other candidate has been proposed.

The instrumentation is **not** kept in the tree — ~110 lines, and re-deriving it from this
description is cheaper than carrying a debug hook the product does not otherwise have (`os.Getenv`
appears nowhere in `leaves/` or `internal/linehistory/`, and `core.Logger` has no Debug level).

### 4.4 Three reconstruct-at-the-merge attempts, all reverted — do not propose a fourth of this kind

**Attempt 1 — reconcile the shared accumulator against the merged tree.** Mirror what consumers
have been told (per file, per ownership band); at each `Merge()` compare against the merged tree and
emit the difference. Cleared `render-pdf` outright but regressed `meko-etl-tool` (head count from
+11 % to −13 %). Diagnosed to one line: `liveBandHistogram` returning nothing means **"not mine",
not "does not exist"** — the mirror is global while the tree it is compared against belongs to one
branch, so files live on a third branch were booked out. Fatal to the design, not tunable; four
variants measured, every one that cleared negatives inflated the head count.

Four things it got right, each measured, all still true:

1. Correct the _count_, never the _band_. `mergeLineValues` adopts the older side's ownership and a
   later merge can adopt the other side again, so band corrections oscillate — on
   `backend-for-microscope` re-attribution carried 53 505 of mass against a real defect of 37 444
   and drove single cells from −126 to −25 748.
2. Correcting only the restoring direction is also wrong. A double removal both pushes one band
   below zero _and_ credits the same lines to another.
3. A correction must be booked at the sample the faulty delta landed in, not at the merge tick —
   otherwise every row in between stays wrong and shows up as a step in the surface.
4. The mirror must be updated when changes are **emitted**, not when they are produced.

**Attempt 2 — reconcile from per-branch contributions instead.** Track `D_i` (each branch's deltas
per file and band since the fork), snapshot `H_pre` (branch 0's band histogram) before the
`file.Merge()` loop and `H_m` after; correct by `(H_m − H_pre) − Σ_{i≥1} D_i`. No global state, so a
file live on a branch outside the merge is untouched — attempt 1's exact failure. Two implementation
facts worth keeping: accounting has to be **per fork, not per branch object** (a stack with one
frame per fork — the fork origin keeps its branch slot and otherwise offers an enclosing merge
deltas from before the fork), and `H_pre` has to be snapshotted in `Consume()`, not `Merge()`,
because consuming a merge commit overwrites the ownership of every line it touches with
`TreeMergeMark`.

Killed by instrumenting the derivation against a mirror of what was actually emitted: `H_pre + Σ D_i`
matched the real accumulator in **29 % of corrections** (350 corrections on
`backend-for-microscope`, total drift 69 060; 53 negative corrections drove the true accumulator
29 339 lines below zero). The drift comes from `mergeLineValues`, which silently re-bands tree lines
without emitting a delta:

```
file 98, band (author 7, tick 325): derived 27894, really accumulated 0      -> books -27894
file 98, band (author 3, tick 520): derived     0, really accumulated 25717  -> books +25717
```

Same lines, two bands — `backend-for-microscope`'s −25 749 cell. **The tree is not a record of what
was accumulated, so no snapshot of it is a valid correction target.**

**Attempt 3 — emit the delta in `mergeLineValues`, where the ownership actually changes.** The
targeted defect is real and the unit test pins it (`TestFileMergeDisplacedOwnershipIsReported` fails
without the emission). The corpus says no. One-sided (book out the destination's claim when it loses
to an older version), full 13-repo gate: **3 regress, 10 unchanged, none improve** —
`mekorp-backend` 1 748 → 1 877, `mekorp-webclient` 1 447 → **1 724**, `meko-etl-tool` 553 → 606. The
symmetric variant (book out whichever side loses) is strictly worse and costs `render-pdf` its clean
0 / 0. Why: `mergeLineValues` pairs the two files **by position**. Lines at the same index are not
the same line — they are two files of equal length laid side by side. "Both branches introduced this
line" is an inference the function is not entitled to make, and the symmetric version makes twice as
many such claims and is twice as wrong, which is the confirmation.

**Conclusion.** Attempt 2 hit the wall from the accumulator side and attempt 3 from the tree side:
**neither the merged contents nor the merge itself can supply the correction.** Closing B1c means
carrying line identity across the fork rather than reconstructing it at the merge — a change to what
`File` stores, not to how a merge is accounted. Substantially larger than any of the three attempts.

**Also blocked on the same wall: the merge buffer hand-over (P4).** `synchronizeLineHistoryBranch`
clears `target.pendingChanges`; a target that performed a merge of its own and is synchronized
before consuming another ordinary commit loses those deltas entirely. Confirmed defect, trigger
shape occurs 1 097× in `mekorp-webclient`. The fix was measured and **rejected on evidence**, and
the repro landed **skipped**. It is a dependent of this phase, not an independent one, and dies with
it if B1c is closed as won't-fix — see [appendix](#appendix-a).

### 4.5 What "carrying line identity" would cost — scoped, not built

`File` stores no lines. It is `{tree *rbtree.RBTree; updaters []Updater; Id FileId}`
(`file.go:29-33`) over a tree of **run boundaries**: `rbtree.Item{Key, Value uint32}`
(`rbtree.go:38-41`), `Key` = the run's first line index, `Value` = the packed owner (tick in the low
14 bits, author above — `line_history.go:926-937`). A 1 000-line file with 20 ownership runs costs
21 nodes of 24 B, not 1 000. `Value` has **no spare bit**: 14 tick + 18 author, with three author
values and one tick value already reserved. The only output is `updateTime(curr, prev, delta)`
(`file.go:554-571`) — a count, with no position and no identity.

**(a) A stable per-line id minted at insertion.** The only one that is not an inference. Mint from
the counter that already survives `Fork` by pointer (`line_history.go:57`, `525-527`), shift ids with
the tree, fold two branches' removals on `(FileId, lineId)`. The only option that also repairs the
mis-credited _positive_ bands, because with real ids `mergeLineValues` could emit an honest re-band
delta instead of silently overwriting (`file.go:411-436`). Price is structural: ids are unique per
line, so **runs stop compressing** — the hottest structure in the program goes from O(runs) to
O(lines) nodes, 20–50× on typical source, ×24-32 B, **multiplied by every live branch** because
`Allocator.Clone` copies storage eagerly (`rbtree.go:91`). Also needs a seventh hibernation buffer
and a format-version bump (`rbtree.go:190-380`), and invalidates `mergeLineValues`' index pairing and
length-equality panic (`file.go:417-420`) — with ids a merge is a set union and the parents need not
be equal length. Keep the ids **internal** and fold buffered removals at `Merge` (as
`foldMergeRemovals` already folds by `FileId`, `line_history.go:617-633`); putting the id into
`core.LineHistoryChange` instead would break the coalescing at `line_history.go:1060-1069`, the
`linedump` text format and all of `line_loader.go`, for no gain. Deferred folding is legal because
burndown accumulates order-independently (`burndown.go:989-991` into a sparse map, grouped only at
`Finalize`) — the same property `pendingChanges` already relies on.

**(b) A per-`(fork, file, band)` removal budget.** Cheap, no `File` change — and it cannot express
"the same line", only "the siblings between them removed more than the band held". That makes it a
clamp, and clamps walk straight into the traps below: suppressing a removal raises the head total.
Every variant already measured and rejected has this shape.

**(c) Per-branch accumulators — abandon `ForkSamePipelineItem` for the five leaves.** Costs memory
×branches on `globalHistory`/`peopleHistories`/`matrix`/`fileHistories`, revives B11's hibernation
hazard, needs a real `Fork`/`Merge` on all five leaves (`MergeResults` is a different operation and
cannot be reused — sibling histories share a tick axis and must not be rebased), and then
**inherits attempt 2 wholesale**: at the merge you still have to decide which sibling's deltas are
real, and without line identity the only thing left to compare against is the merged tree. Same
wall, more code.

### 4.6 Recommendation and the tasks that would settle it

**Recommendation: do not start.** The premise holds, so this is about price. Only (a) is
mechanically sound, and its honest price is a 20–50× blowup of the hottest structure in the program
times the branch fan-out, plus a hibernation format change — against 327 residual file cells and
~4 100 project cells corpus-wide, concentrated in one repository, on a metric that warns rather than
fails. Not a trade worth making on the evidence in hand.

If B1c is reopened, **nothing below may be skipped**, in this order:

- [x] **T4.1 — Is the double removal actually the residue?** **Done 2026-07-31, yes.** See §4.3.
- [ ] **T4.2 — Is the price real?** Prototype (a) as _ids only, no behaviour change_ — mint, store,
      shift, assert invariance — and measure peak RSS and wall clock on `mekorp-webclient` (1 097
      merge-of-merge commits) and `mekorp-backend`. Inside ~2× current, (a) becomes arguable.
      **This is the deciding measurement, and the one that can still kill (a) outright.**
- [ ] **T4.3 — Gate on both metrics, on all 13 repositories.** `negative_cells` _and_
      `residual_cells`, plus the project matrix's final-row sum against `git grep -I -c '' HEAD`.
      Negatives and head totals trade against each other; every variant so far that fixed one broke
      the other.

### 4.7 Traps for whoever picks this up

- **Judge on the corpus suite, never on an ad-hoc repository set.** The five-repository set used
  throughout the older measurements (`render-pdf`, `personio-ipoffice-sync`,
  `backend-for-microscope`, `meko-etl-tool`, `mekorp-backend`) misses `mekorp-webclient`, which was
  the single largest regression attempt 3 caused.
- **Negatives and head totals trade against each other.** Every variant measured across all three
  attempts that cleared negatives inflated the project matrix's final-row sum relative to
  `git grep -I -c '' HEAD`, and vice versa. Both halves are mis-attributions of the same kind, so a
  change that only moves one of them has not been validated.
- **Every ad-hoc run must pass `--diff-timeout=3600` or higher** and its `.err` must be grepped for
  `time budget` before the numbers are used. The binary warns on stderr (`file diffing hit its time
budget and stopped early … not reproducible`) but **exits 0**. Two figures reported during P3/P4
  came from truncated runs and were withdrawn. Corpus runs are unaffected — the suite pins the
  timeout and fails the repository as `MEASUREMENT INVALID`.
- **The old binary's clamped output is not something to converge towards.**
- **Closed negative result — do not redo.** Per-_change_ dedup in `foldMergeRemovals` (`seen` is
  marked on the first change, not the first branch, so a file spanning several ownership bands has
  only its first band booked out) is a genuine loss and was the top-ranked finding of two
  independent audits. Correcting it makes everything worse at once: on `meko-etl-tool` the head
  total goes 24 009 → 20 162 against a git ground truth of **24 908**, negative cells 553 → 933, mass
  84 464 → 371 449. The dropped bands are load-bearing — they mask B1c's double removal. Reverted,
  with the numbers recorded in a comment at the call site.

### 4.8 Preserved code

The three attempts were never merged and live only in the session scratchpad:

- `scratchpad/b1c-patch/` — attempt 1 (`merge_reconcile.go` + `line_history.diff`).
- `scratchpad/b1c-attempt2/` — attempt 2 (`merge_reconcile.go` + a diff against `2cb415d` for
  `line_history.go` and `file.go`); variants behind `HCK_RECONCILE`, `HCK_RESTORE_ONLY`, `HCK_TRACE`.
- `scratchpad/b1c-mergelinevalues/symmetric.diff` — attempt 3, symmetric version, containing the
  one-sided version as its first half plus both unit tests.

What _did_ land: P3 as `internal/linehistory/merge_adoption.go` (PR #6, `41bced3`); P4's repro
**skipped** in `internal/linehistory/line_history_merge_test.go` (`b1f438f`). P4's rejected fix
(`adoptBranchPendingChanges` plus the path-keyed `foldMergeRemovals` dedup) was **not** kept.

Worktree builds need `GOFLAGS=-buildvcs=false` **and** `CGO_ENABLED=0` (the render packages pull in
freetype via matplotlib-go and fail on a missing `ft2build.h`).

---

<a id="phase-5"></a>

## Phase 5 — recorded, deliberately not done

Nothing here is scheduled. Recorded so it is not repeatedly re-proposed or re-discovered as an
oversight.

- **Metrics hercules cannot answer at all (M10).**
  - _Everything from the forge_ — review latency, review coverage, who-reviews-whom, PR size,
    time-to-merge. Hercules reads git; none of this is in git. For a 38-person organisation probably
    the most valuable missing dimension, and it belongs in a separate collector feeding alongside
    hercules, not in a leaf.
  - _Test against production code._ `LanguagesDetection.detectLanguage`
    (`internal/plumbing/languages.go:126-135`) calls `enry.GetLanguage(path.Base(name), blob.Data)`
    at `:131` — content-aware, but language-only. No leaf, plumbing item or mode records a
    test/non-test distinction. `enry` already exposes `IsTest`/`IsVendor`; this fork never calls
    them. A test-ratio-over-time chart is close to free once one is wired into the languages leaf.
  - _Cross-repo dependency/import graph._ `ImportsPerDeveloper` sees imports per developer, never per
    repository, and is not mergeable (T3.3). Needs a manifest parser, not a git walk.
- **`TestFileMergeShallow` (`file_test.go:639`) / `TestFileMergeDeep` (`:678`) assert
  `status[6] == 10`** (lines `674` and `713`) for a band the merged file holds no lines of —
  confirmed. That is the `mergeLineValues` re-banding defect seen from the test side: the assertions
  encode the current behaviour, not the correct one, and would have to change under §4.5(a).
- **`RefactoringProxy` discards the raw rename count**, storing only
  `RenameRatios[i] = Renames/TotalChanges`. Merging works exactly anyway (the count-weighted mean
  keeps `r*t` in `float64`; residual error ~1e-16 against the ~1e-7 the existing `float32` narrowing
  already costs), so adding a `Renames []int` field to the result and `pb.proto` would buy nothing
  measurable. **Not done on purpose.**
- **`SubsystemConcentration` is not summed** in the B13 fix — it keeps the _more concentrated_ of two
  readings of a directory, matching the max `SubsystemBusFactor` already takes. It cannot be summed:
  the result carries the per-directory Gini/HHI but not the distributions they were computed from.
  Making it additive means adding those distributions to both the result and `pb.proto` — worth
  doing only if a subsystem reading of a _combined_ run is actually wanted, and summing a directory
  named `src` across unrelated repositories is a questionable statistic in itself.
- **Combined file burndown has never existed.** `BurndownAnalysis.MergeResults` merges
  `GlobalHistory`, people and repositories but never `FileHistories` — and **upstream does not
  either**. Per-repository `--burndown-files` works (305 files render from `meko-etl-tool` alone);
  the combined chart directory comes out empty with `Burndown stats for files were not collected`.
  Worth a warning instead of an empty directory, but that is a downstream ergonomics item.
- **Intentionally deferred, revisit only on a concrete trigger** (carried over from the Go-renderer
  migration): native JSON export, code-review metrics, remote cloning, caching.

---

<a id="appendix-a"></a>

## Appendix A — done, for cross-reference

Full diagnoses are in this file's git history and in the merged PRs.

### B1c phases P0–P4, P6

| phase  | what                                                                                | outcome                                                                                                      |
| ------ | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| **P0** | trustworthy measurement — corpus suite, determinism, truncation detection           | ✅ done                                                                                                      |
| **P1** | merge-resolution deltas reach the accumulator at all (B1, B1b, B1d)                 | ✅ done; line totals track the `082bf15` baseline within 0.5 %                                               |
| **P2** | rename identity — exact renames must not become delete+create                       | ✅ done; `render-pdf` lost 99.8 % of its negative mass, worst cell −7386 → −58                               |
| **P3** | file identity across a merge — `adoptMergeCreatedFileIds`                           | ✅ merged (PR #6, `41bced3`), gate PASS 13/13, **−44 % negative file cells, −45 % residual**, none regressed |
| **P4** | merge buffer hand-over — `synchronizeLineHistoryBranch` drops a branch's own deltas | 🟡 defect confirmed, repro landed **skipped** (`b1f438f`), fix measured and **rejected**; dependent of §4    |
| **P6** | downstream acceptance in `ewws-statistics`                                          | ✅ passed — 36/36, 0 failed, all three workaround switches re-enabled and then **deleted** (`079b0bc`)       |

Facts from those phases that stay load-bearing:

- **P3's fix has two parts, both required.** Re-key merge-minted files onto the creating branch's id
  _before_ marks resolve, and **do not** register the vacated id as an abandoned name —
  `abandonedFileID` prefers the _highest_ matching id, so leaving it behind lets a later merge
  resurrect a key holding no accounting. Adoption must also skip an id **still live at another
  path** (a rename carries the `FileId` with it): `adoptableFileId` consults a `liveFileIds` map
  (`8fb2305`), pinned by `TestLinesMergeSkipsAdoptionOfIdLiveAtAnotherPath`.
- **P3 is a demonstrated no-op on the project dimension** — identical in all 13 repositories,
  exactly as `updateGlobal`'s missing `FileId` predicts. Its apparent `mekorp-webclient` regression
  (1 447 → 1 481) was **truncation noise**, gone once `--diff-timeout` was pinned.
- **The corpus baseline was re-seeded on a clean tree at `41bced3`**, `"working_tree": "clean"`,
  13/13 PASS, zero truncation. The baseline is **not comparable to anything older than `abc217c`**.
- Tests landed with P3: `TestLinesMergeAdoptsCreatingBranchFileId` (structural invariant) and
  `TestLinesMergeAdoptedFileIdKeepsRemovalPaired` (the accounting consequence), both verified to
  fail with the fix reverted. The second prints the defect directly — without adoption the path's
  deltas are `map[1:10 2:-10]`. The _aggregate_ net is zero either way, so only the per-`FileId`
  breakdown discriminates; an aggregate assertion would have been a false negative.
- **P4's rejected hand-over, measured** on `mekorp-webclient`, `--diff-timeout=300000`, untruncated:
  HEAD 1 413 cells / 5 104 060 mass / −77 421 worst / 43 residual → hand-over 1 433 / 5 121 627 /
  −77 422 / **44**. The +20 cells sit in three matrices and one is _residual_, so not transient
  noise. Cause: `foldMergeRemovals` writes into `analyser.changes`, which `Merge` folds into
  `pendingChanges`, so inner-merge **deletions** travel in the buffer and get re-emitted, recreating
  B1c's own double removal. Dedup by path or by `FileId` both leave a residue — the same wall §4 hits.

### Bugs

| item       | what it was                                                                                                                                                                                                                                                                                                | commits                         |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------- |
| B1         | merge-resolution deltas discarded, project matrix corrupted                                                                                                                                                                                                                                                | `9e570a3`, `1be72df`            |
| B1b        | file deletion inside a merge commit emitted nothing (B1 alone overshoots, B1b alone deepens the undercount — read together)                                                                                                                                                                                | `1abecda`, `9c010ef`            |
| B1c (half) | `sortableChange.Less` was not a valid ordering (returned `true` on the first smaller byte without checking whether an earlier byte was already greater), so `matchExactRenames`' single merge scan turned R100 moves into delete+create. Same bug is in upstream, which is why B1c reproduces on `082bf15` | —                               |
| B1d        | the merge commit was consumed by only one branch; merged lines now keep their real author. Does **not** absorb the residue — cell counts fell in three repositories but negative mass rose in two, which is B1d working                                                                                    | —                               |
| B2         | person matrices have _always_ contained negatives; decided **(a) the accounting is wrong** — no negative cell recovers by the final row. Demoted to a warning behind `--strict-burndown-balances`; the defect itself is B1c, magnitude in §4.2                                                             | `ebc8ded`, `a7c8cba`, `0ea3cc0` |
| B3         | `OwnershipSnapshot` underflow — a structural consequence of divergent branches sharing one accumulator; totals stay signed and only the metric snapshots clamp                                                                                                                                             | `fafb754`, `c04d08c`            |
| B4         | `--only` filtered after the merge; an unmergeable analysis sank the whole combine. `RefactoringProxy` is now mergeable, with tick axes rebased                                                                                                                                                             | `2f57e3d`                       |
| B5         | `HotspotRisk` merged to empty because `TopN` is 0 on the combine path                                                                                                                                                                                                                                      | —                               |
| B6         | `--blob-cache-max-blob-size` was fail-closed; oversized blobs are now recorded as binary and warned about                                                                                                                                                                                                  | —                               |
| B7         | `--lines-hibernation-disk` serialized an allocator that never hibernated                                                                                                                                                                                                                                   | —                               |
| B8         | person burndown aborted on contributors with no activity                                                                                                                                                                                                                                                   | `c04d08c`                       |
| B9         | YAML burndown serialization panicked on nil `PeopleMatrix`; the `--pb` path panicked too                                                                                                                                                                                                                   | —                               |
| B10        | non-deterministic execution plan; a merge could be resolved against the wrong branch                                                                                                                                                                                                                       | `5937149`                       |
| B11        | `changeHibernation` hibernated per branch index while `ForkSamePipelineItem` shares one instance, so a fork's idle side put its sibling's analyser to sleep mid-run                                                                                                                                        | `0dd3211`                       |
| B12        | rename matching raced two non-equivalent greedy matchers and kept whichever finished first — before this, single-run measurements were not trustworthy                                                                                                                                                     | —                               |
| B13        | combined bus factor and ownership reported **one repository**, not the corpus — both `MergeResults` picked the snapshot with the larger `TotalLines` instead of summing. Fixed 2026-08-01, details below                                                                                                   | —                               |
| suite      | opt-in corpus regression test, baseline-relative                                                                                                                                                                                                                                                           | `b97f549`, `c329229`            |

**B13's fix, because its two decisions are reusable.** Both merges now sum, sharing
`leaves/ownership_merge.go`: `mergeOwnershipTicks` rebases the two tick axes, translates author
indices and adds the distributions; each leaf recomputes its metrics from the sum.

- **Tick alignment** reuses B4's rebasing — `mergedTickOffsets`, extracted verbatim out of
  `RefactoringProxy` (which now calls it) onto the merged `BeginTime`. A repository that stops
  producing snapshots **carries its last distribution forward** — those lines still exist — and
  contributes nothing before its first snapshot. This composes across a reduce over N repositories,
  so the result does not depend on merge order.
- **Derived metrics come after the sum**, never merged from the parts: the bus factor is a threshold
  over the summed distribution; Gini and HHI are shape metrics over it.
- **Identities** go through `join.PeopleIdentities`, the same alias-graph join the burndown people
  merge uses, and the merged result now carries the _merged_ dictionary. It previously kept the
  first result's dictionary while copying the second's author indices verbatim, so the ownership pie
  was mislabelled as well as single-repository.
- Mismatching tick sizes (both leaves) and thresholds (bus factor) are now merge errors, mirroring
  `RefactoringProxy` — neither is comparable across repositories and both come from one global flag.
- Verified: `ewws-auth` 85 128 + `ewws-files` 10 180 + `ewws-render` 133 955 = combined **229 263**
  to the line, `people=18` as the union rather than the first repository's 16.
- **One cost worth knowing:** the merged tick axis is the union of all repositories' ticks and each
  tick carries a per-identity map, so a combined `.pb` grows roughly as (union of ticks) × (union of
  people). Correct data, but not the old size.

---

<a id="appendix-b"></a>

## Appendix B — how to judge a change: the corpus regression suite

`just test-corpus`, gated on `HERCULES_CORPUS_DIR` (`/mnt/projekte/Code/MeKo`), skipping cleanly when
unset so `go test ./...` stays green. 13 repositories, ~7 min to seed (~56 min for a full clean
re-seed of both dimensions).

- **It asserts no regression against `test/corpus/baseline.json`, not the absence of negatives** —
  the latter cannot pass while B1c's residue exists, so as written that suite would have been red
  from birth and ignored.
- Per repository it records exit code, negative cells, negative mass, worst cell and residual cells.
  **Only `exit_code`, `negative_cells` and `residual_cells` gate the build**; mass and worst cell are
  reported in both directions but ungated.
- **Two dimensions.** `flags`/`repositories` (project + people) and `file_flags`/`file_repositories`
  (`--burndown-files`), measured by a second run per repository so the project/people numbers stay
  produced by the identical command. Adding the file dimension exposed what the suite was blind to:
  repositories with a clean project matrix are **not** clean per file — `go-clients` 0 → 479,
  `render-pdf` 0 → 393, `ewws-render` 0 → 21. Corpus-wide the file dimension carried ~26 700 negative
  cells against ~4 100 at project scope, so **most of the accounting damage was never measured**.
- **The suite pins `--diff-timeout=300000`** (`abc217c`) and treats truncation as fatal — it matches
  `not reproducible` on the binary's stderr (hercules exits 0 when it truncates) and fails the
  repository as `MEASUREMENT INVALID`, refusing to parse, gate or record the numbers. Before that it
  ran at the 1 000 ms default (the flag is in **milliseconds** — `internal/plumbing/diff.go`) while
  gating `negative_cells`, which therefore depended on machine speed: `mekorp-webclient` measured
  1 447 truncated and **1 413** untruncated, and two binaries with identical negativity reported
  1 447 vs 1 481. **Ad-hoc runs are still on their own** — see the trap in §4.7.
- It reads YAML, not `.pb`, because `pb.ToBurndownSparseMatrix` clamps negatives away.
- `people_interaction` is excluded — see §4.1.
- `UPDATE_CORPUS_BASELINE=1` / `just update-corpus-baseline [REPOS]` re-seeds, preserving entries not
  re-measured. **Re-seed only after a change is understood, never to make a red run green.** The file
  records the commit and working-tree state it was measured against; seeding against a dirty tree has
  already invalidated one baseline once.
- Building inside a `git worktree` needs `GOFLAGS=-buildvcs=false`, otherwise the build fails with
  `error obtaining VCS status: exit status 128`.
