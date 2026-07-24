package core

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// PipelineItemRegistry contains all the known PipelineItem-s.
type PipelineItemRegistry struct {
	provided     map[string][]reflect.Type
	registered   map[string]reflect.Type
	preferred    map[string]struct{}
	flags        map[string]reflect.Type
	featureFlags arrayFeatureFlags
}

// Register adds another PipelineItem to the registry.
func (registry *PipelineItemRegistry) Register(example PipelineItem) {
	registry.RegisterPreferred(example, false)
}

func (registry *PipelineItemRegistry) RegisterPreferred(example PipelineItem, preferred bool) {
	t := reflect.TypeOf(example)
	exampleName := example.Name()

	registry.registered[exampleName] = t
	if fpi, ok := example.(LeafPipelineItem); ok {
		registry.flags[fpi.Flag()] = t
		if preferred {
			registry.preferred[exampleName] = struct{}{}
		} else {
			delete(registry.preferred, exampleName)
		}
	}

	for _, dep := range example.Provides() {
		ts := registry.provided[dep]
		if preferred && len(ts) > 0 {
			ts = append(ts, ts[0])
			ts[0] = t
		} else {
			ts = append(ts, t)
		}

		registry.provided[dep] = ts
	}
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
			items = append(items, reflect.New(t.Elem()).Interface().(PipelineItem))
		}

		if t, exists := registry.registered[providesOrName]; exists {
			items = append(items, reflect.New(t.Elem()).Interface().(PipelineItem))
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
		items = append(items, reflect.New(registry.flags[key].Elem()).Interface().(LeafPipelineItem))
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
			items = append(items, iface.(PipelineItem))
		}
	}

	return items
}

