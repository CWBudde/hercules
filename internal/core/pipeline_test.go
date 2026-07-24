package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/test"
)

type testPipelineItem struct {
	Initialized      bool
	DepsConsumed     bool
	Disposed         bool
	Forked           bool
	Merged           *bool
	CommitMatches    bool
	IndexMatches     bool
	MergeState       *int
	TestError        bool
	ConfigureRaises  bool
	InitializeRaises bool
	InitializePanics bool
	ConsumePanics    bool
	Logger           Logger
}

func (item *testPipelineItem) Name() string {
	return "Test"
}

func (item *testPipelineItem) Provides() []string {
	return []string{"test"}
}

func (item *testPipelineItem) Requires() []string {
	return []string{}
}

func (item *testPipelineItem) Configure(facts map[string]any) error {
	if item.ConfigureRaises {
		return errors.New("test1")
	}
	if l, ok := facts[ConfigLogger].(Logger); ok {
		item.Logger = l
	}
	return nil
}

func (item *testPipelineItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *testPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	options := [...]ConfigurationOption{{
		Name:        "TestOption",
		Description: "The option description.",
		Flag:        "test-option",
		Type:        IntConfigurationOption,
		Default:     10,
	}}
	return options[:]
}

func (item *testPipelineItem) Flag() string {
	return "mytest"
}

func (item *testPipelineItem) Description() string {
	return "description!"
}

func (item *testPipelineItem) Features() []string {
	f := [...]string{"power"}
	return f[:]
}

func (item *testPipelineItem) Initialize(repository *git.Repository) error {
	if item.InitializePanics {
		panic("!")
	}
	item.Initialized = repository != nil
	item.Merged = new(bool)
	item.MergeState = new(int)
	if item.InitializeRaises {
		return errors.New("test2")
	}
	return nil
}

func (item *testPipelineItem) Consume(deps map[string]any) (map[string]any, error) {
	if item.TestError {
		return nil, errors.New("error")
	}
	if item.ConsumePanics {
		panic("!")
	}
	obj, exists := deps[DependencyCommit]
	item.DepsConsumed = exists
	if item.DepsConsumed {
		commit := obj.(*object.Commit)
		item.CommitMatches = commit.Hash == plumbing.NewHash(
			"af9ddc0db70f09f3f27b4b98e415592a7485171c",
		)
		obj, item.DepsConsumed = deps[DependencyIndex]
		if item.DepsConsumed {
			item.IndexMatches = obj.(int) == 0
		}
	}

	if obj, exists = deps[DependencyIsMerge]; exists {
		*item.MergeState++
		if b, ok := obj.(bool); ok && b {
			*item.MergeState++
		}
	}
	return map[string]any{"test": item}, nil
}

func (item *testPipelineItem) Dispose() {
	item.Disposed = true
}

func (item *testPipelineItem) Fork(n int) []PipelineItem {
	result := make([]PipelineItem, n)
	for i := range n {
		result[i] = &testPipelineItem{Merged: item.Merged, MergeState: item.MergeState}
	}
	item.Forked = true
	return result
}

func (item *testPipelineItem) Merge(branches []PipelineItem) {
	*item.Merged = true
}

func (item *testPipelineItem) Finalize() any {
	return item
}

func (item *testPipelineItem) Serialize(result any, binary bool, writer io.Writer) error {
	return nil
}

type dependingTestPipelineItem struct {
	DependencySatisfied  bool
	TestNilConsumeReturn bool
	Hibernated           bool
	Booted               bool
	RaiseHibernateError  bool
	RaiseBootError       bool
}

func (item *dependingTestPipelineItem) Name() string {
	return "Test2"
}

func (item *dependingTestPipelineItem) Provides() []string {
	return []string{"test2"}
}

func (item *dependingTestPipelineItem) Requires() []string {
	return []string{"test"}
}

func (item *dependingTestPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	options := [...]ConfigurationOption{{
		Name:        "TestOption2",
		Description: "The option description.",
		Flag:        "test-option2",
		Type:        IntConfigurationOption,
		Default:     10,
	}}
	return options[:]
}

func (item *dependingTestPipelineItem) Configure(facts map[string]any) error {
	return nil
}

func (item *dependingTestPipelineItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *dependingTestPipelineItem) Initialize(repository *git.Repository) error {
	return nil
}

func (item *dependingTestPipelineItem) Flag() string {
	return "depflag"
}

func (item *dependingTestPipelineItem) Description() string {
	return "another description"
}

func (item *dependingTestPipelineItem) Consume(deps map[string]any) (map[string]any, error) {
	_, exists := deps["test"]
	item.DependencySatisfied = exists
	if !item.TestNilConsumeReturn {
		return map[string]any{"test2": item}, nil
	}
	return nil, nil
}

func (item *dependingTestPipelineItem) Fork(n int) []PipelineItem {
	clones := make([]PipelineItem, n)
	for i := range clones {
		clones[i] = item
	}
	return clones
}

