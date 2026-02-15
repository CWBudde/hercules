# Hercules build recipes

# Set environment variables
export GO111MODULE := "on"

# Detect OS and set executable extension
exe := if os() == "windows" { ".exe" } else { "" }

# Detect package architecture
pkg := `go env GOOS` + "_" + `go env GOARCH`

# Default GOBIN
gobin := env_var_or_default("GOBIN", ".")

# Build tags (can be overridden)
tags := env_var_or_default("TAGS", "")

# Default recipe (runs when you just type 'just')
default: hercules

# Build the hercules binary
hercules: vendor pb-go pb-python plugin-template
    go build -tags "{{tags}}" -ldflags "-X github.com/meko-christian/hercules.BinaryGitHash=`git rev-parse HEAD`" github.com/meko-christian/hercules/cmd/hercules

# Run all tests for the default build (without Babelfish)
test: hercules
    go test ./...

# Run all tests including Babelfish/UAST tests (requires Babelfish server)
test-all: hercules
    go test -tags babelfish ./...

# Run unit tests (alias for test)
test-unit: test

# Install Python labours package using uv
install-labours:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v uv &> /dev/null; then
        echo "Error: uv is not installed. Install it with: curl -LsSf https://astral.sh/uv/install.sh | sh"
        exit 1
    fi
    cd python && uv pip install -e .

# Start Babelfish server for UAST analysis tests
setup-babelfish:
    #!/usr/bin/env bash
    set -euo pipefail

    # Check if Docker is installed
    if ! command -v docker &> /dev/null; then
        echo "Error: Docker is not installed. Please install Docker first."
        echo "Visit: https://docs.docker.com/get-docker/"
        exit 1
    fi

    # Check if Docker daemon is running
    if ! docker info &> /dev/null; then
        echo "Error: Docker daemon is not running. Please start Docker."
        exit 1
    fi

    # Check if container exists (running or stopped)
    if docker ps -a | grep -q bblfshd; then
        echo "Babelfish container already exists."
        # Check if it's running
        if docker ps | grep -q bblfshd; then
            echo "✓ Babelfish is already running on port 9432"
        else
            echo "Starting existing container..."
            docker start bblfshd
            sleep 2
            echo "✓ Babelfish started on port 9432"
        fi
    else
        echo "Creating new Babelfish container..."
        docker run -d --name bblfshd --privileged -p 9432:9432 bblfsh/bblfshd
        echo "✓ Babelfish container started on port 9432"

        # Wait a moment for the server to be ready
        echo "Waiting for server to be ready..."
        sleep 3
    fi

    # Install language drivers
    echo "Installing language drivers..."
    docker exec bblfshd bblfshctl driver install go bblfsh/go-driver:latest || echo "Go driver already installed"
    docker exec bblfshd bblfshctl driver install python bblfsh/python-driver:latest || echo "Python driver already installed"

    echo ""
    echo "✓ Babelfish setup complete!"
    echo "  Server running at: 0.0.0.0:9432"
    echo "  Run 'just test' to run full test suite including UAST tests"
    echo "  Run 'just stop-babelfish' to stop the server"

# Check Babelfish server status
babelfish-status:
    #!/usr/bin/env bash
    if docker ps | grep -q bblfshd; then
        echo "✓ Babelfish is running"
        docker exec bblfshd bblfshctl driver list
    elif docker ps -a | grep -q bblfshd; then
        echo "✗ Babelfish container exists but is not running"
        echo "  Run 'just setup-babelfish' to start it"
    else
        echo "✗ Babelfish is not set up"
        echo "  Run 'just setup-babelfish' to install and start it"
    fi

# Stop Babelfish server
stop-babelfish:
    #!/usr/bin/env bash
    set -euo pipefail
    if docker ps | grep -q bblfshd; then
        echo "Stopping Babelfish..."
        docker stop bblfshd
        echo "✓ Babelfish stopped"
    else
        echo "Babelfish is not running"
    fi

# Remove Babelfish container completely
remove-babelfish:
    #!/usr/bin/env bash
    set -euo pipefail
    if docker ps -a | grep -q bblfshd; then
        echo "Removing Babelfish container..."
        docker rm -f bblfshd
        echo "✓ Babelfish container removed"
    else
        echo "Babelfish container does not exist"
    fi

# Format code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Run linter
lint:
    golangci-lint run --config ./.golangci.toml --timeout 2m

# Run linter with fix
lint-fix:
    golangci-lint run --config ./.golangci.toml --timeout 2m --fix

# Check if code is formatted (error if changes needed)
check-formatted:
    #!/usr/bin/env bash
    set -euo pipefail
    treefmt --allow-missing-formatter
    if ! git diff --exit-code; then
        echo "Error: Code is not formatted. Run 'just fmt' to format."
        exit 1
    fi

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

    # Install prettier (Node.js formatter)
    command -v prettier >/dev/null 2>&1 || { echo "Installing prettier..."; npm install -g prettier || echo "Prettier installation failed - npm not found. Please install Node.js/npm manually."; }

    # Install gofumpt (Go formatter)
    command -v gofumpt >/dev/null 2>&1 || { echo "Installing gofumpt..."; go install mvdan.cc/gofumpt@latest; }

    # Install gci (Go import formatter)
    command -v gci >/dev/null 2>&1 || { echo "Installing gci..."; go install github.com/daixiang0/gci@latest; }

    # Install shfmt (Shell formatter)
    command -v shfmt >/dev/null 2>&1 || { echo "Installing shfmt..."; go install mvdan.cc/sh/v3/cmd/shfmt@latest; }

    # Install golangci-lint (Go linter)
    command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.61.0; }

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

# Generate Go protobuf code
pb-go: protoc-gen-gogo
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f internal/pb/pb.pb.go ] || [ internal/pb/pb.proto -nt internal/pb/pb.pb.go ]; then
        protoc --gogo_out=internal/pb --proto_path=internal/pb internal/pb/pb.proto
    fi

# Generate Python protobuf code
pb-python:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f python/labours/pb_pb2.py ] || [ internal/pb/pb.proto -nt python/labours/pb_pb2.py ]; then
        protoc --python_out python/labours --proto_path=internal/pb internal/pb/pb.proto
    fi

# Generate plugin template source
plugin-template:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f cmd/hercules/plugin_template_source.go ] || [ cmd/hercules/plugin.template -nt cmd/hercules/plugin_template_source.go ]; then
        cd cmd/hercules && go generate
    fi

# Vendor dependencies
vendor:
    go mod vendor

# Clean build artifacts
clean:
    rm -f hercules{{exe}}
    rm -f protoc-gen-gogo{{exe}}
    rm -f internal/pb/pb.pb.go
    rm -f python/labours/pb_pb2.py
    rm -f cmd/hercules/plugin_template_source.go
    rm -rf vendor

# Show available recipes
help:
    @just --list
