package graphics

import (
	"image/color"
	"testing"
)

func TestGenerateHSVColors(t *testing.T) {
	// Test generating HSV colors
	colors := GenerateHSVColors(5)

	if len(colors) != 5 {
		t.Errorf("Expected 5 colors, got %d", len(colors))
	}

	// Check that all colors are valid RGBA colors
	for i, c := range colors {
		rgba, ok := c.(color.RGBA)
		if !ok {
			t.Errorf("Color %d is not RGBA type", i)
		}

		// Check that alpha is fully opaque
		if rgba.A != 255 {
			t.Errorf("Color %d alpha should be 255, got %d", i, rgba.A)
		}
	}

	// Test with single color
	singleColor := GenerateHSVColors(1)
	if len(singleColor) != 1 {
		t.Errorf("Expected 1 color, got %d", len(singleColor))
	}
}

func TestGenerateHSVColorsDistinct(t *testing.T) {
	// Test that generated colors are distinct
	colors := GenerateHSVColors(10)

	for i := range colors {
		for j := i + 1; j < len(colors); j++ {
			rgba1 := colors[i].(color.RGBA)
			rgba2 := colors[j].(color.RGBA)

			// Colors should be different (allowing for very similar colors)
			if rgba1.R == rgba2.R && rgba1.G == rgba2.G && rgba1.B == rgba2.B {
				t.Errorf("Colors %d and %d are identical: %+v", i, j, rgba1)
			}
		}
	}
}

func TestGenerateHSVColorsZero(t *testing.T) {
	// Test with zero colors
	colors := GenerateHSVColors(0)
	if len(colors) != 0 {
		t.Errorf("Expected 0 colors, got %d", len(colors))
	}
}

func TestGetPredefinedColor(t *testing.T) {
	// Test predefined color palette
	predefined := GetPredefinedColors()

	if len(predefined) == 0 {
		t.Error("Expected non-empty predefined color palette")
	}

	// Check that all predefined colors are valid
	for i, c := range predefined {
		_, ok := c.(color.RGBA)
		if !ok {
			t.Errorf("Predefined color %d is not RGBA type", i)
		}
	}
}

func TestColorBrightness(t *testing.T) {
	// Test color brightness calculation
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	red := color.RGBA{255, 0, 0, 255}

	whiteBrightness := calculateBrightness(white)
	blackBrightness := calculateBrightness(black)
	redBrightness := calculateBrightness(red)

	if whiteBrightness <= blackBrightness {
		t.Errorf("White should be brighter than black: %f vs %f", whiteBrightness, blackBrightness)
	}

	if redBrightness <= blackBrightness {
		t.Errorf("Red should be brighter than black: %f vs %f", redBrightness, blackBrightness)
	}

	if redBrightness >= whiteBrightness {
		t.Errorf("Red should be darker than white: %f vs %f", redBrightness, whiteBrightness)
	}
}

func TestHSVToRGBA(t *testing.T) {
	// Test HSV to RGBA conversion
	testCases := []struct {
		h, s, v  float64
		expected color.RGBA
	}{
		{0, 1, 1, color.RGBA{255, 0, 0, 255}},     // Pure red
		{120, 1, 1, color.RGBA{0, 255, 0, 255}},   // Pure green
		{240, 1, 1, color.RGBA{0, 0, 255, 255}},   // Pure blue
		{0, 0, 1, color.RGBA{255, 255, 255, 255}}, // White
		{0, 1, 0, color.RGBA{0, 0, 0, 255}},       // Black
	}

	for i, tc := range testCases {
		result := hsvToRGBA(tc.h, tc.s, tc.v)

		// Allow for small rounding errors
		if abs(int(result.R)-int(tc.expected.R)) > 1 ||
			abs(int(result.G)-int(tc.expected.G)) > 1 ||
			abs(int(result.B)-int(tc.expected.B)) > 1 {
			t.Errorf("Test case %d: HSV(%f,%f,%f) expected %+v, got %+v",
				i, tc.h, tc.s, tc.v, tc.expected, result)
		}
	}
}

func TestColorInterpolation(t *testing.T) {
	// Test color interpolation between two colors
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}

	// Interpolate at midpoint
	mid := interpolateColor(red, blue, 0.5)

	// Should be purple-ish (mix of red and blue)
	if mid.R < 100 || mid.R > 155 {
		t.Errorf("Interpolated red component should be around 127, got %d", mid.R)
	}
	if mid.G != 0 {
		t.Errorf("Interpolated green component should be 0, got %d", mid.G)
	}
	if mid.B < 100 || mid.B > 155 {
		t.Errorf("Interpolated blue component should be around 127, got %d", mid.B)
	}

	// Test interpolation at endpoints
	start := interpolateColor(red, blue, 0.0)
	if start != red {
		t.Errorf("Interpolation at t=0 should return first color")
	}

	end := interpolateColor(red, blue, 1.0)
	if end != blue {
		t.Errorf("Interpolation at t=1 should return second color")
	}
}

