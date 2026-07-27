# Visual Validation Framework

This directory contains the visual validation framework for the in-repo Go renderer
(`internal/render`, formerly labours-go). Pull requests use deterministic, pixel-exact
Go-renderer goldens; the extended historical suite uses perceptual comparison where
the Go and Python rendering engines cannot be pixel-identical.

## 📁 Directory Layout (hercules port)

- Harness code: `test/visual/*.go` (tracked)
- Input fixtures: `test/visual/testdata/`, `internal/render/testdata/example_data/`, and `internal/render/testdata/hercules/` (tracked)
- Golden reference images: `test/visual/golden/` (tracked and required by the CI gate)
- Python reference images: `test/visual/reference/` (untracked, git-ignored; copied from labours-go `analysis_results/reference/python_*.png`)
- Failure analysis dumps: `test/visual/analysis_output/` (untracked, git-ignored; written only when a parity test fails)

All relative paths in the tests are resolved from the test working directory, which Go sets to the package directory (`test/visual/`).

## 🎯 Framework Overview

The visual validation framework provides:

- **Perceptual Similarity Testing**: Uses advanced image comparison algorithms
- **Configurable Validation Levels**: Multiple thresholds for different use cases
- **Functional Chart Validation**: Validates chart structure and components
- **Python Compatibility Testing**: Compares with original Python labours output
- **Automated Reference Management**: Golden file generation and management

## 🔧 Core Components

### 1. Similarity Analysis (`similarity.go`)

**Histogram Intersection**: Measures color distribution similarity

- Captures overall visual content without focusing on exact pixel locations
- Resistant to minor rendering differences
- Range: 0.0 to 1.0 (higher is more similar)

**SSIM (Structural Similarity Index)**: Perceptually accurate structural comparison

- Focuses on luminance, contrast, and structure
- More relevant than pixel-wise comparison (MSE)
- Accounts for human visual perception

**Color Distance RMS**: Euclidean distance in RGB space

- Measures overall color accuracy
- Lower values indicate better color matching

**Overall Similarity**: Weighted combination of all metrics

- 40% Histogram Intersection (color distribution)
- 40% SSIM (structural similarity)
- 20% Color Distance (inverted, normalized)

### 2. Validation Levels

```go
ValidationStrict   // >99% similarity - for regression testing
ValidationStandard // >90% similarity - for development
ValidationLenient  // >85% similarity - for cross-platform testing
```

### 3. Chart Generation (`chart_generator.go`)

Integrates with the existing render modes (`internal/render/modes`):

- `burndown-project` / `burndown-project-relative`
- `burndown-file` / `burndown-person`
- `ownership` / `devs`
- `couples-people` / `couples-files`

Auto-detects input formats (YAML/Protobuf) and uses appropriate readers.

### 4. Test Framework (`regression_test.go`)

**Chart Structure Tests**: Validate that generated images exist, decode, are non-empty, and have sane dimensions. These run by default.
**Visual Regression Tests**: Require the exact expected artifact set and compare decoded pixels with the committed goldens at the strict 99% threshold. CI and `just test-visual-gate` enable these with `LABOURS_GO_VISUAL_PARITY=1`; absent references fail.
**Python Compatibility Tests**: Validate functional similarity with historical Python labours references. `just test-visual-extended` enables these with `LABOURS_GO_PYTHON_PARITY=1`; absent references fail.

## 🚀 Usage

### Visual Regression Testing

```bash
# Run structural visual tests (always on, no goldens required)
just test-visual

# Run the same committed regression gate as pull-request CI
just test-visual-gate

# Deliberately refresh the reviewed Go goldens
just update-visual-goldens

# Also run historical Python compatibility (requires reference/*.png)
just test-visual-extended
```

### Custom Testing

```go
// Create chart generator
generator := NewChartGenerator("/tmp/visual-test")

// Generate chart
chartPath, err := generator.GenerateChart(t, "burndown-project", "data.yaml")

// Compare with reference
metrics, err := CompareImages(chartPath, "reference.png")

// Check validation
if metrics.IsValidationPassing(ValidationStandard) {
    t.Log("✅ Visual validation passed")
} else {
    t.Errorf("❌ Visual validation failed: %s",
        metrics.GetDetailedReport(ValidationStandard))
}
```

## 📊 Example Results

### Successful Self-Similarity Test

