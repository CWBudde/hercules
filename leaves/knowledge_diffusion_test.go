package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
)

const (
	dummyHashOne = "1111111111111111111111111111111111111111"
	dummyHashTwo = "2222222222222222222222222222222222222222"
)

// knowledgeDiffusionDeps builds a Consume dependency map for the editor-tracking tests: no
// commit, so Finalize reports no line counts, and no line stats, so nothing counts as churn.
// The tests that exercise those two factors supply them explicitly.
func knowledgeDiffusionDeps(changes object.Changes, author, tick int) map[string]any {
	return knowledgeDiffusionDepsWithStats(changes, author, tick, nil)
}

func knowledgeDiffusionDepsWithStats(
	changes object.Changes, author, tick int, lineStats map[object.ChangeEntry]items.LineStats,
) map[string]any {
	if lineStats == nil {
		lineStats = map[object.ChangeEntry]items.LineStats{}
	}

	return map[string]any{
		core.DependencyCommit:       (*object.Commit)(nil),
		items.DependencyTreeChanges: changes,
		items.DependencyLineStats:   lineStats,
		identity.DependencyAuthor:   author,
		items.DependencyTick:        tick,
	}
}

func consumeKnowledgeDiffusion(
	t *testing.T, kd *KnowledgeDiffusionAnalysis, deps map[string]any,
) {
	t.Helper()
	_, err := kd.Consume(deps)
	assert.NoError(t, err)
}

// makeInsertChange creates a Change representing a file insertion.
func makeInsertChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{},
		To: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: plumbing.NewHash(dummyHashOne)},
		},
	}
}

// makeModifyChange creates a Change representing a file modification (same name).
func makeModifyChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: plumbing.NewHash(dummyHashOne)},
		},
		To: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: plumbing.NewHash(dummyHashTwo)},
		},
	}
}

// makeRenameChange creates a Change representing a file rename.
func makeRenameChange(from, to string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      from,
			TreeEntry: object.TreeEntry{Name: from, Hash: plumbing.NewHash(dummyHashOne)},
		},
		To: object.ChangeEntry{
			Name:      to,
			TreeEntry: object.TreeEntry{Name: to, Hash: plumbing.NewHash(dummyHashTwo)},
		},
	}
}

// makeDeleteChange creates a Change representing a file deletion.
func makeDeleteChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: plumbing.NewHash(dummyHashOne)},
		},
		To: object.ChangeEntry{},
	}
}

func TestKnowledgeDiffusionMeta(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.Equal(t, "KnowledgeDiffusion", kd.Name())
	assert.Empty(t, kd.Provides())
	assert.Contains(t, kd.Requires(), identity.DependencyAuthor)
	assert.Contains(t, kd.Requires(), items.DependencyTreeChanges)
	assert.Contains(t, kd.Requires(), items.DependencyLineStats)
	assert.Contains(t, kd.Requires(), items.DependencyTick)
	// DependencyCommit is injected by the pipeline and must not be declared - the lifecycle test
	// rejects a leaf which requires a dependency nothing provides.
	assert.NotContains(t, kd.Requires(), core.DependencyCommit)
	assert.Equal(t, "knowledge-diffusion", kd.Flag())
	assert.NotEmpty(t, kd.Description())
}

func TestKnowledgeDiffusionRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&KnowledgeDiffusionAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "KnowledgeDiffusion", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&KnowledgeDiffusionAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestKnowledgeDiffusionListConfigurationOptions(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	opts := kd.ListConfigurationOptions()
	assert.Len(t, opts, 1)
	assert.Equal(t, ConfigKnowledgeDiffusionWindowMonths, opts[0].Name)
	assert.Equal(t, "knowledge-diffusion-window", opts[0].Flag)
}

