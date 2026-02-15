package leaves

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
)

var (
	dummyHash1 = plumbing.NewHash("1111111111111111111111111111111111111111")
	dummyHash2 = plumbing.NewHash("2222222222222222222222222222222222222222")
	dummyHash3 = plumbing.NewHash("3333333333333333333333333333333333333333")
)

// makeInsertChange creates a Change representing a file insertion.
func makeInsertChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{},
		To: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: dummyHash1},
		},
	}
}

// makeModifyChange creates a Change representing a file modification (same name).
func makeModifyChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: dummyHash1},
		},
		To: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: dummyHash2},
		},
	}
}

// makeRenameChange creates a Change representing a file rename.
func makeRenameChange(from, to string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      from,
			TreeEntry: object.TreeEntry{Name: from, Hash: dummyHash1},
		},
		To: object.ChangeEntry{
			Name:      to,
			TreeEntry: object.TreeEntry{Name: to, Hash: dummyHash2},
		},
	}
}

// makeDeleteChange creates a Change representing a file deletion.
func makeDeleteChange(name string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{
			Name:      name,
			TreeEntry: object.TreeEntry{Name: name, Hash: dummyHash1},
		},
		To: object.ChangeEntry{},
	}
}

func TestKnowledgeDiffusionMeta(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.Equal(t, "KnowledgeDiffusion", kd.Name())
	assert.Len(t, kd.Provides(), 0)
	assert.Contains(t, kd.Requires(), identity.DependencyAuthor)
	assert.Contains(t, kd.Requires(), items.DependencyTreeChanges)
	assert.Contains(t, kd.Requires(), items.DependencyTick)
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
	facts := map[string]interface{}{
		identity.FactIdentityDetectorReversedPeopleDict: []string{"Alice", "Bob", "Charlie"},
		items.FactTickSize:                   24 * time.Hour,
		ConfigKnowledgeDiffusionWindowMonths: 12,
		core.ConfigLogger:                    core.NewLogger(),
	}
	assert.Nil(t, kd.Configure(facts))
	assert.Equal(t, []string{"Alice", "Bob", "Charlie"}, kd.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, kd.tickSize)
	assert.Equal(t, 12, kd.WindowMonths)
}

func TestKnowledgeDiffusionConfigureDefaults(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.Nil(t, kd.Configure(map[string]interface{}{}))
	assert.Nil(t, kd.Initialize(test.Repository))
	assert.Equal(t, 6, kd.WindowMonths)
}

func TestKnowledgeDiffusionInitialize(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.Nil(t, kd.Initialize(test.Repository))
	assert.NotNil(t, kd.fileAuthors)
	assert.Equal(t, -1, kd.lastTick)
	assert.Equal(t, 6, kd.WindowMonths)
}

func TestKnowledgeDiffusionConfigureUpstream(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	assert.Nil(t, kd.ConfigureUpstream(map[string]interface{}{}))
}

func TestKnowledgeDiffusionConsumeBasic(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Tick 0: Alice inserts file.go
	changes := object.Changes{makeInsertChange("file.go")}
	deps := map[string]interface{}{
		items.DependencyTreeChanges: changes,
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	res, err := kd.Consume(deps)
	assert.Nil(t, res)
	assert.Nil(t, err)

	assert.Len(t, kd.fileAuthors, 1)
	assert.Len(t, kd.fileAuthors["file.go"], 1)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].LastTick)
}

func TestKnowledgeDiffusionConsumeMultipleAuthors(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Tick 0: Author 0 inserts file.go
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("file.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	// Tick 5: Author 1 modifies file.go
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeModifyChange("file.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        5,
	}
	kd.Consume(deps)

	// Tick 10: Author 2 modifies file.go
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeModifyChange("file.go")},
		identity.DependencyAuthor:   2,
		items.DependencyTick:        10,
	}
	kd.Consume(deps)

	assert.Len(t, kd.fileAuthors["file.go"], 3)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 5, kd.fileAuthors["file.go"][1].FirstTick)
	assert.Equal(t, 10, kd.fileAuthors["file.go"][2].FirstTick)
}

