# Hercules Release Guide

This guide records the release policy for the `CWBudde/hercules` fork.

## Supported Default Build

Default release artifacts are cgo-free and do not require TensorFlow or legacy parser services.

```bash
CGO_ENABLED=0 go build ./cmd/hercules
CGO_ENABLED=0 go build ./cmd/labours
```

The `just` default recipe is also supported for local release preparation:

```bash
CGO_ENABLED=0 just
```

The project requires Go 1.26.5 or newer, matching `go.mod`.

## Optional Build Tags

- `tensorflow`: enables the experimental `--sentiment` analysis. This requires `libtensorflow`.
- `cgo_lz4`: restores the legacy cgo-backed LZ4 hibernation path. Default releases use pure-Go LZ4.

Optional builds are not the default release artifacts unless a release note explicitly says so.
When enabling either cgo-only option, also use the `purego` tag so the renderer does not select its
separately provisioned native FreeType backend.

## Version Policy

`hercules version` prints:

- `Version`: the API version derived from the Go module path.
- `Git`: the Git hash embedded at build time.

Builds produced through `just` embed the current Git hash with:

```bash
-ldflags "-X github.com/cwbudde/hercules.BinaryGitHash=$(git rev-parse HEAD)"
```

For tagged releases, create the tag from the commit used to build the binary and verify
`hercules version` reports that same commit hash.

Output schema changes follow [SCHEMAS.md](SCHEMAS.md) and [SCHEMA_CHANGELOG.md](SCHEMA_CHANGELOG.md).

## Migration Notes From Old Upstream

This fork intentionally does not preserve all old `src-d/hercules` behavior:

- Legacy Babelfish/UAST parser-service support was removed.
- Structural analyses use tree-sitter.
- Default builds do not include TensorFlow.
- The old `srcd/hercules` Docker image is not this fork's release artifact.
- Removed protobuf messages and compatibility decisions are documented in [SCHEMAS.md](SCHEMAS.md).

Users migrating from the old upstream should start with local clones and presets:

```bash
hercules --preset quick --burndown /path/to/repo
hercules --preset large-repo --burndown /path/to/large/repo
```

## Pre-Tag Checklist

Run these commands before creating a release tag:

```bash
export CGO_ENABLED=0
go fmt ./...
go vet ./...
go build ./cmd/hercules
go build ./cmd/labours
go test ./internal/plumbing ./leaves ./cmd/hercules ./cmd/labours
./hercules --dry-run .
./hercules --preset quick .
```

Run `just test` as the broad test command. If it fails because of known repository-fixture drift,
record the exact failing tests in the release notes before tagging.

For large-repo confidence, run:

```bash
just bench-large-repo /path/to/large/repo
```