func (item *dependingTestPipelineItem) Merge(branches []PipelineItem) {
}

func (item *dependingTestPipelineItem) Hibernate() error {
	item.Hibernated = true
	if item.RaiseHibernateError {
		return errors.New("error")
	}
	return nil
}

func (item *dependingTestPipelineItem) Boot() error {
	item.Booted = true
	if item.RaiseBootError {
		return errors.New("error")
	}
	return nil
}

func (item *dependingTestPipelineItem) Finalize() any {
	return true
}

func (item *dependingTestPipelineItem) Serialize(result any, binary bool, writer io.Writer) error {
	return nil
}

func TestPipelineFeatures(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.SetFeature("feat")
	val, _ := pipeline.GetFeature("feat")
	assert.True(t, val)
	_, exists := pipeline.GetFeature("!")
	assert.False(t, exists)
	Registry.featureFlags.Set("777")
	defer func() {
		Registry.featureFlags = arrayFeatureFlags{Flags: []string{}, Choices: map[string]bool{}}
	}()
	pipeline.SetFeaturesFromFlags()
	_, exists = pipeline.GetFeature("777")
	assert.False(t, exists)
	assert.Panics(t, func() {
		pipeline.SetFeaturesFromFlags(
			&PipelineItemRegistry{}, &PipelineItemRegistry{},
		)
	})
}

func TestPipelineErrors(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	item.ConfigureRaises = true
	err := pipeline.Initialize(map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "configure")
	assert.Contains(t, err.Error(), "test1")
	item.ConfigureRaises = false
	item.InitializeRaises = true
	err = pipeline.Initialize(map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "initialize")
	assert.Contains(t, err.Error(), "test2")
	item.InitializeRaises = false
	item.InitializePanics = true
	assert.Panics(t, func() { pipeline.Initialize(map[string]any{}) })
}

func TestPipelineInitialize(t *testing.T) {
	t.Run("without logger fact", func(t *testing.T) {
		pipeline := NewPipeline(test.FixtureRepository())
		item := &testPipelineItem{}
		pipeline.AddItem(item)
		require.NoError(t, pipeline.Initialize(map[string]any{}))
		// pipeline logger should be initialized, and item logger should be the same
		require.NotNil(t, pipeline.l)
		assert.Equal(t, pipeline.l, item.Logger)
	})

	t.Run("with logger fact", func(t *testing.T) {
		pipeline := NewPipeline(test.FixtureRepository())
		item := &testPipelineItem{}
		logger := NewLogger()
		pipeline.AddItem(item)
		require.NoError(t, pipeline.Initialize(map[string]any{
			ConfigLogger: logger,
		}))
		// pipeline logger should be set, and the item logger should be the same
		assert.Equal(t, logger, pipeline.l)
		assert.Equal(t, logger, item.Logger)
	})
}

func TestPipelineRun(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	assert.NoError(t, pipeline.Initialize(map[string]any{}))
	assert.True(t, item.Initialized)
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, item, result[item].(*testPipelineItem))
	common := result[nil].(*CommonAnalysisResult)
	assert.Equal(t, int64(1481719198), common.BeginTime)
	assert.Equal(t, int64(1481719198), common.EndTime)
	assert.Equal(t, 1, common.CommitsNumber)
	assert.Less(t, common.RunTime.Nanoseconds()/1e6, 100)
	assert.Len(t, common.RunTimePerItem, 1)
	for key, val := range common.RunTimePerItem {
		assert.GreaterOrEqual(t, val, 0, key)
	}
	assert.True(t, item.DepsConsumed)
	assert.True(t, item.Disposed)
	assert.True(t, item.CommitMatches)
	assert.True(t, item.IndexMatches)
	assert.Equal(t, 1, *item.MergeState)
	assert.True(t, item.Forked)
	assert.False(t, *item.Merged)
	pipeline.RemoveItem(item)
	result, err = pipeline.Run(commits)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestPipelineRunBranches(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]any{})
	assert.True(t, item.Initialized)
	hashes := []string{
		"6db8065cdb9bb0758f36a7e75fc72ab95f9e8145",
		"f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee",
		"8a03b5620b1caa72ec9cb847ea88332621e2950a",
		"dd9dd084d5851d7dc4399fc7dbf3d8292831ebc5",
		"f4ed0405b14f006c0744029d87ddb3245607587a",
	}
	commits := make([]*object.Commit, len(hashes))
	for i, h := range hashes {
		var err error
		commits[i], err = test.FixtureRepository().CommitObject(plumbing.NewHash(h))
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := pipeline.Run(commits)
	assert.NoError(t, err)
	assert.True(t, item.Forked)
	assert.True(t, *item.Merged)
	assert.Len(t, result, 2)
	assert.Equal(t, item, result[item].(*testPipelineItem))
	common := result[nil].(*CommonAnalysisResult)
	assert.Equal(t, 5, common.CommitsNumber)
	assert.Equal(t, 6, *item.MergeState)
}