```
Visual Similarity Analysis Report
=====================================
Validation Level: standard (threshold: 90.0%)
Status: PASS

Detailed Metrics:
- Histogram Intersection: 100.00% (color distribution similarity)
- SSIM: 100.00% (structural similarity)
- Color Distance RMS: 0.000 (lower is better)
- Overall Similarity: 100.00%

Assessment: Images are nearly identical - excellent compatibility
```

### Chart Structure Validation

```
✅ Chart structure validation passed: 1536x768, 200 colors, 47.7% white
```

## 🎨 Benefits Over Pixel-Perfect Testing

### Handles Real-World Variations

- **Anti-aliasing differences**: Different rendering engines produce slightly different edge smoothing
- **Font rendering variations**: System fonts render differently across platforms
- **Color space differences**: Minor RGB variations that don't affect visual perception
- **Compression artifacts**: PNG compression can introduce minimal pixel differences

### Focuses on Meaningful Differences

- **Data accuracy**: Ensures the same data trends and values are displayed
- **Visual layout**: Validates chart structure, proportions, and component placement
- **Color schemes**: Confirms appropriate color usage for data representation
- **Functional correctness**: Verifies charts convey the same information

### CI/CD Friendly

- **Reduces false positives**: Won't fail on insignificant rendering differences
- **Cross-platform compatible**: Works across different operating systems
- **Configurable sensitivity**: Adjust thresholds based on testing context
- **Actionable feedback**: Provides detailed analysis of any differences found

## 🔍 Advanced Features

### Difference Analysis

When validation fails, the framework automatically saves:

- Current and expected images for comparison
- Detailed similarity analysis report
- Visual difference highlighting (planned)

### Theme Compatibility

The framework works with all renderer themes:

- Default (matplotlib-compatible colors)
- Dark (dark background theme)
- Minimal (grayscale theme)
- Vibrant (high-contrast theme)
- Custom themes loaded from YAML

### Multiple Input Formats

Supports both hercules output formats:

- **YAML files**: Human-readable, used in examples
- **Protobuf files**: Binary format, used in production

## 🧪 Test Data Requirements

### For Visual Regression Tests

- The required, license-safe PNGs are committed in `test/visual/golden/`.
- They are generated only from the small, repository-owned fixtures listed above.
- A missing golden or output artifact fails the test.
- Use `just update-visual-goldens` after reviewing an intentional renderer change.

### For Python Compatibility Tests

- Python reference images in `test/visual/reference/` (git-ignored)
- Generated using original Python labours implementation
- Same input data as Go tests for accurate comparison

## 🎯 Quality Thresholds

### Current Mode Thresholds

| Test                 | Mode                          | Threshold     |
| -------------------- | ----------------------------- | ------------- |
| Go golden regression | `burndown-project`            | Strict (99%+) |
| Go golden regression | `devs`                        | Strict (99%+) |
| Go golden regression | `couples-files` heatmap       | Strict (99%+) |
| Go golden regression | `couples-files` ranked pairs  | Strict (99%+) |
| Python compatibility | `burndown-project`            | Lenient       |
| Python compatibility | `burndown-project --relative` | Lenient       |

### Strict (99%+)

- For critical regression testing
- Ensures minimal visual changes
- Used in CI/CD pipelines

### Standard (90%+) - **Recommended**

- Balanced approach for development
- Allows minor rendering differences
- Catches significant visual changes

### Lenient (85%+)

- For cross-platform testing
- Accommodates system-specific rendering
- Focuses on functional correctness

## 🔮 Future Enhancements

### Planned Features

- **Interactive difference viewer**: Web-based visual diff tool
- **Performance benchmarking**: Track chart generation performance
- **Multi-theme testing**: Automated testing across all themes
- **Statistical analysis**: Track similarity trends over time
- **Integration testing**: End-to-end CLI validation

### Advanced Similarity Metrics

- **Feature-based matching**: Detect specific chart elements
- **Perceptual hashing**: Content-aware image fingerprinting
- **Machine learning**: AI-powered visual similarity assessment

## 📈 Integration with Existing Testing

The visual framework integrates seamlessly with the existing hercules test suites:

- **Unit tests**: Continue to test individual functions
- **Integration tests**: Validate end-to-end data processing
- **Visual tests**: Ensure chart output quality and consistency
- **Performance tests**: Monitor rendering speed and memory usage

This multi-layered approach ensures comprehensive quality assurance for the Go renderer.

---

**Status**: ✅ **Production Ready** - Framework successfully validates chart generation and maintains Python compatibility through functional similarity testing.
