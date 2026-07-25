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