func TestKnowledgeDiffusionConfigure(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	facts := map[string]any{
		identity.FactIdentityDetectorReversedPeopleDict: []string{testPersonAlice, testPersonBob, testPersonCharlie},
		items.FactTickSize:                   24 * time.Hour,
		ConfigKnowledgeDiffusionWindowMonths: 12,
		core.ConfigLogger:                    core.NewLogger(),
	}
	assert.NoError(t, kd.Configure(facts))
	assert.Equal(t, []string{testPersonAlice, testPersonBob, testPersonCharlie}, kd.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, kd.tickSize)
	assert.Equal(t, 12, kd.WindowMonths)
}

func TestKnowledgeDiffusionConfigureDefaults(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Configure(map[string]any{}))
	assert.NoError(t, kd.Initialize(test.Repository))
	assert.Equal(t, 6, kd.WindowMonths)
}

func TestKnowledgeDiffusionInitialize(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	assert.NotNil(t, kd.fileAuthors)
	assert.Equal(t, -1, kd.lastTick)
	assert.Equal(t, 6, kd.WindowMonths)
}

func TestKnowledgeDiffusionConfigureUpstream(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.ConfigureUpstream(map[string]any{}))
}

func TestKnowledgeDiffusionConsumeBasic(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Tick 0: Alice inserts file.go
	changes := object.Changes{makeInsertChange("file.go")}
	deps := knowledgeDiffusionDeps(changes, 0, 0)
	res, err := kd.Consume(deps)
	assert.Nil(t, res)
	assert.NoError(t, err)

	assert.Len(t, kd.fileAuthors, 1)
	assert.Len(t, kd.fileAuthors["file.go"], 1)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].LastTick)
}

func TestKnowledgeDiffusionConsumeMultipleAuthors(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Tick 0: Author 0 inserts file.go
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange("file.go")}, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Tick 5: Author 1 modifies file.go
	deps = knowledgeDiffusionDeps(object.Changes{makeModifyChange("file.go")}, 1, 5)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Tick 10: Author 2 modifies file.go
	deps = knowledgeDiffusionDeps(object.Changes{makeModifyChange("file.go")}, 2, 10)
	consumeKnowledgeDiffusion(t, &kd, deps)

	assert.Len(t, kd.fileAuthors["file.go"], 3)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 5, kd.fileAuthors["file.go"][1].FirstTick)
	assert.Equal(t, 10, kd.fileAuthors["file.go"][2].FirstTick)
}

func TestKnowledgeDiffusionConsumeDelete(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Insert file
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange("deleted.go")}, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Delete file - should NOT add author 1 as editor
	deps = knowledgeDiffusionDeps(object.Changes{makeDeleteChange("deleted.go")}, 1, 5)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Only author 0 should be recorded
	assert.Len(t, kd.fileAuthors["deleted.go"], 1)
	assert.Contains(t, kd.fileAuthors["deleted.go"], 0)
}

func TestKnowledgeDiffusionConsumeRename(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Insert old.go by author 0
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange("old.go")}, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Rename old.go -> new.go by author 1
	deps = knowledgeDiffusionDeps(object.Changes{makeRenameChange("old.go", "new.go")}, 1, 5)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// old.go should be gone, new.go should have both authors
	_, oldExists := kd.fileAuthors["old.go"]
	assert.False(t, oldExists, "old.go should be removed after rename")
	assert.Len(t, kd.fileAuthors["new.go"], 2)
	assert.Contains(t, kd.fileAuthors["new.go"], 0) // original author carried over
	assert.Contains(t, kd.fileAuthors["new.go"], 1) // renaming author
}

func TestKnowledgeDiffusionConsumeSameAuthorMultipleTicks(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Same author edits same file at different ticks
	for tick := range 5 {
		action := makeModifyChange("file.go")
		if tick == 0 {
			action = makeInsertChange("file.go")
		}
		deps := knowledgeDiffusionDeps(object.Changes{action}, 0, tick)
		consumeKnowledgeDiffusion(t, &kd, deps)
	}

	// Still only one unique author
	assert.Len(t, kd.fileAuthors["file.go"], 1)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 4, kd.fileAuthors["file.go"][0].LastTick)
}

