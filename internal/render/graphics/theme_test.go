package graphics

import (
	"image/color"
	"path/filepath"
	"testing"
)

func TestThemeValidation(t *testing.T) {
	tests := []struct {
		name    string
		theme   Theme
		wantErr bool
	}{
		{
			name: "valid theme",
			theme: Theme{
				Name: "test",
				ColorPalette: []ColorRGB{
					{R: 255, G: 0, B: 0, A: 255},
				},
				Text:  TextStyle{Size: 10},
				Chart: ChartStyle{FillOpacity: 0.5},
			},
			wantErr: false,
		},
		{
			name: "empty palette",
			theme: Theme{
				Name:  "test",
				Text:  TextStyle{Size: 10},
				Chart: ChartStyle{FillOpacity: 0.5},
			},
			wantErr: true,
		},
		{
			name: "no name",
			theme: Theme{
				ColorPalette: []ColorRGB{
					{R: 255, G: 0, B: 0, A: 255},
				},
				Text:  TextStyle{Size: 10},
				Chart: ChartStyle{FillOpacity: 0.5},
			},
			wantErr: true,
		},
		{
			name: "invalid text size",
			theme: Theme{
				Name: "test",
				ColorPalette: []ColorRGB{
					{R: 255, G: 0, B: 0, A: 255},
				},
				Text:  TextStyle{Size: -1},
				Chart: ChartStyle{FillOpacity: 0.5},
			},
			wantErr: true,
		},
		{
			name: "invalid opacity",
			theme: Theme{
				Name: "test",
				ColorPalette: []ColorRGB{
					{R: 255, G: 0, B: 0, A: 255},
				},
				Text:  TextStyle{Size: 10},
				Chart: ChartStyle{FillOpacity: 1.5},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.theme.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Theme.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestColorRGBToColor(t *testing.T) {
	rgb := ColorRGB{R: 255, G: 128, B: 64, A: 200}
	expected := color.RGBA{R: 255, G: 128, B: 64, A: 200}

	result := rgb.ToColor()
	if result != expected {
		t.Errorf("ColorRGB.ToColor() = %v, want %v", result, expected)
	}
}

func TestThemeGetColorPalette(t *testing.T) {
	theme := Theme{
		ColorPalette: []ColorRGB{
			{R: 255, G: 0, B: 0, A: 255},
			{R: 0, G: 255, B: 0, A: 255},
			{R: 0, G: 0, B: 255, A: 255},
		},
	}

	palette := theme.GetColorPalette()
	if len(palette) != 3 {
		t.Errorf("GetColorPalette() returned %d colors, want 3", len(palette))
	}

	expected := []color.Color{
		color.RGBA{R: 255, G: 0, B: 0, A: 255},
		color.RGBA{R: 0, G: 255, B: 0, A: 255},
		color.RGBA{R: 0, G: 0, B: 255, A: 255},
	}

	for i, c := range palette {
		if c != expected[i] {
			t.Errorf("GetColorPalette()[%d] = %v, want %v", i, c, expected[i])
		}
	}
}

func TestThemeGetHeatColor(t *testing.T) {
	theme := Theme{
		HeatMap: HeatStyle{
			ColdColor: ColorRGB{R: 0, G: 0, B: 255, A: 255}, // Blue
			HotColor:  ColorRGB{R: 255, G: 0, B: 0, A: 255}, // Red
		},
	}

	// Test cold color (ratio = 0)
	coldColor := theme.GetHeatColor(0.0)
	expectedCold := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	if coldColor != expectedCold {
		t.Errorf("GetHeatColor(0.0) = %v, want %v", coldColor, expectedCold)
	}

	// Test hot color (ratio = 1)
	hotColor := theme.GetHeatColor(1.0)
	expectedHot := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	if hotColor != expectedHot {
		t.Errorf("GetHeatColor(1.0) = %v, want %v", hotColor, expectedHot)
	}

	// Test mid color (ratio = 0.5)
	midColor := theme.GetHeatColor(0.5)
	expectedMid := color.RGBA{R: 127, G: 0, B: 127, A: 255} // Interpolated
	if midColor != expectedMid {
		t.Errorf("GetHeatColor(0.5) = %v, want %v", midColor, expectedMid)
	}
}

func TestThemeManager(t *testing.T) {
	tm := NewThemeManager()

	// Test that built-in themes are loaded
	themes := tm.ListThemes()
	expectedThemes := []string{"default", "dark", "minimal", "vibrant"}

	if len(themes) < len(expectedThemes) {
		t.Errorf("Expected at least %d themes, got %d", len(expectedThemes), len(themes))
	}

	for _, expected := range expectedThemes {
		found := false
		for _, theme := range themes {
			if theme == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected theme '%s' not found in list", expected)
		}
	}

	// Test getting a theme
	theme, err := tm.GetTheme("default")
	if err != nil {
		t.Errorf("GetTheme('default') failed: %v", err)
	}
	if theme.Name != "default" {
		t.Errorf("GetTheme('default').Name = %s, want 'default'", theme.Name)
	}

	// Test getting non-existent theme
	_, err = tm.GetTheme("nonexistent")
	if err == nil {
		t.Error("GetTheme('nonexistent') should have returned an error")
	}
}

func TestThemeManagerSaveLoad(t *testing.T) {
	tm := NewThemeManager()

	// Create a test theme
	testTheme := Theme{
		Name: "test-theme",
		ColorPalette: []ColorRGB{
			{R: 100, G: 150, B: 200, A: 255},
		},
		Background: ColorRGB{R: 255, G: 255, B: 255, A: 255},
		Text: TextStyle{
			Size:  12,
			Color: ColorRGB{R: 0, G: 0, B: 0, A: 255},
		},
		Chart: ChartStyle{
			FillOpacity: 0.7,
		},
	}

	// Create temp directory
	tempDir := t.TempDir()
	themeFile := filepath.Join(tempDir, "test-theme.yaml")

	// Save theme
	err := tm.SaveThemeToFile(&testTheme, themeFile)
	if err != nil {
		t.Errorf("SaveThemeToFile failed: %v", err)
	}

	// Load theme
	err = tm.LoadThemeFromFile(themeFile)
	if err != nil {
		t.Errorf("LoadThemeFromFile failed: %v", err)
	}

	// Verify theme was loaded
	loadedTheme, err := tm.GetTheme("test-theme")
	if err != nil {
		t.Errorf("GetTheme after loading failed: %v", err)
	}

	if loadedTheme.Name != testTheme.Name {
		t.Errorf("Loaded theme name = %s, want %s", loadedTheme.Name, testTheme.Name)
	}

	if len(loadedTheme.ColorPalette) != len(testTheme.ColorPalette) {
		t.Errorf("Loaded theme palette length = %d, want %d",
			len(loadedTheme.ColorPalette), len(testTheme.ColorPalette))
	}
}

func TestBuiltinThemes(t *testing.T) {
	// Test that all built-in themes are valid
	for name, theme := range BuiltinThemes {
		t.Run(name, func(t *testing.T) {
			if err := theme.Validate(); err != nil {
				t.Errorf("Built-in theme '%s' is invalid: %v", name, err)
			}

			// Test that theme has at least one color
			if len(theme.ColorPalette) == 0 {
				t.Errorf("Built-in theme '%s' has no colors", name)
			}

			// Test that theme name matches key
			if theme.Name != name {
				t.Errorf("Built-in theme key '%s' doesn't match theme name '%s'", name, theme.Name)
			}
		})
	}
}
