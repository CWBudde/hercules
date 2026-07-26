package linehistory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

func TestLineHistoryLoaderMeta(t *testing.T) {
	loader := &LineHistoryLoader{}
	assert.Equal(t, "LineHistoryLoader", loader.Name())

	provides := loader.Provides()
	assert.Len(t, provides, 3)
	assert.Contains(t, provides, DependencyLineHistory)
	assert.Contains(t, provides, items.DependencyTick)
	assert.Contains(t, provides, identity.DependencyAuthor)

	requires := loader.Requires()
	assert.Empty(t, requires)

	features := loader.Features()
	assert.Len(t, features, 1)
	assert.Equal(t, core.FeatureGitStub, features[0])
}

func TestLineHistoryLoaderListConfigurationOptions(t *testing.T) {
	loader := &LineHistoryLoader{}
	opts := loader.ListConfigurationOptions()

	assert.Len(t, opts, 1)
	assert.Equal(t, ConfigLinesLoadFrom, opts[0].Name)
	assert.Equal(t, "history-line-load", opts[0].Flag)
	assert.Equal(t, core.PathConfigurationOption, opts[0].Type)
	assert.Empty(t, opts[0].Default)
}

func TestLineHistoryLoaderConfigure(t *testing.T) {
	loader := &LineHistoryLoader{}
	logger := core.NewLogger()

	facts := map[string]any{
		core.ConfigLogger: logger,
	}

	err := loader.Configure(facts)
	require.NoError(t, err)
	assert.Equal(t, logger, loader.l)

	// Check that facts are populated
	assert.NotNil(t, facts[core.FactLineHistoryResolver])
	assert.NotNil(t, facts[core.FactIdentityResolver])
	assert.NotNil(t, facts[core.ConfigPipelineCommits])

	// Verify resolver types
	_, ok := facts[core.FactLineHistoryResolver].(loadedFileIdResolver)
	assert.True(t, ok)

	_, ok = facts[core.FactIdentityResolver].(authorResolver)
	assert.True(t, ok)
}

func TestLineHistoryLoaderConfigureWithoutLogger(t *testing.T) {
	loader := &LineHistoryLoader{}
	facts := map[string]any{}

	err := loader.Configure(facts)
	require.NoError(t, err)
	assert.NotNil(t, loader.l)
}

func TestLineHistoryLoaderConfigureUpstream(t *testing.T) {
	loader := &LineHistoryLoader{}
	err := loader.ConfigureUpstream(map[string]any{})
	require.NoError(t, err)
}

func TestLineHistoryLoaderInitialize(t *testing.T) {
	loader := &LineHistoryLoader{}
	logger := &testLogger{}
	loader.l = logger
	loader.files = map[FileId]fileInfo{1: {
		Name: "test",
		Lines: []lineRun{{
			Author: 0,
			Tick:   1,
			Count:  2,
		}},
	}}
	loader.nextCommit = 5

	err := loader.Initialize(nil)
	require.NoError(t, err)
	require.Contains(t, loader.files, FileId(1))
	assert.Equal(t, "test", loader.files[1].Name)
	assert.Equal(t, []lineRun{{Author: 0, Tick: 1, Count: 2}}, loader.files[1].Lines)
	assert.Equal(t, 0, loader.nextCommit)
	assert.Same(t, logger, loader.l)
}

func TestLineHistoryLoaderConsumeEmpty(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.commits = []commitInfo{}
	loader.nextCommit = 0

	result, err := loader.Consume(map[string]any{})
	require.NoError(t, err)
	assert.NotNil(t, result)

	// When no commits, should return AuthorMissing
	assert.Equal(t, int(core.AuthorMissing), result[identity.DependencyAuthor])
	assert.Equal(t, 0, result[items.DependencyTick])

	changes, ok := result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)
	assert.Empty(t, changes.Changes)
	assert.NotNil(t, changes.Resolver)
}