func TestKnowledgeDiffusionFinalize(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour
	kd.reversedPeopleDict = []string{testPersonAlice, testPersonBob, testPersonCharlie}

	// file1.go: touched by Alice at tick 0, Bob at tick 5
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange(testFileOnePath)}, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	deps = knowledgeDiffusionDeps(object.Changes{makeModifyChange(testFileOnePath)}, 1, 5)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// file2.go: touched only by Charlie at tick 3
	deps = knowledgeDiffusionDeps(object.Changes{makeInsertChange(testFileTwoPath)}, 2, 3)
	consumeKnowledgeDiffusion(t, &kd, deps)

	result := kd.Finalize().(KnowledgeDiffusionResult)

	assert.Len(t, result.Files, 2)

	// file1.go: 2 unique editors
	f1 := result.Files[testFileOnePath]
	assert.Equal(t, 2, f1.UniqueEditorsCount)
	assert.Equal(t, []int{0, 1}, f1.Authors)
	assert.Equal(t, 1, f1.UniqueEditorsOverTime[0]) // Alice at tick 0
	assert.Equal(t, 2, f1.UniqueEditorsOverTime[5]) // Bob at tick 5

	// file2.go: 1 unique editor
	f2 := result.Files[testFileTwoPath]
	assert.Equal(t, 1, f2.UniqueEditorsCount)
	assert.Equal(t, []int{2}, f2.Authors)
	assert.Equal(t, 1, f2.UniqueEditorsOverTime[3])

	// Distribution: 1 file with 2 editors, 1 file with 1 editor
	assert.Equal(t, 1, result.Distribution[1])
	assert.Equal(t, 1, result.Distribution[2])

	assert.Equal(t, []string{testPersonAlice, testPersonBob, testPersonCharlie}, result.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, result.tickSize)
}

func TestKnowledgeDiffusionFinalizeRecentEditors(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour
	kd.WindowMonths = 6

	// Author 0 edits file at tick 0 (long ago)
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange("file.go")}, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	// Author 1 edits file at tick 200 (recent, within 6 months ≈ 183 ticks at 1 day/tick)
	deps = knowledgeDiffusionDeps(object.Changes{makeModifyChange("file.go")}, 1, 200)
	consumeKnowledgeDiffusion(t, &kd, deps)

	result := kd.Finalize().(KnowledgeDiffusionResult)

	f := result.Files["file.go"]
	assert.Equal(t, 2, f.UniqueEditorsCount)
	// With 6 months ≈ 183 ticks and lastTick=200, cutoff ≈ 200-183=17.
	// Author 0 last edit at tick 0 < 17 → not recent.
	// Author 1 last edit at tick 200 >= 17 → recent.
	assert.Equal(t, 1, f.RecentEditorsCount)
}

func TestKnowledgeDiffusionFinalizeAllRecent(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour
	kd.WindowMonths = 6

	// Both authors edit recently
	deps := knowledgeDiffusionDeps(object.Changes{makeInsertChange("file.go")}, 0, 10)
	consumeKnowledgeDiffusion(t, &kd, deps)

	deps = knowledgeDiffusionDeps(object.Changes{makeModifyChange("file.go")}, 1, 12)
	consumeKnowledgeDiffusion(t, &kd, deps)

	result := kd.Finalize().(KnowledgeDiffusionResult)

	f := result.Files["file.go"]
	assert.Equal(t, 2, f.UniqueEditorsCount)
	// Both edits are within window (lastTick=12, window=183, cutoff=-171), so all are recent.
	assert.Equal(t, 2, f.RecentEditorsCount)
}

