package main

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/leaves"
)

// swapDeploymentTables replaces the package-level deployment tables that
// pipelineDeploymentList reads for the duration of one test.
func swapDeploymentTables(t *testing.T, deployed map[string]*bool, activations map[string][]string) {
	t.Helper()

	previousDeployed, previousActivations := cmdlineDeployed, activationByFlags
	cmdlineDeployed, activationByFlags = deployed, activations
	t.Cleanup(func() {
		cmdlineDeployed, activationByFlags = previousDeployed, previousActivations
	})
}

func TestPipelineDeploymentListIsSortedAndStable(t *testing.T) {
	enabled, disabled := true, false
	swapDeploymentTables(t, map[string]*bool{
		"Zeta":    &enabled,
		"Alpha":   &enabled,
		"Mu":      &enabled,
		"Beta":    &enabled,
		"Omega":   &enabled,
		"Gamma":   &enabled,
		"Skipped": &disabled,
	}, map[string][]string{})

	expected := [][]string{{"Alpha"}, {"Beta"}, {"Gamma"}, {"Mu"}, {"Omega"}, {"Zeta"}}
	for run := range 20 {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		require.Equalf(t, expected, pipelineDeploymentList(flags), "run %d", run)
	}
}

func TestPresetLargeRepoActivatesOnlyRequestedAnalyses(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("preset", "", "")
	flags.Bool("first-parent", false, "")
	flags.Bool("head", false, "")
	configuration, deployed, activations := hercules.Registry.AddFlagsWithConfiguration(flags)
	swapDeploymentTables(t, deployed, activations)

	require.NoError(t, flags.Parse([]string{"--preset", "large-repo", "--couples"}))
	applyPreset(flags)

	assert.Equal(t, [][]string{{"Couples"}}, pipelineDeploymentList(flags))

	// The preset values must still reach the pipeline facts and the plain flag readers.
	facts := configuration.Snapshot()
	assert.Equal(t, 30, facts[leaves.ConfigBurndownGranularity])
	assert.Equal(t, 200000, facts[linehistory.ConfigLinesHibernationThreshold])
	granularity, err := flags.GetInt("granularity")
	require.NoError(t, err)
	assert.Equal(t, 30, granularity)
	firstParent, err := flags.GetBool("first-parent")
	require.NoError(t, err)
	assert.True(t, firstParent)
}
