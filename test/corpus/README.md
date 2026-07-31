# Burndown corpus regression suite

An opt-in suite that replays a fixed analysis over real repositories and checks
that burndown negativity does not get **worse**.

## Why a baseline and not "no negatives"

The obvious spec — assert that no serialized matrix contains a negative value —
cannot pass today. Negative cells exist in quantity across the corpus, and a
suite that is red from birth gets ignored. So this suite compares against a
committed baseline instead: any metric that worsens fails, any metric that
improves is reported via `t.Log`. That is what makes merge-accounting work
measurable — without it there is no way to tell a real improvement from a new
leak.

## Running it

```sh
HERCULES_CORPUS_DIR=/path/to/clones just test-corpus
```

The suite skips entirely when `HERCULES_CORPUS_DIR` is unset, so `go test ./...`
stays green and fast. There are no build tags anywhere in `test/`.

A repository named in the suite but absent from `HERCULES_CORPUS_DIR` is skipped
with a log line, so a partial checkout works. A repository present in the corpus
but absent from `baseline.json` is likewise reported and skipped, never failed —
otherwise the suite would be unusable until someone ran a multi-hour seeding job.

## Two measured dimensions, two runs per repository

Each repository is analysed twice, and the two results are recorded under
separate counters in `baseline.json`.

| dimension | flags | matrices counted | baseline keys |
| --- | --- | --- | --- |
| project | `flags` (burndown, people, devs, couples, …) | `project`, `people[*]`, `repositories[*]` | `flags`, `repositories` |
| files | `file_flags` (`--burndown --burndown-files`) | `files[*]` | `file_flags`, `file_repositories` |

The per-file dimension is separate on purpose:

- **Separate counters**, so the long-standing project/people numbers keep meaning
  what they meant. Folding per-file negatives into `negative_cells` would make
  every historical figure incomparable.
- **A separate run**, because `--burndown-files` emits one band matrix per file
  that ever existed — thousands of matrices and an order of magnitude more YAML
  on the large repositories — and pairing that with `--couples`/`--devs`/
  `--burndown-people` in one process multiplies peak memory for nothing: none of
  those leaves influences the per-file matrices. The cost is a second pass per
  repository, so the suite takes roughly twice as long as before.
- The per-file run also emits a `project` matrix. It is **not** counted, or the
  first run's project matrix would be counted twice.

A repository with a `repositories` entry but no `file_repositories` entry is
measured only in the project dimension, with a log line — so a corpus baselined
before this dimension existed does not silently double in runtime.

## Metrics

The same metric set is computed per dimension, over the band matrices listed
above:

| Metric           | Meaning                                                  | Gated |
| ---------------- | -------------------------------------------------------- | ----- |
| `exit_code`      | Process exit status; expected to be 0.                    | yes   |
| `negative_cells` | Count of negative cells anywhere in the sampled history.  | yes   |
| `residual_cells` | Negative cells in the **final** sampled row.              | yes   |
| `negative_mass`  | Σ\|negative\| over the negative cells.                    | no    |
| `worst_cell`     | The single most negative value.                           | no    |
| `matrices`       | How many matrices were inspected (a shape sanity check).  | no    |

`residual_cells` is the important one. The residual/transient split is the
honest measure of the defect: a transient negative that a later sample repairs
is a bookkeeping wobble, a residual one is a leak that never closes.

Only the counting metrics fail the build. `negative_mass` and `worst_cell` drift
between runs on repositories with merges (see below), so gating them would make
the suite flaky. They are recorded and reported both ways, because a large move
in either is worth a human look. Re-gate them once burndown is deterministic.

`people_interaction` is deliberately **not** inspected. It is a signed
added/removed interaction matrix rather than a band matrix, so its negatives are
correct by construction.

The suite reads YAML, not `.pb`, on purpose: `pb.ToBurndownSparseMatrix` clamps
with `max(value, 0)`, so a protobuf report can never show a negative cell.

## All thirteen repositories are baselined (was: eleven)

`mekorp-backend` and `mekorp-webclient` were originally left out of
`baseline.json` because their output was not reproducible: the same binary, from
the same clean checkout, over the same clone produced three different `Burndown`
sections in three runs.

```
run1 md5=155f6a49899a96285665e60b6ea7891d
run2 md5=e41284579eda1f2c02549a0ee02a1752
run3 md5=fd2de28e13f50154f62b4c72fadf2072
```

That was PLAN.md B12: rename detection ran two non-equivalent greedy matchers
concurrently and kept whichever goroutine finished first, so the Go scheduler
decided which files counted as renames. Fixed; both repositories now reproduce
byte-for-byte and are gated like the rest.

## Wall-clock truncation: pinned, and fatal if it happens anyway

`--diff-timeout` and `--renames-timeout` are wall-clock budgets, and a run that
hits one produces a different — not wrong, but different — answer than an
untruncated run. `--diff-timeout` is in **milliseconds** and defaults to **1000**,
which the corpus exceeds routinely: at the default, `mekorp-webclient` measures
1447 negative cells truncated against 1413 untruncated, and two binaries with
provably identical negativity reported 1447 and 1481. That is the same order of
magnitude as the regressions this suite exists to catch, so at the default the
gate was partly measuring machine speed.

The suite therefore runs with `--diff-timeout=300000` (5 minutes per single file
diff — verified to leave `mekorp-webclient`, the slowest repository, untruncated)
in **both** flag sets, so the deadline is unreachable rather than merely generous.

Truncation is also no longer silent. hercules warns on stderr and still exits 0,
and the numbers of a truncated run look like ordinary numbers, so the suite
scans stderr for `not reproducible`. When it matches, that repository's
measurement in that dimension is declared **invalid**: the subtest fails with the
stderr tail, the numbers are not gated, and `UPDATE_CORPUS_BASELINE=1` refuses to
write them. A deadline firing on a busy machine can therefore no longer be
mistaken for a burndown regression — or be seeded into the baseline as one.

## Refreshing the baseline

```sh
# whole corpus (hours of wall clock)
HERCULES_CORPUS_DIR=/path/to/clones just update-corpus-baseline

# a subset; entries that are not re-measured are preserved
HERCULES_CORPUS_DIR=/path/to/clones just update-corpus-baseline 'render-pdf|meko-etl-tool'
```

Refresh the baseline when a burndown or merge-accounting fix legitimately
changes the numbers, and record why in the commit message. `provenance` in
`baseline.json` records the commit and `git status --short` of the tree the
numbers were measured against; numbers are only comparable against that tree.

The `flags` and `file_flags` lists in the baseline are checked against the flag
sets the suite runs. Changing either invalidates every committed number in that
dimension and the suite says so rather than comparing apples to oranges.

### The baseline must be re-seeded

Adding `--diff-timeout=300000` changed `flags`, so **every number recorded before
it is void** — including the ones quoted in `PLAN.md`. Until the corpus is
re-seeded the suite fails immediately with the flag-set mismatch; it does not
compare against the stale figures. `file_repositories` starts out empty and is
filled by the same re-seed.

```sh
HERCULES_CORPUS_DIR=/path/to/clones just update-corpus-baseline
```

Expect roughly twice the previous wall clock, since every repository is now
analysed twice.

Re-seed the **whole** corpus after a flag change. Entries recorded under the old
flags are void, so an update run that finds a changed flag list discards them
instead of preserving them — a subset re-seed would otherwise leave a baseline
half of which meant something else. (With the flags unchanged, the usual
"entries not re-measured are preserved" behaviour applies.)