func TestKnowledgeDiffusionConsumeDelete(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Insert file
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("deleted.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	// Delete file - should NOT add author 1 as editor
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeDeleteChange("deleted.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        5,
	}
	kd.Consume(deps)

	// Only author 0 should be recorded
	assert.Len(t, kd.fileAuthors["deleted.go"], 1)
	assert.Contains(t, kd.fileAuthors["deleted.go"], 0)
}

func TestKnowledgeDiffusionConsumeRename(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Insert old.go by author 0
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("old.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	// Rename old.go -> new.go by author 1
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeRenameChange("old.go", "new.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        5,
	}
	kd.Consume(deps)

	// old.go should be gone, new.go should have both authors
	_, oldExists := kd.fileAuthors["old.go"]
	assert.False(t, oldExists, "old.go should be removed after rename")
	assert.Len(t, kd.fileAuthors["new.go"], 2)
	assert.Contains(t, kd.fileAuthors["new.go"], 0) // original author carried over
	assert.Contains(t, kd.fileAuthors["new.go"], 1) // renaming author
}

func TestKnowledgeDiffusionConsumeSameAuthorMultipleTicks(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Same author edits same file at different ticks
	for tick := 0; tick < 5; tick++ {
		action := makeModifyChange("file.go")
		if tick == 0 {
			action = makeInsertChange("file.go")
		}
		deps := map[string]interface{}{
			items.DependencyTreeChanges: object.Changes{action},
			identity.DependencyAuthor:   0,
			items.DependencyTick:        tick,
		}
		kd.Consume(deps)
	}

	// Still only one unique author
	assert.Len(t, kd.fileAuthors["file.go"], 1)
	assert.Equal(t, 0, kd.fileAuthors["file.go"][0].FirstTick)
	assert.Equal(t, 4, kd.fileAuthors["file.go"][0].LastTick)
}

func TestKnowledgeDiffusionFinalize(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour
	kd.reversedPeopleDict = []string{"Alice", "Bob", "Charlie"}

	// file1.go: touched by Alice at tick 0, Bob at tick 5
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("file1.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeModifyChange("file1.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        5,
	}
	kd.Consume(deps)

	// file2.go: touched only by Charlie at tick 3
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("file2.go")},
		identity.DependencyAuthor:   2,
		items.DependencyTick:        3,
	}
	kd.Consume(deps)

	result := kd.Finalize().(KnowledgeDiffusionResult)

	assert.Len(t, result.Files, 2)

	// file1.go: 2 unique editors
	f1 := result.Files["file1.go"]
	assert.Equal(t, 2, f1.UniqueEditorsCount)
	assert.Equal(t, []int{0, 1}, f1.Authors)
	assert.Equal(t, 1, f1.UniqueEditorsOverTime[0]) // Alice at tick 0
	assert.Equal(t, 2, f1.UniqueEditorsOverTime[5]) // Bob at tick 5

	// file2.go: 1 unique editor
	f2 := result.Files["file2.go"]
	assert.Equal(t, 1, f2.UniqueEditorsCount)
	assert.Equal(t, []int{2}, f2.Authors)
	assert.Equal(t, 1, f2.UniqueEditorsOverTime[3])

	// Distribution: 1 file with 2 editors, 1 file with 1 editor
	assert.Equal(t, 1, result.Distribution[1])
	assert.Equal(t, 1, result.Distribution[2])

	assert.Equal(t, []string{"Alice", "Bob", "Charlie"}, result.reversedPeopleDict)
	assert.Equal(t, 24*time.Hour, result.tickSize)
}

func TestKnowledgeDiffusionFinalizeRecentEditors(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour
	kd.WindowMonths = 6

	// Author 0 edits file at tick 0 (long ago)
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("file.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	// Author 1 edits file at tick 200 (recent, within 6 months ≈ 183 ticks at 1 day/tick)
	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeModifyChange("file.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        200,
	}
	kd.Consume(deps)

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
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour
	kd.WindowMonths = 6

	// Both authors edit recently
	deps := map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeInsertChange("file.go")},
		identity.DependencyAuthor:   0,
		items.DependencyTick:        10,
	}
	kd.Consume(deps)

	deps = map[string]interface{}{
		items.DependencyTreeChanges: object.Changes{makeModifyChange("file.go")},
		identity.DependencyAuthor:   1,
		items.DependencyTick:        12,
	}
	kd.Consume(deps)

	result := kd.Finalize().(KnowledgeDiffusionResult)

	f := result.Files["file.go"]
	assert.Equal(t, 2, f.UniqueEditorsCount)
	// Both edits are within window (lastTick=12, window=183, cutoff=-171), so all are recent.
	assert.Equal(t, 2, f.RecentEditorsCount)
}