func TestKnowledgeDiffusionFinalizeChurnAndAge(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour
	kd.WindowMonths = 6 // 183 ticks

	insert := makeInsertChange("file.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{insert}, 0, 0,
		map[object.ChangeEntry]items.LineStats{insert.To: {Added: 100, Removed: 0, Changed: 0}},
	))

	// Tick 190 is inside the window (cutoff = 200-183 = 17); tick 5 is not.
	early := makeModifyChange("file.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{early}, 0, 5,
		map[object.ChangeEntry]items.LineStats{early.To: {Added: 7, Removed: 3, Changed: 0}},
	))

	recent := makeModifyChange("file.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{recent}, 1, 190,
		map[object.ChangeEntry]items.LineStats{recent.To: {Added: 20, Removed: 5, Changed: 5}},
	))

	// A later commit touching a different file moves the analysis' last tick to 200, which is
	// what the file's age is measured against.
	other := makeInsertChange("other.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{other}, 1, 200,
		map[object.ChangeEntry]items.LineStats{other.To: {Added: 1}},
	))

	result := kd.Finalize().(KnowledgeDiffusionResult)

	file := result.Files["file.go"]
	assert.Equal(t, 140, file.Churn)      // 100 + 10 + 30
	assert.Equal(t, 30, file.RecentChurn) // only the tick-190 change is inside the window
	assert.Equal(t, 10, file.TicksSinceLastEdit)
	// No commit tree is available in this test, so no file has a line count.
	assert.Zero(t, file.Lines)

	assert.Zero(t, result.Files["other.go"].TicksSinceLastEdit)
}

func TestKnowledgeDiffusionSkipsBinaryChurn(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// LinesStatsCalculator omits binary changes, so the entry is simply absent.
	insert := makeInsertChange("logo.png")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{insert}, 0, 0, map[object.ChangeEntry]items.LineStats{},
	))

	result := kd.Finalize().(KnowledgeDiffusionResult)
	assert.Equal(t, 1, result.Files["logo.png"].UniqueEditorsCount)
	assert.Zero(t, result.Files["logo.png"].Churn)
}

func TestKnowledgeDiffusionRenameCarriesChurn(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	insert := makeInsertChange("old.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{insert}, 0, 0,
		map[object.ChangeEntry]items.LineStats{insert.To: {Added: 50}},
	))

	rename := makeRenameChange("old.go", "new.go")
	consumeKnowledgeDiffusion(t, &kd, knowledgeDiffusionDepsWithStats(
		object.Changes{rename}, 1, 5,
		map[object.ChangeEntry]items.LineStats{rename.To: {Added: 4, Removed: 2}},
	))

	result := kd.Finalize().(KnowledgeDiffusionResult)
	assert.NotContains(t, result.Files, "old.go")
	// The churn history follows the path the same way the editor history does.
	assert.Equal(t, 56, result.Files["new.go"].Churn)
}

func TestKnowledgeDiffusionSerializeText(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	result := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			testMainPath: {
				UniqueEditorsCount:    2,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2},
				RecentEditorsCount:    1,
				Authors:               []int{0, 1},
				Lines:                 412,
				Churn:                 1830,
				RecentChurn:           220,
				TicksSinceLastEdit:    4,
			},
		},
		Distribution:       map[int]int{2: 1},
		WindowMonths:       6,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := kd.Serialize(result, false, &buf)
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "knowledge_diffusion:")
	assert.Contains(t, output, "window_months: 6")
	assert.Contains(t, output, "\"main.go\":")
	assert.Contains(t, output, "unique_editors: 2")
	assert.Contains(t, output, "recent_editors: 1")
	assert.Contains(t, output, "lines: 412")
	assert.Contains(t, output, "churn: 1830")
	assert.Contains(t, output, "recent_churn: 220")
	assert.Contains(t, output, "ticks_since_last_edit: 4")
	assert.Contains(t, output, "editors_over_time:")
	assert.Contains(t, output, "distribution:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, testPersonAlice)
	assert.Contains(t, output, testPersonBob)
}

