// Package pluginsmoke contains the plugin compatibility smoke test
// (PLAN.md Phase 11): it verifies that a minimal plugin built with
// `go build -buildmode=plugin` from the current source tree loads into a
// hercules binary built from the same tree, registers its command line flag
// and produces a result.
//
// Go plugins only work when both the plugin and the loading binary are built
// with cgo, so the test builds a dedicated CGO_ENABLED=1 hercules binary and
// skips entirely when cgo is unavailable (e.g. under `just test`, which pins
// CGO_ENABLED=0). Run it explicitly with `just test-plugin`.
package pluginsmoke

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot walks up from the working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		_, err = os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not locate the repository root (go.mod)")
		dir = parent
	}
}

// goEnv returns the value of `go env <name>`.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	require.NoError(t, err, "go env %s", name)
	return strings.TrimSpace(string(out))
}

// runGo executes a go command in the repository root with cgo enabled.
func runGo(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go %s failed:\n%s", strings.Join(args, " "), string(out))
}

// makeTinyRepo creates an on-disk Git repository with two commits.
func makeTinyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{
		Name:  "Plugin Smoke",
		Email: "plugin-smoke@example.com",
		When:  time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, name), fmt.Appendf(nil, "content %d\n", i), 0o600,
		))
		_, err = worktree.Add(name)
		require.NoError(t, err)
		signature.When = signature.When.Add(time.Hour)
		_, err = worktree.Commit(fmt.Sprintf("commit %d", i), &git.CommitOptions{
			Author: signature, Committer: signature,
		})
		require.NoError(t, err)
	}
	return dir
}

func TestPluginCompatibilitySmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the plugin smoke test in -short mode")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("Go plugins are not supported on %s", runtime.GOOS)
	}
	if goEnv(t, "CGO_ENABLED") != "1" {
		t.Skip("Go plugins require cgo (CGO_ENABLED=1); run `just test-plugin`")
	}
	_, err := exec.LookPath(goEnv(t, "CC"))
	if err != nil {
		t.Skipf("no C compiler available: %v", err)
	}

	root := repoRoot(t)
	tmp := t.TempDir()
	pluginPath := filepath.Join(tmp, "minimal_plugin.so")
	binaryPath := filepath.Join(tmp, "hercules")

	// Build the minimal plugin and a plugin-capable (cgo) hercules binary
	// from the same source tree. `-tags purego` keeps matplotlib-go's render
	// stack on its pure-Go FreeType path — the cgo FreeType binding needs a
	// vendored FreeType which is not shipped (see PLAN.md Phase 1). The tags
	// must match between the plugin and the binary: Go refuses to load a
	// plugin whose shared package builds differ from the host's.
	runGo(t, root, "build", "-tags", "purego", "-buildmode=plugin", "-o", pluginPath,
		"./test/plugin_smoke/testdata/minimal_plugin")
	runGo(t, root, "build", "-tags", "purego", "-o", binaryPath, "./cmd/hercules")

	// The plugin must load and register its command line flag. loadPlugins()
	// only logs loading failures, so grepping --help for the flag is the
	// actual compatibility check.
	helpCmd := exec.Command(binaryPath, "--plugin", pluginPath, "--help")
	helpOut, err := helpCmd.CombinedOutput()
	require.NoError(t, err, "hercules --plugin --help failed:\n%s", string(helpOut))
	assert.NotContains(t, string(helpOut), "Failed to load plugin",
		"the plugin failed to load")
	require.Contains(t, string(helpOut), "--minimal-plugin-test",
		"the plugin's flag was not registered")
	// Cobra re-wraps the description text, so only check a single word of it.
	assert.Contains(t, string(helpOut), "MinimalPluginTest",
		"the plugin's analysis was not registered")

	// The plugin analysis must actually run and serialize its result.
	repoPath := makeTinyRepo(t)
	runCmd := exec.Command(binaryPath,
		"--plugin", pluginPath, "--minimal-plugin-test", "--quiet", repoPath)
	runOut, err := runCmd.CombinedOutput()
	require.NoError(t, err, "hercules --minimal-plugin-test failed:\n%s", string(runOut))
	assert.Contains(t, string(runOut), "MinimalPluginTest:",
		"the plugin analysis did not appear in the output")
	assert.Contains(t, string(runOut), "consumed_commits: 2",
		"the plugin analysis produced a wrong result")
}
