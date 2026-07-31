# PLAN: what is still wrong in the burndown accounting

**Status:** one open item — **B1c**, residual negative burndown cells from the same lines
being removed twice across sibling branches. Everything else filed in the original 0.2.0
regression sweep (B1, B1b, B1d, B2–B12, plus the corpus regression suite) is fixed and
covered by tests; see the appendix for the one-line record.

B1c is split into phases below: **P0–P3 are done and merged**, **P4 is a confirmed defect with
no shippable fix, its repro landed as a skipped test**, **P5 is the actual remaining residue —
scoped, its premise measured and confirmed, with a recommendation not to build it on price**.

**Filed:** 2026-07-30 against `b330adf` (tag `0.2.0`). **Corpus:** `/mnt/projekte/Code/MeKo/*`,
the repositories listed in `ewws-statistics/.core-repos`.

Two facts that keep being re-derived, so they are stated once here:

- **`082bf15` is not a correctness baseline.** Until `3e31507` both serializers clamped
  negatives away (`yaml.PrintMatrix(..., fixNegative=true)`, and `if v < 0 { v = 0 }` in
  `pb.ToBurndownSparseMatrix`). Rebuilt with the clamp off, the old binary's project matrix
  is already negative — `ewws-wiki` 22 cells / min −227, `render-pdf` 53 cells / min −6912.
  0.2.0 did not start producing negatives, it started reporting them.
- **Negatives no longer abort a run.** They warn once; `--strict-burndown-balances` restores
  the hard failure. So B1c is a numbers-are-wrong item, not an availability item.

---

## B1c — the same lines removed twice across sibling branches 🔴

### The defect

Two sibling branches each delete the same lines from their own copy of a file. All five
leaves that consume `DependencyLineHistory` fork with `core.ForkSamePipelineItem`, so both
removals land in **one shared accumulator**, while the branches feeding it do not share
state. The merge sees both parents with the lines already gone and has nothing to re-add.
The negative is permanent — it is residual, not transient, and it rides through every later
sampled row as a constant offset because burndown rows are cumulative.

Traced change by change on `render-pdf` (before the comparator fix, which is why this
particular instance is gone):

```
TRACE 0090e98b file=395 prev=304 curr=304 delta= 7413
TRACE bc3aea9f file=395 prev=304 curr=416 delta=-7396   <- sibling of 52ca72b7
TRACE 52ca72b7 file=395 prev=304 curr=416 delta=-7388   <- sibling of bc3aea9f
TRACE bc3aea9f file=510 prev=416 curr=416 delta= 7412
```

Band 304 receives +7413 −7396 −7388 = **−7371** and stays there.

Note the second, milder half visible in the same trace: the lines are re-credited to band
416 instead of keeping band 304, so the _positive_ bands are wrong too. Downstream never
sees that, because the protobuf serializer clamps only negatives.

### Current magnitude

From `test/corpus/baseline.json` (commit `b97f549`, flags `--burndown --burndown-people
--devs --couples --bus-factor --ownership-concentration --hotspot-risk --skip-blacklist
--granularity=30 --sampling=30`). "Residual" = cells whose age band is still negative in the
final sampled row.

| repository                                                             | negative cells | residual |
| ---------------------------------------------------------------------- | -------------: | -------: |
| `mekorp-backend`                                                       |          1 748 |       67 |
| `mekorp-webclient`                                                     |          1 447 |       45 |
| `meko-etl-tool`                                                        |            553 |       12 |
| `backend-for-microscope`                                               |            137 |        7 |
| `ewws-wiki`                                                            |             83 |        3 |
| `process-manager`                                                      |            114 |        6 |
| `personio-ipoffice-sync`                                               |             34 |        2 |
| `ShelfStockScanner`                                                    |              2 |        1 |
| `MKTools`, `ewws-render`, `ewws-tailscale`, `go-clients`, `render-pdf` |              0 |        0 |

`mekorp-backend` carries the overwhelming share of the mass and is the case to drive from.
These counts exclude `people_interaction:` — a signed interaction matrix whose negatives are
correct by construction; counting it inflated every figure in the older tables by roughly an
order of magnitude. **Older tables in git history are not comparable to these.**

### Phase overview

