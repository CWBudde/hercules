package plumbing

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureTreeDiff() *TreeDiff {
	td := TreeDiff{}
	td.Configure(nil)
	td.Initialize(test.Repository)
	return &td
}

func TestTreeDiffMeta(t *testing.T) {
	td := fixtureTreeDiff()
	assert.Equal(t, "TreeDiff", td.Name())
	assert.Empty(t, td.Requires())
	assert.Len(t, td.Provides(), 1)
	assert.Equal(t, DependencyTreeChanges, td.Provides()[0])
	opts := td.ListConfigurationOptions()
	assert.Len(t, opts, 4)
	logger := core.NewLogger()
	assert.NoError(t, td.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, td.l)
}

func TestTreeDiffConfigure(t *testing.T) {
	td := fixtureTreeDiff()
	facts := map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"vendor"},
		ConfigTreeDiffLanguages:           []string{"go"},
		ConfigTreeDiffFilterRegexp:        "_.*",
	}
	assert.NoError(t, td.Configure(facts))
	assert.Equal(t, map[string]bool{"go": true}, td.Languages)
	assert.Equal(t, []string{"vendor"}, td.SkipFiles)
	assert.Equal(t, "_.*", td.NameFilter.String())
	delete(facts, ConfigTreeDiffLanguages)
	td.Languages = nil
	assert.NoError(t, td.Configure(facts))
	assert.Equal(t, map[string]bool{"all": true}, td.Languages)
	td.SkipFiles = []string{"test"}
	delete(facts, ConfigTreeDiffEnableBlacklist)
	assert.NoError(t, td.Configure(facts))
	assert.Equal(t, []string{"test"}, td.SkipFiles)
}

func TestTreeDiffRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&TreeDiff{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "TreeDiff", summoned[0].Name())
	summoned = core.Registry.Summon((&TreeDiff{}).Provides()[0])
	assert.GreaterOrEqual(t, len(summoned), 1)
	matched := false
	for _, tp := range summoned {
		matched = matched || tp.Name() == "TreeDiff"
	}
	assert.True(t, matched)
}

func TestTreeDiffConsume(t *testing.T) {
	td := fixtureTreeDiff()
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	prevCommit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"fbe766ffdc3f87f6affddc051c6f8b419beea6a2",
	))
	td.previousTree, _ = prevCommit.Tree()
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 12)
	baseline := map[string]merkletrie.Action{
		"analyser.go":               merkletrie.Delete,
		"cmd/hercules/main.go":      merkletrie.Modify,
		"blob_cache.go":             merkletrie.Insert,
		"burndown.go":               merkletrie.Insert,
		"day.go":                    merkletrie.Insert,
		"dummies.go":                merkletrie.Insert,
		"identity.go":               merkletrie.Insert,
		"pipeline.go":               merkletrie.Insert,
		"renames.go":                merkletrie.Insert,
		"toposort/toposort.go":      merkletrie.Insert,
		"toposort/toposort_test.go": merkletrie.Insert,
		"tree_diff.go":              merkletrie.Insert,
	}
	for _, change := range changes {
		action, err := change.Action()
		assert.NoError(t, err)
		if change.From.Name != "" {
			assert.Contains(t, baseline, change.From.Name)
			assert.Equal(t, baseline[change.From.Name], action)
		} else {
			assert.Contains(t, baseline, change.To.Name)
			assert.Equal(t, baseline[change.To.Name], action)
		}
	}
}

func TestTreeDiffConsumeFirst(t *testing.T) {
	td := fixtureTreeDiff()
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 21)
	for _, change := range changes {
		action, err := change.Action()
		assert.NoError(t, err)
		assert.Equal(t, merkletrie.Insert, action)
	}
}

func TestTreeDiffBadCommit(t *testing.T) {
	td := fixtureTreeDiff()
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"2b1ed978194a94edeabbca6de7ff3b5771d4d665",
	))
	commit.TreeHash = plumbing.NewHash("0000000000000000000000000000000000000000")
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	res, err := td.Consume(deps)
	assert.Nil(t, res)
	assert.Error(t, err)
}

func TestTreeDiffConsumeSkip(t *testing.T) {
	// consume without skipping
	td := fixtureTreeDiff()
	assert.Contains(t, td.Languages, allLanguages)
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"aefdedf7cafa6ee110bae9a3910bf5088fdeb5a9",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	prevCommit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"1e076dc56989bc6aa1ef5f55901696e9e01423d4",
	))
	td.previousTree, _ = prevCommit.Tree()
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 37)

	// consume with skipping
	td = fixtureTreeDiff()
	td.previousTree, _ = prevCommit.Tree()
	td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"vendor/"},
	})
	res, err = td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 31)
}

