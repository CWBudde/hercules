package pb

import (
	"os"
	"reflect"
	"testing"

	"github.com/cwbudde/hercules/internal/pb/schema"
)

// TestProtobufSchemaSnapshot guards pb.proto against unreviewed changes:
// the parsed schema (including reserved statements) plus the current
// SchemaVersion must match the checked-in snapshot pb.schema.json.
// CI additionally diffs the snapshot against the merge base via
// cmd/schema-guard to enforce the compatibility policy in docs/SCHEMAS.md.
func TestProtobufSchemaSnapshot(t *testing.T) {
	data, err := os.ReadFile("pb.proto")
	if err != nil {
		t.Fatalf("read proto schema: %v", err)
	}
	actual, err := schema.ParseProto(data)
	if err != nil {
		t.Fatalf("parse proto schema: %v", err)
	}
	actual.Version = SchemaVersion

	if os.Getenv("UPDATE_PB_SCHEMA_SNAPSHOT") == "1" {
		err := schema.Write("pb.schema.json", actual)
		if err != nil {
			t.Fatalf("write protobuf schema snapshot: %v", err)
		}
	}

	expected, err := schema.Load("pb.schema.json")
	if err != nil {
		t.Fatalf("load protobuf schema snapshot: %v", err)
	}

	if actual.Version != expected.Version {
		t.Fatalf("pb.SchemaVersion (%d) differs from pb.schema.json version (%d); "+
			"review docs/SCHEMAS.md, update docs/SCHEMA_CHANGELOG.md, then refresh with "+
			"UPDATE_PB_SCHEMA_SNAPSHOT=1 go test ./internal/pb -run TestProtobufSchemaSnapshot -count=1",
			actual.Version, expected.Version)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf(
			"protobuf schema differs from pb.schema.json; review compatibility (docs/SCHEMAS.md), " +
				"update docs/SCHEMA_CHANGELOG.md, then refresh with UPDATE_PB_SCHEMA_SNAPSHOT=1 " +
				"go test ./internal/pb -run TestProtobufSchemaSnapshot -count=1",
		)
	}
}
