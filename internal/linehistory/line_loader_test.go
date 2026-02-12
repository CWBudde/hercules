package linehistory

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
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
	assert.Equal(t, "", opts[0].Default)
}

func TestLineHistoryLoaderConfigure(t *testing.T) {
	loader := &LineHistoryLoader{}
	logger := core.NewLogger()

	facts := map[string]interface{}{
		core.ConfigLogger: logger,
	}

	err := loader.Configure(facts)
	assert.NoError(t, err)
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
	facts := map[string]interface{}{}

	err := loader.Configure(facts)
	assert.NoError(t, err)
	assert.NotNil(t, loader.l)
}

func TestLineHistoryLoaderConfigureUpstream(t *testing.T) {
	loader := &LineHistoryLoader{}
	err := loader.ConfigureUpstream(map[string]interface{}{})
	assert.NoError(t, err)
}

func TestLineHistoryLoaderInitialize(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{1: {Name: "test"}}
	loader.nextCommit = 5

	err := loader.Initialize(nil)
	assert.NoError(t, err)
	assert.Empty(t, loader.files)
	assert.Equal(t, 0, loader.nextCommit)
	assert.NotNil(t, loader.l)
}

func TestLineHistoryLoaderConsumeEmpty(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.commits = []commitInfo{}
	loader.nextCommit = 0

	result, err := loader.Consume(map[string]interface{}{})
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// When no commits, should return AuthorMissing
	assert.Equal(t, int(core.AuthorMissing), result[identity.DependencyAuthor])
	assert.Equal(t, 0, result[items.DependencyTick])

	changes := result[DependencyLineHistory].(core.LineHistoryChanges)
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
	result, err := loader.Consume(map[string]interface{}{})
	assert.NoError(t, err)
	assert.Equal(t, 1, loader.nextCommit)
	assert.Equal(t, int(core.AuthorId(1)), result[identity.DependencyAuthor])
	assert.Equal(t, 10, result[items.DependencyTick])

	changes := result[DependencyLineHistory].(core.LineHistoryChanges)
	assert.Len(t, changes.Changes, 2)
	assert.Equal(t, core.FileId(1), changes.Changes[0].FileId)
	assert.Equal(t, 100, changes.Changes[0].Delta)

	// Second consume
	result, err = loader.Consume(map[string]interface{}{})
	assert.NoError(t, err)
	assert.Equal(t, 2, loader.nextCommit)
	assert.Equal(t, int(core.AuthorId(2)), result[identity.DependencyAuthor])
	assert.Equal(t, 20, result[items.DependencyTick])

	changes = result[DependencyLineHistory].(core.LineHistoryChanges)
	assert.Len(t, changes.Changes, 1)
	assert.Equal(t, -10, changes.Changes[0].Delta)

	// Third consume (past end)
	result, err = loader.Consume(map[string]interface{}{})
	assert.NoError(t, err)
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
		1: {Name: "file1.go"},
		2: {Name: "file2.go"},
		3: {Name: "dir/file3.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	assert.Equal(t, "", resolver.NameOf(0))
	assert.Equal(t, "file1.go", resolver.NameOf(1))
	assert.Equal(t, "file2.go", resolver.NameOf(2))
	assert.Equal(t, "dir/file3.go", resolver.NameOf(3))
	assert.Equal(t, "", resolver.NameOf(999))
}

func TestLoadedFileIdResolverNameOfNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	assert.Equal(t, "", resolver.NameOf(1))
}

func TestLoadedFileIdResolverMergedWith(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: "file1.go"},
		2: {Name: "file2.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	// Existing file
	id, name, present := resolver.MergedWith(1)
	assert.Equal(t, FileId(1), id)
	assert.Equal(t, "file1.go", name)
	assert.True(t, present)

	// Non-existing file
	id, name, present = resolver.MergedWith(999)
	assert.Equal(t, FileId(0), id)
	assert.Equal(t, "", name)
	assert.False(t, present)
}

func TestLoadedFileIdResolverMergedWithNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	id, name, present := resolver.MergedWith(1)
	assert.Equal(t, FileId(0), id)
	assert.Equal(t, "", name)
	assert.False(t, present)
}

func TestLoadedFileIdResolverForEachFile(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: "file1.go"},
		2: {Name: "file2.go"},
		3: {Name: "file3.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	visited := make(map[FileId]string)
	result := resolver.ForEachFile(func(id FileId, name string) {
		visited[id] = name
	})

	assert.True(t, result)
	assert.Len(t, visited, 3)
	assert.Equal(t, "file1.go", visited[1])
	assert.Equal(t, "file2.go", visited[2])
	assert.Equal(t, "file3.go", visited[3])
}

func TestLoadedFileIdResolverForEachFileNil(t *testing.T) {
	resolver := loadedFileIdResolver{analyser: nil}
	result := resolver.ForEachFile(func(id FileId, name string) {})
	assert.False(t, result)
}