// GetFeaturedItems returns all FeaturedPipelineItem-s registered.
func (registry *PipelineItemRegistry) GetFeaturedItems() map[string][]PipelineItem {
	features := map[string][]PipelineItem{}

	for _, t := range registry.registered {
		item := reflect.New(t.Elem()).Interface().(PipelineItem)
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

var pathFlagTypeMasquerade bool

// EnablePathFlagTypeMasquerade changes the type of all "path" command line arguments from "string"
// to "path". This operation cannot be canceled and is intended to be used for better --help output.
func EnablePathFlagTypeMasquerade() {
	pathFlagTypeMasquerade = true
}

type pathValue struct {
	origin pflag.Value
}

func wrapPathValue(val pflag.Value) pflag.Value {
	return &pathValue{val}
}

func (s *pathValue) Set(val string) error {
	return s.origin.Set(val)
}

func (s *pathValue) Type() string {
	if pathFlagTypeMasquerade {
		return "path"
	}

	return "string"
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
		return fmt.Errorf("feature \"%s\" is not registered", value)
	}

	acf.Flags = append(acf.Flags, value)

	return nil
}

func (acf *arrayFeatureFlags) Type() string {
	return "string"
}

// AddFlags inserts the cmdline options from PipelineItem.ListConfigurationOptions(),
// FeaturedPipelineItem().Features() and LeafPipelineItem.Flag() into the global "flag" parser
// built into the Go runtime.
// Returns the "facts" which can be fed into PipelineItem.Configure() and the dictionary of
// runnable analysis (LeafPipelineItem) choices. E.g. if "BurndownAnalysis" was activated
// through "-burndown" cmdline argument, this mapping would contain ["BurndownAnalysis"] = *true.
func (registry *PipelineItemRegistry) AddFlags(flagSet *pflag.FlagSet) (
	flags map[string]any, deployed map[string]*bool, activations map[string][]string,
) {
	flags = map[string]any{}
	deployed = map[string]*bool{}
	activations = map[string][]string{}
	reusableOptions := map[string]ConfigurationOption{}

	for name, it := range registry.registered {
		itemIface := reflect.New(it.Elem()).Interface()
		registry.addItemFlags(
			flagSet, name, itemIface, flags, deployed, activations, reusableOptions,
		)
	}

	addPipelineFlags(flagSet, flags)
	registry.addFeatureFlag(flagSet)

	return flags, deployed, activations
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
	flags map[string]any,
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
	for _, option := range itemIface.(PipelineItem).ListConfigurationOptions() {
		if option.Shared && registry.reuseOption(name, option, reusableOptions, addActivation) {
			continue
		}

		flags[option.Name] = addConfigurationFlag(flagSet, name, option)
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
	fmt.Println(message)
	panic(message)
}

func addConfigurationFlag(flagSet *pflag.FlagSet, itemName string, option ConfigurationOption) any {
	help := fmt.Sprintf("%s [%s]", option.Description, itemName)
	var value any
	valuePointer := func() unsafe.Pointer {
		return unsafe.Add(unsafe.Pointer(&value), unsafe.Sizeof(&value))
	}

	switch option.Type {
	case BoolConfigurationOption:
		value = any(true)
		*(**bool)(valuePointer()) = flagSet.Bool(option.Flag, option.Default.(bool), help)
	case IntConfigurationOption:
		value = any(0)
		*(**int)(valuePointer()) = flagSet.Int(option.Flag, option.Default.(int), help)
	case StringConfigurationOption, PathConfigurationOption:
		value = any("")

		*(**string)(valuePointer()) = flagSet.String(option.Flag, option.Default.(string), help)
		if option.Type == PathConfigurationOption {
			err := cobra.MarkFlagFilename(flagSet, option.Flag)
			if err != nil {
				panic(err)
			}

			PathifyFlagValue(flagSet.Lookup(option.Flag))
		}
	case FloatConfigurationOption:
		value = any(float32(0))
		*(**float32)(valuePointer()) = flagSet.Float32(option.Flag, option.Default.(float32), help)
	case StringsConfigurationOption:
		value = any([]string{})
		*(**[]string)(valuePointer()) = flagSet.StringSlice(option.Flag, option.Default.([]string), help)
	}

	return value
}

func addPipelineFlags(flagSet *pflag.FlagSet, flags map[string]any) {
	addPipelineStringFlag(
		flagSet, flags, ConfigPipelineDAGPath, "dump-dag", "Write the pipeline DAG to a Graphviz file.",
	)
	PathifyFlagValue(flagSet.Lookup("dump-dag"))
	addPipelineBoolFlag(
		flagSet, flags, ConfigPipelineDryRun, "dry-run",
		"Do not run any analyses - only resolve the DAG. Useful for --dump-dag or --dump-plan.",
	)
	addPipelineBoolFlag(
		flagSet, flags, ConfigPipelineDumpPlan, "dump-plan", "Print the pipeline execution plan to stderr.",
	)
	addPipelineIntFlag(
		flagSet, flags, ConfigPipelineHibernationDistance, "hibernation-distance",
		"Minimum number of actions between two sequential usages of a branch to activate "+
			"the hibernation optimization (cpu-memory trade-off). 0 disables.",
	)
	addPipelineBoolFlag(
		flagSet, flags, ConfigPipelinePrintActions, "print-actions", "Print the executed actions to stderr.",
	)
}

func addPipelineStringFlag(
	flagSet *pflag.FlagSet, flags map[string]any, key, name, help string,
) {
	value := any("")
	*(**string)(unsafe.Add(unsafe.Pointer(&value), unsafe.Sizeof(&value))) = flagSet.String(name, "", help)
	flags[key] = value
}

func addPipelineBoolFlag(
	flagSet *pflag.FlagSet, flags map[string]any, key, name, help string,
) {
	value := any(true)
	*(**bool)(unsafe.Add(unsafe.Pointer(&value), unsafe.Sizeof(&value))) = flagSet.Bool(name, false, help)
	flags[key] = value
}

func addPipelineIntFlag(
	flagSet *pflag.FlagSet, flags map[string]any, key, name, help string,
) {
	value := any(0)
	*(**int)(unsafe.Add(unsafe.Pointer(&value), unsafe.Sizeof(&value))) = flagSet.Int(name, 0, help)
	flags[key] = value
}

// Registry contains all known pipeline item types.
var Registry = &PipelineItemRegistry{
	provided:     map[string][]reflect.Type{},
	registered:   map[string]reflect.Type{},
	preferred:    map[string]struct{}{},
	flags:        map[string]reflect.Type{},
	featureFlags: arrayFeatureFlags{Flags: []string{}, Choices: map[string]bool{}},
}
