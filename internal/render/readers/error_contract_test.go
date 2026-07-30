package readers

import (
	"errors"
	"testing"

	"github.com/cwbudde/hercules/internal/pb"
)

func TestLegacyReadersTypeMissingAnalysisErrors(t *testing.T) {
	protobuf := &ProtobufReader{data: &pb.AnalysisResults{Contents: map[string][]byte{}}}
	yaml := &YamlReader{data: map[string]any{}}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "protobuf burndown",
			call: func() error {
				_, err := protobuf.GetFilesBurndown()
				return err
			},
		},
		{
			name: "protobuf couples",
			call: func() error {
				_, _, err := protobuf.GetFileCooccurrence()
				return err
			},
		},
		{
			name: "protobuf devs",
			call: func() error {
				_, err := protobuf.GetDeveloperStats()
				return err
			},
		},
		{
			name: "yaml burndown",
			call: func() error {
				_, err := yaml.GetPeopleBurndown()
				return err
			},
		},
		{
			name: "yaml couples",
			call: func() error {
				_, _, err := yaml.GetPeopleCooccurrence()
				return err
			},
		},
		{
			name: "yaml devs",
			call: func() error {
				_, err := yaml.GetDeveloperStats()
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if !errors.Is(err, ErrAnalysisMissing) {
				t.Fatalf("error = %v, want ErrAnalysisMissing", err)
			}
		})
	}
}

func TestMalformedAnalysisIsNotMissing(t *testing.T) {
	reader := &ProtobufReader{data: &pb.AnalysisResults{
		Contents: map[string][]byte{"Devs": {0xff}},
	}}
	_, err := reader.GetDeveloperStats()
	if !errors.Is(err, ErrAnalysisMalformed) {
		t.Fatalf("error = %v, want ErrAnalysisMalformed", err)
	}
	if errors.Is(err, ErrAnalysisMissing) {
		t.Fatalf("malformed analysis was classified as missing: %v", err)
	}
}
