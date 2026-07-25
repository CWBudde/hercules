# Hercules build recipes

# Set environment variables
export GO111MODULE := "on"

# The in-repo Go renderer (matplotlib-go) only supports the non-cgo build path
# (the cgo path needs a vendored FreeType; see PLAN.md Phase 1). Override with
# CGO_ENABLED=1 in the environment if you really need cgo.
export CGO_ENABLED := env_var_or_default("CGO_ENABLED", "0")

# Detect OS and set executable extension
exe := if os() == "windows" { ".exe" } else { "" }

# Detect package architecture
pkg := `go env GOOS` + "_" + `go env GOARCH`

# Default GOBIN
gobin := env_var_or_default("GOBIN", ".")

# Build tags (can be overridden)
tags := env_var_or_default("TAGS", "")

# Default recipe (runs when you just type 'just')
default: hercules labours

# Build the hercules binary
hercules: vendor pb-go
    go build -tags "{{tags}}" -ldflags "-X github.com/cwbudde/hercules.BinaryGitHash=`git rev-parse HEAD`" github.com/cwbudde/hercules/cmd/hercules

# Build the labours binary (Go renderer)
labours: vendor pb-go
    go build -tags "{{tags}}" -ldflags "-X main.version=`git describe --tags --always` -X main.commit=`git rev-parse HEAD` -X main.date=`date -u +%Y-%m-%dT%H:%M:%SZ`" github.com/cwbudde/hercules/cmd/labours

# Run all tests for the default build
test: hercules labours
    go test ./...

# Run all tests (alias for test)
test-all: hercules labours
    go test ./...

# Run unit tests (alias for test)
test-unit: test

# Run the plugin compatibility smoke test. Go plugins need cgo, so this
# overrides the repo-wide CGO_ENABLED=0 default; the test builds a dedicated
# `-tags purego` hercules binary (the cgo FreeType path is not shipped).
test-plugin:
    CGO_ENABLED=1 go test -v -run TestPluginCompatibilitySmoke ./test/plugin_smoke/

# Run structural visual tests (no goldens or references required)
test-visual:
    go test ./test/visual/

# Run opt-in golden and Python visual parity tests
test-visual-parity:
    LABOURS_GO_VISUAL_PARITY=1 LABOURS_GO_PYTHON_PARITY=1 go test ./test/visual/ -v

# Generate a complete report directory for a repository.
report REPO OUTPUT="./report": hercules
    ./hercules{{exe}} report -o "{{OUTPUT}}" "{{REPO}}"

# RESAMPLE takes a pandas-style offset alias: "3M" for quarterly bands (the
# default), "year", "month", ... GRANULARITY (band width) and SAMPLING (how
# often the state is recorded) are in ticks/days; 30/30 with a 3M resample is
# the same combination MeKo/ewws-statistics uses (see its ewws-stats.yaml).
# Larger values mean fewer real samples and more interpolated filler, so the
# curves flow more; smaller values approach the day-exact staircase. NOTE: they
# currently have no effect anyway, because BurndownAnalysis.Initialize() forces
# both back to the default of 30 — which is what this chart wants, so the recipe
# is reproducible as it stands. The giant docs/linux.svg example is excluded
# because it dwarfs the real history. The chart title is blanked out when
# ImageMagick is available, since the page embedding it carries its own caption.
#
# Regenerate the self-analysis burndown chart (hercules replaying its own history)
burndown-chart RESAMPLE="3M" GRANULARITY="30" SAMPLING="30" OUTPUT="self-analysis/hercules-burndown.png": hercules labours
    #!/usr/bin/env sh
    set -eu
    dir=$(dirname "{{OUTPUT}}")
    mkdir -p "$dir"
    ./hercules{{exe}} --burndown --granularity {{GRANULARITY}} --sampling {{SAMPLING}} \
        --skip-blacklist --blacklisted-prefixes docs/linux.svg --pb . > "$dir/burndown.pb"
    ./labours{{exe}} -i "$dir/burndown.pb" -m burndown-project \
        --resample "{{RESAMPLE}}" -q -o "{{OUTPUT}}"
    if command -v convert >/dev/null 2>&1; then
        # Blank out the centred chart title only. Do NOT crop the top band away:
        # it also carries the y-axis magnitude ("1e4"), whose loss made the
        # published chart read as single-digit line counts. See PLAN.md DOC-02.
        convert "{{OUTPUT}}" -fill white -draw "rectangle 300,0 1350,35" "{{OUTPUT}}"
    else
        echo "note: ImageMagick 'convert' not found, chart title left in place"
    fi
    echo "chart written to {{OUTPUT}}"

