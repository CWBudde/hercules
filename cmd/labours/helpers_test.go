package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render"
	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestParseFlexibleDateAcceptsCommonPythonCompatibleForms(t *testing.T) {
	for _, date := range []string{
		"2024-01-02",
		"January 2, 2024",
		"2024-01-02T15:04:05Z",
	} {
		t.Run(date, func(t *testing.T) {
			if _, err := parseFlexibleDate(date); err != nil {
				t.Fatalf("parseFlexibleDate() unexpected error: %v", err)
			}
		})
	}
}

func TestPythonCompatibleFlagsAreRegistered(t *testing.T) {
	for _, name := range []string{
		"mode",
		"devs-parallel-fallback",
		"sentiment-fallback",
		"temporal-legend-threshold",
		"temporal-legend-single-col-threshold",
	} {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("expected flag %q to be registered", name)
		}
	}
}

func TestFallbackFlagsAreBoundToViper(t *testing.T) {
	for _, name := range []string{"devs-parallel-fallback", "sentiment-fallback"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil {
			t.Fatalf("expected flag %q to be registered", name)
		}
		previousFlag := flag.Value.String()
		previousViper := viper.GetBool(name)
		defer func(name, previousFlag string, previousViper bool) {
			if err := rootCmd.PersistentFlags().Set(name, previousFlag); err != nil {
				t.Fatalf("failed to restore flag %q: %v", name, err)
			}
			viper.Set(name, previousViper)
		}(name, previousFlag, previousViper)

		if err := rootCmd.PersistentFlags().Set(name, "true"); err != nil {
			t.Fatalf("failed to set flag %q: %v", name, err)
		}
		if !viper.GetBool(name) {
			t.Fatalf("expected viper key %q to follow flag value", name)
		}
	}
}

func TestRepositoryModeRequirementsCoverConcreteModes(t *testing.T) {
	concreteModes := []string{
		"burndown-project",
		"burndown-file",
		"burndown-person",
		"burndown-repository",
		"burndown-repos-combined",
		"overwrites-matrix",
		"ownership",
		"couples-files",
		"couples-people",
		"couples-shotness",
		"shotness",
		"devs",
		"devs-efforts",
		"old-vs-new",
		"languages",
		"temporal-activity",
		"devs-parallel",
		"run-times",
		"bus-factor",
		"ownership-concentration",
		"knowledge-diffusion",
		"hotspot-risk",
		"sentiment",
		"refactoring-proxy",
	}

	if len(repositoryModeRequirements) != len(concreteModes) {
		t.Fatalf(
			"repositoryModeRequirements has %d entries, want %d",
			len(repositoryModeRequirements), len(concreteModes),
		)
	}
	for _, mode := range concreteModes {
		requirement, exists := repositoryModeRequirements[mode]
		if !exists {
			t.Errorf("repositoryModeRequirements is missing %q", mode)
			continue
		}
		if len(requirement.Analyses) == 0 && requirement.Unsupported == "" {
			t.Errorf("repositoryModeRequirements[%q] has neither analyses nor an unsupported reason", mode)
		}
	}
}

func TestRequiredAnalysesForModesReturnsSortedUnion(t *testing.T) {
	got, err := requiredAnalysesForModes([]string{
		"burndown-file",
		"devs-parallel",
		"temporal-activity",
		"burndown-person",
	})
	if err != nil {
		t.Fatalf("requiredAnalysesForModes() unexpected error: %v", err)
	}
	want := []string{"burndown", "burndown-files", "burndown-people", "couples", "devs", "temporal-activity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requiredAnalysesForModes() = %#v, want %#v", got, want)
	}
}

func TestRequiredAnalysesForModesAggregatesUnsupportedModes(t *testing.T) {
	_, err := requiredAnalysesForModes([]string{
		"burndown-repository",
		"burndown-repos-combined",
	})
	if err == nil {
		t.Fatal("requiredAnalysesForModes() expected an error")
	}
	for _, mode := range []string{"burndown-repository", "burndown-repos-combined"} {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("requiredAnalysesForModes() error %q does not mention %q", err, mode)
		}
	}
}

func TestDiscoverGitRepositorySupportsWorktreesAndBareRepositories(t *testing.T) {
	parent := t.TempDir()
	mainRepo := filepath.Join(parent, "main")
	runTestGit(t, parent, "init", mainRepo)
	runTestGit(t, mainRepo, "config", "user.name", "Labours Test")
	runTestGit(t, mainRepo, "config", "user.email", "labours@example.com")
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write repository fixture: %v", err)
	}
	runTestGit(t, mainRepo, "add", "README.md")
	runTestGit(t, mainRepo, "commit", "-m", "initial")

	nested := filepath.Join(mainRepo, "nested", "directory")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("create nested repository directory: %v", err)
	}
	got, err := discoverGitRepository(nested)
	if err != nil {
		t.Fatalf("discoverGitRepository(nested) unexpected error: %v", err)
	}
	if got != mainRepo {
		t.Fatalf("discoverGitRepository(nested) = %q, want %q", got, mainRepo)
	}

	linkedWorktree := filepath.Join(parent, "linked")
	runTestGit(t, mainRepo, "worktree", "add", "-b", "linked-test", linkedWorktree)
	got, err = discoverGitRepository(linkedWorktree)
	if err != nil {
		t.Fatalf("discoverGitRepository(worktree) unexpected error: %v", err)
	}
	if got != linkedWorktree {
		t.Fatalf("discoverGitRepository(worktree) = %q, want %q", got, linkedWorktree)
	}

	bareRepo := filepath.Join(parent, "bare.git")
	runTestGit(t, parent, "init", "--bare", bareRepo)
	got, err = discoverGitRepository(bareRepo)
	if err != nil {
		t.Fatalf("discoverGitRepository(bare) unexpected error: %v", err)
	}
	if got != bareRepo {
		t.Fatalf("discoverGitRepository(bare) = %q, want %q", got, bareRepo)
	}
}

