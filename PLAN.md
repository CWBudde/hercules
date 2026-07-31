# PLAN: what is still wrong in the burndown accounting

**Status:** one open item — **B1c**, residual negative burndown cells from the same lines
being removed twice across sibling branches. Everything else filed in the original 0.2.0
regression sweep (B1, B1b, B1d, B2–B12, plus the corpus regression suite) is fixed and
covered by tests; see the appendix for the one-line record.

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
416 instead of keeping band 304, so the *positive* bands are wrong too. Downstream never
sees that, because the protobuf serializer clamps only negatives.

### Current magnitude

From `test/corpus/baseline.json` (commit `b97f549`, flags `--burndown --burndown-people
--devs --couples --bus-factor --ownership-concentration --hotspot-risk --skip-blacklist
--granularity=30 --sampling=30`). "Residual" = cells whose age band is still negative in the
final sampled row.

| repository | negative cells | residual |
| --- | ---: | ---: |
| `mekorp-backend` | 1 748 | 67 |
| `mekorp-webclient` | 1 447 | 45 |
| `meko-etl-tool` | 553 | 12 |
| `backend-for-microscope` | 137 | 7 |
| `ewws-wiki` | 83 | 3 |
| `process-manager` | 114 | 6 |
| `personio-ipoffice-sync` | 34 | 2 |
| `ShelfStockScanner` | 2 | 1 |
| `MKTools`, `ewws-render`, `ewws-tailscale`, `go-clients`, `render-pdf` | 0 | 0 |

`mekorp-backend` carries the overwhelming share of the mass and is the case to drive from.
These counts exclude `people_interaction:` — a signed interaction matrix whose negatives are
correct by construction; counting it inflated every figure in the older tables by roughly an
order of magnitude. **Older tables in git history are not comparable to these.**

### What was fixed around it, and did not close it

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
  absorb the residue: cell counts fell in three repositories but negative mass *rose* in two.
  That is B1d working, not regressing — moving merged lines onto their real author relocates
  later removals into bands where the matching positive was never inserted, which makes the
  double-removal more visible.
- **B12** — rename matching raced two non-equivalent greedy matchers. Fixed, so burndown
  output is now reproducible run to run and every figure above can be believed. Before this,
  single-run measurements were not trustworthy.

### Three attempts, all reverted — and why the family is closed

Read this section before starting a fourth attempt. Expect the answer to be that a fourth of
the same kind is not worth making.

**Attempt 1 — reconcile the shared accumulator against the merged tree.**
Mirror what consumers have been told (per file, per ownership band); at each `Merge()`
compare against the merged tree and emit the difference. Cleared `render-pdf` outright but
regressed `meko-etl-tool` (head count from +11 % to −13 %). Diagnosed to one line:
`liveBandHistogram` returning nothing means **"not mine", not "does not exist"** — the mirror
is global while the tree it is compared against belongs to one branch, so files live on a
third branch were booked out. Fatal to the design, not tunable; four variants were measured
and every one that cleared negatives inflated the head count.

Four things it got right, each measured, and all still true:

1. Correct the *count*, never the *band*. `mergeLineValues` adopts the older side's ownership
   and a later merge can adopt the other side again, so band corrections oscillate — on
   `backend-for-microscope` re-attribution carried 53 505 of mass against a real defect of
   37 444 and drove single cells from −126 to −25 748.
2. Correcting only the restoring direction is also wrong. A double removal both pushes one
   band below zero *and* credits the same lines to another.
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

### Traps for whoever picks this up

- **File identity is unresolved one level up.** B1d made `matchingMergeFiles` pair files by
  *path* across differing `FileId`s, while all accounting keys on `FileId`. Merged-in content
  is therefore counted under both ids. Any reconciliation has to resolve identity first.
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

---

## Smaller open threads

Each is independent of B1c's main line and none currently fails anything.

