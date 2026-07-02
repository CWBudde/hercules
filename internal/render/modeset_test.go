package render

import (
	"reflect"
	"testing"
)

func TestResolveModesPythonCompatibleAliases(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "burndown alias",
			input: []string{"burndown"},
			want:  []string{"burndown-project"},
		},
		{
			name:  "couples alias",
			input: []string{"couples"},
			want:  []string{"couples-files", "couples-people", "couples-shotness"},
		},
		{
			name:  "comma separated modes",
			input: []string{"burndown-project,devs"},
			want:  []string{"burndown-project", "devs"},
		},
		{
			name:  "python all",
			input: []string{"all"},
			want:  pythonAllModes,
		},
		{
			name:  "known unimplemented report mode is valid",
			input: []string{"temporal-activity"},
			want:  []string{"temporal-activity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveModes(tt.input)
			if err != nil {
				t.Fatalf("ResolveModes() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ResolveModes() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveModesRejectsUnknownMode(t *testing.T) {
	_, err := ResolveModes([]string{"not-a-mode"})
	if err == nil {
		t.Fatal("expected unknown mode error")
	}
}

func TestResolveModesAllowsEmptyModes(t *testing.T) {
	modes, err := ResolveModes(nil)
	if err != nil {
		t.Fatalf("unexpected empty mode error: %v", err)
	}
	if len(modes) != 0 {
		t.Fatalf("ResolveModes(nil) = %#v, want empty modes", modes)
	}
}

func TestNormalizeInputFormat(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: "auto"},
		{input: "AUTO", want: "auto"},
		{input: "yaml", want: "yaml"},
		{input: "pb", want: "pb"},
		{input: "json", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeInputFormat(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeInputFormat() = %q, want %q", got, tt.want)
			}
		})
	}
}
