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
    go build -tags "{{tags}}" -ldflags "-X github.com/cyraxred/hercules.BinaryGitHash=`git rev-parse HEAD`" github.com/cyraxred/hercules/cmd/hercules

# Run all tests
test: hercules
    go test github.com/cyraxred/hercules

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
