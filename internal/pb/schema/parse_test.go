package schema

import (
	"reflect"
	"testing"
)

func TestParseProtoMessagesAndFields(t *testing.T) {
	proto := `
syntax = "proto3";

// a comment
message Metadata {
    // this format is versioned
    int32 version = 1;
    string hash = 2;
    map<string, double> run_time_per_item = 8;
}

message BurndownSparseMatrix {
    string name = 1;
    repeated BurndownSparseMatrixRow rows = 4;
}
`
	snapshot, err := ParseProto([]byte(proto))
	if err != nil {
		t.Fatalf("ParseProto: %v", err)
	}

	expected := Snapshot{Messages: []Message{
		{
			Name: "BurndownSparseMatrix",
			Fields: []Field{
				{Number: 1, Name: "name", Type: "string"},
				{Number: 4, Name: "rows", Type: "BurndownSparseMatrixRow", Label: "repeated"},
			},
		},
		{
			Name: "Metadata",
			Fields: []Field{
				{Number: 1, Name: "version", Type: "int32"},
				{Number: 2, Name: "hash", Type: "string"},
				{Number: 8, Name: "run_time_per_item", Type: "map", Key: "string", Value: "double"},
			},
		},
	}}
	if !reflect.DeepEqual(snapshot, expected) {
		t.Fatalf("snapshot mismatch:\n got: %+v\nwant: %+v", snapshot, expected)
	}
}

func TestParseProtoReserved(t *testing.T) {
	proto := `
message Legacy {
    reserved 2, 4 to 6;
    reserved "old_name", "gone";
    string kept = 1;
}
`
	snapshot, err := ParseProto([]byte(proto))
	if err != nil {
		t.Fatalf("ParseProto: %v", err)
	}
	msg := snapshot.Messages[0]
	if !reflect.DeepEqual(msg.ReservedNumbers, []int{2, 4, 5, 6}) {
		t.Fatalf("reserved numbers: %v", msg.ReservedNumbers)
	}
	if !reflect.DeepEqual(msg.ReservedNames, []string{"gone", "old_name"}) {
		t.Fatalf("reserved names: %v", msg.ReservedNames)
	}
	if len(msg.Fields) != 1 || msg.Fields[0].Name != "kept" {
		t.Fatalf("fields: %+v", msg.Fields)
	}
}

func TestParseProtoRejectsMalformedReserved(t *testing.T) {
	proto := `
message Bad {
    reserved 2 to max;
}
`
	_, err := ParseProto([]byte(proto))
	if err == nil {
		t.Fatal("expected error for 'to max' reserved range")
	}
}
