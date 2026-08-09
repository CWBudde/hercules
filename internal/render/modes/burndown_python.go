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

var (
	errNoRepositoryData        = errors.New("no repository data available")
	errEmptyCombinedRepoMatrix = errors.New("empty combined repository burndown matrix")
)

// GenerateBurndownProjectPython creates a Python-compatible burndown chart.
func GenerateBurndownProjectPython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample

	return GenerateBurndownProjectPythonWithOptions(reader, output, opts)
}

func GenerateBurndownProjectPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-project (Python-compatible)")

	progEstimator := progress.NewProgressEstimator(!opts.Quiet)

	progEstimator.StartMultiOperation(4, "Python-Compatible Burndown Analysis")
	defer progEstimator.FinishMultiOperation()

	progEstimator.NextOperation("Validating output path")

	output, err := prepareProjectBurndownOutput(output, opts.Quiet)
	if err != nil {
		return err
	}

	progEstimator.NextOperation("Loading burndown data")

	header, name, matrix, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return fmt.Errorf("failed to load burndown data: %w", err)
	}

	if !opts.Quiet {
		fmt.Printf("Processing %s with %d age bands and %d time points\n", name, len(matrix), len(matrix[0]))
		fmt.Printf("Header: start=%d, last=%d, sampling=%d, granularity=%d, tick_size=%.3f\n",
			header.Start, header.Last, header.Sampling, header.Granularity, header.TickSize)
	}

	progEstimator.NextOperation("Processing data with Python algorithms")
	// Python labours always titles the aggregate burndown "project" (see
	// labours' burndown-project mode: plot_burndown(args, "project", ...)),
	// regardless of how many repositories were combined. Using the reader's
	// name here (a "&"-joined list of every repo) overflows the title.
	processedData, err := burndown.LoadBurndown(
		header, "project", matrix, defaultBurndownResample(opts.Resample), true, true,
	)
	if err != nil {
		return fmt.Errorf("failed to process burndown data: %w", err)
	}

	err = reportProjectBurndown(processedData, header.Sampling, opts.Quiet)
	if err != nil {
		return err
	}

	progEstimator.NextOperation("Generating Python-style visualization")

	err = graphics.PlotBurndownMatplotlibWithOptions(processedData, output, opts.Relative, opts.Graphics)
	if err != nil {
		return fmt.Errorf("error creating Python-style burndown plot: %w", err)
	}

	if !opts.Quiet {
		fmt.Printf("Python-compatible chart saved to %s\n", output)
	}

	return nil
}

func prepareProjectBurndownOutput(output string, quiet bool) (string, error) {
	if output == "" {
		output = "burndown_project_python.png"
		if !quiet {
			fmt.Printf("Output not provided, using default: %s\n", output)
		}
	}

	outputDir := filepath.Dir(output)

	err := os.MkdirAll(outputDir, 0o750)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return output, nil
}

func defaultBurndownResample(resample string) string {
	if resample == "" {
		return resampleYear
	}

	return resample
}

func reportProjectBurndown(data *burndown.ProcessedBurndown, sampling int, quiet bool) error {
	if quiet {
		return nil
	}

	fmt.Printf("Processed into %d layers: %v\n", len(data.Labels), data.Labels)
	fmt.Printf("Final matrix dimensions: %dx%d\n", len(data.Matrix), len(data.Matrix[0]))

	err := burndown.WriteSurvivalFunction(os.Stdout, data.Survival, sampling)
	if err != nil {
		return fmt.Errorf("print survival analysis: %w", err)
	}

	return nil
}

// GenerateBurndownFilePython creates Python-compatible file-level burndown charts.
func GenerateBurndownFilePython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample

	return GenerateBurndownFilePythonWithOptions(reader, output, opts)
}

func GenerateBurndownFilePythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-file (Python-compatible)")

	files, err := reader.GetFilesBurndown()
	if err != nil {
		return fmt.Errorf("failed to get files burndown data: %w", err)
	}

	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return fmt.Errorf("failed to get burndown header: %w", err)
	}

	if !opts.Quiet {
		fmt.Printf("Processing %d files\n", len(files))
	}

	outputFiles, err := fileBurndownOutputPaths(output, files)
	if err != nil {
		return fmt.Errorf("plan file burndown outputs: %w", err)
	}

	var failures []error

	for i, file := range files {
		err := renderFileBurndown(header, file, outputFiles[i], i, len(files), opts)
		if err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func fileBurndownOutputPaths(output string, files []readers.FileBurndown) ([]string, error) {
	identities := make([]string, len(files))
	for index, file := range files {
		identities[index] = file.Filename
	}

	return outputpath.FanoutPaths(output, "burndown_file", identities)
}

func renderFileBurndown(
	header burndown.BurndownHeader,
	file readers.FileBurndown,
	output string,
	index, total int,
	opts Options,
) error {
	if !opts.Quiet {
		fmt.Printf("Processing file %d/%d: %s\n", index+1, total, file.Filename)
	}

	data, err := burndown.LoadBurndown(
		header, file.Filename, file.Matrix, defaultBurndownResample(opts.Resample), false, false,
	)
	if err != nil {
		return fmt.Errorf("process %s: %w", file.Filename, err)
	}

	err = graphics.PlotBurndownMatplotlibWithOptions(data, output, opts.Relative, opts.Graphics)
	if err != nil {
		return fmt.Errorf("create plot for %s: %w", file.Filename, err)
	}

	if !opts.Quiet {
		fmt.Printf("Chart saved: %s\n", output)
	}

	return nil
}

// GenerateBurndownRepositoryPython creates Python-compatible repository-level burndown charts.
func GenerateBurndownRepositoryPython(reader readers.Reader, output string, relative bool, resample string) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample = relative, resample

	return GenerateBurndownRepositoryPythonWithOptions(reader, output, opts)
}

func GenerateBurndownRepositoryPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-repository (Python-compatible)")

	repositories, header, err := loadRepositoryBurndown(reader)
	if err != nil {
		return err
	}

	outputFiles, err := repositoryBurndownOutputPaths(output, repositories)
	if err != nil {
		return err
	}

	var failures []error

	for i, repository := range repositories {
		err := renderRepositoryBurndown(
			header, repository, outputFiles[i], i, len(repositories), opts,
		)
		if err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func loadRepositoryBurndown(
	reader readers.Reader,
) ([]readers.RepositoryBurndown, burndown.BurndownHeader, error) {
	repoReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		return nil, burndown.BurndownHeader{},
			fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}

	repositories, err := repoReader.GetRepositoriesBurndown()
	if err != nil {
		return nil, burndown.BurndownHeader{},
			fmt.Errorf("failed to get repositories burndown data: %w", err)
	}

	if len(repositories) == 0 {
		return nil, burndown.BurndownHeader{},
			fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}

	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return nil, burndown.BurndownHeader{}, fmt.Errorf("failed to get burndown header: %w", err)
	}

	return repositories, header, nil
}

func repositoryBurndownOutputPaths(
	output string,
	repositories []readers.RepositoryBurndown,
) ([][]string, error) {
	if output == "" {
		output = "."
	}

	err := os.MkdirAll(output, 0o750)
	if err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", output, err)
	}

	identities := make([]string, len(repositories))
	for index, repository := range repositories {
		identities[index] = repository.Repository
	}

	outputFiles, err := outputpath.AssetFanoutPaths(
		output, "burndown-repository", identities, []string{extensionPNG, extensionSVG},
	)
	if err != nil {
		return nil, fmt.Errorf("plan repository burndown outputs: %w", err)
	}

	return outputFiles, nil
}

func renderRepositoryBurndown(
	header burndown.BurndownHeader,
	repository readers.RepositoryBurndown,
	outputs []string,
	index, total int,
	opts Options,
) error {
	if !opts.Quiet {
		fmt.Printf("Processing repository %d/%d: %s\n", index+1, total, repository.Repository)
	}

	data, err := burndown.LoadBurndown(
		header, repository.Repository, repository.Matrix, defaultBurndownResample(opts.Resample), false, false,
	)
	if err != nil {
		return fmt.Errorf("process repository %s: %w", repository.Repository, err)
	}

	err = graphics.PlotBurndownMatplotlibWithOptions(data, outputs[0], opts.Relative, opts.Graphics)
	if err != nil {
		return fmt.Errorf("create plot for repository %s: %w", repository.Repository, err)
	}

	err = graphics.PlotBurndownMatplotlibWithOptions(data, outputs[1], opts.Relative, opts.Graphics)
	if err != nil {
		return fmt.Errorf("create SVG plot for repository %s: %w", repository.Repository, err)
	}

	if !opts.Quiet {
		fmt.Printf("Charts saved: %s and %s\n", outputs[0], outputs[1])
	}

	return nil
}

// GenerateBurndownReposCombinedPython creates one burndown chart from all repository matrices combined.
// maxRepos limits how many repositories are shown as individual bands; the
// remaining (smallest) repositories are aggregated into a single "Other" band.
// A value <= 0 disables the limit and shows every repository.
func GenerateBurndownReposCombinedPython(
	reader readers.Reader,
	output string,
	relative bool,
	resample string,
	maxRepos int,
) error {
	opts := defaultOptions()
	opts.Relative, opts.Resample, opts.MaxRepos = relative, resample, maxRepos

	return GenerateBurndownReposCombinedPythonWithOptions(reader, output, opts)
}