func TestPipelineOnProgress(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	progressOk := 0

	onProgress := func(step, total int, action string) {
		if step == 1 && total == 4 && action == "emerge" {
			progressOk++
		}
		if step == 2 && total == 4 && action == "af9ddc0" {
			progressOk++
		}
		if step == 3 && total == 4 && action == "finalize" {
			progressOk++
		}
		if step == 4 && total == 4 && action == "" {
			progressOk++
		}
	}

	pipeline.OnProgress = onProgress
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 4, progressOk)
}

func TestPipelineCommitsFull(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	commits, err := pipeline.Commits(false)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(commits), 100)
	hashMap := map[plumbing.Hash]bool{}
	for _, c := range commits {
		hashMap[c.Hash] = true
	}
	assert.Len(t, hashMap, len(commits))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
}

func TestPipelineCommitsFirstParent(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	commits, err := pipeline.Commits(true)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(commits), 100)
	hashMap := map[plumbing.Hash]bool{}
	for _, c := range commits {
		hashMap[c.Hash] = true
	}
	assert.Len(t, hashMap, len(commits))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	assert.NotContains(t, hashMap, plumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
}

func TestPipelineHeadCommit(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	commits, err := pipeline.HeadCommit()
	assert.NoError(t, err)
	assert.Len(t, commits, 1)
	assert.NotEmpty(t, commits[0].ParentHashes)
	head, _ := test.FixtureRepository().Head()
	assert.Equal(t, head.Hash(), commits[0].Hash)
}

func TestLoadCommitsFromFile(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "hercules-test-")
	assert.NoError(t, err)
	tmp.WriteString("cce947b98a050c6d356bc6ba95030254914027b1\n6db8065cdb9bb0758f36a7e75fc72ab95f9e8145")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err := LoadCommitsFromFile(tmp.Name(), test.FixtureRepository())
	assert.NoError(t, err)
	assert.Len(t, commits, 2)
	assert.Equal(t, commits[0].Hash, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	assert.Equal(t, commits[1].Hash, plumbing.NewHash(
		"6db8065cdb9bb0758f36a7e75fc72ab95f9e8145",
	))
	commits, err = LoadCommitsFromFile("/WAT?xxx!", test.FixtureRepository())
	assert.Nil(t, commits)
	assert.Error(t, err)
	tmp, err = os.CreateTemp(t.TempDir(), "hercules-test-")
	assert.NoError(t, err)
	tmp.WriteString("WAT")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err = LoadCommitsFromFile(tmp.Name(), test.FixtureRepository())
	assert.Nil(t, commits)
	assert.Error(t, err)
	tmp, err = os.CreateTemp(t.TempDir(), "hercules-test-")
	assert.NoError(t, err)
	tmp.WriteString("ffffffffffffffffffffffffffffffffffffffff")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err = LoadCommitsFromFile(tmp.Name(), test.FixtureRepository())
	assert.Nil(t, commits)
	assert.Error(t, err)
}

func TestPipelineDeps(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item1 := &dependingTestPipelineItem{}
	item2 := &testPipelineItem{}
	pipeline.AddItem(item1)
	pipeline.AddItem(item2)
	assert.Equal(t, 2, pipeline.Len())
	pipeline.Initialize(map[string]any{})
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NoError(t, err)
	assert.True(t, result[item1].(bool))
	assert.Equal(t, result[item2], item2)
	item1.TestNilConsumeReturn = true
	_, err = pipeline.Run(commits)
	assert.Error(t, err)
}

func TestPipelineDeployFeatures(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.DeployItem(&testPipelineItem{})
	f, _ := pipeline.GetFeature("power")
	assert.True(t, f)
}

func TestPipelineError(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	item.TestError = true
	pipeline.AddItem(item)
	assert.NoError(t, pipeline.Initialize(map[string]any{}))
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestPipelineDryRun(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	item.TestError = true
	pipeline.AddItem(item)
	pipeline.DryRun = true
	pipeline.Initialize(map[string]any{})
	assert.True(t, pipeline.DryRun)
	pipeline.DryRun = false
	pipeline.Initialize(map[string]any{ConfigPipelineDryRun: true})
	assert.True(t, pipeline.DryRun)
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Contains(t, result, nil)
	assert.NoError(t, err)
}

func TestPipelineDryRunFalse(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]any{ConfigPipelineDryRun: false})
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NotNil(t, result)
	assert.Len(t, result, 2)
	assert.Contains(t, result, nil)
	assert.Contains(t, result, item)
	assert.NoError(t, err)
	assert.True(t, item.DepsConsumed)
	assert.True(t, item.CommitMatches)
	assert.True(t, item.IndexMatches)
	assert.Equal(t, 1, *item.MergeState)
	assert.True(t, item.Forked)
	assert.False(t, *item.Merged)
}

