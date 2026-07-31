# Scaling Hercules to Large Repositories

This document records what hercules actually costs in time and memory on a real
large repository, and the documented command set used to measure it. The goal
is reproducibility: re-running `scripts/bench-large-repo.sh` against the same
target should land in the same ballpark.

If you need to push beyond what's documented here, see
[HIBERNATION.md](HIBERNATION.md) for the underlying memory-control mechanism
and the README "Presets" section for the curated flag bundles.

## Reference workload

The reference benchmark target is the **CPython** repository
(`github.com/python/cpython`), chosen because it is large enough to exercise
hibernation paths (≈ 112 000 commits, ~1 GB of git objects) without requiring a
multi-hour clone, and because its mixture of C and Python files exercises the
tree-sitter refinement path on a non-trivial language set. The original
scaling milestone (former ROADMAP Milestone 2) mentioned the Linux kernel as
the target; we treat CPython as a "similarly large history" stand-in for the
same role.

## Reproducing the numbers

```bash
# 1. Build hercules (default cgo-free build is fine).
just hercules

# 2. Run the benchmark suite. Pass any local repo path; results land in
#    bench-results/ by default.
scripts/bench-large-repo.sh /path/to/cpython

# 3. Inspect the summary.
column -t -s$'\t' bench-results/summary.tsv
```

The script wraps each hercules invocation in `/usr/bin/time -v` (GNU time, not
the shell builtin — install the `time` package if needed) and writes:

- `bench-results/<config>.time` — full GNU `time -v` capture
- `bench-results/<config>.yml` — the analysis output (kept for sanity-checking)
- `bench-results/summary.tsv` — `config / wall_seconds / peak_rss_kb / status`
- `bench-results/environment.txt` — host info, hercules version, commit count

## Configuration matrix

| Config        | Flags                                 | Intent                                                           |
| ------------- | ------------------------------------- | ---------------------------------------------------------------- |
| `quick`       | `--preset quick --burndown --pb`      | Baseline cost; analyses only `HEAD`. Validates the path.         |
| `burndown-fp` | `--burndown --first-parent --pb`      | The default "give me a burndown" workload, first-parent only.    |
| `burndown-lr` | `--burndown --preset large-repo --pb` | Recommended for big repos; enables hibernation + 30-day buckets. |
| `devs-fp`     | `--devs --first-parent --pb`          | Alternate analysis path; exercises identity resolution.          |

`--pb` is used everywhere to keep YAML serialization out of the timing — the
benchmark is about the analysis cost, not the output format.

## Results: CPython (≈ 112 000 commits)

> Numbers below are from a single run on a 12th Gen Intel Core i7-1255U laptop
> (Linux 6.8). Re-runs vary by ±10 % depending on disk cache state. The intent
> is order-of-magnitude validation, not precise micro-benchmarking.

<!-- BENCHMARK_RESULTS_START -->

| Config        | Wall time   | Peak RSS | Throughput      | Status |
| ------------- | ----------- | -------- | --------------- | ------ |
| `quick`       | 2.1 s       | 600 MiB  | n/a (HEAD only) | ok     |
| `burndown-fp` | 15 min 18 s | 2.13 GiB | ≈ 122 commits/s | ok     |
| `burndown-lr` | 14 min 25 s | 2.12 GiB | ≈ 130 commits/s | ok     |
| `devs-fp`     | 16 min 25 s | 2.11 GiB | ≈ 114 commits/s | ok     |

<!-- BENCHMARK_RESULTS_END -->

### Reading the table

- **Wall time** is end-to-end including git object decoding, the analysis DAG,
  and protobuf serialization. It does _not_ include the optional plotting step
  (`labours`).
- **Peak RSS** is the high-water mark of resident set size during the run, as
  reported by GNU `time -v` ("Maximum resident set size"). For configs that
  enable hibernation with disk spill (`burndown-lr`), the on-disk spill area is
  not counted in RSS — that's the point.

