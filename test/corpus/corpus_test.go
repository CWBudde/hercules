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
// Each repository is measured in two independent dimensions, recorded under separate counters:
// the project/people/repository matrices of the historical flag set, and the per-file matrices of
// a second --burndown-files run. The per-file dimension is the one a change to file-identity
// accounting moves, and it was invisible to the suite until it got its own run.
//
// There are no build tags here on purpose (matching the rest of test/): gating is
// a t.Skip on HERCULES_CORPUS_DIR, so `go test ./...` stays green and fast.
package corpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// diffTimeoutFlag pins the per-file diff deadline far out of reach.
//
// --diff-timeout is in MILLISECONDS and defaults to 1000 (internal/plumbing/diff.go). At that
// default the file diff stage truncates on the larger repositories, and a truncated diff is a
// different answer than an untruncated one: the measurement then depends on how fast the machine
// is rather than on hercules. Measured on mekorp-webclient, the slowest repository in the corpus:
// 1447 negative cells truncated against 1413 untruncated, and two binaries with provably
// identical negativity reported 1447 and 1481. That is the same order as the regressions this
// suite exists to catch, so the deadline has to be unreachable rather than merely generous.
// 300000 ms (5 min per single file diff) was verified to leave mekorp-webclient untruncated.
const diffTimeoutFlag = "--diff-timeout=300000"

// analysisFlags is the flag set PLAN.md's negativity measurements used, plus the pinned diff
// deadline. Keep it in sync with the baseline: changing it invalidates every committed number —
// adding diffTimeoutFlag did exactly that, so every entry recorded before it is void and the
// baseline has to be re-seeded (test/corpus/README.md says so too).
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
	diffTimeoutFlag,
}

// fileAnalysisFlags measures the per-file burndown dimension, in a SECOND run per repository.
//
// Two reasons it is not folded into analysisFlags. First, the existing numbers stay produced by
// the command that produced them before, so they keep meaning what they meant. Second,
// --burndown-files emits one band matrix per file that ever existed: on the large repositories
// that is thousands of matrices and a report an order of magnitude bigger, and pairing that cost
// with --couples/--devs/--burndown-people in one process multiplies peak memory for no gain —
// none of those leaves influences the per-file matrices. The price is a second pass over each
// repository, which roughly doubles the suite's wall clock.
var fileAnalysisFlags = []string{
	"--burndown",
	"--burndown-files",
	"--skip-blacklist",
	"--granularity=30",
	"--sampling=30",
	diffTimeoutFlag,
}

// matrixScope selects which parts of the Burndown section a run is measured over.
//
// people_interaction is in neither scope: it is a signed added/removed interaction matrix, not a
// band matrix, so its negatives are correct by construction and would drown the signal.
type matrixScope struct {
	name    string
	project bool
	groups  []string
}

var (
	// projectScope is the historical scope: the project matrix plus the per-person and
	// per-repository ones. "files" is absent because analysisFlags does not request it.
	projectScope = matrixScope{
		name: "project", project: true, groups: []string{"people", "repositories"},
	}
	// fileScope is the new dimension, kept strictly disjoint from projectScope so the two sets
	// of counters never mix — the per-file run also emits a project matrix, and counting it
	// would double-count what the first run already measured.
	fileScope = matrixScope{name: "files", groups: []string{"files"}}
)

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
//
// Truncated is not a measurement but a verdict on one: it is set when the binary reported that a
// wall-clock deadline changed the analysis, which makes every other field in the struct a
// property of this machine rather than of hercules. Such a measurement is never gated and never
// written to the baseline.
type metrics struct {
	ExitCode      int  `json:"exit_code"`
	NegativeCells int  `json:"negative_cells"`
	NegativeMass  int  `json:"negative_mass"`
	WorstCell     int  `json:"worst_cell"`
	ResidualCells int  `json:"residual_cells"`
	Matrices      int  `json:"matrices"`
	Truncated     bool `json:"truncated,omitempty"`
}

