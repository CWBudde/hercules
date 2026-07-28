package readers

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// DetectAndReadInput detects the format (if "auto"), creates the appropriate Reader, and reads the input.
func DetectAndReadInput(input, format string) (Reader, error) {
	return DetectAndReadInputWithOptions(input, format, false)
}

// DetectAndReadInputWithOptions reads input without consulting global
// configuration.
func DetectAndReadInputWithOptions(input, format string, quiet bool) (Reader, error) {
	// Open input source
	var file io.Reader
	if input == "-" {
		file = os.Stdin
	} else {
		f, err := os.Open(input) // #nosec G304 - CLI input path is intentionally user-provided.
		if err != nil {
			return nil, fmt.Errorf("error opening file %s: %w", input, err)
		}
		defer func() { _ = f.Close() }()
		file = f
	}

	// Detect format if set to "auto"
	if format == "auto" {
		var err error
		format, file, err = detectFormat(file)
		if err != nil {
			return nil, err
		}
	}

	// Create the appropriate Reader
	reader, err := createReaderWithOptions(format, quiet)
	if err != nil {
		return nil, err
	}

	// Read the input using the Reader
	if err := reader.Read(file); err != nil {
		return nil, fmt.Errorf("error reading input with %s reader: %w", format, err)
	}

	return reader, nil
}

// detectFormat inspects the input to determine the format (YAML or Protobuf).
func detectFormat(file io.Reader) (string, io.Reader, error) {
	buffer := make([]byte, 16)
	_, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return "", nil, fmt.Errorf("error reading input for format detection: %w", err)
	}

	// Rewind the file for further reading
	file = io.MultiReader(bytes.NewReader(buffer), file)

	if isYAML(buffer) {
		return "yaml", file, nil
	}
	return "pb", file, nil
}

// isYAML checks if the buffer contains YAML-specific patterns.
func isYAML(buffer []byte) bool {
	return bytes.Contains(buffer, []byte("hercules"))
}

// createReader initializes the correct Reader implementation.
func createReader(format string) (Reader, error) {
	return createReaderWithOptions(format, false)
}

func createReaderWithOptions(format string, quiet bool) (Reader, error) {
	switch strings.ToLower(format) {
	case "yaml":
		return &YamlReader{Quiet: quiet}, nil
	case "pb":
		return &ProtobufReader{Quiet: quiet}, nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// transposeMatrix transposes a 2D integer matrix (swaps rows and columns)
func transposeMatrix(matrix [][]int) [][]int {
	if len(matrix) == 0 || len(matrix[0]) == 0 {
		return [][]int{}
	}

	rows := len(matrix)
	cols := len(matrix[0])
	for _, row := range matrix {
		if len(row) != cols {
			return [][]int{}
		}
	}

	// Create transposed matrix
	transposed := make([][]int, cols)
	for i := range transposed {
		transposed[i] = make([]int, rows)
	}

	// Fill transposed matrix
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			transposed[j][i] = matrix[i][j]
		}
	}

	return transposed
}

func aggregateLanguageStats(timeSeries *DeveloperTimeSeriesData) ([]LanguageStat, error) {
	if timeSeries == nil {
		return nil, fmt.Errorf("%w: developer time series", ErrAnalysisMissing)
	}

	totals := languageTotals(timeSeries.Days)
	if len(totals) == 0 {
		return nil, fmt.Errorf("%w: language stats", ErrAnalysisMissing)
	}

	return sortedLanguageStats(totals), nil
}

func languageTotals(days map[int]map[int]DevDay) map[string]int {
	totals := make(map[string]int)

	for _, dayStats := range days {
		for _, stats := range dayStats {
			for language, values := range stats.Languages {
				if language == "" {
					continue
				}

				totals[language] += sumInts(values)
			}
		}
	}

	return totals
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}

	return total
}

func sortedLanguageStats(totals map[string]int) []LanguageStat {
	languages := make([]LanguageStat, 0, len(totals))
	for language, lines := range totals {
		languages = append(languages, LanguageStat{
			Language: language,
			Lines:    lines,
		})
	}
	sort.Slice(languages, func(i, j int) bool {
		if languages[i].Lines == languages[j].Lines {
			return languages[i].Language < languages[j].Language
		}
		return languages[i].Lines > languages[j].Lines
	})

	return languages
}
