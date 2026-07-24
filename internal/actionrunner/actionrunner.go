package actionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	DefaultHerculesExecutable = "./hercules"
	DefaultLaboursExecutable  = "./labours"
	DefaultRepository         = "."
)

var (
	errArgumentsJSONNull = errors.New("args-json must be a JSON string array, not null")
	errArgumentNUL       = errors.New("contains a NUL byte")
	errValueEmpty        = errors.New("must not be empty")
	errValueNUL          = errors.New("contains a NUL byte")
	errValueNewline      = errors.New("must not contain a newline")
)

type AnalysisConfig struct {
	Executable       string
	Repository       string
	ArgumentsJSON    string
	LegacyArguments  string
	OutputPath       string
	GitHubOutputPath string
	Stdin            io.Reader
	Stderr           io.Writer
}

type ChartsConfig struct {
	Executable string
	InputPath  string
	Mode       string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// ParseArguments returns an argv without invoking a shell. A non-empty JSON
// value takes precedence over the deprecated whitespace-split representation.
func ParseArguments(argumentsJSON, legacyArguments string) ([]string, error) {
	if strings.TrimSpace(argumentsJSON) != "" {
		var arguments []string

		err := json.Unmarshal([]byte(argumentsJSON), &arguments)
		if err != nil {
			return nil, fmt.Errorf("parse args-json as a JSON string array: %w", err)
		}

		if arguments == nil {
			return nil, errArgumentsJSONNull
		}

		err = validateArguments(arguments)
		if err != nil {
			return nil, err
		}

		return arguments, nil
	}

	arguments := strings.Fields(legacyArguments)

	err := validateArguments(arguments)
	if err != nil {
		return nil, err
	}

	return arguments, nil
}

func validateArguments(arguments []string) error {
	for index, argument := range arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("argument %d: %w", index, errArgumentNUL)
		}
	}

	return nil
}

func RunAnalysis(ctx context.Context, config AnalysisConfig) error {
	executable := config.Executable
	if executable == "" {
		executable = DefaultHerculesExecutable
	}

	repository := config.Repository
	if repository == "" {
		repository = DefaultRepository
	}

	err := validateProtocolValue("output", config.OutputPath)
	if err != nil {
		return err
	}

	arguments, err := ParseArguments(config.ArgumentsJSON, config.LegacyArguments)
	if err != nil {
		return err
	}

	arguments = append(arguments, repository)

	output, err := os.OpenFile(config.OutputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create analysis output %q: %w", config.OutputPath, err)
	}

	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdin = config.Stdin
	command.Stdout = output
	command.Stderr = config.Stderr
	runErr := command.Run()
	closeErr := output.Close()

	if runErr != nil {
		return fmt.Errorf("run Hercules: %w", runErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close analysis output %q: %w", config.OutputPath, closeErr)
	}

	if config.GitHubOutputPath != "" {
		err = appendGitHubOutput(config.GitHubOutputPath, "file", config.OutputPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func RunCharts(ctx context.Context, config ChartsConfig) error {
	executable := config.Executable
	if executable == "" {
		executable = DefaultLaboursExecutable
	}

	err := validateArgumentValue("input", config.InputPath)
	if err != nil {
		return err
	}

	err = validateArgumentValue("labours mode", config.Mode)
	if err != nil {
		return err
	}

	command := exec.CommandContext(ctx, executable, "-m", config.Mode, "-i", config.InputPath)
	command.Stdin = config.Stdin
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr

	err = command.Run()
	if err != nil {
		return fmt.Errorf("run Labours: %w", err)
	}

	return nil
}

func validateArgumentValue(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s: %w", name, errValueEmpty)
	}

	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%s: %w", name, errValueNUL)
	}

	return nil
}

func validateProtocolValue(name, value string) error {
	err := validateArgumentValue(name, value)
	if err != nil {
		return err
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s: %w", name, errValueNewline)
	}

	return nil
}

func appendGitHubOutput(path, key, value string) error {
	err := validateProtocolValue("GitHub output key", key)
	if err != nil {
		return err
	}

	err = validateProtocolValue("GitHub output value", value)
	if err != nil {
		return err
	}

	output, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}

	_, err = fmt.Fprintf(output, "%s=%s\n", key, value)
	if err != nil {
		_ = output.Close()

		return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
	}

	err = output.Close()
	if err != nil {
		return fmt.Errorf("close GITHUB_OUTPUT: %w", err)
	}

	return nil
}
