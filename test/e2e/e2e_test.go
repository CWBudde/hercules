package e2e

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

const (
	herculesBinaryEnvironment = "HERCULES_E2E_BIN"
	laboursBinaryEnvironment  = "LABOURS_E2E_BIN"
	remoteCacheMarker         = ".hercules-cache"
)

type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

func TestCLIExitCodes(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	labours := requiredBinary(t, laboursBinaryEnvironment)

	result := runCommand(t, hercules)
	if result.exitCode == 0 {
		t.Fatalf("hercules without a repository exited successfully\nstderr:\n%s", result.stderr)
	}
	if len(bytes.TrimSpace(result.stderr)) == 0 {
		t.Fatal("hercules argument failure did not write a diagnostic to stderr")
	}

	result = runCommand(t, hercules, "version")
	if result.exitCode != 0 {
		t.Fatalf("hercules version exit code = %d\nstderr:\n%s", result.exitCode, result.stderr)
	}
	if !bytes.Contains(result.stdout, []byte("Schema:")) {
		t.Fatalf("hercules version output omitted schema version:\n%s", result.stdout)
	}

	missingInput := filepath.Join(t.TempDir(), "missing.pb")
	result = runCommand(t, labours, "--quiet", "--input", missingInput, "--mode", "devs")
	if result.exitCode == 0 {
		t.Fatalf("labours accepted a missing input\nstdout:\n%s", result.stdout)
	}
	if len(bytes.TrimSpace(result.stderr)) == 0 {
		t.Fatal("labours input failure did not write a diagnostic to stderr")
	}
}

func TestDocumentedHelpAndREADMEExamples(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	labours := requiredBinary(t, laboursBinaryEnvironment)

	herculesHelp := runCommand(t, hercules, "--help")
	requireSuccess(t, herculesHelp)
	laboursHelp := runCommand(t, labours, "--help")
	requireSuccess(t, laboursHelp)
	var knownHerculesFlags strings.Builder
	knownHerculesFlags.Write(herculesHelp.stdout)
	for _, command := range []string{"report", "combine", "generate-plugin", "completion"} {
		result := runCommand(t, hercules, command, "--help")
		requireSuccess(t, result)
		knownHerculesFlags.Write(result.stdout)
	}

	readmePath := repositoryPath("README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README examples: %v", err)
	}
	for _, test := range []struct {
		name  string
		help  string
		flags []string
	}{
		{
			name:  "hercules",
			help:  knownHerculesFlags.String(),
			flags: documentedCommandFlags(string(readme), "hercules"),
		},
		{
			name:  "labours",
			help:  string(laboursHelp.stdout),
			flags: documentedCommandFlags(string(readme), "labours"),
		},
	} {
		t.Run(test.name+" flags", func(t *testing.T) {
			for _, flag := range test.flags {
				if !strings.Contains(test.help, flag) {
					t.Errorf("README command uses %s %s, but it is absent from CLI help", test.name, flag)
				}
			}
		})
	}

	// Exercise the README's basic Hercules -> Labours workflow without network access.
	repository := createRepository(t)
	analysis := runCommand(
		t, hercules, "--preset", "quick", "--burndown", "--pb", "--quiet", repository,
	)
	requireSuccess(t, analysis)
	analysisPath := filepath.Join(t.TempDir(), "burndown.pb")
	err = os.WriteFile(analysisPath, analysis.stdout, 0o600)
	if err != nil {
		t.Fatalf("write documented workflow output: %v", err)
	}
	chartPath := filepath.Join(t.TempDir(), "burndown.png")
	rendered := runCommand(
		t, labours, "--quiet", "--input", analysisPath, "--input-format", "pb",
		"--mode", "burndown-project", "--resample", "month", "--output", chartPath,
	)
	requireSuccess(t, rendered)
	requireRegularFile(t, chartPath)
}

var documentedFlagPattern = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

func documentedCommandFlags(markdown, binary string) []string {
	flags := map[string]struct{}{}
	inFence := false
	for line := range strings.Lines(markdown) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence || strings.Contains(line, "docker ") {
			continue
		}
		_, command, found := strings.Cut(line, binary)
		if !found {
			continue
		}
		if beforePipe, _, hasPipe := strings.Cut(command, "|"); hasPipe {
			command = beforePipe
		}
		for _, flag := range documentedFlagPattern.FindAllString(command, -1) {
			flags[flag] = struct{}{}
		}
	}
	result := make([]string, 0, len(flags))
	for flag := range flags {
		result = append(result, flag)
	}
	return result
}

