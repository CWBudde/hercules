// Package corpus holds the opt-in burndown regression suite. It replays a fixed
// analysis over real repositories and compares the burndown negativity metrics
// against a committed baseline.
//
// The suite deliberately does NOT assert that no negative cells exist. Today
// they do exist in quantity, so an absence assertion would be red from birth and
// promptly ignored. Instead it asserts no *regression* against
// baseline.json, which is what makes the merge-accounting work measurable: a
// number going up fails, a number going down is reported so a real fix is
// visible.
//
// There are no build tags here on purpose (matching the rest of test/): gating is
// a t.Skip on HERCULES_CORPUS_DIR, so `go test ./...` stays green and fast.
package corpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	herculesBinaryEnvironment  = "HERCULES_E2E_BIN"
	corpusDirectoryEnvironment = "HERCULES_CORPUS_DIR"
	updateBaselineEnvironment  = "UPDATE_CORPUS_BASELINE"
)

// analysisFlags is the flag set PLAN.md's negativity measurements used. Keep it
// in sync with the baseline: changing it invalidates every committed number.
var analysisFlags = []string{
	"--burndown",
	"--burndown-people",
	"--devs",
	"--couples",
	"--bus-factor",
	"--ownership-concentration",
	"--hotspot-risk",
	"--skip-blacklist",
	"--granularity=30",
	"--sampling=30",
}

// corpusRepositories are the repositories PLAN.md names, resolved relative to
// HERCULES_CORPUS_DIR. Missing ones are skipped, so a partial checkout works.
var corpusRepositories = []string{
	"mekorp-backend",
	"mekorp-webclient",
	"meko-etl-tool",
	"backend-for-microscope",
	"render-pdf",
	"personio-ipoffice-sync",
	"ewws-wiki",
	"process-manager",
	"ewws-render",
	"go-clients",
	"ewws-tailscale",
	"MKTools",
	"ShelfStockScanner",
}

// metrics summarises burndown negativity for one repository.
//
// NegativeCells/NegativeMass/WorstCell describe every negative cell anywhere in
// the sampled history, including cells that a later sample repairs. ResidualCells
// counts only the negatives that survive into the final sampled row — the
// transient/residual split PLAN.md uses as its discriminator, and the honest
// measure of the defect. Empirically every negative is residual today, so a rise
// in ResidualCells is the strongest available regression signal.
type metrics struct {
	ExitCode      int `json:"exit_code"`
	NegativeCells int `json:"negative_cells"`
	NegativeMass  int `json:"negative_mass"`
	WorstCell     int `json:"worst_cell"`
	ResidualCells int `json:"residual_cells"`
	Matrices      int `json:"matrices"`
}

// baseline is the committed manifest. Provenance records the tree the numbers
// were measured against, because the numbers only mean something relative to it.
type baseline struct {
	Provenance   provenance         `json:"provenance"`
	Flags        []string           `json:"flags"`
	Repositories map[string]metrics `json:"repositories"`
}

type provenance struct {
	Commit      string `json:"commit"`
	WorkingTree string `json:"working_tree"`
	Note        string `json:"note"`
}

func baselinePath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate corpus test source")
	}

	return filepath.Join(filepath.Dir(file), "baseline.json")
}

// TestCorpusBurndownNegativity is the whole suite. Subtests are sequential on
// purpose: hercules is already internally parallel and these runs are CPU- and
// memory-heavy (mekorp-webclient has >11k commits), so running repositories
// concurrently mostly trades wall-clock for OOM risk and noisier timings.
func TestCorpusBurndownNegativity(t *testing.T) {
	corpus := requiredCorpus(t)
	hercules := requiredBinary(t, herculesBinaryEnvironment)
	updating := os.Getenv(updateBaselineEnvironment) == "1"

	committed := readBaseline(t)
	measured := map[string]metrics{}

	for _, name := range corpusRepositories {
		path := filepath.Join(corpus, name)
		if !isDirectory(path) {
			t.Logf("%s: absent from %s; skipping", name, corpus)
			continue
		}

		expected, known := committed.Repositories[name]
		if !known && !updating {
			t.Logf(
				"%s: no baseline entry; skipping. Run `just update-corpus-baseline` to add it.",
				name,
			)

			continue
		}

		t.Run(name, func(t *testing.T) {
			actual := measure(t, hercules, path)
			t.Logf("%s: %s", name, describe(actual))

			if updating {
				measured[name] = actual
				return
			}

			compare(t, expected, actual)
		})
	}

	if updating {
		writeBaseline(t, committed, measured)
	}
}

