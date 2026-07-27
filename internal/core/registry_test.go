package core

import (
	"os"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/test"
)

func getRegistry() *PipelineItemRegistry {
	return &PipelineItemRegistry{
		provided:     map[string][]reflect.Type{},
		registered:   map[string]reflect.Type{},
		flags:        map[string]reflect.Type{},
		featureFlags: arrayFeatureFlags{Flags: []string{}, Choices: map[string]bool{}},
	}
}

type dummyPipelineItem struct{}

func (item *dummyPipelineItem) Name() string {
	return testDummyItem
}

func (item *dummyPipelineItem) Provides() []string {
	return []string{testDummyItem}
}

func (item *dummyPipelineItem) Requires() []string {
	return []string{}
}

func (item *dummyPipelineItem) Features() []string {
	return []string{testPowerFeature}
}

func (item *dummyPipelineItem) Configure(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	options := [...]ConfigurationOption{{
		Name:        "DummyOption",
		Description: testOptionDescription,
		Flag:        "dummy-option",
		Type:        BoolConfigurationOption,
		Default:     false,
	}}
	return options[:]
}

func (item *dummyPipelineItem) Initialize(repository *git.Repository) error {
	return nil
}

func (item *dummyPipelineItem) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{testDummyItem: nil}, nil
}

func (item *dummyPipelineItem) Fork(n int) []PipelineItem {
	return nil
}

func (item *dummyPipelineItem) Merge(branches []PipelineItem) {
}

type dummyPipelineItem2 struct{}

func (item *dummyPipelineItem2) Name() string {
	return testDummyItemTwo
}

func (item *dummyPipelineItem2) Provides() []string {
	return []string{testDummyItemTwo}
}

func (item *dummyPipelineItem2) Requires() []string {
	return []string{}
}

func (item *dummyPipelineItem2) Features() []string {
	return []string{"other"}
}

func (item *dummyPipelineItem2) Configure(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem2) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem2) ListConfigurationOptions() []ConfigurationOption {
	return []ConfigurationOption{}
}

func (item *dummyPipelineItem2) Initialize(repository *git.Repository) error {
	return nil
}

func (item *dummyPipelineItem2) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{testDummyItemTwo: nil}, nil
}

func (item *dummyPipelineItem2) Fork(n int) []PipelineItem {
	return nil
}

func (item *dummyPipelineItem2) Merge(branches []PipelineItem) {
}

type dummyPipelineItem3 struct{}

func (item *dummyPipelineItem3) Name() string {
	return testDummyItemThree
}

func (item *dummyPipelineItem3) Provides() []string {
	return []string{testDummyItemThree}
}

func (item *dummyPipelineItem3) Requires() []string {
	return []string{testDummyItem}
}

func (item *dummyPipelineItem3) Configure(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem3) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem3) ListConfigurationOptions() []ConfigurationOption {
	return nil
}

func (item *dummyPipelineItem3) Initialize(repository *git.Repository) error {
	return nil
}

func (item *dummyPipelineItem3) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{testDummyItem: nil}, nil
}

func (item *dummyPipelineItem3) Fork(n int) []PipelineItem {
	return nil
}

func (item *dummyPipelineItem3) Merge(branches []PipelineItem) {
}

type dummyPipelineItem4 struct{}

func (item *dummyPipelineItem4) Name() string {
	return "dummy4"
}

func (item *dummyPipelineItem4) Provides() []string {
	return []string{"dummy4"}
}

func (item *dummyPipelineItem4) Requires() []string {
	return []string{testDummyItemThree}
}

func (item *dummyPipelineItem4) Configure(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem4) ConfigureUpstream(facts map[string]any) error {
	return nil
}

func (item *dummyPipelineItem4) ListConfigurationOptions() []ConfigurationOption {
	return nil
}

func (item *dummyPipelineItem4) Initialize(repository *git.Repository) error {
	return nil
}

func (item *dummyPipelineItem4) Consume(deps map[string]any) (map[string]any, error) {
	return map[string]any{testDummyItem: nil}, nil
}

func (item *dummyPipelineItem4) Fork(n int) []PipelineItem {
	return nil
}

func (item *dummyPipelineItem4) Merge(branches []PipelineItem) {
}

