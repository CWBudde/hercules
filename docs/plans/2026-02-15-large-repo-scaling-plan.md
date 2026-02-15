# Large-Repo Scaling Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent OOM kills on large repositories (e.g., Linux kernel) by enabling hibernation and adding scaling presets.

**Architecture:** Three-part approach: (A) `--preset` flag system in CLI, (B) sensible hibernation defaults for LineHistory, (C) hibernation support for new BurndownAnalysis. Presets bundle flags with explicit-flag-wins precedence.

**Tech Stack:** Go, cobra/pflag CLI, encoding/gob + compress/flate for serialization, testify for tests.

---

### Task 1: Add BurndownAnalysis Hibernation — Test

**Files:**
- Modify: `leaves/burndown_test.go` (append new tests)
- Modify: `leaves/burndown_shared.go` (add config constants)

**Step 1: Add hibernation config constants to burndown_shared.go**

In `leaves/burndown_shared.go`, add after the existing constants (after line 27):

```go
// ConfigBurndownHibernationDisk enables writing hibernated burndown data to disk.
ConfigBurndownHibernationDisk = "Burndown.HibernationDisk"
// ConfigBurndownHibernationDir is the temp directory for hibernated burndown data.
ConfigBurndownHibernationDir = "Burndown.HibernationDir"
```

**Step 2: Write the failing test for Hibernate/Boot round-trip**

Append to `leaves/burndown_test.go`:

```go
func TestBurndownHibernateBoot(t *testing.T) {
	bd := BurndownAnalysis{}
	assert.Nil(t, bd.Initialize(test.Repository))

	// Populate with some data
	bd.globalHistory.updateDelta(0, 0, 50)
	bd.globalHistory.updateDelta(0, 10, -10)
	bd.globalHistory.updateDelta(10, 10, 20)

	bd.fileHistories[1] = sparseHistory{}
	bd.fileHistories[1].updateDelta(0, 0, 30)

	// Hibernate
	assert.Nil(t, bd.Hibernate())
	// Maps should be nil after hibernation
	assert.Nil(t, bd.globalHistory)
	assert.Nil(t, bd.fileHistories)
	assert.Nil(t, bd.peopleHistories)
	assert.Nil(t, bd.matrix)

	// Boot
	assert.Nil(t, bd.Boot())
	// Data should be restored
	assert.Equal(t, int64(50), bd.globalHistory[0].deltas[0])
	assert.Equal(t, int64(-10), bd.globalHistory[10].deltas[0])
	assert.Equal(t, int64(20), bd.globalHistory[10].deltas[10])
	assert.Equal(t, int64(30), bd.fileHistories[1][0].deltas[0])
}

func TestBurndownHibernateBootDisk(t *testing.T) {
	bd := BurndownAnalysis{}
	assert.Nil(t, bd.Initialize(test.Repository))
	bd.HibernationToDisk = true

	bd.globalHistory.updateDelta(0, 5, 100)

	assert.Nil(t, bd.Hibernate())
	assert.NotEmpty(t, bd.hibernatedFileName)
	assert.Nil(t, bd.globalHistory)

	assert.Nil(t, bd.Boot())
	assert.Empty(t, bd.hibernatedFileName)
	assert.Equal(t, int64(100), bd.globalHistory[5].deltas[0])
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./leaves -run TestBurndownHibernate -v`
Expected: FAIL — `Hibernate` method not found.

**Step 4: Commit failing test**

```bash
git add leaves/burndown_test.go leaves/burndown_shared.go
git commit -m "test: add failing tests for BurndownAnalysis hibernation"
```

---

### Task 2: Implement BurndownAnalysis Hibernation

**Files:**
- Modify: `leaves/burndown.go` (add fields, methods, config options)

**Step 1: Add hibernation fields to BurndownAnalysis struct**

In `leaves/burndown.go`, add fields to the `BurndownAnalysis` struct (after line 68, before `l core.Logger`):

```go
// HibernationToDisk saves hibernated data to disk rather than keeping in memory.
HibernationToDisk bool
// HibernationDirectory is the temp directory for hibernated data files.
HibernationDirectory string

hibernatedData     []byte
hibernatedFileName string
```

**Step 2: Add hibernation config options to ListConfigurationOptions**

Replace the `ListConfigurationOptions` method (line 92-94) to include the new options:

