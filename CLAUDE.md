# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

- `just` or `just hercules` - Build the hercules binary
- `just test` - Run all Go tests
- `just install-labours` - Install the Python labours package for plotting/visualization using uv
- `just clean` - Clean build artifacts
- `just help` - List all available recipes
- `go test github.com/meko-christian/hercules` - Run Go tests directly

## Project Architecture

Hercules is a Git repository analysis engine with two main components:

1. **hercules** (Go) - Core analysis engine that processes Git commits through a DAG pipeline
2. **labours** (Python) - Visualization and plotting companion for analysis results

### Core Architecture Components

**Pipeline System** (`internal/core/pipeline.go`):

- Central execution engine that orchestrates analysis through a Directed Acyclic Graph (DAG)
- Pipeline items implement the `PipelineItem` interface with dependency management
- Supports branching/merging for Git history analysis
- Items can be forked and merged to handle repository branches

**Analysis Items** (`leaves/`):

- `burndown.go` - Line burndown analysis (core feature)
- `couples.go` - File/developer coupling analysis
- `devs.go` - Developer activity and contribution analysis
- `commits.go` - Basic commit statistics
- `file_history.go` - Per-file change tracking
- `shotness.go` - Structural hotness analysis (requires Babelfish)
- `temporal_activity.go` - Temporal activity patterns (weekday, hour, month, ISO week)

**Plumbing Components** (`internal/plumbing/`):

- Low-level analysis building blocks (diff processing, identity resolution, etc.)
- Provide dependencies for leaf analysis items

### Key Concepts

- **Pipeline Items**: Modular analysis units with declared dependencies and outputs
- **Features**: Can be enabled/disabled to control which analysis items are included
- **Protocol Buffers**: Binary output format option (use `--pb` flag)
- **Hibernation**: Memory optimization for large repositories
- **Plugin System**: Custom analyses can be loaded via `--plugin` flag

### Entry Points

- `cmd/hercules/root.go` - Main CLI application entry point
- `core.go` - Public API facade that exports internal types
- Analysis items register themselves via `init()` functions in the `Registry`

### Data Flow

1. Git repository → commits list
2. Pipeline resolves dependencies and creates execution plan
3. Items process commits sequentially, passing data through declared dependencies
4. Leaf items produce final analysis results
5. Results serialized to YAML or Protocol Buffers

The pipeline automatically handles Git branching/merging by forking and merging analysis state across branches.

## Temporal Activity Analysis

The `--temporal-activity` analysis tracks when developers work by extracting temporal patterns from commit timestamps.

### Usage

**Basic analysis (commits):**

```bash
hercules --temporal-activity /path/to/repo > temporal.yml
labours -m temporal-activity temporal.yml
```

**Lines mode:**

```bash
hercules --temporal-activity --temporal-mode=lines /path/to/repo > temporal.yml
labours -m temporal-activity temporal.yml
```

**Combined with other analyses:**

```bash
hercules --burndown --devs --temporal-activity /path/to/repo > analysis.yml
labours -m all analysis.yml
```

### Output

The analysis produces five visualizations showing activity distribution:

**Stacked Bar Charts:**

- **Weekdays** (Sunday-Saturday): Identifies work patterns across the week
- **Hours** (0-23): Shows when developers are most active during the day
- **Months** (January-December): Reveals seasonal patterns
- **ISO Weeks** (1-53): Tracks activity across the calendar year

**Heatmap:**

- **Weekday × Hour Matrix**: 2D heatmap showing activity intensity across all combinations of weekdays and hours, revealing detailed temporal patterns and identifying when developers are most productive

### Modes

- `--temporal-mode=commits` (default): Counts number of commits
- `--temporal-mode=lines`: Counts total lines changed (additions + deletions)

### Use Cases

- Understanding team work patterns and time zones
- Detecting work-life balance indicators
- Identifying peak productivity periods
- Analyzing distributed team coordination
