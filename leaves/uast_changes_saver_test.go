package leaves

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/meko-christian/hercules/internal/core"
	items "github.com/meko-christian/hercules/internal/plumbing"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/stretchr/testify/assert"
)

func addBlobHash(t *testing.T, cache map[plumbing.Hash]*items.CachedBlob, hash string) {
	t.Helper()
	objhash := plumbing.NewHash(hash)
	blob, err := test.Repository.BlobObject(objhash)
	assert.NoError(t, err)
	cb := &items.CachedBlob{Blob: *blob}
	assert.NoError(t, cb.Cache())
	cache[objhash] = cb
}

func fixtureUASTChangesSaver(t *testing.T) *UASTChangesSaver {
	t.Helper()
	saver := &UASTChangesSaver{}
	assert.NoError(t, saver.Initialize(test.Repository))
	return saver
}

func TestUASTChangesSaverMeta(t *testing.T) {
	saver := fixtureUASTChangesSaver(t)
	assert.Equal(t, "UASTChangesSaver", saver.Name())
	assert.Equal(t, "dump-uast-changes", saver.Flag())
	assert.NotEmpty(t, saver.Description())
	assert.Len(t, saver.Provides(), 0)
	assert.Equal(t, []string{items.DependencyTreeChanges, items.DependencyBlobCache}, saver.Requires())
	opts := saver.ListConfigurationOptions()
	assert.Len(t, opts, 1)
	assert.Equal(t, ConfigUASTChangesSaverOutputPath, opts[0].Name)
	assert.Equal(t, "changed-uast-dir", opts[0].Flag)
	assert.NoError(t, saver.Configure(map[string]interface{}{
		core.ConfigLogger:                core.NewLogger(),
		ConfigUASTChangesSaverOutputPath: t.TempDir(),
	}))
}

func TestUASTChangesSaverRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&UASTChangesSaver{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "UASTChangesSaver", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	found := false
	for _, leaf := range leaves {
		if leaf.Flag() == "dump-uast-changes" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestUASTChangesSaverConsumeAndSerialize(t *testing.T) {
	outputDir := t.TempDir()
	saver := &UASTChangesSaver{}
	assert.NoError(t, saver.Configure(map[string]interface{}{
		ConfigUASTChangesSaverOutputPath: outputDir,
	}))
	assert.NoError(t, saver.Initialize(test.Repository))

	cache := map[plumbing.Hash]*items.CachedBlob{}
	addBlobHash(t, cache, "334cde09da4afcb74f8d2b3e6fd6cce61228b485") // analyser.go new
	addBlobHash(t, cache, "dc248ba2b22048cc730c571a748e8ffcf7085ab9") // analyser.go old
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash("a1eb2ea76eb7f9bfbde9b243861474421000eb96"))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash("994eac1cd07235bb9815e547a75c84265dea00f5"))
	changes := object.Changes{
		&object.Change{
			From: object.ChangeEntry{
				Name: "analyser.go",
				Tree: treeFrom,
				TreeEntry: object.TreeEntry{
					Name: "analyser.go",
					Mode: 0o100644,
					Hash: plumbing.NewHash("dc248ba2b22048cc730c571a748e8ffcf7085ab9"),
				},
			},
			To: object.ChangeEntry{
				Name: "analyser.go",
				Tree: treeTo,
				TreeEntry: object.TreeEntry{
					Name: "analyser.go",
					Mode: 0o100644,
					Hash: plumbing.NewHash("334cde09da4afcb74f8d2b3e6fd6cce61228b485"),
				},
			},
		},
	}
	commit, _ := test.Repository.CommitObject(plumbing.NewHash("2b1ed978194a94edeabbca6de7ff3b5771d4d665"))
	deps := map[string]interface{}{
		core.DependencyCommit:       commit,
		items.DependencyTreeChanges: changes,
		items.DependencyBlobCache:   cache,
	}
	_, err := saver.Consume(deps)
	assert.NoError(t, err)

	result := saver.Finalize().([]UASTChangeRecord)
	if assert.Len(t, result, 1) {
		record := result[0]
		assert.Equal(t, "analyser.go", record.FileName)
		for _, p := range []string{record.SrcBefore, record.SrcAfter, record.UASTBefore, record.UASTAfter} {
			_, statErr := os.Stat(p)
			assert.NoError(t, statErr)
		}

		beforePayload, readErr := os.ReadFile(record.UASTBefore)
		assert.NoError(t, readErr)
		var beforeNodes []ast_items.Node
		assert.NoError(t, json.Unmarshal(beforePayload, &beforeNodes))
		assert.NotEmpty(t, beforeNodes)
	}

	var yamlOut bytes.Buffer
	assert.NoError(t, saver.Serialize(result, false, &yamlOut))
	assert.Contains(t, yamlOut.String(), "file: analyser.go")
	assert.Contains(t, yamlOut.String(), "uast0:")

	var binaryOut bytes.Buffer
	assert.NoError(t, saver.Serialize(result, true, &binaryOut))
	var payload map[string][]UASTChangeRecord
	assert.NoError(t, json.Unmarshal(binaryOut.Bytes(), &payload))
	assert.Len(t, payload["changes"], 1)
}
