# Release Hygiene Design

## Goal

Complete Roadmap Milestone 6 by making the release-facing documentation match the current cgo-free default build, optional TensorFlow support, preset workflows, version policy, and migration status.

## Scope

This milestone is a release hygiene pass, not a new analysis feature. The work stays limited to:

- README and docs updates for installation, Go version, build tags, presets, limitations, release versioning, and migration from the old upstream.
- A small CLI/help text correction so `--sentiment` is marked experimental in TensorFlow builds as well as default builds.
- Quality-gate verification with formatting, vetting, build, focused tests, and smoke commands.

Existing Milestone 5 identity changes remain intact and are not refactored as part of this milestone.

## Documentation Shape

The README remains the main user entry point. A new compact release guide documents maintainer-facing policy:

- Go version and default build expectations.
- Optional build tags and dependencies.
- Version policy for `hercules version`.
- Release checklist and migration notes.

The roadmap is updated only after the checks have been run, with any known failing gate explicitly recorded instead of hidden.

## Code Shape

`leaves/comment_sentiment.go` will prepend `[EXPERIMENTAL]` to the TensorFlow-enabled sentiment description. The non-TensorFlow stub already carries that marker.

## Verification

Run:

```bash
gofmt -w leaves/comment_sentiment.go
go fmt ./...
go vet ./...
go build ./cmd/hercules
go test ./internal/plumbing/identity ./cmd/hercules ./leaves -count=1
./hercules --dry-run .
./hercules --preset quick .
just test
```

If `just test` continues to fail in the known `internal/core` fixture tests, report those failures and keep the roadmap honest.