func GenerateBurndownReposCombinedPythonWithOptions(reader readers.Reader, output string, opts Options) error {
	fmt.Println("Running: burndown-repos-combined (Python-compatible)")

	repositories, header, err := loadCombinedRepositoryBurndown(reader)
	if err != nil {
		return err
	}

	processedData, err := combinedRepositoryBurndownData(repositories, header, opts)
	if err != nil {
		return err
	}

	output, err = prepareCombinedRepositoryOutput(output)
	if err != nil {
		return err
	}

	err = graphics.PlotBurndownMatplotlibWithOptions(processedData, output, opts.Relative, opts.Graphics)
	if err != nil {
		return fmt.Errorf("error creating combined repository burndown plot: %w", err)
	}

	if !opts.Quiet {
		fmt.Printf("Combined repository burndown chart saved to %s\n", output)
	}

	return nil
}

func loadCombinedRepositoryBurndown(
	reader readers.Reader,
) ([]readers.RepositoryBurndown, burndown.BurndownHeader, error) {
	repoReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		// Match Python labours: ValueError("No repository data available").
		return nil, burndown.BurndownHeader{}, errNoRepositoryData
	}

	repositories, err := repoReader.GetRepositoriesBurndown()
	if err != nil {
		return nil, burndown.BurndownHeader{},
			fmt.Errorf("failed to get repositories burndown data: %w", err)
	}

	if len(repositories) == 0 {
		return nil, burndown.BurndownHeader{}, errNoRepositoryData
	}

	header, _, _, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return nil, burndown.BurndownHeader{}, fmt.Errorf("failed to get burndown header: %w", err)
	}

	return repositories, header, nil
}

func combinedRepositoryBurndownData(
	repositories []readers.RepositoryBurndown,
	header burndown.BurndownHeader,
	opts Options,
) (*burndown.ProcessedBurndown, error) {
	// Each repository becomes a stacked band containing its total surviving
	// lines at every sample point, matching Python labours.
	repoMatrix, labels := repositoryBands(repositories)
	if len(repoMatrix) == 0 || len(repoMatrix[0]) == 0 {
		return nil, errEmptyCombinedRepoMatrix
	}

	repoMatrix, labels = limitRepositoryBands(repoMatrix, labels, opts.MaxRepos)
	dateRange := repositoryBurndownDates(header, len(repoMatrix[0]))

	return &burndown.ProcessedBurndown{
		Name:         "combined repositories",
		Matrix:       repoMatrix,
		DateRange:    dateRange,
		Labels:       labels,
		Granularity:  header.Granularity,
		Sampling:     header.Sampling,
		ResampleMode: opts.Resample,
	}, nil
}

func repositoryBurndownDates(header burndown.BurndownHeader, columns int) []time.Time {
	start := burndown.FloorDateTime(time.Unix(header.Start, 0), header.TickSize)
	secondsPerSample := int64(header.TickSize) * int64(header.Sampling)

	dateRange := make([]time.Time, columns)
	for index := range dateRange {
		dateRange[index] = start.Add(time.Duration(int64(index)*secondsPerSample) * time.Second)
	}

	return dateRange
}

func prepareCombinedRepositoryOutput(output string) (string, error) {
	if output == "" {
		output = "burndown-repos-combined.png"
	}

	dir := filepath.Dir(output)
	if dir == "" || dir == "." {
		return output, nil
	}

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", dir, err)
	}

	return output, nil
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

	sizes := repositoryBandSizes(matrix)
	sort.SliceStable(sizes, func(a, b int) bool { return sizes[a].peak > sizes[b].peak })

	keep := make(map[int]bool, maxRepos)
	for i := range maxRepos {
		keep[sizes[i].idx] = true
	}

	return combineRepositoryBands(matrix, labels, keep)
}

type repositoryBandSize struct {
	idx  int
	peak float64
}

func repositoryBandSizes(matrix [][]float64) []repositoryBandSize {
	sizes := make([]repositoryBandSize, len(matrix))
	for i, row := range matrix {
		var peak float64
		for _, v := range row {
			if v > peak {
				peak = v
			}
		}

		sizes[i] = repositoryBandSize{idx: i, peak: peak}
	}

	return sizes
}

func combineRepositoryBands(matrix [][]float64, labels []string, keep map[int]bool) ([][]float64, []string) {
	newMatrix := make([][]float64, 0, len(keep)+1)
	newLabels := make([]string, 0, len(keep)+1)
	cols := len(matrix[0])
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
