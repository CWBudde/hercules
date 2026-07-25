package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/fatih/camelcase"
	"github.com/spf13/cobra"
)

// ShlibExts is the mapping between platform names and shared library file name extensions.
var ShlibExts = map[string]string{
	"windows": "dll",
	"linux":   "so",
	"darwin":  "dylib",
	"freebsd": "dylib",
}

// generatePluginCmd represents the generatePlugin command.
var generatePluginCmd = &cobra.Command{
	Use:   "generate-plugin",
	Short: "Write the plugin source skeleton.",
	Long:  ``,
	RunE:  runGeneratePlugin,
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
	if err := generatePluginProtoGo(artifacts); err != nil {
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
	pkg, err := flags.GetString("package")
	if err != nil {
		return pluginGenerationOptions{}, err
	}

	nameParts := camelcase.Split(name)
	if varName == "" {
		varName = strings.ToLower(nameParts[0])
	}
	if flag == "" {
		flag = strings.ToLower(strings.Join(nameParts, "-"))
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
			"name":    options.name,
			"varname": options.varName,
			"flag":    options.flag,
			"package": options.pkg,
			"output":  outputPath,
			"shlib":   sharedLibrary,
			"proto":   protoPath,
			"protogo": protoGo,
			"outdir":  options.outputDir,
		},
	}
}

func writePluginSource(artifacts pluginArtifacts) error {
	generator, err := template.New("plugin").Parse(PluginTemplateSource)
	if err != nil {
		return fmt.Errorf("parse plugin source template: %w", err)
	}
	var output bytes.Buffer
	if err := generator.Execute(&output, artifacts.values); err != nil {
		return fmt.Errorf("render plugin source: %w", err)
	}
	// #nosec G306 -- generated Go source should use normal source-file permissions.
	if err := os.WriteFile(artifacts.outputPath, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write plugin source: %w", err)
	}
	return nil
}

func writePluginProto(artifacts pluginArtifacts) error {
	source := fmt.Sprintf(`syntax = "proto3";
	option go_package = "%s";
	
	message %sResultMessage {
	  // add fields here
	  // reference: https://developers.google.com/protocol-buffers/docs/proto3
	  // example: pb/pb.proto https://github.com/src-d/hercules/blob/master/pb/pb.proto
	}
	`, artifacts.values["package"], artifacts.values["name"])
	// #nosec G306 -- generated protobuf source should use normal source-file permissions.
	if err := os.WriteFile(artifacts.protoPath, []byte(source), 0o644); err != nil {
		return fmt.Errorf("write plugin protobuf definition: %w", err)
	}
	return nil
}

func generatePluginProtoGo(artifacts pluginArtifacts) error {
	protoc, err := exec.LookPath("protoc")
	if err != nil {
		return fmt.Errorf("find protoc: %w", err)
	}
	outputDir := artifacts.values["outdir"]
	cmdargs := [...]string{
		protoc,
		"--gogo_out=" + outputDir,
		"--proto_path=" + outputDir,
		artifacts.protoPath,
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
		"PATH=%s%c%s", os.Getenv("PATH"), os.PathListSeparator, extraPath,
	))
	protocCmd := exec.Cmd{
		Path: protoc, Args: cmdargs[:], Env: env, Stdout: os.Stdout, Stderr: os.Stderr,
	}
	if err := protocCmd.Run(); err != nil {
		return fmt.Errorf("generate plugin protobuf Go source: %w", err)
	}
	return nil
}

const pluginMakefileTemplate = `GO111MODULE = on

all: {{.shlib}}

{{.shlib}}: {{.output}} {{.protogo}}
	go build -buildmode=plugin ${GOFLAGS} {{.output}} {{.protogo}}

{{.protogo}}: {{.proto}}
	PATH=$$PATH:$$GOBIN protoc --gogo_out=. --proto_path=. {{.proto}}
`

func writePluginMakefile(artifacts pluginArtifacts) error {
	generator, err := template.New("plugin-makefile").Parse(pluginMakefileTemplate)
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
	// #nosec G306 -- generated Makefiles should use normal source-file permissions.
	if err := os.WriteFile(makefile, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write plugin Makefile: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(generatePluginCmd)
	generatePluginCmd.SetUsageFunc(generatePluginCmd.UsageFunc())
	gpFlags := generatePluginCmd.Flags()
	gpFlags.StringP("name", "n", "", "Name of the plugin, CamelCase. Required.")
	if err := generatePluginCmd.MarkFlagRequired("name"); err != nil {
		panic(err)
	}
	gpFlags.StringP("output", "o", ".", "Output directory for the generated plugin files.")
	gpFlags.String("varname", "", "Name of the plugin instance variable, If not "+
		"specified, inferred from -n.")
	gpFlags.String("flag", "", "Name of the plugin activation cmdline flag, If not "+
		"specified, inferred from -varname.")
	gpFlags.Bool("no-makefile", false, "Do not generate the Makefile.")
	gpFlags.String("package", "main", "Name of the package.")
}
