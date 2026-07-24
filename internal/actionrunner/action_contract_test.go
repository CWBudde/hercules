package actionrunner_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompositeActionKeepsExpressionsOutOfShellScripts(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine test source path")
	}
	actionPath := filepath.Join(filepath.Dir(source), "..", "..", "action.yml")
	content, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "run:") {
			continue
		}

		runIndent := indentation(line)
		runSource := strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
		if runSource == "|" || runSource == ">" {
			var builder strings.Builder
			for index++; index < len(lines); index++ {
				candidate := lines[index]
				if strings.TrimSpace(candidate) != "" && indentation(candidate) <= runIndent {
					index--
					break
				}
				builder.WriteString(candidate)
				builder.WriteByte('\n')
			}
			runSource = builder.String()
		}

		if strings.Contains(runSource, "${{") {
			t.Errorf("run script contains a GitHub expression:\n%s", runSource)
		}
		for _, unsafeShell := range []string{"eval ", "bash -c", "sh -c"} {
			if strings.Contains(runSource, unsafeShell) {
				t.Errorf("run script contains unsafe shell execution %q:\n%s", unsafeShell, runSource)
			}
		}
	}
}

func TestCompositeActionExposesSafeArgumentContract(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine test source path")
	}
	actionPath := filepath.Join(filepath.Dir(source), "..", "..", "action.yml")
	content, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}
	action := string(content)

	for _, required := range []string{
		"args-json:",
		"working-directory: ${{ github.action_path }}",
		"HERCULES_ACTION_RUNNER: ${{ github.action_path }}/hercules-action",
		"HERCULES_ACTION_HERCULES: ${{ github.action_path }}/hercules",
		"HERCULES_ACTION_REPOSITORY: ${{ github.workspace }}",
		"HERCULES_ACTION_ARGS_JSON: ${{ inputs.args-json }}",
		"HERCULES_ACTION_ARGS: ${{ inputs.args }}",
		"HERCULES_ACTION_LABOURS: ${{ github.action_path }}/labours",
		"best-effort-charts:",
		`if ! "$HERCULES_ACTION_RUNNER" charts; then`,
	} {
		if !strings.Contains(action, required) {
			t.Errorf("action.yml does not contain %q", required)
		}
	}

	bestEffortStart := strings.Index(action, "  best-effort-charts:")
	if bestEffortStart == -1 {
		t.Fatal("action.yml does not define best-effort-charts")
	}
	bestEffortSection := strings.SplitN(action[bestEffortStart:], "\n\n", 2)[0]
	if !strings.Contains(bestEffortSection, `default: "false"`) {
		t.Errorf("best-effort-charts must default to false:\n%s", bestEffortSection)
	}
}

func indentation(value string) int {
	return len(value) - len(strings.TrimLeft(value, " \t"))
}
