package visual

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// SimilarityMetrics holds various similarity comparison results.
type SimilarityMetrics struct {
	HistogramIntersection float64 // 0.0 to 1.0, higher is more similar
	SSIM                  float64 // Structural Similarity Index
	ColorDistanceRMS      float64 // Root Mean Square color distance
	OverallSimilarity     float64 // Weighted combination of metrics
}

// ValidationLevel defines different similarity thresholds.
type ValidationLevel string

const (
	ValidationStrict   ValidationLevel = "strict"   // >95% similarity
	ValidationStandard ValidationLevel = "standard" // >90% similarity
	ValidationLenient  ValidationLevel = "lenient"  // >85% similarity
)

// SimilarityThresholds defines the minimum similarity scores for each level.
var SimilarityThresholds = map[ValidationLevel]float64{
	ValidationStrict:   0.95,
	ValidationStandard: 0.90,
	ValidationLenient:  0.85,
}

// CompareImages performs comprehensive similarity analysis between two images.
func CompareImages(img1Path, img2Path string) (*SimilarityMetrics, error) {
	// Load images
	img1, err := loadImage(img1Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load first image: %w", err)
	}

	img2, err := loadImage(img2Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load second image: %w", err)
	}

	// Ensure images have same dimensions for comparison
	bounds1 := img1.Bounds()
	bounds2 := img2.Bounds()

	if bounds1.Dx() != bounds2.Dx() || bounds1.Dy() != bounds2.Dy() {
		return nil, fmt.Errorf("image dimensions don't match: %dx%d vs %dx%d",
			bounds1.Dx(), bounds1.Dy(), bounds2.Dx(), bounds2.Dy())
	}

	// Calculate similarity metrics
	metrics := &SimilarityMetrics{}

	metrics.HistogramIntersection = calculateHistogramIntersection(img1, img2)
	metrics.SSIM = calculateSSIM(img1, img2)
	metrics.ColorDistanceRMS = calculateColorDistanceRMS(img1, img2)

	// Calculate weighted overall similarity
	metrics.OverallSimilarity = calculateOverallSimilarity(metrics)

	return metrics, nil
}

// IsValidationPassing checks if the similarity meets the specified validation level.
func (m *SimilarityMetrics) IsValidationPassing(level ValidationLevel) bool {
	threshold, exists := SimilarityThresholds[level]
	if !exists {
		threshold = SimilarityThresholds[ValidationStandard]
	}

	return m.OverallSimilarity >= threshold
}

// GetDetailedReport returns a human-readable report of the similarity analysis.
func (m *SimilarityMetrics) GetDetailedReport(level ValidationLevel) string {
	threshold := SimilarityThresholds[level]
	passed := m.IsValidationPassing(level)

	status := "PASS"
	if !passed {
		status = "FAIL"
	}

	return fmt.Sprintf(`Visual Similarity Analysis Report
=====================================
Validation Level: %s (threshold: %.1f%%)
Status: %s

Detailed Metrics:
- Histogram Intersection: %.2f%% (color distribution similarity)
- SSIM: %.2f%% (structural similarity) 
- Color Distance RMS: %.3f (lower is better)
- Overall Similarity: %.2f%%

Assessment: %s
`, string(level), threshold*100, status,
		m.HistogramIntersection*100,
		m.SSIM*100,
		m.ColorDistanceRMS,
		m.OverallSimilarity*100,
		getAssessment(m, passed))
}

// loadImage loads an image from file path.
func loadImage(path string) (image.Image, error) {
	file, err := os.Open(path) // #nosec G304 - visual test path is provided by test setup.
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = file.Close() }()

	img, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %w", err)
	}

	return img, nil
}

// calculateHistogramIntersection computes the intersection of color histograms.
func calculateHistogramIntersection(img1, img2 image.Image) float64 {
	hist1 := buildColorHistogram(img1)
	hist2 := buildColorHistogram(img2)

	intersection := 0.0
	total1 := 0.0
	total2 := 0.0

	// Calculate histogram intersection using min() approach
	for r := 0; r < 256; r += 8 { // Sample every 8th value for efficiency
		for g := 0; g < 256; g += 8 {
			for b := 0; b < 256; b += 8 {
				key := fmt.Sprintf("%d,%d,%d", r, g, b)
				val1 := hist1[key]
				val2 := hist2[key]

				intersection += math.Min(val1, val2)
				total1 += val1
				total2 += val2
			}
		}
	}

	// Normalize by the smaller histogram
	totalMin := math.Min(total1, total2)
	if totalMin == 0 {
		return 0.0
	}

	return intersection / totalMin
}

