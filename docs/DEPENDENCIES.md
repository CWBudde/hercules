# Dependency update policy

Hercules supports the Go version declared by the `go` directive in `go.mod`. Maintainers keep the
default cgo-free build free of reachable vulnerabilities published in the Go vulnerability
database.

## Automated updates

Dependabot checks Go modules and GitHub Actions weekly. Updates in each ecosystem are grouped so
the repository receives one coherent compatibility change instead of a stream of tightly coupled
pull requests. Security updates may be opened separately by GitHub when an advisory requires
immediate action.

The `test-vulnerability` CI job runs `govulncheck ./...` with `CGO_ENABLED=0`. A reachable
vulnerability makes that job fail. Pull requests also run GitHub's dependency review action,
which rejects newly introduced vulnerable modules even when vulnerable symbols are not reachable
from the default build. Source analysis is the security gate for the supported default feature
set; optional platform-specific or plugin configurations still need their dedicated tests.

## Review and release expectations

- Direct runtime dependencies receive priority over developer tooling and indirect dependencies.
- Security fixes are upgraded to the first compatible fixed release or newer. Critical fixes
  should not wait for the weekly Dependabot run.
- Related modules, such as `go-git` and `go-billy`, are reviewed and upgraded together when their
  compatibility or transport behavior is coupled.
- Major-version changes and updates that alter serialized output require a dedicated pull request
  and the relevant compatibility or schema review.
- Dependency downgrades and vulnerability suppressions require a documented reason, affected
  versions, compensating controls, and a removal date or tracking issue.

## Verification checklist

For a Go dependency update:

1. Run `GOFLAGS=-mod=mod go mod tidy` and review both `go.mod` and `go.sum`.
2. Run the default test suite with `CGO_ENABLED=0 go test -count=1 ./...`.
3. For `go-git`, `go-billy`, or transport changes, run the remote HTTPS/SSH, local/file, Siva,
   submodule, and authentication tests in `cmd/hercules` and `internal/plumbing`.
4. Run the plugin compatibility smoke test with `just test-plugin`.
5. Run `CGO_ENABLED=0 go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`.
6. Run the repository formatting, vet, and lint checks before merging.

If a baseline test unrelated to the dependency update is already known to fail, record the exact
test names and reproduce the failure on the base revision. New failures introduced by an update
must be fixed before it is merged.
