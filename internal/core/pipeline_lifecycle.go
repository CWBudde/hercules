package core

import (
	"fmt"
	"slices"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/pkg/errors"
)

func (pipeline *Pipeline) logInitializationFailure(cleanReturn *bool) {
	if *cleanReturn {
		return
	}

	remotes, _ := pipeline.repository.Remotes()
	if len(remotes) > 0 {
		pipeline.l.Errorf("Failed to initialize the pipeline on %s", remotes[0].Config().URLs)
	}
}

func (pipeline *Pipeline) validateConfigurationFactTypes(facts map[string]any) error {
	for _, validate := range []func(map[string]any) error{
		validateOptionalFactType[Logger](ConfigLogger),
		validateOptionalFactType[string](ConfigPipelineDAGPath),
		validateOptionalFactType[bool](ConfigPipelineDryRun),
		validateOptionalFactType[[]*object.Commit](ConfigPipelineCommits),
		validateOptionalFactType[bool](ConfigPipelineDumpPlan),
		validateOptionalFactType[int](ConfigPipelineHibernationDistance),
		validateOptionalFactType[bool](ConfigPipelinePrintActions),
	} {
		err := validate(facts)
		if err != nil {
			return err
		}
	}

	for _, item := range pipeline.items {
		for _, option := range item.ListConfigurationOptions() {
			err := validateConfigurationOptionFact(facts, option)
			if err != nil {
				return fmt.Errorf("%s configuration: %w", item.Name(), err)
			}
		}
	}

	return validateSharedFactTypes(facts)
}

func validateConfigurationOptionFact(
	facts map[string]any, option ConfigurationOption,
) error {
	if option.Name == "TicksSinceStart.TickSize" {
		return validateTickSizeFact(facts, option.Name)
	}

	switch option.Type {
	case BoolConfigurationOption:
		return validateOptionalFact[bool](facts, option.Name)
	case IntConfigurationOption:
		return validateOptionalFact[int](facts, option.Name)
	case StringConfigurationOption, PathConfigurationOption:
		return validateOptionalFact[string](facts, option.Name)
	case FloatConfigurationOption:
		return validateOptionalFact[float32](facts, option.Name)
	case StringsConfigurationOption:
		return validateOptionalFact[[]string](facts, option.Name)
	default:
		return fmt.Errorf(
			"%w: configuration option %q has unknown declared type %d",
			ErrInvalidFactType, option.Name, option.Type,
		)
	}
}

func validateOptionalFactType[T any](key string) func(map[string]any) error {
	return func(facts map[string]any) error {
		return validateOptionalFact[T](facts, key)
	}
}

func validateOptionalFact[T any](facts map[string]any, key string) error {
	_, _, err := FactValue[T](facts, key)

	return err
}

func validateSharedFactTypes(facts map[string]any) error {
	for _, validate := range []func(map[string]any) error{
		validateOptionalFactType[IdentityResolver](FactIdentityResolver),
		validateOptionalFactType[FileIdResolver](FactLineHistoryResolver),
		validateOptionalFactType[[]string]("IdentityDetector.ReversedPeopleDict"),
		validateOptionalFactType[map[int][]plumbing.Hash]("TicksSinceStart.Commits"),
		validateOptionalFactType[int](FactMergeHashCount),
	} {
		err := validate(facts)
		if err != nil {
			return err
		}
	}

	return validateTickSizeFact(facts, "TicksSinceStart.TickSize")
}

func validateTickSizeFact(facts map[string]any, key string) error {
	value, exists := facts[key]
	if !exists {
		return nil
	}

	switch value.(type) {
	case int, time.Duration:
		return nil
	default:
		return fmt.Errorf(
			"%w: %q expects int hours or time.Duration, got %T",
			ErrInvalidFactType, key, value,
		)
	}
}

func (pipeline *Pipeline) applyConfigurationFacts(facts map[string]any) error {
	if err := pipeline.applyLoggerFact(facts); err != nil {
		return err
	}
	if err := pipeline.applyBoolConfigurationFacts(facts); err != nil {
		return err
	}
	return pipeline.applyHibernationDistanceFact(facts)
}