func TestLoadedFileIdResolverScanFileNotImplemented(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.files = map[FileId]fileInfo{
		1: {Name: "file1.go"},
	}

	resolver := loadedFileIdResolver{analyser: loader}

	// ScanFile calls ForEach which panics "not implemented"
	assert.Panics(t, func() {
		resolver.ScanFile(1, func(line int, tick core.TickNumber, author core.AuthorId) {})
	})
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
	loader.authors = []string{"Alice", "Bob", "Charlie"}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, 3, resolver.MaxCount())
}

func TestAuthorResolverMaxCountNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	assert.Equal(t, 0, resolver.MaxCount())
}

func TestAuthorResolverCount(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{"Alice", "Bob"}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, 2, resolver.Count())
}

func TestAuthorResolverCountNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	assert.Equal(t, 0, resolver.Count())
}

func TestAuthorResolverPrivateNameOf(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{"Alice", "Bob"}

	resolver := authorResolver{identities: loader}
	assert.Equal(t, "Alice", resolver.PrivateNameOf(0))
	assert.Equal(t, "Bob", resolver.PrivateNameOf(1))
}

func TestAuthorResolverFriendlyNameOf(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{"Alice", "Bob", "Charlie"}

	resolver := authorResolver{identities: loader}

	assert.Equal(t, "Alice", resolver.FriendlyNameOf(0))
	assert.Equal(t, "Bob", resolver.FriendlyNameOf(1))
	assert.Equal(t, "Charlie", resolver.FriendlyNameOf(2))

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
	loader.authors = []string{"Alice", "Bob", "Charlie"}

	resolver := authorResolver{identities: loader}

	visited := make(map[core.AuthorId]string)
	result := resolver.ForEachIdentity(func(id core.AuthorId, name string) {
		visited[id] = name
	})

	assert.True(t, result)
	assert.Len(t, visited, 3)
	assert.Equal(t, "Alice", visited[0])
	assert.Equal(t, "Bob", visited[1])
	assert.Equal(t, "Charlie", visited[2])
}

func TestAuthorResolverForEachIdentityNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	result := resolver.ForEachIdentity(func(id core.AuthorId, name string) {})
	assert.False(t, result)
}

func TestAuthorResolverCopyNames(t *testing.T) {
	loader := &LineHistoryLoader{}
	loader.authors = []string{"Alice", "Bob"}

	resolver := authorResolver{identities: loader}
	copy := resolver.CopyNames(false)

	assert.Len(t, copy, 2)
	assert.Equal(t, "Alice", copy[0])
	assert.Equal(t, "Bob", copy[1])

	// Verify it's a copy, not the same slice
	copy[0] = "Modified"
	assert.Equal(t, "Alice", loader.authors[0])
}

func TestAuthorResolverCopyNamesNil(t *testing.T) {
	resolver := authorResolver{identities: nil}
	copy := resolver.CopyNames(false)
	assert.Nil(t, copy)
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
      1 -1 -1 0 10 100
      2 -1 -1 0 10 50
    bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb: |
      1 0 10 1 20 -10
      2 0 10 1 20 5
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
	assert.Equal(t, "file1.go", loader.files[1].Name)
	assert.Equal(t, "file2.go", loader.files[2].Name)
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
	assert.Equal(t, core.AuthorId(-1), change.PrevAuthor)
	assert.Equal(t, core.TickNumber(-1), change.PrevTick)
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
      1 -1 -1 0 10
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
      1 -1 -1 0 invalid 100
`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse")
}

func TestLineHistoryLoaderLoadChangesFromYamlInvalidYaml(t *testing.T) {
	yamlData := `invalid: yaml: data:`

	loader := &LineHistoryLoader{}
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlData)))

	err := loader.loadChangesFromYaml(decoder)
	require.Error(t, err)
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
      1 -1 -1 0 10 100
`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "history.yml")
	err := os.WriteFile(tmpFile, []byte(yamlData), 0o644)
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
      1 -1 -1 0 5 50
`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "history.yml")
	err := os.WriteFile(tmpFile, []byte(yamlData), 0o644)
	require.NoError(t, err)

	loader := &LineHistoryLoader{}
	facts := map[string]interface{}{
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

func TestFileInfoForEachPanics(t *testing.T) {
	fi := fileInfo{Name: "test.go"}
	assert.PanicsWithValue(t, "not implemented", func() {
		fi.ForEach(func(line int, value int) {})
	})
}

// testLogger is a minimal logger implementation for testing
type testLogger struct {
	criticalCalled bool
	lastMessage    string
}

func (l *testLogger) Critical(args ...interface{}) {
	l.criticalCalled = true
	if len(args) > 0 {
		l.lastMessage = args[0].(string)
	}
}

func (l *testLogger) Criticalf(format string, args ...interface{}) {
	l.criticalCalled = true
	l.lastMessage = format
}

func (l *testLogger) Info(...interface{})           {}
func (l *testLogger) Infof(string, ...interface{})  {}
func (l *testLogger) Warn(...interface{})           {}
func (l *testLogger) Warnf(string, ...interface{})  {}
func (l *testLogger) Error(...interface{})          {}
func (l *testLogger) Errorf(string, ...interface{}) {}
