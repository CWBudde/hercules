package render

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestDetectOutputFormatTreatsAggAsRenderingBackend(t *testing.T) {
	previousBackend := viper.GetString("backend")
	defer viper.Set("backend", previousBackend)

	viper.Set("backend", "Agg")
	if got := DetectOutputFormat("chart.svg"); got != "svg" {
		t.Fatalf("DetectOutputFormat() = %q, want svg", got)
	}
	if got := DetectOutputFormat("chart"); got != "png" {
		t.Fatalf("DetectOutputFormat() = %q, want png", got)
	}
}

func TestPlanModeOutputSingleMode(t *testing.T) {
	previousBackend := viper.GetString("backend")
	defer viper.Set("backend", previousBackend)
	viper.Set("backend", "auto")

	tmpDir := t.TempDir()
	tests := []struct {
		name       string
		baseOutput string
		mode       string
		modeCount  int
		want       string
	}{
		{
			name:       "file path is preserved",
			baseOutput: filepath.Join(tmpDir, "chart.svg"),
			mode:       "devs",
			modeCount:  1,
			want:       filepath.Join(tmpDir, "chart.svg"),
		},
		{
			name:       "directory output receives mode filename",
			baseOutput: tmpDir,
			mode:       "devs",
			modeCount:  1,
			want:       filepath.Join(tmpDir, "devs.png"),
		},
		{
			name:       "extensionless single output is file base",
			baseOutput: filepath.Join(tmpDir, "chart"),
			mode:       "devs",
			modeCount:  1,
			want:       filepath.Join(tmpDir, "chart.png"),
		},
		{
			name:       "empty output receives mode filename",
			baseOutput: "",
			mode:       "devs",
			modeCount:  1,
			want:       "devs.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planModeOutput(tt.baseOutput, tt.mode, tt.modeCount); got != tt.want {
				t.Fatalf("planModeOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanModeOutputMultipleModes(t *testing.T) {
	previousBackend := viper.GetString("backend")
	defer viper.Set("backend", previousBackend)
	viper.Set("backend", "auto")

	tmpDir := t.TempDir()
	tests := []struct {
		name       string
		baseOutput string
		mode       string
		want       string
	}{
		{
			name:       "directory base gets per-mode file",
			baseOutput: tmpDir,
			mode:       "devs",
			want:       filepath.Join(tmpDir, "devs.png"),
		},
		{
			name:       "extensionless base is directory in multi-mode",
			baseOutput: filepath.Join(tmpDir, "charts"),
			mode:       "languages",
			want:       filepath.Join(tmpDir, "charts", "languages.png"),
		},
		{
			name:       "file base contributes extension and parent directory",
			baseOutput: filepath.Join(tmpDir, "report.svg"),
			mode:       "ownership",
			want:       filepath.Join(tmpDir, "ownership.svg"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planModeOutput(tt.baseOutput, tt.mode, 2); got != tt.want {
				t.Fatalf("planModeOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanModeOutputMultiAssetModes(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []struct {
		name       string
		baseOutput string
		mode       string
		want       string
	}{
		{
			name:       "file output means write assets next to requested file",
			baseOutput: filepath.Join(tmpDir, "couples-files.png"),
			mode:       "couples-files",
			want:       tmpDir,
		},
		{
			name:       "directory output is passed through",
			baseOutput: tmpDir,
			mode:       "shotness",
			want:       tmpDir,
		},
		{
			name:       "empty output defaults to current directory",
			baseOutput: "",
			mode:       "sentiment",
			want:       ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planModeOutput(tt.baseOutput, tt.mode, 1); got != tt.want {
				t.Fatalf("planModeOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModeOutputConventionsCoverImplementedModes(t *testing.T) {
	for mode := range modeHandlers {
		convention, ok := modeOutputConventions[mode]
		if !ok {
			t.Fatalf("mode %q is missing an output convention", mode)
		}
		if convention.Kind == "" {
			t.Fatalf("mode %q has an empty output convention kind", mode)
		}
		if convention.Description == "" {
			t.Fatalf("mode %q has an empty output convention description", mode)
		}
		if len(convention.Assets) == 0 {
			t.Fatalf("mode %q does not document any output assets", mode)
		}
	}

	for mode := range modeOutputConventions {
		if _, ok := modeHandlers[mode]; !ok {
			t.Fatalf("output convention exists for non-implemented mode %q", mode)
		}
	}
}

func TestOutputConventionsMatchPlanner(t *testing.T) {
	previousBackend := viper.GetString("backend")
	defer viper.Set("backend", previousBackend)
	viper.Set("backend", "auto")

	tmpDir := t.TempDir()
	requestedFile := filepath.Join(tmpDir, "requested.svg")

	for mode, convention := range modeOutputConventions {
		t.Run(mode, func(t *testing.T) {
			got := planModeOutput(requestedFile, mode, 1)
			switch convention.Kind {
			case outputAssetDir:
				if got != tmpDir {
					t.Fatalf("asset-directory mode planned %q, want %q", got, tmpDir)
				}
				if !isMultiAssetMode(mode) {
					t.Fatalf("asset-directory mode %q is not treated as multi-asset", mode)
				}
			case outputSingleFile, outputFileFanout, outputCompanions:
				if got != requestedFile {
					t.Fatalf("%s mode planned %q, want requested file %q", convention.Kind, got, requestedFile)
				}
				if isMultiAssetMode(mode) {
					t.Fatalf("%s mode %q should not be treated as directory-style multi-asset", convention.Kind, mode)
				}
			default:
				t.Fatalf("unknown output convention kind %q", convention.Kind)
			}
		})
	}
}

func TestFileFanoutModesKeepRequestedBasename(t *testing.T) {
	previousBackend := viper.GetString("backend")
	defer viper.Set("backend", previousBackend)
	viper.Set("backend", "auto")

	tmpDir := t.TempDir()
	for _, mode := range []string{"burndown-file", "burndown-person"} {
		t.Run(mode, func(t *testing.T) {
			requested := filepath.Join(tmpDir, mode+".png")
			if got := planModeOutput(requested, mode, 1); got != requested {
				t.Fatalf("planModeOutput() = %q, want basename-preserving path %q", got, requested)
			}
		})
	}
}