```go
func (analyser *BurndownAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	opts := make([]core.ConfigurationOption, len(BurndownSharedOptions))
	copy(opts, BurndownSharedOptions[:])
	opts = append(opts, core.ConfigurationOption{
		Name:        ConfigBurndownHibernationDisk,
		Description: "Save hibernated burndown data to disk rather than keep in memory.",
		Flag:        "burndown-hibernation-disk",
		Type:        core.BoolConfigurationOption,
		Default:     false,
	}, core.ConfigurationOption{
		Name:        ConfigBurndownHibernationDir,
		Description: "Temporary directory for hibernated burndown data files.",
		Flag:        "burndown-hibernation-dir",
		Type:        core.PathConfigurationOption,
		Default:     "",
	})
	return opts
}
```

**Step 3: Add hibernation config reading to Configure**

In the `Configure` method (after line 128), add:

```go
if val, exists := facts[ConfigBurndownHibernationDisk].(bool); exists {
	analyser.HibernationToDisk = val
}
if val, exists := facts[ConfigBurndownHibernationDir].(string); exists {
	analyser.HibernationDirectory = val
}
```

**Step 4: Add imports needed**

Add to imports in `leaves/burndown.go`:

```go
"bytes"
"compress/flate"
"encoding/gob"
"os"
```

**Step 5: Implement Hibernate and Boot methods**

Add before the `Finalize()` method (before line 287):

```go
// burndownState holds the serializable state for hibernation.
type burndownState struct {
	GlobalHistory   map[int]map[int]int64
	FileHistories   map[core.FileId]map[int]map[int]int64
	PeopleHistories []map[int]map[int]int64
	Matrix          []map[core.AuthorId]int64
}

func sparseHistoryToMap(sh sparseHistory) map[int]map[int]int64 {
	if sh == nil {
		return nil
	}
	m := make(map[int]map[int]int64, len(sh))
	for k, v := range sh {
		m[k] = v.deltas
	}
	return m
}

func mapToSparseHistory(m map[int]map[int]int64) sparseHistory {
	if m == nil {
		return nil
	}
	sh := make(sparseHistory, len(m))
	for k, v := range m {
		sh[k] = sparseHistoryEntry{deltas: v}
	}
	return sh
}

// Hibernate compresses the burndown analysis state to save memory.
func (analyser *BurndownAnalysis) Hibernate() error {
	state := burndownState{
		GlobalHistory: sparseHistoryToMap(analyser.globalHistory),
		Matrix:        analyser.matrix,
	}
	if analyser.fileHistories != nil {
		state.FileHistories = make(map[core.FileId]map[int]map[int]int64, len(analyser.fileHistories))
		for k, v := range analyser.fileHistories {
			state.FileHistories[k] = sparseHistoryToMap(v)
		}
	}
	if analyser.peopleHistories != nil {
		state.PeopleHistories = make([]map[int]map[int]int64, len(analyser.peopleHistories))
		for i, v := range analyser.peopleHistories {
			state.PeopleHistories[i] = sparseHistoryToMap(v)
		}
	}

	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(fw).Encode(state); err != nil {
		fw.Close()
		return err
	}
	if err := fw.Close(); err != nil {
		return err
	}

	analyser.globalHistory = nil
	analyser.fileHistories = nil
	analyser.peopleHistories = nil
	analyser.matrix = nil

	if analyser.HibernationToDisk {
		file, err := os.CreateTemp(analyser.HibernationDirectory, "*-hercules-burndown.bin")
		if err != nil {
			return err
		}
		analyser.hibernatedFileName = file.Name()
		if _, err := file.Write(buf.Bytes()); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	} else {
		analyser.hibernatedData = buf.Bytes()
	}
	return nil
}

// Boot restores the burndown analysis state from hibernation.
func (analyser *BurndownAnalysis) Boot() error {
	var data []byte
	if analyser.hibernatedFileName != "" {
		var err error
		data, err = os.ReadFile(analyser.hibernatedFileName)
		if err != nil {
			return err
		}
		if err := os.Remove(analyser.hibernatedFileName); err != nil {
			return err
		}
		analyser.hibernatedFileName = ""
	} else {
		data = analyser.hibernatedData
		analyser.hibernatedData = nil
	}

	fr := flate.NewReader(bytes.NewReader(data))
	defer fr.Close()

	var state burndownState
	if err := gob.NewDecoder(fr).Decode(&state); err != nil {
		return err
	}

	analyser.globalHistory = mapToSparseHistory(state.GlobalHistory)
	analyser.matrix = state.Matrix

	if state.FileHistories != nil {
		analyser.fileHistories = make(map[core.FileId]sparseHistory, len(state.FileHistories))
		for k, v := range state.FileHistories {
			analyser.fileHistories[k] = mapToSparseHistory(v)
		}
	}
	if state.PeopleHistories != nil {
		analyser.peopleHistories = make([]sparseHistory, len(state.PeopleHistories))
		for i, v := range state.PeopleHistories {
			analyser.peopleHistories[i] = mapToSparseHistory(v)
		}
	}

	return nil
}
```

