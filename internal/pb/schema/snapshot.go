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
	Name            string  `json:"name"`
	Fields          []Field `json:"fields"`
	ReservedNumbers []int
	ReservedNames   []string
}

// MarshalJSON keeps the checked-in snapshot's established snake_case wire keys
// without coupling the public Go field names to that external representation.
func (message Message) MarshalJSON() ([]byte, error) {
	values := []any{message.Name, message.Fields, message.ReservedNumbers, message.ReservedNames}
	encoded := make([][]byte, len(values))

	for index, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode message field: %w", err)
		}

		encoded[index] = data
	}

	result := fmt.Sprintf(`{"name":%s,"fields":%s`, encoded[0], encoded[1])

	if len(message.ReservedNumbers) > 0 {
		result += fmt.Sprintf(`,"reserved_numbers":%s`, encoded[2])
	}

	if len(message.ReservedNames) > 0 {
		result += fmt.Sprintf(`,"reserved_names":%s`, encoded[3])
	}

	return []byte(result + "}"), nil
}

// UnmarshalJSON accepts the established snapshot representation.
func (message *Message) UnmarshalJSON(data []byte) error {
	var values map[string]json.RawMessage

	err := json.Unmarshal(data, &values)
	if err != nil {
		return fmt.Errorf("decode message object: %w", err)
	}

	fields := []struct {
		key   string
		value any
	}{
		{"name", &message.Name},
		{"fields", &message.Fields},
		{"reserved_numbers", &message.ReservedNumbers},
		{"reserved_names", &message.ReservedNames},
	}
	for _, field := range fields {
		if raw, exists := values[field.key]; exists {
			err = json.Unmarshal(raw, field.value)
			if err != nil {
				return fmt.Errorf("decode message field %q: %w", field.key, err)
			}
		}
	}

	return nil
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
