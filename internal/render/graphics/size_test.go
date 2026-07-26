package graphics

import (
	"testing"
)

func TestParsePlotSizeFloats(t *testing.T) {
	tests := []struct {
		name           string
		sizeStr        string
		expectedWidth  float64
		expectedHeight float64
		expectError    bool
	}{
		{name: "valid size string", sizeStr: "14,10", expectedWidth: 14.0, expectedHeight: 10.0},
		{name: "valid size string with spaces", sizeStr: " 12 , 8 ", expectedWidth: 12.0, expectedHeight: 8.0},
		{name: "Python labours compatible format", sizeStr: "16,12", expectedWidth: 16.0, expectedHeight: 12.0},
		{name: "decimal values", sizeStr: "12.5,8.5", expectedWidth: 12.5, expectedHeight: 8.5},
		{name: "invalid format - no comma", sizeStr: "12x8", expectError: true},
		{name: "invalid format - too many parts", sizeStr: "12,8,4", expectError: true},
		{name: "invalid width", sizeStr: "abc,8", expectError: true},
		{name: "invalid height", sizeStr: "12,xyz", expectError: true},
		{name: "zero width", sizeStr: "0,8", expectError: true},
		{name: "negative height", sizeStr: "12,-5", expectError: true},
		{name: "dimensions too large", sizeStr: "100,80", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height, err := parsePlotSizeFloats(tt.sizeStr)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if width != tt.expectedWidth {
				t.Errorf("width mismatch: expected %f, got %f", tt.expectedWidth, width)
			}
			if height != tt.expectedHeight {
				t.Errorf("height mismatch: expected %f, got %f", tt.expectedHeight, height)
			}
		})
	}
}

func TestGetPlotSizeInches(t *testing.T) {
	tests := []struct {
		name           string
		chartType      ChartType
		expectedWidth  float64
		expectedHeight float64
	}{
		{name: "default chart type", chartType: ChartTypeDefault, expectedWidth: 16.0, expectedHeight: 8.0},
		{name: "square chart type", chartType: ChartTypeSquare, expectedWidth: 12.0, expectedHeight: 12.0},
		{name: "compact chart type", chartType: ChartTypeCompact, expectedWidth: 10.0, expectedHeight: 6.0},
		{name: "wide chart type", chartType: ChartTypeWide, expectedWidth: 16.0, expectedHeight: 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := GetPlotSizeInches(tt.chartType)

			if width != tt.expectedWidth {
				t.Errorf("width mismatch: expected %f, got %f", tt.expectedWidth, width)
			}
			if height != tt.expectedHeight {
				t.Errorf("height mismatch: expected %f, got %f", tt.expectedHeight, height)
			}
		})
	}
}

func TestGetPlotSizeInchesHonorsSizeFlag(t *testing.T) {
	width, height := GetPlotSizeInchesWithOptions(ChartTypeDefault, Options{Size: "14,9"})
	if width != 14.0 || height != 9.0 {
		t.Fatalf("GetPlotSizeInches() = (%f, %f), want (14, 9)", width, height)
	}
}

func TestDefaultSizes(t *testing.T) {
	chartTypes := []ChartType{
		ChartTypeDefault,
		ChartTypeSquare,
		ChartTypeCompact,
		ChartTypeWide,
	}

	for _, ct := range chartTypes {
		size, exists := defaultSizes[ct]
		if !exists {
			t.Errorf("no default size defined for chart type %d", ct)
			continue
		}
		if size[0] <= 0 || size[1] <= 0 {
			t.Errorf("invalid default size for chart type %d: [%f, %f]", ct, size[0], size[1])
		}
	}
}

func BenchmarkGetPlotSizeInches(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetPlotSizeInches(ChartTypeDefault)
	}
}
