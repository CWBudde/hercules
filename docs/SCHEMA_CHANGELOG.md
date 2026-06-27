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

- PB: added `internal/pb/pb.schema.json` as the checked-in compatibility
  snapshot and `internal/pb/schema_snapshot_test.go` as the CI guardrail.
  Compatibility: documentation/tooling only. User action: none.
