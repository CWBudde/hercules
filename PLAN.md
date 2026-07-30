# PLAN: correctness regressions blocking real-world use of 0.2.0

**Status:** open
**Filed:** 2026-07-30, against `b330adf` (tag `0.2.0`)
**Reference build for "worked before":** `082bf15` (the `Version: 0` binary still installed at `/usr/local/bin/hercules`)

## Why this exists

Repointing `MeKo/ewws-statistics` from the old `082bf15` binary to 0.2.0 took the
pipeline from _all repositories analysed_ to **14 of 38 failing outright**,
including the two most important ones (`mekorp-webclient`, 11 275 commits;
`mekorp-backend`, 8 172 commits). Every failure is a hard abort, not a degraded
chart — one bad invariant kills the whole run and its `.pb` is never written.

Downstream has been worked around by disabling analyses in `ewws-stats.yaml`
(`burndown_files`, `ownership_analyses`, `refactoring_proxy` are now all
opt-out). Those switches should be deleted again once the items below are fixed.

Corpus used throughout: `/mnt/projekte/Code/MeKo/*`, the 38 repositories listed
in `ewws-statistics/.core-repos`.

---

## B1 — merge-resolution deltas were discarded, corrupting the _project_ matrix ✅ FIXED (uncommitted)

**Two claims in the original filing were wrong; both were checked against builds.**

1. _"Only `--burndown-people` corrupts the project matrix."_ No. `ewws-wiki` fails
   identically with plain `--burndown`, with or without the people flag and with or
   without explicit `--granularity`/`--sampling`. `GlobalHistory` never sees an
   author: without people tracking every author is clamped to `AuthorMissing`, which
   only disables `updateAuthor`/`updateChurnMatrix`. The flag is incidental.
2. _"`082bf15` was OK."_ Only its _output_ was. Until `3e31507` both serializers
   clamped negatives away — `yaml.PrintMatrix(..., fixNegative=true)` and
   `pb.ToBurndownSparseMatrix`'s `if v < 0 { v = 0 }`, the latter still carrying
   upstream's `TODO(vmarkovtsev): find the root cause of tiny negative balances`.
   Rebuilding `082bf15` with `fixNegative=false` shows its project matrix was already
   negative on `ewws-wiki` (22 cells, min `-227`) and `render-pdf` (53 cells, min
   `-6912`). 0.2.0 did not start producing negatives, it started _reporting_ them. Any
   "worked before" baseline read from the old binary's YAML or `.pb` is an artifact of
   that clamp.

### Root cause of the regression (fixed)

`a16f55f` moved merge accounting from burndown into `internal/linehistory` and lost the
deltas in transit. A merge commit is consumed with `analyser.tick = TreeMergeMark`
(`line_history.go:352-357`), and `File.updateTime` (`file.go:542-560`) suppresses every
updater while that mark is set — by design, since the sibling parents' state is only
reachable in `Merge()`. `Merge()` then resolves the marks via `resolveMergeMarks`
(`file.go:436-455`), which produces exactly the missing positive deltas — and threw them
away on the next line (`analyser.changes = nil`, `line_history.go:514`). Nothing
recovered them: `Merge` has no output channel into the pipeline, and
`BurndownAnalysis.Merge` is a deliberate no-op.

Lines first owned at a merge were therefore never inserted into `globalHistory`, while
their later removal still emitted a negative delta.

**Fix** (uncommitted): buffer the deltas in `LineHistoryAnalyser.pendingChanges` and
emit them with the next commit the branch consumes; `Fork` and
`synchronizeLineHistoryBranch` clear the buffer so no clone re-emits it. For a history
that _ends_ on a merge commit (20 of 288 repos under `/mnt/projekte/Code/MeKo`) there is
no next commit, so `FileIdResolver.PendingChanges()` exposes the leftovers and
`BurndownAnalysis.Finalize` / `CodeChurnAnalysis.Finalize` drain them. `hotspot_risk`
needs no drain — it reads ownership from the live resolver tree. Regression tests:
`internal/linehistory/line_history_merge_test.go`.

### Measured effect

Project matrix with output clamping disabled in all three builds so the raw numbers are
comparable; default timeouts, `--granularity=30 --sampling=30`:

