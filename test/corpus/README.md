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

## Metrics

Computed over every project, per-person, per-file and per-repository burndown
band matrix in the YAML report:

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

One caveat survives, and the binary now says so out loud. `--diff-timeout` and
`--renames-timeout` are wall-clock budgets, and a run that hits one produces a
different — not wrong, but different — answer than an untruncated run. On
`mekorp-backend` at the defaults, 8 file diffs do hit the 1 s limit. If a
baseline refuses to settle, check stderr for `not reproducible` and re-measure
with those two timeouts raised.

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

The `flags` list in the baseline is checked against the flag set the suite runs.
Changing the flags invalidates every committed number and the suite says so
rather than comparing apples to oranges.