// buildColorHistogram creates a color histogram from an image.
func buildColorHistogram(img image.Image) map[string]float64 {
	histogram := make(map[string]float64)
	bounds := img.Bounds()
	totalPixels := 0.0

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)

			// Quantize colors to reduce histogram size
			r := (c.R / 8) * 8
			g := (c.G / 8) * 8
			b := (c.B / 8) * 8

			key := fmt.Sprintf("%d,%d,%d", r, g, b)
			histogram[key]++
			totalPixels++
		}
	}

	// Normalize histogram
	for key, count := range histogram {
		histogram[key] = count / totalPixels
	}

	return histogram
}

// calculateSSIM computes Structural Similarity Index between two images.
func calculateSSIM(img1, img2 image.Image) float64 {
	bounds := img1.Bounds()

	var meanX, meanY, varX, varY, covXY float64
	var n float64

	// Calculate means
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c1 := color.GrayModel.Convert(img1.At(x, y)).(color.Gray)
			c2 := color.GrayModel.Convert(img2.At(x, y)).(color.Gray)

			val1 := float64(c1.Y)
			val2 := float64(c2.Y)

			meanX += val1
			meanY += val2
			n++
		}
	}

	meanX /= n
	meanY /= n

	// Calculate variances and covariance
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c1 := color.GrayModel.Convert(img1.At(x, y)).(color.Gray)
			c2 := color.GrayModel.Convert(img2.At(x, y)).(color.Gray)

			val1 := float64(c1.Y)
			val2 := float64(c2.Y)

			diffX := val1 - meanX
			diffY := val2 - meanY

			varX += diffX * diffX
			varY += diffY * diffY
			covXY += diffX * diffY
		}
	}

	varX /= n - 1
	varY /= n - 1
	covXY /= n - 1

	// SSIM constants for numerical stability
	const (
		c1 = 6.5025  // (0.01 * 255)^2
		c2 = 58.5225 // (0.03 * 255)^2
	)

	// Calculate SSIM
	numerator := (2*meanX*meanY + c1) * (2*covXY + c2)
	denominator := (meanX*meanX + meanY*meanY + c1) * (varX + varY + c2)

	if denominator == 0 {
		return 1.0 // Identical images
	}

	ssim := numerator / denominator

	return math.Max(0, ssim) // Ensure non-negative result
}

// calculateColorDistanceRMS computes RMS color distance between images.
func calculateColorDistanceRMS(img1, img2 image.Image) float64 {
	bounds := img1.Bounds()
	var totalDist float64
	var n float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c1 := color.RGBAModel.Convert(img1.At(x, y)).(color.RGBA)
			c2 := color.RGBAModel.Convert(img2.At(x, y)).(color.RGBA)

			// Calculate Euclidean distance in RGB space
			dr := float64(c1.R) - float64(c2.R)
			dg := float64(c1.G) - float64(c2.G)
			db := float64(c1.B) - float64(c2.B)

			dist := math.Sqrt(dr*dr + dg*dg + db*db)
			totalDist += dist * dist
			n++
		}
	}

	return math.Sqrt(totalDist / n)
}

// calculateOverallSimilarity computes a weighted combination of all metrics.
func calculateOverallSimilarity(m *SimilarityMetrics) float64 {
	// Weights based on importance for chart validation
	const (
		histogramWeight = 0.4 // Color distribution is important
		ssimWeight      = 0.4 // Structural similarity is important
		colorWeight     = 0.2 // RMS color distance (inverted)
	)

	// Normalize color distance to 0-1 scale (lower distance = higher similarity)
	// Assuming max reasonable RMS distance is 100 for 8-bit RGB
	colorSimilarity := math.Max(0, 1.0-m.ColorDistanceRMS/100.0)

	overall := histogramWeight*m.HistogramIntersection +
		ssimWeight*m.SSIM +
		colorWeight*colorSimilarity

	return math.Min(1.0, overall) // Cap at 1.0
}

// getAssessment provides human-readable assessment of the similarity result.
func getAssessment(m *SimilarityMetrics, passed bool) string {
	if passed {
		switch {
		case m.OverallSimilarity >= 0.98:
			return "Images are nearly identical - excellent compatibility"
		case m.OverallSimilarity >= 0.95:
			return "Images are very similar - minor rendering differences only"
		default:
			return "Images are adequately similar - functional compatibility maintained"
		}
	}

	switch {
	case m.OverallSimilarity >= 0.80:
		return "Images are similar but below threshold - review for acceptable differences"
	case m.OverallSimilarity >= 0.60:
		return "Images show significant differences - investigate chart generation logic"
	default:
		return "Images are substantially different - major compatibility issues detected"
	}
}