| repository               | 082bf15                         | 0.2.0                          | 0.2.0 + fix            |
| ------------------------ | ------------------------------- | ------------------------------ | ---------------------- |
| `ewws-wiki`              | 22 neg, min −227, Σ 1 098 211   | 14 neg, min −1, Σ 636 783      | **0 neg**, Σ 1 104 135 |
| `process-manager`        | 0 neg, Σ 241 786                | 3 neg, min −118, Σ 226 776     | **0 neg**, Σ 260 003   |
| `personio-ipoffice-sync` | 3 neg, min −18                  | 3 neg, min −18                 | 3 neg, min −18         |
| `render-pdf`             | 53 neg, min −6912, Σ 3 563 642  | 53 neg, min −6817, Σ 3 173 527 | 53 neg, Σ 5 787 185    |
| `backend-for-microscope` | (old binary produced no output) | 64 neg, min −43                | 64 neg, min −43        |
| `meko-etl-tool`          | (old binary produced no output) | 86 neg, min −2084              | 44 neg, min −2120      |

`ewws-wiki` is the clean case: 0.2.0 was losing 42 % of the accumulated lines
(636 783 vs 1 098 211); the fix restores them to within 0.5 % of the old baseline and
removes every negative. `process-manager` likewise returns to zero.

**Superseded by B1b — read that table, not this one.** The "0 neg" columns here and
`render-pdf`'s Σ 5 787 185 are what B1 looks like _in isolation_; the negatives it appeared
to remove were merely being cancelled by the deletion leak B1b fixes.

### What is left (not this regression)

`personio-ipoffice-sync`, `render-pdf`, `backend-for-microscope` and `meko-etl-tool`
still have negative project cells, unchanged or barely changed — and `render-pdf`'s were
already there in `082bf15` at the same magnitude. That is a **separate, long-standing
accounting bug of the same class as B2**, and it is what actually keeps these
repositories failing. See B1c.

**Note for B2's decision:** it now applies at project scope too, so the choice between
"fix the accumulator" and "clamp + warn" cannot be scoped to people alone.

---

## B1b — file deletion inside a merge commit emits nothing ✅ FIXED (uncommitted)

`handleDeletion` suppressed both the removal deltas (`file.Update` with `TreeMergeMark`,
which makes `File.updateTime` return before running any updater) and the
`NewLineHistoryDeletion` marker when `analyser.tick == TreeMergeMark`, yet dropped the
file from `analyser.files` regardless. Its lines stayed in `globalHistory` forever.

**Fix:** a removal has nothing for `Merge()` to resolve — every removed line already
carries its own owner, and the path is gone from this branch's tree either way — so it is
charged to the real commit tick instead of the mark. `LineHistoryAnalyser.commitTick`
holds that tick while `tick` carries `TreeMergeMark`; `deletionTick()` falls back to the
mark only when the file's own lines are still unresolved marks, which `updateTime()`
refuses to touch and which were never added to any history anyway. Regression tests:
`TestLinesDeletion*` in `line_history_merge_test.go`.

### The second claim in this item was wrong

`synchronizeLineHistoryBranch` does **not** leak deltas. `File.Delete()` only calls
`tree.Erase()`; it runs no updaters and emits nothing, so the `target.changes = nil`
next to it discards an empty slice. There is no bug there.

### Measured effect — this is what actually restored the line totals

Same three-build setup as B1 (clamping disabled, default timeouts,
`--granularity=30 --sampling=30`):

| repository               | 082bf15                         | B1 only                | B1 + B1b                       |
| ------------------------ | ------------------------------- | ---------------------- | ------------------------------ |
| `ewws-wiki`              | Σ 1 098 211, 22 neg, min −227   | Σ 1 104 135, **0 neg** | Σ 1 097 779, 22 neg, min −227  |
| `render-pdf`             | Σ 3 563 642, 53 neg, min −6912  | Σ 5 787 185, 53 neg    | Σ 3 549 470, 53 neg, min −6836 |
| `process-manager`        | Σ 241 786, 0 neg                | Σ 260 003, 0 neg       | Σ 243 549, **0 neg**           |
| `meko-etl-tool`          | (old binary produced no output) | Σ 5 003 113, 44 neg    | Σ 4 848 902, 116 neg           |
| `backend-for-microscope` | (old binary produced no output) | Σ 1 594 601, 64 neg    | Σ 1 594 601, 64 neg            |
| `personio-ipoffice-sync` | Σ 73 182, 3 neg                 | Σ 73 182, 3 neg        | Σ 73 182, 3 neg                |