| phase  | what                                                                                    | state                                                                           |
| ------ | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| **P0** | trustworthy measurement — corpus suite, determinism, truncation detection               | ✅ done                                                                         |
| **P1** | merge-resolution deltas reach the accumulator at all (B1, B1b, B1d)                     | ✅ done                                                                         |
| **P2** | rename identity — exact renames must not become delete+create                           | ✅ done                                                                         |
| **P3** | **file identity across a merge** — `adoptMergeCreatedFileIds`                           | ✅ merged (PR #6), gates PASS 13/13, −44 % negative file cells                  |
| **P4** | **merge buffer hand-over** — `synchronizeLineHistoryBranch` drops a branch's own deltas | 🟡 defect confirmed; repro landed **skipped**, fix measured and **rejected**    |
| **P5** | **line identity across the fork** — the project-matrix residue itself                   | 🟠 scoped, premise **measured and confirmed**; **recommendation: do not start** |
| **P6** | downstream acceptance in `ewws-statistics`                                              | ✅ **passed** — 36/36, 0 failed, all three workaround switches re-enabled        |

P3 and P4 were found by tracing the shared accumulator; they are _prerequisites_ that PLAN.md
previously flagged as blocking (see "Traps"), not closures of B1c. **Only P5 can close B1c**:
`updateGlobal` calls `globalHistory.updateDelta(PrevTick, CurrTick, Delta)` with **no
`FileId`**, so no amount of file-identity work can reach the project or people matrices. P3
repairs per-file histories (`--burndown-files`), which is one of the three analyses disabled
downstream as a workaround.

---

### P0–P2 — done (what was fixed around B1c, and did not close it)

- **B1 / B1b** — merge-resolution deltas were discarded and merge-commit deletions emitted
  nothing. Both fixed; line totals now track the `082bf15` baseline within 0.5 %. These two
  must be read together: B1 alone overshoots, B1b alone deepens the undercount.
- **The rename half of B1c** — `sortableChange.Less` was not a valid ordering (it returned
  `true` on the first smaller byte without checking whether an earlier byte was already
  greater), so for almost every pair of real blob hashes both `a.Less(b)` and `b.Less(a)`
  held. `matchExactRenames` walks `added`/`deleted` as a single merge scan, so inconsistent
  orders turned verbatim moves (git's R100) into a delete plus a create. Fixed; `render-pdf`
  lost 99.8 % of its negative mass and its worst cell went −7386 → −58. The same bug is in
  upstream, which is why B1c reproduces on `082bf15`.
- **B1d** — the merge commit was consumed by only one branch. Fixed, and it does **not**
  absorb the residue: cell counts fell in three repositories but negative mass _rose_ in two.
  That is B1d working, not regressing — moving merged lines onto their real author relocates
  later removals into bands where the matching positive was never inserted, which makes the
  double-removal more visible.
- **B12** — rename matching raced two non-equivalent greedy matchers. Fixed, so burndown
  output is now reproducible run to run and every figure above can be believed. Before this,
  single-run measurements were not trustworthy.

---

### P3 — file identity across a merge 🟡

**The defect.** When a merge commit brings in a path the merging branch never held, `newFile`
runs with `analyser.tick == TreeMergeMark`, `abandonedFileID` does not match, and a **fresh
`FileId` is minted**. Because `File.updateTime` suppresses every updater under
`TreeMergeMark`, **no insertion is ever emitted for that id** — while the branch that created
the path already emitted one under _its_ id. `matchingMergeFiles` then pairs the two by path,
synchronization discards the other analyser's file, and the accounting is split across two
keys: one holding lines nobody will ever remove, one that will receive a removal against an
insertion that was never sent. This is exactly the "file identity is unresolved one level up"
trap recorded below, now traced to its line.

**The fix (candidate).** `adoptMergeCreatedFileIds` — record merge-minted files in
`mergeCreatedFiles`, and before the `file.Merge` loop resolves the marks, re-key each onto the
id the creating branch used (matching by path _and_ equal length, lowest id wins for
determinism). Two parts, both load-bearing:

- re-key before marks resolve, so the resolution deltas land on the surviving id;
- **do not** register the vacated id as an abandoned name. `abandonedFileID` prefers the
  _highest_ matching id, so leaving it behind lets a later merge resurrect a key that now
  holds no accounting at all and mix its bands into the adopted file. (This is the difference
  between candidate v1 and v2; v1 measurably regressed further.)

**Measured.** Fires **227×** on `meko-etl-tool` with 0 non-matches. Per-file burndown
(`--burndown-files`, both runs verified untruncated):

|             | negative cells |        mass | worst cell |
| ----------- | -------------: | ----------: | ---------: |
| HEAD        |         12 820 |   1 408 483 |     −2 652 |
| adoption v2 |      **3 939** | **149 829** |   **−384** |

Attribution is clean — the P4 hand-over alone leaves it at 12 820.

**The apparent corpus regression was a measurement artifact — resolved.** At corpus flags
(default 1 000 ms diff timeout) `mekorp-webclient` reported 1 447 → 1 481 negative cells,
which stalled this phase. Re-measured with `--diff-timeout=300000`, both runs verified free of
the truncation warning, the two binaries produce **identical negativity**:

|             | negative cells |      mass | worst cell |
| ----------- | -------------: | --------: | ---------: |
| HEAD        |          1 413 | 5 104 060 |    −77 421 |
| adoption v2 |          1 413 | 5 104 060 |    −77 421 |

Per matrix (31 of them: `project` + 30 people) the negative statistics are equal
**cell for cell**; `project` alone is 257 / 2 130 531 / −62 686 in both. The only difference
anywhere in the burndown output is **41 cells in `project`, each off by exactly +1, all in
positive bands** (e.g. `3986 → 3987`) — one line's attribution, no negative cell touched.

That is precisely what the mechanism predicts: `updateGlobal` carries no `FileId`, so
file-identity work cannot move the project or people matrices. **P3 neither helps nor harms
the gated metric.** The 34 "new" cells never existed — they were truncation noise, and so is
the gap between the untruncated 1 413 and `baseline.json`'s 1 447.

**Gate result against the re-seeded, truncation-free baseline: PASS, 13/13, no regression in
any repository or dimension.** The project dimension is **identical in all 13 repositories** —
the predicted no-op, now demonstrated rather than argued. The per-file dimension:

| repository                            | files base |   files P3 |           Δ | residual base | residual P3 |
| ------------------------------------- | ---------: | ---------: | ----------: | ------------: | ----------: |
| `meko-etl-tool`                       |     12 267 |      3 386 |      −8 881 |           263 |          73 |
| `mekorp-webclient`                    |     10 047 |      8 786 |      −1 261 |           191 |         172 |
| `mekorp-backend`                      |      3 023 |      2 117 |        −906 |            73 |          53 |
| `render-pdf`                          |        393 |         63 |        −330 |            19 |           2 |
| `process-manager`                     |        127 |      **0** |        −127 |             8 |       **0** |
| `backend-for-microscope`              |        265 |        174 |         −91 |            14 |          10 |
| `ShelfStockScanner`                   |         95 |          4 |         −91 |             5 |           1 |
| `MKTools`                             |          1 |      **0** |          −1 |             1 |       **0** |
| `go-clients`, `ewws-render`, 3 others |  unchanged |            |          ±0 |               |             |
| **total**                             | **26 718** | **15 030** | **−11 688** |       **590** |     **327** |

**−44 % of all negative file cells and −45 % of the residual ones**, two repositories cleared
outright, none regressed.

**Open work:**

- [x] Locate the 34 regressing cells — they were an artifact of the default diff timeout.
- [x] **Pin `--diff-timeout` and re-seed.** Done in `wt-corpus`: `--diff-timeout=300000` in
      both flag sets, and truncation is now _loud_ — the suite matches `not reproducible` on
      the binary's stderr (hercules exits 0 when it truncates) and fails the repository as
      `MEASUREMENT INVALID`, refusing to parse, gate or record the numbers. Re-seed took
      20.5 min, 13/13, zero truncation. Only two repositories moved, both **improving**:
      `backend-for-microscope` 137 → 100, `mekorp-webclient` 1 447 → 1 413. The other eleven
      are bit-identical — so the old baseline overstated negativity purely through
      machine-speed-dependent truncation, on exactly the two repositories that were truncating.
      Also fixed on the way: `readBaseline` rejected a flag-set mismatch even in update mode,
      which made re-seeding after a deliberate flag change impossible without deleting the file
      by hand.
- [x] Unit test for `adoptMergeCreatedFileIds`. `TestLinesMergeAdoptsCreatingBranchFileId`
      (structural invariant) and `TestLinesMergeAdoptedFileIdKeepsRemovalPaired` (the
      accounting consequence) in `internal/linehistory/line_history_merge_test.go`. Both were
      verified to fail with the fix reverted and pass with it applied. The second one prints
      the defect directly: without adoption the path's deltas are `map[1:10 2:-10]` — the
      insertion on the creating branch's id, the removal on the merge-minted id. Note that the
      _aggregate_ net is zero either way, so only the per-`FileId` breakdown discriminates;
      an aggregate assertion would have been a false negative.
- [x] Extend the corpus to gate `--burndown-files`. Done in `wt-corpus` as a **separate
      dimension** (`file_flags` / `file_repositories` beside the existing keys), measured by a
      second run per repository so the existing project/people numbers stay produced by the
      identical command. This immediately exposed something the suite was blind to: repos with
      a clean project matrix are **not** clean per file — `go-clients` 0 → 479, `render-pdf`
      0 → 393, `ewws-render` 0 → 21. `render-pdf`'s celebrated 0/0 after the rename fix holds
      only at project scope. Corpus-wide the file dimension carries ~26 700 negative cells
      against ~4 100 at project scope, so **most of the accounting damage was never measured**.
- [x] Merge `wt-adopt` (fix + 2 tests) and `wt-corpus` (suite + re-seeded `baseline.json`).
      Landed together in PR #6 (`bfc9445` adoption, `abc217c` corpus suite + timeout pin,
      `8fb2305` the review fix below), merged as `41bced3`. The new baseline is _not_
      comparable to anything older than `abc217c`.
- [x] **Re-seeded on a clean tree.** The committed baseline recorded `77c736b` plus a
      six-file dirty working tree, so its provenance named no reproducible state. Re-seeded at
      `41bced3`, `"working_tree": "clean"`: 13/13 PASS in 56 min, zero truncation. Twelve
      repositories are **bit-identical** in both dimensions; the one move is `meko-etl-tool` per
      file, 3 386 → 3 342 cells and 73 → 72 residual. That is not drift — the run reproduces
      exactly on re-measurement, and measuring `5bfca9f` (the commit before the review fix)
      against the same baseline gives 3 386, so the −44 is `8fb2305` and it is an improvement.
- [x] Review follow-up: adoption could take an id that is **still live at another path**, because
      a rename carries the `FileId` with it — a branch that renamed the path away holds that id
      under the new name while the sibling still holds it under the old one. Adopting anyway put
      two live files on one key. Fixed in `8fb2305` (`adoptableFileId` consults a `liveFileIds`
      map), pinned by `TestLinesMergeSkipsAdoptionOfIdLiveAtAnotherPath`.

---

### P4 — the merge buffer hand-over 🟡

**The defect, confirmed.** `synchronizeLineHistoryBranch` clears `target.pendingChanges`. For
a synchronized _sibling_ that is right — the merging branch owns the resolution deltas. But a
target that performed a merge **of its own** and is synchronized before consuming another
ordinary commit loses those deltas entirely (a merge never drains `pendingChanges`; only an
ordinary commit does). Previously listed as an unconfirmed thread; now pinned by
`TestLinesMergeSiblingBufferSurvivesSynchronize`, which fails at HEAD with
`expected 10, actual 0`. The trigger shape — a merge commit whose parent is a merge commit —
occurs **1 097×** in `mekorp-webclient`, 40× in `mekorp-backend`, 11× in
`backend-for-microscope`, 6× in `meko-etl-tool`.

**Why there is no shippable fix yet.** Handing the buffer to the merging analyser
(`adoptBranchPendingChanges`) regresses `mekorp-webclient`. Diagnosed: `foldMergeRemovals`
writes into `analyser.changes`, which `Merge` folds into `pendingChanges`, so inner-merge
**deletions** travel in the buffer and get re-emitted — recreating B1c's own double removal.
Deduplicating buffered removals against the merge's own removal set helps but does not close
it, under either keying (by `FileId`, or by path via `NameOf` — ids differ across branches, so
path is the correct key).

**Open work:**

- [x] **Re-measured cleanly, and the regression is real** — unlike P3's, it did not evaporate
      with the timeout fixed. `mekorp-webclient`, `--diff-timeout=300000`, no truncation
      warning on either run:

      | | negative cells | mass | worst | residual |
      | --- | ---: | ---: | ---: | ---: |
      | HEAD | 1 413 | 5 104 060 | −77 421 | 43 |
      | hand-over | 1 433 | 5 121 627 | −77 422 | **44** |

      The +20 cells sit in three matrices — `project` +1, one person +2, one person +17 — and
      one of them is *residual*, so this is not transient noise. The hand-over as written is
      rejected on evidence.

- [x] Keep `TestLinesMergeSiblingBufferSurvivesSynchronize`. Landed on `main` in `b1f438f`,
      **skipped** with the reasoning inline, and re-verified against post-P3 `main`: remove the
      skip and it still fails `expected 10, actual 0`.
- [x] Decide whether P4 is reachable before P5 carries line identity. **It is not.** The
      buffered deltas cannot be delivered while inner-merge _removals_ travel in the same
      buffer, and dedup by path or by `FileId` both leave a residue — the same wall P5 hits,
      because both need to recognise two branches' removal of one line as one removal. P4 is
      therefore a dependent of P5, not an independent phase, and dies with it if B1c is closed
      as won't-fix.

**Closed negative result — do not redo.** Per-_change_ dedup in `foldMergeRemovals` (`seen` is
marked on the first change, not the first branch, so a file spanning several ownership bands
has only its first band booked out) is a genuine loss and was the top-ranked finding of two
independent audits. Correcting it makes everything worse at once: on `meko-etl-tool` the head
total goes 24 009 → 20 162 against a git ground truth of **24 908**, negative cells 553 → 933,
mass 84 464 → 371 449. The dropped bands are load-bearing — they mask B1c's double removal.
Reverted, with the numbers recorded in a comment at the call site.

**P0 note added by this round:** ad-hoc runs silently hit the file-diff time budget, and the
binary says so on stderr (`file diffing hit its time budget and stopped early … not
reproducible`). Two figures reported during this round came from truncated runs and were
withdrawn. **Every ad-hoc run must pass `--diff-timeout=3600` or higher and its `.err` must be
grepped for `time budget` before the numbers are used.** Corpus runs are unaffected.

---

### P5 — line identity across the fork 🔴

Not started. This is what actually closes B1c. Three attempts at reconstructing the correction
at the merge were made and all reverted; read them before proposing a fourth of the same kind.
Expect the answer to be that a fourth is not worth making.

**Attempt 1 — reconcile the shared accumulator against the merged tree.**
Mirror what consumers have been told (per file, per ownership band); at each `Merge()`
compare against the merged tree and emit the difference. Cleared `render-pdf` outright but
regressed `meko-etl-tool` (head count from +11 % to −13 %). Diagnosed to one line:
`liveBandHistogram` returning nothing means **"not mine", not "does not exist"** — the mirror
is global while the tree it is compared against belongs to one branch, so files live on a
third branch were booked out. Fatal to the design, not tunable; four variants were measured
and every one that cleared negatives inflated the head count.

Four things it got right, each measured, and all still true:

1. Correct the _count_, never the _band_. `mergeLineValues` adopts the older side's ownership
   and a later merge can adopt the other side again, so band corrections oscillate — on
   `backend-for-microscope` re-attribution carried 53 505 of mass against a real defect of
   37 444 and drove single cells from −126 to −25 748.
2. Correcting only the restoring direction is also wrong. A double removal both pushes one
   band below zero _and_ credits the same lines to another.
3. A correction must be booked at the sample the faulty delta landed in, not at the merge
   tick — otherwise every row in between stays wrong and it shows up as a step in the surface.
4. The mirror must be updated when changes are **emitted**, not when they are produced.

**Attempt 2 — reconcile from per-branch contributions instead.**
Track `D_i` (each branch's deltas per file and band since the fork), snapshot `H_pre` (branch
0's band histogram) before the `file.Merge()` loop and `H_m` after; correct by
`(H_m − H_pre) − Σ_{i≥1} D_i`. No global state, so a file live on a branch outside the merge
is untouched — the exact failure of attempt 1. Two implementation facts worth keeping:
accounting has to be **per fork, not per branch object** (a stack with one frame per fork —
the fork origin keeps its branch slot and otherwise offers an enclosing merge deltas from
before the fork), and `H_pre` has to be snapshotted in `Consume()`, not `Merge()`, because
consuming a merge commit overwrites the ownership of every line it touches with
`TreeMergeMark`.

It was killed by instrumenting the derivation against a mirror of what was actually emitted:
`H_pre + Σ D_i` matched the real accumulator in **29 % of corrections** (350 corrections on
`backend-for-microscope`, total drift 69 060; 53 negative corrections drove the true
accumulator 29 339 lines below zero). The drift comes from `mergeLineValues`, which silently
re-bands tree lines without emitting a delta:

```
file 98, band (author 7, tick 325): derived 27894, really accumulated 0      -> books -27894
file 98, band (author 3, tick 520): derived     0, really accumulated 25717  -> books +25717
```

Same lines, two bands — `backend-for-microscope`'s −25 749 cell. **The tree is not a record
of what was accumulated, so no snapshot of it is a valid correction target.**

**Attempt 3 — emit the delta in `mergeLineValues`, where the ownership actually changes.**
The targeted defect is real and the unit test pins it
(`TestFileMergeDisplacedOwnershipIsReported` fails without the emission). The corpus says no.
One-sided (book out the destination's claim when it loses to an older version), full 13-repo
gate: **3 regress, 10 unchanged, none improve** — `mekorp-backend` 1 748 → 1 877,
`mekorp-webclient` 1 447 → **1 724**, `meko-etl-tool` 553 → 606. The symmetric variant (book
out whichever side loses) is strictly worse and costs `render-pdf` its clean 0 / 0.

Why: `mergeLineValues` pairs the two files **by position**. Lines at the same index are not
the same line — they are two files of equal length laid side by side. "Both branches
introduced this line" is an inference the function is not entitled to make, and the symmetric
version makes twice as many such claims and is twice as wrong, which is the confirmation.

**Conclusion.** Attempt 2 hit the wall from the accumulator side and attempt 3 from the tree
side: **neither the merged contents nor the merge itself can supply the correction.** Closing
B1c means carrying line identity across the fork rather than reconstructing it at the merge —
a change to what `File` stores, not to how a merge is accounted. That is substantially larger
than any of the three attempts and **should not be started without first deciding B1c is
worth it.**

#### What "carrying line identity" would cost — scoped, not built

`File` stores no lines. It is `{tree *rbtree.RBTree; updaters []Updater; Id FileId}`
(`file.go:29-33`) over a tree of **run boundaries**: `rbtree.Item{Key, Value uint32}`
(`rbtree.go:38-41`), `Key` = the run's first line index, `Value` = the packed owner (tick in the
low 14 bits, author above — `line_history.go:926-937`). A 1 000-line file with 20 ownership runs
costs 21 nodes of 24 B, not 1 000. `Value` has **no spare bit**: 14 tick + 18 author, with three
author values and one tick value already reserved. The only output is
`updateTime(curr, prev, delta)` (`file.go:554-571`) — a count, with no position and no identity.

Three mechanisms were considered against that.

**(a) A stable per-line id minted at insertion.** The only one that is not an inference. Mint from
the counter that already survives `Fork` by pointer (`line_history.go:57`, `525-527`), shift ids
with the tree, and fold two branches' removals on `(FileId, lineId)`. It is the only option that
also repairs the mis-credited _positive_ bands, because with real ids `mergeLineValues` could emit
an honest re-band delta instead of silently overwriting (`file.go:411-436`).
The price is structural: ids are unique per line, so **runs stop compressing** — the hottest
structure in the program goes from O(runs) to O(lines) nodes, 20–50× on typical source, ×24-32 B,
**multiplied by every live branch** because `Allocator.Clone` copies storage eagerly
(`rbtree.go:91`). It also needs a seventh hibernation buffer and a format-version bump
(`rbtree.go:190-380`), and it invalidates `mergeLineValues`' index pairing and length-equality
panic (`file.go:417-420`) — with ids a merge is a set union and the parents need not be equal
length. Keep the ids **internal** and fold buffered removals at `Merge` (as `foldMergeRemovals`
already folds by `FileId`, `line_history.go:617-633`); putting the id into
`core.LineHistoryChange` instead would break the coalescing at `line_history.go:1060-1069`, the
`linedump` text format and all of `line_loader.go`, for no gain. Deferred folding is legal because
burndown accumulates order-independently (`burndown.go:989-991` into a sparse map, grouped only at
`Finalize`) — the same property `pendingChanges` already relies on.

**(b) A per-`(fork, file, band)` removal budget.** Cheap, no `File` change — and it cannot express
"the same line", only "the siblings between them removed more than the band held". That makes it a
clamp, and clamps walk straight into the Traps below: suppressing a removal raises the head total.
Every variant already measured and rejected has this shape.

**(c) Per-branch accumulators — abandon `ForkSamePipelineItem` for the five leaves.** Costs
memory ×branches on `globalHistory`/`peopleHistories`/`matrix`/`fileHistories`, revives B11's
hibernation hazard, needs a real `Fork`/`Merge` on all five leaves (`MergeResults` is a different
operation and cannot be reused — sibling histories share a tick axis and must not be rebased), and
then **inherits attempt 2 wholesale**: at the merge you still have to decide which sibling's deltas
are real, and without line identity the only thing left to compare against is the merged tree.
Same wall, more code.

#### The premise, now measured — the double removal is real and is the right size

Everything above rested on an unverified premise: that the residue _is_ the double removal. Measured
2026-07-31 with throwaway instrumentation (mirror `map[FileId]map[PrevTick]int64` of the running line
count, hooked into `updateGlobal` at `burndown.go:989` and reported at `Finalize`; it records the
part of each removal the band could not cover, then clamps at zero so one over-removal does not
cascade). The instrumented binary reproduces `baseline.json` cell for cell, so it is not perturbing
what it measures. Corpus flags, `--diff-timeout=300000`, neither run truncated.

| repository       | over-removed (file, band) pairs | events | **excess mass** | project residual mass | all-matrix residual mass |
| ---------------- | ------------------------------: | -----: | --------------: | --------------------: | -----------------------: |
| `mekorp-backend` |                             624 |  2 739 |     **123 611** |               107 392 |                  217 766 |
| `meko-etl-tool`  |                             283 |  1 176 |       **3 937** |                   984 |                    2 269 |

Compare against the **residual** mass — the final sampled row — not the total negative mass, because
burndown rows are cumulative and one over-removal shows up in every later row. On the case to drive
from, over-removal accounts for **115 % of the project residue**; on the cross-check, 4×. Excess
exceeding the residue is exactly what the mechanism predicts: some over-removed lines are later
re-inserted and the cell recovers, so the excess is an upper bound on what survives to the last row.
Note also that the people matrices' residual mass (110 374) tracks the project's (107 392), which is
the same residue counted per author rather than a second, independent one.

**Verdict: P5 is aimed at the right mechanism.** The caveat worth stating: the mirror detects
over-removal of _any_ origin, not specifically two siblings doing it. But over-removal at this scale,
in an accumulator that is shared across branches by `ForkSamePipelineItem` and reconciled by nothing
(`burndown.go:308-318`), is the signature that mechanism predicts, and no other candidate has been
proposed that would produce it.

**Recommendation: do not start B1c anyway.** The premise holds, so the recommendation is now about
price rather than aim. Only (a) is mechanically sound, and its honest price is a
20–50× blowup of the hottest structure in the program times the branch fan-out, plus a hibernation
format change. What is left after the rename fix and P3 is **327 residual file cells and ~4 100
project cells corpus-wide**, concentrated in one repository, on a metric that now warns rather than
fails. That is not a trade worth making on the evidence in hand.

**What would settle it, in order** — nothing below should be skipped if B1c is reopened:

1. ~~Is the double removal actually the residue?~~ **Done, 2026-07-31 — yes.** See the table above.
2. **Is the price real?** Prototype (a) as _ids only, no behaviour change_ — mint, store, shift,
   assert invariance — and measure peak RSS and wall clock on `mekorp-webclient` (1 097
   merge-of-merge commits) and `mekorp-backend`. Inside ~2× current, (a) becomes arguable.
   **This is now the deciding measurement**, and it is the one that can still kill (a) outright.
3. **Gate on both metrics, on all 13 repositories.** `negative_cells` _and_ `residual_cells`, plus
   the project matrix's final-row sum against `git grep -I -c '' HEAD`. Negatives and head totals
   trade against each other; every variant so far that fixed one broke the other.

The instrumentation is not kept in the tree — it is ~110 lines and re-deriving it from this
description is cheaper than carrying a debug hook the product does not otherwise have (`os.Getenv`
appears nowhere in `leaves/` or `internal/linehistory/`, and `core.Logger` has no Debug level).

Note also that `TestFileMergeShallow` (`file_test.go:639`) and `TestFileMergeDeep` (`:678`) assert
`status[6] == 10` (lines `674` and `713`) for a band the merged dump holds no line of — confirmed.
Those assertions encode
the `mergeLineValues` re-banding defect and would have to change under (a).

### Traps for whoever picks this up

- **File identity is unresolved one level up.** B1d made `matchingMergeFiles` pair files by
  _path_ across differing `FileId`s, while all accounting keys on `FileId`. Merged-in content
  is therefore counted under both ids. Any reconciliation has to resolve identity first.
  **Traced, fixed and merged — see P3.**
- **Judge on the corpus suite, never on an ad-hoc repository set.** The five-repository set
  used throughout the older measurements (`render-pdf`, `personio-ipoffice-sync`,
  `backend-for-microscope`, `meko-etl-tool`, `mekorp-backend`) misses `mekorp-webclient`,
  which was the single largest regression attempt 3 caused.
- **Negatives and head totals trade against each other.** Every variant measured across all
  three attempts that cleared negatives inflated the project matrix's final-row sum relative
  to `git grep -I -c '' HEAD`, and vice versa. Both halves are mis-attributions of the same
  kind, so a change that only moves one of them has not been validated.
- The old binary's clamped output is not something to converge towards.

### Preserved code

Nothing was merged. All three attempts live only in the session scratchpad:

- `scratchpad/b1c-patch/` — attempt 1 (`merge_reconcile.go` + `line_history.diff`).
- `scratchpad/b1c-attempt2/` — attempt 2 (`merge_reconcile.go` + a diff against `2cb415d`
  for `line_history.go` and `file.go`); variants behind `HCK_RECONCILE`, `HCK_RESTORE_ONLY`,
  `HCK_TRACE`.
- `scratchpad/b1c-mergelinevalues/symmetric.diff` — attempt 3, symmetric version, which
  contains the one-sided version as its first half plus both unit tests.
  P3 and P4's test have since landed on `main` and are no longer scratchpad-only:

- **P3** shipped as `internal/linehistory/merge_adoption.go` (PR #6, merged `41bced3`).
- **P4's repro** shipped skipped in `internal/linehistory/line_history_merge_test.go` (`b1f438f`).
  Its rejected fix — `adoptBranchPendingChanges` plus the path-keyed `foldMergeRemovals` dedup —
  was **not** kept; the measurements that killed it are recorded under P4 above.

Worktree builds need `GOFLAGS=-buildvcs=false` **and** `CGO_ENABLED=0` (the render packages
pull in freetype via matplotlib-go and fail on a missing `ft2build.h`).

---

## Smaller open threads

Each is independent of B1c's main line and none currently fails anything.

- **`TestFileMergeShallow` / `TestFileMergeDeep` assert `status[6] == 10`** for a band the
  merged file holds no lines of. That is the `mergeLineValues` re-banding defect seen from
  the test side, and it is worth knowing about independently of any fix — the assertions
  encode the current behaviour, not the correct one.
- **`RefactoringProxy` discards the raw rename count**, storing only
  `RenameRatios[i] = Renames/TotalChanges`. Merging works exactly anyway (the count-weighted
  mean keeps `r*t` in `float64`; residual error ~1e-16 against the ~1e-7 the existing
  `float32` narrowing already costs), so adding a `Renames []int` field to the result and
  `pb.proto` would buy nothing measurable. **Not done on purpose** — recorded so it is not
  re-discovered as an oversight.

---

## How to judge a change: the corpus regression suite

`just test-corpus`, gated on `HERCULES_CORPUS_DIR` (`/mnt/projekte/Code/MeKo`), skipping
cleanly when unset so `go test ./...` stays green. 13 repositories, ~7 min to seed.

- **It asserts no regression against `test/corpus/baseline.json`, not the absence of
  negatives** — the latter cannot pass while B1c's residue exists, so as written that suite
  would have been red from birth and ignored.
- Per repository it records exit code, negative cells, negative mass, worst cell and residual
  cells. **Only `exit_code`, `negative_cells` and `residual_cells` gate the build**; mass and
  worst cell are reported in both directions but ungated, because the wall-clock diff timeout
  can still truncate a run (the binary now warns when it does). Their older run-to-run drift
  was B12 and is fixed, so gating them has become defensible.
- **Fixed in `abc217c`: the suite now pins `--diff-timeout=300000`** and treats truncation as
  fatal — it matches `not reproducible` on the binary's stderr (hercules exits 0 when it
  truncates) and fails the repository as `MEASUREMENT INVALID`, refusing to parse, gate or record
  the numbers. Before that the suite ran at the 1 000 ms default (the flag is in **milliseconds** —
  `internal/plumbing/diff.go`) and `negative_cells` was gated while depending on machine speed:
  `mekorp-webclient` measured 1 447 truncated and **1 413** untruncated, and two binaries with
  identical negativity reported 1 447 vs 1 481. **Ad-hoc runs are still on their own** — pass a
  large timeout and grep stderr for `time budget` before believing any number.
- It reads YAML, not `.pb`, because `pb.ToBurndownSparseMatrix` clamps negatives away.
- `people_interaction` is excluded — see the note under "Current magnitude".
- `UPDATE_CORPUS_BASELINE=1` / `just update-corpus-baseline [REPOS]` re-seeds, preserving
  entries not re-measured. **Re-seed only after a change is understood, never to make a red
  run green.** The file records the commit and working-tree state it was measured against;
  seeding against a dirty tree has already invalidated one baseline.
- Building inside a `git worktree` needs `GOFLAGS=-buildvcs=false`, otherwise the build fails
  with `error obtaining VCS status: exit status 128`.

## P6 — downstream acceptance ✅

The real acceptance test is the pipeline this sweep was filed from. Run 2026-07-31 in
`MeKo/ewws-statistics` (clean on `main` at `2dd063a`) against hercules `e347ebc`.

- [x] `just tools-check` — both binaries resolve out of `$HERCULES_DIR`, schema v2 on both sides.
- [x] `./ewws-stats analyze hercules --include-file .core-repos` — **36 processed, 0 failed.**
      The old target of "38" was wrong, not a shortfall: `.core-repos` lists 38, but
      `microscope-tablet-client` is not cloned locally and `ewws-statistics` excludes itself from
      discovery (`--include-only ewws-statistics` → "no repositories found after filtering").
      36/36 of what is discoverable is a clean pass.
- [x] `./ewws-stats combine --include-file .core-repos --include-data-exports` — exit 0.
- [x] Re-enabled all three workaround switches and re-ran both steps: **still 36/36, 0 failed,
      combine exit 0.** B2/B3/B4 are confirmed fixed from the downstream side:
      `ownership_analyses` now produces the bus-factor and ownership-concentration charts, and
      `refactoring_proxy` no longer takes down the combine (`all_project_refactoring_proxy.svg`
      is generated). Eleven `negative burndown balance` warnings appear and are B1c's residue
      warning by design; the two largest match `baseline.json` exactly (`mekorp-backend` 1 748
      cells, `meko-etl-tool` 553), which independently confirms the corpus suite and the
      downstream pipeline measure the same thing.

The switches are now **deleted** rather than left as configuration, in `ewws-statistics` `079b0bc`.
Deleting the keys alone would have re-disabled the analyses — all three defaulted to `false` in
`internal/config/config.go` — so the fields came out end to end and the flags are requested
unconditionally. `burndown_files` survives as a switch, but on cost rather than breakage.

**One caveat found by enabling `burndown_files`, and it is not a 0.2.0 regression.** The
per-repository `.pb` does carry file burndown (305 files render from `meko-etl-tool` alone), but
the **combined** chart directory comes out empty, with labours reporting `Burndown stats for
files were not collected`. Cause: `BurndownAnalysis.MergeResults` merges `GlobalHistory`,
people and repositories, but never `FileHistories` (`leaves/burndown.go`, the `MergeResults`
body) — and **upstream does not either**, so combined file burndown has never existed. Enabling
the switch is safe and useful for per-repository charts; the combined file-burndown chart is
simply not a thing hercules produces. Worth a warning instead of an empty directory, but that is
a downstream ergonomics item, not an accounting defect.

---

## Appendix: fixed, for cross-reference

Full diagnoses are in the git history of this file and in the merged PRs.

| item       | what it was                                                                                                                                                                                                                                                                        | commits                         |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------- |
| B1         | merge-resolution deltas discarded, project matrix corrupted                                                                                                                                                                                                                        | `9e570a3`, `1be72df`            |
| B1b        | file deletion inside a merge commit emitted nothing                                                                                                                                                                                                                                | `1abecda`, `9c010ef`            |
| B1c (half) | `sortableChange.Less` was not a valid ordering, so exact renames became delete+create                                                                                                                                                                                              | —                               |
| B1d        | the merge commit was consumed by only one branch; merged lines now keep their real author                                                                                                                                                                                          | —                               |
| B2         | person matrices have _always_ contained negatives; decided **(a) the accounting is wrong** — no negative cell recovers by the final row, so branch divergence explains none of them. Demoted to a warning behind `--strict-burndown-balances`; the accounting defect itself is B1c | `ebc8ded`, `a7c8cba`, `0ea3cc0` |
| B3         | `OwnershipSnapshot` underflow — a structural consequence of divergent branches sharing one accumulator, not an accounting slip; totals stay signed and only the metric snapshots clamp                                                                                             | `fafb754`, `c04d08c`            |
| B4         | `--only` filtered after the merge; an unmergeable analysis sank the whole combine. `RefactoringProxy` is now mergeable, with tick axes rebased                                                                                                                                     | `2f57e3d`                       |
| B5         | `HotspotRisk` merged to empty because `TopN` is 0 on the combine path                                                                                                                                                                                                              | —                               |
| B6         | `--blob-cache-max-blob-size` was fail-closed; oversized blobs are now recorded as binary and warned about                                                                                                                                                                          | —                               |
| B7         | `--lines-hibernation-disk` serialized an allocator that never hibernated                                                                                                                                                                                                           | —                               |
| B8         | person burndown aborted on contributors with no activity                                                                                                                                                                                                                           | `c04d08c`                       |
| B9         | YAML burndown serialization panicked on nil `PeopleMatrix`; the `--pb` path panicked too                                                                                                                                                                                           | —                               |
| B10        | non-deterministic execution plan; a merge could be resolved against the wrong branch                                                                                                                                                                                               | `5937149`                       |
| B11        | `changeHibernation` hibernated per branch index while `ForkSamePipelineItem` shares one instance, so a fork's idle side put its sibling's analyser to sleep mid-run                                                                                                                | `0dd3211`                       |
| B12        | rename matching raced two non-equivalent greedy matchers and kept whichever finished first                                                                                                                                                                                         | —                               |
| suite      | opt-in corpus regression test, baseline-relative                                                                                                                                                                                                                                   | `b97f549`, `c329229`            |