func TestRemoteCacheSafety(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	repository := createRepository(t)
	remote := (&url.URL{Scheme: "file", Path: filepath.ToSlash(repository)}).String()

	cache := filepath.Join(t.TempDir(), "managed-cache")
	result := runCommand(
		t, hercules, "--commits-stat", "--pb", "--quiet", "--", remote, cache,
	)
	requireSuccess(t, result)
	requireRegularFile(t, filepath.Join(cache, remoteCacheMarker))

	staleFile := filepath.Join(cache, "stale-user-data")
	if err := os.WriteFile(staleFile, []byte("replace me"), 0o600); err != nil {
		t.Fatalf("write stale cache file: %v", err)
	}
	result = runCommand(
		t, hercules, "--commits-stat", "--pb", "--quiet", "--force-cache-replace",
		"--", remote, cache,
	)
	requireSuccess(t, result)
	requireRegularFile(t, filepath.Join(cache, remoteCacheMarker))
	if _, err := os.Stat(staleFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forced replacement left stale cache data behind: %v", err)
	}

	unrelated := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Mkdir(unrelated, 0o750); err != nil {
		t.Fatalf("create unrelated directory: %v", err)
	}
	sentinel := filepath.Join(unrelated, "keep-me")
	if err := os.WriteFile(sentinel, []byte("unrelated"), 0o600); err != nil {
		t.Fatalf("write unrelated sentinel: %v", err)
	}
	result = runCommand(
		t, hercules, "--commits-stat", "--pb", "--quiet", "--force-cache-replace",
		"--", remote, unrelated,
	)
	if result.exitCode == 0 {
		t.Fatal("forced cache replacement accepted an unrelated non-empty directory")
	}
	requireRegularFile(t, sentinel)
}

func TestCombineCommand(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	fixture := repositoryPath("internal", "render", "testdata", "hercules", "report_default.pb")

	result := runCommand(t, hercules, "combine", "--only", "Devs", fixture, fixture)
	requireSuccess(t, result)

	var combined pb.AnalysisResults
	if err := proto.Unmarshal(result.stdout, &combined); err != nil {
		t.Fatalf("decode combined protobuf: %v", err)
	}
	if err := analysisio.ValidateAndMigrateAnalysisResults(
		&combined, analysisio.DefaultLimits(),
	); err != nil {
		t.Fatalf("validate combined protobuf: %v", err)
	}
	if len(combined.GetContents()["Devs"]) == 0 {
		t.Fatalf("combined protobuf omitted Devs result: %v", combined.GetContents())
	}
}

func TestReportCommand(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	fixture := repositoryPath("cmd", "hercules", "test_data", "hercules.siva")
	output := filepath.Join(t.TempDir(), "report")

	result := runCommand(
		t, hercules, "report", "--output", output,
		"--analysis", "devs", "--mode", "devs", fixture,
	)
	requireSuccess(t, result)
	for _, path := range []string{
		filepath.Join(output, "index.html"),
		filepath.Join(output, "manifest.json"),
		filepath.Join(output, "report.pb"),
		filepath.Join(output, "charts", "devs.png"),
	} {
		requireRegularFile(t, path)
	}
}

func TestLaboursFromRepository(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	labours := requiredBinary(t, laboursBinaryEnvironment)
	repository := createRepository(t)
	output := filepath.Join(t.TempDir(), "devs.png")

	result := runCommand(
		t, labours, "--hercules", hercules, "--from-repo", repository,
		"--mode", "devs", "--hercules-flags=--head", "--output", output, "--quiet",
	)
	requireSuccess(t, result)
	requireRegularFile(t, output)
}

func requiredBinary(t *testing.T, environment string) string {
	t.Helper()
	path := os.Getenv(environment)
	if path == "" {
		t.Skipf("%s is unset; build the CLI binaries or run `just test-e2e`", environment)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", environment, err)
	}
	requireRegularFile(t, absolute)
	return absolute
}

func runCommand(t *testing.T, binary string, arguments ...string) commandResult {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = repositoryPath()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if err == nil {
		return result
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run %s %s: %v", binary, strings.Join(arguments, " "), err)
	}
	result.exitCode = exitError.ExitCode()
	return result
}

func requireSuccess(t *testing.T, result commandResult) {
	t.Helper()
	if result.exitCode != 0 {
		t.Fatalf(
			"command exit code = %d\nstdout:\n%s\nstderr:\n%s",
			result.exitCode, result.stdout, result.stderr,
		)
	}
}

func requireRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("%s is not a non-empty regular file: mode=%s size=%d", path, info.Mode(), info.Size())
	}
}

func createRepository(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository")
	repository, err := git.PlainInit(path, false)
	if err != nil {
		t.Fatalf("initialize Git repository: %v", err)
	}
	readme := filepath.Join(path, "README.md")
	if err := os.WriteFile(readme, []byte("# E2E fixture\n"), 0o600); err != nil {
		t.Fatalf("write repository fixture: %v", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatalf("open repository worktree: %v", err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatalf("stage repository fixture: %v", err)
	}
	signature := &object.Signature{
		Name:  "Hercules CI",
		Email: "hercules-ci@example.invalid",
		When:  time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
	}
	if _, err := worktree.Commit("Initial commit", &git.CommitOptions{
		Author: signature, Committer: signature,
	}); err != nil {
		t.Fatalf("commit repository fixture: %v", err)
	}
	return path
}

func repositoryPath(elements ...string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate e2e test source")
	}
	parts := []string{filepath.Dir(file), "..", ".."}
	parts = append(parts, elements...)
	return filepath.Clean(filepath.Join(parts...))
}