// compare fails on any metric that got worse and logs any that got better.
func compare(t *testing.T, expected, actual metrics) {
	t.Helper()

	if actual.ExitCode != expected.ExitCode {
		t.Errorf("exit code = %d, baseline %d", actual.ExitCode, expected.ExitCode)
	}

	// WorstCell is the most negative value, so "worse" means smaller.
	for _, check := range []struct {
		name             string
		actual, expected int
		worseWhenSmaller bool
	}{
		{name: "negative cells", actual: actual.NegativeCells, expected: expected.NegativeCells},
		{name: "negative mass", actual: actual.NegativeMass, expected: expected.NegativeMass},
		{name: "residual cells", actual: actual.ResidualCells, expected: expected.ResidualCells},
		{
			name: "worst cell", actual: actual.WorstCell, expected: expected.WorstCell,
			worseWhenSmaller: true,
		},
	} {
		worse := check.actual > check.expected
		better := check.actual < check.expected

		if check.worseWhenSmaller {
			worse, better = better, worse
		}

		switch {
		case worse:
			t.Errorf("%s regressed: %d, baseline %d", check.name, check.actual, check.expected)
		case better:
			t.Logf(
				"%s improved: %d, baseline %d — refresh the baseline once the fix lands",
				check.name, check.actual, check.expected,
			)
		}
	}
}

func describe(m metrics) string {
	return fmt.Sprintf(
		"exit=%d matrices=%d negative_cells=%d negative_mass=%d worst_cell=%d residual_cells=%d",
		m.ExitCode, m.Matrices, m.NegativeCells, m.NegativeMass, m.WorstCell, m.ResidualCells,
	)
}

// measure runs the analysis and reduces its YAML burndown matrices to metrics.
func measure(t *testing.T, hercules, repository string) metrics {
	t.Helper()

	arguments := append(append([]string{}, analysisFlags...), repository)
	command := exec.Command(hercules, arguments...)

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr

	result := metrics{}

	err := command.Run()
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run %s %s: %v", hercules, strings.Join(arguments, " "), err)
		}

		result.ExitCode = exitError.ExitCode()
		t.Errorf(
			"analysis exit code = %d\nstderr:\n%s",
			result.ExitCode, tail(stderr.Bytes()),
		)

		return result
	}

	for _, matrix := range burndownMatrices(t, stdout.Bytes()) {
		result.Matrices++
		accumulate(&result, matrix)
	}

	return result
}

func accumulate(result *metrics, matrix [][]int) {
	for _, row := range matrix {
		for _, value := range row {
			if value >= 0 {
				continue
			}

			result.NegativeCells++
			result.NegativeMass += -value

			if value < result.WorstCell {
				result.WorstCell = value
			}
		}
	}

	if len(matrix) == 0 {
		return
	}

	for _, value := range matrix[len(matrix)-1] {
		if value < 0 {
			result.ResidualCells++
		}
	}
}

// burndownMatrices extracts every burndown band matrix from the YAML report:
// the project matrix plus the per-person, per-file and per-repository ones.
//
// people_interaction is deliberately excluded. It is a signed added/removed
// interaction matrix, not a band matrix, so its negatives are correct by
// construction and would drown the signal we care about.
//
// leaves/burndown_shared.go has an equivalent audit, but it is unexported and
// therefore unreachable from here; this stays small and local on purpose.
func burndownMatrices(t *testing.T, report []byte) [][][]int {
	t.Helper()

	var document map[string]any
	if err := yaml.Unmarshal(report, &document); err != nil {
		t.Fatalf("decode analysis YAML: %v", err)
	}

	section, ok := document["Burndown"].(map[string]any)
	if !ok {
		t.Fatalf("analysis output has no Burndown section (keys: %v)", sortedKeys(document))
	}

	var matrices [][][]int

	if project, ok := section["project"].(string); ok {
		matrices = append(matrices, parseMatrix(t, "project", project))
	}

	for _, group := range []string{"people", "files", "repositories"} {
		entries, ok := section[group].(map[string]any)
		if !ok {
			continue
		}

		for _, key := range sortedKeys(entries) {
			text, ok := entries[key].(string)
			if !ok {
				t.Fatalf("%s[%q] is not a matrix string", group, key)
			}

			matrices = append(matrices, parseMatrix(t, group+"["+key+"]", text))
		}
	}

	if len(matrices) == 0 {
		t.Fatal("Burndown section contained no matrices")
	}

	return matrices
}

