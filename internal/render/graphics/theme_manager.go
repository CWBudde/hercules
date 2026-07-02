package graphics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ThemeManager handles theme loading and management
type ThemeManager struct {
	themes map[string]Theme
}

// NewThemeManager creates a new theme manager with built-in themes
func NewThemeManager() *ThemeManager {
	tm := &ThemeManager{
		themes: make(map[string]Theme),
	}

	// Load built-in themes
	for name, theme := range BuiltinThemes {
		tm.themes[name] = theme
	}

	return tm
}

// LoadThemeFromFile loads a theme from a YAML file
func (tm *ThemeManager) LoadThemeFromFile(filepath string) error {
	data, err := os.ReadFile(filepath) // #nosec G304 - theme config path is caller-provided by design.
	if err != nil {
		return fmt.Errorf("failed to read theme file %s: %w", filepath, err)
	}

	var theme Theme
	if err := yaml.Unmarshal(data, &theme); err != nil {
		return fmt.Errorf("failed to parse theme file %s: %w", filepath, err)
	}

	if err := theme.Validate(); err != nil {
		return fmt.Errorf("invalid theme in file %s: %w", filepath, err)
	}

	tm.themes[theme.Name] = theme
	return nil
}

// LoadThemesFromDirectory loads all theme files from a directory
func (tm *ThemeManager) LoadThemesFromDirectory(dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("failed to read theme directory %s: %w", dirPath, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fileName := entry.Name()
		if !strings.HasSuffix(strings.ToLower(fileName), ".yaml") &&
			!strings.HasSuffix(strings.ToLower(fileName), ".yml") {
			continue
		}

		fullPath := filepath.Join(dirPath, fileName)
		if err := tm.LoadThemeFromFile(fullPath); err != nil {
			// Log warning but continue loading other themes
			fmt.Printf("Warning: failed to load theme from %s: %v\n", fullPath, err)
		}
	}

	return nil
}

// GetTheme retrieves a theme by name
func (tm *ThemeManager) GetTheme(name string) (*Theme, error) {
	theme, exists := tm.themes[name]
	if !exists {
		return nil, fmt.Errorf("theme '%s' not found", name)
	}

	return &theme, nil
}

// ListThemes returns a list of available theme names
func (tm *ThemeManager) ListThemes() []string {
	names := make([]string, 0, len(tm.themes))
	for name := range tm.themes {
		names = append(names, name)
	}
	return names
}

// SetCurrentTheme sets the global current theme
func (tm *ThemeManager) SetCurrentTheme(name string) error {
	theme, err := tm.GetTheme(name)
	if err != nil {
		return err
	}

	CurrentTheme = *theme

	// Update the legacy ColorPalette for backwards compatibility
	ColorPalette = theme.GetColorPalette()

	return nil
}

// SaveThemeToFile saves a theme to a YAML file
func (tm *ThemeManager) SaveThemeToFile(theme *Theme, filepath string) error {
	data, err := yaml.Marshal(theme)
	if err != nil {
		return fmt.Errorf("failed to marshal theme: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write theme file %s: %w", filepath, err)
	}

	return nil
}

// RegisterTheme registers a new theme
func (tm *ThemeManager) RegisterTheme(theme Theme) error {
	if err := theme.Validate(); err != nil {
		return fmt.Errorf("invalid theme: %w", err)
	}

	tm.themes[theme.Name] = theme
	return nil
}

// CreateCustomTheme creates a custom theme based on an existing theme with modifications
func (tm *ThemeManager) CreateCustomTheme(baseName string, customizations map[string]interface{}) (*Theme, error) {
	base, err := tm.GetTheme(baseName)
	if err != nil {
		return nil, fmt.Errorf("base theme not found: %w", err)
	}

	// Create a copy of the base theme
	custom := *base

	// Apply customizations (simplified version - could be expanded)
	if name, ok := customizations["name"].(string); ok {
		custom.Name = name
	}

	if bg, ok := customizations["background"].(map[string]interface{}); ok {
		if r, ok := bg["r"].(int); ok {
			custom.Background.R = colorByte(r)
		}
		if g, ok := bg["g"].(int); ok {
			custom.Background.G = colorByte(g)
		}
		if b, ok := bg["b"].(int); ok {
			custom.Background.B = colorByte(b)
		}
	}

	return &custom, nil
}

func colorByte(value int) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}

// ExportTheme exports a built-in theme to a file for customization
func (tm *ThemeManager) ExportTheme(themeName, outputPath string) error {
	theme, err := tm.GetTheme(themeName)
	if err != nil {
		return err
	}

	return tm.SaveThemeToFile(theme, outputPath)
}

// Global theme manager instance
var GlobalThemeManager = NewThemeManager()

// LoadUserThemes loads themes from user directories
func LoadUserThemes() error {
	// Try to load from current directory themes/
	if _, err := os.Stat("themes"); err == nil {
		if err := GlobalThemeManager.LoadThemesFromDirectory("themes"); err != nil {
			fmt.Printf("Warning: failed to load themes from ./themes: %v\n", err)
		}
	}

	// Try to load from home directory ~/.labours-go/themes/
	homeDir, err := os.UserHomeDir()
	if err == nil {
		themeDir := filepath.Join(homeDir, ".labours-go", "themes")
		if _, err := os.Stat(themeDir); err == nil {
			if err := GlobalThemeManager.LoadThemesFromDirectory(themeDir); err != nil {
				fmt.Printf("Warning: failed to load themes from %s: %v\n", themeDir, err)
			}
		}
	}

	return nil
}

// SetTheme sets the current theme by name
func SetTheme(name string) error {
	return GlobalThemeManager.SetCurrentTheme(name)
}

// GetTheme gets a theme by name
func GetTheme(name string) (*Theme, error) {
	return GlobalThemeManager.GetTheme(name)
}

// ListThemes lists all available themes
func ListThemes() []string {
	return GlobalThemeManager.ListThemes()
}