func TestLineHistoryLoaderConsumeWithCommits(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.commits = []commitInfo{
		{
			Hash:   plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Tick:   10,
			Author: 1,
			Changes: []core.LineHistoryChange{
				{FileId: 1, Delta: 100, CurrAuthor: 1, CurrTick: 10},
				{FileId: 2, Delta: 50, CurrAuthor: 1, CurrTick: 10},
			},
		},
		{
			Hash:   plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			Tick:   20,
			Author: 2,
			Changes: []core.LineHistoryChange{
				{FileId: 1, Delta: -10, PrevAuthor: 1, PrevTick: 10, CurrAuthor: 2, CurrTick: 20},
			},
		},
	}
	loader.nextCommit = 0

	// First consume
	result, err := loader.Consume(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, loader.nextCommit)
	assert.Equal(t, int(core.AuthorId(1)), result[identity.DependencyAuthor])
	assert.Equal(t, 10, result[items.DependencyTick])

	changes, ok := result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)
	assert.Len(t, changes.Changes, 2)
	assert.Equal(t, core.FileId(1), changes.Changes[0].FileId)
	assert.Equal(t, 100, changes.Changes[0].Delta)

	// Second consume
	result, err = loader.Consume(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 2, loader.nextCommit)
	assert.Equal(t, int(core.AuthorId(2)), result[identity.DependencyAuthor])
	assert.Equal(t, 20, result[items.DependencyTick])

	changes, ok = result[DependencyLineHistory].(core.LineHistoryChanges)
	require.True(t, ok)
	assert.Len(t, changes.Changes, 1)
	assert.Equal(t, -10, changes.Changes[0].Delta)

	// Third consume (past end)
	result, err = loader.Consume(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, int(core.AuthorMissing), result[identity.DependencyAuthor])
}

func TestLineHistoryLoaderFork(t *testing.T) {
	loader := &LineHistoryLoader{}
	forks := loader.Fork(3)

	assert.Len(t, forks, 3)
	for _, fork := range forks {
		assert.Equal(t, loader, fork)
	}
}

func TestLineHistoryLoaderMerge(t *testing.T) {
	loader := &LineHistoryLoader{}
	logger := &testLogger{}
	loader.l = logger

	// Merge should log critical error
	loader.Merge([]core.PipelineItem{})
	assert.True(t, logger.criticalCalled)
	assert.Equal(t, "cant be merged", logger.lastMessage)
}

func TestLoadedFileIdResolverNameOf(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: testFileOnePath},
		2: {Name: testFileTwoPath},
		3: {Name: "dir/file3.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	assert.Empty(t, resolver.NameOf(0))
	assert.Equal(t, testFileOnePath, resolver.NameOf(1))
	assert.Equal(t, testFileTwoPath, resolver.NameOf(2))
	assert.Equal(t, "dir/file3.go", resolver.NameOf(3))
	assert.Empty(t, resolver.NameOf(999))
}

func TestLoadedFileIdResolverNameOfNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	assert.Empty(t, resolver.NameOf(1))
}

func TestLoadedFileIdResolverMergedWith(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: testFileOnePath},
		2: {Name: testFileTwoPath},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	// Existing file
	id, name, present := resolver.MergedWith(1)
	assert.Equal(t, FileId(1), id)
	assert.Equal(t, testFileOnePath, name)
	assert.True(t, present)

	// Non-existing file
	id, name, present = resolver.MergedWith(999)
	assert.Equal(t, FileId(0), id)
	assert.Empty(t, name)
	assert.False(t, present)
}

func TestLoadedFileIdResolverMergedWithNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	id, name, present := resolver.MergedWith(1)
	assert.Equal(t, FileId(0), id)
	assert.Empty(t, name)
	assert.False(t, present)
}

func TestLoadedFileIdResolverForEachFile(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: testFileOnePath},
		2: {Name: testFileTwoPath},
		3: {Name: "file3.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	visited := make(map[FileId]string)
	result := resolver.ForEachFile(func(id FileId, name string) {
		visited[id] = name
	})

	assert.True(t, result)
	assert.Len(t, visited, 3)
	assert.Equal(t, testFileOnePath, visited[1])
	assert.Equal(t, testFileTwoPath, visited[2])
	assert.Equal(t, "file3.go", visited[3])
}

func TestLoadedFileIdResolverForEachFileNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	result := resolver.ForEachFile(func(id FileId, name string) {})
	assert.False(t, result)
}

func TestLoadedFileIdResolverScanFile(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {
			Name: testFileOnePath,
			Lines: []lineRun{
				{Author: 1, Tick: 10, Count: 2},
				{Author: 2, Tick: 20, Count: 1},
			},
		},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	type visitedLine struct {
		line   int
		tick   core.TickNumber
		author core.AuthorId
	}
	var visited []visitedLine
	assert.True(t, resolver.ScanFile(1, func(line int, tick core.TickNumber, author core.AuthorId) {
		visited = append(visited, visitedLine{line: line, tick: tick, author: author})
	}))
	assert.Equal(t, []visitedLine{
		{line: 0, tick: 10, author: 1},
		{line: 1, tick: 10, author: 1},
		{line: 2, tick: 20, author: 2},
	}, visited)
}

func TestLoadedFileIdResolverForEachFileIsSorted(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		3: {Name: "three"},
		1: {Name: "one"},
		2: {Name: "two"},
	}

	var ids []FileId
	resolver := loadedFileIdResolver{analyser: loader}
	assert.True(t, resolver.ForEachFile(func(id FileId, _ string) {
		ids = append(ids, id)
	}))
	assert.Equal(t, []FileId{1, 2, 3}, ids)
}

func TestLoadedFileIdResolverScanFileNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	result := resolver.ScanFile(1, func(line int, tick core.TickNumber, author core.AuthorId) {})
	assert.False(t, result)
}

func TestLoadedFileIdResolverScanFileNotFound(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{}

	resolver := loadedFileIdResolver{analyser: loader}
	result := resolver.ScanFile(1, func(line int, tick core.TickNumber, author core.AuthorId) {})
	assert.False(t, result)
}

func TestAuthorResolverMaxCount(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob, testAuthorCharlie}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, 3, resolver.MaxCount())
}

func TestAuthorResolverMaxCountNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	assert.Equal(t, 0, resolver.MaxCount())
}

func TestAuthorResolverCount(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, 2, resolver.Count())
}

func TestAuthorResolverCountNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	assert.Equal(t, 0, resolver.Count())
}

func TestAuthorResolverPrivateNameOf(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, testAuthorAlice, resolver.PrivateNameOf(0))
	assert.Equal(t, testAuthorBob, resolver.PrivateNameOf(1))
}

func TestAuthorResolverFriendlyNameOf(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob, testAuthorCharlie}

	resolver := authorResolver{identities: loader}

	assert.Equal(t, testAuthorAlice, resolver.FriendlyNameOf(0))
	assert.Equal(t, testAuthorBob, resolver.FriendlyNameOf(1))
	assert.Equal(t, testAuthorCharlie, resolver.FriendlyNameOf(2))

	// Out of bounds
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(999))
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(-1))
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(core.AuthorMissing))
}

func TestAuthorResolverFriendlyNameOfNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(0))
}

func TestAuthorResolverForEachIdentity(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob, testAuthorCharlie}

	resolver := authorResolver{identities: loader}

	visited := make(map[core.AuthorId]string)
	result := resolver.ForEachIdentity(func(id core.AuthorId, name string) {
		visited[id] = name
	})

	assert.True(t, result)
	assert.Len(t, visited, 3)
	assert.Equal(t, testAuthorAlice, visited[0])
	assert.Equal(t, testAuthorBob, visited[1])
	assert.Equal(t, testAuthorCharlie, visited[2])
}

func TestAuthorResolverForEachIdentityNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	result := resolver.ForEachIdentity(func(id core.AuthorId, name string) {})
	assert.False(t, result)
}