Every repository with a baseline now lands within 0.5 % of it — `render-pdf`'s 62 %
overshoot under B1 alone is gone. The inflation was real and this was its cause.

**Honest consequence:** `ewws-wiki`'s zero-negative result under B1 alone was an accident.
The missing deletion negatives were partially cancelling the double-counted merge
positives; with both sides accounted for, `ewws-wiki` reproduces `082bf15` exactly (22
cells, min −227) and `meko-etl-tool` goes from 44 to 116 negative cells. B1b makes the
totals right and re-exposes the long-standing negatives underneath — which are B1c/B2, not
a new defect. At project scope the corpus therefore still fails until B2 is decided.

---

## B1d — the merge commit is consumed by only one branch 🟠

Not filed originally; found while measuring B1b. Upstream replays the merge commit on
**every** parent branch (`if parentBranch != branch { appendCommit(commit, parentBranch) }`
in `appendMergeIfNeeded`), so each branch diffs it against its own head and all branches
end up holding the same tree. `Merge()` then compares them line by line and hands each
merged line back to its original author and tick.

Our `planBuilder.mergeBranches` (`internal/core/forks.go`) appends the merge commit to
`mergeBranch` only. The siblings stay at their pre-merge state, so
`matchingMergeFiles`'s `file.Id == target.Id && file.Len() == target.Len()` test rejects
them, `resolveMergeMarks` finds every merged line still marked, and attributes the lot to
the merge author at the merge tick — a second count of lines the sibling branch already
counted. The `Len()` filter itself only exists to stop `mergeLineValues` from panicking on
the length mismatch that this causes; upstream needs no such filter.

The divergence predates `082bf15` (which had no merge handling in `linehistory` at all —
merge commits were consumed as ordinary commits, double-counting the same way). B1b masks
the symptom by restoring the compensating negatives, so totals now agree with the old
baseline, but the _attribution_ is still wrong: merged lines are credited to whoever
pushed the merge button rather than to their author. That corrupts `--burndown-people`,
`--devs` and ownership, and is a plausible contributor to B2's person-scope negatives.

Evidence that this was a deliberate-looking but unnoticed change: `TestPrepareRunPlanBig`'s
"extra commit actions" column is `0` in every case here, against upstream's `1`–`7`.

Fixing it means restoring the replay in `forks.go`, teaching `isMergeAction`
(`pipeline_execution.go:238`) to recognise the adjacent replicas the way upstream's
`isMerge` does, and re-baselining `TestPrepareRunPlanBig`. Every leaf then sees merge
commits once per parent branch, as upstream does — so commit counts in `devs`,
`temporal-activity` and friends need checking before this lands.

---

## B1c — project-scope negatives predating 0.2.0 🔴

The residue from B1's table. Reproduces on `082bf15` once output clamping is disabled,
so it is long-standing and was only ever hidden. Mechanically the same problem as B2 and
should be decided together with it. It **no longer aborts** — see B2's demotion — but the
accounting is still wrong.

**Affected in the corpus:** `backend-for-microscope`, `meko-etl-tool`, `mekorp-backend`,
`personio-ipoffice-sync`, `render-pdf`.

---

## B2 — person burndown matrices have _always_ contained negatives 🟠

Distinct from B1, and important not to conflate with it: the **people** matrices
went negative in `082bf15` too. 0.2.0 did not break this — it started checking.

```bash
/usr/local/bin/hercules --burndown --granularity=30 --sampling=30 \
    --burndown-people /mnt/projekte/Code/MeKo/render-pdf > old.yaml
# → exit 0, but the `people:` section contains 80 negative integers, e.g.
#   11874  0  -4487  -23  -1361  -1418  -17  -76  -2  -1612  0  -5  0
```

So `validateBurndownResultBalances` (`leaves/burndown_shared.go:67`, called from
`leaves/burndown.go:581,597,735,747,750`) is surfacing a long-standing accounting
bug and converting silently-wrong output into a fatal error.

**Decision needed before writing code** — these are mutually exclusive:

- **(a) The invariant is right, the accounting is wrong.** Per-person alive-line
  counts must never go negative, so fix the accumulator. Highest value, hardest;
  every person-scoped number ever produced by this tool has been suspect.
