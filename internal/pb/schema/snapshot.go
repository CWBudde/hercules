// Package schema parses internal/pb/pb.proto into a comparable snapshot and
// classifies schema changes as compatible or breaking according to the policy
// in docs/SCHEMAS.md. It backs both the snapshot test in internal/pb and the
// schema-guard CI tool in cmd/schema-guard.
package schema

import (
	"encoding/json"
	"fmt"
	"os"
)

// Snapshot is the checked-in representation of the protobuf output schema
// (internal/pb/pb.schema.json).
type Snapshot struct {
	// Version is the output schema version emitted in Metadata.version;
	// it mirrors pb.SchemaVersion at the time the snapshot was refreshed.
	Version  int       `json:"version"`
	Messages []Message `json:"messages"`
}

// Message captures one protobuf message: its live fields plus the reserved
// field numbers and names that must never be reused.
type Message struct {
	Name            string   `json:"name"`
	Fields          []Field  `json:"fields"`
	ReservedNumbers []int    `json:"reserved_numbers,omitempty"`
	ReservedNames   []string `json:"reserved_names,omitempty"`
}

// Field captures one protobuf field. Map fields use Type "map" with Key/Value
// set; all other fields use Type and an optional "repeated" Label.
type Field struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Label  string `json:"label,omitempty"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

// Load reads a snapshot JSON file.
func Load(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read schema snapshot %q: %w", path, err)
	}

	var snapshot Snapshot

	err = json.Unmarshal(data, &snapshot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode schema snapshot %q: %w", path, err)
	}

	return snapshot, nil
}

// Write stores a snapshot as indented JSON.
func Write(path string, snapshot Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schema snapshot: %w", err)
	}

	data = append(data, '\n')

	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		return fmt.Errorf("write schema snapshot %q: %w", path, err)
	}

	return nil
}
