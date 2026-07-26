package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePluginRequiresName(t *testing.T) {
	cmd, err := newGeneratePluginCommand()
	require.NoError(t, err)
	cmd.SetArgs(nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err = cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "name" not set`)
}

func TestReadPluginGenerationOptionsRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "plugin", "9Plugin", "My_Plugin", "My-Plugin"} {
		t.Run(name, func(t *testing.T) {
			cmd, err := newGeneratePluginCommand()
			require.NoError(t, err)
			require.NoError(t, cmd.Flags().Set("name", name))

			assert.NotPanics(t, func() {
				_, err = readPluginGenerationOptions(cmd)
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid plugin name")
		})
	}
}

func TestReadPluginGenerationOptionsValidatesOverrides(t *testing.T) {
	tests := []struct {
		flag  string
		value string
		want  string
	}{
		{flag: "varname", value: "type", want: "invalid plugin variable name"},
		{flag: pluginPackageOption, value: "with-dash", want: "invalid plugin package"},
		{flag: "flag", value: "Not-Kebab", want: "invalid plugin flag"},
	}
	for _, test := range tests {
		t.Run(test.flag, func(t *testing.T) {
			cmd, err := newGeneratePluginCommand()
			require.NoError(t, err)
			require.NoError(t, cmd.Flags().Set("name", "MyPlugin"))
			require.NoError(t, cmd.Flags().Set(test.flag, test.value))

			_, err = readPluginGenerationOptions(cmd)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestReadPluginGenerationOptionsInfersStableNames(t *testing.T) {
	cmd, err := newGeneratePluginCommand()
	require.NoError(t, err)
	require.NoError(t, cmd.Flags().Set("name", "MyHTTP2Plugin"))
	require.NoError(t, cmd.Flags().Set("output", "generated"))

	options, err := readPluginGenerationOptions(cmd)

	require.NoError(t, err)
	assert.Equal(t, "my", options.varName)
	assert.Equal(t, "my-http-2-plugin", options.flag)
	assert.Equal(t, filepath.Clean("generated"), options.outputDir)
}

func TestPluginTemplatesReportMissingValuesAndWriteErrors(t *testing.T) {
	outputDir := t.TempDir()
	artifacts := pluginArtifacts{
		outputPath: filepath.Join(outputDir, "plugin.go"),
		values: map[string]string{
			pluginPackageOption: "main",
		},
	}

	err := writePluginSource(artifacts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render plugin source")

	artifacts = newPluginArtifacts(pluginGenerationOptions{
		name:      "MyPlugin",
		outputDir: outputDir,
		varName:   "my",
		flag:      "my-plugin",
		pkg:       "main",
		makefile:  true,
	})
	artifacts.outputPath = outputDir

	err = writePluginSource(artifacts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write plugin source")
}

func TestGeneratePluginIsByteReproducible(t *testing.T) {
	if runtime.GOOS == windowsOSName {
		t.Skip("test protoc shim is a POSIX shell script")
	}
	toolDir := t.TempDir()
	protoc := filepath.Join(toolDir, "protoc")
	// #nosec G306 -- the test shim must be executable.
	require.NoError(t, os.WriteFile(protoc, []byte(`#!/bin/sh
set -eu
out=""
source=""
for arg in "$@"; do
  case "$arg" in
    --gogo_out=paths=source_relative:*) out=${arg#*:} ;;
    --*) ;;
    *) source=$arg ;;
  esac
done
base=${source%.proto}
printf '%s\n' '// deterministic protoc test output' 'package main' > "$out/$base.pb.go"
`), 0o700))
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	outputDir := filepath.Join(t.TempDir(), "example")
	generate := func() map[string]string {
		t.Helper()
		cmd, err := newGeneratePluginCommand()
		require.NoError(t, err)
		cmd.SetArgs([]string{"--name", "ExamplePlugin", "--output", outputDir})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		require.NoError(t, cmd.Execute())

		result := map[string]string{}
		for _, name := range []string{
			"example_plugin.go",
			"example_plugin.proto",
			"example_plugin.pb.go",
			"Makefile",
		} {
			contents, err := os.ReadFile(filepath.Join(outputDir, name))
			require.NoError(t, err)
			result[name] = string(contents)
		}
		return result
	}

	first := generate()
	second := generate()

	assert.Equal(t, first, second)
	assert.Contains(t, first["Makefile"], "protoc-gen-gogo@$(PROTOC_GEN_GOGO_VERSION)")
	assert.NotContains(t, first["Makefile"], "@latest")
	assert.True(t, strings.HasSuffix(first["example_plugin.proto"], "\n"))
}