- **(b) Negatives are legitimate in some edge case** (rename chains, merge
  re-attribution, authors resolved after their lines were counted). Then the
  validator is too strict: clamp to zero, count the corrections, and emit one
  warning per run instead of aborting.

Whichever is chosen, **the validator must not abort a whole multi-hour run over
a single `-1`.**

### The demotion is done ✅ (uncommitted) — the decision is not

`validateBurndownResultBalances` was replaced by `auditBurndownResultBalances`, which scans
the whole result instead of stopping at the first bad cell, and `reportBurndownBalances`,
which applies the policy. Default: warn once and continue. `--strict-burndown-balances`
(a shared option, so both `Burndown` and `LegacyBurndown` honour it) restores the abort and
reports the first offending cell in scan order, so the strict message stays stable.

The warning names the _worst_ cell plus the extent, which is far more useful than the first:

```
[WARN] negative burndown balance: person "clausbissinger|…" became -7386 at tick 1230
       (row 41), age band 300 (column 10) during finalization; 208 cell(s) affected in
       total (person: 155, project: 53). The matrix is reported as computed - pass
       --strict-burndown-balances to fail the run instead.
```

It is emitted at most once per analyser (`balancesReported`), because the same result passes
finalization, serialization and any merge it joins — three copies of one diagnostic is noise.

**What reaches the output.** `pb.ToBurndownSparseMatrix` already clamps (`max(value, 0)`), so
the `.pb` that `ewws-statistics` consumes is unchanged by this — it never sees a negative.
The YAML serializer does not clamp, so `-o -` output now shows the raw negatives. That is a
diagnostic format, and hiding the defect there again would be a step backwards.

**Measured:** all eight repositories that aborted at project or person scope now exit 0 and
write their `.pb` (`ewws-wiki`, `render-pdf`, `personio-ipoffice-sync`, `meko-etl-tool`,
`backend-for-microscope`, `mekorp-personio-sync`), with `process-manager` and
`ShelfStockScanner` now clean of negatives entirely thanks to B1/B1b. Tests:
`leaves/burndown_balances_test.go`.

This changes nothing about the accounting. The choice between (a) and (b) above is still
open, and is now purely about whether those cells should exist — not about whether they
should kill the run.

**Affected in the corpus:** 6 repositories were failing at `person` scope
(`ShelfStockScanner` −947 "Tobias", `backend-for-mekorp`, `meko-dms-to-yuuvis`,
`mekorp-personio-sync` −110 "Claus", `mekorp-tablet-client`, `ms-graph-to-mekorp`). They now
complete; `ShelfStockScanner` has no negative cells left at all after B1/B1b, so part of this
was B1 after all.

---

## B3 — `OwnershipSnapshot` underflows, killing `--bus-factor` / `--ownership-concentration` 🔴

Both flags share the accumulator added in `745f25b`, so either one aborts the run:

```bash
hercules --burndown --bus-factor --pb --skip-blacklist \
         /mnt/projekte/Code/MeKo/ewws-render > /dev/null
# → run pipeline: OwnershipSnapshot failed to consume commit:
#   update ownership snapshot: incremental ownership total became negative:
#   file 39 author 5 has -12 lines after delta -32
```

Also reproduces on `go-clients` (`file 35 author 1 … -20`). `ewws-tailscale`
passes, so it is repository-dependent.

**Re-measured after B1/B1b** (which changed what the shared line-history dependency delivers):
unchanged. `ewws-render` still fails on `file 39 author 5 … -12 after delta -32`, `go-clients`
still on `file 35 … -20 after delta -34` (author index moved 1 → 3, magnitudes identical),
`ewws-tailscale` still passes. The merge-delta fix did not touch this.

**One confirmed defect, and it is not the whole story.**
`ownershipSnapshotAccumulator.apply` (`leaves/ownership_snapshot.go:166`) skips
`change.IsDelete()` entirely. A deletion sentinel means _drop the whole file_, so
its per-author lines stay in `authorLines`/`totalLines` forever — every later
snapshot is inflated, and a reused `FileId` starts from stale state. Compare
`leaves/burndown.go:336`, which correctly calls `updateFileDelete`.