// parseMatrix reads the whitespace-separated integer rows of a `|-` block.
func parseMatrix(t *testing.T, label, text string) [][]int {
	t.Helper()

	var matrix [][]int

	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		row := make([]int, len(fields))

		for i, field := range fields {
			value, err := strconv.Atoi(field)
			if err != nil {
				t.Fatalf("parse %s cell %q: %v", label, field, err)
			}

			row[i] = value
		}

		matrix = append(matrix, row)
	}

	return matrix
}

func readBaseline(t *testing.T) baseline {
	t.Helper()

	content, err := os.ReadFile(baselinePath())
	if err != nil {
		if os.Getenv(updateBaselineEnvironment) == "1" && errors.Is(err, os.ErrNotExist) {
			return baseline{Flags: analysisFlags, Repositories: map[string]metrics{}}
		}

		t.Fatalf("read corpus baseline: %v", err)
	}

	var committed baseline
	if err := json.Unmarshal(content, &committed); err != nil {
		t.Fatalf("decode corpus baseline: %v", err)
	}

	if committed.Repositories == nil {
		committed.Repositories = map[string]metrics{}
	}

	if len(committed.Flags) != 0 && !equalStrings(committed.Flags, analysisFlags) {
		t.Fatalf(
			"baseline was recorded with a different flag set and cannot be compared:\n"+
				"baseline: %s\ncurrent:  %s",
			strings.Join(committed.Flags, " "), strings.Join(analysisFlags, " "),
		)
	}

	return committed
}

// writeBaseline merges the freshly measured repositories into the committed
// manifest, so refreshing a subset never drops the entries it did not run.
func writeBaseline(t *testing.T, committed baseline, measured map[string]metrics) {
	t.Helper()

	if len(measured) == 0 {
		t.Log("no repositories measured; leaving the baseline untouched")
		return
	}

	committed.Flags = analysisFlags
	committed.Provenance = currentProvenance(t)

	for name, value := range measured {
		committed.Repositories[name] = value
	}

	content, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("encode corpus baseline: %v", err)
	}

	err = os.WriteFile(baselinePath(), append(content, '\n'), 0o600)
	if err != nil {
		t.Fatalf("write corpus baseline: %v", err)
	}

	t.Logf("refreshed %d baseline entries in %s", len(measured), baselinePath())
}

func currentProvenance(t *testing.T) provenance {
	t.Helper()

	repository := filepath.Dir(filepath.Dir(filepath.Dir(baselinePath())))
	result := provenance{
		Commit:      strings.TrimSpace(gitOutput(repository, "rev-parse", "HEAD")),
		WorkingTree: "clean",
		Note: "Numbers are only comparable against this tree. Re-seed after any " +
			"change to burndown or merge accounting.",
	}

	if status := strings.TrimSpace(gitOutput(repository, "status", "--short")); status != "" {
		result.WorkingTree = status
	}

	return result
}

func gitOutput(repository string, arguments ...string) string {
	command := exec.Command("git", arguments...)
	command.Dir = repository

	output, err := command.Output()
	if err != nil {
		return "unknown"
	}

	return string(output)
}

func requiredCorpus(t *testing.T) string {
	t.Helper()

	path := os.Getenv(corpusDirectoryEnvironment)
	if path == "" {
		t.Skipf(
			"%s is unset; run `just test-corpus` with a directory of real clones",
			corpusDirectoryEnvironment,
		)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", corpusDirectoryEnvironment, err)
	}

	if !isDirectory(absolute) {
		t.Fatalf("%s=%s is not a directory", corpusDirectoryEnvironment, absolute)
	}

	return absolute
}

func requiredBinary(t *testing.T, environment string) string {
	t.Helper()

	path := os.Getenv(environment)
	if path == "" {
		t.Skipf("%s is unset; build the CLI binaries or run `just test-corpus`", environment)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", environment, err)
	}

	requireRegularFile(t, absolute)

	return absolute
}

func requireRegularFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf(
			"%s is not a non-empty regular file: mode=%s size=%d",
			path, info.Mode(), info.Size(),
		)
	}
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

// tail keeps a failure message readable when a long analysis log precedes it.
func tail(content []byte) []byte {
	const limit = 4096
	if len(content) <= limit {
		return content
	}

	return content[len(content)-limit:]
}
