package modes

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// CouplesPeople generates people coupling embeddings (Python-compatible).
func CouplesPeople(reader readers.Reader, output string) error {
	return CouplesPeopleWithOptions(reader, output, defaultOptions())
}

func CouplesPeopleWithOptions(reader readers.Reader, output string, opts Options) error {
	quiet := opts.Quiet
	progEstimator := progress.NewProgressEstimator(!quiet)

	totalPhases := 3 // data extraction, preprocessing, embeddings
	progEstimator.StartMultiOperation(totalPhases, "People Coupling Analysis")

	// Phase 1: Extract people coupling data
	progEstimator.NextOperation("Extracting people coupling data")

	peopleNames, couplingMatrix, err := reader.GetPeopleCooccurrence()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("get people coupling data: %w", err)
	}

	if len(peopleNames) == 0 {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("%w: people coupling", readers.ErrAnalysisMissing)
	}

	// Phase 2: Preprocess matrix (Python-compatible outlier handling)
	progEstimator.NextOperation("Preprocessing coupling matrix")

	outlierThreshold := sparseCouplingOutlierThreshold(couplingMatrix)

	// Phase 3: Generate embeddings
	progEstimator.NextOperation("Training embeddings")

	if err := writeSparseEmbeddings(
		"people", output, peopleNames, couplingMatrix, outlierThreshold, opts,
	); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to write people embeddings: %w", err)
	}

	progEstimator.FinishMultiOperation()

	if !quiet {
		fmt.Println("People coupling embeddings completed successfully.")
	}

	return nil
}

// sparseCouplingOutlierThreshold computes the historical 99th-percentile cap
// using only O(nonzero) temporary memory.
func sparseCouplingOutlierThreshold(matrix readers.SparseMatrix) int {
	values := make([]int, 0, matrix.NonZeroCount())
	for _, value := range matrix.Values {
		if value > 0 {
			values = append(values, value)
		}
	}

	if len(values) == 0 {
		return 0
	}

	sort.Ints(values)
	percentileIndex := int(math.Ceil(0.99*float64(len(values)))) - 1

	return values[percentileIndex]
}

func cappedCouplingValue(value, threshold int) float64 {
	if threshold > 0 && value > threshold {
		return float64(threshold)
	}

	return float64(value)
}

// writeSparseEmbeddings streams dense TensorFlow Projector rows directly from
// CSR. The file format is necessarily dense, but renderer memory remains
// O(nonzero+entities) instead of O(entities²).
func writeSparseEmbeddings(
	prefix, outputDir string,
	index []string,
	matrix readers.SparseMatrix,
	outlierThreshold int,
	optionValues ...Options,
) error {
	opts := defaultOptions()
	if len(optionValues) > 0 {
		opts = optionValues[0]
	}

	tmpdir := opts.TempDir

	if len(index) == 0 || matrix.Rows == 0 {
		return errors.New("empty matrix or index")
	}

	err := os.MkdirAll(outputDir, 0o750)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	vocabFile := filepath.Join(outputDir, prefix+"_vocabulary.tsv")

	err = writeSparseVocabularyFile(vocabFile, index)
	if err != nil {
		return fmt.Errorf("failed to write vocabulary file: %w", err)
	}

	vectorFile := filepath.Join(outputDir, prefix+"_vectors.tsv")

	err = writeSparseVectorFile(vectorFile, index, matrix, outlierThreshold)
	if err != nil {
		return fmt.Errorf("failed to write vector file: %w", err)
	}

	disableProjector := opts.DisableProjector
	if !disableProjector {
		metadataFile := filepath.Join(outputDir, prefix+"_metadata.tsv")

		err := writeSparseMetadataFile(
			metadataFile, index, matrix, outlierThreshold,
		)
		if err != nil {
			return fmt.Errorf("failed to write metadata file: %w", err)
		}

		fmt.Printf("Embeddings written to:\n")
		fmt.Printf("  Vocabulary: %s\n", vocabFile)
		fmt.Printf("  Vectors: %s\n", vectorFile)
		fmt.Printf("  Metadata: %s\n", metadataFile)
	} else {
		fmt.Printf("Embeddings written to:\n")
		fmt.Printf("  Vocabulary: %s\n", vocabFile)
		fmt.Printf("  Vectors: %s\n", vectorFile)
		fmt.Printf("  (Projector files disabled)\n")
	}

	if tmpdir != "" {
		fmt.Printf("  Using tmpdir: %s\n", tmpdir)
	}

	return nil
}

func writeSparseVocabularyFile(filename string, index []string) error {
	file, err := os.Create(filename) // #nosec G304 -- caller explicitly chooses the output path.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	for _, label := range index {
		if _, err := file.WriteString(label + "\n"); err != nil {
			return err
		}
	}

	return nil
}

func writeSparseVectorFile(
	filename string,
	index []string,
	matrix readers.SparseMatrix,
	threshold int,
) error {
	file, err := os.Create(filename) // #nosec G304 -- caller explicitly chooses the output path.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	for row := range index {
		columns, values := matrix.Row(row)

		err := writeNormalizedSparseRow(file, len(index), columns, values, threshold)
		if err != nil {
			return err
		}
	}

	return nil
}

func writeNormalizedSparseRow(
	writer io.Writer,
	width int,
	columns, values []int,
	threshold int,
) error {
	norm := sparseRowNorm(values, threshold)
	position := 0

	for column := range width {
		value := 0.0
		if position < len(columns) && columns[position] == column {
			value = cappedCouplingValue(values[position], threshold)
			position++
		}

		if norm > 0 {
			value /= norm
		}

		err := writeSparseRowValue(writer, column, value)
		if err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(writer)

	return err
}

func sparseRowNorm(values []int, threshold int) float64 {
	normSquared := 0.0

	for _, value := range values {
		capped := cappedCouplingValue(value, threshold)
		normSquared += capped * capped
	}

	return math.Sqrt(normSquared)
}

func writeSparseRowValue(writer io.Writer, column int, value float64) error {
	if column > 0 {
		if _, err := fmt.Fprint(writer, "\t"); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(writer, "%.6f", value)

	return err
}

func writeSparseMetadataFile(
	filename string,
	index []string,
	matrix readers.SparseMatrix,
	threshold int,
) error {
	file, err := os.Create(filename) // #nosec G304 -- caller explicitly chooses the output path.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if _, err := file.WriteString("Name\tDiagonal\n"); err != nil {
		return err
	}

	for row, label := range index {
		diagonal := cappedCouplingValue(matrix.At(row, row), threshold)
		if _, err := fmt.Fprintf(file, "%s\t%.6f\n", label, diagonal); err != nil {
			return err
		}
	}

	return nil
}