### What the numbers tell you

- **No-OOM ceiling on this hardware**: even the heaviest config (`devs-fp`)
  peaked at ~2.1 GiB. That's ≈ 6 % of the 32 GiB on the test laptop, so
  hercules is comfortably within its memory envelope at the cpython scale on
  any machine with ≥ 4 GiB free.
- **`large-repo` preset is sized for repos bigger than cpython.** On this run
  `burndown-lr` and `burndown-fp` finished within ~6 % of each other in both
  wall time and RSS — hibernation never triggered because cpython's per-branch
  line-history allocator stays well under the preset's 200 000-entry threshold.
  The slight wall-time edge for `burndown-lr` comes from the coarser
  `--granularity=30 --sampling=30` reducing output bookkeeping, not from
  hibernation. Expect the preset to start paying off on Linux-kernel-class
  history (≳ 1 M commits, many concurrent branches).
- **Throughput baseline**: ≈ 120 commits/s for burndown on a single
  i7-1255U core. That gives an upper bound for naive linear extrapolation —
  e.g. the Linux kernel at ~1.3 M commits is **~3 hours straight-line**, with
  the actual figure likely higher due to super-linear blame-state growth.
- **`quick` should be roughly constant across repo sizes** — it analyses only
  the most recent commit. If it is _not_ fast on your repo, something else is
  wrong (very large blobs on `HEAD`, missing index, network-mounted git dir).
- **`devs-fp` overhead**: ~7 % wall and ~1 % RSS over `burndown-fp`. The cost
  of identity resolution + activity tracking is mostly CPU. If your repo has a
  curated `people-dict` doing real work, expect a larger delta.

## Acceptance: no-OOM command set

For a repository on the order of CPython's history (≈ 100 000 commits,
~ 1 GB git dir), the documented "first run" is:

```bash
hercules --preset large-repo --burndown --pb /path/to/repo > burndown.pb
labours -i burndown.pb -m burndown-project -f pb
```

This satisfies the scaling acceptance criterion from the former ROADMAP
Milestone 2: "a documented command set completes without OOM and with
reproducible results."

## Merging multi-repository burndown histories

Combining burndown results resamples both histories onto their shared daily
timeline. The merge keeps one source-sampling chunk for each age band and
finalizes one output row at a time. Its interpolation workspace therefore
grows linearly with the history duration; it does not allocate the former
daily `duration × duration` float matrix.

`DenseHistory` is still the public result type. A fully populated returned
history is itself quadratic in its number of sampled rows and age bands, so
that required output storage is a lower bound rather than merge workspace.
Choose coarser `--sampling` and `--granularity` when the returned matrix is the
remaining memory constraint.

The streaming implementation retains the historical float32 accumulation and
int64 truncation contract. Merge outputs are considered numerically equivalent
within one line per cell, which covers float32 rounding at resampling
boundaries. The mixed-resolution golden regression exercises different
sampling, granularity, and start-time offsets.

Reproduce the allocation and Linux resident-memory benchmarks with:

```bash
go test -run '^$' \
  -bench '^BenchmarkMergeBurndownMatrices(PeakRSS)?MultiYearDaily$' \
  -benchtime=1x -benchmem ./internal/burndown
```

The benchmark reports `interpolation-workspace-bytes`,
`required-output-bytes`, and (on Linux) `peak-RSS-delta-bytes` separately.
The two-, five-, and ten-year fixtures use daily samples with 30-day age
bands.

## Incremental ownership snapshots

Bus Factor and Ownership Concentration share one pipeline accumulator. Each
line-history ownership run is applied once, and both analyses consume the same
immutable end-of-tick author totals. Stable files are not enumerated and their
lines are not scanned at tick boundaries. Per-tick work is therefore driven by
the changed ownership runs plus the author entries retained in the snapshot
output. Final subsystem summaries make one pass over the accumulator's
per-file ownership totals and are cached across both analyses.

