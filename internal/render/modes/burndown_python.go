package modes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/graphics"
	"github.com/cwbudde/hercules/internal/render/outputpath"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// GenerateBurndownProjectPython creates a Python-compatible burndown chart
func GenerateBurndownProjectPython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample
	return GenerateBurndownProjectPythonWithOptions(reader, output, opts)
}

func GenerateBurndownProjectPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-project (Python-compatible)")

	// Initialize progress tracking
	quiet := opts.Quiet
	progEstimator := progress.NewProgressEstimator(!quiet)

	totalPhases := 4 // validation, data loading, processing, plotting
	progEstimator.StartMultiOperation(totalPhases, "Python-Compatible Burndown Analysis")

	// Phase 1: Validation and setup
	progEstimator.NextOperation("Validating output path")
	if output == "" {
		output = "burndown_project_python.png"
		if !quiet {
			fmt.Printf("Output not provided, using default: %s\n", output)
		}
	}

	outputDir := filepath.Dir(output)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to create output directory %s: %v", outputDir, err)
	}

	// Phase 2: Load burndown data with Python-compatible header
	progEstimator.NextOperation("Loading burndown data")
	header, name, matrix, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to load burndown data: %w", err)
	}

	if !quiet {
		fmt.Printf("Processing %s with %d age bands and %d time points\n", name, len(matrix), len(matrix[0]))
		fmt.Printf("Header: start=%d, last=%d, sampling=%d, granularity=%d, tick_size=%.3f\n",
			header.Start, header.Last, header.Sampling, header.Granularity, header.TickSize)
	}

	// Phase 3: Process data using Python-compatible algorithms
	progEstimator.NextOperation("Processing data with Python algorithms")
	if opts.Resample == "" {
		opts.Resample = "year" // Default to yearly like Python
	}

	// Python labours always titles the aggregate burndown "project" (see
	// labours' burndown-project mode: plot_burndown(args, "project", ...)),
	// regardless of how many repositories were combined. Using the reader's
	// name here (a "&"-joined list of every repo) overflows the title.
	_ = name
	titleName := "project"
	processedData, err := burndown.LoadBurndown(header, titleName, matrix, opts.Resample, true, true)
	if err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("failed to process burndown data: %v", err)
	}

	if !quiet {
		fmt.Printf("Processed into %d layers: %v\n", len(processedData.Labels), processedData.Labels)
		fmt.Printf("Final matrix dimensions: %dx%d\n", len(processedData.Matrix), len(processedData.Matrix[0]))
	}

	if !quiet {
		if err := burndown.WriteSurvivalFunction(os.Stdout, processedData.Survival, header.Sampling); err != nil {
			progEstimator.FinishMultiOperation()
			return fmt.Errorf("print survival analysis: %w", err)
		}
	}

	// Phase 4: Generate visualization
	progEstimator.NextOperation("Generating Python-style visualization")
	if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, output, opts.Relative, opts.Graphics); err != nil {
		progEstimator.FinishMultiOperation()
		return fmt.Errorf("error creating Python-style burndown plot: %v", err)
	}

	progEstimator.FinishMultiOperation()
	if !quiet {
		fmt.Printf("Python-compatible chart saved to %s\n", output)
	}
	return nil
}

// GenerateBurndownFilePython creates Python-compatible file-level burndown charts
func GenerateBurndownFilePython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample
	return GenerateBurndownFilePythonWithOptions(reader, output, opts)
}

func GenerateBurndownFilePythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-file (Python-compatible)")

	// Get files burndown data
	files, err := reader.GetFilesBurndown()
	if err != nil {
		return fmt.Errorf("failed to get files burndown data: %w", err)
	}

	// Get header information
	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return fmt.Errorf("failed to get burndown header: %w", err)
	}

	quiet := opts.Quiet
	if !quiet {
		fmt.Printf("Processing %d files\n", len(files))
	}

	identities := make([]string, len(files))
	for index, file := range files {
		identities[index] = file.Filename
	}
	outputFiles, err := outputpath.FanoutPaths(output, "burndown_file", identities)
	if err != nil {
		return fmt.Errorf("plan file burndown outputs: %w", err)
	}

	// Process each file
	var failures []error
	for i, file := range files {
		if !quiet {
			fmt.Printf("Processing file %d/%d: %s\n", i+1, len(files), file.Filename)
		}

		if opts.Resample == "" {
			opts.Resample = "year"
		}

		processedData, err := burndown.LoadBurndown(header, file.Filename, file.Matrix, opts.Resample, false, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("process %s: %w", file.Filename, err))
			continue
		}

		fileOutput := outputFiles[i]

		if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, fileOutput, opts.Relative, opts.Graphics); err != nil {
			failures = append(failures, fmt.Errorf("create plot for %s: %w", file.Filename, err))
			continue
		}

		if !quiet {
			fmt.Printf("Chart saved: %s\n", fileOutput)
		}
	}

	return errors.Join(failures...)
}