- **`synchronizeLineHistoryBranch` clears `pendingChanges`** (`line_history.go:683`). The
  comment argues the merge branch owns the resolution deltas so siblings must not re-emit
  them, which is right for a synchronized *sibling* — but a target that performed a merge of
  its own and is synchronized before consuming another commit loses those deltas entirely.
  Found while instrumenting attempt 1, never confirmed against a real repository. Worth a
  focused test before deciding whether it is a bug.
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
- It reads YAML, not `.pb`, because `pb.ToBurndownSparseMatrix` clamps negatives away.
- `people_interaction` is excluded — see the note under "Current magnitude".
- `UPDATE_CORPUS_BASELINE=1` / `just update-corpus-baseline [REPOS]` re-seeds, preserving
  entries not re-measured. **Re-seed only after a change is understood, never to make a red
  run green.** The file records the commit and working-tree state it was measured against;
  seeding against a dirty tree has already invalidated one baseline.
- Building inside a `git worktree` needs `GOFLAGS=-buildvcs=false`, otherwise the build fails
  with `error obtaining VCS status: exit status 128`.

## Downstream acceptance

The real acceptance test is the pipeline this sweep was filed from. In `MeKo/ewws-statistics`:

- [ ] `just tools-check` — confirm the binaries resolve
- [ ] `./ewws-stats analyze hercules --include-file .core-repos` — target 38 processed / 0 failed
- [ ] `./ewws-stats combine --include-file .core-repos --include-data-exports`
- [ ] Re-enable `burndown_files`, `ownership_analyses` and `refactoring_proxy` in
      `ewws-stats.yaml` and confirm the run still completes — those three switches exist only
      as workarounds for B2/B3/B4 and should be **deleted**, not left as permanent configuration.

---

## Appendix: fixed, for cross-reference

Full diagnoses are in the git history of this file and in the merged PRs.

| item | what it was | commits |
| --- | --- | --- |
| B1 | merge-resolution deltas discarded, project matrix corrupted | `9e570a3`, `1be72df` |
| B1b | file deletion inside a merge commit emitted nothing | `1abecda`, `9c010ef` |
| B1c (half) | `sortableChange.Less` was not a valid ordering, so exact renames became delete+create | — |
| B1d | the merge commit was consumed by only one branch; merged lines now keep their real author | — |
| B2 | person matrices have *always* contained negatives; decided **(a) the accounting is wrong** — no negative cell recovers by the final row, so branch divergence explains none of them. Demoted to a warning behind `--strict-burndown-balances`; the accounting defect itself is B1c | `ebc8ded`, `a7c8cba`, `0ea3cc0` |
| B3 | `OwnershipSnapshot` underflow — a structural consequence of divergent branches sharing one accumulator, not an accounting slip; totals stay signed and only the metric snapshots clamp | `fafb754`, `c04d08c` |
| B4 | `--only` filtered after the merge; an unmergeable analysis sank the whole combine. `RefactoringProxy` is now mergeable, with tick axes rebased | `2f57e3d` |
| B5 | `HotspotRisk` merged to empty because `TopN` is 0 on the combine path | — |
| B6 | `--blob-cache-max-blob-size` was fail-closed; oversized blobs are now recorded as binary and warned about | — |
| B7 | `--lines-hibernation-disk` serialized an allocator that never hibernated | — |
| B8 | person burndown aborted on contributors with no activity | `c04d08c` |
| B9 | YAML burndown serialization panicked on nil `PeopleMatrix`; the `--pb` path panicked too | — |
| B10 | non-deterministic execution plan; a merge could be resolved against the wrong branch | `5937149` |
| B11 | `changeHibernation` hibernated per branch index while `ForkSamePipelineItem` shares one instance, so a fork's idle side put its sibling's analyser to sleep mid-run | `0dd3211` |
| B12 | rename matching raced two non-equivalent greedy matchers and kept whichever finished first | — |
| suite | opt-in corpus regression test, baseline-relative | `b97f549`, `c329229` |
