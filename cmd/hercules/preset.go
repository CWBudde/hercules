package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

const presetEnabled = "true"

// presetDefaults maps preset names to their flag defaults.
var presetDefaults = map[string]map[string]string{
	"large-repo": {
		"first-parent":                presetEnabled,
		"lines-hibernation-threshold": "200000",
		"lines-hibernation-disk":      presetEnabled,
		"granularity":                 "30",
		"sampling":                    "30",
	},
	"quick": {
		"head": presetEnabled,
	},
}

// applyPreset reads the --preset flag and applies its defaults to any flag
// that the user did not explicitly set on the command line.
func applyPreset(flags *pflag.FlagSet) {
	presetName, err := flags.GetString("preset")
	if err != nil || presetName == "" {
		return
	}

	defaults, ok := presetDefaults[presetName]
	if !ok {
		fmt.Fprintf(os.Stderr, "warning: unknown preset %q (available: large-repo, quick)\n", presetName)
		return
	}

	for flagName, value := range defaults {
		flag := flags.Lookup(flagName)
		if flag == nil {
			continue
		}
		if flag.Changed {
			continue
		}
		if err := flags.Set(flagName, value); err != nil {
			fmt.Fprintf(os.Stderr, "warning: preset %q: failed to set --%s=%s: %v\n",
				presetName, flagName, value, err)
		}
	}
}