func TestFromRepoRendersExactRequestedModeWithOneHerculesRun(t *testing.T) {
	previousLookPath := lookPath
	previousDiscover := discoverRepository
	previousRun := runRepositoryHercules
	previousRender := renderRepositoryModes
	defer func() {
		lookPath = previousLookPath
		discoverRepository = previousDiscover
		runRepositoryHercules = previousRun
		renderRepositoryModes = previousRender
	}()

	viperKeys := []string{
		"modes", "mode", "hercules", "output", "quiet", "sentiment",
		"start-date", "end-date",
	}
	previousViper := make(map[string]any, len(viperKeys))
	for _, key := range viperKeys {
		previousViper[key] = viper.Get(key)
	}
	defer func() {
		for key, value := range previousViper {
			viper.Set(key, value)
		}
	}()

	tests := []struct {
		mode     string
		analyses []string
	}{
		{mode: "temporal-activity", analyses: []string{"temporal-activity"}},
		{mode: "bus-factor", analyses: []string{"bus-factor"}},
		{mode: "hotspot-risk", analyses: []string{"hotspot-risk"}},
		{mode: "couples-people", analyses: []string{"couples"}},
		{mode: "burndown-file", analyses: []string{"burndown", "burndown-files"}},
	}

	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			viper.Set("modes", []string{test.mode})
			viper.Set("mode", []string{})
			viper.Set("hercules", "configured-hercules")
			viper.Set("output", t.TempDir())
			viper.Set("quiet", true)
			viper.Set("sentiment", false)
			viper.Set("start-date", "")
			viper.Set("end-date", "")

			var herculesRuns int
			var renderedModes []string
			var actualAnalyses []string
			lookPath = func(name string) (string, error) {
				if name != "configured-hercules" {
					t.Fatalf("lookPath() name = %q, want configured-hercules", name)
				}
				return "/resolved/hercules", nil
			}
			discoverRepository = func(path string) (string, error) {
				if path != "requested-repository" {
					t.Fatalf("discoverRepository() path = %q, want requested-repository", path)
				}
				return "/resolved/repository", nil
			}
			runRepositoryHercules = func(
				herculesPath, repoPath string, analyses []string,
			) (readers.Reader, error) {
				herculesRuns++
				if herculesPath != "/resolved/hercules" || repoPath != "/resolved/repository" {
					t.Fatalf("runRepositoryHercules() paths = %q, %q", herculesPath, repoPath)
				}
				actualAnalyses = append([]string(nil), analyses...)
				return nil, nil
			}
			renderRepositoryModes = func(
				_ readers.Reader, modes []string, _ render.Options,
			) render.Result {
				renderedModes = append([]string(nil), modes...)
				return render.Result{}
			}

			if err := handleHerculesIntegration("requested-repository"); err != nil {
				t.Fatalf("handleHerculesIntegration() unexpected error: %v", err)
			}
			if herculesRuns != 1 {
				t.Fatalf("Hercules ran %d times, want exactly once", herculesRuns)
			}
			if !reflect.DeepEqual(actualAnalyses, test.analyses) {
				t.Fatalf("Hercules analyses = %#v, want %#v", actualAnalyses, test.analyses)
			}
			if !reflect.DeepEqual(renderedModes, []string{test.mode}) {
				t.Fatalf("rendered modes = %#v, want exact mode %q", renderedModes, test.mode)
			}
		})
	}
}

func TestRunHerculesAndVisualizeReturnsAggregateRenderErrors(t *testing.T) {
	previousRun := runRepositoryHercules
	previousRender := renderRepositoryModes
	defer func() {
		runRepositoryHercules = previousRun
		renderRepositoryModes = previousRender
	}()

	previousOutput := viper.Get("output")
	viper.Set("output", t.TempDir())
	defer viper.Set("output", previousOutput)

	runRepositoryHercules = func(string, string, []string) (readers.Reader, error) {
		return nil, nil
	}
	renderRepositoryModes = func(readers.Reader, []string, render.Options) render.Result {
		return render.Result{Modes: []render.ModeResult{
			{Mode: "temporal-activity", Err: errors.New("temporal failure")},
			{Mode: "bus-factor", Err: errors.New("bus failure")},
		}}
	}

	err := runHerculesAndVisualize(
		"/resolved/hercules",
		"/resolved/repository",
		[]string{"temporal-activity", "bus-factor"},
		[]string{"bus-factor", "temporal-activity"},
	)
	if err == nil {
		t.Fatal("runHerculesAndVisualize() expected an error")
	}
	for _, message := range []string{"temporal failure", "bus failure"} {
		if !strings.Contains(err.Error(), message) {
			t.Errorf("aggregate error %q does not contain %q", err, message)
		}
	}
}

func runTestGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git executable: %v", err)
	}
	cmd := exec.Command(gitPath, arguments...) // #nosec G204 -- test executable is resolved with exec.LookPath.
	cmd.Dir = directory
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