// GenerateBurndownRepositoryPython creates Python-compatible repository-level burndown charts.
func GenerateBurndownRepositoryPython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample
	return GenerateBurndownRepositoryPythonWithOptions(reader, output, opts)
}

func GenerateBurndownRepositoryPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-repository (Python-compatible)")

	repoReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		return fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}

	repositories, err := repoReader.GetRepositoriesBurndown()
	if err != nil {
		return fmt.Errorf("failed to get repositories burndown data: %w", err)
	}
	if len(repositories) == 0 {
		return fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}

	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return fmt.Errorf("failed to get burndown header: %w", err)
	}

	if output == "" {
		output = "."
	}
	if err := os.MkdirAll(output, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", output, err)
	}
	if opts.Resample == "" {
		opts.Resample = "year"
	}

	quiet := opts.Quiet
	identities := make([]string, len(repositories))
	for index, repository := range repositories {
		identities[index] = repository.Repository
	}
	outputFiles, err := outputpath.AssetFanoutPaths(
		output, "burndown-repository", identities, []string{".png", ".svg"},
	)
	if err != nil {
		return fmt.Errorf("plan repository burndown outputs: %w", err)
	}

	var failures []error
	for i, repository := range repositories {
		if !quiet {
			fmt.Printf("Processing repository %d/%d: %s\n", i+1, len(repositories), repository.Repository)
		}

		processedData, err := burndown.LoadBurndown(header, repository.Repository, repository.Matrix, opts.Resample, false, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("process repository %s: %w", repository.Repository, err))
			continue
		}

		repoPNG := outputFiles[i][0]
		if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, repoPNG, opts.Relative, opts.Graphics); err != nil {
			failures = append(failures, fmt.Errorf("create plot for repository %s: %w", repository.Repository, err))
			continue
		}
		repoSVG := outputFiles[i][1]
		if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, repoSVG, opts.Relative, opts.Graphics); err != nil {
			failures = append(failures, fmt.Errorf("create SVG plot for repository %s: %w", repository.Repository, err))
			continue
		}
		if !quiet {
			fmt.Printf("Charts saved: %s and %s\n", repoPNG, repoSVG)
		}
	}

	return errors.Join(failures...)
}

// GenerateBurndownReposCombinedPython creates one burndown chart from all repository matrices combined.
// maxRepos limits how many repositories are shown as individual bands; the
// remaining (smallest) repositories are aggregated into a single "Other" band.
// A value <= 0 disables the limit and shows every repository.
func GenerateBurndownReposCombinedPython(reader readers.Reader, output string, relative bool, resample string, maxRepos int) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample, opts.MaxRepos = relative, resample, maxRepos
	return GenerateBurndownReposCombinedPythonWithOptions(reader, output, opts)
}

func GenerateBurndownReposCombinedPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-repos-combined (Python-compatible)")

	repoReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		// Match Python labours: ValueError("No repository data available").
		return fmt.Errorf("No repository data available")
	}

	repositories, err := repoReader.GetRepositoriesBurndown()
	if err != nil {
		return fmt.Errorf("failed to get repositories burndown data: %w", err)
	}
	if len(repositories) == 0 {
		return fmt.Errorf("No repository data available")
	}

	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return fmt.Errorf("failed to get burndown header: %w", err)
	}

	// Build one stacked band per repository: each band is that repository's
	// total surviving lines of code at every sample point (the sum over its age
	// bands). This matches Python labours' burndown-repos-combined, which stacks
	// repositories over time — NOT age cohorts (which would duplicate the
	// project burndown). The x-axis is the shared sample grid, so the title
	// reads "combined repositories <#repos> x <#samples>".
	repoMatrix, labels := repositoryBands(repositories)
	if len(repoMatrix) == 0 || len(repoMatrix[0]) == 0 {
		return fmt.Errorf("empty combined repository burndown matrix")
	}
	// Keep only the largest maxRepos repositories as individual bands and fold
	// the rest into a single "Other" band so the legend stays legible when many
	// repositories are combined.
	repoMatrix, labels = limitRepositoryBands(repoMatrix, labels, opts.MaxRepos)
	cols := len(repoMatrix[0])

	// Sample dates along the shared tick axis: start + t * sampling * tick.
	start := burndown.FloorDateTime(time.Unix(header.Start, 0), header.TickSize)
	secondsPerSample := int64(header.TickSize) * int64(header.Sampling)
	dateRange := make([]time.Time, cols)
	for t := range dateRange {
		dateRange[t] = start.Add(time.Duration(int64(t)*secondsPerSample) * time.Second)
	}

	if output == "" {
		output = "burndown-repos-combined.png"
	}
	if dir := filepath.Dir(output); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("failed to create output directory %s: %v", dir, err)
		}
	}

	processedData := &burndown.ProcessedBurndown{
		Name:         "combined repositories",
		Matrix:       repoMatrix,
		DateRange:    dateRange,
		Labels:       labels,
		Granularity:  header.Granularity,
		Sampling:     header.Sampling,
		ResampleMode: opts.Resample,
	}

	if err := graphics.PlotBurndownMatplotlibWithOptions(processedData, output, opts.Relative, opts.Graphics); err != nil {
		return fmt.Errorf("error creating combined repository burndown plot: %v", err)
	}

	if !opts.Quiet {
		fmt.Printf("Combined repository burndown chart saved to %s\n", output)
	}
	return nil
}

// repositoryBands builds one stacked band per repository for the
// burndown-repos-combined chart. Each band holds the repository's total
// surviving lines of code at every sample point (the sum over its age bands),
// aligned to the shared sample grid. Returns the per-repository matrix and the
// matching repository-name labels.
func repositoryBands(repositories []readers.RepositoryBurndown) ([][]float64, []string) {
	cols := 0
	for _, repository := range repositories {
		for _, row := range repository.Matrix {
			if len(row) > cols {
				cols = len(row)
			}
		}
	}
	if cols == 0 {
		return nil, nil
	}

	matrix := make([][]float64, 0, len(repositories))
	labels := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		total := make([]float64, cols)
		for _, row := range repository.Matrix {
			for j := 0; j < cols && j < len(row); j++ {
				total[j] += float64(row[j])
			}
		}
		matrix = append(matrix, total)
		// Use the repository's basename as the legend label: Hercules records the
		// path it was invoked with, which may be absolute
		// (/mnt/projekte/Code/MeKo/foo) and would otherwise bloat the legend.
		labels = append(labels, filepath.Base(strings.TrimRight(repository.Repository, "/")))
	}
	return matrix, labels
}

// limitRepositoryBands keeps the largest maxRepos repositories (ranked by their
// peak surviving lines of code) as individual bands, in their original stacking
// order, and folds every remaining repository into a single trailing "Other"
// band. A maxRepos <= 0, or a repository count already within the limit, is
// returned unchanged. This keeps the legend readable for org-wide charts that
// combine hundreds of repositories.
func limitRepositoryBands(matrix [][]float64, labels []string, maxRepos int) ([][]float64, []string) {
	if maxRepos <= 0 || len(matrix) <= maxRepos {
		return matrix, labels
	}
	cols := len(matrix[0])

	type sized struct {
		idx  int
		peak float64
	}
	sizes := make([]sized, len(matrix))
	for i, row := range matrix {
		var peak float64
		for _, v := range row {
			if v > peak {
				peak = v
			}
		}
		sizes[i] = sized{idx: i, peak: peak}
	}
	sort.SliceStable(sizes, func(a, b int) bool { return sizes[a].peak > sizes[b].peak })

	keep := make(map[int]bool, maxRepos)
	for i := 0; i < maxRepos; i++ {
		keep[sizes[i].idx] = true
	}

	newMatrix := make([][]float64, 0, maxRepos+1)
	newLabels := make([]string, 0, maxRepos+1)
	other := make([]float64, cols)
	otherCount := 0
	for i := range matrix {
		if keep[i] {
			newMatrix = append(newMatrix, matrix[i])
			newLabels = append(newLabels, labels[i])
			continue
		}
		for j := 0; j < cols && j < len(matrix[i]); j++ {
			other[j] += matrix[i][j]
		}
		otherCount++
	}
	if otherCount > 0 {
		newMatrix = append(newMatrix, other)
		newLabels = append(newLabels, fmt.Sprintf("Other (%d repos)", otherCount))
	}
	return newMatrix, newLabels
}
