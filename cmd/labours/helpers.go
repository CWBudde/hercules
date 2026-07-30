package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render"
	"github.com/cwbudde/hercules/internal/render/readers"
)

// parseFlexibleDate parses a date string into a time.Time object.
// Returns an error if the date cannot be parsed.
func parseFlexibleDate(dateStr string) (time.Time, error) {
	parsedDate, err := dateparse.ParseAny(dateStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date format: %w", err)
	}
	return parsedDate, nil
}

func parseDates() (startTime, endTime *time.Time, err error) {
	if startTimeStr := viper.GetString("start-date"); startTimeStr != "" {
		parsedStartTime, parseErr := parseFlexibleDate(startTimeStr)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse start date: %w", parseErr)
		}
		startTime = &parsedStartTime
	}

	if endTimeStr := viper.GetString("end-date"); endTimeStr != "" {
		parsedEndTime, parseErr := parseFlexibleDate(endTimeStr)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse end date: %w", parseErr)
		}
		endTime = &parsedEndTime
	}

	return startTime, endTime, nil
}

func validateDateRange(startTime, endTime *time.Time) error {
	if startTime != nil && endTime != nil && endTime.Before(*startTime) {
		return errors.New("end date must be after start date")
	}
	return nil
}

func detectAndReadInput(input, inputFormat string) (readers.Reader, error) {
	format, err := render.NormalizeInputFormat(inputFormat)
	if err != nil {
		return nil, fmt.Errorf("detect or read input: %w", err)
	}
	reader, err := readers.DetectAndReadInputWithOptions(input, format, viper.GetBool("quiet"))
	if err != nil {
		return nil, fmt.Errorf("detect or read input: %w", err)
	}
	return reader, nil
}

func resolveModes() ([]string, error) {
	rawModes := append([]string{}, viper.GetStringSlice("modes")...)
	rawModes = append(rawModes, viper.GetStringSlice("mode")...)
	modes, err := render.ResolveModes(rawModes)
	if err != nil {
		return nil, err
	}
	return modes, nil
}

type repositoryModeRequirement struct {
	Analyses    []string
	Unsupported string
}

// repositoryModeRequirements is the single mode-to-analysis dependency table
// for --from-repo. Entries are concrete modes returned by render.ResolveModes.
var repositoryModeRequirements = map[string]repositoryModeRequirement{
	"burndown-project":        {Analyses: []string{"burndown"}},
	"burndown-file":           {Analyses: []string{"burndown", "burndown-files"}},
	"burndown-person":         {Analyses: []string{"burndown", "burndown-people"}},
	"burndown-repository":     {Unsupported: "requires combined multi-repository input, which --from-repo cannot produce"},
	"burndown-repos-combined": {Unsupported: "requires combined multi-repository input, which --from-repo cannot produce"},
	"overwrites-matrix":       {Analyses: []string{"burndown", "burndown-people"}},
	"ownership":               {Analyses: []string{"burndown", "burndown-people"}},
	"couples-files":           {Analyses: []string{"couples"}},
	"couples-people":          {Analyses: []string{"couples"}},
	"couples-shotness":        {Analyses: []string{"shotness"}},
	"shotness":                {Analyses: []string{"shotness"}},
	"devs":                    {Analyses: []string{"devs"}},
	"devs-efforts":            {Analyses: []string{"devs"}},
	"old-vs-new":              {Analyses: []string{"devs"}},
	"languages":               {Analyses: []string{"devs"}},
	"temporal-activity":       {Analyses: []string{"temporal-activity"}},
	"devs-parallel":           {Analyses: []string{"burndown", "burndown-people", "couples", "devs"}},
	"run-times":               {Analyses: []string{"commits-stat"}},
	"bus-factor":              {Analyses: []string{"bus-factor"}},
	"ownership-concentration": {Analyses: []string{"ownership-concentration"}},
	"knowledge-diffusion":     {Analyses: []string{"knowledge-diffusion"}},
	"hotspot-risk":            {Analyses: []string{"hotspot-risk"}},
	"sentiment":               {Analyses: []string{"sentiment"}},
	"refactoring-proxy":       {Analyses: []string{"refactoring-proxy"}},
}

var (
	lookPath              = exec.LookPath
	discoverRepository    = discoverGitRepository
	runRepositoryHercules = runHerculesForRepository
	renderRepositoryModes = render.Run
)

