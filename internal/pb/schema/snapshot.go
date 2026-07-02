// Package schema parses internal/pb/pb.proto into a comparable snapshot and
// classifies schema changes as compatible or breaking according to the policy
// in docs/SCHEMAS.md. It backs both the snapshot test in internal/pb and the
// schema-guard CI tool in cmd/schema-guard.
package schema

import (
	"encoding/json"
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
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Write stores a snapshot as indented JSON.
func Write(path string, snapshot Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
