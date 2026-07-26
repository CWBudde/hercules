package render

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/outputpath"
)

// DetectOutputFormat determines the output format ("png", "svg", or "pdf")
// from the output path's file extension, unless the viper "backend" key
// overrides it. Unknown extensions and rendering backends (e.g. "Agg")
// fall back to PNG.
func DetectOutputFormat(outputPath string) string {
	// Check if backend flag overrides format detection
	if backend := viper.GetString("backend"); backend != "" {
		switch strings.ToLower(backend) {
		case "pdf":
			return "pdf"
		case "png":
			return "png"
		case "svg":
			return "svg"
		case "auto":
			// Fall through to extension detection
		default:
			// For unknown backends, fall through to extension detection
		}
	}

	// Detect from file extension
	ext := strings.ToLower(filepath.Ext(outputPath))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".svg":
		return "svg"
	case ".png", "":
		return "png" // Default to PNG
	default:
		return "png" // Default to PNG for unknown extensions
	}
}

// GenerateOutputPath returns basePath with the extension matching format,
// replacing any existing extension.
func GenerateOutputPath(basePath, format string) string {
	ext := "." + format

	// If basePath already has the correct extension, use it as-is
	if strings.HasSuffix(strings.ToLower(basePath), ext) {
		return basePath
	}

	// Remove any existing extension and add the correct one
	nameWithoutExt := strings.TrimSuffix(basePath, filepath.Ext(basePath))
	return nameWithoutExt + ext
}

type outputConventionKind string

const (
	outputSingleFile outputConventionKind = "single-file"
	outputFileFanout outputConventionKind = "file-fanout"
	outputAssetDir   outputConventionKind = "asset-directory"
	outputCompanions outputConventionKind = "primary-file-with-companions"
)

type outputConvention struct {
	Kind        outputConventionKind
	Description string
	Assets      []string
}

var modeOutputConventions = map[string]outputConvention{
	"burndown-project": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested chart file",
		Assets:      []string{"<output>"},
	},
	"burndown-file": {
		Kind:        outputFileFanout,
		Description: "uses the requested file path as a basename and writes one chart per file with a stable identity hash",
		Assets:      []string{"<base>_<rune-safe-file-slug>-<hash><ext>"},
	},
	"burndown-person": {
		Kind:        outputFileFanout,
		Description: "uses the requested file path as a basename and writes one chart per person with a stable identity hash",
		Assets:      []string{"<base>_<rune-safe-person-slug>-<hash><ext>"},
	},
	"burndown-repository": {
		Kind:        outputAssetDir,
		Description: "writes one PNG and SVG per repository using stable identity hashes",
		Assets:      []string{"burndown-repository_<rune-safe-repository-slug>-<hash>.png", "burndown-repository_<rune-safe-repository-slug>-<hash>.svg"},
	},
	"burndown-repos-combined": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested combined repository chart file",
		Assets:      []string{"<output>"},
	},
	"overwrites-matrix": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested matrix chart file",
		Assets:      []string{"<output>"},
	},
	"ownership": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested ownership chart file",
		Assets:      []string{"<output>"},
	},
	"couples-files": {
		Kind:        outputAssetDir,
		Description: "writes TensorBoard-style file coupling projector assets into the requested directory",
		Assets:      []string{"files_vocabulary.tsv", "files_vectors.tsv", "files_metadata.tsv"},
	},
	"couples-people": {
		Kind:        outputAssetDir,
		Description: "writes TensorBoard-style people coupling projector assets into the requested directory",
		Assets:      []string{"people_vocabulary.tsv", "people_vectors.tsv", "people_metadata.tsv"},
	},
	"couples-shotness": {
		Kind:        outputAssetDir,
		Description: "writes shotness coupling charts into the requested directory",
		Assets:      []string{"shotness_coupling_heatmap.png", "shotness_coupling_heatmap.svg", "top_shotness_coupling_pairs.png", "top_shotness_coupling_pairs.svg"},
	},
	"shotness": {
		Kind:        outputAssetDir,
		Description: "prints statistics and writes PNG/SVG charts into the requested directory",
		Assets:      []string{"shotness.png", "shotness.svg"},
	},
	"devs": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested developer chart file",
		Assets:      []string{"<output>"},
	},
	"devs-efforts": {
		Kind:        outputSingleFile,
		Description: "writes the requested developer efforts chart file; --devs-efforts-detail also writes a Go-only productivity ranking sibling",
		Assets:      []string{"<output>", "<base>_productivity_ranking<ext> (only with --devs-efforts-detail)"},
	},
	"old-vs-new": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested additions-vs-changes chart file",
		Assets:      []string{"<output>"},
	},
	"languages": {
		Kind:        outputSingleFile,
		Description: "writes the requested chart file; direct directory calls write languages.png and languages.svg",
		Assets:      []string{"<output>"},
	},
	"temporal-activity": {
		Kind:        outputCompanions,
		Description: "writes the Python labours sibling set: eight stacked bar charts (weekdays/hours/months/weeks × commits/lines) plus two weekday×hour heatmaps",
		Assets: []string{
			"<base>_weekdays_commits<ext>", "<base>_hours_commits<ext>", "<base>_months_commits<ext>", "<base>_weeks_commits<ext>",
			"<base>_weekdays_lines<ext>", "<base>_hours_lines<ext>", "<base>_months_lines<ext>", "<base>_weeks_lines<ext>",
			"<base>_heatmap_commits<ext>", "<base>_heatmap_lines<ext>",
		},
	},
	"devs-parallel": {
		Kind:        outputSingleFile,
		Description: "writes the parallel-coordinates developer chart and prints a parallel-development summary; --devs-parallel-detail also writes a Go-only concurrency timeline sibling",
		Assets:      []string{"<output>", "<base>_concurrency_timeline<ext> (only with --devs-parallel-detail)"},
	},
	"run-times": {
		Kind:        outputSingleFile,
		Description: "prints the runtime summary; the Go-only breakdown chart is written only with --run-times-detail (Python parity: text-only)",
		Assets:      []string{"<output> (only with --run-times-detail)"},
	},
	"bus-factor": {
		Kind:        outputCompanions,
		Description: "writes timeline, gauge, and subsystem bus-factor sibling charts",
		Assets:      []string{"<base>_timeline<ext>", "<base>_gauge<ext>", "<base>_subsystems<ext>"},
	},
	"ownership-concentration": {
		Kind:        outputCompanions,
		Description: "writes timeline and subsystem ownership concentration charts as sibling files",
		Assets:      []string{"<base>_timeline<ext>", "<base>_subsystems<ext>"},
	},
	"knowledge-diffusion": {
		Kind:        outputCompanions,
		Description: "writes distribution, silos, and Lorenz-curve sibling charts; --knowledge-diffusion-detail also writes a Go-only trend sibling",
		Assets:      []string{"<base>_distribution<ext>", "<base>_silos<ext>", "<base>_lorenz<ext>", "<base>_trend<ext> (only with --knowledge-diffusion-detail)"},
	},
	"hotspot-risk": {
		Kind:        outputCompanions,
		Description: "writes the requested risk chart plus a TSV table sibling",
		Assets:      []string{"<output>", "<base>_table.tsv"},
	},
	"sentiment": {
		Kind:        outputAssetDir,
		Description: "writes sentiment charts into the requested directory",
		Assets:      []string{"sentiment-overview.png", "sentiment-overview.svg", "sentiment-developers.png", "sentiment-developers.svg", "sentiment-languages.png", "sentiment-languages.svg"},
	},
	"refactoring-proxy": {
		Kind:        outputSingleFile,
		Description: "writes exactly the requested refactoring proxy chart file",
		Assets:      []string{"<output>"},
	},
}

