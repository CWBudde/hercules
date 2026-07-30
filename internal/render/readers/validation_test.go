package readers

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

func TestYAMLReaderRejectsMalformedAndOversizedMatrices(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want error
	}{
		{
			name: "ragged",
			yaml: "hercules:\n  version: 2\nBurndown:\n  project: |\n    1 2\n    3\n",
			want: ErrAnalysisMalformed,
		},
		{
			name: "invalid number",
			yaml: "hercules:\n  version: 2\nBurndown:\n  project: |\n    1 nope\n",
			want: ErrAnalysisMalformed,
		},
		{
			name: "negative sparse index",
			yaml: "hercules:\n  version: 2\nCouples:\n  files_coocc:\n    matrix:\n      - {-1: 2}\n",
			want: ErrAnalysisMalformed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &YamlReader{Quiet: true}
			err := reader.Read(strings.NewReader(test.yaml))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	reader := &YamlReader{Limits: analysisio.Limits{MaxDecodedCells: 3}, Quiet: true}
	err := reader.Read(strings.NewReader(
		"hercules:\n  version: 2\nBurndown:\n  project: |\n    1 2\n    3 4\n",
	))
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("cell-limit error = %v, want ErrAnalysisTooLarge", err)
	}
}

func TestReadersEnforceConfigurableInputLimit(t *testing.T) {
	for name, reader := range map[string]Reader{
		"YAML":     &YamlReader{Limits: analysisio.Limits{MaxInputBytes: 4}, Quiet: true},
		"protobuf": &ProtobufReader{Limits: analysisio.Limits{MaxInputBytes: 4}, Quiet: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := reader.Read(strings.NewReader("12345"))
			if !errors.Is(err, ErrAnalysisTooLarge) {
				t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
			}
		})
	}
}

func TestReadersValidateSchemaVersions(t *testing.T) {
	t.Run("protobuf", func(t *testing.T) {
		tests := []struct {
			name    string
			header  *pb.Metadata
			wantErr error
		}{
			{"supported old", &pb.Metadata{Version: 1}, nil},
			{"current", &pb.Metadata{Version: pb.SchemaVersion}, nil},
			{"newer", &pb.Metadata{Version: pb.SchemaVersion + 1}, ErrAnalysisVersionUnsupported},
			{"missing", &pb.Metadata{}, ErrAnalysisMalformed},
			{"malformed", &pb.Metadata{Version: -1}, ErrAnalysisMalformed},
			{"missing header", nil, ErrAnalysisMalformed},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				data, err := proto.Marshal(&pb.AnalysisResults{Header: test.header})
				if err != nil {
					t.Fatal(err)
				}

				reader := &ProtobufReader{Quiet: true}
				err = reader.Read(bytes.NewReader(data))
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
			})
		}
	})

	t.Run("YAML", func(t *testing.T) {
		tests := []struct {
			name    string
			input   string
			wantErr error
		}{
			{"supported old", "hercules:\n  version: 0\n", nil},
			{"current", "hercules:\n  version: 2\n", nil},
			{"newer", "hercules:\n  version: 3\n", ErrAnalysisVersionUnsupported},
			{"missing", "hercules:\n  repository: repo\n", ErrAnalysisMalformed},
			{"malformed", "hercules:\n  version: two\n", ErrAnalysisMalformed},
			{"missing header", "Burndown: {}\n", ErrAnalysisMalformed},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				reader := &YamlReader{Quiet: true}
				err := reader.Read(strings.NewReader(test.input))
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				if err == nil {
					header := reader.data["hercules"].(map[string]any)
					if header["version"] != int(pb.SchemaVersion) {
						t.Fatalf("normalized version = %v, want %d", header["version"], pb.SchemaVersion)
					}
				}
			})
		}
	})
}

func TestProtobufReaderRejectsHostileDeclaredDimensions(t *testing.T) {
	payload, err := proto.Marshal(&pb.BurndownAnalysisResults{
		Project: &pb.BurndownSparseMatrix{
			NumberOfRows:    1_000_000,
			NumberOfColumns: 1_000_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := proto.Marshal(&pb.AnalysisResults{
		Header:   &pb.Metadata{Version: pb.SchemaVersion},
		Contents: map[string][]byte{"Burndown": payload},
	})
	if err != nil {
		t.Fatal(err)
	}

	reader := &ProtobufReader{Quiet: true}
	err = reader.Read(bytes.NewReader(envelope))
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
	}
}

func FuzzYAMLReader(f *testing.F) {
	f.Add([]byte("hercules:\n  repository: test\n"))
	f.Add([]byte("Burndown:\n  project: |\n    1 2\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := &YamlReader{Limits: analysisio.Limits{
			MaxInputBytes:    1 << 20,
			MaxDecodedCells:  1 << 16,
			MaxRows:          1 << 10,
			MaxColumns:       1 << 10,
			MaxNestedRecords: 1 << 16,
		}, Quiet: true}
		_ = reader.Read(bytes.NewReader(data))
	})
}

func FuzzProtobufReader(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := &ProtobufReader{Limits: analysisio.Limits{
			MaxInputBytes:    1 << 20,
			MaxDecodedCells:  1 << 16,
			MaxRows:          1 << 10,
			MaxColumns:       1 << 10,
			MaxNestedRecords: 1 << 16,
		}, Quiet: true}
		_ = reader.Read(bytes.NewReader(data))
	})
}
