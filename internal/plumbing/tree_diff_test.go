package plumbing

import (
	"regexp"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureTreeDiff() *TreeDiff {
	td := TreeDiff{}
	err := td.Configure(nil)
	if err != nil {
		panic(err)
	}
	err = td.Initialize(test.Repository)
	if err != nil {
		panic(err)
	}
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
	require.NoError(t, td.Configure(map[string]any{
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

func TestTreeDiffConfigureRejectsInvalidRegexp(t *testing.T) {
	td := fixtureTreeDiff()
	original := regexp.MustCompile("^safe/")
	td.NameFilter = original

	err := td.Configure(map[string]any{
		ConfigTreeDiffFilterRegexp: "[",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidTreeDiffConfigValue)
	assert.Contains(t, err.Error(), ConfigTreeDiffFilterRegexp)
	assert.Same(t, original, td.NameFilter)
}

func FuzzTreeDiffConfigureRegexp(f *testing.F) {
	f.Add("^src/.*\\.go$")
	f.Add("[")
	f.Add("")

	f.Fuzz(func(t *testing.T, expression string) {
		td := &TreeDiff{}
		err := td.Configure(map[string]any{
			ConfigTreeDiffFilterRegexp: expression,
		})
		if err == nil && td.NameFilter == nil {
			t.Fatal("valid regular expression did not configure a filter")
		}
	})
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
	require.NoError(t, err)
	assert.Len(t, res, 1)
	changes := res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 12)
	baseline := map[string]merkletrie.Action{
		testAnalyserPath:            merkletrie.Delete,
		testHerculesMainPath:        merkletrie.Modify,
		"blob_cache.go":             merkletrie.Insert,
		testBurndownPath:            merkletrie.Insert,
		"day.go":                    merkletrie.Insert,
		"dummies.go":                merkletrie.Insert,
		"identity.go":               merkletrie.Insert,
		testPipelinePath:            merkletrie.Insert,
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
	require.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"vendor/"},
	}))
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
	require.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffFilterRegexp: ".*go",
	}))
	res, err = td.Consume(deps)
	require.NoError(t, err)
	assert.Len(t, res, 1)
	changes = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 27)
}

func TestTreeDiffConsumeLanguageFilterFirst(t *testing.T) {
	td := fixtureTreeDiff()
	require.NoError(t, td.Configure(map[string]any{ConfigTreeDiffLanguages: []string{"Go"}}))
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
	assert.Equal(t, testAnalyserPath, changes[0].To.Name)
	assert.Equal(t, testHerculesMainPath, changes[1].To.Name)
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
	assert.Equal(t, testLaboursPath, changes[0].To.Name)

	// lowercase
	assert.NoError(t, td.Configure(map[string]any{ConfigTreeDiffLanguages: []string{"python"}}))
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	changes = res[DependencyTreeChanges].(object.Changes)
	assert.Len(t, changes, 1)
	assert.Equal(t, testLaboursPath, changes[0].To.Name)
}

func TestTreeDiffCrossBoundaryRenames(t *testing.T) {
	goHash := plumbing.NewHash("975f35a1412b8ae79b5ba2558f71f41e707fd5a9")
	pythonHash := plumbing.NewHash("e69de29bb2d1d6434b8b29ae775ad8c2e48c5391")
	filters := []struct {
		name      string
		configure func(*TreeDiff)
		inside1   object.ChangeEntry
		inside2   object.ChangeEntry
		outside   object.ChangeEntry
	}{
		{
			name: "blacklisted prefix",
			configure: func(td *TreeDiff) {
				td.SkipFiles = []string{"ignored/"}
			},
			inside1: treeDiffEntry("src/alpha.go", goHash),
			inside2: treeDiffEntry("src/beta.go", goHash),
			outside: treeDiffEntry("ignored/alpha.go", goHash),
		},
		{
			name: "vendored path",
			configure: func(td *TreeDiff) {
				td.SkipFiles = []string{"unrelated/"}
			},
			inside1: treeDiffEntry("src/alpha.go", goHash),
			inside2: treeDiffEntry("src/beta.go", goHash),
			outside: treeDiffEntry("vendor/example/alpha.go", goHash),
		},
		{
			name: "regexp whitelist",
			configure: func(td *TreeDiff) {
				td.NameFilter = regexp.MustCompile(`^src/`)
			},
			inside1: treeDiffEntry("src/alpha.go", goHash),
			inside2: treeDiffEntry("src/beta.go", goHash),
			outside: treeDiffEntry("test/alpha.go", goHash),
		},
		{
			name: "language",
			configure: func(td *TreeDiff) {
				td.Languages = map[string]bool{"go": true}
			},
			inside1: treeDiffEntry("alpha.go", goHash),
			inside2: treeDiffEntry("beta.go", goHash),
			outside: treeDiffEntry("alpha.py", pythonHash),
		},
	}

	for _, filter := range filters {
		t.Run(filter.name, func(t *testing.T) {
			td := fixtureTreeDiff()
			filter.configure(td)

			assertTreeDiffTransition(
				t, td, filter.inside1, filter.outside, true, false, merkletrie.Delete,
			)
			assertTreeDiffTransition(
				t, td, filter.outside, filter.inside1, false, true, merkletrie.Insert,
			)
			assertTreeDiffTransition(
				t, td, filter.inside1, filter.inside2, true, true, merkletrie.Modify,
			)
			assertTreeDiffTransition(
				t, td, filter.outside, filter.outside, false, false, merkletrie.Action(0),
			)
		})
	}
}

