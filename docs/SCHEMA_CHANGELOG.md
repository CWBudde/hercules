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
