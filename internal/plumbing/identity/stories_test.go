package identity

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureStoryDetector() *StoryDetector {
	sd := &StoryDetector{
		MergeHashDict: make(map[plumbing.Hash]int),
		MergeNames:    []string{},
	}
	_ = sd.Initialize(test.Repository)
	return sd
}

func TestStoryDetectorMeta(t *testing.T) {
	sd := fixtureStoryDetector()

	assert.Equal(t, "StoryDetector", sd.Name())
	assert.Empty(t, sd.Requires())
	assert.Equal(t, []string{DependencyAuthor}, sd.Provides())
	assert.Equal(t, []string{core.FeatureMergeTracks, core.FeatureGitCommits}, sd.Features())
}

func TestStoryDetectorListConfigurationOptions(t *testing.T) {
	sd := fixtureStoryDetector()

	opts := sd.ListConfigurationOptions()
	assert.Len(t, opts, 1)
	assert.Equal(t, ConfigStoryDetectorMergeDictPath, opts[0].Name)
	assert.Equal(t, "story-dict", opts[0].Flag)
	assert.Equal(t, core.PathConfigurationOption, opts[0].Type)
	assert.Equal(t, "", opts[0].Default)
}

func TestStoryDetectorConfigureWithMergeDict(t *testing.T) {
	sd := &StoryDetector{}

	// Test with explicit merge dict
	mergeDict := map[plumbing.Hash]string{
		plumbing.NewHash("1111111111111111111111111111111111111111"): "Story A",
		plumbing.NewHash("2222222222222222222222222222222222222222"): "Story B",
		plumbing.NewHash("3333333333333333333333333333333333333333"): "Story A", // Same name
	}

	facts := map[string]interface{}{
		FactStoryDetectorMergeDict: mergeDict,
		core.ConfigLogger:          core.NewLogger(),
	}

	err := sd.Configure(facts)
	require.NoError(t, err)

	// Should have 2 unique names
	assert.Len(t, sd.MergeNames, 2)
	assert.Len(t, sd.MergeHashDict, 3)
	assert.Equal(t, 2, sd.mergeNameCount)

	// Check that resolver was added to facts
	resolver, ok := facts[core.FactIdentityResolver].(core.IdentityResolver)
	require.True(t, ok)
	assert.NotNil(t, resolver)
}

func TestStoryDetectorConfigureWithMergeCount(t *testing.T) {
	sd := &StoryDetector{}

	facts := map[string]interface{}{
		core.FactMergeHashCount: 10,
		core.ConfigLogger:       core.NewLogger(),
	}

	err := sd.Configure(facts)
	require.NoError(t, err)

	assert.NotNil(t, sd.MergeHashDict)
	assert.Equal(t, 10, sd.mergeNameCount)
	assert.True(t, sd.expandMergeDict)
}

