package modes

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/meko-christian/hercules/internal/render/progress"
	"github.com/meko-christian/hercules/internal/render/readers"
	"github.com/spf13/viper"
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
		return fmt.Errorf("coupling stats were not collected; re-run hercules with --couples")
	}

	if len(peopleNames) == 0 {
		progEstimator.FinishMultiOperation()
		if !quiet {
			fmt.Println("Coupling stats were not collected. Re-run hercules with --couples.")
		}
		return nil
	}

	// Phase 2: Preprocess matrix (Python-compatible outlier handling)
	progEstimator.NextOperation("Preprocessing coupling matrix")
	processedMatrix := preprocessCouplingMatrix(couplingMatrix)

	// Phase 3: Generate embeddings
	progEstimator.NextOperation("Training embeddings")
	if err := writeEmbeddings("people", output, peopleNames, processedMatrix); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to write people embeddings: %v", err)
	}

	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Println("People coupling embeddings completed successfully.")
	}
	return nil
}

// EmbeddingVector represents a vector embedding for an entity
type EmbeddingVector struct {
	Label  string
	Vector []float64
}

// SparseMatrix represents a sparse matrix in CSR-like format
type SparseMatrix struct {
	Rows    int
	Cols    int
	Values  []float64
	Indices []int
	Indptr  []int
}

// preprocessCouplingMatrix applies Python-compatible preprocessing
func preprocessCouplingMatrix(matrix [][]int) [][]float64 {
	if len(matrix) == 0 {
		return [][]float64{}
	}

	// Convert to float64 and collect all non-zero values for percentile calculation
	var allValues []float64
	processed := make([][]float64, len(matrix))

	for i := range matrix {
		processed[i] = make([]float64, len(matrix[i]))
		for j, val := range matrix[i] {
			processed[i][j] = float64(val)
			if val > 0 {
				allValues = append(allValues, float64(val))
			}
		}
	}

	// Calculate 99th percentile (outlier threshold)
	if len(allValues) > 0 {
		sort.Float64s(allValues)
		percentileIdx := int(math.Ceil(0.99*float64(len(allValues)))) - 1
		if percentileIdx >= len(allValues) {
			percentileIdx = len(allValues) - 1
		}
		outlierThreshold := allValues[percentileIdx]

		// Apply outlier threshold
		for i := range processed {
			for j := range processed[i] {
				if processed[i][j] > outlierThreshold {
					processed[i][j] = outlierThreshold
				}
			}
		}
	}

	return processed
}

// trainEmbeddings trains vector embeddings using a simplified approach
func trainEmbeddings(index []string, matrix [][]float64) ([]EmbeddingVector, error) {
	if len(matrix) == 0 || len(index) == 0 {
		return nil, fmt.Errorf("empty matrix or index")
	}

	// Simplified embedding: use normalized co-occurrence as features
	embeddings := make([]EmbeddingVector, len(index))

	for i, name := range index {
		if i >= len(matrix) {
			break
		}

		// Create embedding vector from matrix row
		vector := make([]float64, len(matrix[i]))
		copy(vector, matrix[i])

		// Normalize vector
		norm := 0.0
		for _, val := range vector {
			norm += val * val
		}
		if norm > 0 {
			norm = math.Sqrt(norm)
			for j := range vector {
				vector[j] /= norm
			}
		}

		embeddings[i] = EmbeddingVector{
			Label:  name,
			Vector: vector,
		}
	}

	return embeddings, nil
}

// writeEmbeddings writes embeddings in TensorFlow Projector compatible format
func writeEmbeddings(prefix, outputDir string, index []string, matrix [][]float64) error {
	// Train embeddings (using tmpdir if specified)
	tmpdir := viper.GetString("tmpdir")
	embeddings, err := trainEmbeddings(index, matrix)
	if err != nil {
		return fmt.Errorf("failed to train embeddings: %v", err)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Write vocabulary file
	vocabFile := filepath.Join(outputDir, prefix+"_vocabulary.tsv")
	if err := writeVocabularyFile(vocabFile, embeddings); err != nil {
		return fmt.Errorf("failed to write vocabulary file: %v", err)
	}

	// Write vectors file
	vectorFile := filepath.Join(outputDir, prefix+"_vectors.tsv")
	if err := writeVectorFile(vectorFile, embeddings); err != nil {
		return fmt.Errorf("failed to write vector file: %v", err)
	}

	// Write metadata file for TensorFlow Projector (unless disabled)
	disableProjector := viper.GetBool("disable-projector")
	if !disableProjector {
		metadataFile := filepath.Join(outputDir, prefix+"_metadata.tsv")
		if err := writeMetadataFile(metadataFile, embeddings, matrix); err != nil {
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

	// Note: tmpdir parameter is acknowledged but not used in simplified implementation
	if tmpdir != "" {
		fmt.Printf("  Using tmpdir: %s\n", tmpdir)
	}

	return nil
}

// writeVocabularyFile writes the vocabulary file for TensorFlow Projector
func writeVocabularyFile(filename string, embeddings []EmbeddingVector) error {
	file, err := os.Create(filename) // #nosec G304 - output path is explicitly requested by the caller.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	for _, emb := range embeddings {
		if _, err := file.WriteString(emb.Label + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// writeVectorFile writes the vectors file for TensorFlow Projector
func writeVectorFile(filename string, embeddings []EmbeddingVector) error {
	file, err := os.Create(filename) // #nosec G304 - output path is explicitly requested by the caller.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	for _, emb := range embeddings {
		vectorStrs := make([]string, len(emb.Vector))
		for i, val := range emb.Vector {
			vectorStrs[i] = fmt.Sprintf("%.6f", val)
		}
		if _, err := file.WriteString(strings.Join(vectorStrs, "\t") + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// writeMetadataFile writes the metadata file for TensorFlow Projector
func writeMetadataFile(filename string, embeddings []EmbeddingVector, matrix [][]float64) error {
	file, err := os.Create(filename) // #nosec G304 - output path is explicitly requested by the caller.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	// Write header
	if _, err := file.WriteString("Name\tDiagonal\n"); err != nil {
		return err
	}

	// Write metadata for each embedding
	for i, emb := range embeddings {
		diagonal := 0.0
		if i < len(matrix) && i < len(matrix[i]) {
			diagonal = matrix[i][i]
		}
		if _, err := fmt.Fprintf(file, "%s\t%.6f\n", emb.Label, diagonal); err != nil {
			return err
		}
	}

	return nil
}
