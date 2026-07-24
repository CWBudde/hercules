package leaves

import (
	"bytes"
	"testing"
	"time"

	gitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/plumbing/imports"
	importslang "github.com/cwbudde/hercules/internal/plumbing/imports/lang"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureImportsPerDev() *ImportsPerDeveloper {
	d := ImportsPerDeveloper{}
	d.Initialize(test.Repository)
	people := [...]string{"one@srcd", "two@srcd"}
	d.reversedPeopleDict = people[:]
	return &d
}

func TestImportsPerDeveloperMeta(t *testing.T) {
	ipd := fixtureImportsPerDev()
	ass := assert.New(t)
	ass.Equal("ImportsPerDeveloper", ipd.Name())
	ass.Empty(ipd.Provides())
	ass.Len(ipd.Requires(), 3)
	ass.Equal(imports.DependencyImports, ipd.Requires()[0])
	ass.Equal(identity.DependencyAuthor, ipd.Requires()[1])
	ass.Equal(plumbing.DependencyTick, ipd.Requires()[2])
	ass.Equal("imports-per-dev", ipd.Flag())
	assert.Empty(t, ipd.ListConfigurationOptions())
	assert.NotEmpty(t, ipd.Description())
	logger := core.NewLogger()
	assert.NoError(t, ipd.Configure(map[string]any{
		core.ConfigLogger: logger,
		identity.FactIdentityDetectorReversedPeopleDict: []string{"1", "2"},
		plumbing.FactTickSize:                           time.Hour,
	}))
	ass.Equal(logger, ipd.l)
	ass.Equal([]string{"1", "2"}, ipd.reversedPeopleDict)
	ass.Equal(time.Hour, ipd.TickSize)
}

func TestImportsPerDeveloperRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&ImportsPerDeveloper{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "ImportsPerDeveloper", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&ImportsPerDeveloper{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func TestImportsPerDeveloperInitialize(t *testing.T) {
	ipd := fixtureImportsPerDev()
	assert.NotNil(t, ipd.imports)
	assert.Equal(t, time.Hour*24, ipd.TickSize)
}

func TestImportsPerDeveloperConsumeFinalize(t *testing.T) {
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[plumbing.DependencyTick] = 1
	imps := map[gitplumbing.Hash]importslang.File{}
	imps[gitplumbing.NewHash("291286b4ac41952cbd1389fda66420ec03c1a9fe")] = importslang.File{Lang: "Go", Path: "test.go", Imports: []string{"sys"}}
	imps[gitplumbing.NewHash("c29112dbd697ad9b401333b80c18a63951bc18d9")] = importslang.File{Lang: "Python", Path: "test.py", Imports: []string{"sys"}}
	deps[imports.DependencyImports] = imps
	ipd := fixtureImportsPerDev()
	ipd.reversedPeopleDict = []string{"1", "2"}
	_, err := ipd.Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, ImportsMap{
		0: {"Go": {"sys": {1: 1}}, "Python": {"sys": {1: 1}}},
	}, ipd.imports)
	_, err = ipd.Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, ImportsMap{
		0: {"Go": {"sys": {1: 2}}, "Python": {"sys": {1: 2}}},
	}, ipd.imports)
	deps[identity.DependencyAuthor] = 1
	_, err = ipd.Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, ImportsMap{
		0: {"Go": {"sys": {1: 2}}, "Python": {"sys": {1: 2}}},
		1: {"Go": {"sys": {1: 1}}, "Python": {"sys": {1: 1}}},
	}, ipd.imports)
}

func TestImportsPerDeveloperSerializeText(t *testing.T) {
	ipd := fixtureImportsPerDev()
	res := ImportsPerDeveloperResult{Imports: ImportsMap{
		0: {"Go": {"sys": {1: 2}}, "Python": {"sys": {1: 2}}},
		1: {"Go": {"sys": {1: 1}}, "Python": {"sys": {1: 1}}},
	}, reversedPeopleDict: []string{"one", "two"}}
	buffer := &bytes.Buffer{}
	assert.NoError(t, ipd.Serialize(res, false, buffer))
	assert.Equal(t, `  tick_size: 0
  imports:
    "one": {"Go":{"sys":{"1":2}},"Python":{"sys":{"1":2}}}
    "two": {"Go":{"sys":{"1":1}},"Python":{"sys":{"1":1}}}
`, buffer.String())
}

func TestImportsPerDeveloperSerializeBinary(t *testing.T) {
	ipd := fixtureImportsPerDev()
	ass := assert.New(t)
	res := ImportsPerDeveloperResult{Imports: ImportsMap{
		0: {"Go": {"sys": {1: 2}}, "Python": {"sys": {1: 2}}},
		1: {"Go": {"sys": {1: 1}}, "Python": {"sys": {1: 1}}},
	}, reversedPeopleDict: []string{"one", "two"}}
	buffer := &bytes.Buffer{}
	ass.NoError(ipd.Serialize(res, true, buffer))
	back, err := ipd.Deserialize(buffer.Bytes())
	ass.NoError(err)
	ass.Equal(res, back)
}

func TestImportsPerDeveloperSerializeBinarySparseAuthorIndex(t *testing.T) {
	ipd := fixtureImportsPerDev()
	res := ImportsPerDeveloperResult{Imports: ImportsMap{
		4: {"Go": {"fmt": {1: 1}}},
	}, reversedPeopleDict: []string{"one", "two"}}
	buffer := &bytes.Buffer{}

	assert.NotPanics(t, func() {
		assert.NoError(t, ipd.Serialize(res, true, buffer))
	})

	back, err := ipd.Deserialize(buffer.Bytes())
	assert.NoError(t, err)
	assert.Contains(t, back.(ImportsPerDeveloperResult).Imports, 4)
}