func planModeOutput(baseOutput, mode string, modeCount int) string {
	if outputConventionFor(mode).Kind == outputAssetDir {
		return planMultiAssetModeOutput(baseOutput)
	}

	format := DetectOutputFormat(baseOutput)
	if baseOutput == "" {
		return GenerateOutputPath(mode, format)
	}

	if isDirectoryPath(baseOutput) {
		return GenerateOutputPath(filepath.Join(baseOutput, mode), DetectOutputFormat(""))
	}

	if modeCount > 1 {
		if filepath.Ext(baseOutput) == "" {
			return GenerateOutputPath(filepath.Join(baseOutput, mode), DetectOutputFormat(""))
		}
		return GenerateOutputPath(filepath.Join(filepath.Dir(baseOutput), mode), format)
	}

	return GenerateOutputPath(baseOutput, format)
}

func validateModeOutputPlan(baseOutput string, modes []string) error {
	if strings.HasSuffix(strings.ToLower(baseOutput), ".json") {
		return nil
	}
	paths := make([]string, 0, len(modes))
	for _, mode := range modes {
		if outputConventionFor(mode).Kind == outputAssetDir {
			continue
		}
		paths = append(paths, planModeOutput(baseOutput, mode, len(modes)))
	}
	return outputpath.RejectDuplicates(paths)
}

func planMultiAssetModeOutput(baseOutput string) string {
	if baseOutput == "" {
		return "."
	}
	if outputLooksLikeFile(baseOutput) {
		return filepath.Dir(baseOutput)
	}
	return baseOutput
}

func isMultiAssetMode(mode string) bool {
	return outputConventionFor(mode).Kind == outputAssetDir
}

func outputConventionFor(mode string) outputConvention {
	if convention, ok := modeOutputConventions[mode]; ok {
		return convention
	}
	return outputConvention{
		Kind:        outputSingleFile,
		Description: "writes exactly the requested chart file",
		Assets:      []string{"<output>"},
	}
}

func isDirectoryPath(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func outputLooksLikeFile(path string) bool {
	if isDirectoryPath(path) {
		return false
	}
	return filepath.Ext(path) != ""
}