func TestPipelineDumpPlanConfigure(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	pipeline.DumpPlan = true
	pipeline.DryRun = true
	pipeline.Initialize(map[string]any{})
	assert.True(t, pipeline.DumpPlan)
	pipeline.DumpPlan = false
	pipeline.Initialize(map[string]any{ConfigPipelineDumpPlan: true})
	assert.True(t, pipeline.DumpPlan)
	stream := &bytes.Buffer{}
	backupPlanPrintFunc := planPrintFunc
	planPrintFunc = func(args ...any) {
		fmt.Fprintln(stream, args...)
	}
	defer func() {
		planPrintFunc = backupPlanPrintFunc
	}()
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))
	result, err := pipeline.Run(commits)
	assert.NotNil(t, result)
	assert.Len(t, result, 1)
	assert.Contains(t, result, nil)
	assert.NoError(t, err)
	assert.Equal(t, `E [1]
C 1 af9ddc0db70f09f3f27b4b98e415592a7485171c
`, stream.String())
}

func TestCommonAnalysisResultCopy(t *testing.T) {
	c1 := CommonAnalysisResult{
		BeginTime: 1513620635, EndTime: 1513720635, CommitsNumber: 1, RunTime: 100,
		RunTimePerItem: map[string]float64{"one": 1, "two": 2},
	}
	c2 := c1.Copy()
	assert.Equal(t, c1, c2)
	c2.RunTimePerItem["one"] = 100500
	assert.InDelta(t, float64(1), c1.RunTimePerItem["one"], 0.00001)
}

func TestCommonAnalysisResultMerge(t *testing.T) {
	c1 := CommonAnalysisResult{
		BeginTime: 1513620635, EndTime: 1513720635, CommitsNumber: 1, RunTime: 100,
		RunTimePerItem: map[string]float64{"one": 1, "two": 2},
	}
	assert.Equal(t, int64(1513620635), c1.BeginTimeAsTime().Unix())
	assert.Equal(t, int64(1513720635), c1.EndTimeAsTime().Unix())
	c2 := CommonAnalysisResult{
		BeginTime: 1513620535, EndTime: 1513730635, CommitsNumber: 2, RunTime: 200,
		RunTimePerItem: map[string]float64{"two": 4, "three": 8},
	}
	c1.Merge(&c2)
	assert.Equal(t, int64(1513620535), c1.BeginTime)
	assert.Equal(t, int64(1513730635), c1.EndTime)
	assert.Equal(t, 3, c1.CommitsNumber)
	assert.Equal(t, int64(300), c1.RunTime.Nanoseconds())
	assert.Equal(t, map[string]float64{"one": 1, "two": 6, "three": 8}, c1.RunTimePerItem)
}

func TestCommonAnalysisResultMetadata(t *testing.T) {
	c1 := &CommonAnalysisResult{
		BeginTime: 1513620635, EndTime: 1513720635, CommitsNumber: 1, RunTime: 100 * 1e6,
		RunTimePerItem: map[string]float64{"one": 1, "two": 2},
	}
	meta := &pb.Metadata{}
	c1 = MetadataToCommonAnalysisResult(c1.FillMetadata(meta))
	assert.Equal(t, int64(1513620635), c1.BeginTimeAsTime().Unix())
	assert.Equal(t, int64(1513720635), c1.EndTimeAsTime().Unix())
	assert.Equal(t, 1, c1.CommitsNumber)
	assert.Equal(t, c1.RunTime.Nanoseconds(), int64(100*1e6))
	assert.Equal(t, map[string]float64{"one": 1, "two": 2}, c1.RunTimePerItem)
}

func TestConfigurationOptionTypeString(t *testing.T) {
	opt := ConfigurationOptionType(0)
	assert.Empty(t, opt.String())
	opt = ConfigurationOptionType(1)
	assert.Equal(t, "int", opt.String())
	opt = ConfigurationOptionType(2)
	assert.Equal(t, "string", opt.String())
	opt = ConfigurationOptionType(3)
	assert.Equal(t, "float", opt.String())
	opt = ConfigurationOptionType(4)
	assert.Equal(t, "string", opt.String())
	opt = ConfigurationOptionType(5)
	assert.Equal(t, "path", opt.String())
	opt = ConfigurationOptionType(6)
	assert.Panics(t, func() { _ = opt.String() })
}

func TestConfigurationOptionFormatDefault(t *testing.T) {
	opt := ConfigurationOption{Type: StringConfigurationOption, Default: "ololo"}
	assert.Equal(t, "\"ololo\"", opt.FormatDefault())
	opt = ConfigurationOption{Type: IntConfigurationOption, Default: 7}
	assert.Equal(t, "7", opt.FormatDefault())
	opt = ConfigurationOption{Type: BoolConfigurationOption, Default: false}
	assert.Equal(t, "false", opt.FormatDefault())
	opt = ConfigurationOption{Type: FloatConfigurationOption, Default: 0.5}
	assert.Equal(t, "0.5", opt.FormatDefault())
}

