package e2e

import (
	"bytes"
	"errors"
	"fmt"
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
	err := os.WriteFile(staleFile, []byte("replace me"), 0o600)
	if err != nil {
		t.Fatalf("write stale cache file: %v", err)
	}
	result = runCommand(
		t, hercules, "--commits-stat", "--pb", "--quiet", "--force-cache-replace",
		"--", remote, cache,
	)
	requireSuccess(t, result)
	requireRegularFile(t, filepath.Join(cache, remoteCacheMarker))
	_, err = os.Stat(staleFile)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forced replacement left stale cache data behind: %v", err)
	}

	unrelated := filepath.Join(t.TempDir(), "unrelated")
	err = os.Mkdir(unrelated, 0o750)
	if err != nil {
		t.Fatalf("create unrelated directory: %v", err)
	}
	sentinel := filepath.Join(unrelated, "keep-me")
	err = os.WriteFile(sentinel, []byte("unrelated"), 0o600)
	if err != nil {
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
	err := proto.Unmarshal(result.stdout, &combined)
	if err != nil {
		t.Fatalf("decode combined protobuf: %v", err)
	}
	err = analysisio.ValidateAndMigrateAnalysisResults(
		&combined, analysisio.DefaultLimits(),
	)
	if err != nil {
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
	command := exec.CommandContext(t.Context(), binary, arguments...)
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
	err = os.WriteFile(readme, []byte("# E2E fixture\n"), 0o600)
	if err != nil {
		t.Fatalf("write repository fixture: %v", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatalf("open repository worktree: %v", err)
	}
	_, err = worktree.Add("README.md")
	if err != nil {
		t.Fatalf("stage repository fixture: %v", err)
	}
	signature := &object.Signature{
		Name:  "Hercules CI",
		Email: "hercules-ci@example.invalid",
		When:  time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
	}
	_, err = worktree.Commit("Initial commit", &git.CommitOptions{
		Author: signature, Committer: signature,
	})
	if err != nil {
		t.Fatalf("commit repository fixture: %v", err)
	}
	return path
}

// createRepositoryWithLargeFile adds a commit containing a file well above the blob cache limit
// used by TestOversizedBlobPolicy, so the CLI has to apply the skip-or-abort policy.
func createRepositoryWithLargeFile(t *testing.T) string {
	t.Helper()
	path := createRepository(t)
	repository, err := git.PlainOpen(path)
	if err != nil {
		t.Fatalf("open repository fixture: %v", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatalf("open repository worktree: %v", err)
	}
	large := filepath.Join(path, "large.txt")
	payload := bytes.Repeat([]byte("hercules oversized blob fixture\n"), 1024)
	err = os.WriteFile(large, payload, 0o600)
	if err != nil {
		t.Fatalf("write large repository fixture: %v", err)
	}
	_, err = worktree.Add("large.txt")
	if err != nil {
		t.Fatalf("stage large repository fixture: %v", err)
	}
	signature := &object.Signature{
		Name:  "Hercules CI",
		Email: "hercules-ci@example.invalid",
		When:  time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
	}
	_, err = worktree.Commit("Add a large file", &git.CommitOptions{
		Author: signature, Committer: signature,
	})
	if err != nil {
		t.Fatalf("commit large repository fixture: %v", err)
	}
	return path
}

// TestOversizedBlobPolicy pins the exit codes of the blob cache limits. Only an end-to-end run can
// show that an oversized blob no longer aborts the process, since the symptom was a CLI abort.
func TestOversizedBlobPolicy(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	repository := createRepositoryWithLargeFile(t)

	tolerant := runCommand(
		t, hercules, "--burndown", "--quiet", "--blob-cache-max-blob-size=1024", repository,
	)
	requireSuccess(t, tolerant)
	if !bytes.Contains(tolerant.stderr, []byte("large.txt")) {
		t.Fatalf("the oversized blob was skipped without a warning\nstderr:\n%s", tolerant.stderr)
	}
	if !bytes.Contains(tolerant.stderr, []byte("--fail-on-oversized-blobs")) {
		t.Fatalf("the warning does not mention the strict flag\nstderr:\n%s", tolerant.stderr)
	}
	if len(bytes.TrimSpace(tolerant.stdout)) == 0 {
		t.Fatal("the tolerant run produced no analysis output")
	}

	strict := runCommand(
		t, hercules, "--burndown", "--quiet", "--blob-cache-max-blob-size=1024",
		"--fail-on-oversized-blobs", repository,
	)
	if strict.exitCode == 0 {
		t.Fatalf("--fail-on-oversized-blobs did not abort\nstderr:\n%s", strict.stderr)
	}

	// Without a limit low enough to hit, nothing changes.
	normal := runCommand(t, hercules, "--burndown", "--quiet", repository)
	requireSuccess(t, normal)
	if bytes.Contains(normal.stderr, []byte("--fail-on-oversized-blobs")) {
		t.Fatalf("an unrelated run warned about oversized blobs\nstderr:\n%s", normal.stderr)
	}
}

// createRepositoryWithAmbiguousRenames builds a history whose last commit deletes one file and
// adds two near-identical successors, so rename detection has to choose between two plausible
// partners. That choice used to be made by whichever of two concurrent matchers finished first.
func createRepositoryWithAmbiguousRenames(t *testing.T) string {
	t.Helper()

	path := createRepository(t)

	repository, err := git.PlainOpen(path)
	if err != nil {
		t.Fatalf("open repository fixture: %v", err)
	}

	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatalf("open repository worktree: %v", err)
	}

	body := func(marker int) []byte {
		var text bytes.Buffer

		text.WriteString("package main\n\nfunc main() {\n")

		for i := range 40 {
			suffix := "aaa"
			if i == marker {
				suffix = "zzz"
			}

			fmt.Fprintf(&text, "\tprintln(%q, %02d)\n", suffix, i)
		}

		text.WriteString("}\n")

		return text.Bytes()
	}

	commit := func(day int, message string, files map[string][]byte, removed ...string) {
		t.Helper()

		for name, content := range files {
			target := filepath.Join(path, name)
			err := os.MkdirAll(filepath.Dir(target), 0o750)
			if err != nil {
				t.Fatalf("create directory for %s: %v", name, err)
			}

			err = os.WriteFile(target, content, 0o600)
			if err != nil {
				t.Fatalf("write %s: %v", name, err)
			}

			_, err = worktree.Add(name)
			if err != nil {
				t.Fatalf("stage %s: %v", name, err)
			}
		}

		for _, name := range removed {
			_, err := worktree.Remove(name)
			if err != nil {
				t.Fatalf("remove %s: %v", name, err)
			}
		}

		signature := &object.Signature{
			Name:  "Hercules CI",
			Email: "hercules-ci@example.invalid",
			When:  time.Date(2026, time.July, day, 12, 0, 0, 0, time.UTC),
		}
		_, err := worktree.Commit(message, &git.CommitOptions{
			Author: signature, Committer: signature,
		})
		if err != nil {
			t.Fatalf("commit %q: %v", message, err)
		}
	}

	commit(26, "Add thing.go", map[string][]byte{"old/thing.go": body(0)})
	commit(27, "Split thing.go in two", map[string][]byte{
		"left/zzz.go":    body(3),
		"right/thing.go": body(7),
	}, "old/thing.go")

	return path
}

// TestBurndownOutputIsReproducible checks the whole CLI pipeline end to end: the same binary on
// the same repository must produce the same burndown, which is what PLAN.md B12 broke.
//
// It is deliberately not the gate for B12's own cause. Verified by reintroducing the racing
// matcher pair: this test still passed, because on a fixture this small one goroutine wins every
// time. A timing race cannot be gated by repetition without being flaky. The gates for the race
// itself are TestMatchSimilarRenamesPicksDirectionBySetSize and
// TestRenameAnalysisConsumeIsReproducible in internal/plumbing, both of which do fail when the
// race is restored. What this test covers that they cannot is everything downstream of rename
// matching - line history, burndown accumulation, identities, serialization - going unstable.
func TestBurndownOutputIsReproducible(t *testing.T) {
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	repository := createRepositoryWithAmbiguousRenames(t)

	// run_time is a measured duration and is expected to differ; nothing else may.
	stripVolatile := func(output []byte) string {
		var kept []string

		for line := range strings.SplitSeq(string(output), "\n") {
			if strings.Contains(line, "run_time") {
				continue
			}

			kept = append(kept, line)
		}

		return strings.Join(kept, "\n")
	}

	var reference string

	for attempt := range 3 {
		result := runCommand(t, hercules, "--burndown", "--burndown-people", "--quiet", repository)
		requireSuccess(t, result)

		output := stripVolatile(result.stdout)
		if attempt == 0 {
			if len(strings.TrimSpace(output)) == 0 {
				t.Fatal("the run produced no analysis output")
			}

			reference = output

			continue
		}

		if output != reference {
			t.Fatalf("run %d differs from run 1; burndown output is not reproducible", attempt+1)
		}
	}
}

func repositoryPath(elements ...string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate e2e test source")
	}
	parts := make([]string, 0, 3+len(elements))
	parts = append(parts, filepath.Dir(file), "..", "..")
	parts = append(parts, elements...)
	return filepath.Clean(filepath.Join(parts...))
}
