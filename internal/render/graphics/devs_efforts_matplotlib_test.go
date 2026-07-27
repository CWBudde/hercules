package graphics

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevsEffortsYRangeMatchesRenderedDataExtent(t *testing.T) {
	layers := nonNegativeEffortLayers([][]float64{
		{10, -200, 30},
		{5, 10, -400},
	})

	yRange := devsEffortsYRange(layers)
	if yRange.DataMin != 0 || yRange.AxisMin != yRange.DataMin {
		t.Fatalf("lower extent and axis = %v, %v; want 0, 0", yRange.DataMin, yRange.AxisMin)
	}
	if yRange.DataMax != 30 {
		t.Fatalf("rendered maximum = %v, want 30", yRange.DataMax)
	}
	if yRange.AxisMax != yRange.DataMax*1.02 {
		t.Fatalf("axis maximum = %v, want %v", yRange.AxisMax, yRange.DataMax*1.02)
	}
	if fill := (yRange.DataMax - yRange.DataMin) / (yRange.AxisMax - yRange.AxisMin); fill < 0.98 {
		t.Fatalf("rendered data fills %.2f%% of the y axis, want at least 98%%", fill*100)
	}

	for i, layer := range layers {
		for j, value := range layer {
			if value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
				t.Fatalf("rendered layer[%d][%d] = %v, want a finite non-negative value", i, j, value)
			}
		}
	}
}

func TestPlotDevsEffortsClampsNegativeCumulativeValues(t *testing.T) {
	dates := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	output := filepath.Join(t.TempDir(), "devs-efforts.svg")
	err := PlotDevsEffortsMatplotlib(
		dates,
		[][]float64{
			{100, -50000, 140},
			{20, 25, 30},
		},
		[]string{"Alice", "others"},
		MatplotlibDevsEffortsOptions{
			Title:        "Efforts through time",
			Output:       output,
			WidthInches:  8,
			HeightInches: 5,
		},
	)
	if err != nil {
		t.Fatalf("plot devs-efforts: %v", err)
	}

	svgBytes, err := os.ReadFile(output) // #nosec G304 - test path is under t.TempDir.
	if err != nil {
		t.Fatalf("read devs-efforts SVG: %v", err)
	}
	svg := string(svgBytes)
	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, ">Alice</text>") {
		t.Fatalf("devs-efforts SVG is missing expected chart content")
	}
	if strings.Contains(svg, ">-50000</text>") {
		t.Fatalf("devs-efforts SVG exposes a negative cumulative value")
	}
}