func TestRegistrySummon(t *testing.T) {
	reg := getRegistry()
	assert.Empty(t, reg.Summon("whatever"))
	reg.Register(&testPipelineItem{})
	summoned := reg.Summon((&testPipelineItem{}).Provides()[0])
	assert.Len(t, summoned, 1)
	assert.Equal(t, summoned[0].Name(), (&testPipelineItem{}).Name())
	summoned = reg.Summon((&testPipelineItem{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, summoned[0].Name(), (&testPipelineItem{}).Name())
}

func TestRegistryAddFlags(t *testing.T) {
	reg := getRegistry()
	reg.Register(&testPipelineItem{})
	reg.Register(&dummyPipelineItem{})
	testCmd := &cobra.Command{
		Use:   testFeatureName,
		Short: testCommandDescription,
		Long:  ``,
		Args:  cobra.MaximumNArgs(0),
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	facts, deployed, activations := reg.AddFlags(testCmd.Flags())
	assert.Equal(t, map[string][]string{"test-option": {testItemName}}, activations)
	assert.Len(t, facts, 7)
	assert.IsType(t, 0, facts[(&testPipelineItem{}).ListConfigurationOptions()[0].Name])
	assert.IsType(t, true, facts[(&dummyPipelineItem{}).ListConfigurationOptions()[0].Name])
	assert.Contains(t, facts, ConfigPipelineDryRun)
	assert.Contains(t, facts, ConfigPipelineDAGPath)
	assert.Contains(t, facts, ConfigPipelineDumpPlan)
	assert.Contains(t, facts, ConfigPipelineHibernationDistance)
	assert.Len(t, deployed, 1)
	assert.Contains(t, deployed, (&testPipelineItem{}).Name())
	assert.NotNil(t, testCmd.Flags().Lookup((&testPipelineItem{}).Flag()))
	assert.NotNil(t, testCmd.Flags().Lookup("feature"))
	assert.NotNil(t, testCmd.Flags().Lookup("dump-dag"))
	assert.NotNil(t, testCmd.Flags().Lookup("dump-plan"))
	assert.NotNil(t, testCmd.Flags().Lookup("dry-run"))
	assert.NotNil(t, testCmd.Flags().Lookup("hibernation-distance"))
	assert.NotNil(t, testCmd.Flags().Lookup("print-actions"))
	assert.NotNil(t, testCmd.Flags().Lookup(
		(&testPipelineItem{}).ListConfigurationOptions()[0].Flag,
	))
	assert.NotNil(t, testCmd.Flags().Lookup(
		(&dummyPipelineItem{}).ListConfigurationOptions()[0].Flag,
	))
	testCmd.UsageString() // to test that nothing is broken
}

func TestRegistryAddFlagsCompatibilityFactsFollowParsing(t *testing.T) {
	reg := getRegistry()
	reg.Register(&testPipelineItem{})
	reg.Register(&dummyPipelineItem{})
	flags := pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)

	facts, _, _ := reg.AddFlags(flags)
	require.NoError(t, flags.Parse([]string{
		"--test-option=42",
		"--dummy-option",
		"--dump-dag=graph.dot",
		"--dry-run",
	}))

	assert.Equal(t, 42, facts["TestOption"])
	assert.Equal(t, true, facts["DummyOption"])
	assert.Equal(t, "graph.dot", facts[ConfigPipelineDAGPath])
	assert.Equal(t, true, facts[ConfigPipelineDryRun])
}

func TestFlagConfigurationSnapshotCopiesTypedValues(t *testing.T) {
	const (
		intKey   = "int"
		pathKey  = "path"
		floatKey = "float"
	)

	flags := pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)
	configuration := &FlagConfiguration{}
	options := []ConfigurationOption{
		{Name: "bool", Flag: "bool", Type: BoolConfigurationOption, Default: false},
		{Name: intKey, Flag: intKey, Type: IntConfigurationOption, Default: 1},
		{Name: "string", Flag: "string", Type: StringConfigurationOption, Default: "old"},
		{Name: pathKey, Flag: pathKey, Type: PathConfigurationOption, Default: ""},
		{Name: floatKey, Flag: floatKey, Type: FloatConfigurationOption, Default: float32(0.5)},
		{Name: "strings", Flag: "strings", Type: StringsConfigurationOption, Default: []string{"old"}},
	}
	for _, option := range options {
		configuration.bindings = append(
			configuration.bindings,
			addConfigurationFlag(flags, "Test", option),
		)
	}

	require.NoError(t, flags.Parse([]string{
		"--bool",
		"--int=7",
		"--string=new",
		"--path=output.json",
		"--float=1.25",
		"--strings=one,two",
	}))
	first := configuration.Snapshot()

	assert.Equal(t, true, first["bool"])
	assert.Equal(t, 7, first[intKey])
	assert.Equal(t, "new", first["string"])
	assert.Equal(t, "output.json", first[pathKey])
	assert.InDelta(t, 1.25, first[floatKey], 0.0001)
	assert.Equal(t, []string{"one", "two"}, first["strings"])

	first["strings"].([]string)[0] = "mutated"
	second := configuration.Snapshot()
	assert.Equal(t, []string{"one", "two"}, second["strings"])
}

func TestRegistryFeatures(t *testing.T) {
	reg := getRegistry()
	reg.Register(&dummyPipelineItem{})
	reg.Register(&dummyPipelineItem2{})
	testCmd := &cobra.Command{
		Use:   testFeatureName,
		Short: testCommandDescription,
		Long:  ``,
		Args:  cobra.MaximumNArgs(0),
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	reg.AddFlags(testCmd.Flags())
	args := [...]string{testFeatureFlag, "other", testFeatureFlag, testPowerFeature}
	require.NoError(t, testCmd.ParseFlags(args[:]))
	pipeline := NewPipeline(test.FixtureRepository())
	val, _ := pipeline.GetFeature(testPowerFeature)
	assert.False(t, val)
	val, _ = pipeline.GetFeature("other")
	assert.False(t, val)
	pipeline.SetFeaturesFromFlags(reg)
	val, _ = pipeline.GetFeature(testPowerFeature)
	assert.True(t, val)
	val, _ = pipeline.GetFeature("other")
	assert.True(t, val)
}

func TestRegistryFeaturesUnknownFeature(t *testing.T) {
	reg := getRegistry()
	reg.Register(&dummyPipelineItem{})
	testCmd := &cobra.Command{
		Use:   testFeatureName,
		Short: testCommandDescription,
		Long:  ``,
		Args:  cobra.MaximumNArgs(0),
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	reg.AddFlags(testCmd.Flags())
	err := testCmd.ParseFlags([]string{testFeatureFlag, "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered")
}

func TestRegistryCollectAllDependencies(t *testing.T) {
	reg := getRegistry()
	reg.Register(&dummyPipelineItem{})
	reg.Register(&dummyPipelineItem3{})
	reg.Register(&dummyPipelineItem4{})
	assert.Empty(t, reg.CollectAllDependencies(&dummyPipelineItem{}))
	deps := reg.CollectAllDependencies(&dummyPipelineItem4{})
	assert.Len(t, deps, 2)
	assert.Equal(t, deps[0].Name(), (&dummyPipelineItem{}).Name())
	assert.Equal(t, deps[1].Name(), (&dummyPipelineItem3{}).Name())
}

func TestRegistryLeaves(t *testing.T) {
	reg := getRegistry()
	reg.Register(&testPipelineItem{})
	reg.Register(&dependingTestPipelineItem{})
	reg.Register(&dummyPipelineItem{})
	leaves := reg.GetLeaves()
	assert.Len(t, leaves, 2)
	assert.Equal(t, leaves[0].Name(), (&dependingTestPipelineItem{}).Name())
	assert.Equal(t, leaves[1].Name(), (&testPipelineItem{}).Name())
}

func TestRegistryPlumbingItems(t *testing.T) {
	reg := getRegistry()
	reg.Register(&testPipelineItem{})
	reg.Register(&dependingTestPipelineItem{})
	reg.Register(&dummyPipelineItem{})
	plumbing := reg.GetPlumbingItems()
	assert.Len(t, plumbing, 1)
	assert.Equal(t, plumbing[0].Name(), (&dummyPipelineItem{}).Name())
}

func TestRegistryFeaturedItems(t *testing.T) {
	reg := getRegistry()
	reg.Register(&testPipelineItem{})
	reg.Register(&dependingTestPipelineItem{})
	reg.Register(&dummyPipelineItem{})
	reg.Register(&dummyPipelineItem3{})
	reg.Register(&dummyPipelineItem4{})
	featured := reg.GetFeaturedItems()
	assert.Len(t, featured, 1)
	power := featured[testPowerFeature]
	assert.Len(t, power, 5)
	assert.Equal(t, power[0].Name(), (&testPipelineItem{}).Name())
	assert.Equal(t, power[1].Name(), (&dependingTestPipelineItem{}).Name())
	assert.Equal(t, power[2].Name(), (&dummyPipelineItem{}).Name())
	assert.Equal(t, power[3].Name(), (&dummyPipelineItem3{}).Name())
	assert.Equal(t, power[4].Name(), (&dummyPipelineItem4{}).Name())
}

func TestRegistryPathMasquerade(t *testing.T) {
	fs := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)
	var value string
	fs.StringVar(&value, testFeatureName, "", "usage")
	flag := fs.Lookup(testFeatureName)
	PathifyFlagValue(flag)
	assert.Equal(t, configurationStringType, flag.Value.Type())
	require.NoError(t, flag.Value.Set("xxx"))
	assert.Equal(t, "xxx", flag.Value.String())
	EnablePathFlagTypeMasquerade()
	assert.Equal(t, "path", flag.Value.Type())
	assert.Equal(t, "xxx", flag.Value.String())
}
