# Hercules Output Schemas (YAML + Protocol Buffers)

This document describes the **effective output schemas** currently produced by Hercules.
It is derived from live `Serialize()` implementations in `leaves/` and protobuf definitions in `internal/pb/pb.proto`.

Scope:

- YAML output (`hercules ...`)
- Protocol Buffers output (`hercules --pb ...`)
- One compact example payload per analysis target

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
| `--codechurn`               | `CodeChurn`              | none (currently not serialized)              |
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

## Schema Details + Examples

### Burndown (`--burndown`)

YAML fields:

- `granularity` int
- `sampling` int
- `tick_size` int seconds
- `"project"` multiline matrix
- optional: `files`, `files_ownership`, `people_sequence`, `people`, `people_interaction`, `repository_sequence`, `repositories`

PB: `BurndownAnalysisResults`

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

Current state:

- `Serialize()` returns `nil` and emits no structured payload.
- PB payload is not defined/used.

Example:

```yaml
CodeChurn:
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

Example:

```yaml
FileHistoryAnalysis:
  - "main.go":
    commits: ["deadbeef","cafebabe"]
    people: {0:[10,2,1],1:[3,0,0]}
```

### Hotspot Risk (`--hotspot-risk`)

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
- `AnalysisResults.contents` keys use `Leaf.Name()` values (see table above).
- `CodeChurn` and `LineDumper` currently do not provide protobuf payloads.
- `UASTChangesSaver` binary payload is JSON-bytes in `contents["UASTChangesSaver"]`.
- `Sentiment` is behind build tag `tensorflow`; non-tensorflow builds expose the flag but return a clear runtime error.
