package readers

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

func TestYAMLReaderRejectsMalformedAndOversizedMatrices(t *testing.T) {
	viper.Set("quiet", true)
	t.Cleanup(func() { viper.Set("quiet", false) })

	tests := []struct {
		name string
		yaml string
		want error
	}{
		{
			name: "ragged",
			yaml: "Burndown:\n  project: |\n    1 2\n    3\n",
			want: ErrAnalysisMalformed,
		},
		{
			name: "invalid number",
			yaml: "Burndown:\n  project: |\n    1 nope\n",
			want: ErrAnalysisMalformed,
		},
		{
			name: "negative sparse index",
			yaml: "Couples:\n  files_coocc:\n    matrix:\n      - {-1: 2}\n",
			want: ErrAnalysisMalformed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &YamlReader{}
			err := reader.Read(strings.NewReader(test.yaml))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	reader := &YamlReader{Limits: analysisio.Limits{MaxDecodedCells: 3}}
	err := reader.Read(strings.NewReader("Burndown:\n  project: |\n    1 2\n    3 4\n"))
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("cell-limit error = %v, want ErrAnalysisTooLarge", err)
	}
}

func TestReadersEnforceConfigurableInputLimit(t *testing.T) {
	viper.Set("quiet", true)
	t.Cleanup(func() { viper.Set("quiet", false) })

	for name, reader := range map[string]Reader{
		"YAML":     &YamlReader{Limits: analysisio.Limits{MaxInputBytes: 4}},
		"protobuf": &ProtobufReader{Limits: analysisio.Limits{MaxInputBytes: 4}},
	} {
		t.Run(name, func(t *testing.T) {
			err := reader.Read(strings.NewReader("12345"))
			if !errors.Is(err, ErrAnalysisTooLarge) {
				t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
			}
		})
	}
}

func TestProtobufReaderRejectsHostileDeclaredDimensions(t *testing.T) {
	viper.Set("quiet", true)
	t.Cleanup(func() { viper.Set("quiet", false) })

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
		Contents: map[string][]byte{"Burndown": payload},
	})
	if err != nil {
		t.Fatal(err)
	}

	reader := &ProtobufReader{}
	err = reader.Read(bytes.NewReader(envelope))
	if !errors.Is(err, ErrAnalysisTooLarge) {
		t.Fatalf("error = %v, want ErrAnalysisTooLarge", err)
	}
}

func FuzzYAMLReader(f *testing.F) {
	viper.Set("quiet", true)
	f.Add([]byte("hercules:\n  repository: test\n"))
	f.Add([]byte("Burndown:\n  project: |\n    1 2\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := &YamlReader{Limits: analysisio.Limits{
			MaxInputBytes:    1 << 20,
			MaxDecodedCells:  1 << 16,
			MaxRows:          1 << 10,
			MaxColumns:       1 << 10,
			MaxNestedRecords: 1 << 16,
		}}
		_ = reader.Read(bytes.NewReader(data))
	})
}

func FuzzProtobufReader(f *testing.F) {
	viper.Set("quiet", true)
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := &ProtobufReader{Limits: analysisio.Limits{
			MaxInputBytes:    1 << 20,
			MaxDecodedCells:  1 << 16,
			MaxRows:          1 << 10,
			MaxColumns:       1 << 10,
			MaxNestedRecords: 1 << 16,
		}}
		_ = reader.Read(bytes.NewReader(data))
	})
}
