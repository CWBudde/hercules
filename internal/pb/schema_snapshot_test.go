package pb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type protoSchemaSnapshot struct {
	Messages []protoMessageSnapshot `json:"messages"`
}

type protoMessageSnapshot struct {
	Name   string               `json:"name"`
	Fields []protoFieldSnapshot `json:"fields"`
}

type protoFieldSnapshot struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Label  string `json:"label,omitempty"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

func TestProtobufSchemaSnapshot(t *testing.T) {
	actual := parseProtoSchemaSnapshot(t, "pb.proto")
	if os.Getenv("UPDATE_PB_SCHEMA_SNAPSHOT") == "1" {
		writeProtoSchemaSnapshot(t, "pb.schema.json", actual)
	}
	expected := loadProtoSchemaSnapshot(t, "pb.schema.json")

	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("protobuf schema differs from pb.schema.json; review compatibility, update docs/SCHEMAS.md and docs/SCHEMA_CHANGELOG.md, then refresh with UPDATE_PB_SCHEMA_SNAPSHOT=1 go test ./internal/pb -run TestProtobufSchemaSnapshot -count=1")
	}
}

func parseProtoSchemaSnapshot(t *testing.T, path string) protoSchemaSnapshot {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proto schema: %v", err)
	}

	messageRe := regexp.MustCompile(`^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$`)
	fieldRe := regexp.MustCompile(`^(?:(repeated)\s+)?([A-Za-z_][A-Za-z0-9_\.]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`)
	mapRe := regexp.MustCompile(`^map<\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*,\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*>\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([0-9]+)\s*;`)

	var snapshot protoSchemaSnapshot
	var current *protoMessageSnapshot

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripProtoLineComment(raw))
		if line == "" {
			continue
		}

		if current == nil {
			matches := messageRe.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			snapshot.Messages = append(snapshot.Messages, protoMessageSnapshot{Name: matches[1]})
			current = &snapshot.Messages[len(snapshot.Messages)-1]
			continue
		}

		if line == "}" {
			current = nil
			continue
		}

		if matches := mapRe.FindStringSubmatch(line); matches != nil {
			current.Fields = append(current.Fields, protoFieldSnapshot{
				Number: atoiProtoFieldNumber(t, matches[4]),
				Name:   matches[3],
				Type:   "map",
				Key:    matches[1],
				Value:  matches[2],
			})
			continue
		}

		if matches := fieldRe.FindStringSubmatch(line); matches != nil {
			current.Fields = append(current.Fields, protoFieldSnapshot{
				Number: atoiProtoFieldNumber(t, matches[4]),
				Name:   matches[3],
				Type:   matches[2],
				Label:  matches[1],
			})
		}
	}

	sort.Slice(snapshot.Messages, func(i, j int) bool {
		return snapshot.Messages[i].Name < snapshot.Messages[j].Name
	})
	for i := range snapshot.Messages {
		sort.Slice(snapshot.Messages[i].Fields, func(j, k int) bool {
			return snapshot.Messages[i].Fields[j].Number < snapshot.Messages[i].Fields[k].Number
		})
	}
	return snapshot
}

func stripProtoLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func atoiProtoFieldNumber(t *testing.T, value string) int {
	t.Helper()

	var number int
	if _, err := fmt.Sscanf(value, "%d", &number); err != nil {
		t.Fatalf("parse field number %q: %v", value, err)
	}
	return number
}

func loadProtoSchemaSnapshot(t *testing.T, path string) protoSchemaSnapshot {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read protobuf schema snapshot: %v", err)
	}
	var snapshot protoSchemaSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("decode protobuf schema snapshot: %v", err)
	}
	return snapshot
}

func writeProtoSchemaSnapshot(t *testing.T, path string, snapshot protoSchemaSnapshot) {
	t.Helper()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("encode protobuf schema snapshot: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write protobuf schema snapshot: %v", err)
	}
}