// baseline is the committed manifest. Provenance records the tree the numbers
// were measured against, because the numbers only mean something relative to it.
//
// The per-file dimension is a separate run with a separate flag set, so it gets separate
// counters: file_flags/file_repositories mirror flags/repositories rather than folding into
// them, and the numbers under repositories keep meaning exactly what they meant before.
type baseline struct {
	Provenance       provenance         `json:"provenance"`
	Flags            []string           `json:"flags"`
	Repositories     map[string]metrics `json:"repositories"`
	FileFlags        []string           `json:"file_flags"`
	FileRepositories map[string]metrics `json:"file_repositories"`
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
	fileMeasured := map[string]metrics{}

	for _, name := range corpusRepositories {
		path := filepath.Join(corpus, name)
		if !isDirectory(path) {
			t.Logf("%s: absent from %s; skipping", name, corpus)
			continue
		}

		expected, known := committed.Repositories[name]
		fileExpected, fileKnown := committed.FileRepositories[name]

		if !known && !fileKnown && !updating {
			t.Logf(
				"%s: no baseline entry; skipping. Run `just update-corpus-baseline` to add it.",
				name,
			)

			continue
		}

		t.Run(name, func(t *testing.T) {
			if known || updating {
				actual := measure(t, hercules, path, analysisFlags, projectScope)
				t.Logf("%s: %s", name, describe(actual))

				switch {
				case actual.Truncated:
					// measure already failed the subtest; refuse to seed or gate a
					// number that only describes this machine.
				case updating:
					measured[name] = actual
				default:
					compare(t, projectScope.name, expected, actual)
				}
			}

			// The per-file dimension is a second, much larger run. Skip it when there is
			// nothing to compare against, so a corpus baselined before this dimension
			// existed does not silently double in runtime.
			if !fileKnown && !updating {
				t.Logf(
					"%s: no per-file baseline entry; skipping the --burndown-files run. "+
						"Run `just update-corpus-baseline` to add it.",
					name,
				)

				return
			}

			fileActual := measure(t, hercules, path, fileAnalysisFlags, fileScope)
			t.Logf("%s (files): %s", name, describe(fileActual))

			switch {
			case fileActual.Truncated:
				// As above: an invalid measurement is neither seeded nor gated.
			case updating:
				fileMeasured[name] = fileActual
			default:
				compare(t, fileScope.name, fileExpected, fileActual)
			}
		})
	}

	if updating {
		writeBaseline(t, committed, measured, fileMeasured)
	}
}

// compare fails on any gated metric that got worse and logs every other change.
//
// Only the counting metrics are gated. Burndown output is not bit-reproducible
// on repositories with substantial merge topology: replaying the same commit of
// hercules over the same clone three times yields three different Burndown
// sections. Across repeated full corpus passes the counts (negative cells,
// residual cells) reproduced exactly on every repository, while the magnitudes
// (negative mass, worst cell) drifted by fractions of a percent. So the counts
// are a sound gate and the magnitudes are not — gating them would make the suite
// flaky, and a flaky gate is a gate people learn to ignore. They are still
// recorded and reported, because a large move in either is worth a human look.
// Re-gate them once burndown is deterministic on merges.
func compare(t *testing.T, dimension string, expected, actual metrics) {
	t.Helper()

	if actual.ExitCode != expected.ExitCode {
		t.Errorf("[%s] exit code = %d, baseline %d", dimension, actual.ExitCode, expected.ExitCode)
	}

	// WorstCell is the most negative value, so "worse" means smaller.
	for _, check := range []struct {
		name             string
		actual, expected int
		worseWhenSmaller bool
		gated            bool
	}{
		{
			name: "negative cells", actual: actual.NegativeCells,
			expected: expected.NegativeCells, gated: true,
		},
		{
			name: "residual cells", actual: actual.ResidualCells,
			expected: expected.ResidualCells, gated: true,
		},
		{name: "negative mass", actual: actual.NegativeMass, expected: expected.NegativeMass},
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
		case worse && check.gated:
			t.Errorf(
				"[%s] %s regressed: %d, baseline %d",
				dimension, check.name, check.actual, check.expected,
			)
		case worse:
			t.Logf(
				"[%s] %s worsened (ungated, not reproducible run-to-run): %d, baseline %d",
				dimension, check.name, check.actual, check.expected,
			)
		case better:
			t.Logf(
				"[%s] %s improved: %d, baseline %d — refresh the baseline once the fix lands",
				dimension, check.name, check.actual, check.expected,
			)
		}
	}
}

func describe(m metrics) string {
	if m.Truncated {
		return "INVALID (truncated by a wall-clock deadline; nothing was measured)"
	}

	return fmt.Sprintf(
		"exit=%d matrices=%d negative_cells=%d negative_mass=%d worst_cell=%d residual_cells=%d",
		m.ExitCode, m.Matrices, m.NegativeCells, m.NegativeMass, m.WorstCell, m.ResidualCells,
	)
}

// truncationMarker is the substring both wall-clock truncation warnings in
// internal/plumbing/truncation.go end on. Matching the message rather than the exit status is
// deliberate: hercules treats truncation as a warning and still exits 0, so nothing else
// distinguishes a truncated run from a clean one.
const truncationMarker = "not reproducible"

