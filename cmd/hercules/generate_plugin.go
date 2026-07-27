package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"github.com/fatih/camelcase"
	"github.com/spf13/cobra"
)

const (
	pluginPackageOption = "package"
	windowsOSName       = "windows"
)

var (
	pluginNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	pluginFlagPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
)

// ShlibExts is the mapping between platform names and shared library file name extensions.
var ShlibExts = map[string]string{
	windowsOSName: "dll",
	"linux":       "so",
	"darwin":      "dylib",
	"freebsd":     "dylib",
}

// generatePluginCmd represents the generatePlugin command.
var generatePluginCmd = mustNewGeneratePluginCommand()

func mustNewGeneratePluginCommand() *cobra.Command {
	cmd, err := newGeneratePluginCommand()
	if err != nil {
		panic(err)
	}
	return cmd
}

func newGeneratePluginCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "generate-plugin",
		Short: "Write the plugin source skeleton.",
		RunE:  runGeneratePlugin,
	}
	flags := cmd.Flags()
	flags.StringP("name", "n", "", "Name of the plugin, CamelCase. Required.")
	flags.StringP("output", "o", ".", "Output directory for the generated plugin files.")
	flags.String("varname", "", "Name of the plugin instance variable. Inferred from --name by default.")
	flags.String("flag", "", "Name of the plugin activation flag. Inferred from --name by default.")
	flags.Bool("no-makefile", false, "Do not generate the Makefile.")
	flags.String(pluginPackageOption, "main", "Name of the package.")
	if err := cmd.MarkFlagRequired("name"); err != nil {
		return nil, fmt.Errorf("mark plugin name flag as required: %w", err)
	}
	return cmd, nil
}

func runGeneratePlugin(cmd *cobra.Command, _ []string) error {
	options, err := readPluginGenerationOptions(cmd)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(options.outputDir, 0o755); err != nil {
		return fmt.Errorf("create plugin output directory: %w", err)
	}

	artifacts := newPluginArtifacts(options)
	if err := writePluginSource(artifacts); err != nil {
		return err
	}
	if err := writePluginProto(artifacts); err != nil {
		return err
	}
	if err := generatePluginProtoGo(cmd.Context(), artifacts); err != nil {
		return err
	}
	if options.makefile {
		return writePluginMakefile(artifacts)
	}
	return nil
}

type pluginGenerationOptions struct {
	name      string
	outputDir string
	varName   string
	flag      string
	pkg       string
	makefile  bool
}

func readPluginGenerationOptions(cmd *cobra.Command) (pluginGenerationOptions, error) {
	flags := cmd.Flags()
	name, err := flags.GetString("name")
	if err != nil {
		return pluginGenerationOptions{}, err
	}
	outputDir, err := flags.GetString("output")
	if err != nil {
		return pluginGenerationOptions{}, err
	}
	varName, err := flags.GetString("varname")
	if err != nil {
		return pluginGenerationOptions{}, err
	}
	flag, err := flags.GetString("flag")
	if err != nil {
		return pluginGenerationOptions{}, err
	}
	disableMakefile, err := flags.GetBool("no-makefile")
	if err != nil {
		return pluginGenerationOptions{}, err
	}
	pkg, err := flags.GetString(pluginPackageOption)
	if err != nil {
		return pluginGenerationOptions{}, err
	}

	if err := validatePluginName(name); err != nil {
		return pluginGenerationOptions{}, err
	}
	nameParts := camelcase.Split(name)
	if varName == "" {
		varName = strings.ToLower(nameParts[0])
	}
	if flag == "" {
		flag = strings.ToLower(strings.Join(nameParts, "-"))
	}
	if !isGoIdentifier(varName) {
		return pluginGenerationOptions{}, fmt.Errorf(
			"invalid plugin variable name %q: expected a Go identifier", varName,
		)
	}
	if !isGoIdentifier(pkg) {
		return pluginGenerationOptions{}, fmt.Errorf(
			"invalid plugin package %q: expected a Go identifier", pkg,
		)
	}
	if err := validatePluginFlag(flag); err != nil {
		return pluginGenerationOptions{}, err
	}

	return pluginGenerationOptions{
		name:      name,
		outputDir: filepath.Clean(outputDir),
		varName:   varName,
		flag:      flag,
		pkg:       pkg,
		makefile:  !disableMakefile,
	}, nil
}

func validatePluginName(name string) error {
	if pluginNamePattern.MatchString(name) {
		return nil
	}
	return fmt.Errorf("invalid plugin name %q: expected non-empty CamelCase", name)
}

func isGoIdentifier(value string) bool {
	return value != "_" && token.IsIdentifier(value)
}

func validatePluginFlag(flag string) error {
	if pluginFlagPattern.MatchString(flag) {
		return nil
	}
	return fmt.Errorf("invalid plugin flag %q: expected a lowercase kebab-case name", flag)
}

type pluginArtifacts struct {
	outputPath string
	protoPath  string
	protoGo    string
	values     map[string]string
}