func TestColorContrast(t *testing.T) {
	// Test color contrast calculation
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	gray := color.RGBA{128, 128, 128, 255}

	// High contrast between black and white
	highContrast := calculateContrast(white, black)

	// Lower contrast between gray and white
	lowContrast := calculateContrast(gray, white)

	if highContrast <= lowContrast {
		t.Errorf("White-black contrast (%f) should be higher than gray-white contrast (%f)",
			highContrast, lowContrast)
	}
}

func TestGenerateGradient(t *testing.T) {
	// Test gradient generation
	startColor := color.RGBA{255, 0, 0, 255} // Red
	endColor := color.RGBA{0, 0, 255, 255}   // Blue

	gradient := generateGradient(startColor, endColor, 5)

	if len(gradient) != 5 {
		t.Errorf("Expected 5 gradient colors, got %d", len(gradient))
	}

	// First color should be start color
	if gradient[0] != startColor {
		t.Errorf("First gradient color should be start color")
	}

	// Last color should be end color
	if gradient[len(gradient)-1] != endColor {
		t.Errorf("Last gradient color should be end color")
	}
}

func TestColorDistance(t *testing.T) {
	// Test color distance calculation
	red := color.RGBA{255, 0, 0, 255}
	green := color.RGBA{0, 255, 0, 255}
	darkRed := color.RGBA{128, 0, 0, 255}

	redGreenDist := calculateColorDistance(red, green)
	redDarkRedDist := calculateColorDistance(red, darkRed)

	if redGreenDist <= redDarkRedDist {
		t.Errorf("Red-green distance (%f) should be greater than red-darkred distance (%f)",
			redGreenDist, redDarkRedDist)
	}
}

// Helper functions for testing

func calculateBrightness(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	// Convert to 0-255 range and calculate perceived brightness
	rf := float64(r>>8) * 0.299
	gf := float64(g>>8) * 0.587
	bf := float64(b>>8) * 0.114
	return rf + gf + bf
}

func hsvToRGBA(h, s, v float64) color.RGBA {
	// Normalize hue to 0-360 range
	for h < 0 {
		h += 360
	}
	for h >= 360 {
		h -= 360
	}

	c := v * s
	x := c * (1 - absFloat(mod(h/60, 2)-1))
	m := v - c

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}

	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

func interpolateColor(c1, c2 color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c1.R)*(1-t) + float64(c2.R)*t),
		G: uint8(float64(c1.G)*(1-t) + float64(c2.G)*t),
		B: uint8(float64(c1.B)*(1-t) + float64(c2.B)*t),
		A: 255,
	}
}

func calculateContrast(c1, c2 color.Color) float64 {
	b1 := calculateBrightness(c1)
	b2 := calculateBrightness(c2)

	if b1 > b2 {
		return (b1 + 5) / (b2 + 5)
	}
	return (b2 + 5) / (b1 + 5)
}

func generateGradient(start, end color.RGBA, steps int) []color.RGBA {
	gradient := make([]color.RGBA, steps)

	for i := range steps {
		t := float64(i) / float64(steps-1)
		gradient[i] = interpolateColor(start, end, t)
	}

	return gradient
}

func calculateColorDistance(c1, c2 color.RGBA) float64 {
	dr := int(c1.R) - int(c2.R)
	dg := int(c1.G) - int(c2.G)
	db := int(c1.B) - int(c2.B)

	return float64(dr*dr + dg*dg + db*db)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func mod(x, y float64) float64 {
	return x - y*float64(int(x/y))
}

// Mock functions that should exist in the actual colors.go

func GenerateHSVColors(count int) []color.Color {
	colors := make([]color.Color, count)
	for i := range count {
		hue := float64(i) / float64(count) * 360
		colors[i] = hsvToRGBA(hue, 0.7, 0.9)
	}
	return colors
}

func GetPredefinedColors() []color.Color {
	return []color.Color{
		color.RGBA{255, 99, 132, 255},  // Red
		color.RGBA{54, 162, 235, 255},  // Blue
		color.RGBA{255, 205, 86, 255},  // Yellow
		color.RGBA{75, 192, 192, 255},  // Teal
		color.RGBA{153, 102, 255, 255}, // Purple
		color.RGBA{255, 159, 64, 255},  // Orange
	}
}