func TestAuthorResolverCopyNames(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{testAuthorAlice, testAuthorBob}

	resolver := authorResolver{identities: loader}
	names := resolver.CopyNames(false)

	assert.Len(t, names, 2)
	assert.Equal(t, testAuthorAlice, names[0])
	assert.Equal(t, testAuthorBob, names[1])

	// Verify it's a copy, not the same slice
	names[0] = "Modified"
	assert.Equal(t, testAuthorAlice, loader.authors[0])
}

func TestAuthorResolverCopyNamesNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	names := resolver.CopyNames(false)
	assert.Nil(t, names)
}

func TestLineHistoryLoaderBuildCommits(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.commits = []commitInfo{
		{
			Hash:   plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Tick:   10,
			Author: 1,
		},
		{
			Hash:   plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			Tick:   20,
			Author: 2,
		},
		{
			Hash:   plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
			Tick:   30,
			Author: 1,
		},
	}

	commits := loader.buildCommits()

	assert.Len(t, commits, 3)

	// First commit has no parents
	assert.Equal(t, plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), commits[0].Hash)
	assert.Empty(t, commits[0].ParentHashes)

	// Second commit has first as parent
	assert.Equal(t, plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), commits[1].Hash)
	assert.Len(t, commits[1].ParentHashes, 1)
	assert.Equal(t, plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), commits[1].ParentHashes[0])

	// Third commit has second as parent
	assert.Equal(t, plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"), commits[2].Hash)
	assert.Len(t, commits[2].ParentHashes, 1)
	assert.Equal(t, plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), commits[2].ParentHashes[0])
}

func TestLineHistoryLoaderLoadChangesFromYaml(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice <alice@example.com>"
    - "Bob <bob@example.com>"
  file_sequence:
    1: "file1.go"
    2: "file2.go"
    3: "dir/file3.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 10 0 10 100
      2 0 10 0 10 50
    bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb: |
      1 0 10 1 20 -10
      2 1 20 1 20 5
`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.NoError(t, err)

	// Check authors
	assert.Len(t, loader.authors, 2)
	assert.Equal(t, "Alice <alice@example.com>", loader.authors[0])
	assert.Equal(t, "Bob <bob@example.com>", loader.authors[1])

	// Check files
	assert.Len(t, loader.files, 3)
	assert.Equal(t, testFileOnePath, loader.files[1].Name)
	assert.Equal(t, testFileTwoPath, loader.files[2].Name)
	assert.Equal(t, "dir/file3.go", loader.files[3].Name)

	// Check commits
	assert.Len(t, loader.commits, 2)

	// First commit
	assert.Equal(t, plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), loader.commits[0].Hash)
	assert.Equal(t, core.TickNumber(10), loader.commits[0].Tick)
	assert.Equal(t, core.AuthorId(0), loader.commits[0].Author)
	assert.Len(t, loader.commits[0].Changes, 2)

	change := loader.commits[0].Changes[0]
	assert.Equal(t, core.FileId(1), change.FileId)
	assert.Equal(t, core.AuthorId(0), change.PrevAuthor)
	assert.Equal(t, core.TickNumber(10), change.PrevTick)
	assert.Equal(t, core.AuthorId(0), change.CurrAuthor)
	assert.Equal(t, core.TickNumber(10), change.CurrTick)
	assert.Equal(t, 100, change.Delta)

	// Second commit
	assert.Equal(t, plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), loader.commits[1].Hash)
	assert.Equal(t, core.TickNumber(20), loader.commits[1].Tick)
	assert.Equal(t, core.AuthorId(1), loader.commits[1].Author)
	assert.Len(t, loader.commits[1].Changes, 2)

	change = loader.commits[1].Changes[0]
	assert.Equal(t, core.FileId(1), change.FileId)
	assert.Equal(t, core.AuthorId(0), change.PrevAuthor)
	assert.Equal(t, core.TickNumber(10), change.PrevTick)
	assert.Equal(t, core.AuthorId(1), change.CurrAuthor)
	assert.Equal(t, core.TickNumber(20), change.CurrTick)
	assert.Equal(t, -10, change.Delta)

	resolver := loadedFileIdResolver{analyser: loader}
	ownership := map[lineOwner]int{}
	require.True(t, resolver.ScanFile(1, func(_ int, tick core.TickNumber, author core.AuthorId) {
		ownership[lineOwner{Author: author, Tick: tick}]++
	}))
	assert.Equal(t, map[lineOwner]int{{Author: 0, Tick: 10}: 90}, ownership)

	ownership = map[lineOwner]int{}
	require.True(t, resolver.ScanFile(2, func(_ int, tick core.TickNumber, author core.AuthorId) {
		ownership[lineOwner{Author: author, Tick: tick}]++
	}))
	assert.Equal(t, map[lineOwner]int{
		{Author: 0, Tick: 10}: 50,
		{Author: 1, Tick: 20}: 5,
	}, ownership)
}

func TestLineHistoryLoaderLoadedResolversSurviveInitialize(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice"
    - "Bob"
  file_sequence:
    1: "main.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 1 0 1 3
    bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb: |
      1 0 1 1 2 -2
      1 1 2 1 2 1
`

	logger := &testLogger{}
	loader := &LineHistoryLoader{}
	facts := map[string]any{
		core.ConfigLogger:   logger,
		ConfigLinesLoadFrom: writeLineHistoryFixture(t, yamlData),
	}
	require.NoError(t, loader.Configure(facts))
	require.NoError(t, loader.Initialize(nil))

	assert.Same(t, logger, loader.l)
	resolver, ok := facts[core.FactLineHistoryResolver].(core.FileIdResolver)
	require.True(t, ok)
	assert.Equal(t, "main.go", resolver.NameOf(1))

	var files []FileId
	require.True(t, resolver.ForEachFile(func(id FileId, name string) {
		files = append(files, id)
		assert.Equal(t, "main.go", name)
	}))
	assert.Equal(t, []FileId{1}, files)

	var owners []lineOwner
	require.True(t, resolver.ScanFile(1, func(line int, tick core.TickNumber, author core.AuthorId) {
		assert.Equal(t, len(owners), line)
		owners = append(owners, lineOwner{Author: author, Tick: tick})
	}))
	assert.Equal(t, []lineOwner{
		{Author: 0, Tick: 1},
		{Author: 1, Tick: 2},
	}, owners)
}

