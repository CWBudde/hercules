package plumbing

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

func TestLanguagesDetectionMeta(t *testing.T) {
	ls := &LanguagesDetection{}
	assert.Equal(t, "LanguagesDetection", ls.Name())
	assert.Len(t, ls.Provides(), 1)
	assert.Equal(t, DependencyLanguages, ls.Provides()[0])
	assert.Len(t, ls.Requires(), 2)
	assert.Equal(t, DependencyTreeChanges, ls.Requires()[0])
	assert.Equal(t, DependencyBlobCache, ls.Requires()[1])
	opts := ls.ListConfigurationOptions()
	assert.Empty(t, opts)
	assert.NoError(t, ls.Configure(nil))
	logger := core.NewLogger()
	assert.NoError(t, ls.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, ls.l)
	assert.NoError(t, ls.Initialize(nil))
}

func TestLanguagesDetectionRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&LanguagesDetection{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "LanguagesDetection", summoned[0].Name())
	summoned = core.Registry.Summon((&LanguagesDetection{}).Provides()[0])
	assert.GreaterOrEqual(t, len(summoned), 1)
	matched := false
	for _, tp := range summoned {
		matched = matched || tp.Name() == "LanguagesDetection"
	}
	assert.True(t, matched)
}

func TestLanguagesDetectionConsume(t *testing.T) {
	ls := &LanguagesDetection{}
	changes := make(object.Changes, 3)
	// 2b1ed978194a94edeabbca6de7ff3b5771d4d665
	treeFrom, _ := test.Repository.TreeObject(plumbing.NewHash(
		"96c6ece9b2f3c7c51b83516400d278dea5605100",
	))
	treeTo, _ := test.Repository.TreeObject(plumbing.NewHash(
		"251f2094d7b523d5bcc60e663b6cf38151bf8844",
	))
	changes[0] = &object.Change{
		From: object.ChangeEntry{
			Name: "analyser.go",
			Tree: treeFrom,
			TreeEntry: object.TreeEntry{
				Name: "analyser.go",
				Mode: 0o100644,
				Hash: plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1"),
			},
		}, To: object.ChangeEntry{},
	}
	changes[1] = &object.Change{
		From: object.ChangeEntry{}, To: object.ChangeEntry{
			Name: "burndown.bin",
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: "burndown.bin",
				Mode: 0o100644,
				Hash: plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2"),
			},
		},
	}
	changes[2] = &object.Change{
		From: object.ChangeEntry{
			Name: "cmd/hercules/main.go",
			Tree: treeFrom,
			TreeEntry: object.TreeEntry{
				Name: "cmd/hercules/main.go",
				Mode: 0o100644,
				Hash: plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9"),
			},
		}, To: object.ChangeEntry{
			Name: "cmd/hercules/main2.go",
			Tree: treeTo,
			TreeEntry: object.TreeEntry{
				Name: "cmd/hercules/main.go",
				Mode: 0o100644,
				Hash: plumbing.NewHash("f7d918ec500e2f925ecde79b51cc007bac27de72"),
			},
		},
	}
	cache := map[plumbing.Hash]*CachedBlob{}
	AddHash(t, cache, "baa64828831d174f40140e4b3cfa77d1e917a2c1")
	cache[plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2")] = &CachedBlob{Data: make([]byte, 1000)}
	AddHash(t, cache, "c29112dbd697ad9b401333b80c18a63951bc18d9")
	AddHash(t, cache, "f7d918ec500e2f925ecde79b51cc007bac27de72")

	deps := map[string]any{}
	deps[DependencyBlobCache] = cache
	deps[DependencyTreeChanges] = changes
	result, err := ls.Consume(deps)
	assert.NoError(t, err)
	langs := result[DependencyLanguages].(map[plumbing.Hash]string)
	assert.Equal(t, "Go", langs[plumbing.NewHash("baa64828831d174f40140e4b3cfa77d1e917a2c1")])
	assert.Equal(t, "Go", langs[plumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9")])
	assert.Equal(t, "Go", langs[plumbing.NewHash("f7d918ec500e2f925ecde79b51cc007bac27de72")])
	lang, exists := langs[plumbing.NewHash("29c9fafd6a2fae8cd20298c3f60115bc31a4c0f2")]
	assert.True(t, exists)
	assert.Empty(t, lang)
}

func TestLanguagesDetectionTypeScriptAliases(t *testing.T) {
	ls := &LanguagesDetection{}
	lang := ls.detectLanguage("component.tsx", &CachedBlob{Data: []byte("import React from 'react';\n")})
	assert.Equal(t, "TypeScript", lang)
}

func TestLanguagesDetectionGherkinLowercase(t *testing.T) {
	ls := &LanguagesDetection{}
	lang := ls.detectLanguage("login.feature", &CachedBlob{Data: []byte("Feature: Login\n  Scenario: User logs in\n")})
	assert.Equal(t, "gherkin", lang)
}
