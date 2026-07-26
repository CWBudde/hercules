package modes

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// CouplesPeople generates people coupling embeddings (Python-compatible)
func CouplesPeople(reader readers.Reader, output string) error {
	quiet := viper.GetBool("quiet")
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
		"people", output, peopleNames, couplingMatrix, outlierThreshold,
	); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to write people embeddings: %v", err)
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
) error {
	tmpdir := viper.GetString("tmpdir")
	if len(index) == 0 || matrix.Rows == 0 {
		return fmt.Errorf("empty matrix or index")
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	vocabFile := filepath.Join(outputDir, prefix+"_vocabulary.tsv")
	if err := writeSparseVocabularyFile(vocabFile, index); err != nil {
		return fmt.Errorf("failed to write vocabulary file: %v", err)
	}
	vectorFile := filepath.Join(outputDir, prefix+"_vectors.tsv")
	if err := writeSparseVectorFile(vectorFile, index, matrix, outlierThreshold); err != nil {
		return fmt.Errorf("failed to write vector file: %v", err)
	}

	disableProjector := viper.GetBool("disable-projector")
	if !disableProjector {
		metadataFile := filepath.Join(outputDir, prefix+"_metadata.tsv")
		if err := writeSparseMetadataFile(
			metadataFile, index, matrix, outlierThreshold,
		); err != nil {
			return fmt.Errorf("failed to write metadata file: %v", err)
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
		normSquared := 0.0
		for _, value := range values {
			capped := cappedCouplingValue(value, threshold)
			normSquared += capped * capped
		}
		norm := math.Sqrt(normSquared)
		position := 0
		for column := range index {
			value := 0.0
			if position < len(columns) && columns[position] == column {
				value = cappedCouplingValue(values[position], threshold)
				position++
			}
			if norm > 0 {
				value /= norm
			}
			if column > 0 {
				if _, err := file.WriteString("\t"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(file, "%.6f", value); err != nil {
				return err
			}
		}
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	return nil
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
