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

## B1 — `--burndown-people` corrupts the _project_ burndown matrix 🔴

**The main regression.** Enabling people tracking makes the global history go
negative. The project matrix is fine without it.

```
new b330adf  --burndown                                    → OK
new b330adf  --burndown --burndown-people                  → negative burndown balance:
                                                              project "project" became -1
                                                              at tick 450 (row 15), age band 30 (column 1)
                                                              during finalization
old 082bf15  --burndown                                    → OK
old 082bf15  --burndown --burndown-people                  → OK
```

Exact repro (≈20 s):

```bash
hercules --burndown --granularity=30 --sampling=30 --burndown-people \
         --progress=none /mnt/projekte/Code/MeKo/ewws-wiki > /dev/null
```

`ewws-wiki` is the smallest reproducer found (`-1`, so it is a single
off-by-one leak, not a structural error — good for bisecting). Larger ones:
`process-manager` (`-118`), `meko-etl-tool` (`-32`), `mekorp-backend` (`-64`),
`render-pdf` (`-5558`).

**Where to look.** People tracking must be double-subtracting from, or otherwise
writing through to, `GlobalHistory`. Start at `leaves/burndown.go` where the
per-person deltas are applied, and at the `updateFileDelete` / merge paths — the
error surfaces in `validateBurndownResultBalances` during _finalization_, so the
corruption happens while accumulating, not while serialising.

**Affected in the corpus:** 6 repositories fail at `project` scope
(`backend-for-microscope`, `ewws-wiki`, `meko-etl-tool`, `mekorp-backend`,
`personio-ipoffice-sync`, `process-manager`, `render-pdf`).

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
a single `-1`.** Even under (a), demote it to a warning behind a
`--strict-burndown-balances` flag while the accounting is being fixed. That
single change would take the corpus from 14 failures to 1.

**Affected in the corpus:** 6 repositories fail at `person` scope
(`ShelfStockScanner` −947 "Tobias", `backend-for-mekorp`, `meko-dms-to-yuuvis`,
`mekorp-personio-sync` −110 "Claus", `mekorp-tablet-client`, `ms-graph-to-mekorp`).

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

1. **B2's decision, then the demotion.** Turning `validateBurndownResultBalances`
   into a warning (or clamping) is a few lines and takes the corpus from 14
   failures to 1. Everything else is easier to measure once runs complete.
2. **B1.** Isolated, tiny reproducer (`ewws-wiki`, `-1`), clear before/after
   against `082bf15` — the most tractable real bug here.
3. **B4's `--only` fix.** Unblocks all combined charts and is likely small.
4. **B3.** Two suspected causes, one already confirmed wrong-and-fixed-nowhere.
5. **B8 commit** + regression test.
6. **B5, B6, B7.** Lower frequency; B6 may be documentation only.

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