func (pipeline *Pipeline) applyLoggerFact(facts map[string]any) error {
	logger, exists, err := FactValue[Logger](facts, ConfigLogger)
	if err != nil {
		return err
	}

	if exists {
		pipeline.l = logger
	} else {
		facts[ConfigLogger] = pipeline.l
	}
	return nil
}

func (pipeline *Pipeline) applyBoolConfigurationFacts(facts map[string]any) error {
	configurations := []struct {
		key    string
		target *bool
	}{
		{ConfigPipelinePrintActions, &pipeline.PrintActions},
		{ConfigPipelineDumpPlan, &pipeline.DumpPlan},
		{ConfigPipelineDryRun, &pipeline.DryRun},
	}
	for _, configuration := range configurations {
		value, exists, err := FactValue[bool](facts, configuration.key)
		if err != nil {
			return err
		}
		if exists {
			*configuration.target = value
		}
	}
	return nil
}

func (pipeline *Pipeline) applyHibernationDistanceFact(facts map[string]any) error {
	distance, exists, err := FactValue[int](facts, ConfigPipelineHibernationDistance)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if distance < 0 {
		err := fmt.Errorf("%w (got %d)", errNegativeHibernationDistance, distance)
		pipeline.l.Error(err)
		return err
	}
	pipeline.HibernationDistance = distance
	return nil
}

func (pipeline *Pipeline) prepareRun(facts map[string]any, mergeTracks bool) error {
	commits, ok := facts[ConfigPipelineCommits].([]*object.Commit)
	if !ok {
		pipeline.lifecycle.preparedRun = nil
		return fmt.Errorf("%w: %s is not available", ErrNoCommits, ConfigPipelineCommits)
	}

	prepared := &preparedRun{commitCount: len(commits)}

	var err error

	prepared.plan, prepared.mergeHashCount, err = prepareRunPlan(
		commits, pipeline.HibernationDistance, mergeTracks,
	)
	if err != nil {
		pipeline.lifecycle.preparedRun = nil
		return err
	}

	if mergeTracks {
		facts[FactMergeHashCount] = prepared.mergeHashCount
	}

	pipeline.lifecycle.preparedRun = prepared

	return nil
}

func (pipeline *Pipeline) configureItems(facts map[string]any) error {
	err := validateSharedFactTypes(facts)
	if err != nil {
		return err
	}

	for _, item := range pipeline.items {
		err = item.Configure(facts)
		if err != nil {
			return errors.Wrapf(err, "%s failed to configure", item.Name())
		}

		err = validateSharedFactTypes(facts)
		if err != nil {
			return errors.Wrapf(err, "%s produced an invalid fact", item.Name())
		}
	}

	return nil
}

func (pipeline *Pipeline) initializeItems(facts map[string]any) error {
	for _, item := range slices.Backward(pipeline.items) {
		err := item.ConfigureUpstream(facts)
		if err != nil {
			return errors.Wrapf(err, "%s failed to configure upstream", item.Name())
		}
	}

	for _, item := range pipeline.items {
		err := item.Initialize(pipeline.repository)
		if err != nil {
			return errors.Wrapf(err, "%s failed to initialize", item.Name())
		}
	}

	pipeline.lifecycle.initializedResources = disposableItems(pipeline.items)

	return nil
}

func disposableItems(items []PipelineItem) []DisposablePipelineItem {
	resources := make([]DisposablePipelineItem, 0)

	for _, item := range items {
		if disposable, ok := item.(DisposablePipelineItem); ok {
			resources = append(resources, disposable)
		}
	}

	return resources
}

func (pipeline *Pipeline) disposeItems(items []PipelineItem) {
	for _, disposable := range disposableItems(items) {
		disposable.Dispose()
	}
}

func (pipeline *Pipeline) disposeInitializedResources() {
	for _, disposable := range pipeline.lifecycle.initializedResources {
		disposable.Dispose()
	}

	pipeline.lifecycle.initializedResources = nil
}