func TestPrepareRunPlanTiny(t *testing.T) {
	rootCommit, err := test.FixtureRepository().CommitObject(plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := prepareRunPlan([]*object.Commit{rootCommit}, 0, false)
	assert.Len(t, plan, 2)
	assert.Equal(t, runActionEmerge, plan[0].Action)
	assert.Equal(t, rootBranchIndex, plan[0].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[0].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[1].Action)
	assert.Equal(t, rootBranchIndex, plan[1].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[1].Commit.Hash.String())
}

func TestPrepareRunPlanSmall(t *testing.T) {
	cit, err := test.FixtureRepository().Log(&git.LogOptions{From: plumbing.ZeroHash})
	if err != nil {
		panic(err)
	}
	defer cit.Close()
	var commits []*object.Commit
	timeCutoff := time.Date(2016, 12, 15, 0, 0, 0, 0, time.FixedZone("CET", 7200))
	cit.ForEach(func(commit *object.Commit) error {
		reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
			commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
			commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
		if reliableTime.Before(timeCutoff) {
			commits = append(commits, commit)
		}
		return nil
	})
	plan, _ := prepareRunPlan(commits, 0, false)
	/*for _, p := range plan {
		if p.Commit != nil {
			fmt.Println(p.Action, p.Commit.Hash.String(), p.Items)
		} else {
			fmt.Println(p.Action, strings.Repeat(" ", 40), p.Items)
		}
	}*/
	// fork, merge and one artificial commit per branch
	assert.Len(t, plan, len(commits)+1)
	assert.Equal(t, runActionEmerge, plan[0].Action)
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[0].Commit.Hash.String())
	assert.Equal(t, rootBranchIndex, plan[0].Items[0])
	assert.Equal(t, runActionCommit, plan[1].Action)
	assert.Equal(t, rootBranchIndex, plan[1].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[1].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[2].Action)
	assert.Equal(t, rootBranchIndex, plan[2].Items[0])
	assert.Equal(t, "a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3", plan[2].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[10].Action)
	assert.Equal(t, rootBranchIndex, plan[10].Items[0])
	assert.Equal(t, "a28e9064c70618dc9d68e1401b889975e0680d11", plan[10].Commit.Hash.String())
}

