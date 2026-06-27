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
tree-sitter refinement path on a non-trivial language set. ROADMAP Milestone 2
mentions the Linux kernel as the original target; we treat CPython as a
"similarly large history" stand-in for the same role.

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

| Config         | Flags                                          | Intent                                                       |
| -------------- | ---------------------------------------------- | ------------------------------------------------------------ |
| `quick`        | `--preset quick --burndown --pb`               | Baseline cost; analyses only `HEAD`. Validates the path.    |
| `burndown-fp`  | `--burndown --first-parent --pb`               | The default "give me a burndown" workload, first-parent only.|
| `burndown-lr`  | `--burndown --preset large-repo --pb`          | Recommended for big repos; enables hibernation + 30-day buckets. |
| `devs-fp`      | `--devs --first-parent --pb`                   | Alternate analysis path; exercises identity resolution.     |

`--pb` is used everywhere to keep YAML serialization out of the timing — the
benchmark is about the analysis cost, not the output format.

## Results: CPython (≈ 112 000 commits)

> Numbers below are from a single run on a 12th Gen Intel Core i7-1255U laptop
> (Linux 6.8). Re-runs vary by ±10 % depending on disk cache state. The intent
> is order-of-magnitude validation, not precise micro-benchmarking.

<!-- BENCHMARK_RESULTS_START -->
| Config         | Wall time   | Peak RSS  | Throughput        | Status |
| -------------- | ----------- | --------- | ----------------- | ------ |
| `quick`        | 2.1 s       | 600 MiB   | n/a (HEAD only)   | ok     |
| `burndown-fp`  | 15 min 18 s | 2.13 GiB  | ≈ 122 commits/s   | ok     |
| `burndown-lr`  | 14 min 25 s | 2.12 GiB  | ≈ 130 commits/s   | ok     |
| `devs-fp`      | 16 min 25 s | 2.11 GiB  | ≈ 114 commits/s   | ok     |
<!-- BENCHMARK_RESULTS_END -->

### Reading the table

- **Wall time** is end-to-end including git object decoding, the analysis DAG,
  and protobuf serialization. It does *not* include the optional plotting step
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
  the most recent commit. If it is *not* fast on your repo, something else is
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

This satisfies the ROADMAP Milestone 2 acceptance criterion: "a documented
command set completes without OOM and with reproducible results."

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