Adding a `forget(FileId)` that subtracts the file's authors and deletes the entry
was tried and **did not** fix the underflow (the error moved but persisted), so
there is a second cause. Two candidates, both visible in the same function:

1. Line 167 also skips every change with `PrevAuthor == core.AuthorMissing`,
   including positive deltas. If lines can be _added_ under `AuthorMissing` and
   later removed under a resolved author, removals are counted while the matching
   insertions never were — which is exactly this underflow signature.
2. `burndown.go:344-348` clamps `PrevAuthor >= peopleCount` back to
   `AuthorMissing`; `ownership_snapshot.go` does no such clamping. With a
   `--people-dict` in play the two consumers disagree about who an author is.

Fix the delete handling regardless — it is unambiguously wrong — then chase the
sign error.

---

## B4 — four analyses are unmergeable, and `combine --only` does not help them 🟠

`hercules combine` fails outright if _any_ input `.pb` contains one of:

```
CommitsStat   FileHistoryAnalysis   ImportsPerDeveloper   RefactoringProxy
→ "<Name>: ResultMergeablePipelineItem is not implemented"
```

One unmergeable analysis takes down **every** combined chart, not just its own.

**`--only` is advertised as the escape hatch and is not one.** It still attempts
to merge everything present in the inputs:

```bash
hercules combine --only Burndown output/data/hercules-pb/*.pb > /dev/null
# → FileHistoryAnalysis: ResultMergeablePipelineItem is not implemented
```

Tested with all 13 analysis names — every single one fails on some _other_
analysis in the file. Either `--only` should filter inputs before merging (which
is what the help text implies), or the docs should stop implying it.

Actions:

- Make `--only` actually restrict the merge set. This alone makes the four
  unmergeable analyses harmless.
- Implement `ResultMergeablePipelineItem` for `RefactoringProxy` (the only one of
  the four that has a labours mode and is therefore worth merging).
- Consider skip-with-warning rather than hard failure for the rest.

Downstream workaround: `ewws-statistics` stopped requesting `--commits-stat`,
`--file-history` and `--imports-per-dev` altogether — no labours mode renders
them, so they were pure cost plus a combine failure.

---

## B5 — `HotspotRisk` merges to empty 🟠

Renders correctly from a single repository's `.pb`, produces nothing after
`combine`:

```bash
hercules --hotspot-risk --pb --skip-blacklist ../ewws-render > hr.pb
labours -i hr.pb -f pb -m hotspot-risk -o hr.svg          # → OK, ranked file table
hercules combine hr.pb hr.pb > hr2.pb
labours -i hr2.pb -f pb -m hotspot-risk -o hr2.svg        # → "no hotspot risk files found"
```

The merge drops the file list. Downstream now generates `hotspot-risk` per
repository only.

---

## B6 — `--blob-cache-max-blob-size` is fail-closed, and that is a trap 🟡

Documented in `docs/SCALING.md` as a memory bound, and it reads like one, but a
blob over the limit **aborts the run**:

```
run pipeline: BlobCache failed to consume commit:
cache new blob "bin/ewws-events" (…): blob exceeds cache size limit
```

`ewws-events` passes at the 100 MiB default and fails at 20 MiB. Any repository
that has ever committed a binary is a landmine, so the knob cannot be lowered as
a routine memory measure — which is exactly what SCALING.md's "lowered for
constrained environments" invites.

