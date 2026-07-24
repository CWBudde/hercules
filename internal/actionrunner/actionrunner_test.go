package actionrunner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/hercules/internal/actionrunner"
)

const helperProcessEnvironment = "HERCULES_ACTION_TEST_HELPER_PROCESS"

func TestMain(testingMain *testing.M) {
	if os.Getenv(helperProcessEnvironment) == "1" {
		runArgumentRecorder()
	}

	os.Exit(testingMain.Run())
}

func TestParseArguments(t *testing.T) {
	t.Parallel()

	t.Run("JSON takes precedence and preserves argument boundaries", func(t *testing.T) {
		t.Parallel()

		arguments, err := actionrunner.ParseArguments(
			`["--burndown","--people=Jane Doe","$(touch should-not-run)"]`,
			"--ignored",
		)
		if err != nil {
			t.Fatalf("ParseArguments() error = %v", err)
		}
		expected := []string{
			"--burndown",
			"--people=Jane Doe",
			"$(touch should-not-run)",
		}
		if !reflect.DeepEqual(arguments, expected) {
			t.Fatalf("ParseArguments() = %#v, want %#v", arguments, expected)
		}
	})

	t.Run("legacy arguments are split without shell evaluation", func(t *testing.T) {
		t.Parallel()

		arguments, err := actionrunner.ParseArguments("", "--burndown; touch /tmp/not-executed #")
		if err != nil {
			t.Fatalf("ParseArguments() error = %v", err)
		}
		expected := []string{
			"--burndown;",
			"touch",
			"/tmp/not-executed",
			"#",
		}
		if !reflect.DeepEqual(arguments, expected) {
			t.Fatalf("ParseArguments() = %#v, want %#v", arguments, expected)
		}
	})

	t.Run("empty JSON array disables the legacy default", func(t *testing.T) {
		t.Parallel()

		arguments, err := actionrunner.ParseArguments("[]", "--burndown --devs")
		if err != nil {
			t.Fatalf("ParseArguments() error = %v", err)
		}
		if len(arguments) != 0 {
			t.Fatalf("ParseArguments() = %#v, want an empty argv", arguments)
		}
	})

	for _, test := range []struct {
		name      string
		value     string
		wantError string
	}{
		{
			name:      "invalid JSON",
			value:     `["--burndown"`,
			wantError: "parse args-json",
		},
		{
			name:      "JSON null",
			value:     "null",
			wantError: "not null",
		},
		{
			name:      "NUL byte",
			value:     `["before\u0000after"]`,
			wantError: "NUL byte",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := actionrunner.ParseArguments(test.value, "")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ParseArguments() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestRunAnalysisDoesNotEvaluateArguments(t *testing.T) {
	for _, test := range []struct {
		name           string
		argumentsJSON  string
		legacyArgument func(string) string
		expected       func(string) []string
	}{
		{
			name: "JSON arguments",
			argumentsJSON: `["--burndown","--devs","--flag=$(touch SENTINEL)",` +
				`"; touch SENTINEL #","redirect > target","quote ' \"","line\nbreak","Unicode: 🛡️"]`,
			expected: func(sentinel string) []string {
				return []string{
					"--burndown",
					"--devs",
					"--flag=$(touch " + sentinel + ")",
					"; touch " + sentinel + " #",
					"redirect > target",
					"quote ' \"",
					"line\nbreak",
					"Unicode: 🛡️",
					actionrunner.DefaultRepository,
				}
			},
		},
		{
			name: "legacy arguments",
			legacyArgument: func(sentinel string) string {
				return "--burndown; touch " + sentinel + " #"
			},
			expected: func(sentinel string) []string {
				return []string{
					"--burndown;",
					"touch",
					sentinel,
					"#",
					actionrunner.DefaultRepository,
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			recorder := testExecutable(t)
			sentinel := filepath.Join(directory, "command-was-executed")
			outputSentinel := "sec01-output-command-was-executed-" + filepath.Base(directory)
			t.Cleanup(func() {
				_ = os.Remove(outputSentinel)
			})
			output := filepath.Join(
				directory,
				"analysis ü space; redirect> 'quote' \"double\" $(touch "+outputSentinel+").yml",
			)
			githubOutput := filepath.Join(directory, "github-output")
			t.Setenv(helperProcessEnvironment, "1")

			argumentsJSON := strings.ReplaceAll(test.argumentsJSON, "SENTINEL", sentinel)
			legacyArguments := ""
			if test.legacyArgument != nil {
				legacyArguments = test.legacyArgument(sentinel)
			}
			err := actionrunner.RunAnalysis(context.Background(), actionrunner.AnalysisConfig{
				Executable:       recorder,
				ArgumentsJSON:    argumentsJSON,
				LegacyArguments:  legacyArguments,
				OutputPath:       output,
				GitHubOutputPath: githubOutput,
			})
			if err != nil {
				t.Fatalf("RunAnalysis() error = %v", err)
			}

			_, statErr := os.Stat(sentinel)
			if !os.IsNotExist(statErr) {
				t.Fatalf("hostile argument executed a command; os.Stat() error = %v", statErr)
			}

			_, statErr = os.Stat(outputSentinel)
			if !os.IsNotExist(statErr) {
				t.Fatalf("hostile output path executed a command; os.Stat() error = %v", statErr)
			}
			if actual := readRecordedArguments(t, output); !reflect.DeepEqual(actual, test.expected(sentinel)) {
				t.Fatalf("received argv = %#v, want %#v", actual, test.expected(sentinel))
			}
			if actual := readFile(t, githubOutput); actual != "file="+output+"\n" {
				t.Fatalf("GITHUB_OUTPUT = %q, want %q", actual, "file="+output+"\n")
			}
		})
	}
}

func TestRunChartsDoesNotEvaluateInputs(t *testing.T) {
	directory := t.TempDir()
	recorder := testExecutable(t)
	sentinel := filepath.Join(directory, "command-was-executed")
	mode := "all; $(touch " + sentinel + ") > 'chart'\nUnicode: 🛡️"
	input := "analysis ü.yml; $(touch " + sentinel + ") > \"chart\"\nnext"
	t.Setenv(helperProcessEnvironment, "1")

	var output bytes.Buffer
	err := actionrunner.RunCharts(context.Background(), actionrunner.ChartsConfig{
		Executable: recorder,
		InputPath:  input,
		Mode:       mode,
		Stdout:     &output,
	})
	if err != nil {
		t.Fatalf("RunCharts() error = %v", err)
	}

	_, statErr := os.Stat(sentinel)
	if !os.IsNotExist(statErr) {
		t.Fatalf("hostile chart input executed a command; os.Stat() error = %v", statErr)
	}
	expected := []string{"-m", mode, "-i", input}
	if actual := decodeRecordedArguments(t, output.Bytes()); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("received argv = %#v, want %#v", actual, expected)
	}
}

func TestRunChartsPropagatesFailure(t *testing.T) {
	t.Parallel()

	err := actionrunner.RunCharts(context.Background(), actionrunner.ChartsConfig{
		Executable: filepath.Join(t.TempDir(), "does-not-exist"),
		InputPath:  "analysis.yml",
		Mode:       "all",
	})
	if err == nil || !strings.Contains(err.Error(), "run Labours") {
		t.Fatalf("RunCharts() error = %v, want command failure", err)
	}
}

func TestRunAnalysisRejectsOutputProtocolInjection(t *testing.T) {
	t.Parallel()

	err := actionrunner.RunAnalysis(context.Background(), actionrunner.AnalysisConfig{
		OutputPath: "analysis.yml\ninjected=value",
	})
	if err == nil || !strings.Contains(err.Error(), "newline") {
		t.Fatalf("RunAnalysis() error = %v, want newline validation error", err)
	}
}

func testExecutable(t *testing.T) string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	return executable
}

func runArgumentRecorder() {
	arguments, err := json.Marshal(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode recorded arguments: %v\n", err)
		os.Exit(2)
	}

	_, err = os.Stdout.Write(arguments)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write recorded arguments: %v\n", err)
		os.Exit(2)
	}

	os.Exit(0)
}

func readRecordedArguments(t *testing.T, path string) []string {
	t.Helper()

	return decodeRecordedArguments(t, []byte(readFile(t, path)))
}

func decodeRecordedArguments(t *testing.T, value []byte) []string {
	t.Helper()

	var arguments []string
	err := json.Unmarshal(value, &arguments)
	if err != nil {
		t.Fatalf("decode recorded arguments: %v", err)
	}

	return arguments
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(value)
}