func TestMergeDag(t *testing.T) {
	cit, err := test.FixtureRepository().Log(&git.LogOptions{From: plumbing.ZeroHash})
	if err != nil {
		panic(err)
	}
	defer cit.Close()
	var commits []*object.Commit
	timeCutoff := time.Date(2017, 8, 12, 0, 0, 0, 0, time.FixedZone("CET", 7200))
	cit.ForEach(func(commit *object.Commit) error {
		reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
			commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
			commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
		if reliableTime.Before(timeCutoff) {
			commits = append(commits, commit)
		}
		return nil
	})
	hashes, dag := buildDag(commits)
	leaveRootComponent(hashes, dag)
	mergedDag, _ := mergeDag(hashes, dag)
	for key, vals := range mergedDag {
		if key != plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11") &&
			key != plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5") {
			assert.Len(t, vals, len(dag[key]), key.String())
		} else {
			mvals := map[string]bool{}
			for _, val := range vals {
				mvals[val.Hash.String()] = true
			}
			if key == plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11") {
				assert.Contains(t, mvals, "db325a212d0bc99b470e000641d814745024bbd5")
				assert.Contains(t, mvals, "be9b61e09b08b98e64ed461a4004c9e2412f78ee")
			}
			if key == plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5") {
				assert.Contains(t, mvals, "f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee")
				assert.Contains(t, mvals, "8a03b5620b1caa72ec9cb847ea88332621e2950a")
			}
		}
	}
	assert.Len(t, mergedDag, 8)
	assert.Contains(t, mergedDag, plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1"))
	assert.Contains(t, mergedDag, plumbing.NewHash("a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3"))
	assert.Contains(t, mergedDag, plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11"))
	assert.Contains(t, mergedDag, plumbing.NewHash("be9b61e09b08b98e64ed461a4004c9e2412f78ee"))
	assert.Contains(t, mergedDag, plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5"))
	assert.Contains(t, mergedDag, plumbing.NewHash("f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee"))
	assert.Contains(t, mergedDag, plumbing.NewHash("8a03b5620b1caa72ec9cb847ea88332621e2950a"))
	assert.Contains(t, mergedDag, plumbing.NewHash("dd9dd084d5851d7dc4399fc7dbf3d8292831ebc5"))
	queue := []plumbing.Hash{plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1")}
	visited := map[plumbing.Hash]bool{}
	for len(queue) > 0 {
		head := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if visited[head] {
			continue
		}
		visited[head] = true
		for _, child := range mergedDag[head] {
			queue = append(queue, child.Hash)
		}
	}
	assert.Len(t, visited, 8)
}

func TestPrepareRunPlanBig(t *testing.T) {
	cases := [][7]int{
		{2017, 8, 9, 0, 0, 0, 0},
		{2017, 8, 10, 0, 0, 0, 0},
		{2017, 8, 24, 0, 1, 1, 1},
		{2017, 9, 19, -2, 1, 1, 1},
		{2017, 9, 23, -2, 1, 1, 1},
		{2017, 12, 8, 0, 1, 1, 1},
		{2017, 12, 9, 0, 1, 1, 1},
		{2017, 12, 10, 0, 1, 1, 1},
		{2017, 12, 11, 0, 2, 2, 2},
		{2017, 12, 19, 0, 3, 3, 3},
		{2017, 12, 27, 0, 3, 3, 3},
		{2018, 1, 10, 0, 3, 3, 3},
		{2018, 1, 16, 0, 3, 3, 3},
		{2018, 1, 18, 0, 5, 4, 4},
		{2018, 1, 23, 0, 5, 5, 5},
		// The hermetic siva fixture's history ends at its master commit
		// (authored 2018-01-25), so cases with later cutoffs — which relied on
		// upstream commits not present in the fixture — are not included.
	}
	for _, testCase := range cases {
		func() {
			cit, err := test.FixtureRepository().Log(&git.LogOptions{From: plumbing.ZeroHash})
			if err != nil {
				panic(err)
			}
			defer cit.Close()
			var commits []*object.Commit
			timeCutoff := time.Date(
				testCase[0], time.Month(testCase[1]), testCase[2], 0, 0, 0, 0, time.FixedZone("CET", 7200),
			)
			cit.ForEach(func(commit *object.Commit) error {
				reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
					commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
					commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
				if reliableTime.Before(timeCutoff) {
					commits = append(commits, commit)
				}
				return nil
			})
			plan, _ := prepareRunPlan(commits, 0, false)
			/*for _, p := range plan {
				if p.Commit != nil {
					fmt.Println(p.Action, p.Commit.Hash.String(), p.Items)
				} else {
					fmt.Println(p.Action, strings.Repeat(" ", 40), p.Items)
				}
			}*/
			numCommits := 0
			numForks := 0
			numMerges := 0
			numDeletes := 0
			numEmerges := 0
			processed := map[plumbing.Hash]map[int]int{}
			for _, p := range plan {
				switch p.Action {
				case runActionCommit:
					branches := processed[p.Commit.Hash]
					if branches == nil {
						branches = map[int]int{}
						processed[p.Commit.Hash] = branches
					}
					branches[p.Items[0]]++
					for _, parent := range p.Commit.ParentHashes {
						assert.Contains(t, processed, parent)
					}
					numCommits++
				case runActionFork:
					numForks++
				case runActionMerge:
					counts := map[int]int{}
					for _, i := range p.Items {
						counts[i]++
					}
					for x, v := range counts {
						assert.Equal(t, 1, v, x)
					}
					numMerges++
				case runActionDelete:
					numDeletes++
				case runActionEmerge:
					numEmerges++
				}
			}
			for c, branches := range processed {
				for b, v := range branches {
					assert.Equal(t, 1, v, fmt.Sprint(c.String(), b))
				}
			}
			assert.Equal(t, len(commits)+testCase[3], numCommits, "commits %v", testCase)
			assert.Equal(t, testCase[4], numForks, "forks %v", testCase)
			assert.Equal(t, testCase[5], numMerges, "merges %v", testCase)
			assert.Equal(t, testCase[6], numDeletes, "deletes %v", testCase)
			assert.Equal(t, 1, numEmerges, "emerges %v", testCase)
		}()
	}
}

func TestPipelineRunHibernation(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.HibernationDistance = 2
	pipeline.AddItem(&testPipelineItem{})
	item := &dependingTestPipelineItem{}
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]any{})
	hashes := []string{
		"0183e08978007c746468fca9f68e6e2fbf32100c",
		"b467a682f680a4dcfd74869480a52f8be3a4fdf0",
		"31c9f752f9ce103e85523442fa3f05b1ff4ea546",
		"6530890fcd02fb5e6e85ce2951fdd5c555f2c714",
		"feb2d230777cbb492ecbc27dea380dc1e7b8f437",
		"9b30d2abc043ab59aa7ec7b50970c65c90b98853",
	}
	commits := make([]*object.Commit, len(hashes))
	for i, h := range hashes {
		var err error
		commits[i], err = test.FixtureRepository().CommitObject(plumbing.NewHash(h))
		if err != nil {
			t.Fatal(err)
		}
	}
	pipeline.PrintActions = true
	_, err := pipeline.Run(commits)
	assert.NoError(t, err)
	assert.True(t, item.Hibernated)
	assert.True(t, item.Booted)
	item.RaiseHibernateError = true
	_, err = pipeline.Run(commits)
	assert.Error(t, err)
	item.RaiseHibernateError = false
	pipeline.Run(commits)
	item.RaiseBootError = true
	_, err = pipeline.Run(commits)
	assert.Error(t, err)
}

// configUpstreamFailItem is a minimal PipelineItem whose ConfigureUpstream always fails.
type configUpstreamFailItem struct {
	NoopMerger
}

func (item *configUpstreamFailItem) Name() string { return "UpstreamFail" }

func (item *configUpstreamFailItem) Provides() []string { return []string{"upstreamfail"} }

func (item *configUpstreamFailItem) Requires() []string                              { return []string{} }
func (item *configUpstreamFailItem) ListConfigurationOptions() []ConfigurationOption { return nil }
func (item *configUpstreamFailItem) Configure(facts map[string]any) error            { return nil }
func (item *configUpstreamFailItem) ConfigureUpstream(facts map[string]any) error {
	return errors.New("upstream config error")
}
func (item *configUpstreamFailItem) Initialize(repository *git.Repository) error { return nil }
func (item *configUpstreamFailItem) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{"upstreamfail": true}, nil
}