**Step 6: Run tests**

Run: `go test ./leaves -run TestBurndownHibernate -v`
Expected: PASS

**Step 7: Run full burndown test suite to check for regressions**

Run: `go test ./leaves -run TestBurndown -v`
Expected: All PASS

**Step 8: Commit**

```bash
git add leaves/burndown.go
git commit -m "feat: add hibernation support to BurndownAnalysis"
```

---

### Task 3: Update BurndownMeta test for new config options

**Files:**
- Modify: `leaves/burndown_test.go`

**Step 1: Update TestBurndownMeta to expect the new config options**

In `TestBurndownMeta` (line 49-58), the test counts config options. Update the switch to include the new hibernation options:

```go
	opts := bd.ListConfigurationOptions()
	matches := 0
	for _, opt := range opts {
		switch opt.Name {
		case ConfigBurndownGranularity, ConfigBurndownSampling, ConfigBurndownTrackFiles,
			ConfigBurndownTrackPeople, ConfigBurndownHibernationDisk, ConfigBurndownHibernationDir:
			matches++
		}
	}
	assert.Len(t, opts, matches)
```

**Step 2: Run meta test**

Run: `go test ./leaves -run TestBurndownMeta -v`
Expected: PASS

**Step 3: Commit**

```bash
git add leaves/burndown_test.go
git commit -m "test: update burndown meta test for hibernation config options"
```

---

### Task 4: Add Preset System — Test

**Files:**
- Create: `cmd/hercules/preset_test.go`

**Step 1: Write failing test for preset application**

Create `cmd/hercules/preset_test.go`:

```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/hercules -run TestApplyPreset -v`
Expected: FAIL — `applyPreset` not defined.

**Step 3: Commit failing test**

```bash
git add cmd/hercules/preset_test.go
git commit -m "test: add failing tests for --preset flag system"
```

---

### Task 5: Implement Preset System

**Files:**
- Create: `cmd/hercules/preset.go`
- Modify: `cmd/hercules/root.go` (add flag and call)

**Step 1: Create preset.go**

Create `cmd/hercules/preset.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

// presetDefaults maps preset names to their flag defaults.
// Each entry is a map of flag-name → default-value (as string).
var presetDefaults = map[string]map[string]string{
	"large-repo": {
		"first-parent":                "true",
		"lines-hibernation-threshold": "200000",
		"lines-hibernation-disk":      "true",
		"granularity":                 "30",
		"sampling":                    "30",
	},
	"quick": {
		"head": "true",
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
			// User explicitly set this flag — don't override.
			continue
		}
		if err := flags.Set(flagName, value); err != nil {
			fmt.Fprintf(os.Stderr, "warning: preset %q: failed to set --%s=%s: %v\n",
				presetName, flagName, value, err)
		}
	}
}
```

**Step 2: Add --preset flag and call applyPreset in root.go**

In `cmd/hercules/root.go` `init()` function (after line 642, after the `--profile` flag):

```go
rootFlags.String("preset", "",
	"Apply a named set of flag defaults. Available: large-repo, quick. "+
		"Explicit flags override preset values.")
```

In the `Run` function of `rootCmd` (at line 208, right after `flags := cmd.Flags()`), add:

```go
applyPreset(flags)
```

**Step 3: Run preset tests**

Run: `go test ./cmd/hercules -run TestApplyPreset -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add cmd/hercules/preset.go cmd/hercules/root.go
git commit -m "feat: add --preset flag with large-repo and quick presets"
```

---

### Task 6: Verify Full Test Suite

**Files:** None (verification only)

**Step 1: Run all Go tests**

Run: `go test ./...`
Expected: All PASS

**Step 2: Build the binary**

Run: `go build -o hercules ./cmd/hercules`
Expected: Success

**Step 3: Smoke test with --preset**

Run: `./hercules --preset quick --burndown .`
Expected: Runs successfully on the hercules repo itself.

Run: `./hercules --help | grep preset`
Expected: Shows the `--preset` flag with description.

**Step 4: Commit any fixups if needed, then squash/rebase**

No action if all tests pass.

---

### Task 7: Update ROADMAP.md

**Files:**
- Modify: `ROADMAP.md`

**Step 1: Mark completed items**

In `ROADMAP.md` Milestone 2, check off:
- `[x]` for "Add scaling presets"
- `[x]` for the `large-repo` and `quick` preset sub-items
- Add a note under the performance validation item that hibernation is now enabled by default for LineHistory and supported for BurndownAnalysis.

**Step 2: Commit**

```bash
git add ROADMAP.md
git commit -m "docs: update ROADMAP.md with Milestone 2 progress"
```