func TestLineHistoryLoaderRejectsInvalidRangesAndState(t *testing.T) {
	tests := map[string]struct {
		yamlData string
		target   error
	}{
		"invalid file ID": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {0: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "0 0 1 0 1 1"
`, target: ErrLineHistoryRange},
		"invalid author": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "1 1 1 1 1 1"
`, target: ErrLineHistoryRange},
		"invalid tick": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "1 0 16383 0 16383 1"
`, target: ErrLineHistoryRange},
		"state underflow": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "1 0 1 0 1 -1"
`, target: ErrLineHistoryState},
		"truncated change": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "1 0 1"
`, target: ErrLineHistoryMalformed},
		"second document": {yamlData: `
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: "1 0 1 0 1 1"
---
LineDumper: {}
`, target: ErrLineHistoryMalformed},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			loader := &LineHistoryLoader{}
			err := loader.loadChangesFromYaml(yaml.NewDecoder(strings.NewReader(test.yamlData)))
			require.Error(t, err)
			require.ErrorIs(t, err, test.target)
		})
	}
}

func TestLineHistoryLoaderFailedLoadDoesNotEraseMetadata(t *testing.T) {
	loader := &LineHistoryLoader{
		authors: []string{"preserved"},
		files:   map[FileId]fileInfo{7: {Name: "preserved.go"}},
		commits: []commitInfo{{
			Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		}},
	}

	err := loader.loadChangesFromYaml(yaml.NewDecoder(strings.NewReader(`
LineDumper:
  author_sequence: ["Alice"]
  file_sequence: {1: "main.go"}
  commits:
    invalid: "1 0 1 0 1 1"
`)))
	require.Error(t, err)
	assert.Equal(t, []string{"preserved"}, loader.authors)
	assert.Equal(t, "preserved.go", loader.files[7].Name)
	require.Len(t, loader.commits, 1)
	assert.Equal(t, plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), loader.commits[0].Hash)
}

func TestLineHistoryLoaderLoadChangesFromYamlInvalidFieldCount(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice"
  file_sequence:
    1: "file1.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 10 0 10
`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected number of fields")
}

