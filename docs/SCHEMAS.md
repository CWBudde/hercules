# Hercules Output Schemas (YAML + Protocol Buffers)

This document describes the **effective output schemas** currently produced by Hercules.
It is derived from live `Serialize()` implementations in `leaves/` and protobuf definitions in `internal/pb/pb.proto`.

Scope:

- YAML output (`hercules ...`)
- Protocol Buffers output (`hercules --pb ...`)
- One compact example payload per analysis target

### No native JSON output (by design)

Hercules intentionally emits only YAML and Protocol Buffers. There is no `--json` mode: the
`labours` renderer and `hercules combine` consume YAML/PB, and both formats are already
machine-readable and versioned (see [Schema Version](#schema-version)). Consumers that need JSON
can convert the YAML stream with any off-the-shelf `yaml→json` tool, or decode the PB envelope
against `internal/pb/pb.proto`. A first-class JSON export (with per-analysis JSON Schemas) is
deferred until a concrete downstream requirement appears. The recommended path, if revisited: add a
`--json` branch marshaling the per-leaf PB messages with `github.com/gogo/protobuf/jsonpb`, and
generate JSON Schemas from `internal/pb/pb.proto` so validation stays aligned with `pb.SchemaVersion`.

## Schema Version

The output schema is versioned by a single constant: `pb.SchemaVersion` in
`internal/pb/version.go` (currently **2**). It is emitted in:

- the YAML header: `hercules.version`
- the PB envelope: `Metadata.version` (also for `hercules combine` output)
- `hercules version` (the `Schema:` line) and `labours version`
- the checked-in snapshot `internal/pb/pb.schema.json` (top-level `version`)

Compatible changes (see policy below) keep the version; every breaking change
must bump it. CI enforces this via `cmd/schema-guard`
(`.github/workflows/test-schema.yaml`, locally `just check-schema`).

## Top-Level Envelope

### YAML

YAML output is a stream with:

- `hercules:` metadata block
- one top-level block per enabled analysis, keyed by `Leaf.Name()`

Example:

```yaml
hercules:
  version: 2
  hash: abcdef0
  repository: /path/to/repo
  begin_unix_time: 1700000000
  end_unix_time: 1700100000
  commits: 123
  run_time: 456
Burndown:
  granularity: 30
  sampling: 30
  tick_size: 86400
  "project": |-
    10 9 8
    10 9 8
```

### Protocol Buffers

Binary output uses envelope `AnalysisResults`:

- `header` (`Metadata`)
- `contents` map where:
  - key = analysis `Name()` (e.g. `"Burndown"`, `"Devs"`)
  - value = serialized bytes for that analysis payload

See `internal/pb/pb.proto` for envelope/messages.

## Analysis Key Map

| CLI flag                    | YAML key / `Name()`      | PB payload type                              |
| --------------------------- | ------------------------ | -------------------------------------------- |
| `--burndown`                | `Burndown`               | `BurndownAnalysisResults`                    |
| `--legacy-burndown`         | `LegacyBurndown`         | `BurndownAnalysisResults`                    |
| `--bus-factor`              | `BusFactor`              | `BusFactorAnalysisResults`                   |
| `--codechurn`               | `CodeChurn`              | `CodeChurnAnalysisResults`                   |
| `--commits-stat`            | `CommitsStat`            | `CommitsAnalysisResults`                     |
| `--couples`                 | `Couples`                | `CouplesAnalysisResults`                     |
| `--devs`                    | `Devs`                   | `DevsAnalysisResults`                        |
| `--dump-uast-changes`       | `UASTChangesSaver`       | JSON bytes payload (not a protobuf message)  |
| `--file-history`            | `FileHistoryAnalysis`    | `FileHistoryResultMessage`                   |
| `--hotspot-risk`            | `HotspotRisk`            | `HotspotRiskResults`                         |
| `--imports-per-dev`         | `ImportsPerDeveloper`    | `ImportsPerDeveloperResults`                 |
| `--knowledge-diffusion`     | `KnowledgeDiffusion`     | `KnowledgeDiffusionResults`                  |
| `--linedump`                | `LineDumper`             | none (binary not supported)                  |
| `--onboarding`              | `Onboarding`             | `OnboardingResults`                          |
| `--ownership-concentration` | `OwnershipConcentration` | `OwnershipConcentrationResults`              |
| `--refactoring-proxy`       | `RefactoringProxy`       | `RefactoringProxyResults`                    |
| `--sentiment`               | `Sentiment`              | `CommentSentimentResults` (tensorflow build) |
| `--shotness`                | `Shotness`               | `ShotnessAnalysisResults`                    |
| `--temporal-activity`       | `TemporalActivity`       | `TemporalActivityResults`                    |
| `--typos-dataset`           | `TyposDataset`           | `TyposDataset`                               |

### Filtered repository view semantics

Repository filters are applied independently to the old and new side of every
tree change. This includes vendor detection, blacklist and whitelist path
filters, and language detection. A path that changes from included to excluded
is presented to analyses as a deletion (`From` only); excluded to included is
an insertion (`To` only); included on both sides remains a modification; and a
change excluded on both sides is omitted.

Language-detection failures are returned as errors. The tree-diff stage advances
its previous-tree and commit state only after filtering succeeds, so a failed
commit cannot become the baseline for a later diff. The same filters apply to
the initial tree.

## Schema Details + Examples

### Burndown (`--burndown`)

YAML fields:

- `granularity` int
- `sampling` int
- `tick_size` int seconds
- `"project"` multiline matrix
- optional: `files`, `files_ownership`, `people_sequence`, `people`, `people_interaction`, `repository_sequence`, `repositories`

PB: `BurndownAnalysisResults`

All project, file, person, and repository balances must be non-negative.
Hercules validates them at finalization, merge, and serialization boundaries.
Invalid internal state returns a diagnostic containing the scope, tick, age
band, value, and operation; YAML serialization does not clamp it to zero.

Example:

```yaml
Burndown:
  granularity: 30
  sampling: 30
  tick_size: 86400
  "project": |-
    100 80 60
    110 85 61
```

### Legacy Burndown (`--legacy-burndown`)

Same schema family as Burndown; YAML/PB shapes are compatible with `BurndownAnalysisResults`.

Example:

```yaml
LegacyBurndown:
  granularity: 30
  sampling: 30
  tick_size: 86400
  "project": |-
    50 45
    52 46
```

### Bus Factor (`--bus-factor`)

YAML fields:

- `bus_factor.threshold` float
- `bus_factor.per_tick.<tick> = {bus_factor, total_lines}`
- optional `bus_factor.per_subsystem.<path> = int`
- `bus_factor.people` list
- `bus_factor.tick_size` seconds

PB: `BusFactorAnalysisResults`

Each occupied tick is an end-of-tick snapshot: it contains all commits from that tick and no
commit from a later tick. The last occupied tick is always emitted; a repository with no commits
has no snapshots. For a threshold `t` and `L` living lines, the required coverage is exactly
`ceil(t * L)` lines using the configured decimal threshold, so small repositories do not lose a
fractional line to truncation. Final subsystem values use the same ownership totals as the final
global snapshot.

Example:

```yaml
BusFactor:
  bus_factor:
    threshold: 0.80
    per_tick:
      0: { bus_factor: 2, total_lines: 1000 }
    people:
      - "alice|alice@example.com"
    tick_size: 86400
```

### Code Churn (`--codechurn`)

Code Churn is experimental. It reports lifetime and current ownership counts together with two
bounded familiarity estimates for every author/file pair:

- `inserted_lines` is the lifetime number of lines inserted by the author.
- `owned_lines` is the number of the author's lines still present at the end of the analysis.
- `awareness` is a short-term familiarity score in `[0, 1]`.
- `memorability` is a longer-term familiarity score in `[0, 1]`.

Both scores use ticks as their time unit. If `dt` ticks elapsed, a score `x` first decays as
`x_decay = x * 2^(-dt / H)`, where `H = 30` ticks for awareness and `H = 180` ticks for
memorability. Thus their half-life durations are respectively `30 * tick_size` and
`180 * tick_size`.

For each line-history change, let `L` be the author's owned lines before the change, `I` inserted
lines, `S` self-deleted lines, and `O` lines deleted by another author. The reinforcement and
disruption shares are:

```text
r = min(1, (I + S) / max(1, L + I))
d = min(1, O / max(1, L))
score_next = clamp((x_decay + (1 - x_decay) * r) * (1 - d), 0, 1)
```

Insertions and self-deletions are intentional direct interactions with the author's own code, so
they reinforce both scores. A deletion by another author does not reinforce familiarity; it
reduces both scores by the fraction of owned lines removed. Scores are finally decayed to the
latest line-history tick observed anywhere in the repository, including file-deletion events.
Changes sharing a tick are applied in emitted line-history order, without decay between them; they
are not batch-aggregated. `inserted_lines` and `owned_lines` are signed 32-bit PB counters, and the
analysis returns an error instead of wrapping if an individual or cumulative update exceeds those
bounds. `granularity` and `sampling` are retained as compatibility metadata and do not change these
equations.

YAML fields:

- `people` list of developer identities
- `tick_size`, `granularity`, `sampling`
- `authors` map keyed by author index
  - `name`
  - `files` map keyed by file path
    - `inserted_lines`, `owned_lines`, `memorability`, `awareness`
    - `delete_history`, keyed by deleting author, then deletion tick, then original line tick; the
      leaf values are negative deleted-line deltas

PB: `CodeChurnAnalysisResults`

Example:

```yaml
CodeChurn:
  people:
    - "alice"
  tick_size: 86400
  granularity: 30
  sampling: 30
  authors:
    0:
      name: "alice"
      files:
        "main.go":
          inserted_lines: 10
          owned_lines: 7
          memorability: 0.75
          awareness: 0.80
          delete_history:
            1:
              30:
                10: -3
```

### Commits Stat (`--commits-stat`)

YAML fields:

- `commits` list with entries:
  - `hash`, `when` (unix), `author` (int index), `files`
  - file item: `name`, `language`, `stat: [added, changed, removed]`
- `people` list (author index)

PB: `CommitsAnalysisResults`

Example:

```yaml
CommitsStat:
  commits:
    - hash: deadbeef
      when: 1700000000
      author: 0
      files:
        - name: "main.go"
          language: "Go"
          stat: [10, 2, 1]
  people:
    - "alice|alice@example.com"
```

### Couples (`--couples`)

YAML fields:

- `files_coocc.index` list
- `files_coocc.lines` list
- `files_coocc.matrix` list of sparse row maps `{col: value}`
- `people_coocc.index` list
- `people_coocc.matrix` list of sparse row maps
- `people_coocc.author_files` list of `author -> [files]`

PB: `CouplesAnalysisResults`

When partial results are merged, file and developer identities are translated
through their respective dictionaries before matrix cells and line totals are
added. Valid file and developer indices are checked independently. This makes
the identity mapping associative: changing how valid partial results are
grouped does not change the merged result.

Example:

```yaml
Couples:
  files_coocc:
    index:
      - "a.go"
      - "b.go"
    lines:
      - 10
      - 20
    matrix:
      - { 1: 3 }
      - { 0: 3 }
  people_coocc:
    index:
      - "alice"
      - "bob"
    matrix:
      - { 1: 2 }
      - { 0: 2 }
    author_files:
      - "alice":
          - "a.go"
```

### Devs (`--devs`)

YAML fields:

- `ticks.<tick>.<dev> = [commits, added, removed, changed, {lang: [a,r,c]}]`
- `people` list
- `tick_size` seconds

PB: `DevsAnalysisResults`

Example:

```yaml
Devs:
  ticks:
    0:
      0: [1, 10, 2, 1, { Go: [10, 2, 1] }]
  people:
    - "alice"
  tick_size: 86400
```

### UAST Changes Saver (`--dump-uast-changes`)

YAML fields (list items):

- `file`, `src0`, `src1`, `uast0`, `uast1`
- `uast*.ast.json` files contain tree-sitter named-node arrays

Binary mode:

- payload bytes are JSON object: `{"changes":[...records...]}` (not protobuf)

Example:

```yaml
UASTChangesSaver:
  - {
      file: main.go,
      src0: /tmp/abc_before.src,
      src1: /tmp/abc_after.src,
      uast0: /tmp/abc_before.ast.json,
      uast1: /tmp/abc_after.ast.json,
    }
```

### File History (`--file-history`)

YAML fields:

- list entries keyed by file path
- per file:
  - `commits` list of hashes
  - `people` map-like string: `dev:[added,removed,changed]`

PB: `FileHistoryResultMessage`

Renaming a file transfers its complete history to the new path: commit hashes
and every developer's line statistics are conserved. If the destination path
already has history, both histories are retained, matching developer statistics
are added, and the rename commit is recorded. Hashes remain in deterministic
destination-first order and duplicates are removed.

Example:

```yaml
FileHistoryAnalysis:
  - "main.go":
    commits: ["deadbeef","cafebabe"]
    people: {0:[10,2,1],1:[3,0,0]}
```

### Hotspot Risk (`--hotspot-risk`)

Hotspot Risk reports current text files only. Its factors have the following
semantics:

- `size` is the current number of text lines.
- `churn` is the number of added, removed, or changed text lines in ticks whose
  start is no more than `window_days * 24h` before the latest tick start. Both
  boundaries are inclusive. The current tick is always included, so when a
  tick is longer than the configured window the result retains that complete
  current tick.
- `coupling_degree` is the number of distinct text-file histories that changed
  in the same commit at any point in the analysis. Renaming a file moves its
  churn and coupling histories to the new path and rewrites coupling
  references to that path. Deleting a file removes it from the output but does
  not erase its historical coupling from surviving files.
- `ownership_gini` is calculated from the owners of the file's current lines.
  Removed lines disappear from their previous owners; they are not assigned to
  the author who deleted them.

Size uses logarithmic max normalization; churn and coupling use linear max
normalization; ownership Gini is already in `[0,1]`. `risk_score` is the
weighted arithmetic mean of the four normalized factors. A zero factor
contributes zero without collapsing the other factors, and a weight of `0.0`
removes that factor from both the numerator and denominator. If all weights
are disabled, the score is zero.

YAML fields:

- `window_days`
- `files` list with:
  - `path`, `risk_score`, `size`, `churn`, `coupling_degree`, `ownership_gini`
  - `normalized.size/churn/coupling/ownership`

PB: `HotspotRiskResults`

Example:

```yaml
HotspotRisk:
  window_days: 90
  files:
    - path: "main.go"
      risk_score: 0.712300
      size: 500
      churn: 30
      coupling_degree: 8
      ownership_gini: 0.450000
      normalized:
        size: 0.600000
        churn: 0.700000
        coupling: 0.500000
        ownership: 0.450000
```

### Imports Per Developer (`--imports-per-dev`)

YAML fields:

- `tick_size`
- `imports.<developer_name>` = JSON object string with nested language/import/tick counts

PB: `ImportsPerDeveloperResults`

Example:

```yaml
ImportsPerDeveloper:
  tick_size: 86400
  imports:
    "alice": { "Go": { "fmt": { "0": 12 } } }
```

### Knowledge Diffusion (`--knowledge-diffusion`)

YAML fields:

- `knowledge_diffusion.window_months`
- `knowledge_diffusion.files.<path>`:
  - `unique_editors`, `recent_editors`, `editors_over_time`
- `knowledge_diffusion.distribution.<editor_count> = files_count`
- `knowledge_diffusion.people` list
- `knowledge_diffusion.tick_size` seconds

PB: `KnowledgeDiffusionResults`

Example:

```yaml
KnowledgeDiffusion:
  knowledge_diffusion:
    window_months: 6
    files:
      "main.go":
        unique_editors: 3
        recent_editors: 2
        editors_over_time: { 0: 1, 10: 3 }
    distribution:
      1: 5
      2: 3
    people:
      - "alice"
    tick_size: 86400
```

### Line Dump (`--linedump`)

YAML fields:

- `commits.<hash>` literal block with change rows:
  - `file_id prev_author prev_tick curr_author curr_tick delta`
- `file_sequence.<id> = path`
- `author_sequence` list

Binary mode:

- not supported (`Serialize()` returns error)

`--history-line-load` replays this YAML through the normal pipeline lifecycle. The loader
validates commit hashes and all file, tick, author, and ownership transitions before analysis.
Because change rows contain aggregate ownership deltas rather than edit positions, the loaded
file resolver exposes deterministic synthetic line indices; each live line's author and tick are
preserved exactly, but its original source position is not part of this format.

Example:

```yaml
LineDumper:
  commits:
    deadbeef: |-
      1      0     0      1     1 3
  file_sequence:
    1: "main.go"
  author_sequence:
    - "alice"
```

### Onboarding (`--onboarding`)

YAML fields:

- `onboarding.window_days`
- `onboarding.meaningful_threshold`
- `onboarding.authors.<author_id>`:
  - `first_commit_tick`, `join_cohort`, `snapshots.<days>`
- `onboarding.cohorts.<yyyy-mm>`:
  - `author_count`, `average_snapshots.<days>`
- `onboarding.people` list
- `onboarding.tick_size` seconds

PB: `OnboardingResults`

`join_cohort` is the calendar month (`YYYY-MM`) of the author's earliest
analysed commit, using that commit's author timestamp and UTC offset. A snapshot
for `N` days contains only commits whose author timestamp is at or before
`first_commit_timestamp + N * 24h`; the boundary is inclusive. If there is no
later commit inside the window, the snapshot contains the first commit rather
than selecting the next commit after the boundary. Tick size is retained as
output metadata but does not round or widen onboarding windows, including when
a tick is longer than the requested window.

Example:

```yaml
Onboarding:
  onboarding:
    window_days: [7, 30, 90]
    meaningful_threshold: 10
    authors:
      0:
        first_commit_tick: 12
        join_cohort: "2025-01"
        snapshots:
          7:
            {
              days: 7,
              commits: 3,
              files: 5,
              lines: 80,
              meaningful_commits: 2,
              meaningful_files: 4,
              meaningful_lines: 70,
            }
    cohorts:
      "2025-01":
        author_count: 4
        average_snapshots:
          7:
            {
              days: 7,
              commits: 2,
              files: 3,
              lines: 40,
              meaningful_commits: 1,
              meaningful_files: 2,
              meaningful_lines: 30,
            }
    people:
      - "alice"
    tick_size: 86400
```

### Ownership Concentration (`--ownership-concentration`)

YAML fields:

- `ownership_concentration.per_tick.<tick> = {gini, hhi, total_lines}`
- optional `ownership_concentration.per_subsystem.<path> = {gini, hhi}`
- `ownership_concentration.people` list
- `ownership_concentration.tick_size`

PB: `OwnershipConcentrationResults`

Tick and empty-repository semantics match Bus Factor: every occupied tick describes ownership
after that tick's last commit and before any later tick, the final occupied tick is retained, and
an input with no commits has no snapshots. Final subsystem Gini and HHI values are derived from the
same per-file ownership totals as the final global snapshot.

Example:

```yaml
OwnershipConcentration:
  ownership_concentration:
    per_tick:
      0: { gini: 0.3000, hhi: 0.2200, total_lines: 1000 }
    per_subsystem:
      "pkg": { gini: 0.2500, hhi: 0.2000 }
    people:
      - "alice"
    tick_size: 86400
```

### Refactoring Proxy (`--refactoring-proxy`)

YAML fields:

- `refactoring_proxy.threshold`
- `refactoring_proxy.tick_size`
- arrays: `ticks`, `rename_ratios`, `is_refactoring`, `total_changes`

PB: `RefactoringProxyResults`

Example:

```yaml
RefactoringProxy:
  refactoring_proxy:
    threshold: 0.50
    tick_size: 86400
    ticks: [0, 1, 2]
    rename_ratios: [0.1000, 0.7000, 0.2000]
    is_refactoring: [false, true, false]
    total_changes: [10, 15, 12]
```

### Sentiment (`--sentiment`)

YAML fields:

- per tick line: `<tick>: [score, [commit_hashes], "comment1|comment2|..."]`

PB: `CommentSentimentResults`

Notes:

- available only in tensorflow builds
- non-tensorflow builds return an explicit error

Example:

```yaml
Sentiment:
  42: [0.8123, [deadbeef, cafebabe], "Looks good|please refactor this"]
```

### Shotness (`--shotness`)

YAML fields:

- list entries with:
  - `name`, `file`, `internal_role`, `counters` map (`node_index -> score`)

PB: `ShotnessAnalysisResults`

An entity's stable identity is its source path, node kind (`internal_role`), and
qualified name. Qualified names include receiver or enclosing-scope context
where the language exposes it. Moving an entity within the same file preserves
its identity; changing its qualified name or kind creates a new identity.
Explicit whole-file renames migrate the stored identities and counters to the
new path, while moving an individual entity between files is treated as a new
identity.

Each `counters` key is another entity's stable index, not a time tick. The
`couples-shotness` renderer treats every entity as a co-occurrence profile
`v_i` and computes:

```text
C[i,j] = sum_k(v_i[k] * v_j[k])
```

The matrix is symmetric and its diagonal is each profile's squared norm.
Pair rankings exclude the diagonal and include only positive, distinct pairs.
YAML and Protocol Buffer inputs use this same matrix construction. Display
labels use `file:name` and add the node kind when that pair would otherwise be
ambiguous.

Example:

```yaml
Shotness:
  - name: Alpha
    file: "main.go"
    internal_role: "ast:function_declaration"
    counters: { "0": 3, "2": 1 }
```

### Temporal Activity (`--temporal-activity`)

YAML fields:

- `temporal_activity.activities.<dev_id>` with arrays:
  - `weekdays_commits`, `weekdays_lines`
  - `hours_commits`, `hours_lines`
  - `months_commits`, `months_lines`
  - `weeks_commits`, `weeks_lines`
- `temporal_activity.people` list

PB: `TemporalActivityResults` (includes `activities`, per-tick `ticks`, `tick_size`)

Example:

```yaml
TemporalActivity:
  temporal_activity:
    activities:
      0:
        weekdays_commits: [1, 2, 0, 0, 1, 0, 0]
        weekdays_lines: [10, 20, 0, 0, 5, 0, 0]
        hours_commits: [0, 0, 1]
        hours_lines: [0, 0, 10]
        months_commits: [1, 0, 0]
        months_lines: [10, 0, 0]
        weeks_commits: [1, 0, 0]
        weeks_lines: [10, 0, 0]
    people:
      - "alice"
```

### Typos Dataset (`--typos-dataset`)

YAML fields:

- list entries:
  - `wrong`, `correct`, `commit`, `file`, `line`

PB: `TyposDataset`

Example:

```yaml
TyposDataset:
  - wrong: "lnegth"
    correct: "length"
    commit: deadbeef
    file: "main.go"
    line: 12
```

## Compatibility Notes

- PB envelope and message definitions: `internal/pb/pb.proto`.
- PB field compatibility is guarded by `internal/pb/pb.schema.json` and
  `internal/pb/schema_snapshot_test.go`. Run
  `UPDATE_PB_SCHEMA_SNAPSHOT=1 go test ./internal/pb -run TestProtobufSchemaSnapshot -count=1`
  only after reviewing the compatibility impact.
- `AnalysisResults.contents` keys use `Leaf.Name()` values (see table above).
- `LineDumper` currently does not provide a protobuf payload.
- `UASTChangesSaver` binary payload is JSON-bytes in `contents["UASTChangesSaver"]`.
- `Sentiment` is behind build tag `tensorflow`; non-tensorflow builds expose the flag but return a clear runtime error.

### PB Schema Change Policy

Compatible changes:

- Add a new field with a previously unused field number.
- Add a new message that is not required by existing payloads.
- Add comments or documentation without changing field names, types, labels, or numbers.

Breaking changes:

- Remove or rename a message or field.
- Change a field number, type, label, map key type, or map value type.
- Reuse a removed field number or name.
- Change `AnalysisResults.contents` keys for an existing analysis.

Required process for PB changes:

1. Update `internal/pb/pb.proto`.
2. If removing a field, add a `reserved` statement for its field number and name.
   If removing a whole message, list it under "Retired message names" in the
   pb.proto header comment.
3. For breaking changes, bump `pb.SchemaVersion` in `internal/pb/version.go`.
4. Update this document and any affected examples.
5. Record the change in `docs/SCHEMA_CHANGELOG.md`.
6. Refresh `internal/pb/pb.schema.json` with the command above.
7. Regenerate Go/Python protobuf files when required.

CI enforcement: the `test-schema` workflow diffs `pb.schema.json` against the
push/PR base with `cmd/schema-guard` and fails when a schema change lacks a
`docs/SCHEMA_CHANGELOG.md` entry, when a breaking change lacks a version bump,
when a removed field is not reserved, or when a new field reuses a reserved
number or name. Run `just check-schema [base]` to check locally.
