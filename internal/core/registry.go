package core

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var errFeatureNotRegistered = errors.New("is not registered")

// PipelineItemRegistry contains all the known PipelineItem-s.
type PipelineItemRegistry struct {
	provided               map[string][]reflect.Type
	registered             map[string]reflect.Type
	preferred              map[string]struct{}
	flags                  map[string]reflect.Type
	featureFlags           arrayFeatureFlags
	pathFlagTypeMasquerade bool
}

// Register adds another PipelineItem to the registry.
func (registry *PipelineItemRegistry) Register(example PipelineItem) {
	registry.RegisterPreferred(example, false)
}

func (registry *PipelineItemRegistry) RegisterPreferred(example PipelineItem, preferred bool) {
	itemType := reflect.TypeOf(example)
	exampleName := example.Name()

	registry.registered[exampleName] = itemType
	if fpi, ok := example.(LeafPipelineItem); ok {
		registry.flags[fpi.Flag()] = itemType
		if preferred {
			registry.preferred[exampleName] = struct{}{}
		} else {
			delete(registry.preferred, exampleName)
		}
	}

	for _, dep := range example.Provides() {
		providers := registry.provided[dep]
		if preferred && len(providers) > 0 {
			providers = append(providers, providers[0])
			providers[0] = itemType
		} else {
			providers = append(providers, itemType)
		}

		registry.provided[dep] = providers
	}
}

// RegisterPipelineItem registers an item in the global registry and returns a
// value suitable for declaration-time registration.
func RegisterPipelineItem(example PipelineItem) struct{} {
	Registry.Register(example)

	return struct{}{}
}

// RegisterPreferredPipelineItem registers a preferred item in the global
// registry and returns a value suitable for declaration-time registration.
func RegisterPreferredPipelineItem(example PipelineItem, preferred bool) struct{} {
	Registry.RegisterPreferred(example, preferred)

	return struct{}{}
}

// Summon searches for PipelineItem-s which provide the specified entity or named after
// the specified string. It materializes all the found types and returns them.
func (registry *PipelineItemRegistry) Summon(providesOrNames ...string) []PipelineItem {
	if registry.provided == nil {
		return nil
	}

	var items []PipelineItem

	for _, providesOrName := range providesOrNames {
		ts := registry.provided[providesOrName]
		for _, t := range ts {
			items = append(items, mustPipelineItem(reflect.New(t.Elem()).Interface()))
		}

		if t, exists := registry.registered[providesOrName]; exists {
			items = append(items, mustPipelineItem(reflect.New(t.Elem()).Interface()))
		}
	}

	return items
}