func (item *configUpstreamFailItem) Fork(n int) []PipelineItem {
	return ForkSamePipelineItem(item, n)
}

func TestConfigurationOptionFormatDefaultStrings(t *testing.T) {
	opt := ConfigurationOption{Type: StringsConfigurationOption, Default: []string{"a", "b", "c"}}
	assert.Equal(t, "\"a,b,c\"", opt.FormatDefault())

	opt = ConfigurationOption{Type: PathConfigurationOption, Default: "/usr/bin"}
	assert.Equal(t, "/usr/bin", opt.FormatDefault())
}

func TestGetSensibleRemote(t *testing.T) {
	remote := GetSensibleRemote(test.FixtureRepository())
	assert.NotEmpty(t, remote)
	assert.NotEqual(t, "<no remote>", remote)
}

func TestCommonAnalysisResultMergePanic(t *testing.T) {
	t.Run("car EndTime is zero", func(t *testing.T) {
		assert.Panics(t, func() {
			c1 := CommonAnalysisResult{BeginTime: 100, EndTime: 0, RunTimePerItem: map[string]float64{}}
			c2 := CommonAnalysisResult{BeginTime: 100, EndTime: 200, RunTimePerItem: map[string]float64{}}
			c1.Merge(&c2)
		})
	})
	t.Run("other BeginTime is zero", func(t *testing.T) {
		assert.Panics(t, func() {
			c1 := CommonAnalysisResult{BeginTime: 100, EndTime: 200, RunTimePerItem: map[string]float64{}}
			c2 := CommonAnalysisResult{BeginTime: 0, EndTime: 300, RunTimePerItem: map[string]float64{}}
			c1.Merge(&c2)
		})
	})
}

func TestCommonAnalysisResultMergeNoTimeChange(t *testing.T) {
	c1 := CommonAnalysisResult{
		BeginTime: 100, EndTime: 500, CommitsNumber: 1, RunTime: 100,
		RunTimePerItem: map[string]float64{},
	}
	c2 := CommonAnalysisResult{
		BeginTime: 200, EndTime: 400, CommitsNumber: 2, RunTime: 200,
		RunTimePerItem: map[string]float64{},
	}
	c1.Merge(&c2)
	assert.Equal(t, int64(100), c1.BeginTime)
	assert.Equal(t, int64(500), c1.EndTime)
	assert.Equal(t, 3, c1.CommitsNumber)
}

func TestPipelineDeployItemOnce(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item1 := &testPipelineItem{}
	pipeline.AddItem(item1)

	// DeployItemOnce with same Name() should return existing item
	item2 := &testPipelineItem{}
	result := pipeline.DeployItemOnce(item2)
	assert.Same(t, item1, result)
	assert.Equal(t, 1, pipeline.Len())
}

func TestPipelineDeployItemOnceNew(t *testing.T) {
	// DeployItemOnce with a new item (not yet in pipeline) should add it
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	result := pipeline.DeployItemOnce(item)
	assert.Same(t, item, result)
	assert.Equal(t, 1, pipeline.Len())
}

func TestPipelineRunPreparedPlanNil(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})
	pipeline.Initialize(map[string]any{})

	result, err := pipeline.RunPreparedPlan()
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not prepared")
}

func TestPipelineRunPreparedPlanValid(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)

	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))

	err := pipeline.InitializeExt(map[string]any{
		ConfigPipelineCommits: commits,
	}, func(items []PipelineItem) PipelineItem { return items[0] }, true)
	require.NoError(t, err)

	result, err := pipeline.RunPreparedPlan()
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 2)

	// Second call should fail (preparedRun was consumed)
	result2, err2 := pipeline.RunPreparedPlan()
	assert.Nil(t, result2)
	assert.Error(t, err2)
}