func TestTreeDiffReturnsLanguageDetectionErrors(t *testing.T) {
	td := fixtureTreeDiff()
	td.Languages = map[string]bool{"go": true}
	valid := treeDiffEntry(
		"valid.go",
		plumbing.NewHash("975f35a1412b8ae79b5ba2558f71f41e707fd5a9"),
	)
	missing := treeDiffEntry(
		"missing.go",
		plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
	)

	tests := map[string]*object.Change{
		"old entry": {From: missing, To: valid},
		"new entry": {From: valid, To: missing},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			filtered, err := td.filterDiffs(object.Changes{change})
			assert.Nil(t, filtered)
			assert.ErrorContains(t, err, "detect language")
			assert.ErrorContains(t, err, "missing.go")
		})
	}
}

func TestTreeDiffFilterErrorDoesNotAdvanceCommitState(t *testing.T) {
	td := fixtureTreeDiff()
	td.Languages = map[string]bool{"go": true}

	emptyRepository, err := git.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)
	td.repository = emptyRepository

	commit, err := test.Repository.CommitObject(plumbing.NewHash(
		"fbe766ffdc3f87f6affddc051c6f8b419beea6a2",
	))
	require.NoError(t, err)
	result, err := td.Consume(map[string]any{core.DependencyCommit: commit})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "detect language")
	assert.Nil(t, td.previousTree)
	assert.Equal(t, plumbing.ZeroHash, td.previousCommit)

	td.repository = test.Repository
	result, err = td.Consume(map[string]any{core.DependencyCommit: commit})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, td.previousTree)
	assert.Equal(t, commit.Hash, td.previousCommit)
}

func TestTreeDiffInitialTreeUsesPathAndNameFilters(t *testing.T) {
	td := fixtureTreeDiff()
	require.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"vendor/"},
		ConfigTreeDiffFilterRegexp:        `^doc\.go$`,
		ConfigTreeDiffLanguages:           []string{"go"},
	}))
	commit, err := test.Repository.CommitObject(plumbing.NewHash(
		"fbe766ffdc3f87f6affddc051c6f8b419beea6a2",
	))
	require.NoError(t, err)

	result, err := td.Consume(map[string]any{core.DependencyCommit: commit})
	require.NoError(t, err)
	changes := result[DependencyTreeChanges].(object.Changes)
	require.Len(t, changes, 1)
	assert.Equal(t, "doc.go", changes[0].To.Name)
}

func TestTreeDiffVendorFilterWithEmptyPrefixList(t *testing.T) {
	td := fixtureTreeDiff()
	require.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{},
	}))

	vendorEntry := treeDiffEntry(
		"vendor/example/alpha.go",
		plumbing.NewHash("975f35a1412b8ae79b5ba2558f71f41e707fd5a9"),
	)
	filtered, err := td.filterDiffs(object.Changes{{To: vendorEntry}})
	require.NoError(t, err)
	assert.Empty(t, filtered)
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
	require.NoError(t, err)
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

	newDiffs, err := td.filterDiffs(diffs)
	require.NoError(t, err)
	assert.Len(t, newDiffs, 2)
	assert.NoError(t, td.Configure(map[string]any{
		ConfigTreeDiffEnableBlacklist:     true,
		ConfigTreeDiffBlacklistedPrefixes: []string{"whatever"},
	}))
	newDiffs, err = td.filterDiffs(diffs)
	require.NoError(t, err)
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

func treeDiffEntry(name string, hash plumbing.Hash) object.ChangeEntry {
	return object.ChangeEntry{
		Name: name,
		TreeEntry: object.TreeEntry{
			Name: name,
			Hash: hash,
		},
	}
}

func assertTreeDiffTransition(
	t *testing.T,
	td *TreeDiff,
	from, to object.ChangeEntry,
	fromIncluded, toIncluded bool,
	expectedAction merkletrie.Action,
) {
	t.Helper()

	state := map[string]bool{}
	if fromIncluded {
		state[from.Name] = true
	}

	filtered, err := td.filterDiffs(object.Changes{{From: from, To: to}})
	require.NoError(t, err)
	if !fromIncluded && !toIncluded {
		assert.Empty(t, filtered)
		assert.Empty(t, state)

		return
	}

	require.Len(t, filtered, 1)
	action, err := filtered[0].Action()
	require.NoError(t, err)
	assert.Equal(t, expectedAction, action)

	switch action {
	case merkletrie.Delete:
		delete(state, filtered[0].From.Name)
	case merkletrie.Insert:
		state[filtered[0].To.Name] = true
	case merkletrie.Modify:
		delete(state, filtered[0].From.Name)
		state[filtered[0].To.Name] = true
	}

	expectedState := map[string]bool{}
	if toIncluded {
		expectedState[to.Name] = true
	}

	assert.Equal(t, expectedState, state)
}