func TestKnowledgeDiffusionSerializeBinaryRoundtrip(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	result := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			testMainPath: {
				UniqueEditorsCount:    2,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2},
				RecentEditorsCount:    1,
				Authors:               []int{0, 1},
				Lines:                 412,
				Churn:                 1830,
				RecentChurn:           220,
				TicksSinceLastEdit:    4,
			},
			"util.go": {
				UniqueEditorsCount:    1,
				UniqueEditorsOverTime: map[int]int{3: 1},
				RecentEditorsCount:    1,
				Authors:               []int{0},
			},
		},
		Distribution:       map[int]int{1: 1, 2: 1},
		WindowMonths:       6,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	// Serialize to binary
	var buf bytes.Buffer
	err := kd.Serialize(result, true, &buf)
	assert.NoError(t, err)
	assert.Positive(t, buf.Len())

	// Deserialize
	rawResult2, err := kd.Deserialize(buf.Bytes())
	assert.NoError(t, err)
	result2 := rawResult2.(KnowledgeDiffusionResult)

	// Compare
	assert.Equal(t, result.reversedPeopleDict, result2.reversedPeopleDict)
	assert.Equal(t, result.tickSize, result2.tickSize)
	assert.Equal(t, result.WindowMonths, result2.WindowMonths)
	assert.Len(t, result2.Files, 2)

	f1 := result2.Files[testMainPath]
	assert.Equal(t, 2, f1.UniqueEditorsCount)
	assert.Equal(t, 1, f1.RecentEditorsCount)
	assert.Equal(t, map[int]int{0: 1, 5: 2}, f1.UniqueEditorsOverTime)
	assert.Equal(t, []int{0, 1}, f1.Authors)
	assert.Equal(t, 412, f1.Lines)
	assert.Equal(t, 1830, f1.Churn)
	assert.Equal(t, 220, f1.RecentChurn)
	assert.Equal(t, 4, f1.TicksSinceLastEdit)

	f2 := result2.Files["util.go"]
	assert.Equal(t, 1, f2.UniqueEditorsCount)
	assert.Equal(t, 1, f2.RecentEditorsCount)
	assert.Equal(t, map[int]int{3: 1}, f2.UniqueEditorsOverTime)
	assert.Equal(t, []int{0}, f2.Authors)
	// All-zero decodes as unset, which is what a file written before these fields existed carries.
	assert.Zero(t, f2.Lines)
	assert.Zero(t, f2.Churn)
	assert.Zero(t, f2.RecentChurn)
	assert.Zero(t, f2.TicksSinceLastEdit)

	assert.Equal(t, result.Distribution, result2.Distribution)
}

func TestKnowledgeDiffusionFork(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.fileAuthors = map[string]map[int]*authorFileInfo{
		"file.go": {0: {FirstTick: 0, LastTick: 5}},
	}

	forks := kd.Fork(2)
	assert.Len(t, forks, 2)

	kd2 := forks[0].(*KnowledgeDiffusionAnalysis)
	kd3 := forks[1].(*KnowledgeDiffusionAnalysis)
	assert.Equal(t, kd.fileAuthors, kd2.fileAuthors)
	assert.Equal(t, kd.fileAuthors, kd3.fileAuthors)
}

func TestKnowledgeDiffusionMergeResults(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}

	r1 := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			"shared.go": {
				UniqueEditorsCount:    1,
				UniqueEditorsOverTime: map[int]int{0: 1},
				RecentEditorsCount:    1,
				Authors:               []int{0},
			},
			"only_r1.go": {
				UniqueEditorsCount:    2,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2},
				RecentEditorsCount:    2,
				Authors:               []int{0, 1},
			},
		},
		Distribution:       map[int]int{1: 1, 2: 1},
		WindowMonths:       6,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob},
		tickSize:           24 * time.Hour,
	}

	r2 := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			"shared.go": {
				UniqueEditorsCount:    3,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2, 10: 3},
				RecentEditorsCount:    2,
				Authors:               []int{0, 1, 2},
			},
			"only_r2.go": {
				UniqueEditorsCount:    1,
				UniqueEditorsOverTime: map[int]int{2: 1},
				RecentEditorsCount:    1,
				Authors:               []int{2},
			},
		},
		Distribution:       map[int]int{1: 1, 3: 1},
		WindowMonths:       6,
		reversedPeopleDict: []string{testPersonAlice, testPersonBob, testPersonCharlie},
		tickSize:           24 * time.Hour,
	}

	c1 := &core.CommonAnalysisResult{}
	c2 := &core.CommonAnalysisResult{}

	merged := kd.MergeResults(r1, r2, c1, c2).(KnowledgeDiffusionResult)

	// shared.go: r2 has more editors (3 > 1), so r2 wins
	assert.Equal(t, 3, merged.Files["shared.go"].UniqueEditorsCount)
	// only_r1.go: preserved from r1
	assert.Equal(t, 2, merged.Files["only_r1.go"].UniqueEditorsCount)
	// only_r2.go: preserved from r2
	assert.Equal(t, 1, merged.Files["only_r2.go"].UniqueEditorsCount)

	// Distribution is recomputed from merged files
	assert.Equal(t, 1, merged.Distribution[1]) // only_r2.go
	assert.Equal(t, 1, merged.Distribution[2]) // only_r1.go
	assert.Equal(t, 1, merged.Distribution[3]) // shared.go
}