// measure runs the analysis and reduces the YAML burndown matrices in `scope` to metrics.
//
// A run whose stderr admits truncation fails the subtest. Truncation is invisible in the output
// itself — the numbers look like ordinary numbers — so if the suite ignored the warning, a
// deadline that fired on a busy machine would arrive as a burndown regression and be debugged as
// one. That already happened once (see diffTimeoutFlag), which is why this is loud.
func measure(t *testing.T, hercules, repository string, flags []string, scope matrixScope) metrics {
	t.Helper()

	arguments := append(append([]string{}, flags...), repository)
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
			"[%s] analysis exit code = %d\nstderr:\n%s",
			scope.name, result.ExitCode, tail(stderr.Bytes()),
		)

		return result
	}

	if bytes.Contains(stderr.Bytes(), []byte(truncationMarker)) {
		result.Truncated = true

		t.Errorf(
			"[%s] MEASUREMENT INVALID: a wall-clock deadline truncated this run, so its numbers "+
				"describe this machine and not hercules. They are neither gated nor recorded. "+
				"Raise --diff-timeout/--renames-timeout and re-run.\nstderr:\n%s",
			scope.name, tail(stderr.Bytes()),
		)

		return result
	}

	for _, matrix := range burndownMatrices(t, stdout.Bytes(), scope) {
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

// burndownMatrices extracts the burndown band matrices `scope` selects from the YAML report.
//
// people_interaction is deliberately excluded from every scope. It is a signed added/removed
// interaction matrix, not a band matrix, so its negatives are correct by construction and would
// drown the signal we care about.
//
// leaves/burndown_shared.go has an equivalent audit, but it is unexported and
// therefore unreachable from here; this stays small and local on purpose.
func burndownMatrices(t *testing.T, report []byte, scope matrixScope) [][][]int {
	t.Helper()

	var document map[string]any
	err := yaml.Unmarshal(report, &document)
	if err != nil {
		t.Fatalf("decode analysis YAML: %v", err)
	}

	section, ok := document["Burndown"].(map[string]any)
	if !ok {
		t.Fatalf("analysis output has no Burndown section (keys: %v)", sortedKeys(document))
	}

	var matrices [][][]int

	if scope.project {
		if project, ok := section["project"].(string); ok {
			matrices = append(matrices, parseMatrix(t, "project", project))
		}
	}

	for _, group := range scope.groups {
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
		t.Fatalf(
			"Burndown section contained no %s matrices (keys: %v)",
			scope.name, sortedKeys(section),
		)
	}

	return matrices
}

// parseMatrix reads the whitespace-separated integer rows of a `|-` block.
func parseMatrix(t *testing.T, label, text string) [][]int {
	t.Helper()

	var matrix [][]int

	for line := range strings.SplitSeq(text, "\n") {
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
			return baseline{
				Flags:            analysisFlags,
				Repositories:     map[string]metrics{},
				FileFlags:        fileAnalysisFlags,
				FileRepositories: map[string]metrics{},
			}
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

	if committed.FileRepositories == nil {
		committed.FileRepositories = map[string]metrics{}
	}

	updating := os.Getenv(updateBaselineEnvironment) == "1"
	committed.Repositories = checkFlags(
		t, updating, "flags", committed.Flags, analysisFlags, committed.Repositories,
	)
	committed.FileRepositories = checkFlags(
		t, updating, "file_flags", committed.FileFlags, fileAnalysisFlags,
		committed.FileRepositories,
	)

	return committed
}

// checkFlags refuses to compare numbers against a differently-flagged run, and returns the
// entries that survive the check.
//
// An empty flag list is tolerated only while the dimension holds no entries, which is how a
// baseline that predates the per-file dimension reads. When re-seeding, a flag change does not
// abort — that would make a deliberate flag change impossible to record without deleting the file
// by hand — but the entries recorded under the old flags are dropped rather than preserved: they
// are void by definition, and merging them with freshly measured ones would produce a baseline
// half of which means something else. Preserve-what-you-did-not-measure still holds for the
// ordinary case where the flags are unchanged.
func checkFlags(
	t *testing.T,
	updating bool,
	field string,
	committed, current []string,
	entries map[string]metrics,
) map[string]metrics {
	t.Helper()

	if len(committed) == 0 {
		if len(entries) != 0 {
			t.Fatalf("baseline records %d measurements but no %s to interpret them by",
				len(entries), field)
		}

		return entries
	}

	if equalStrings(committed, current) {
		return entries
	}

	if updating {
		t.Logf(
			"%s changed since the baseline was recorded; discarding %d void entries.\n"+
				"baseline: %s\ncurrent:  %s",
			field, len(entries), strings.Join(committed, " "), strings.Join(current, " "),
		)

		return map[string]metrics{}
	}

	t.Fatalf(
		"baseline %s differ from the flag set the suite runs, so the numbers cannot be "+
			"compared and the baseline has to be re-seeded:\nbaseline: %s\ncurrent:  %s",
		field, strings.Join(committed, " "), strings.Join(current, " "),
	)

	return entries
}

// writeBaseline merges the freshly measured repositories into the committed
// manifest, so refreshing a subset never drops the entries it did not run.
func writeBaseline(t *testing.T, committed baseline, measured, fileMeasured map[string]metrics) {
	t.Helper()

	if len(measured) == 0 && len(fileMeasured) == 0 {
		t.Log("no repositories measured; leaving the baseline untouched")
		return
	}

	committed.Flags = analysisFlags
	committed.FileFlags = fileAnalysisFlags
	committed.Provenance = currentProvenance(t)

	maps.Copy(committed.Repositories, measured)

	maps.Copy(committed.FileRepositories, fileMeasured)

	content, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("encode corpus baseline: %v", err)
	}

	err = os.WriteFile(baselinePath(), append(content, '\n'), 0o600)
	if err != nil {
		t.Fatalf("write corpus baseline: %v", err)
	}

	t.Logf(
		"refreshed %d project and %d per-file baseline entries in %s",
		len(measured), len(fileMeasured), baselinePath(),
	)
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
