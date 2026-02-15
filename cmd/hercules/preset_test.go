package main

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

func TestApplyPresetLargeRepo(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("preset", "", "")
	flags.Bool("first-parent", false, "")
	flags.Int("lines-hibernation-threshold", 0, "")
	flags.Bool("lines-hibernation-disk", false, "")
	flags.Int("granularity", 30, "")
	flags.Int("sampling", 30, "")
	flags.Bool("head", false, "")

	err := flags.Set("preset", "large-repo")
	assert.NoError(t, err)

	applyPreset(flags)

	fp, _ := flags.GetBool("first-parent")
	assert.True(t, fp)
	thresh, _ := flags.GetInt("lines-hibernation-threshold")
	assert.Equal(t, 200000, thresh)
	disk, _ := flags.GetBool("lines-hibernation-disk")
	assert.True(t, disk)
	gran, _ := flags.GetInt("granularity")
	assert.Equal(t, 30, gran)
	samp, _ := flags.GetInt("sampling")
	assert.Equal(t, 30, samp)
}

func TestApplyPresetQuick(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("preset", "", "")
	flags.Bool("head", false, "")
	flags.Bool("first-parent", false, "")

	err := flags.Set("preset", "quick")
	assert.NoError(t, err)

	applyPreset(flags)

	head, _ := flags.GetBool("head")
	assert.True(t, head)
}

func TestApplyPresetExplicitFlagWins(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("preset", "", "")
	flags.Bool("first-parent", false, "")
	flags.Int("lines-hibernation-threshold", 0, "")
	flags.Bool("lines-hibernation-disk", false, "")
	flags.Int("granularity", 30, "")
	flags.Int("sampling", 30, "")
	flags.Bool("head", false, "")

	// User explicitly sets threshold to 500000
	err := flags.Set("preset", "large-repo")
	assert.NoError(t, err)
	err = flags.Set("lines-hibernation-threshold", "500000")
	assert.NoError(t, err)

	applyPreset(flags)

	// Explicit flag should win
	thresh, _ := flags.GetInt("lines-hibernation-threshold")
	assert.Equal(t, 500000, thresh)
	// But preset should still apply to non-explicit flags
	fp, _ := flags.GetBool("first-parent")
	assert.True(t, fp)
}

func TestApplyPresetNone(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("preset", "", "")
	flags.Bool("first-parent", false, "")

	// No preset set — should be a no-op
	applyPreset(flags)

	fp, _ := flags.GetBool("first-parent")
	assert.False(t, fp)
}