func requiredAnalysesForModes(modes []string) ([]string, error) {
	analyses := map[string]struct{}{}
	var failures []error
	for _, mode := range modes {
		requirement, exists := repositoryModeRequirements[mode]
		switch {
		case !exists:
			failures = append(failures, fmt.Errorf(
				"mode %q is unsupported with --from-repo: no Hercules analysis mapping", mode,
			))
		case requirement.Unsupported != "":
			failures = append(failures, fmt.Errorf(
				"mode %q is unsupported with --from-repo: %s", mode, requirement.Unsupported,
			))
		default:
			for _, analysis := range requirement.Analyses {
				analyses[analysis] = struct{}{}
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return nil, err
	}

	result := make([]string, 0, len(analyses))
	for analysis := range analyses {
		result = append(result, analysis)
	}
	sort.Strings(result)
	return result, nil
}

func discoverGitRepository(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve repository path %q: %w", path, err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("find git executable: %w", err)
	}

	bare, err := runGitRepositoryDiscovery(gitPath, absolutePath, "--is-bare-repository")
	if err != nil {
		return "", fmt.Errorf("%q is not a Git repository: %w", path, err)
	}
	if strings.TrimSpace(bare) == "true" {
		gitDir, gitErr := runGitRepositoryDiscovery(gitPath, absolutePath, "--absolute-git-dir")
		if gitErr != nil {
			return "", fmt.Errorf("discover bare repository %q: %w", path, gitErr)
		}
		return filepath.Clean(strings.TrimSpace(gitDir)), nil
	}

	topLevel, err := runGitRepositoryDiscovery(gitPath, absolutePath, "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("discover worktree repository %q: %w", path, err)
	}
	return filepath.Clean(strings.TrimSpace(topLevel)), nil
}

func runGitRepositoryDiscovery(gitPath, path, argument string) (string, error) {
	cmd := exec.Command(gitPath, "-C", path, "rev-parse", argument) // #nosec G204 -- executable is resolved with exec.LookPath.
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
		return "", err
	}
	return string(output), nil
}

func runHerculesForRepository(
	herculesPath, repoPath string, analyses []string,
) (reader readers.Reader, err error) {
	output, err := os.CreateTemp(viper.GetString("tmpdir"), "labours-hercules-*.pb")
	if err != nil {
		return nil, fmt.Errorf("create temporary Hercules output: %w", err)
	}
	outputPath := output.Name()
	defer func() {
		if removeErr := os.Remove(outputPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temporary Hercules output: %w", removeErr))
		}
	}()

	arguments := []string{"--pb"}
	for _, analysis := range analyses {
		arguments = append(arguments, "--"+analysis)
	}
	if userFlags := viper.GetString("hercules-flags"); userFlags != "" {
		arguments = append(arguments, strings.Fields(userFlags)...)
	}
	arguments = append(arguments, "--", repoPath)

	var stderr bytes.Buffer
	cmd := exec.Command(herculesPath, arguments...) // #nosec G204 -- executable is resolved with exec.LookPath or explicitly configured.
	cmd.Stdout = output
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		_ = output.Close()
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("run Hercules: %w: %s", runErr, message)
		}
		return nil, fmt.Errorf("run Hercules: %w", runErr)
	}
	if closeErr := output.Close(); closeErr != nil {
		return nil, fmt.Errorf("close Hercules output: %w", closeErr)
	}

	reader, err = detectAndReadInput(outputPath, "pb")
	if err != nil {
		return nil, fmt.Errorf("read Hercules output: %w", err)
	}
	return reader, nil
}

func runHerculesAndVisualize(
	herculesPath, repoPath string, modes, analyses []string,
	rendererOptions ...render.Options,
) error {
	reader, err := runRepositoryHercules(herculesPath, repoPath, analyses)
	if err != nil {
		return err
	}

	startDate, endDate, err := parseDates()
	if err != nil {
		return err
	}
	if err := validateDateRange(startDate, endDate); err != nil {
		return err
	}

	outputPath := viper.GetString("output")
	if outputPath == "" {
		outputPath = "analysis_results"
		if err := os.MkdirAll(outputPath, 0o750); err != nil {
			return fmt.Errorf("create default output directory: %w", err)
		}
	}

	var opts render.Options
	if len(rendererOptions) > 0 {
		opts = rendererOptions[0]
	} else {
		themeName := viper.GetString("theme")
		if viper.GetBool("matplotlib-colors") {
			themeName = "matplotlib"
		}
		opts, err = renderOptionsFromViper(themeName)
		if err != nil {
			return err
		}
	}
	opts.Output = outputPath
	opts.StartTime, opts.EndTime = startDate, endDate
	result := renderRepositoryModes(reader, modes, opts)
	return result.Err()
}