func newPluginArtifacts(options pluginGenerationOptions) pluginArtifacts {
	nameParts := camelcase.Split(options.name)
	outputPath := filepath.Join(
		options.outputDir, strings.ToLower(strings.Join(nameParts, "_"))+".go",
	)
	basePath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath))
	sharedLibrary := filepath.Base(basePath) + "." + ShlibExts[runtime.GOOS]
	protoPath := basePath + ".proto"
	protoGo := basePath + ".pb.go"
	return pluginArtifacts{
		outputPath: outputPath,
		protoPath:  protoPath,
		protoGo:    protoGo,
		values: map[string]string{
			"name":        options.name,
			"varname":     options.varName,
			"flag":        options.flag,
			"package":     options.pkg,
			"output":      outputPath,
			"shlib":       sharedLibrary,
			"proto":       protoPath,
			"protogo":     protoGo,
			"outdir":      options.outputDir,
			"outputbase":  filepath.Base(outputPath),
			"protobase":   filepath.Base(protoPath),
			"protogobase": filepath.Base(protoGo),
		},
	}
}

func writePluginSource(artifacts pluginArtifacts) error {
	generator, err := template.New("plugin").Option("missingkey=error").Parse(PluginTemplateSource)
	if err != nil {
		return fmt.Errorf("parse plugin source template: %w", err)
	}
	var output bytes.Buffer
	if err := generator.Execute(&output, artifacts.values); err != nil {
		return fmt.Errorf("render plugin source: %w", err)
	}
	source, err := format.Source(output.Bytes())
	if err != nil {
		return fmt.Errorf("format plugin source: %w", err)
	}
	if err := writeGeneratedFile(artifacts.outputPath, source); err != nil {
		return fmt.Errorf("write plugin source: %w", err)
	}
	return nil
}

func writePluginProto(artifacts pluginArtifacts) error {
	source := fmt.Sprintf(`syntax = "proto3";
option go_package = "%s";

message %sResultMessage {
  // Add fields here.
  // Reference: https://protobuf.dev/programming-guides/proto3/
  // Hercules schema: https://github.com/cwbudde/hercules/blob/main/internal/pb/pb.proto
}
`, artifacts.values["package"], artifacts.values["name"])
	if err := writeGeneratedFile(artifacts.protoPath, []byte(source)); err != nil {
		return fmt.Errorf("write plugin protobuf definition: %w", err)
	}
	return nil
}

func writeGeneratedFile(path string, contents []byte) error {
	// #nosec G304 -- the path is selected by the user for generated output.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(contents)
	if writeErr == nil && written != len(contents) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func generatePluginProtoGo(ctx context.Context, artifacts pluginArtifacts) error {
	protoc, err := exec.LookPath("protoc")
	if err != nil {
		return fmt.Errorf("find protoc: %w", err)
	}
	outputDir, err := filepath.Abs(artifacts.values["outdir"])
	if err != nil {
		return fmt.Errorf("resolve plugin output directory: %w", err)
	}
	env := os.Environ()
	extraPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return fmt.Errorf("resolve executable directory: %w", err)
	}
	gobin := os.Getenv("GOBIN")
	if gobin != "" {
		extraPath = gobin + string(os.PathListSeparator) + extraPath
	}
	env = append(env, fmt.Sprintf(
		"PATH=%s%c%s", extraPath, os.PathListSeparator, os.Getenv("PATH"),
	))
	protocCmd := exec.CommandContext(
		ctx,
		protoc,
		"--gogo_out=paths=source_relative:"+outputDir,
		"--proto_path="+outputDir,
		filepath.Base(artifacts.protoPath),
	)
	protocCmd.Env = env
	protocCmd.Dir = outputDir
	output, err := protocCmd.CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			return fmt.Errorf("generate plugin protobuf Go source: %w", err)
		}
		return fmt.Errorf("generate plugin protobuf Go source: %w: %s", err, details)
	}
	return nil
}

const pluginMakefileTemplate = `GO111MODULE := on
PROTOC_GEN_GOGO_VERSION := v1.3.2
GOBIN ?= $(shell go env GOPATH)/bin
TAGS ?= purego

.PHONY: all protoc-gen-gogo clean

all: {{.shlib}}

{{.shlib}}: {{.output}} {{.protogo}}
	CGO_ENABLED=1 go build -tags "$(TAGS)" -buildmode=plugin -o $@ $(GOFLAGS) {{.output}} {{.protogo}}

protoc-gen-gogo:
	GOBIN="$(GOBIN)" go install github.com/gogo/protobuf/protoc-gen-gogo@$(PROTOC_GEN_GOGO_VERSION)

{{.protogo}}: {{.proto}} | protoc-gen-gogo
	PATH="$(GOBIN):$$PATH" protoc --gogo_out=paths=source_relative:. --proto_path=. {{.proto}}

clean:
	rm -f {{.shlib}} {{.protogo}}
`

func writePluginMakefile(artifacts pluginArtifacts) error {
	generator, err := template.New("plugin-makefile").
		Option("missingkey=error").
		Parse(pluginMakefileTemplate)
	if err != nil {
		return fmt.Errorf("parse plugin Makefile template: %w", err)
	}
	values := make(map[string]string, len(artifacts.values))
	maps.Copy(values, artifacts.values)
	for _, name := range []string{"output", "protogo", "proto"} {
		values[name] = filepath.Base(values[name])
	}

	var output bytes.Buffer
	if err := generator.Execute(&output, values); err != nil {
		return fmt.Errorf("render plugin Makefile: %w", err)
	}
	makefile := filepath.Join(artifacts.values["outdir"], "Makefile")
	if err := writeGeneratedFile(makefile, output.Bytes()); err != nil {
		return fmt.Errorf("write plugin Makefile: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(generatePluginCmd)
}