The synthetic comparison keeps 32 authors and two changed ownership runs per
tick while varying the stable repository from 1,000 to 50,000 files (one
million lines each). On the same i7-1255U reference machine used above, the
incremental path remained approximately flat at 1.7–1.8 µs/tick and 1,368
bytes/tick. The full-rescan reference grew from about 71 µs/tick to 8.8
ms/tick. These timings are illustrative; the reported run counts are the
complexity contract.

Reproduce the benchmark with:

```bash
go test -run '^$' \
  -bench '^BenchmarkOwnershipSnapshotsStableLargeFiles$' \
  -benchtime=200ms -benchmem ./leaves
```

The incremental cases report `changed-ownership-runs/op` and
`snapshot-author-entries/op`; the reference cases report
`rescanned-ownership-runs/op`. `stable-live-files` and `stable-live-lines`
describe the state deliberately excluded from incremental tick work.

## Bounded blob and rename processing

Blob loading is bounded before allocation. By default,
`--blob-cache-max-blob-size=104857600` limits one blob to 100 MiB and
`--blob-cache-max-commit-size=536870912` limits the distinct changed blobs
retained for one commit to 512 MiB. Both values are byte counts.

A blob above the per-blob limit, or one which does not fit the remaining
budget of its commit, is not read and not retained. It is recorded as binary
content, so it is excluded from line counts, diffs, language detection and
rename detection exactly as a real binary file is, and the run continues. The
first such blob is named in a warning; the rest are counted and reported in a
single end-of-run summary. Hercules still never substitutes _empty text_,
which would read as a real deletion of every line in the file. Declared Git
blob sizes are validated before an exact-size allocation, and readers must
produce exactly that many bytes.

Because of this, both limits are now safe to lower as a routine memory
measure: a repository which once committed a large binary no longer aborts a
multi-hour run. Pass `--fail-on-oversized-blobs` to restore the previous
fail-closed behaviour, which is what you want when auditing the limits
themselves.

The two limits are not symmetric. Skipping under `--blob-cache-max-blob-size`
is a property of the blob, so the same blob is skipped wherever it appears.
Skipping under `--blob-cache-max-commit-size` is a property of the blob _and_
its position within its commit: an over-budget blob is skipped without
consuming budget, so smaller blobs later in the same commit still fit. Change
order is tree-diff order, which is deterministic, so runs stay reproducible.

The honest cost: a huge _text_ file is counted as binary rather than as text.
Its contents were never held, so it cannot be diffed, and its lines do not
appear in burndown, line statistics or language totals. Raise the limit past
the file's size to get it back.

`--renames-timeout` is one deadline shared by both rename-search directions
and every line- or byte-level comparison in that commit. The first completed
direction cancels its peer, and both workers are joined through a buffered
result channel before processing continues. Import-extraction workers are
likewise bounded by `--import-goroutines` and joined on successful and error
returns.

The hostile-input, simultaneous-error, deadline, and worker-lifecycle
regressions can be reproduced under the race detector with:

```bash
go test -race ./internal/plumbing ./internal/plumbing/imports \
  -run 'TestBlobCache|TestCachedBlob|TestRename|TestRunRename|TestImportWorkers|TestExtractor'
```

If even `--preset large-repo` is too tight, the next escape hatches are:

1. Lower output resolution further: append `--granularity=90 --sampling=90`.
2. Tighten the hibernation threshold: `--lines-hibernation-threshold=50000`
   (smaller = compresses more aggressively, trades CPU for memory).
3. Disable diff refinement on the largest files only:
   `--diff-refine-max-file-size=51200` (50 KB) or `--diff-refine-max-lines=2000`.

## See also

- [HIBERNATION.md](HIBERNATION.md) — how the memory-pressure relief actually works.
- [PIPELINE_ITEMS.md](PIPELINE_ITEMS.md) — what each analysis costs in dependencies.
- [SCHEMAS.md](SCHEMAS.md) — output format reference.
