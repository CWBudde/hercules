# PLAN: correctness regressions blocking real-world use of 0.2.0

**Status:** open — 9 of 13 items fixed; B1c's double-removal residue carries the remaining
accounting defect
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

## Status

Checkbox = done / not done. The emoji is the severity of what is left.

- [x] [**B1**](#b1--merge-resolution-deltas-were-discarded-corrupting-the-project-matrix--fixed) — merge-resolution deltas discarded, project matrix corrupted (`9e570a3`, `1be72df`)
- [x] [**B1b**](#b1b--file-deletion-inside-a-merge-commit-emits-nothing--fixed) — file deletion inside a merge commit emits nothing (`1abecda`, `9c010ef`)
- [ ] [**B1c**](#b1c--residual-negatives-the-same-lines-removed-twice-across-branches-) — residual negatives: the same lines removed twice across branches 🟡 (rename comparator fixed; genuine double-removal open)
- [x] [**B1d**](#b1d--the-merge-commit-is-consumed-by-only-one-branch--fixed) — merge commit is consumed by only one branch; merged lines now keep their real author
- [x] [**B2** (demotion)](#the-demotion-is-done---the-decision-is-not) — negative balances warn instead of aborting (`ebc8ded`, `a7c8cba`, `0ea3cc0`)
- [x] [**B2** (decision)](#b2--person-burndown-matrices-have-always-contained-negatives--decided-a) — decided **(a)**: the accounting is wrong; the residual/transient split is now measured and reported
- [x] [**B3**](#b3--ownershipsnapshot-underflows-killing---bus-factor----ownership-concentration--fixed) — `OwnershipSnapshot` underflow (`fafb754`, `c04d08c`)
- [x] [**B4**](#b4--unmergeable-analyses-used-to-sink-the-whole-combine--mostly-fixed) — `--only` filters before the merge, unmergeable analyses skipped (`2f57e3d`)
- [x] **B4** (rest) — `RefactoringProxy` implements `ResultMergeablePipelineItem`; ratios merge weighted by `total_changes`
- [x] [**B5**](#b5--hotspotrisk-merges-to-empty--fixed) — `HotspotRisk` merged to empty because `TopN` is 0 on the combine path
- [x] [**B6**](#b6----blob-cache-max-blob-size-is-fail-closed-and-that-is-a-trap--fixed) — oversized blobs are recorded as binary and warned about instead of aborting the run
- [x] [**B7**](#b7----lines-hibernation-disk-panics-during-serialization--fixed) — `--lines-hibernation-disk` serialized an allocator that never hibernated
- [x] [**B8**](#b8--person-burndown-aborted-on-contributors-with-no-activity--fixed) — person burndown aborted on contributors with no activity (`c04d08c`)
- [x] **B8** (rest) — regression test with a deliberately idle person in the fixture
- [x] [**B9**](#b9--yaml-burndown-serialization-panics-on-people-with-no-interactions--fixed) — YAML burndown serialization panicked on nil `PeopleMatrix`; a zero-row matrix panicked on the `--pb` path too
- [x] [**B10**](#b10--the-execution-plan-was-non-deterministic-and-a-merge-could-be-resolved-against-the-wrong-branch--fixed) — non-deterministic execution plan; merges resolved against the wrong branch (`5937149`)
- [x] [**B11**](#b11--a-hibernated-burndown-analyser-consumes-a-commit--fixed) — a branch hibernated an analyser instance its sibling was still consuming
- [x] [**B12**](#b12--burndown-output-is-not-reproducible-run-to-run--fixed) — rename matching raced two non-equivalent matchers and kept whichever won
- [x] [**Corpus regression test**](#regression-coverage-worth-adding) — opt-in `HERCULES_CORPUS_DIR` suite, baseline-relative

---

## B1 — merge-resolution deltas were discarded, corrupting the _project_ matrix ✅ FIXED

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

**Fix** (`9e570a3`, `1be72df`): buffer the deltas in `LineHistoryAnalyser.pendingChanges` and
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

## B1b — file deletion inside a merge commit emits nothing ✅ FIXED

`handleDeletion` suppressed both the removal deltas (`file.Update` with `TreeMergeMark`,
which makes `File.updateTime` return before running any updater) and the
`NewLineHistoryDeletion` marker when `analyser.tick == TreeMergeMark`, yet dropped the
file from `analyser.files` regardless. Its lines stayed in `globalHistory` forever.

**Fix** (`1abecda`, `9c010ef`): a removal has nothing for `Merge()` to resolve — every removed line already
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

## B1d — the merge commit is consumed by only one branch ✅ FIXED

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

### Measured first: the length filter accounted for all of it

Before touching anything, `matchingMergeFiles` was instrumented to count, per file merge, how many
parents it accepted and how many lines `resolveMergeMarks` then charged to the merge author:

| repository | file merges | parent dropped | lines charged to the merge author |
| --- | ---: | ---: | ---: |
| `mekorp-backend` | 106 567 | 2 536 | 103 014 |
| `meko-etl-tool` | 6 974 | 913 | 38 390 |
| `render-pdf` | 3 322 | 116 | 11 241 |
| `backend-for-microscope` | 1 685 | 90 | 3 079 |

Splitting those lines by whether the file merge had lost a parent gave **100 % from the lost-parent
population and zero from the clean one**. When every parent is present `mergeLineValues` resolves
every mark and nothing reaches the merge author, so the 2–13 % of file merges that lose a parent
were producing all of the misattribution.

### The fix

1. `planBuilder.appendMergeReplicas` (`internal/core/forks.go`) replays the merge commit on every
   parent branch but the authoritative one, which comes first so that `OneShotMergeProcessor`
   keeps the right sighting. `TreeDiff` forks by copy, so each replica diffs against its own head.
2. `isMergeAction` accepts an adjacent commit replica as well as the merge action. Every replica
   must answer true, or a branch would offer its merge-introduced lines as ground truth.
3. `matchingMergeFiles` drops the `file.Id == target.Id` requirement. Once the parents converge,
   that was the **only** remaining reason to reject one — measured `noPath=0` and every rejection
   `otherId` with identical length. Upstream matches on the path alone; the length check stays
   only as a guard against the `mergeLineValues` panic.
4. `LineHistoryAnalyser.foldMergeRemovals` keeps one removal per file across the parents. This is
   load-bearing: a merge commit suppresses everything it inserts or edits, so removals are the only
   deltas it emits, and a file several parents hold reports one per parent. Left alone that
   double-removal recreates B1c's shape — measured 11 697 duplicate removals on `mekorp-backend`.
   Dropping them all instead would lose the file only one parent held.
5. `commits.go` and `knowledge_diffusion.go` gained `core.IsMergeReplica(deps)` guards; the ten
   leaves which already embed `OneShotMergeProcessor` were fine. Replicas do not advance
   `DependencyIndex`.

### Effect

Lines charged to the merge author instead of their real author:

| repository | before | after |
| --- | ---: | ---: |
| `mekorp-backend` | 103 014 | **341** |
| `meko-etl-tool` | 38 390 | **74** |
| `render-pdf` | 11 241 | **27** |
| `backend-for-microscope` | 3 079 | **2** |

99.7 % gone; the remainder is genuine conflict resolution, which does belong to the merge author.

The project matrix also moves sharply towards the truth. Comparing the final sampled row against
`git grep -I -c ''` at HEAD:

| repository | git at HEAD | before | after |
| --- | ---: | ---: | ---: |
| `meko-etl-tool` | 24 908 | 65 141 (+161 %) | **27 685 (+11 %)** |
| `render-pdf` | 81 290 | 93 195 (+15 %) | **81 725 (+0.5 %)** |
| `backend-for-microscope` | 42 591 | 45 781 (+7.5 %) | **42 704 (+0.3 %)** |
| `personio-ipoffice-sync` | 5 014 | 5 014 | 5 014 |
| `mekorp-backend` | 958 910 | 882 918 (−7.9 %) | 823 907 (−14.1 %) |

The inflation those matrices carried was the merge re-adding lines a parent had already counted.

**`mekorp-backend` is the open end.** It was already under-counting before this change, and the
same correction pushes it further under. The likely reading is a separate pre-existing shortfall
there — B6's fail-closed blob size limit would do it, and that repository has the large generated
files for it — with B1d's correction applied on top rather than a new defect. Not proven. It is
the repository to drive the corpus regression suite from.

### Tests

`TestMergeActionLeadsWithTheConsumingBranch` additionally asserts that every branch a merge
reconciles has consumed it. `verifyLineageInvariants` gained invariant 1b, keyed off what the
planner decided rather than the raw parent count — a redundant merge edge is collapsed away and
must then *not* be replayed. `TestPrepareRunPlanBig`'s "extra commit actions" column went from `0`
everywhere to the per-case merge count, which is upstream's `1`–`7` shape.

---

## B1c — residual negatives: the same lines removed twice across branches 🔴

The residue from B1's table, and — now that B2 is decided — the item that carries the whole
accounting defect. Reproduces on `082bf15` once output clamping is disabled, so it is
long-standing and was only ever hidden. It **no longer aborts** (see B2's demotion), but the
numbers are wrong.

**Root cause, traced on `render-pdf` change by change.** A file's lines are removed twice,
once by each of two sibling branches, and nothing ever reconciles the duplicate:

```
TRACE 0090e98b file=395 prev=304 curr=304 delta= 7413  samples/Messbericht mit Bezeichner/data.json
TRACE bc3aea9f file=395 prev=304 curr=416 delta=-7396  samples/other/…/data.json
TRACE 52ca72b7 file=395 prev=304 curr=416 delta=-7388  samples/other/…/data.json
TRACE bc3aea9f file=510 prev=416 curr=416 delta= 7412  samples/default/…/data.json
```

`bc3aea9f` and `52ca72b7` are **siblings** — both have parent `6e5bc06`, and
`git merge-base --is-ancestor` confirms neither reaches the other.

- `52ca72b7` genuinely truncates the file by 7620 lines. Legitimate.
- `bc3aea9f` only **renames** `samples/other/… → samples/default/…`; `git show --stat` reports
  zero content change. Rename detection did not carry the file identity across, so it is
  accounted as delete file 395 (−7396 charged to age band 304) plus create file 510 (+7412
  credited to age band 416).

Both branches feed the one accumulator that `ForkSamePipelineItem` shares, so band 304 receives
+7413 −7396 −7388 = **−7371** and stays there. That is the −7386 cell the warning names.

**This is what settles B2 as (a).** B3 assumed the merge would deliver the compensating
positive, so the duplication would cancel. It does not: the merge sees both parents with the
lines already gone and has nothing to re-add. The negative is permanent.

The rename losing the file's age is a second, milder problem visible in the same trace: the
lines are re-credited to band 416 instead of keeping band 304, so even the *positive* bands
are wrong. Downstream sees that one without any warning at all, because
`pb.ToBurndownSparseMatrix` clamps only the negatives.

### Half of it was a broken sort comparator ✅ FIXED

The rename that "did not carry the file identity across" was not a heuristic falling short —
`sortableChange.Less` (`internal/plumbing/renames.go`) was not a valid ordering:

```go
for x := range 20 {
    if change.hash[x] < other.hash[x] {
        return true          // never checks whether an earlier byte was already greater
    }
}
```

A greater leading byte could be outvoted by a smaller trailing one, so for two hashes both
`a.Less(b)` and `b.Less(a)` held. Across 20 uniformly distributed bytes that is the *normal*
case, not a corner: almost every pair of real blob hashes compares as mutually smaller. So
`sort.Sort` produced an arbitrary order, and it ordered `added` and `deleted` differently.
`matchExactRenames` walks those two lists as a single merge scan, which only pairs identical
hashes when both are sorted consistently — so verbatim moves (git's R100) were emitted as a
delete plus a create. That is exactly `bc3aea9f`.

The same code is in upstream (`hercules-original/internal/plumbing/renames.go:545`), which is
why B1c reproduces on `082bf15`.

Fixed by returning at the first differing byte. Pinned by
`TestSortableChangesIsAntisymmetric` and `TestRenameAnalysisConsumeDetectsExactRenames`; both
fail against the old comparator. Note the pre-existing `TestSortableChanges` compares all-`00`
against all-`ff`, the one pair the mistake gets right — which is how it survived. The new
fixtures use random hashes for that reason.

**Measured over the affected corpus** (`--burndown --burndown-people`, granularity/sampling 30;
"mass" is the sum of the absolute values of all negative cells):

| repository | cells before | after | mass before | after | worst before | after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `mekorp-backend` | 2180 | 2226 | 7 745 717 | 7 819 365 | −80 417 | −80 417 |
| `meko-etl-tool` | 514 | 498 | 259 619 | 233 161 | −2 177 | −2 177 |
| `backend-for-microscope` | 183 | 183 | 4 044 | 4 044 | −126 | −126 |
| `render-pdf` | 158 | 47 | 463 735 | **1 114** | −7 386 | **−58** |
| `personio-ipoffice-sync` | 6 | 6 | 104 | 104 | −18 | −18 |

`render-pdf` — the repository the trace above came from — loses 99.8% of its negative mass and
its worst cell drops from −7386 to −58. That confirms the mechanism end to end.

It is **not** the whole of B1c. Three repositories are untouched and `mekorp-backend`, by far
the largest by mass, is unchanged; its cell count even rises slightly, because better rename
detection redistributes lines between age bands. Cell count is the weaker signal here — mass
and worst-cell are the ones to watch.

Beware: this changes file identity tracking for *every* analysis, not just burndown. It should
be strictly an improvement (more true renames detected), but it moves numbers everywhere.

### What remains 🔴

The residue is the genuine double-removal: two sibling branches each delete the same lines from
their own copy, both feeding the accumulator `ForkSamePipelineItem` shares, and the merge has
nothing to re-add. Fixing that means reconciling the two branches' line state at the merge
rather than letting both removals stand — the same territory as B1d. Do not attempt it without
the corpus regression suite in place first, and re-measure `mekorp-backend` specifically, since
it is now the only large unexplained case.

### B1d did not absorb it — measured

B1d shares the mechanism, so it was worth checking whether it closed the residue as a side
effect. It did not. Both columns below are fresh runs of the same command
(`--burndown --burndown-people`, granularity/sampling 30): "pre" is a build of `77171d5` (the
comparator fix, before the merge replay), "now" is the current tree. Figures are
cells / negative mass, split by matrix scope.

| repository | project pre | project now | people pre | people now |
| --- | ---: | ---: | ---: | ---: |
| `mekorp-backend` | 435 / 3 751 055 | 508 / 3 749 220 | 1791 / 4 068 310 | 1450 / 3 972 954 |
| `meko-etl-tool` | 107 / 100 251 | 167 / **147 164** | 391 / 132 910 | 401 / **183 942** |
| `backend-for-microscope` | 64 / 946 | **23 / 613** | 119 / 3 098 | 114 / 3 095 |
| `render-pdf` | 0 / 0 | 0 / 0 | 52 / 2 088 | 29 / **8 641** |
| `personio-ipoffice-sync` | 3 / 52 | 3 / 52 | 3 / 52 | 3 / 52 |

Cell counts fall in three repositories, but **negative mass rises in two** — `render-pdf`'s
people scope 2 088 → 8 641 and `meko-etl-tool` throughout. That is consistent with B1d working
as intended rather than regressing: moving merged lines off the merge author and onto their real
author relocates the later removals into bands where the matching positive was never inserted,
which makes the double-removal *more* visible. Worst cell is unchanged everywhere except
`render-pdf` (−92 → −323) and `meko-etl-tool` (−2177 → −2248).

One genuine win to keep: `render-pdf`'s **project** matrix is clean at both revisions, and every
negative it has left sits in a single person's matrix. Its −7386 project cell was entirely the
comparator.

### Attempt 1: reconcile the accumulator against the merged tree — not shippable

Tried and reverted. The code is kept at
`…/scratchpad/b1c-patch/` (`merge_reconcile.go` plus `line_history.diff`); a worktree build sits
at `…/scratchpad/hercules-t2`. Worth reading before attempting this again — most of it is right,
and the failure is specific.

**The idea.** The five leaves that consume `DependencyLineHistory` all fork with
`ForkSamePipelineItem`, so they share one accumulator while the branches feeding it do not. Mirror
what the consumers have been told (per file, per ownership band), and at every `Merge()` compare
it against the merged tree. The difference is the accumulated error, whatever produced it; emit it
as ordinary deltas. No need to enumerate the causes.

**Four things this got right, each measured:**

1. Correcting the _band_ a line is filed under, as opposed to how many lines there are, is a
   mistake. `mergeLineValues` adopts the older side's ownership for a line both branches rewrote,
   and a later merge can adopt the other side again — the corrections oscillate. On
   `backend-for-microscope` re-attribution carried 53 505 of mass against the real defect's
   37 444, and drove single cells from −126 to −25 748. Correct the count only.
2. Correcting only the restoring direction is also a mistake, even though only that direction
   clears negatives. A double removal does not just push one band below zero, it credits the same
   lines to another band — B1c's own trace has band 304 at −7371 while band 416 holds +7412. Fixing
   the deficit alone inflated `render-pdf` by exactly the restored 11 498 lines.
3. A correction must be booked at the sample the faulty delta landed in, not at the merge tick.
   Otherwise every row in between stays wrong and the correction shows up as a step in the
   surface. Tracking the last write per (file, band) and booking there is what took `render-pdf`
   from 29 negative cells to **zero**.
4. A branch's `pendingChanges` are silently discarded by `synchronizeLineHistoryBranch`. Those
   deltas never reach any consumer. Pre-existing, unrelated to this attempt, and worth its own
   item.

**Where it stands** (`--burndown --burndown-people`, granularity/sampling 30; "head" is the
project matrix final row against `git grep -I -c ''` at HEAD):

| repository | negatives before | after | head before | after |
| --- | ---: | ---: | ---: | ---: |
| `render-pdf` | 29 / 8 641 | **0 / 0** | +0.5% | **−0.2%** |
| `backend-for-microscope` | 137 / 3 708 | 95 / 2 141 | +0.3% | −2.9% |
| `personio-ipoffice-sync` | 6 / 104 | 6 / 104 | ±0% | ±0% |
| `meko-etl-tool` | 568 / 331 106 | 469 / **389 249** | +11.1% | **−13.4%** |

Three repositories improve and one is clean outright. `meko-etl-tool` regresses: fewer cells but
more mass, worst cell −2248 → −4983, and the head count moves from 11% over to 13% under. That is
why this is not merged.

**Why it fails — diagnosed, and it is the approach, not a tuning problem.**

The regression concentrates on one author: in `meko-etl-tool`'s people matrices `clausbissinger`
goes 98 425 → 6 421, `kanngiesser` 29 105 → 108 and `torben voltmer` 10 361 → 0, while
`costa-kolini|emil klahn` alone goes 46 051 → 192 089 and carries the −4983. Traced to a single
correction, and then to the project matrix column it lands in:

```
file id 954, band (author 2, tick 261): believed 5278, tree says 0  ->  emit -5278 at tick 261

project matrix, band column 8:
  row     8      9     10     20     30     40     52
  before  9208   6613  6576   5779   307    307    302     <- drawn down by ordinary commits
  after   3923   1329  1292    494  -4978  -4978  -4983
  diff   -5285  -5285 -5285  -5285  -5285  -5285  -5285
```

The band was not wrong. Those 5278 lines were alive in the accumulator at row 8 and were then
removed normally, decaying to 302 by the end. Subtracting them at row 8 removes them a second
time, and the constant −5285 offset is that double removal riding through every later row.

So `actual = 0` was the wrong reading. File id 954 was not gone — it was simply not named in the
tree of the branch performing this merge, and it was still live on a branch that was not party to
it. **`liveBandHistogram` returning nothing means "not mine", not "does not exist."**

That is fatal to the whole design and not just to the dead-file case, because the mirror is global
while the tree it is compared against belongs to one branch. Four variants were measured and the
trade is structural, not tunable:

| variant | `meko-etl-tool` negatives | head | `render-pdf` negatives | head |
| --- | ---: | ---: | ---: | ---: |
| none (B1d baseline) | 568 / 331 106 | +11.1% | 29 / 8 641 | +0.5% |
| dead ids, both directions | 469 / 389 249 | −13.4% | **0 / 0** | −0.2% |
| dead ids skipped entirely | 446 / 73 468 | +42.5% | 29 / 8 641 | +5.4% |
| dead ids, restore only | **256 / 31 680** | +152.2% | **0 / 0** | +14.4% |

Every variant that clears negatives inflates the head count and vice versa, because both halves
are mis-attributions of the same kind. Booking dead-id corrections at the merge tick instead of
the last band write was also tried: no effect on `meko-etl-tool`, and it cost `render-pdf` its
zero (0 → 2 cells, −5160 worst).

**The corrected design, for a second attempt.** Drop the global mirror. Reconcile from per-branch
contributions instead, so a correction can only ever involve the branches actually merging:

- Track `D_i`, the deltas each branch has emitted per file and band since the fork — `touchedFiles`
  already tracks the file set, so this is the same bookkeeping one level finer.
- Snapshot `H_pre`, branch 0's band histogram, before the `file.Merge()` loop; `H_m` is the same
  after it.
- The correction is `(H_m - H_pre) - Σ_{i≥1} D_i`. No `H_0` term survives, and no global state is
  needed.

A file that is live on a branch outside the merge contributes to no `D_i` here and is therefore
left alone, which is exactly the mis-attribution that sank attempt 1.

Two further leads, both measured and both dead ends as implemented — the mirror must be updated
when changes are _emitted_, not when they are produced:

- Recording merge output into the mirror at `Merge()` time leaves it there forever whenever
  `synchronizeLineHistoryBranch` discards the buffer (see 4 above): `meko-etl-tool`'s head fell to
  −62%.
- Adding a _shared_ ledger of outstanding buffers, so a sibling merging in between sees them,
  fails the same way for the same reason (−62%). It does fix a real double-correction — one file
  restored 2652 lines on two consecutive merges — so the problem it solves is genuine even though
  this solution is not.

The corpus regression suite now exists, which is what makes a second attempt reasonable.

### Attempt 2: reconcile from per-branch contributions — also not shippable, but it found the cause

The design above was built and measured. Getting it right took four corrections, each of which is
worth recording because each is a real property of the pipeline:

1. **Accounting has to be per fork, not per branch object.** A branch that merges below this point
   has its own accounting zeroed, so an enclosing merge sees no `D_i` for it and re-adds lines that
   were already booked. Fixed by folding a merged branch's deltas into the branch performing the
   merge (`absorbBranchAccounting`), and then by making the accounting a stack with one frame per
   fork — the fork origin keeps its branch slot, so without a frame of its own it offers an
   enclosing merge deltas from before that fork and cancels work that merge never saw.
2. **`H_pre` has to be snapshotted in `Consume()`, not in `Merge()`.** Consuming a merge commit
   overwrites the ownership of every line it touches with `TreeMergeMark`. By the time `Merge()`
   runs, those lines have no band, so `H_pre` is missing them while `H_m` has them at their
   resolved band — the count is added and never subtracted. Uncorrected this inflated
   `backend-for-microscope`'s head by 99%.
3. **The invariant the whole design rests on is false.** Instrumented against a mirror of what was
   actually emitted, `H_pre + Σ D_i` matched the real accumulator in **29% of corrections**
   (350 corrections on `backend-for-microscope`, total drift 69 060). 53 negative corrections drove
   the true accumulator below zero, 29 339 lines' worth.
4. The drift is created by `mergeLineValues` (`file.go`). When both branches introduced the same
   line, it silently adopts the older side's ownership — the tree is re-banded and **no delta is
   emitted**. So the tree and the accumulated history diverge, permanently and cumulatively, and
   any correction derived from a tree snapshot inherits the whole accumulated divergence. The
   clearest single instance:

   ```
   file 98, band (author 7, tick 325): derived 27894, really accumulated 0      -> books -27894
   file 98, band (author 3, tick 520): derived     0, really accumulated 25717  -> books +25717
   ```

   Same lines, two different bands. That is `backend-for-microscope`'s −25 749 cell.

**Measured variants**, all fresh runs, `--burndown --burndown-people`, granularity/sampling 30.
Negatives are cells / mass across both matrix scopes; head is the final-row sum against
`git grep -I -c '' HEAD`.

| variant | render-pdf | head | backend-for-microscope | head | meko-etl-tool | head |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| none (current tree) | 29 / 8 641 | +0.5% | 137 / 3 708 | +0.3% | 568 / 331 106 | +11.1% |
| per-branch, full | 29 / 8 612 | +0.7% | 146 / 949 771 | −21.6% | 375 / 494 311 | +14.7% |
| per-branch, restore only | 29 / 8 612 | +2.4% | **18 / 54** | +70.5% | 242 / 93 359 | +44.3% |
| against the emission mirror | 29 / 8 293 | +5.6% | 102 / 173 527 | +58.2% | **83 / 9 106** | +176% |
| `mergeLineValues` emission only | 29 / 8 641 | **−0.8%** | 137 / 3 708 | **+0.25%** | 647 / 336 486 | +9.3% |

`personio-ipoffice-sync` is 6 / 104 with an exact head under every variant.

Two readings, both firm. **Negatives and head totals still trade against each other** — every
variant that clears negatives inflates the head, which is what attempt 1 found too, and now has an
explanation rather than a correlation. And **reconciling at merge time cannot work at all**: the
tree is not a record of what was accumulated, so no snapshot of it is a valid target.

**Where the fix looked like it had to go.** Emit the delta at the point the ownership actually
changes, in `mergeLineValues` — book out the version being dropped, since both branches reported
an insertion and only one survives. That was attempt 3; it does not work either, see below.

**Also found, unfixed.** The emission-mirror variant is defeated by file identity:
`matchingMergeFiles` now pairs files by path across differing `FileId`s (B1d), while all accounting
keys on `FileId`, so merged-in content is counted under both ids. Any future reconciliation has to
resolve identity first.

### Attempt 3: emit the delta in `mergeLineValues` — not shippable, and it closes the question

Built with tests, measured against the corpus baseline seeded at `b97f549`, reverted. The defect it
targets is real and the unit test pins it: `TestFileMergeDisplacedOwnershipIsReported` fails without
the emission. It is the measurement that says no.

**One-sided** (book out the destination's claim when it loses to an older version). Both builds were
run twice and every figure reproduced exactly, so this is not B12 variance:

| repository | baseline | one-sided | head, baseline → one-sided |
| --- | ---: | ---: | ---: |
| `render-pdf` | 0 / 0 | 0 / 0 | +0.54% → −0.78% |
| `personio-ipoffice-sync` | 6 / 104 | 6 / 104 | ±0 |
| `backend-for-microscope` | 137 / 3 708 | 137 / 3 708 | +0.27% → +0.25% |
| `meko-etl-tool` | 573 / 244 067 | **626 / 250 656** | +14.88% → +13.01% |
| `mekorp-backend` | 1 809 / 7 112 000 | **1 927 / 7 946 422** | −13.37% → **−14.50%** |

`just test-corpus` fails on it: `mekorp-backend` 1748 → 1877 negative cells, residual 67 → 71.
Note the head column — an earlier reading of this variant called it "the only one that moves every
head towards `git`", which was measured on a four-repository set that left `mekorp-backend` out.
With it in, the head gets worse too. That claim was wrong.

**Symmetric** (book out whichever side loses, not just this one — the accounting argument applies
equally to the branch whose version is discarded). Strictly worse:

| repository | baseline | one-sided | symmetric |
| --- | ---: | ---: | ---: |
| `render-pdf` | **0 / 0** | **0 / 0** | 23 / 1 473 |
| `backend-for-microscope` | 137 / 3 708 | 137 / 3 708 | 171 / 41 819 |
| `meko-etl-tool` | 573 / 244 067 | 626 / 250 656 | 701 / 266 683 |

**Why both fail, and why this closes the approach.** `mergeLineValues` pairs the two files **by
position**. Lines at the same index are not the same line — they are two files of equal length laid
side by side. So "both branches introduced this line" is an inference the function is not entitled
to make, and booking out either side removes lines that were never duplicates. The symmetric
version makes twice as many of those claims and is twice as wrong, which is the confirmation.

This is the same wall attempt 2 hit from the other side. The tree is not a record of what was
accumulated, and positional pairing is not a record of line identity, so **neither the merged
contents nor the merge itself can supply the correction**. Anything that closes B1c has to carry
line identity across the fork rather than reconstruct it at the merge — which is a change to what
`File` stores, not to how a merge is accounted. That is a much larger piece of work than any of the
three attempts, and it should not be started without deciding B1c is worth it.

The symmetric diff (which contains the one-sided version as its first half, plus both unit tests) is
preserved at `scratchpad/b1c-mergelinevalues/symmetric.diff`. Two existing assertions in
`TestFileMergeShallow`/`TestFileMergeDeep` had to change for the symmetric variant: they assert
`status[6] == 10` for a band the merged file no longer holds any lines of — which is the same defect
seen from the other side, and worth knowing about independently of any fix.

Attempt 2's code is preserved at `scratchpad/b1c-attempt2/` (`merge_reconcile.go` plus a diff
against `2cb415d` for `line_history.go` and `file.go`); the variants are behind `HCK_RECONCILE`,
`HCK_RESTORE_ONLY` and `HCK_TRACE`. Nothing was merged.

**Affected in the corpus, on the current tree:** `mekorp-backend` (1958 cells,
mass 7 722 174, worst −80 417), `meko-etl-tool` (568 / 331 106), `backend-for-microscope`
(137 / 3 708), `render-pdf` (29 / 8 641), `personio-ipoffice-sync` (6 / 104).
`mekorp-backend` still carries 96% of the mass, and its project matrix alone is
508 cells / 3 749 220.

---

## B2 — person burndown matrices have _always_ contained negatives ✅ DECIDED (a)

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

### The decision: (a) ✅

**(a) is right. The accounting is wrong.** The two options were never actually exclusive —
they answer different questions:

- **(b)** is a question of _policy_: should a negative cell kill the run? Answered "no", shipped.
  See "The demotion is done" below. Nothing here reopens it.
- **(a)** is a question of _accounting_: should those cells exist? Answered "no", and the
  mechanism is now traced. See B1c.

The reasoning below records how the earlier reading of (b) came about and what refuted it,
because the argument for (b) was genuinely persuasive and someone will make it again.

#### The discriminator: transient versus residual

The burndown matrix is not a delta log. `groupSparseHistory` emits a **cumulative** matrix —
one row per sample, each row a snapshot of alive lines per age band. That gives a test which
separates the two candidate explanations cleanly:

> By the final row every branch has merged. A negative which branch divergence produced must
> have recovered by then. A negative still present in the final row cannot be explained that
> way.

Measured across the five affected repositories, over every project and person matrix:

| repository | negative cells | still negative in the final row | recovered |
| --- | ---: | ---: | ---: |
| `mekorp-backend` | 2180 | 2180 | 0 |
| `meko-etl-tool` | 514 | 514 | 0 |
| `backend-for-microscope` | 183 | 183 | 0 |
| `render-pdf` | 158 | 158 | 0 |
| `personio-ipoffice-sync` | 6 | 6 | 0 |

**Not one negative cell recovers.** The transient explanation accounts for none of them.

`render-pdf`'s project matrix shows the shape plainly — a single sample step into the negative,
then flat for three years:

```
col 10:  9449  9390  9306  -5576  -5577  -5577 … -6831  -6836
col  9:  3795  3793  1980   -962  -1107  -1107 … -1109  -1117
```

Column 10 falls by 14 882 in one step when only ~9 306 lines were alive to remove. That is one
removal counted roughly twice. B1c traces it to the individual commits.

#### What was implemented

`auditBurndownResultBalances` now classifies each negative cell as residual (its age band is
still negative in the final sampled row) or transient, and the warning reports both:

```
… 158 cell(s) affected in total (person: 105, project: 53), 158 of them still negative in
the final sampled row. …
```

Residual is the honest measure of the defect and the invariant worth regression-testing —
it has no false positives from legitimate divergence, and empirically it loses nothing.
Test: `TestBurndownBalancesSeparatesResidualFromTransient`.

The remaining work — actually fixing the double count — lives in B1c.

### Superseded: B3's trace looked like it answered this — (b), with a caveat 🔎

_Kept for the record. The conclusion below is wrong; the measurement above refutes it._

The `OwnershipSnapshot` investigation (see B3) traced an identical negative to its
mechanism, on `ewws-render`, change by change. It is **structural, not an accounting
slip**: divergent branches each remove the same lines from their own copy of a file, and
every branch feeds one shared accumulator (`ForkSamePipelineItem`), so a total goes negative
until the branches merge and the duplication cancels. Confirmed by
`git merge-base --is-ancestor`: the two commits that removed the same 32 lines are on
branches neither of which reaches the other.

Burndown's `sparseHistory` is fed by the same `LineHistoryChanges` in the same way, so its
negative cells have the same origin — the difference is only that `sparseHistory` tolerates
the dip and `ownershipSnapshotAccumulator` used to abort on it.

That makes **(b) the supported reading**, with one important refinement over how (b) is
worded above: _do not clamp the accumulator_. The negative is a placeholder for a positive
the merge is going to deliver; clamping it away destroys the cancellation and inflates every
later total. Clamp at the reporting boundary only. That is exactly what B3's fix does, and
what `pb.ToBurndownSparseMatrix` has always done for burndown.

The caveat: a **final** matrix should still balance out, because by then every branch has
merged. Cells that are still negative after `Finalize` — `render-pdf`'s −7386, 208 cells —
are not explained by this and remain open. So the honest split is: transient negatives are
expected and must be tolerated; residual ones at the end are a real defect still to chase,
and B1c is part of that.

**Where this went wrong:** it assumed the merge delivers the compensating positive. It does
not. When both branches have already removed the lines, the merged tree has them gone too and
there is nothing to re-add. B1c shows this on `render-pdf`. The "caveat" in the last paragraph
turned out to be the whole phenomenon, not an edge of it — every negative is residual.

B3's own fix is unaffected: keeping the accumulator signed and clamping at the reporting
boundary is still correct, because clamping mid-run would corrupt the positive bands as well.

### The demotion is done ✅ — the decision is not

Committed in `ebc8ded`, `a7c8cba`, `0ea3cc0`. `validateBurndownResultBalances` was replaced by
`auditBurndownResultBalances`, which scans
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

## B3 — `OwnershipSnapshot` underflows, killing `--bus-factor` / `--ownership-concentration` ✅ FIXED

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

### Root cause: found, and it is not an accounting slip ✅

Both earlier hypotheses (skipped `AuthorMissing` insertions; missing
`peopleCount` clamping) are **wrong**. Tracing every change for the offending
`FileId` on `ewws-render` shows the actual mechanism.

File 39 accumulates 32 lines for author 5 at tick 7. Commit `b84333d6` (tick 42)
replaces 12 of them: `+12` for author 2 at tick 42, `-12` for author 5 at tick 7,
leaving `{a5: 20, a2: 12}`. Commit `93355521` (tick 75) then deletes the file and
emits `-32` for author 5 at tick 7 — more than the 20 that remain.

`93355521` is **not a descendant of `b84333d6`**; `git merge-base --is-ancestor`
says neither reaches the other. They are on divergent branches. Each branch holds
its own line-history tree, and each legitimately removes the whole file _as its
own branch sees it_. Every branch feeds the one shared accumulator
(`ForkSamePipelineItem`), so the same lines are removed twice and the total goes
negative until the branches merge and the duplication cancels.

The same trace on file 13 is unambiguous: `93355521`'s removals replay, delta for
delta, exactly the ownership breakdown file 13 had _before_ `b84333d6` rewrote it
(`-4@t7 a5`, `-3@t36 a8`, `-1@t40 a8`, `-20@t7 a5`, …).

**So the invariant was wrong, not the arithmetic.** A shared accumulator fed by
divergent branches cannot be assumed monotonically non-negative, and aborting on
the first dip kills `--bus-factor` and `--ownership-concentration` on perfectly
ordinary histories.

**This is the same mechanism as B2's negative burndown balances** — burndown's
`sparseHistory` simply tolerates the dip and carries a negative cell instead of
erroring. See the note under B2.

### Fix (`fafb754`, `c04d08c`)

- Internal totals stay **signed**. Clamping in `apply` would destroy the
  compensating positive the merge later delivers and inflate every subsequent
  total.
- Clamping happens at the reporting boundary only — `snapshot()` and
  `subsystemOwnership()` report zero for a negative total and drop authors left
  with nothing, since no metric downstream can use a negative line count.
- One `[WARN]` per run names the first dip and why it happens; repeating it would
  be one line per commit on a repository with a long-lived branch.

Measured: `ewws-render`, `go-clients` and `ewws-tailscale` all complete
(`exit 0`), the first two with one warning each, `ewws-tailscale` with none.
`labours -m bus-factor` renders the result.

### Deliberately not changed

`apply` still ignores the `IsDelete()` sentinel, and the earlier claim that this
is "unambiguously wrong" does not survive the trace. `handleDeletion`
(`line_history.go:1106`) calls `file.Update(..., 0, 0, lines)` **before**
`file.Delete()`, so the per-line removals are already emitted explicitly; the
sentinel is pure bookkeeping, exactly as `BurndownAnalysis.updateFileDelete`
treats it. Subtracting the file's remaining authors on the sentinel would
double-subtract — and worse, would drop the lines a _sibling branch_ still holds
in that file. What is genuinely open is whether a per-file entry should be
retired for `subsystemOwnership` purposes when the file dies on every branch;
that needs a merge-aware answer, not a local one.

---

## B4 — unmergeable analyses used to sink the whole combine ✅ (mostly fixed)

`hercules combine` failed outright if _any_ input `.pb` contained an analysis
without `ResultMergeablePipelineItem`:

```
CommitsStat   FileHistoryAnalysis   ImportsPerDeveloper   RefactoringProxy
→ "<Name>: ResultMergeablePipelineItem is not implemented"
```

The set is larger than originally recorded. Enumerating the registry, **10 of 20
leaves are mergeable** and 10 are not:

| mergeable                                                                                                                                                    | not mergeable                                                                                                                                                            |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `Burndown`, `BusFactor`, `CodeChurn`, `Couples`, `Devs`, `HotspotRisk`, `KnowledgeDiffusion`, `LegacyBurndown`, `OwnershipConcentration`, `TemporalActivity` | `CommitsStat`, `FileHistoryAnalysis`, `ImportsPerDeveloper`, `RefactoringProxy`, `LineDumper`, `Onboarding`, `Sentiment`, `Shotness`, `TyposDataset`, `UASTChangesSaver` |

### What was wrong

`deserializeAnalysisContents` decoded **every** key in the file and recorded a
failure for each unmergeable one; `--only` was applied later, in `mergeResults`,
long after those failures had been collected. So the flag never restricted
anything that mattered. Note the 0.2.0 aspect of the regression: `082bf15`
printed the same messages but exited 0, whereas 0.2.0 returns them from `RunE`.

### Fix (`2f57e3d`)

- `--only` now filters at deserialization, so an analysis nobody asked for cannot
  fail the run on its way in.
- Without `--only`, an unmergeable analysis is **named and stepped over** rather
  than fatal — it is a property of the analysis, not a defect in the input, and
  killing the combine discards every mergeable analysis alongside it. The
  messages moved from the `Errors:` block to a new `Skipped:` block on stderr and
  no longer contribute to the exit code.
- Naming an unmergeable analysis in `--only` stays a hard error: there the caller
  asked for exactly that.
- `--only`'s help text now lists the 10 mergeable leaves only, instead of
  advertising choices `combine` can never honour.

Measured on `ewws-tailscale` analysed with `--burndown --devs --file-history
--commits-stat`:

| invocation                             | before | after                                                |
| -------------------------------------- | ------ | ---------------------------------------------------- |
| `combine a.pb a.pb`                    | exit 1 | exit 0, contents `[Burndown Devs]`, 2 skips reported |
| `combine --only Burndown …`            | exit 1 | exit 0, contents `[Burndown]`                        |
| `combine --only FileHistoryAnalysis …` | exit 1 | exit 1, with the reason named                        |

### The rest: `RefactoringProxy` is now mergeable ✅ FIXED

It was the one unmergeable leaf with a labours mode; everything else in that column has no
combined rendering, so skipping remains the right end state for them.

`Deserialize` already existed and was correct — only `MergeResults` was missing, and the
embedded `core.NoopMerger` (the *branch* merger) was being mistaken for it.

**The one real design question was the dropped rename count.** `Finalize` stores
`RenameRatios[i] = Renames/TotalChanges` and discards the raw count, which looks like it
forces a lossy `round(ratio*total)` reconstruction at merge time. It does not: the merge
never needs an integer. The count-weighted mean

```
ratio_merged = (r1*t1 + r2*t2) / (t1 + t2)
```

keeps `r*t` in `float64`, and it is the *rounding* that would lose information. Residual
error is ~1e-16 relative, against the ~1e-7 the existing `float32` narrowing in
`serializeBinary` already costs — so adding a `Renames []int` field to the result and
`pb.proto` (with its `just pb-go-force`, schema-snapshot refresh, `cmd/schema-guard` review
and a new raw-vs-derived consistency invariant) would buy nothing measurable. Not done, on
purpose.

Tick axes are rebased onto the earlier repository's start exactly as `DevsAnalysis` does,
with two guards devs does not need: nil commons and a zero tick size, both reachable from
hand-built results. This matters because `pb_reader` plots `start + tickIndex*tickSize` with
`start` = the merged `BeginTime`, which `CommonAnalysisResult.Merge` sets to the minimum of
the two — without the offset, the second repository's tick 0 lands on the first
repository's first day. Mismatching `tickSize` **and** `Threshold` are rejected rather than
silently taking the first: unlike `WindowDays`-style metadata the threshold is the
classifier, and labours draws it as a reference line, so inheriting it would re-label the
other repository's ticks under a threshold it was never analysed with.

`--only`'s help list needed no change — it filters by the type assertion, so the leaf
appears as soon as the method exists.

Tests: `TestRefactoringProxyMergeResults*` in `leaves/refactoring_proxy_test.go` plus
`TestRunCombineMergesRefactoringProxy` in `cmd/hercules/combine_test.go`. The one that
earns its keep is the rebasing test — stubbing the offsets to `0, 0` collapses two
repositories two days apart from `[0,1,2,3]` with totals `[10,10,20,20]` to `[0,1]` with
`[30,30]`, silently and without error.

### Original report

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

Downstream workaround: `ewws-statistics` stopped requesting `--commits-stat`,
`--file-history` and `--imports-per-dev` altogether — no labours mode renders
them, so they were pure cost plus a combine failure.

---

## B5 — `HotspotRisk` merges to empty ✅ FIXED

Renders correctly from a single repository's `.pb`, produced nothing after
`combine`:

```bash
hercules --hotspot-risk --pb --skip-blacklist ../ewws-render > hr.pb
labours -i hr.pb -f pb -m hotspot-risk -o hr.svg          # → OK, ranked file table
hercules combine hr.pb hr.pb > hr2.pb
labours -i hr2.pb -f pb -m hotspot-risk -o hr2.svg        # → "no hotspot risk files found"
```

### Root cause: `TopN` is zero on the combine path

Not `Deserialize`, not the protobuf — both carry every field intact. `MergeResults` ended
with

```go
if len(allFiles) > hra.TopN {
    allFiles = allFiles[:hra.TopN]
}
```

and `combine` summons the item through `Registry.Summon` → `reflect.New`
(`internal/core/registry.go:87,91`), which yields a **zero-valued struct**. Neither
`Configure` nor `Initialize` runs on that path, and `Initialize` is where the
`if hra.TopN == 0 { hra.TopN = DefaultTopN }` fallback lived. So the condition was
`len(allFiles) > 0` and every non-empty merge was cut to `[:0]`.

The existing unit test missed it for the same reason the bug survived: it constructs
`HotspotRiskAnalysis{TopN: 2}` explicitly, which is exactly what the combine path does not
do.

### Fix

`effectiveTopN()` applies the fallback (on `<= 0`, not `== 0` — a negative `TopN` would
have panicked the slice) and is called from `Initialize`, `Finalize` and `MergeResults`, so
the three cannot drift. A `WindowDays` mismatch between inputs is now an error rather than
silently inheriting `cr1`'s, matching `errDevsMismatchingTickSizes`.

**Deliberately not changed, and recorded in a comment on `MergeResults`:** the merge does
not deduplicate by `Path`, because across two repositories the same path is two different
files and `FileRisk` carries no repository qualifier. And the scores are normalized per-run
by `normalizeAndScore`, so they are not on a common scale across repositories — fixing that
needs a `FileRisk` schema change and is its own item.

Measured on `/mnt/projekte/Code/MeKo/ewws-render`: `combine hr.pb hr.pb` went from a
file-less result to 2854 bytes, and `labours -m hotspot-risk` renders a 43 kB SVG where it
previously reported "no hotspot risk files found". Reverting the truncation makes
`TestHotspotRiskMergeResultsOnZeroValuedAnalysis` fail with "should have 3 item(s), but has
0", while the pre-existing test stays green throughout.

---

## B6 — `--blob-cache-max-blob-size` is fail-closed, and that is a trap ✅ FIXED

Documented in `docs/SCALING.md` as a memory bound, and it reads like one, but a
blob over the limit **aborted the run**:

```
run pipeline: BlobCache failed to consume commit:
cache new blob "bin/ewws-events" (…): blob exceeds cache size limit
```

`ewws-events` passes at the 100 MiB default and fails at 20 MiB. Any repository
that has ever committed a binary is a landmine, so the knob cannot be lowered as
a routine memory measure — which is exactly what SCALING.md's "lowered for
constrained environments" invites.

### The fix: skip-and-warn, with the placeholder typed as *binary*

The obvious skip-and-warn is a trap of its own. The map key cannot simply be dropped —
almost every consumer indexes `map[plumbing.Hash]*CachedBlob` and dereferences without a
nil check (`renames.go`, `line_stats.go`, `diff.go`, `languages.go`, `line_history.go`;
only `leaves/shotness.go` nil-checks). And the placeholder that already exists for missing
submodules is **empty**, which makes the file read as *text with zero lines*, so every one
of its lines is charged as a deletion. That is exactly what SCALING.md was right to refuse
("does not substitute empty content and silently alter downstream metrics").

The way out is that nothing in the tree sniffs `Data` to decide whether a file is text —
every consumer asks `CachedBlob.CountLines()` and tests `errors.Is(err, ErrBinary)`. So an
oversized blob can be recorded as **binary**, routing it down the path a real 200 MB binary
already takes: excluded from line counts, diffs, language detection and rename detection,
with no fabricated deletions and no nil-safety audit. `CachedBlob` gains `Oversized bool`
(plus `OriginalSize` for the diagnostic) and `CountLines` short-circuits on it.

Two details are load-bearing:

- The placeholder's `Size` stays **0**. `renames.go` classifies by `blob.Size` and
  `binaryBlobsAreClose` normalises by `Min64(size1, size2)` — keeping the real, huge size
  would make a zero-byte payload score ~100 % similar to any real file and manufacture
  bogus renames. At `0 < RenameAnalysisMinimumSize (32)` it lands in the `small` bucket and
  is excluded outright. This also rules out a self-describing payload string.
- `--blob-cache-max-commit-size` gets the same treatment under the same flag.
  `commitBlobBudget.reserve` already refuses *without consuming*, so an overflowing blob is
  placeholdered while smaller later blobs still fit, and change order is tree-diff order,
  which is deterministic. The asymmetry is documented: per-blob skipping is a property of
  the blob, per-commit skipping is a property of the blob *and* its position in the commit.

Policy: skip-and-warn is the **default**; `--fail-on-oversized-blobs` restores the abort for
callers who need completeness over completion. One warning names the first offender in
full, the rest are counted and flushed by a new `BlobCache.Dispose()`.

**Two latent bugs had to be fixed first**, both of which the warning path would have
detonated: `Fork` did not copy the logger, so a clone's `l` was a nil interface and
`l.Errorf` would have panicked on *any* error in a forked branch; and `Initialize`
unconditionally clobbered a `Configure`-installed logger, so an embedder's logger never saw
anything.

Also fixed: `leaves/couples.go`'s `fileLineCounts` was a second fail-closed site, hard-wiring
the default limit and turning an unreadable blob into a `Critical` that aborted `Finalize`.
It now warns and counts zero lines, matching the `CountLines` error it already swallowed.

**Measured** on `/mnt/projekte/Code/MeKo/ewws-events`, which aborted before, now `exit 0`:

```
[WARN] blob "bin/ewws-events" (d81810d8…, 31412744 bytes) exceeds --blob-cache-max-blob-size;
       it was not read and is recorded as binary content, so it is excluded from line counts,
       diffs, language detection and rename detection. The run continues; pass
       --fail-on-oversized-blobs to abort instead.
[WARN] 2 blobs (62825488 bytes in total) exceeded the configured blob cache limits and were
       recorded as binary content; the first one was reported above.
```

Tests: `internal/plumbing/blob_cache_oversize_test.go` (skip-as-binary, strict abort, budget
skip, a skipped blob leaving budget for a later smaller one, warn-once plus one `Dispose`
summary, fork sharing the report), downstream semantics in `languages_test.go` and
`line_stats_test.go`, `TestOversizedBlobPolicy` in `test/e2e`, and
`internal/linehistory/line_history_oversized_blob_test.go` — which carries the test that
justifies the whole design: a text→placeholder→text round trip keeps the file's identity,
and its companion shows the *empty* dummy blob would instead trip the source integrity
check.

SCALING.md is rewritten accordingly, including the honest cost: a huge *text* file is now
counted as binary rather than text. Its content was never held, so it cannot be diffed —
and the limits are now genuinely safe to lower as a routine memory measure, which is what
the docs always implied.

---

## B7 — `--lines-hibernation-disk` panics during serialization ✅ FIXED

```
panic: serialization requires the hibernated state
  internal/rbtree/rbtree.go:177
```

Hit on `mekorp-backend` with
`--lines-hibernation-threshold=200000 --lines-hibernation-disk`. Did not
reproduce on `ewws-auth` with the same flags, so it needs specific state.

### The `Fork`/`Merge` race hypothesis was wrong

`Hibernate()` and `Serialize` are consecutive statements in one goroutine; plan steps run
strictly sequentially, and forked clones each own a distinct `*Allocator` via
`fileAllocator.Clone()`. There is nothing to race with.

The actual cause is that `Allocator.Hibernate()` has **three no-op early returns that leave
`storage != nil`**, and `storage` is set to nil only on the success path:

- `len(storage) < HibernationThreshold` — and `NewAllocator` sets `storage: []node{}`, so
  this fires for **every branch smaller than the threshold**. With
  `--lines-hibernation-threshold=200000` that is most of them. This is the reported trigger,
  and it explains the "needs specific state" impression: hibernation steps only exist when
  `--hibernation-distance > 0`, and which branches are small enough is repository-shaped.
- an empty allocator (`hibernatedStorageLen == 0`);
- `deinterleave` reporting empty, which leaves `storage[:0]` — non-nil, zero length.

Note `--preset large-repo` turns on both `lines-hibernation-threshold=200000` and
`lines-hibernation-disk`, so adding `--hibernation-distance` to it walks straight in.

### Fix

- `Allocator.Hibernated() bool` exposes the state, which was otherwise unobservable outside
  the package (`Size() == 0` cannot distinguish the three cases from a real hibernation).
- `LineHistoryAnalyser.Hibernate` enters the disk branch only when the allocator genuinely
  hibernated. This also stops a temp-file leak: the file was created *before* serializing,
  so an unguarded skip would have left one behind per skipped hibernation.
- Both `panic`s became returned errors (`ErrAllocatorNotHibernated`) — `Serialize` and
  `Deserialize` already return `error`, and PLAN.md called the panic the wrong failure mode.
- Found alongside: `Fork`'s `clone := *analyser` copied `hibernatedFileName` into every
  clone and never cleared it, unlike every other per-branch field. Parent and clones would
  have shared one temp file, and the first `Boot()` removes it. Unreachable under the
  current plan ordering, but it was an unguarded invariant sitting next to the bug.

Tests: `TestLinesHibernateBelowThresholdSkipsDisk` and `TestLinesForkClearsHibernatedFileName`
in `internal/linehistory`, plus `TestAllocatorHibernatedEarlyReturns` and the converted
error assertions in `internal/rbtree`. Verified by reverting: with the guard removed and the
panic restored, the new test reproduces the original `panic: serialization requires the
hibernated state` verbatim.

**Measured** on `mekorp-backend`: the serialization failure is gone. The run then hits an
unrelated pre-existing panic — see B11.

---

## B8 — person burndown aborted on contributors with no activity ✅ FIXED

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

Committed in `c04d08c`. **The regression test is now in place** ✅ —
`internal/render/modes/burndownPerson_test.go` covers the fan-out skip itself, not just
`compactPersonBurndown`: an idle person alongside an active one returns nil and renders
only the active person's file; an all-idle set still returns `errNoPersonActivity`; and
`--quiet` suppresses the skip notice. The stub reader takes the legacy path (by returning
`readers.ErrAnalysisMissing` from `GetProjectBurndownWithHeader`), which needs no header
fixture. Verified by reverting the skip branch — two of the three cases then fail with
"person burndown has no contributor activity", which is the original bug.

---

## B9 — YAML burndown serialization panics on people with no interactions ✅ FIXED

Found while writing B2's tests. `writeBurndownPeople` prints `result.PeopleMatrix`
unconditionally, and `yaml.PrintMatrix` indexes `matrix[len(matrix)-1]` — so a result with
`PeopleHistories` but a nil `PeopleMatrix` panicked with `index out of range [-1]`.
`finalizePeopleMatrix` returns nil whenever `analyser.matrix` is empty, which is reachable
with `--burndown-people` on a history that records no author interactions at all.

The guard went into `PrintMatrix` itself rather than the call site: `matrixColumnWidth` and
`matrixValueBounds` already tolerate an empty matrix, and there are four other unguarded
callers with the same shape (project, files, per-person history, repositories). An empty
matrix now emits its block header and nothing else, which keeps the `people_interaction`
key present — `writeBurndownPeople` prints that header itself before calling `PrintMatrix`
with an empty name, and the reader tolerates an empty body (`parseBurndownMatrix` returns an
empty matrix on unparseable input). `LegacyBurndown` needed no separate change; it delegates
to the same `serializeText`.

### The `--pb` path was **not** unaffected — a second panic, also fixed

The original filing said the protobuf path was safe. It is not, for the neighbouring case: a
non-nil **zero-row** `PeopleMatrix` passes the serializers' `!= nil` guard and reaches
`pb.DenseToCompressedSparseRowMatrix`, which dereferences `matrix[0]` and panics with
`index out of range [0] with length 0`. It is reachable by deserializing a payload whose
`PeopleInteraction` declares `NumberOfRows == 0` and re-serializing with `--pb`.

Guarded in `DenseToCompressedSparseRowMatrix` itself, for the same reason as `PrintMatrix` —
it covers every caller at once, and the `!= nil` guards at the call sites are the ones that
were wrong. Tests: `TestPrintMatrixEmpty` (`internal/yaml`),
`TestDenseToCompressedSparseRowMatrix{Empty,KeepsNonEmptyBehaviour}` (`internal/pb`),
`TestBurndownSerializeEmptyPeopleMatrix` and
`TestBurndownDeserializeEmptyPeopleMatrixSerializes` (`leaves`), plus the legacy equivalent.
Every one of them panics against the unfixed code.

---

## B10 — the execution plan was non-deterministic, and a merge could be resolved against the wrong branch ✅ FIXED

**The most consequential defect found so far, and it was not on the original list.**

Running the same binary on the same repository twice produced different results, and
sometimes a hard abort:

```
run pipeline: LineHistory failed to consume commit:
  source line history integrity check failed for CHANGELOG.md: 267 != 253
```

`MKTools` (308 commits) failed on 2 of 6 identical runs; `mekorp-webclient` failed at
commit #1161 in one run and reached #1828 before failing in another. Every failing run
died at the same commit with the same numbers — the drift is deterministic once it
happens, only _whether_ it happens was not.

Ruled out: `--diff-timeout` and `--renames-timeout` (both wall-clock bounded, both
irrelevant — 2/6 failures with either raised to 10 minutes), and goroutine scheduling
(`GOMAXPROCS=1` still gave 3/6).

### Root cause

`--dump-plan` over six runs differs in exactly one line:

```
C 1 b07c5ef6…          <- the merge commit is consumed on branch 1
M [1 14] b07c5ef6…     <- 4 runs: correct
M [14 1] b07c5ef6…     <- 2 runs: branch 14 leads
```

`planBuilder.mergeBranches` (`internal/core/forks.go`) built the merge action's branch
list by iterating `builder.parents[commit.Hash]`, **a map**. Go randomizes that, so the
plan — and therefore the entire analysis — differed from run to run.

Worse than the randomness: `LineHistoryAnalyser.Merge` treats `items[0]` as the branch
which consumed the merge commit and lets it decide which paths exist in the merged tree.
`mergeBranches` only guaranteed that in the `minBranch` case; when no parent qualified
(all parents having more than one child), `items[0]` was whichever parent the map yielded
first. **A merge could be resolved against a branch that never saw the commit.** The tree
then disagreed with git, and the integrity check surfaced it 130+ commits later.

### Fix (`5937149`)

- Walk the parent set in sorted hash order (`sortedHashSet`).
- Promote the consuming branch to `items[0]` in **every** case, not only when a
  `minBranch` was found (`promoteMergeBranch`).

Tests: `TestPrepareRunPlanIsDeterministic` and `TestMergeActionLeadsWithTheConsumingBranch`
in `internal/core/forks_test.go`. Both fail without the fix.

### Measured

- The plan is now byte-identical across 8 runs, and equals the previously-correct variant.
- `MKTools` and `mw_prod_planner`: 0/8 failures (were 2/6 and 1/3). Repeated YAML output
  is identical except for `run_time`.
- Every repository that hit the integrity check now completes, **including
  `mekorp-webclient` (7 684 commits) and `mekorp-backend`** — the two PLAN.md opens by
  naming as the most important.
- It also removes real accounting damage: `render-pdf`'s negative burndown cells drop from
  208 to 158 (person: 155 → 105; project: 53 unchanged).

### What this means for the rest of the plan

Every measurement in B1, B1b, B1c and B2 was taken while the plan could silently vary.
The tables are still directionally right — the effects are far larger than this jitter —
but any number close to a threshold should be re-measured before it is trusted.

**Affected in the corpus:** every repository with merges is affected in principle. It was
_observed_ to abort on `MKTools`, `mw_prod_planner` and `mekorp-webclient`.

---

## B11 — a hibernated burndown analyser consumes a commit ✅ FIXED

Not filed originally; found while fixing B7, on `mekorp-backend`.

```
panic: assignment to entry in nil map
  leaves/burndown_shared.go:350  sparseHistory.updateDelta
  ← BurndownAnalysis.updateGlobal ← consumeLineHistory
```

Line 350 is `p[curTick] = currentHistory`, so the **receiver map itself is nil** — this is
not a nil inner `deltas`. `BurndownAnalysis.Hibernate` sets `globalHistory`,
`fileHistories`, `deletedFileHistories`, `peopleHistories` and `matrix` to nil after
compressing them, and `Boot` restores them. So a burndown analyser consumed a commit while
hibernated: either a `Boot` that never ran, or one whose branch went on being used after
the hibernate step.

Reproduces with plain `--hibernation-distance=100 --lines-hibernation-threshold=200000`,
**without** `--lines-hibernation-disk`, and not at all without hibernation — so it is a
hibernate/boot *scheduling* problem, independent of B7's allocator serialization.

### The cause was not in `forks.go` after all

The filing suspected `insertHibernateBoot` / `hibernationAddons`, which is why this was
parked next to B1d. Those are correct: the hibernate/boot pairs they emit are properly
bracketed and non-overlapping *per branch index*.

The fault is one layer down, in `changeHibernation`
(`internal/core/pipeline_execution.go`), which hibernates by walking
`state.branches[branch]` and calling `Hibernate()` on everything in it. That is only sound
if each branch owns its instances — and `BurndownAnalysis.Fork` uses
`core.ForkSamePipelineItem`, handing **the same pointer** to every branch, because its
history is global and its `Merge` is a deliberate no-op. A fork counts as a use of both
sides, so the plan routinely hibernates the idle side and commits on its sibling in the very
next step:

```
267: F [2 9]
268: H 2      <- nils the shared analyser's five maps
269: C 9 eaa… <- same pointer, commits, panics
398: B 2
```

The `0x10c, 0x9` arguments in the panic trace are plan index 268 / branch 9 — exactly that
pair. Note the quieter half: even without the crash, `B 2` would `restoreBurndownState` the
snapshot taken before the fork and silently discard everything branch 9 accumulated. That is
why the defensive `make(map)` really would have been the wrong fix — it converts a crash into
silent data loss.

### The fix

Hibernation state is now tracked **per item instance** as well as per branch index: an
instance sleeps only when every live branch holding it is asleep, and wakes on the first
branch to need it again, which also keeps `Hibernate`/`Boot` calls balanced. Items that clone
per branch keep hibernating exactly as before. A branch-shared item now never hibernates in
practice — the honest outcome, since it has no per-branch state to swap out.
`BurndownAnalysis.Hibernate` is also idempotent now, as the interface has always promised; a
second call used to encode the nil state over the good snapshot.

**Measured.** The repro exits 0, and on `mekorp-backend` the hibernated run's output is
byte-identical to the non-hibernated one — hibernation is transparent, which it demonstrably
was not before.

### The exposure survey asked for

| item | `Fork` | exposed |
| --- | --- | --- |
| `BurndownAnalysis` | `ForkSamePipelineItem` | **yes — this was the crash** |
| `LegacyBurndownAnalysis` | real per-branch clone | no |
| `LineHistoryAnalyser` | real per-branch clone | no (and it panics loudly on the same misuse) |

Nothing else in the tree implements `HibernateablePipelineItem`; `CodeChurn` does not.

---

## B12 — burndown output is not reproducible run to run ✅ FIXED

Not filed originally; found while seeding the corpus baseline, and confirmed independently.
It outranked the remaining accounting work because it invalidated the measurement method
those items depend on.

Same binary, same clean checkout, same clone, three runs of `mekorp-backend` with
`--burndown --burndown-people --granularity=30 --sampling=30`, with `run_time` and the
timestamps filtered out:

```
9adb56b06f38a922f00a241283fb841a
467bbc2a1df61426b8d417d5ddfb9338
31d95c35fff8e9293ec4b20213e94b01
```

It is the **project matrix itself** that moves, not just metadata — 28 differing rows
between two runs of `meko-etl-tool`. Across three corpus passes, `mekorp-webclient` held
its cell counts (1812/56) while its mass drifted 21 862 418 → 21 878 822 and worst cell
−302 568 → −302 749; `mekorp-backend` drifted in the **counts** too (1992/73, 1992/73,
2010/74).

### This is *not* B10 regressing

`--dump-plan` is byte-identical across three runs (5435 lines). B10's `sortedHashSet` /
`promoteMergeBranch` fix holds; the divergence is downstream of plan construction, in the
analysis itself.

### Hypotheses tested and ruled out

- **Wall-clock timeouts.** Raising both `--diff-timeout` and `--renames-timeout` to
  600 000 ms made it **worse**, not better: 362 differing rows against 28 at the defaults.
  Consistent with more work being done and more opportunities to diverge, not with a
  deadline being the cause.
- **Concurrency alone.** `GOMAXPROCS=1` shrinks the drift from 28 rows to 4 but does **not**
  remove it. So parallelism amplifies the effect without being its only source.

The guess recorded here — a map iteration order deciding *which* pairing is made — was the
right shape and the wrong mechanism. There is no map involved.

### The cause: rename matching kept whichever of two different answers arrived first

`matchSimilarRenames` (`internal/plumbing/renames.go`) ran **two non-equivalent** greedy
matchers concurrently and adopted whichever goroutine delivered first, cancelling its peer:

```go
first := <-results   // :434
cancelWorkers()      // :436
second := <-results  // :438 — discarded
…
return first.matches, first.added, first.deleted   // :406
```

`matchDeletedBlobs` scans the deleted side and greedily claims the best-named addition;
`matchAddedBlobs` scans the other way. Both remove claimed blobs as they go, so the two
directions produce genuinely different pairings whenever a blob has more than one plausible
partner. The Go scheduler picked the winner. A different rename-vs-(delete + add)
classification moves lines between age bands and people — which is exactly why totals moved
and not merely their order.

Inherited verbatim from upstream, where it was a `select` over the two — and Go resolves a
ready `select` uniformly at random.

The bisection that pinned it: everything is bit-stable at `--M=100`, which forces
`sizesAreClose` to demand exactly equal sizes so both workers converge on the same
(essentially empty) fuzzy-rename set. Every threshold that admits fuzzy renames was
unstable, `GOMAXPROCS` and the timeouts notwithstanding.

### The fix, in three parts

1. **The race is gone.** The direction is now derived from the data — iterate whichever of
   {deleted, added} is smaller, ties to the deleted side. That is the cheaper scan, which is
   what the race was really approximating, and it halves the CPU rename matching costs.
2. **The two greedy tie-breaks are total orders now.** `sortableBlob.Less` compared size
   alone under a non-stable sort and `sortRenameCandidates` compared Levenshtein distance
   alone; both decide which partner is tried first, so an arbitrary order leaked into the
   result. Same class of defect as `sortableChange.Less` in `77171d5`.
3. **Deadline truncation is no longer silent.** The per-diff bound is no longer derived from
   the *remaining* commit budget — which made a pair's verdict depend on how long earlier
   pairs took — and both `RenameAnalysis` and `FileDiff` now warn once and summarise at
   `Dispose` when a deadline actually cut work short, saying plainly that the run is not
   reproducible. Flag defaults and meanings are unchanged.

**Measured.** `meko-etl-tool` and `mekorp-backend` are byte-identical across three runs at
the defaults, at `--M 60`, and under `GOMAXPROCS=1` and `8`. Every one of those
configurations produced three distinct hashes before.

### Consequence for B1c — resolved

`test/corpus` now baselines **all thirteen** repositories; `mekorp-backend` and
`mekorp-webclient` were excluded solely because of this. A single-run measurement is
trustworthy again, so B1c's table can be re-measured and believed.

One caveat survives, and the binary says so out loud: `--diff-timeout` and
`--renames-timeout` are wall-clock budgets, and hitting one still yields a different (not
wrong, but different) answer. On `mekorp-backend` at the defaults, 8 file diffs do hit the
1 s limit. Check stderr for `not reproducible` before trusting a marginal number.

---

## Suggested order

- [x] **0. B1, B1b.** Done — merge-resolution deltas are delivered and merge-commit
      deletions are accounted for; both covered by tests. Line totals now track the
      `082bf15` baseline within 0.5 %. They must be read together: B1 alone overshoots,
      B1b alone would deepen the undercount.
- [x] **1. B2's demotion.** Done — negative balances warn once instead of aborting, behind
      `--strict-burndown-balances`. Every burndown-scope failure in the corpus now completes.
  - [x] **B2's actual decision is (a): the accounting is wrong.** Measured over five
        repositories, not one negative cell recovers by the final sampled row, so branch
        divergence explains none of them. The audit now separates residual from transient
        cells and reports both. Note that the old binary's clamped output is _not_ a
        correctness baseline to compare against.
- [x] **2. B4's `--only` fix.** Done — `--only` filters before the merge, and an unmergeable
      analysis is skipped with a report instead of taking the combine down. All combined charts
      are unblocked.
  - [ ] `RefactoringProxy`'s missing `ResultMergeablePipelineItem` remains, and nothing fails
        on it any more.
- [x] **3. B3.** Done — the underflow is a structural consequence of divergent branches sharing
      one accumulator, not an accounting slip. Totals stay signed; only the snapshots handed to
      the metrics clamp. `--bus-factor` and `--ownership-concentration` complete again. Its trace pointed
      at (b) for B2; that reading has since been refuted — see B2's "The decision: (a)".
- [x] **4. B10.** Done — the execution plan no longer depends on map iteration order, and a merge
      action now always leads with the branch that consumed the merge commit. This was not on the
      original list and outranks everything that was: it made results irreproducible and
      intermittently aborted whole runs. **Re-measure anything marginal in B1/B1b/B1c/B2 — those
      numbers were taken while the plan could vary.**
- [x] **5. B2's decision.** Done — **(a)**. The residual/transient split is measured (not one
      negative cell recovers, across five repositories) and now reported by the audit. The
      accounting defect itself moves to B1c, whose root cause is traced to the individual
      commits.
- [x] **6. The corpus regression test.** Done — `test/corpus`, baseline-relative rather than
      "no negatives" (which could not pass while B1c's residue exists). Seeded across all 13
      repositories, all exiting 0. Use it to judge attempt 2 at B1c: re-seed only after a
      change is understood, never to make a red run green.
- [ ] **7. B1c's residue.** Three attempts made and reverted — read "Attempt 1", "Attempt 2" and
      "Attempt 3" in B1c before starting a fourth, and expect the answer to be that a fourth of the
      same kind is not worth making. Between them they rule out the whole family: attempt 2 showed
      the tree is not a record of what was accumulated (the derivation matched the real accumulator
      in 29% of corrections), and attempt 3 showed `mergeLineValues` pairs lines by position, so it
      cannot tell a duplicate from a coincidence — booking out either side regresses the corpus
      gate, and booking out both regresses it twice as far. **Neither the merged contents nor the
      merge itself can supply the correction.** What is left is carrying line identity across the
      fork, which is a change to what `File` stores; decide whether B1c is worth that before
      starting. File identity at the file level needs resolving too: all accounting keys on
      `FileId` while B1d made file merging match on path.
      Both of its neighbours are fixed — the `sortableChange.Less`
      comparator (B1c's rename half) and the merge replay (B1d) — and B1d turned out to share
      the mechanism, since dropping a parent from a file merge is what left the marks unresolved.
      **Re-measured against a pre-B1d build: B1d did not absorb it** (table in B1c). Cell counts
      fell, but negative mass rose in two repositories, because correct attribution makes the
      double-removal more visible rather than less. What is left is the genuine double-removal
      across sibling branches. `mekorp-backend` is the case to drive from: it holds 96% of the
      remaining negative mass, and it is also the one repository whose project matrix B1d moved
      away from the git ground truth.
- [x] **8. B8's regression test.** Done — the fan-out skip is pinned, not just
      `compactPersonBurndown`.
- [x] **9. B5, B6, B7, B9 — and B4's rest.** All done. Two of them were larger than this line
      assumed: B6 was **not** documentation only (it needed a binary-typed placeholder, a new
      opt-out flag, and two latent `Fork`/`Initialize` logger bugs fixed first), and B9's
      "one-line guard" turned out to be two guards — the `--pb` path was also reachable,
      contrary to the original filing.
- [x] **10. B12.** Done — rename matching raced two non-equivalent greedy matchers and kept
      whichever goroutine finished first, so the Go scheduler chose the rename pairing. The
      direction is now derived from the data, the two greedy tie-breaks are total orders, and
      the remaining wall-clock truncation announces itself instead of silently changing the
      numbers. `test/corpus` gates all thirteen repositories now, including the two large
      ones, so **B1c's table can be re-measured and believed**.
- [x] **11. B11.** Done — and the filing pointed at the wrong file. `insertHibernateBoot` and
      `hibernationAddons` are correct; `changeHibernation` hibernated per branch index while
      `ForkSamePipelineItem` shares one instance across branches, so a fork's idle side put
      its sibling's analyser to sleep mid-run. Hibernation is now tracked per instance.
      Nothing in `internal/core/forks.go` had to change.

## Regression coverage worth adding

None of B1–B7 were caught by the existing suite, and all of them reproduce on
ordinary repositories. The gap is that the tests use synthetic fixtures with
clean histories.

- [x] An opt-in corpus test — **built**, as `test/corpus` (`just test-corpus`, gated on
      `HERCULES_CORPUS_DIR`, skipping cleanly when unset so `go test ./...` stays green).

      **It asserts no _regression_ against `test/corpus/baseline.json`, not the absence of
      negatives.** The original spec — "assert no negative values in any serialized matrix"
      — cannot pass while B1c's residue exists, so as written the suite would have been red
      from birth and ignored. Baseline-relative is also the shape the remaining work
      actually needs: it makes a real improvement distinguishable from a new leak, which is
      the reason this was promoted to a prerequisite in the first place.

      Per repository it records exit code, negative cell count, negative mass, worst cell,
      and the **residual** count (cells whose age band is still negative in the final
      sampled row) — B2's own discriminator, and the strongest signal, since empirically
      every negative has been residual. Any metric worsening fails; improvements are logged
      so a fix is visible in the output. It reads YAML rather than `.pb`, because
      `pb.ToBurndownSparseMatrix` clamps negatives away. `UPDATE_CORPUS_BASELINE=1`
      (`just update-corpus-baseline [REPOS]`) re-seeds, preserving entries not re-measured.

      Verified to actually fail: worsening `ewws-wiki`'s entry by hand produces
      `negative cells regressed: 125, baseline 100`. A repository missing from the corpus
      directory is skipped with a log line, so a partial checkout still works.

      **Only `exit_code`, `negative_cells` and `residual_cells` gate the build.**
      `negative_mass` and `worst_cell` are recorded and reported in both directions but
      ungated, because they drifted between runs. That drift was B12 and is now fixed, so
      gating them has become defensible — left ungated for the moment only because the
      wall-clock diff timeout can still truncate a run, which the binary now warns about.

      **`people_interaction` is excluded from the metrics.** It is a signed interaction
      matrix, not a band matrix, so its negatives are correct by construction; counting them
      inflated every figure by roughly an order of magnitude. This is the main reason the
      baseline sits well below the tables in B1c/B2 — those counts almost certainly included
      it. Treat the baseline as the current truth and those tables as history.

      **Seeded across all 13 named repositories, all exiting 0**, re-seeded after B11 and
      B12 landed. `mekorp-backend` and `mekorp-webclient` were originally excluded because
      nothing about them could be gated while B12 stood; with rename matching deterministic
      they reproduce byte-for-byte and are gated like the rest. The whole corpus seeds in
      ~7 min.

      **`baseline.json` was never actually committed** until now: `.gitignore`'s blanket
      `*.json` matched it, so the file every other machine compares against did not exist
      outside the one that seeded it. Fixed with a negation, alongside the two entries
      already there for `pb.schema.json` and the render goldens.

      The file records the commit and working-tree state it was measured against. Re-seed
      after any change to burndown or merge accounting — a first attempt seeded against a
      live working tree was invalidated when a concurrent change moved `process-manager`
      from 0 to 25 103 negative mass mid-seed.

## Verification

The downstream pipeline is the acceptance test. From `MeKo/ewws-statistics`:

- [ ] `just tools-check` — confirm the 0.2.0 binaries resolve
- [ ] `./ewws-stats analyze hercules --include-file .core-repos` — today 22 processed /
      14 failed; target 38 processed / 0 failed
- [ ] `./ewws-stats combine --include-file .core-repos --include-data-exports`
- [ ] Re-enable `burndown_files`, `ownership_analyses` and `refactoring_proxy` in
      `ewws-stats.yaml` and confirm the run still completes — those three switches exist
      only because of B2/B3/B4 and should be deleted, not left as permanent configuration.
