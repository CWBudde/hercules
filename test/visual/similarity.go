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
	ValidationStrict   ValidationLevel = "strict"   // >99% similarity
	ValidationStandard ValidationLevel = "standard" // >90% similarity
	ValidationLenient  ValidationLevel = "lenient"  // >85% similarity
)

func similarityThreshold(level ValidationLevel) float64 {
	switch level {
	case ValidationStrict:
		return 0.99
	case ValidationLenient:
		return 0.85
	default:
		return 0.90
	}
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
		return nil, fmt.Errorf("%w: %dx%d vs %dx%d", errImageDimensionsMismatch,
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
	return m.OverallSimilarity >= similarityThreshold(level)
}

// GetDetailedReport returns a human-readable report of the similarity analysis.
func (m *SimilarityMetrics) GetDetailedReport(level ValidationLevel) string {
	threshold := similarityThreshold(level)
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
			pixelColor := rgbaColor(img.At(x, y))

			// Quantize colors to reduce histogram size
			red := (pixelColor.R / 8) * 8
			green := (pixelColor.G / 8) * 8
			blue := (pixelColor.B / 8) * 8

			key := fmt.Sprintf("%d,%d,%d", red, green, blue)
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
	var pixelCount float64

	// Calculate means
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			firstColor := grayColor(img1.At(x, y))
			secondColor := grayColor(img2.At(x, y))

			val1 := float64(firstColor.Y)
			val2 := float64(secondColor.Y)

			meanX += val1
			meanY += val2
			pixelCount++
		}
	}

	meanX /= pixelCount
	meanY /= pixelCount

	// Calculate variances and covariance
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			firstColor := grayColor(img1.At(x, y))
			secondColor := grayColor(img2.At(x, y))

			val1 := float64(firstColor.Y)
			val2 := float64(secondColor.Y)

			diffX := val1 - meanX
			diffY := val2 - meanY

			varX += diffX * diffX
			varY += diffY * diffY
			covXY += diffX * diffY
		}
	}

	varX /= pixelCount - 1
	varY /= pixelCount - 1
	covXY /= pixelCount - 1

	// SSIM constants for numerical stability
	const (
		luminanceStability = 6.5025  // (0.01 * 255)^2
		contrastStability  = 58.5225 // (0.03 * 255)^2
	)

	// Calculate SSIM
	numerator := (2*meanX*meanY + luminanceStability) * (2*covXY + contrastStability)
	denominator := (meanX*meanX + meanY*meanY + luminanceStability) *
		(varX + varY + contrastStability)

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
	var pixelCount float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			firstColor := rgbaColor(img1.At(x, y))
			secondColor := rgbaColor(img2.At(x, y))

			// Calculate Euclidean distance in RGB space
			dr := float64(firstColor.R) - float64(secondColor.R)
			dg := float64(firstColor.G) - float64(secondColor.G)
			db := float64(firstColor.B) - float64(secondColor.B)

			dist := math.Sqrt(dr*dr + dg*dg + db*db)
			totalDist += dist * dist
			pixelCount++
		}
	}

	return math.Sqrt(totalDist / pixelCount)
}

// calculateOverallSimilarity computes a weighted combination of all metrics.
func calculateOverallSimilarity(metrics *SimilarityMetrics) float64 {
	// Weights based on importance for chart validation
	const (
		histogramWeight = 0.4 // Color distribution is important
		ssimWeight      = 0.4 // Structural similarity is important
		colorWeight     = 0.2 // RMS color distance (inverted)
	)

	// Normalize color distance to 0-1 scale (lower distance = higher similarity)
	// Assuming max reasonable RMS distance is 100 for 8-bit RGB
	colorSimilarity := math.Max(0, 1.0-metrics.ColorDistanceRMS/100.0)

	overall := histogramWeight*metrics.HistogramIntersection +
		ssimWeight*metrics.SSIM +
		colorWeight*colorSimilarity

	return math.Min(1.0, overall) // Cap at 1.0
}

// getAssessment provides human-readable assessment of the similarity result.
func getAssessment(metrics *SimilarityMetrics, passed bool) string {
	if passed {
		switch {
		case metrics.OverallSimilarity >= 0.98:
			return "Images are nearly identical - excellent compatibility"
		case metrics.OverallSimilarity >= 0.95:
			return "Images are very similar - minor rendering differences only"
		default:
			return "Images are adequately similar - functional compatibility maintained"
		}
	}

	switch {
	case metrics.OverallSimilarity >= 0.80:
		return "Images are similar but below threshold - review for acceptable differences"
	case metrics.OverallSimilarity >= 0.60:
		return "Images show significant differences - investigate chart generation logic"
	default:
		return "Images are substantially different - major compatibility issues detected"
	}
}

func rgbaColor(value color.Color) color.RGBA {
	converted, ok := color.RGBAModel.Convert(value).(color.RGBA)
	if !ok {
		panic("RGBA color model returned an unexpected type")
	}

	return converted
}

func grayColor(value color.Color) color.Gray {
	converted, ok := color.GrayModel.Convert(value).(color.Gray)
	if !ok {
		panic("gray color model returned an unexpected type")
	}

	return converted
}
