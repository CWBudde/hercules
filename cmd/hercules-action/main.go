package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cwbudde/hercules/internal/actionrunner"
)

func main() {
	if len(os.Args) != 2 {
		failf("usage: hercules-action analyze|charts")
	}

	var err error
	switch os.Args[1] {
	case "analyze":
		err = actionrunner.RunAnalysis(context.Background(), actionrunner.AnalysisConfig{
			Executable:       os.Getenv("HERCULES_ACTION_HERCULES"),
			Repository:       os.Getenv("HERCULES_ACTION_REPOSITORY"),
			ArgumentsJSON:    os.Getenv("HERCULES_ACTION_ARGS_JSON"),
			LegacyArguments:  os.Getenv("HERCULES_ACTION_ARGS"),
			OutputPath:       os.Getenv("HERCULES_ACTION_OUTPUT"),
			GitHubOutputPath: os.Getenv("GITHUB_OUTPUT"),
			Stdin:            os.Stdin,
			Stderr:           os.Stderr,
		})
	case "charts":
		err = actionrunner.RunCharts(context.Background(), actionrunner.ChartsConfig{
			Executable: os.Getenv("HERCULES_ACTION_LABOURS"),
			InputPath:  os.Getenv("HERCULES_ACTION_OUTPUT"),
			Mode:       os.Getenv("HERCULES_ACTION_LABOURS_MODE"),
			Stdin:      os.Stdin,
			Stdout:     os.Stdout,
			Stderr:     os.Stderr,
		})
	default:
		failf("unknown command %q", os.Args[1])
	}
	if err != nil {
		failf("%v", err)
	}
}

func failf(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "hercules-action: "+format+"\n", arguments...)
	os.Exit(1)
}
