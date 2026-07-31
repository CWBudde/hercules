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

| Metric           | Meaning                                                     |
| ---------------- | ----------------------------------------------------------- |
| `exit_code`      | Process exit status; expected to be 0.                       |
| `negative_cells` | Count of negative cells anywhere in the sampled history.     |
| `negative_mass`  | Σ\|negative\| over those cells.                              |
| `worst_cell`     | The single most negative value.                              |
| `residual_cells` | Negative cells in the **final** sampled row.                 |
| `matrices`       | How many matrices were inspected (a shape sanity check).     |

`residual_cells` is the important one. The residual/transient split is the
honest measure of the defect: a transient negative that a later sample repairs
is a bookkeeping wobble, a residual one is a leak that never closes.

`people_interaction` is deliberately **not** inspected. It is a signed
added/removed interaction matrix rather than a band matrix, so its negatives are
correct by construction.

The suite reads YAML, not `.pb`, on purpose: `pb.ToBurndownSparseMatrix` clamps
with `max(value, 0)`, so a protobuf report can never show a negative cell.

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
