# Large-Repo Scaling & Operational Safety (Milestone 2)

Date: 2026-02-15

## Problem

Running `hercules --burndown --first-parent --pb <large-repo>` on repositories with
large histories (e.g., the Linux kernel with ~1M commits, ~75k files) causes OOM kills.

The primary memory consumer is the LineHistoryAnalyser's RBTree allocator, which tracks
every live line in every file. Its hibernation system (compress + disk spill) exists but
defaults to disabled (threshold=0). Users must discover and combine multiple obscure flags
to run large repos successfully.

## Design

### Part A: Preset System (`--preset`)

A new `--preset` CLI flag that bundles common flag combinations.

**Presets:**

| Preset | Flags applied |
|--------|--------------|
| `large-repo` | `--first-parent`, `--lines-hibernation-threshold 200000`, `--lines-hibernation-disk`, `granularity=30`, `sampling=30` |
| `quick` | `--head` |

**Precedence rule:** Explicit flags always override preset defaults. The preset applies
only to flags the user did not set explicitly.

**Implementation location:** `cmd/hercules/root.go` in a new `applyPreset()` function
called during `PersistentPreRunE`, after cobra parses flags but before pipeline
initialization.

### Part B: LineHistory Hibernation Defaults

Change the default `--lines-hibernation-threshold` from 0 (disabled) to a sensible
value that activates compression for large file trees without penalizing small repos.

The threshold of 200000 nodes means hibernation only triggers for files with complex
change histories. For small repos this never fires. For Linux-scale repos it prevents
unbounded memory growth.

The `large-repo` preset additionally enables `--lines-hibernation-disk` to spill
compressed data to temp files, further reducing peak RSS.

**Implementation location:** `internal/linehistory/line_history.go`, change the default
value in `ListConfigurationOptions()`.

### Part C: BurndownAnalysis Hibernation

Add `HibernateablePipelineItem` interface to the new `BurndownAnalysis`.

**Hibernate():**
- Serialize `globalHistory`, `fileHistories`, `peopleHistories`, and `matrix` to
  compressed byte slices using `encoding/gob` + `compress/flate`.
- Set the original maps to nil to free memory.
- Optionally write compressed data to a temp file (controlled by new
  `--burndown-hibernation-disk` and `--burndown-hibernation-dir` flags).

**Boot():**
- Decompress and deserialize back into the original maps.
- Remove temp file if disk mode was used.

**New CLI flags:**
- `--burndown-hibernation-disk` (bool, default false)
- `--burndown-hibernation-dir` (string, default "")

These follow the same pattern as LineHistory's hibernation flags.

## Files Changed

| File | Change |
|------|--------|
| `cmd/hercules/root.go` | Add `--preset` flag, `applyPreset()` function |
| `internal/linehistory/line_history.go` | Change default hibernation threshold |
| `leaves/burndown.go` | Add `Hibernate()`/`Boot()`, hibernation fields and flags |
| `leaves/burndown_shared.go` | Add hibernation config constants |
| `leaves/burndown_test.go` | Add hibernation round-trip tests |

## Testing

- Unit tests for preset flag application (explicit flags override presets)
- Unit tests for BurndownAnalysis Hibernate/Boot round-trip
- Existing LineHistory hibernation tests already cover Part B
- Manual validation: `hercules --preset large-repo --burndown --pb <linux>`

## Out of Scope

- Adaptive/auto-tuning hibernation based on available RAM
- Pipeline merge tracking correctness tests (ROADMAP optional item)
- Plugin compatibility smoke test (ROADMAP optional item)