# The per-developer counterpart to burndown-chart: the same surviving lines of
# code, but banded by who owns them rather than by when they were written, so a
# band shrinking means that person's code is being rewritten. Note this is the
# `ownership` mode, not `burndown-person` — the latter writes one figure per
# contributor rather than one stacked chart (see PLAN.md DOC-05).
#
# MAX_PEOPLE folds everyone past the cut into an "others" band; past ~20 the
# legend covers the plot and the colour palette wraps, so the top band and
# "others" come out the same colour. The people-dict is not optional: without it
# labours labels each band with the raw `name|email|email` identity string, which
# publishes every contributor's address and splits Christian across four
# identities. scripts/self-analysis-people.txt merges them and gives display-only
# names. SIZE/FONT_SIZE keep the chart legible once a page scales it into an ~800px
# content column, and match burndown-chart's scale.
#
# Regenerate the self-analysis ownership chart (whose code survives, over time)
ownership-chart MAX_PEOPLE="8" SIZE="16,10" FONT_SIZE="15" OUTPUT="self-analysis/hercules-ownership.png": hercules labours
    #!/usr/bin/env sh
    set -eu
    dir=$(dirname "{{OUTPUT}}")
    mkdir -p "$dir"
    ./hercules{{exe}} --burndown --burndown-people --devs --granularity 30 --sampling 30 \
        --people-dict scripts/self-analysis-people.txt \
        --skip-blacklist --blacklisted-prefixes docs/linux.svg --pb . > "$dir/people-devs.pb"
    ./labours{{exe}} -i "$dir/people-devs.pb" -m ownership \
        --max-people {{MAX_PEOPLE}} --size "{{SIZE}}" --font-size {{FONT_SIZE}} \
        -q -o "{{OUTPUT}}"
    echo "chart written to {{OUTPUT}}"

# Format code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Run linter
lint:
    # Match CI: check changed code only and use the cgo-free build.
    # Force module mode because vendor/ is ignored and may be stale locally.
    CGO_ENABLED=0 golangci-lint run --config ./.golangci.toml --timeout 2m --modules-download-mode=mod --new

# Audit structural complexity across an entire package scope, including
# pre-existing issues. Pass a narrower scope while paying down legacy debt,
# for example: `just lint-complexity ./internal/join`.
lint-complexity scope="./...":
    CGO_ENABLED=0 golangci-lint run --config ./.golangci.toml --timeout 2m \
        --modules-download-mode=mod --enable-only cyclop,funlen,gocognit,nestif \
        --tests=false --uniq-by-line=false "{{scope}}"

# Run linter with fix
lint-fix:
    CGO_ENABLED=0 golangci-lint run --config ./.golangci.toml --timeout 2m --modules-download-mode=mod --new --fix

# Check if code is formatted (error if changes needed)
check-formatted:
    #!/usr/bin/env bash
    set -euo pipefail
    treefmt --allow-missing-formatter
    if ! git diff --exit-code; then
        echo "Error: Code is not formatted. Run 'just fmt' to format."
        exit 1
    fi

# Check PB schema compatibility against a git base ref (mirrors CI's test-schema job)
check-schema base="origin/main":
    #!/usr/bin/env bash
    set -euo pipefail
    baseline=$(mktemp)
    trap 'rm -f "$baseline"' EXIT
    git show "{{base}}:internal/pb/pb.schema.json" > "$baseline"
    changelog_flag=""
    if ! git diff --quiet "{{base}}" -- docs/SCHEMA_CHANGELOG.md; then
        changelog_flag="-changelog-updated"
    fi
    go run ./cmd/schema-guard -old "$baseline" $changelog_flag