func TestKnowledgeDiffusionMergeCarriesRankingFactors(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}

	first := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			"shared.go": {UniqueEditorsCount: 1, Lines: 10, Churn: 20, RecentChurn: 5, TicksSinceLastEdit: 90},
			"only_a.go": {UniqueEditorsCount: 1, Lines: 30, Churn: 40, RecentChurn: 7, TicksSinceLastEdit: 3},
		},
		Distribution: map[int]int{1: 2},
		WindowMonths: 6,
	}
	second := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			// More editors, so this side wins the path - and its factors must come with it.
			"shared.go": {UniqueEditorsCount: 3, Lines: 500, Churn: 600, RecentChurn: 70, TicksSinceLastEdit: 1},
		},
		Distribution: map[int]int{3: 1},
		WindowMonths: 6,
	}

	merged := kd.MergeResults(first, second, nil, nil).(KnowledgeDiffusionResult)

	shared := merged.Files["shared.go"]
	assert.Equal(t, 3, shared.UniqueEditorsCount)
	assert.Equal(t, 500, shared.Lines)
	assert.Equal(t, 600, shared.Churn)
	assert.Equal(t, 70, shared.RecentChurn)
	assert.Equal(t, 1, shared.TicksSinceLastEdit)

	onlyA := merged.Files["only_a.go"]
	assert.Equal(t, 30, onlyA.Lines)
	assert.Equal(t, 40, onlyA.Churn)
	assert.Equal(t, 3, onlyA.TicksSinceLastEdit)
}

func TestKnowledgeDiffusionWindowTicks(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{WindowMonths: 6}
	kd.tickSize = 24 * time.Hour

	windowTicks := kd.windowTicks()
	// 6 months * 30.44 days/month ≈ 183 ticks
	assert.InDelta(t, 182, windowTicks, 2)
}

func TestKnowledgeDiffusionWindowTicksZeroTickSize(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{WindowMonths: 6}
	kd.tickSize = 0
	assert.Equal(t, 0, kd.windowTicks())
}

func TestKnowledgeDiffusionConsumeMultipleFiles(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	// Single commit touches multiple files
	changes := object.Changes{
		makeInsertChange("a.go"),
		makeInsertChange("b.go"),
		makeInsertChange("c.go"),
	}
	deps := knowledgeDiffusionDeps(changes, 0, 0)
	consumeKnowledgeDiffusion(t, &kd, deps)

	assert.Len(t, kd.fileAuthors, 3)
	assert.Len(t, kd.fileAuthors["a.go"], 1)
	assert.Len(t, kd.fileAuthors["b.go"], 1)
	assert.Len(t, kd.fileAuthors["c.go"], 1)
}

func TestKnowledgeDiffusionFinalizeEmpty(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.NoError(t, kd.Initialize(test.Repository))
	kd.tickSize = 24 * time.Hour

	result := kd.Finalize().(KnowledgeDiffusionResult)
	assert.Empty(t, result.Files)
	assert.Empty(t, result.Distribution)
}

func TestKnowledgeDiffusionDeserializeError(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	_, err := kd.Deserialize([]byte("invalid protobuf"))
	assert.Error(t, err)
}
