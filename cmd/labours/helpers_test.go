package main

import (
	"testing"

	"github.com/spf13/viper"
)

func TestParseFlexibleDateAcceptsCommonPythonCompatibleForms(t *testing.T) {
	for _, date := range []string{
		"2024-01-02",
		"January 2, 2024",
		"2024-01-02T15:04:05Z",
	} {
		t.Run(date, func(t *testing.T) {
			if _, err := parseFlexibleDate(date); err != nil {
				t.Fatalf("parseFlexibleDate() unexpected error: %v", err)
			}
		})
	}
}

func TestPythonCompatibleFlagsAreRegistered(t *testing.T) {
	for _, name := range []string{
		"mode",
		"devs-parallel-fallback",
		"sentiment-fallback",
		"temporal-legend-threshold",
		"temporal-legend-single-col-threshold",
	} {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("expected flag %q to be registered", name)
		}
	}
}

func TestFallbackFlagsAreBoundToViper(t *testing.T) {
	for _, name := range []string{"devs-parallel-fallback", "sentiment-fallback"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil {
			t.Fatalf("expected flag %q to be registered", name)
		}
		previousFlag := flag.Value.String()
		previousViper := viper.GetBool(name)
		defer func(name, previousFlag string, previousViper bool) {
			if err := rootCmd.PersistentFlags().Set(name, previousFlag); err != nil {
				t.Fatalf("failed to restore flag %q: %v", name, err)
			}
			viper.Set(name, previousViper)
		}(name, previousFlag, previousViper)

		if err := rootCmd.PersistentFlags().Set(name, "true"); err != nil {
			t.Fatalf("failed to set flag %q: %v", name, err)
		}
		if !viper.GetBool(name) {
			t.Fatalf("expected viper key %q to follow flag value", name)
		}
	}
}