func TestStoryDetectorConfigureWithDictPath(t *testing.T) {
	sd := &StoryDetector{}

	// Create temp file with merge dict
	tmpf, err := ioutil.TempFile("", "hercules-story-test-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	content := `1111111111111111111111111111111111111111|2222222222222222222222222222222222222222|Story One
3333333333333333333333333333333333333333|Story Two
4444444444444444444444444444444444444444`

	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	facts := map[string]interface{}{
		ConfigStoryDetectorMergeDictPath: tmpf.Name(),
		core.ConfigLogger:                core.NewLogger(),
	}

	err = sd.Configure(facts)
	require.NoError(t, err)

	assert.Len(t, sd.MergeNames, 3)
	assert.Equal(t, "Story One", sd.MergeNames[0])
	assert.Equal(t, "Story Two", sd.MergeNames[1])
	assert.Equal(t, "Merge #2", sd.MergeNames[2]) // Default name
	assert.Len(t, sd.MergeHashDict, 4)
}

func TestStoryDetectorConfigureError(t *testing.T) {
	sd := &StoryDetector{}

	// No merge dict or count provided
	facts := map[string]interface{}{
		core.ConfigLogger: core.NewLogger(),
	}

	err := sd.Configure(facts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "merge tracks are not available")
}

func TestStoryDetectorConfigureInvalidPath(t *testing.T) {
	sd := &StoryDetector{}

	facts := map[string]interface{}{
		ConfigStoryDetectorMergeDictPath: "/nonexistent/path/to/dict.txt",
		core.ConfigLogger:                core.NewLogger(),
	}

	err := sd.Configure(facts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load")
}

func TestStoryDetectorConfigureUpstream(t *testing.T) {
	sd := &StoryDetector{}
	err := sd.ConfigureUpstream(map[string]interface{}{})
	assert.NoError(t, err)
}

func TestStoryDetectorInitialize(t *testing.T) {
	sd := &StoryDetector{}
	err := sd.Initialize(test.Repository)
	assert.NoError(t, err)
	assert.NotNil(t, sd.l)
}

func TestStoryDetectorConsume(t *testing.T) {
	sd := &StoryDetector{
		MergeHashDict:   make(map[plumbing.Hash]int),
		MergeNames:      []string{"Story 0"},
		mergeNameCount:  1,
		expandMergeDict: false,
		l:               core.NewLogger(),
	}

	hash := plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	sd.MergeHashDict[hash] = 0

	commit := &object.Commit{
		Hash: hash,
		Author: object.Signature{
			Name:  "Author",
			Email: "author@example.com",
			When:  time.Now(),
		},
	}

	deps := map[string]interface{}{
		core.DependencyNextMerge: commit,
	}

	result, err := sd.Consume(deps)
	require.NoError(t, err)
	require.NotNil(t, result)

	authorID, ok := result[DependencyAuthor].(int)
	require.True(t, ok)
	assert.Equal(t, 0, authorID)
}

func TestStoryDetectorConsumeNilCommit(t *testing.T) {
	sd := &StoryDetector{
		MergeHashDict:  make(map[plumbing.Hash]int),
		MergeNames:     []string{},
		mergeNameCount: 10,
		l:              core.NewLogger(),
	}

	deps := map[string]interface{}{
		core.DependencyNextMerge: (*object.Commit)(nil),
	}

	result, err := sd.Consume(deps)
	require.NoError(t, err)
	require.NotNil(t, result)

	authorID, ok := result[DependencyAuthor].(int)
	require.True(t, ok)
	assert.Equal(t, int(core.AuthorMissing), authorID)
}

func TestStoryDetectorConsumeExpandDict(t *testing.T) {
	sd := &StoryDetector{
		MergeHashDict:   make(map[plumbing.Hash]int),
		MergeNames:      []string{},
		mergeNameCount:  10,
		expandMergeDict: true,
		l:               core.NewLogger(),
	}

	hash := plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	commit := &object.Commit{
		Hash: hash,
		Author: object.Signature{
			Name:  "New Author",
			Email: "new@example.com",
			When:  time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		},
	}

	deps := map[string]interface{}{
		core.DependencyNextMerge: commit,
	}

	result, err := sd.Consume(deps)
	require.NoError(t, err)
	require.NotNil(t, result)

	authorID, ok := result[DependencyAuthor].(int)
	require.True(t, ok)
	assert.Equal(t, 0, authorID)

	// Check that merge was added
	assert.Len(t, sd.MergeNames, 1)
	assert.Contains(t, sd.MergeNames[0], "Merge #0")
	assert.Contains(t, sd.MergeNames[0], "bbbbbbb")
	assert.Contains(t, sd.MergeHashDict, hash)
}

func TestStoryDetectorConsumeExceedLimit(t *testing.T) {
	sd := &StoryDetector{
		MergeHashDict:   make(map[plumbing.Hash]int),
		MergeNames:      []string{"Story 0", "Story 1"},
		mergeNameCount:  2,
		expandMergeDict: true,
		l:               core.NewLogger(),
	}

	hash := plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc")
	commit := &object.Commit{
		Hash: hash,
		Author: object.Signature{
			Name: "Author",
			When: time.Now(),
		},
	}

	deps := map[string]interface{}{
		core.DependencyNextMerge: commit,
	}

	result, err := sd.Consume(deps)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "number of merge hashes exceeded")
}

func TestStoryDetectorConsumeUnknownHashNoExpand(t *testing.T) {
	sd := &StoryDetector{
		MergeHashDict:   make(map[plumbing.Hash]int),
		MergeNames:      []string{"Story 0"},
		mergeNameCount:  1,
		expandMergeDict: false,
		l:               core.NewLogger(),
	}

	hash := plumbing.NewHash("dddddddddddddddddddddddddddddddddddddddd")
	commit := &object.Commit{
		Hash: hash,
		Author: object.Signature{
			Name: "Unknown",
			When: time.Now(),
		},
	}

	deps := map[string]interface{}{
		core.DependencyNextMerge: commit,
	}

	result, err := sd.Consume(deps)
	require.NoError(t, err)
	require.NotNil(t, result)

	authorID, ok := result[DependencyAuthor].(int)
	require.True(t, ok)
	assert.Equal(t, int(core.AuthorMissing), authorID)

	// Merge dict should not expand
	assert.Len(t, sd.MergeNames, 1)
}

func TestStoryDetectorFork(t *testing.T) {
	sd := fixtureStoryDetector()

	forks := sd.Fork(3)
	assert.Len(t, forks, 3)

	for _, fork := range forks {
		assert.IsType(t, &StoryDetector{}, fork)
		assert.Equal(t, sd, fork)
	}
}

func TestStoryDetectorLoadMergeDict(t *testing.T) {
	sd := &StoryDetector{}

	tmpf, err := ioutil.TempFile("", "hercules-story-load-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	content := `1111111111111111111111111111111111111111|2222222222222222222222222222222222222222|Story Alpha
3333333333333333333333333333333333333333|Story Beta
4444444444444444444444444444444444444444|5555555555555555555555555555555555555555
6666666666666666666666666666666666666666`

	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	err = sd.LoadMergeDict(tmpf.Name())
	require.NoError(t, err)

	assert.Len(t, sd.MergeNames, 4)
	assert.Equal(t, "Story Alpha", sd.MergeNames[0])
	assert.Equal(t, "Story Beta", sd.MergeNames[1])
	assert.Equal(t, "Merge #2", sd.MergeNames[2])
	assert.Equal(t, "Merge #3", sd.MergeNames[3])

	assert.Len(t, sd.MergeHashDict, 6)
	assert.Equal(t, 0, sd.MergeHashDict[plumbing.NewHash("1111111111111111111111111111111111111111")])
	assert.Equal(t, 0, sd.MergeHashDict[plumbing.NewHash("2222222222222222222222222222222222222222")])
	assert.Equal(t, 1, sd.MergeHashDict[plumbing.NewHash("3333333333333333333333333333333333333333")])
}

func TestStoryDetectorLoadMergeDictInvalidFile(t *testing.T) {
	sd := &StoryDetector{}

	err := sd.LoadMergeDict("/nonexistent/file.txt")
	assert.Error(t, err)
}

func TestStoryDetectorLoadMergeDictInvalidHash(t *testing.T) {
	sd := &StoryDetector{}

	tmpf, err := ioutil.TempFile("", "hercules-story-invalid-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	// Invalid hex
	content := `ZZZZ|Story Invalid`
	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	err = sd.LoadMergeDict(tmpf.Name())
	assert.Error(t, err)
}

func TestStoryDetectorLoadMergeDictShortHash(t *testing.T) {
	sd := &StoryDetector{}

	tmpf, err := ioutil.TempFile("", "hercules-story-short-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	// Too short hash
	content := `1111111111|Story Short`
	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	err = sd.LoadMergeDict(tmpf.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hash must be of")
}

func TestStoryDetectorLoadMergeDictDuplicateHash(t *testing.T) {
	sd := &StoryDetector{}

	tmpf, err := ioutil.TempFile("", "hercules-story-dup-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	// Duplicate hash in different lines
	content := `1111111111111111111111111111111111111111|Story One
1111111111111111111111111111111111111111|Story Two`
	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	err = sd.LoadMergeDict(tmpf.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ambigous hash")
}

func TestStoryDetectorMakeMergeName(t *testing.T) {
	sd := &StoryDetector{}

	hash := plumbing.NewHash("abcdef1234567890abcdef1234567890abcdef12")
	timestamp := time.Date(2024, 2, 15, 14, 30, 45, 0, time.UTC)

	commit := &object.Commit{
		Hash: hash,
		Author: object.Signature{
			When: timestamp,
		},
	}

	name := sd.makeMergeName(5, commit)

	assert.Contains(t, name, "Merge #5")
	assert.Contains(t, name, "abcdef1")
	// RFC822Z format uses 2-digit year, e.g. "15 Feb 24"
	assert.Contains(t, name, "Feb 24")
}

func TestStoryResolverMaxCount(t *testing.T) {
	sd := &StoryDetector{
		MergeNames:     []string{"A", "B", "C"},
		mergeNameCount: 10,
	}

	resolver := storyResolver{identities: sd}
	assert.Equal(t, 10, resolver.MaxCount())

	// Nil case
	nilResolver := storyResolver{identities: nil}
	assert.Equal(t, 0, nilResolver.MaxCount())
}

func TestStoryResolverCount(t *testing.T) {
	sd := &StoryDetector{
		MergeNames: []string{"A", "B", "C"},
	}

	resolver := storyResolver{identities: sd}
	assert.Equal(t, 3, resolver.Count())

	// Nil case
	nilResolver := storyResolver{identities: nil}
	assert.Equal(t, 0, nilResolver.Count())
}

func TestStoryResolverFriendlyNameOf(t *testing.T) {
	sd := &StoryDetector{
		MergeNames: []string{"Story Alpha", "Story Beta", "Story Gamma"},
	}

	resolver := storyResolver{identities: sd}

	assert.Equal(t, "Story Alpha", resolver.FriendlyNameOf(0))
	assert.Equal(t, "Story Beta", resolver.FriendlyNameOf(1))
	assert.Equal(t, "Story Gamma", resolver.FriendlyNameOf(2))

	// Edge cases
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(core.AuthorMissing))
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(-1))
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(3))
	assert.Equal(t, core.AuthorMissingName, resolver.FriendlyNameOf(999))

	// Nil case
	nilResolver := storyResolver{identities: nil}
	assert.Equal(t, core.AuthorMissingName, nilResolver.FriendlyNameOf(0))
}

func TestStoryResolverPrivateNameOf(t *testing.T) {
	sd := &StoryDetector{
		MergeNames: []string{"Story One"},
	}

	resolver := storyResolver{identities: sd}

	// PrivateNameOf delegates to FriendlyNameOf
	assert.Equal(t, "Story One", resolver.PrivateNameOf(0))
	assert.Equal(t, resolver.FriendlyNameOf(0), resolver.PrivateNameOf(0))
}

func TestStoryResolverForEachIdentity(t *testing.T) {
	sd := &StoryDetector{
		MergeNames: []string{"Story A", "Story B", "Story C"},
	}

	resolver := storyResolver{identities: sd}

	var collected []string
	var ids []core.AuthorId

	result := resolver.ForEachIdentity(func(id core.AuthorId, name string) {
		ids = append(ids, id)
		collected = append(collected, name)
	})

	assert.True(t, result)
	assert.Equal(t, []string{"Story A", "Story B", "Story C"}, collected)
	assert.Equal(t, []core.AuthorId{0, 1, 2}, ids)

	// Nil case
	nilResolver := storyResolver{identities: nil}
	called := false
	result = nilResolver.ForEachIdentity(func(id core.AuthorId, name string) {
		called = true
	})
	assert.False(t, result)
	assert.False(t, called)
}

func TestStoryResolverCopyNames(t *testing.T) {
	sd := &StoryDetector{
		MergeNames: []string{"Story X", "Story Y"},
	}

	resolver := storyResolver{identities: sd}

	copied := resolver.CopyNames(false)
	assert.Equal(t, []string{"Story X", "Story Y"}, copied)

	// Verify it's a copy (modification doesn't affect original)
	copied[0] = "Modified"
	assert.Equal(t, "Story X", sd.MergeNames[0])

	// Nil case
	nilResolver := storyResolver{identities: nil}
	assert.Nil(t, nilResolver.CopyNames(false))

	// Test with true parameter (should still ignore it)
	copied2 := resolver.CopyNames(true)
	assert.Equal(t, []string{"Story X", "Story Y"}, copied2)
}

func TestSplitMergeDict(t *testing.T) {
	input := map[plumbing.Hash]string{
		plumbing.NewHash("1111111111111111111111111111111111111111"): "Alice",
		plumbing.NewHash("2222222222222222222222222222222222222222"): "Bob",
		plumbing.NewHash("3333333333333333333333333333333333333333"): "Alice", // Duplicate name
		plumbing.NewHash("4444444444444444444444444444444444444444"): "Charlie",
	}

	hashDict, names := splitMergeDict(input)

	// Should have 3 unique names
	assert.Len(t, names, 3)
	assert.Len(t, hashDict, 4)

	// Verify all hashes mapped
	assert.Contains(t, hashDict, plumbing.NewHash("1111111111111111111111111111111111111111"))
	assert.Contains(t, hashDict, plumbing.NewHash("2222222222222222222222222222222222222222"))
	assert.Contains(t, hashDict, plumbing.NewHash("3333333333333333333333333333333333333333"))
	assert.Contains(t, hashDict, plumbing.NewHash("4444444444444444444444444444444444444444"))

	// Hashes with same name should map to same ID
	id1 := hashDict[plumbing.NewHash("1111111111111111111111111111111111111111")]
	id3 := hashDict[plumbing.NewHash("3333333333333333333333333333333333333333")]
	assert.Equal(t, id1, id3)
	assert.Equal(t, "Alice", names[id1])

	// Different names should have different IDs
	id2 := hashDict[plumbing.NewHash("2222222222222222222222222222222222222222")]
	id4 := hashDict[plumbing.NewHash("4444444444444444444444444444444444444444")]
	assert.NotEqual(t, id1, id2)
	assert.NotEqual(t, id1, id4)
	assert.NotEqual(t, id2, id4)
}

func TestSplitMergeDictEmpty(t *testing.T) {
	input := map[plumbing.Hash]string{}

	hashDict, names := splitMergeDict(input)

	assert.Empty(t, hashDict)
	assert.Empty(t, names)
}

func TestStoryDetectorIntegration(t *testing.T) {
	// Full integration test simulating the complete workflow
	sd := &StoryDetector{}

	// Create merge dict file
	tmpf, err := ioutil.TempFile("", "hercules-story-integration-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpf.Name())

	hash1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hash2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	content := hash1 + "|Project Alpha\n" + hash2 + "|Project Beta"
	_, err = tmpf.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, tmpf.Close())

	// Configure
	facts := map[string]interface{}{
		ConfigStoryDetectorMergeDictPath: tmpf.Name(),
		core.ConfigLogger:                core.NewLogger(),
	}

	err = sd.Configure(facts)
	require.NoError(t, err)

	// Initialize
	err = sd.Initialize(test.Repository)
	require.NoError(t, err)

	// Consume first commit
	commit1 := &object.Commit{
		Hash: plumbing.NewHash(hash1),
		Author: object.Signature{
			Name: "Author 1",
			When: time.Now(),
		},
	}

	result, err := sd.Consume(map[string]interface{}{
		core.DependencyNextMerge: commit1,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result[DependencyAuthor].(int))

	// Consume second commit
	commit2 := &object.Commit{
		Hash: plumbing.NewHash(hash2),
		Author: object.Signature{
			Name: "Author 2",
			When: time.Now(),
		},
	}

	result, err = sd.Consume(map[string]interface{}{
		core.DependencyNextMerge: commit2,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result[DependencyAuthor].(int))

	// Verify resolver
	resolver := facts[core.FactIdentityResolver].(core.IdentityResolver)
	assert.Equal(t, "Project Alpha", resolver.FriendlyNameOf(0))
	assert.Equal(t, "Project Beta", resolver.FriendlyNameOf(1))
}