This may be deliberate (SCALING.md does say "returns an explicit error … Hercules
does not substitute empty content"), but then it is misfiled as a tuning knob.
Suggested: skip-and-warn for the _cache_ (the blob is still readable, it just
isn't retained), and reserve hard failure for the case where correctness truly
depends on it. At minimum, say plainly in the flag help that this aborts.

---

## B7 — `--lines-hibernation-disk` panics during serialization 🟡

```
panic: serialization requires the hibernated state
  internal/rbtree/rbtree.go:177
```

Hit on `mekorp-backend` with
`--lines-hibernation-threshold=200000 --lines-hibernation-disk`. Did not
reproduce on `ewws-auth` with the same flags, so it needs specific state.

`Allocator.Serialize` panics when `allocator.storage != nil`, i.e. it was called
on a non-hibernated allocator. `LineHistoryAnalyser.Hibernate`
(`internal/linehistory/line_history.go:577`) does call `fileAllocator.Hibernate()`
before `Serialize` at line 599, so something re-boots or forks the allocator in
between — likely a branch `Fork`/`Merge` racing the hibernation, given this only
shows up on a repository with heavy branching.

A panic is the wrong failure mode regardless: return an error.

---

## B8 — person burndown aborted on contributors with no activity ✅ FIXED (uncommitted)

`personBurndownActivity` returned `errNoPersonActivity` when a person's matrix
was all zeros, and `BurndownPersonWithOptions` propagated it — so **one** idle
contributor destroyed the entire fan-out. A `--people-dict` routinely covers more
people than any single repository set does, so this fires constantly: our
"External" group has 0 commits in `.core-repos`, which cost us all 25 person
charts.

Fixed in `internal/render/modes/burndownPerson.go`: skip people with no activity
(logging unless `--quiet`), and only error if _every_ person is empty.
`CGO_ENABLED=0 go test ./internal/render/...` passes. Result: 18 of 25 groups
render, 7 skipped cleanly.

**Left uncommitted for review.** Worth a regression test with a deliberately
idle person in the fixture.

---

## Suggested order

0. ~~**B1.**~~ ~~**B1b.**~~ Done — merge-resolution deltas are delivered and merge-commit
   deletions are accounted for; both covered by tests. Line totals now track the
   `082bf15` baseline within 0.5 %. They must be read together: B1 alone overshoots,
   B1b alone would deepen the undercount.
1. ~~**B2's demotion.**~~ Done — negative balances warn once instead of aborting, behind
   `--strict-burndown-balances`. Every burndown-scope failure in the corpus now completes.
   **B2's actual decision — (a) fix the accounting or (b) declare the negatives legitimate —
   is still open and still needs the user.** Note that whatever is chosen, the old binary's
   clamped output is _not_ a correctness baseline to compare against.
2. **B4's `--only` fix.** Unblocks all combined charts and is likely small. This is now the
   only thing left that fails a whole run outright, apart from B3's opt-in flags.
3. **B3.** Two suspected causes, one already confirmed wrong-and-fixed-nowhere.
   Re-measured after B1/B1b: unchanged, so the line-history fixes did not touch it.
4. **B8 commit** + regression test.
5. **B1d.** Nothing fails on it and totals are now right, but merged lines are still
   credited to the merge author instead of their real one — so it must be settled before
   anyone trusts `--burndown-people`, `--devs` or ownership output, and it may well be
   feeding B2.
6. **B5, B6, B7.** Lower frequency; B6 may be documentation only.

## B9 — YAML burndown serialization panics on people with no interactions 🟡

Found while writing B2's tests, not yet filed as a run failure. `writeBurndownPeople`
(`leaves/burndown.go:1257`) prints `result.PeopleMatrix` unconditionally, and
`yaml.PrintMatrix` indexes `matrix[len(matrix)-1]` — so a result with `PeopleHistories` but a
nil `PeopleMatrix` panics with `index out of range [-1]`. `finalizePeopleMatrix` returns nil
whenever `analyser.matrix` is empty, which is reachable with `--burndown-people` on a history
that records no author interactions at all. The `--pb` path is unaffected. Trivial guard;
worth doing next time this file is open.

## Regression coverage worth adding

None of B1–B7 were caught by the existing suite, and all of them reproduce on
ordinary repositories. The gap is that the tests use synthetic fixtures with
clean histories. Suggested: an opt-in corpus test
(`HERCULES_CORPUS_DIR=/path/to/repos go test ./test/corpus`) that runs the full
analysis flag set over a handful of real repositories with a `--people-dict`,
asserts exit 0, and asserts no negative values in any serialized matrix. The
13 failing repositories named above are a ready-made fixture list.

## Verification

The downstream pipeline is the acceptance test. From `MeKo/ewws-statistics`:

```bash
just tools-check                                   # confirm the 0.2.0 binaries resolve
./ewws-stats analyze hercules --include-file .core-repos
# today: Processed: 22, Failed: 14
# target: Processed: 38, Failed: 0
./ewws-stats combine --include-file .core-repos --include-data-exports
```

Then re-enable `burndown_files`, `ownership_analyses` and `refactoring_proxy` in
`ewws-stats.yaml` and confirm the run still completes — those three switches
exist only because of B2/B3/B4 and should be deleted, not left as permanent
configuration.