# Check if go.mod is tidy
check-tidy:
    #!/usr/bin/env bash
    set -euo pipefail
    go mod tidy
    if ! git diff --exit-code go.mod go.sum; then
        echo "Error: go.mod is not tidy. Run 'go mod tidy' to fix."
        exit 1
    fi

# Install development dependencies (formatters and linters)
setup-deps:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Installing development dependencies..."

    # Install treefmt (required for formatting)
    command -v treefmt >/dev/null 2>&1 || { echo "Installing treefmt..."; curl -fsSL https://github.com/numtide/treefmt/releases/download/v2.1.1/treefmt_2.1.1_linux_amd64.tar.gz | sudo tar -C /usr/local/bin -xz treefmt; }

    # Formatter versions are pinned so `just fmt` / `check-formatted` are
    # reproducible: unpinned `@latest`/`npm -g` installs drifted between local
    # runs and CI and produced spurious formatting diffs.

    # Install prettier (Node.js formatter)
    command -v prettier >/dev/null 2>&1 || { echo "Installing prettier..."; npm install -g prettier@3.8.1 || echo "Prettier installation failed - npm not found. Please install Node.js/npm manually."; }

    # Install gofumpt (Go formatter)
    command -v gofumpt >/dev/null 2>&1 || { echo "Installing gofumpt..."; go install mvdan.cc/gofumpt@v0.10.0; }

    # Install gci (Go import formatter)
    command -v gci >/dev/null 2>&1 || { echo "Installing gci..."; go install github.com/daixiang0/gci@v0.14.0; }

    # Install shfmt (Shell formatter)
    command -v shfmt >/dev/null 2>&1 || { echo "Installing shfmt..."; go install mvdan.cc/sh/v3/cmd/shfmt@v3.13.1; }

    # Install golangci-lint (Go linter) — v2.x to match the CI lint action and
    # the version = "2" .golangci.toml (v1.x cannot parse the v2 config).
    command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; GOTOOLCHAIN=go1.26.2 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2; }

    # Note: shellcheck requires manual installation on most systems
    command -v shellcheck >/dev/null 2>&1 || echo "WARNING: shellcheck not found. Please install manually: apt-get install shellcheck (Ubuntu/Debian) or brew install shellcheck (macOS)"

    echo "Development dependencies installation complete!"
    echo "Note: Ensure $(go env GOPATH)/bin is in your PATH for Go-based tools"

# Install protoc-gen-gogo if not present
protoc-gen-gogo:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v protoc-gen-gogo &> /dev/null; then
        echo "Installing protoc-gen-gogo..."
        go install github.com/gogo/protobuf/protoc-gen-gogo@latest
    fi

# Ensure the generated Go protobuf code exists. The committed
# internal/pb/pb.pb.go is the source of truth (proto drift is caught by the
# schema-guard CI and code review), so this only regenerates when the file is
# missing — avoiding a hard dependency on protoc during ordinary builds (CI
# checkout mtimes are non-deterministic, which previously triggered spurious
# protoc runs). Run `just pb-go-force` after editing pb.proto.
pb-go:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f internal/pb/pb.pb.go ]; then
        just pb-go-force
    fi

# Regenerate Go protobuf code from pb.proto (requires protoc + protoc-gen-gogo).
pb-go-force: protoc-gen-gogo
    protoc --gogo_out=internal/pb --proto_path=internal/pb internal/pb/pb.proto

# Vendor dependencies
vendor:
    go mod vendor

# Clean build artifacts
clean:
    rm -f hercules{{exe}}
    rm -f labours{{exe}}
    rm -f protoc-gen-gogo{{exe}}
    rm -rf vendor

# Run the large-repo benchmark suite. Pass a path to any local clone with
# substantial history (e.g. cpython, kubernetes). Results land in bench-results/.
# See docs/SCALING.md for what the configurations measure and how to interpret them.
bench-large-repo REPO_PATH: hercules
    HERCULES="$(pwd)/hercules{{exe}}" scripts/bench-large-repo.sh "{{REPO_PATH}}"

# Show available recipes
help:
    @just --list

fix:
    just lint-fix
    just fmt
