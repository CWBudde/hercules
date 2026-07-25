package imports

import (
	"runtime"
	"testing"

	gitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/imports/lang"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureExtractor() *Extractor {
	ex := &Extractor{}
	err := ex.Initialize(test.Repository)
	if err != nil {
		panic(err)
	}
	return ex
}

func TestExtractorConfigureInitialize(t *testing.T) {
	ex := fixtureExtractor()
	assert.Equal(t, runtime.NumCPU(), ex.Goroutines)
	facts := map[string]any{}
	facts[ConfigImportsGoroutines] = 7
	facts[ConfigMaxFileSize] = 8
	require.NoError(t, ex.Configure(facts))
	assert.Equal(t, 7, ex.Goroutines)
	assert.Equal(t, 8, ex.MaxFileSize)
	facts[ConfigImportsGoroutines] = -1
	facts[ConfigMaxFileSize] = -8
	require.NoError(t, ex.Configure(facts))
	assert.Equal(t, runtime.NumCPU(), ex.Goroutines)
	assert.Equal(t, DefaultMaxFileSize, ex.MaxFileSize)
	assert.NotNil(t, ex.l)
}

func TestExtractorMetadata(t *testing.T) {
	ex := fixtureExtractor()
	assert.Equal(t, "Imports", ex.Name())
	assert.Len(t, ex.Provides(), 1)
	assert.Equal(t, DependencyImports, ex.Provides()[0])
	assert.Equal(t, []string{plumbing.DependencyTreeChanges, plumbing.DependencyBlobCache}, ex.Requires())
	opts := ex.ListConfigurationOptions()
	assert.Len(t, opts, 2)
	assert.Equal(t, ConfigImportsGoroutines, opts[0].Name)
	assert.Equal(t, runtime.NumCPU(), opts[0].Default)
	assert.Equal(t, ConfigMaxFileSize, opts[1].Name)
	assert.Equal(t, DefaultMaxFileSize, opts[1].Default)
}

func TestExtractorRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&Extractor{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "Imports", summoned[0].Name())
	summoned = core.Registry.Summon((&Extractor{}).Provides()[0])
	assert.Len(t, summoned, 1)
	assert.Equal(t, "Imports", summoned[0].Name())
}

func TestExtractorConsumeModification(t *testing.T) {
	commit, _ := test.Repository.CommitObject(gitplumbing.NewHash(
		"af2d8db70f287b52d2428d9887a69a10bc4d1f46",
	))
	changes := make(object.Changes, 1)
	treeFrom, _ := test.Repository.TreeObject(gitplumbing.NewHash(
		"80fe25955b8e725feee25c08ea5759d74f8b670d",
	))
	treeTo, _ := test.Repository.TreeObject(gitplumbing.NewHash(
		"63076fa0dfd93e94b6d2ef0fc8b1fdf9092f83c4",
	))
	changes[0] = &object.Change{From: object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeFrom,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: gitplumbing.NewHash("1cacfc1bf0f048eb2f31973750983ae5d8de647a"),
		},
	}, To: object.ChangeEntry{
		Name: testLaboursPath,
		Tree: treeTo,
		TreeEntry: object.TreeEntry{
			Name: testLaboursPath,
			Mode: 0o100644,
			Hash: gitplumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742"),
		},
	}}
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	deps[plumbing.DependencyTreeChanges] = changes
	cache := &plumbing.BlobCache{}
	require.NoError(t, cache.Initialize(test.Repository))
	blobs, err := cache.Consume(deps)
	require.NoError(t, err)
	deps[plumbing.DependencyBlobCache] = blobs[plumbing.DependencyBlobCache]
	result, err := fixtureExtractor().Consume(deps)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	exIface, exists := result[DependencyImports]
	assert.True(t, exists)
	ex := exIface.(map[gitplumbing.Hash]lang.File)
	assert.Len(t, ex, 1)
	file := ex[gitplumbing.NewHash("c872b8d2291a5224e2c9f6edd7f46039b96b4742")]
	assert.Equal(t, testLaboursPath, file.Path)
	assert.Equal(t, "Python", file.Lang)
	assert.Equal(t, []string{
		"argparse", "datetime", "matplotlib", "matplotlib.pyplot", "numpy",
		"pandas", "sys", "warnings",
	}, file.Imports)
}