// GetLeaves returns all LeafPipelineItem-s registered.
func (registry *PipelineItemRegistry) GetLeaves() []LeafPipelineItem {
	keys := make([]string, 0, len(registry.flags))
	for key := range registry.flags {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	items := make([]LeafPipelineItem, 0, len(keys))
	for _, key := range keys {
		items = append(items, mustLeafPipelineItem(reflect.New(registry.flags[key].Elem()).Interface()))
	}

	return items
}

// GetPlumbingItems returns all non-LeafPipelineItem-s registered.
func (registry *PipelineItemRegistry) GetPlumbingItems() []PipelineItem {
	keys := make([]string, 0, len(registry.registered))
	for key := range registry.registered {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	items := make([]PipelineItem, 0, len(keys))
	for _, key := range keys {
		iface := reflect.New(registry.registered[key].Elem()).Interface()
		if _, ok := iface.(LeafPipelineItem); !ok {
			items = append(items, mustPipelineItem(iface))
		}
	}

	return items
}

func mustPipelineItem(value any) PipelineItem {
	item, ok := value.(PipelineItem)
	if !ok {
		panic("registered type does not implement PipelineItem")
	}

	return item
}

func mustLeafPipelineItem(value any) LeafPipelineItem {
	item, ok := value.(LeafPipelineItem)
	if !ok {
		panic("registered leaf type does not implement LeafPipelineItem")
	}

	return item
}

// GetFeaturedItems returns all FeaturedPipelineItem-s registered.
func (registry *PipelineItemRegistry) GetFeaturedItems() map[string][]PipelineItem {
	features := map[string][]PipelineItem{}

	for _, t := range registry.registered {
		item := mustPipelineItem(reflect.New(t.Elem()).Interface())
		deps := registry.CollectAllDependencies(item)
		deps = append(deps, item)
		depFeatures := map[string]bool{}

		for _, dep := range deps {
			if fiFace, ok := dep.(FeaturedPipelineItem); ok {
				for _, f := range fiFace.Features() {
					depFeatures[f] = true
				}
			}
		}

		for f := range depFeatures {
			features[f] = append(features[f], item)
		}
	}

	for _, vals := range features {
		sort.Slice(vals, func(i, j int) bool {
			return vals[i].Name() < vals[j].Name()
		})
	}

	return features
}

// CollectAllDependencies recursively builds the list of all the items on which the specified item
// depends.
func (registry *PipelineItemRegistry) CollectAllDependencies(item PipelineItem) []PipelineItem {
	deps := map[string]PipelineItem{}

	for stack := []PipelineItem{item}; len(stack) > 0; {
		head := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for _, reqID := range head.Requires() {
			req := registry.Summon(reqID)[0]
			if _, exists := deps[reqID]; !exists {
				deps[reqID] = req
				stack = append(stack, req)
			}
		}
	}

	result := make([]PipelineItem, 0, len(deps))
	for _, val := range deps {
		result = append(result, val)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})

	return result
}

// EnablePathFlagTypeMasquerade changes the type of all "path" command line arguments from "string"
// to "path". This operation cannot be canceled and is intended to be used for better --help output.
func EnablePathFlagTypeMasquerade() {
	Registry.pathFlagTypeMasquerade = true
}

type pathValue struct {
	origin   pflag.Value
	registry *PipelineItemRegistry
}

func wrapPathValue(val pflag.Value) pflag.Value {
	return &pathValue{origin: val, registry: Registry}
}

func (s *pathValue) Set(val string) error {
	err := s.origin.Set(val)
	if err != nil {
		return fmt.Errorf("set path value: %w", err)
	}

	return nil
}

func (s *pathValue) Type() string {
	if s.registry.pathFlagTypeMasquerade {
		return "path"
	}

	return configurationStringType
}

func (s *pathValue) String() string {
	return s.origin.String()
}

// PathifyFlagValue changes the type of string command line argument to "path".
func PathifyFlagValue(flag *pflag.Flag) {
	flag.Value = wrapPathValue(flag.Value)
}

type arrayFeatureFlags struct {
	// Flags contains the features activated through the command line.
	Flags []string
	// Choices contains all registered features.
	Choices map[string]bool
}

func (acf *arrayFeatureFlags) String() string {
	return strings.Join(acf.Flags, ", ")
}

func (acf *arrayFeatureFlags) Set(value string) error {
	if _, exists := acf.Choices[value]; !exists {
		return fmt.Errorf("feature %q %w", value, errFeatureNotRegistered)
	}

	acf.Flags = append(acf.Flags, value)

	return nil
}

func (acf *arrayFeatureFlags) Type() string {
	return configurationStringType
}

type flagBinding struct {
	name     string
	flag     *pflag.Flag
	snapshot func() any
}

// FlagConfiguration owns the typed pointers registered with a pflag set.
// Snapshot must be called after flag parsing to obtain a plain configuration
// map suitable for Pipeline.Initialize.
type FlagConfiguration struct {
	bindings []flagBinding
}

// Snapshot copies the current values of all registered configuration flags.
// The returned map does not alias pflag's typed storage.
func (configuration *FlagConfiguration) Snapshot() map[string]any {
	facts := make(map[string]any, len(configuration.bindings))
	for _, binding := range configuration.bindings {
		facts[binding.name] = cloneConfigurationValue(binding.snapshot())
	}

	return facts
}

func cloneConfigurationValue(value any) any {
	if stringsValue, ok := value.([]string); ok {
		return append([]string(nil), stringsValue...)
	}

	return value
}

// AddFlags inserts the cmdline options from PipelineItem.ListConfigurationOptions(),
// FeaturedPipelineItem().Features() and LeafPipelineItem.Flag() into the global "flag" parser
// built into the Go runtime.
// Returns the "facts" which can be fed into PipelineItem.Configure() and the dictionary of
// runnable analysis (LeafPipelineItem) choices. E.g. if "BurndownAnalysis" was activated
// through "-burndown" cmdline argument, this mapping would contain ["BurndownAnalysis"] = *true.
//
// Deprecated: use AddFlagsWithConfiguration and call FlagConfiguration.Snapshot
// after parsing. This compatibility adapter keeps the returned facts synchronized
// without relying on the runtime representation of interface values.
func (registry *PipelineItemRegistry) AddFlags(
	flagSet *pflag.FlagSet,
) (map[string]any, map[string]*bool, map[string][]string) {
	configuration, deployed, activations := registry.AddFlagsWithConfiguration(flagSet)
	facts := configuration.Snapshot()
	configuration.synchronizeCompatibilityFacts(facts)

	return facts, deployed, activations
}

// AddFlagsWithConfiguration registers all pipeline flags and retains their
// normal typed pflag pointers. Call Snapshot after parsing to build the facts
// passed to Pipeline.Initialize.
func (registry *PipelineItemRegistry) AddFlagsWithConfiguration(
	flagSet *pflag.FlagSet,
) (*FlagConfiguration, map[string]*bool, map[string][]string) {
	configuration := &FlagConfiguration{}
	deployed := map[string]*bool{}
	activations := map[string][]string{}
	reusableOptions := map[string]ConfigurationOption{}

	for name, it := range registry.registered {
		itemIface := reflect.New(it.Elem()).Interface()
		registry.addItemFlags(
			flagSet, name, itemIface, configuration, deployed, activations, reusableOptions,
		)
	}

	addPipelineFlags(flagSet, configuration)
	registry.addFeatureFlag(flagSet)

	return configuration, deployed, activations
}

func (registry *PipelineItemRegistry) addFeatureFlag(flagSet *pflag.FlagSet) {
	features := make([]string, 0, len(registry.featureFlags.Choices))
	for f := range registry.featureFlags.Choices {
		features = append(features, f)
	}

	sort.Strings(features)
	featureHelp := fmt.Sprintf("Enables the items which depend on the specified features. Can be specified "+
		"multiple times. Available features: [%s] (see --feature below).",
		strings.Join(features, ", "))
	flagSet.Var(&registry.featureFlags, "feature",
		featureHelp)
}

func (registry *PipelineItemRegistry) addItemFlags(
	flagSet *pflag.FlagSet,
	name string,
	itemIface any,
	configuration *FlagConfiguration,
	deployed map[string]*bool,
	activations map[string][]string,
	reusableOptions map[string]ConfigurationOption,
) {
	if featured, ok := itemIface.(FeaturedPipelineItem); ok {
		for _, feature := range featured.Features() {
			registry.featureFlags.Choices[feature] = true
		}
	}

	leafFlag := registry.addLeafFlag(flagSet, itemIface, deployed)

	addActivation := func(optionFlag string) {
		registry.addFlagActivation(flagSet, name, leafFlag, optionFlag, activations)
	}
	for _, option := range mustPipelineItem(itemIface).ListConfigurationOptions() {
		if option.Shared && registry.reuseOption(name, option, reusableOptions, addActivation) {
			continue
		}

		configuration.bindings = append(
			configuration.bindings,
			addConfigurationFlag(flagSet, name, option),
		)
		addActivation(option.Flag)
	}
}

func (registry *PipelineItemRegistry) addLeafFlag(
	flagSet *pflag.FlagSet, itemIface any, deployed map[string]*bool,
) string {
	leaf, ok := itemIface.(LeafPipelineItem)
	if !ok {
		return ""
	}

	deployed[leaf.Name()] = flagSet.Bool(
		leaf.Flag(), false, fmt.Sprintf("Runs %s analysis.", leaf.Name()),
	)

	return leaf.Flag()
}

func (registry *PipelineItemRegistry) addFlagActivation(
	flagSet *pflag.FlagSet,
	itemName, leafFlag, optionFlag string,
	activations map[string][]string,
) {
	if leafFlag == "" {
		return
	}

	flagName := flagSet.Lookup(optionFlag).Name

	list := activations[flagName]
	if _, preferred := registry.preferred[itemName]; !preferred || len(list) == 0 {
		activations[flagName] = append(list, itemName)
	} else {
		activations[flagName] = append([]string{itemName}, list...)
	}
}

func (registry *PipelineItemRegistry) reuseOption(
	itemName string,
	option ConfigurationOption,
	reusableOptions map[string]ConfigurationOption,
	addActivation func(string),
) bool {
	optionCopy := option

	reused, exists := reusableOptions[option.Flag]
	if !exists {
		optionCopy.Description = itemName
		reusableOptions[option.Flag] = optionCopy

		return false
	}

	optionCopy.Description = reused.Description
	if reflect.DeepEqual(reused, optionCopy) {
		addActivation(option.Flag)

		return true
	}

	message := fmt.Sprintf(
		"Param conflict of the option %s from: %s, %s", option.Flag, reused.Description, itemName,
	)
	// Registry warnings have no caller error channel; stdout is best effort.
	_, _ = fmt.Fprintln(os.Stdout, message)
	panic(message)
}

func addConfigurationFlag(
	flagSet *pflag.FlagSet, itemName string, option ConfigurationOption,
) flagBinding {
	help := fmt.Sprintf("%s [%s]", option.Description, itemName)

	switch option.Type {
	case BoolConfigurationOption:
		value := flagSet.Bool(option.Flag, configurationDefault[bool](option), help)
		return newFlagBinding(option.Name, flagSet.Lookup(option.Flag), value)
	case IntConfigurationOption:
		value := flagSet.Int(option.Flag, configurationDefault[int](option), help)
		return newFlagBinding(option.Name, flagSet.Lookup(option.Flag), value)
	case StringConfigurationOption, PathConfigurationOption:
		value := flagSet.String(option.Flag, configurationDefault[string](option), help)
		if option.Type == PathConfigurationOption {
			err := cobra.MarkFlagFilename(flagSet, option.Flag)
			if err != nil {
				panic(err)
			}

			PathifyFlagValue(flagSet.Lookup(option.Flag))
		}

		return newFlagBinding(option.Name, flagSet.Lookup(option.Flag), value)
	case FloatConfigurationOption:
		value := flagSet.Float32(
			option.Flag, configurationDefault[float32](option), help,
		)

		return newFlagBinding(option.Name, flagSet.Lookup(option.Flag), value)
	case StringsConfigurationOption:
		value := flagSet.StringSlice(
			option.Flag, configurationDefault[[]string](option), help,
		)

		return newFlagBinding(option.Name, flagSet.Lookup(option.Flag), value)
	}

	panic(fmt.Sprintf("invalid configuration option type %d", option.Type))
}

func newFlagBinding[T any](name string, flag *pflag.Flag, value *T) flagBinding {
	return flagBinding{
		name: name,
		flag: flag,
		snapshot: func() any {
			return *value
		},
	}
}

func addPipelineFlags(flagSet *pflag.FlagSet, configuration *FlagConfiguration) {
	addPipelineStringFlag(
		flagSet, configuration, ConfigPipelineDAGPath, "dump-dag",
		"Write the pipeline DAG to a Graphviz file.",
	)
	PathifyFlagValue(flagSet.Lookup("dump-dag"))
	addPipelineBoolFlag(
		flagSet, configuration, ConfigPipelineDryRun, "dry-run",
		"Do not run any analyses - only resolve the DAG. Useful for --dump-dag or --dump-plan.",
	)
	addPipelineBoolFlag(
		flagSet, configuration, ConfigPipelineDumpPlan, "dump-plan",
		"Print the pipeline execution plan to stderr.",
	)
	addPipelineIntFlag(
		flagSet, configuration, ConfigPipelineHibernationDistance, "hibernation-distance",
		"Minimum number of actions between two sequential usages of a branch to activate "+
			"the hibernation optimization (cpu-memory trade-off). 0 disables.",
	)
	addPipelineBoolFlag(
		flagSet, configuration, ConfigPipelinePrintActions, "print-actions",
		"Print the executed actions to stderr.",
	)
}

func addPipelineStringFlag(
	flagSet *pflag.FlagSet, configuration *FlagConfiguration, key, name, help string,
) {
	value := flagSet.String(name, "", help)
	configuration.bindings = append(
		configuration.bindings,
		newFlagBinding(key, flagSet.Lookup(name), value),
	)
}

func addPipelineBoolFlag(
	flagSet *pflag.FlagSet, configuration *FlagConfiguration, key, name, help string,
) {
	value := flagSet.Bool(name, false, help)
	configuration.bindings = append(
		configuration.bindings,
		newFlagBinding(key, flagSet.Lookup(name), value),
	)
}

func addPipelineIntFlag(
	flagSet *pflag.FlagSet, configuration *FlagConfiguration, key, name, help string,
) {
	value := flagSet.Int(name, 0, help)
	configuration.bindings = append(
		configuration.bindings,
		newFlagBinding(key, flagSet.Lookup(name), value),
	)
}

func (configuration *FlagConfiguration) synchronizeCompatibilityFacts(facts map[string]any) {
	for index := range configuration.bindings {
		binding := &configuration.bindings[index]
		synchronize := func() {
			facts[binding.name] = cloneConfigurationValue(binding.snapshot())
		}

		if sliceValue, ok := binding.flag.Value.(pflag.SliceValue); ok {
			binding.flag.Value = &synchronizedSliceValue{
				synchronizedValue: synchronizedValue{
					origin:      binding.flag.Value,
					synchronize: synchronize,
				},
				slice: sliceValue,
			}
		} else {
			binding.flag.Value = &synchronizedValue{
				origin:      binding.flag.Value,
				synchronize: synchronize,
			}
		}
	}
}

type synchronizedValue struct {
	origin      pflag.Value
	synchronize func()
}

func (value *synchronizedValue) Set(raw string) error {
	err := value.origin.Set(raw)
	if err != nil {
		return fmt.Errorf("set synchronized flag: %w", err)
	}

	value.synchronize()

	return nil
}

func (value *synchronizedValue) Type() string {
	return value.origin.Type()
}

func (value *synchronizedValue) String() string {
	return value.origin.String()
}

type synchronizedSliceValue struct {
	synchronizedValue

	slice pflag.SliceValue
}

func (value *synchronizedSliceValue) Append(raw string) error {
	err := value.slice.Append(raw)
	if err != nil {
		return fmt.Errorf("append synchronized flag value: %w", err)
	}

	value.synchronize()

	return nil
}

func (value *synchronizedSliceValue) Replace(raw []string) error {
	err := value.slice.Replace(raw)
	if err != nil {
		return fmt.Errorf("replace synchronized flag value: %w", err)
	}

	value.synchronize()

	return nil
}

func (value *synchronizedSliceValue) GetSlice() []string {
	return value.slice.GetSlice()
}

// Registry contains all known pipeline item types.
var Registry = &PipelineItemRegistry{
	provided:     map[string][]reflect.Type{},
	registered:   map[string]reflect.Type{},
	preferred:    map[string]struct{}{},
	flags:        map[string]reflect.Type{},
	featureFlags: arrayFeatureFlags{Flags: []string{}, Choices: map[string]bool{}},
}