func TestLineHistoryLoaderLoadChangesFromYamlInvalidNumber(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice"
  file_sequence:
    1: "file1.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 10 0 invalid 100
`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLineHistoryMalformed)
	assert.Contains(t, err.Error(), "unable to parse")
}

func TestLineHistoryLoaderLoadChangesFromYamlInvalidYaml(t *testing.T) {
	yamlData := `invalid: yaml: data:`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLineHistoryMalformed)
}

func TestLineHistoryLoaderLoadChangesFrom(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice"
  file_sequence:
    1: "file1.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 10 0 10 100
`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "history.yml")
	err := os.WriteFile(tmpFile, []byte(yamlData), 0o600)
	require.NoError(t, err)

	loader := &LineHistoryLoader{}
	err = loader.loadChangesFrom(tmpFile)
	require.NoError(t, err)

	assert.Len(t, loader.authors, 1)
	assert.Len(t, loader.files, 1)
	assert.Len(t, loader.commits, 1)
}

func TestLineHistoryLoaderLoadChangesFromNonExistent(t *testing.T) {
	loader := &LineHistoryLoader{}
	err := loader.loadChangesFrom("/nonexistent/file.yml")
	require.Error(t, err)
}

func TestLineHistoryLoaderConfigureWithLoadFrom(t *testing.T) {
	yamlData := `
LineDumper:
  author_sequence:
    - "Alice"
    - "Bob"
  file_sequence:
    1: "main.go"
  commits:
    aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: |
      1 0 5 0 5 50
`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "history.yml")
	err := os.WriteFile(tmpFile, []byte(yamlData), 0o600)
	require.NoError(t, err)

	loader := &LineHistoryLoader{}
	facts := map[string]any{
		ConfigLinesLoadFrom: tmpFile,
	}

	err = loader.Configure(facts)
	require.NoError(t, err)

	// Verify data was loaded
	assert.Len(t, loader.authors, 2)
	assert.Len(t, loader.files, 1)
	assert.Len(t, loader.commits, 1)

	// Verify commits were built
	commits, ok := facts[core.ConfigPipelineCommits]
	assert.True(t, ok)
	assert.Len(t, commits, 1)
}

func TestLineHistoryLoaderRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&LineHistoryLoader{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "LineHistoryLoader", summoned[0].Name())
}

func writeLineHistoryFixture(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "history.yml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

	return path
}

func TestFileInfoForEach(t *testing.T) {
	fi := fileInfo{Name: "test.go", Lines: []lineRun{{Author: 2, Tick: 3, Count: 2}}}

	var lines []int
	fi.ForEach(func(line int, tick core.TickNumber, author core.AuthorId) {
		lines = append(lines, line)
		assert.Equal(t, core.TickNumber(3), tick)
		assert.Equal(t, core.AuthorId(2), author)
	})
	assert.Equal(t, []int{0, 1}, lines)
}

// testLogger is a minimal logger implementation for testing.
type testLogger struct {
	criticalCalled bool
	lastMessage    string
}

func (l *testLogger) Critical(args ...any) {
	l.criticalCalled = true
	if len(args) > 0 {
		l.lastMessage = args[0].(string)
	}
}

func (l *testLogger) Criticalf(format string, args ...any) {
	l.criticalCalled = true
	l.lastMessage = format
}

func (l *testLogger) Info(...any)           {}
func (l *testLogger) Infof(string, ...any)  {}
func (l *testLogger) Warn(...any)           {}
func (l *testLogger) Warnf(string, ...any)  {}
func (l *testLogger) Error(...any)          {}
func (l *testLogger) Errorf(string, ...any) {}