func TestTreeDiffConsumeOnlyFilesThatMatchFilter(t *testing.T) {
	// consume without skipping
	td := fixtureTreeDiff()
	assert.Contains(t, td.Languages, allLanguages)
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"aefdedf7cafa6ee110bae9a3910bf5088fdeb5a9",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	prevCommit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"1e076dc56989bc6aa1ef5f55901696e9e01423d4",
	))
	td.previousTree, _ = prevCommit.Tree()
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 37)

	// consume with skipping
	td = fixtureTreeDiff()
	td.previousTree, _ = prevCommit.Tree()
	td.Configure(map[string]any{
		ConfigTreeDiffFilterRegexp: ".*go",
	})
	res, err = td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 27)
}

func TestTreeDiffConsumeLanguageFilterFirst(t *testing.T) {
	td := fixtureTreeDiff()
	td.Configure(map[string]any{ConfigTreeDiffLanguages: []string{"Go"}})
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"fbe766ffdc3f87f6affddc051c6f8b419beea6a2",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 6)
	assert.Equal(t, "analyser.go", changes[0].To.Name)
	assert.Equal(t, "cmd/hercules/main.go", changes[1].To.Name)
	assert.Equal(t, "doc.go", changes[2].To.Name)
	assert.Equal(t, "file.go", changes[3].To.Name)
	assert.Equal(t, "file_test.go", changes[4].To.Name)
	assert.Equal(t, "rbtree.go", changes[5].To.Name)
}

func TestTreeDiffConsumeLanguageFilter(t *testing.T) {
	td := fixtureTreeDiff()
	assert.NoError(t, td.Configure(map[string]any{ConfigTreeDiffLanguages: []string{"Python"}}))
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"e89c1d10fb31e32668ad905eb59dc44d7a4a021e",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	res, err := td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	commit, _ = test.Repository.CommitObject(plumbing.NewHash(
		"fbe766ffdc3f87f6affddc051c6f8b419beea6a2",
	))
	deps[core.DependencyCommit] = commit
	res, err = td.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 1)
	assert.Equal(t, "labours.py", changes[0].To.Name)

	// lowercase
	assert.NoError(t, td.Configure(map[string]any{ConfigTreeDiffLanguages: []string{"python"}}))
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 1)
	assert.Equal(t, "labours.py", changes[0].To.Name)
}

func TestTreeDiffFork(t *testing.T) {
	td1 := fixtureTreeDiff()
	td1.SkipFiles = append(td1.SkipFiles, "skip")
	clones := td1.Fork(1)
	assert.Len(t, clones, 1)
	td2 := clones[0].(*TreeDiff)
	assert.NotSame(t, td1, td2)
	assert.Equal(t, td1.SkipFiles, td2.SkipFiles)
	assert.Equal(t, td1.previousTree, td2.previousTree)
	td1.Merge([]core.PipelineItem{td2})
}

func TestTreeDiffCheckLanguage(t *testing.T) {
	td := fixtureTreeDiff()
	lang, err := td.checkLanguage(
		"version.go", plumbing.NewHash("975f35a1412b8ae79b5ba2558f71f41e707fd5a9"),
	)
	assert.NoError(t, err)
	assert.True(t, lang)
	td.Languages["go"] = true
	delete(td.Languages, allLanguages)
	lang, err = td.checkLanguage(
		"version.go", plumbing.NewHash("975f35a1412b8ae79b5ba2558f71f41e707fd5a9"),
	)
	assert.NoError(t, err)
	assert.True(t, lang)
}

func TestTreeDiffConsumeEnryFilter(t *testing.T) {
	diffs := object.Changes{&object.Change{
		From: object.ChangeEntry{Name: ""},
		To:   object.ChangeEntry{Name: "vendor/test.go"},
	}, &object.Change{
		From: object.ChangeEntry{Name: "vendor/test.go"},
		To:   object.ChangeEntry{Name: ""},
	}}
	td := fixtureTreeDiff()

	newDiffs := td.filterDiffs(diffs)
	assert.Len(t, newDiffs, 2)
	assert.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"whatever"},
	}))
	newDiffs = td.filterDiffs(diffs)
	assert.Empty(t, newDiffs)
}

func TestTreeDiffCheckLanguageEmpty(t *testing.T) {
	td := fixtureTreeDiff()
	td.Languages["python"] = true
	delete(td.Languages, allLanguages)
	lang, err := td.checkLanguage(
		"__init__.py", plumbing.NewHash("e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"),
	)
	assert.NoError(t, err)
	assert.True(t, lang)
}
