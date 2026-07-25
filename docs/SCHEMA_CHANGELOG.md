# Hercules Schema Changelog

This file records intentional output schema changes. Add entries when
`internal/pb/pb.schema.json`, `internal/pb/pb.proto`, or documented YAML output
contracts change.

## Format

Each entry should include:

- Date and commit/PR reference when known.
- Affected format: YAML, PB, or both.
- Affected analysis or message.
- Compatibility classification: compatible, breaking, or documentation-only.
- Required user action, if any.

## Unreleased

- YAML and PB: defined and corrected the experimental `CodeChurn` semantics
  without changing wire fields or types. Awareness and memorability are bounded
  scores with documented 30-tick and 180-tick half-lives, affected-line
  reinforcement, and other-author disruption. YAML now emits the existing
  deletion-history data, matching PB, and counter overflow returns an error
  instead of wrapping. Compatibility: semantic breaking change (existing
  consumers parse the output unchanged, but scores and YAML content can
  differ). User action: recompute Code Churn output before comparing scores
  across this release boundary.
- YAML and PB: corrected Burndown resampling when a still-forming age band
  decreases, without changing wire fields or types. Existing cohorts now decay
  proportionally instead of being represented as negative new cohorts.
  Project, file, person, and repository matrices are validated before merge or
  serialization, and invalid state returns a typed diagnostic instead of being
  clamped in YAML. Compatibility: semantic breaking change (existing consumers
  parse valid output unchanged, but corrected histories can differ and invalid
  histories now fail). User action: recompute stored Burndown results; callers
  that supplied negative matrices must handle the returned error.
- YAML and PB: corrected `FileHistoryAnalysis` rename handling without changing
  wire fields or types. Renames now conserve the source file's complete commit
  and per-author line-statistics history; destination collisions merge both
  histories instead of dropping one side. Compatibility: semantic breaking
  change (existing consumers parse the output unchanged, but corrected history
  values can differ). User action: recompute results containing file renames,
  especially renames onto a path with existing history.
- YAML and PB: corrected `Couples` partial-result identity mapping without
  changing wire fields or types. File and developer indices are now translated
  through their respective dictionaries, and valid merges are associative.
  Compatibility: semantic breaking change (existing consumers parse the output
  unchanged, but combined matrix values can differ). User action: recompute
  results assembled across pipeline branches.
- YAML and PB: corrected `Shotness` stable identities and `couples-shotness`
  matrix construction without changing wire fields or types. Entities retain
  identity across coordinate moves and explicit file renames by path, kind, and
  qualified name. Coupling charts use entity-profile dot products; diagonals
  are squared norms and are excluded from distinct-pair rankings. YAML and PB
  readers share this construction. Compatibility: semantic breaking change
  (existing consumers parse the data unchanged, but names, counters, matrix
  values, and rankings can differ). User action: recompute Shotness data and
  rerender Shotness coupling charts.
- YAML and PB: corrected the repository-filtered tree view used by analyses
  without changing any analysis wire fields or types. Old and new tree sides
  are filtered independently, so included-to-excluded transitions become
  deletions and excluded-to-included transitions become insertions. Filter
  errors no longer advance the diff baseline. Compatibility: semantic breaking
  change (existing consumers parse the output unchanged, but downstream values
  can differ). User action: recompute analyses using vendor, blacklist,
  whitelist, or language filters.
- YAML and PB: corrected `BusFactor` and `OwnershipConcentration` snapshot
  semantics without changing fields or wire types. Occupied ticks now contain
  only ownership state from commits at or before the labeled tick, including
  the final tick. Both analyses share incremental global and per-file
  ownership accounting, so their final subsystem metrics reconcile with the
  global snapshot. Bus Factor now uses exact ceiling coverage for the
  configured decimal threshold instead of truncating fractional required
  lines. Compatibility: semantic breaking change (existing consumers will
  parse the output unchanged, but corrected per-tick and subsystem values can
  differ). User action: recompute stored Bus Factor and Ownership
  Concentration results before comparing them across this release boundary.
- YAML and PB: corrected `Onboarding` cohort and snapshot semantics without
  changing fields or wire types. Join cohorts now use each author's actual
  earliest author timestamp and offset. Day windows use exact 24-hour durations
  and include only commits at or before the inclusive boundary; they never
  select later activity, even when ticks are coarse or do not divide a day.
  Compatibility: semantic breaking change (existing consumers will parse the
  output unchanged, but corrected cohort keys and snapshot values can differ).
  User action: recompute stored Onboarding results before comparing cohorts or
  snapshots across this release boundary.
- YAML and PB: corrected the `HotspotRisk` metric semantics without changing
  its fields or wire types. Churn now counts real text-line edits from the
  producer's full change-entry keys and uses duration-based tick windows;
  ownership comes from current line history; renames preserve churn and
  coupling history; binary files do not affect text-file coupling; and the
  score is a weighted normalized arithmetic mean in which a `0.0` weight
  disables its factor. Compatibility: semantic breaking change (existing
  consumers will parse the output unchanged, but should expect corrected
  values and rankings). User action: recompute stored Hotspot Risk results
  before comparing scores across this release boundary.
- Both: the schema version is now emitted from the single constant
  `pb.SchemaVersion` (`internal/pb/version.go`, value 2). This fixes the YAML
  header (`hercules.version`) and `hercules combine` PB output, which had
  emitted `0` since the module rename broke the package-suffix-derived
  `BinaryVersion`; `--pb` output already emitted 2 and is unchanged.
  Compatibility: compatible (bug fix; no field/type changes). User action:
  consumers that pinned `version: 0` from the YAML header should expect 2.
- PB: extended `internal/pb/pb.schema.json` with the top-level `version` and
  per-message `reserved_numbers`/`reserved_names`; added `cmd/schema-guard`
  and the `test-schema` CI workflow, which require a changelog entry for every
  schema change and a `pb.SchemaVersion` bump for breaking changes. Documented
  the retired `UASTChange`/`UASTChangesSaverResults` message names in
  `pb.proto`. Compatibility: documentation/tooling only. User action: none.
- PB: added `internal/pb/pb.schema.json` as the checked-in compatibility
  snapshot and `internal/pb/schema_snapshot_test.go` as the CI guardrail.
  Compatibility: documentation/tooling only. User action: none.