func TestPipelineNegativeHibernationDistance(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})
	err := pipeline.Initialize(map[string]any{
		ConfigPipelineHibernationDistance: -1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hibernation-distance")
}

func TestPipelineHibernationDistanceFromFact(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})
	err := pipeline.Initialize(map[string]any{
		ConfigPipelineHibernationDistance: 5,
	})
	assert.NoError(t, err)
	assert.Equal(t, 5, pipeline.HibernationDistance)
}

func TestPipelinePrintActionsFromFact(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})
	pipeline.Initialize(map[string]any{
		ConfigPipelinePrintActions: true,
	})
	assert.True(t, pipeline.PrintActions)
}

func TestPipelineUnsatisfiedDependency(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &dependingTestPipelineItem{} // requires "test" but nothing provides it
	pipeline.AddItem(item)
	err := pipeline.Initialize(map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsatisfied")
}

func TestPipelineDAGDumpToFile(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})

	tmpFile, err := os.CreateTemp(t.TempDir(), "hercules-dag-test-")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	err = pipeline.Initialize(map[string]any{
		ConfigPipelineDAGPath: tmpFile.Name(),
	})
	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.NotEmpty(t, content)
}

func TestPipelineDAGDumpToStderr(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := pipeline.Initialize(map[string]any{
		ConfigPipelineDAGPath: "-",
	})

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	assert.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

func TestPipelineConfigureUpstreamError(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&configUpstreamFailItem{})
	err := pipeline.Initialize(map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "configure upstream")
}

func TestPipelineInitializeMergeTracksNotAllowed(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.SetFeature(FeatureMergeTracks)
	pipeline.AddItem(&testPipelineItem{})
	err := pipeline.Initialize(map[string]any{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "merge tracks")
}

func TestPipelineInitializeExtNoCommits(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&testPipelineItem{})
	err := pipeline.InitializeExt(map[string]any{},
		func(items []PipelineItem) PipelineItem { return items[0] }, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commits are not available")
}

func TestPipelineInitializeDryRunSkipsConfigure(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{ConfigureRaises: true}
	pipeline.AddItem(item)
	// DryRun via fact should skip Configure (which would fail)
	err := pipeline.Initialize(map[string]any{
		ConfigPipelineDryRun: true,
	})
	assert.NoError(t, err)
	assert.True(t, pipeline.DryRun)
}

// circularDepItem creates a circular dependency by requiring and providing the same key.
type circularDepItem struct {
	NoopMerger
}

func (item *circularDepItem) Name() string       { return "Circular" }
func (item *circularDepItem) Provides() []string { return []string{"circular"} }

func (item *circularDepItem) Requires() []string                              { return []string{"circular"} }
func (item *circularDepItem) ListConfigurationOptions() []ConfigurationOption { return nil }
func (item *circularDepItem) Configure(facts map[string]any) error            { return nil }
func (item *circularDepItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}
func (item *circularDepItem) Initialize(repository *git.Repository) error { return nil }
func (item *circularDepItem) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{"circular": true}, nil
}

func (item *circularDepItem) Fork(n int) []PipelineItem {
	return ForkSamePipelineItem(item, n)
}

func TestPipelineResolveCircularDependency(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.AddItem(&circularDepItem{})
	err := pipeline.Initialize(map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "topological sort")
}

func TestGetSensibleRemoteNoRemote(t *testing.T) {
	repo, err := git.Init(memory.NewStorage(), nil)
	require.NoError(t, err)
	remote := GetSensibleRemote(repo)
	assert.Equal(t, "<no remote>", remote)
}

func TestPipelineInitializeWithCommitsFact(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	item := &testPipelineItem{}
	pipeline.AddItem(item)

	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))

	// Pass commits directly via fact to skip Commits() call
	err := pipeline.Initialize(map[string]any{
		ConfigPipelineCommits: commits,
	})
	assert.NoError(t, err)
	assert.True(t, item.Initialized)
}

func TestPipelineInitializeExtMergeTracksWithPreparePlan(t *testing.T) {
	pipeline := NewPipeline(test.FixtureRepository())
	pipeline.SetFeature(FeatureMergeTracks)
	item := &testPipelineItem{}
	pipeline.AddItem(item)

	commits := make([]*object.Commit, 1)
	commits[0], _ = test.FixtureRepository().CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c",
	))

	// merge tracks with preparePlan=true should work
	err := pipeline.InitializeExt(map[string]any{
		ConfigPipelineCommits: commits,
	}, func(items []PipelineItem) PipelineItem { return items[0] }, true)
	assert.NoError(t, err)

	result, err := pipeline.RunPreparedPlan()
	assert.NoError(t, err)
	assert.NotNil(t, result)
}