func TestKnowledgeDiffusionSerializeText(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	result := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			"main.go": {
				UniqueEditorsCount:    2,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2},
				RecentEditorsCount:    1,
				Authors:               []int{0, 1},
			},
		},
		Distribution:       map[int]int{2: 1},
		WindowMonths:       6,
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	var buf bytes.Buffer
	err := kd.Serialize(result, false, &buf)
	assert.Nil(t, err)

	output := buf.String()
	assert.Contains(t, output, "knowledge_diffusion:")
	assert.Contains(t, output, "window_months: 6")
	assert.Contains(t, output, "\"main.go\":")
	assert.Contains(t, output, "unique_editors: 2")
	assert.Contains(t, output, "recent_editors: 1")
	assert.Contains(t, output, "editors_over_time:")
	assert.Contains(t, output, "distribution:")
	assert.Contains(t, output, "people:")
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestKnowledgeDiffusionSerializeBinaryRoundtrip(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	result := KnowledgeDiffusionResult{
		Files: map[string]*KnowledgeDiffusionFileResult{
			"main.go": {
				UniqueEditorsCount:    2,
				UniqueEditorsOverTime: map[int]int{0: 1, 5: 2},
				RecentEditorsCount:    1,
				Authors:               []int{0, 1},
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
		reversedPeopleDict: []string{"Alice", "Bob"},
		tickSize:           24 * time.Hour,
	}

	// Serialize to binary
	var buf bytes.Buffer
	err := kd.Serialize(result, true, &buf)
	assert.Nil(t, err)
	assert.Greater(t, buf.Len(), 0)

	// Deserialize
	rawResult2, err := kd.Deserialize(buf.Bytes())
	assert.Nil(t, err)
	result2 := rawResult2.(KnowledgeDiffusionResult)

	// Compare
	assert.Equal(t, result.reversedPeopleDict, result2.reversedPeopleDict)
	assert.Equal(t, result.tickSize, result2.tickSize)
	assert.Equal(t, result.WindowMonths, result2.WindowMonths)
	assert.Len(t, result2.Files, 2)

	f1 := result2.Files["main.go"]
	assert.Equal(t, 2, f1.UniqueEditorsCount)
	assert.Equal(t, 1, f1.RecentEditorsCount)
	assert.Equal(t, map[int]int{0: 1, 5: 2}, f1.UniqueEditorsOverTime)
	assert.Equal(t, []int{0, 1}, f1.Authors)

	f2 := result2.Files["util.go"]
	assert.Equal(t, 1, f2.UniqueEditorsCount)
	assert.Equal(t, 1, f2.RecentEditorsCount)
	assert.Equal(t, map[int]int{3: 1}, f2.UniqueEditorsOverTime)
	assert.Equal(t, []int{0}, f2.Authors)

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
		reversedPeopleDict: []string{"Alice", "Bob"},
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
		reversedPeopleDict: []string{"Alice", "Bob", "Charlie"},
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
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	// Single commit touches multiple files
	changes := object.Changes{
		makeInsertChange("a.go"),
		makeInsertChange("b.go"),
		makeInsertChange("c.go"),
	}
	deps := map[string]interface{}{
		items.DependencyTreeChanges: changes,
		identity.DependencyAuthor:   0,
		items.DependencyTick:        0,
	}
	kd.Consume(deps)

	assert.Len(t, kd.fileAuthors, 3)
	assert.Len(t, kd.fileAuthors["a.go"], 1)
	assert.Len(t, kd.fileAuthors["b.go"], 1)
	assert.Len(t, kd.fileAuthors["c.go"], 1)
}

func TestKnowledgeDiffusionFinalizeEmpty(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	kd.Initialize(test.Repository)
	kd.tickSize = 24 * time.Hour

	result := kd.Finalize().(KnowledgeDiffusionResult)
	assert.Len(t, result.Files, 0)
	assert.Len(t, result.Distribution, 0)
}

func TestKnowledgeDiffusionDeserializeError(t *testing.T) {
	kd := KnowledgeDiffusionAnalysis{}
	_, err := kd.Deserialize([]byte("invalid protobuf"))
	assert.NotNil(t, err)
}
