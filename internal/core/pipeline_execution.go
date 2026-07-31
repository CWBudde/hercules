package core

import (
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/pkg/errors"
)

//nolint:funlen // Keep the execution transaction and its defers visible in one place.
func (pipeline *Pipeline) runPlan(plan []runAction, commitCount, mergeHashCount int) (map[LeafPipelineItem]any, error) {
	if len(plan) == 0 {
		return nil, ErrNoCommits
	}

	if pipeline.HibernationDistance > 0 {
		gcPercentMu.Lock()

		previousGCPercent := debug.SetGCPercent(20)
		defer func() {
			debug.SetGCPercent(previousGCPercent)
			gcPercentMu.Unlock()
		}()
	}

	startRunTime := time.Now()

	cleanReturn := false
	defer pipeline.reportRunFailure(&cleanReturn)

	onProgress := pipeline.progressCallback()
	pipeline.dumpRunPlan(plan)
	progressSteps := len(plan) + 2

	state := pipeline.newRunState(mergeHashCount)
	defer func() {
		state.dispose()

		pipeline.lifecycle.initializedResources = nil
	}()

	err := state.executePlan(plan, progressSteps, onProgress)
	if err != nil {
		return nil, err
	}

	if !pipeline.DryRun && state.commitIndex != commitCount {
		return nil, fmt.Errorf("%w: consumed %d commits, expected %d",
			errPlanCommitCountMismatch, state.commitIndex, commitCount)
	}

	onProgress(len(plan)+1, progressSteps, MessageFinalize)

	result := state.finalize()

	onProgress(progressSteps, progressSteps, "")

	consumedCommits := state.commitIndex
	if pipeline.DryRun {
		consumedCommits = commitCount
	}

	result[nil] = &CommonAnalysisResult{
		BeginTime:      plan[0].Commit.Committer.When.Unix(),
		EndTime:        state.newestTime,
		CommitsNumber:  consumedCommits,
		RunTime:        time.Since(startRunTime),
		RunTimePerItem: state.runTimePerItem,
	}
	cleanReturn = true

	return result, nil
}

func (pipeline *Pipeline) reportRunFailure(cleanReturn *bool) {
	if *cleanReturn {
		return
	}

	remotes, _ := pipeline.repository.Remotes()
	if len(remotes) > 0 {
		pipeline.l.Errorf("Failed to run the pipeline on %s", remotes[0].Config().URLs)
	}
}

func (pipeline *Pipeline) progressCallback() func(int, int, string) {
	if pipeline.OnProgress != nil {
		return pipeline.OnProgress
	}

	return func(int, int, string) {}
}

func (pipeline *Pipeline) dumpRunPlan(plan []runAction) {
	if !pipeline.DumpPlan {
		return
	}

	for _, action := range plan {
		printAction(pipeline.output, action)
	}
}

func (pipeline *Pipeline) newRunState(mergeHashCount int) pipelineRunState {
	state := pipelineRunState{
		pipeline:       pipeline,
		branches:       map[int][]PipelineItem{},
		runTimePerItem: map[string]float64{},
		mergeHashCount: mergeHashCount,
		seenDisposable: map[DisposablePipelineItem]struct{}{},

		hibernatedBranches: map[int]struct{}{},
		hibernatedItems:    map[PipelineItem]struct{}{},
	}
	state.trackItems(pipeline.items)

	if !pipeline.DryRun {
		// We will need rootClone if there is more than one root branch.
		state.rootClone = cloneItems(pipeline.items, 1)[0]
		state.trackItems(state.rootClone)
	}

	return state
}

func (state *pipelineRunState) executePlan(plan []runAction, progressSteps int,
	onProgress func(int, int, string),
) error {
	for index, step := range plan {
		onProgress(index+1, progressSteps, step.String())

		if state.pipeline.DryRun {
			continue
		}

		if state.pipeline.PrintActions {
			printAction(state.pipeline.output, step)
		}

		if index > 0 && index%100 == 0 && state.pipeline.HibernationDistance > 0 {
			debug.FreeOSMemory()
		}

		err := state.executeStep(plan, index, step)
		if err != nil {
			return err
		}
	}

	return nil
}

func (state *pipelineRunState) finalize() map[LeafPipelineItem]any {
	result := map[LeafPipelineItem]any{}
	if state.pipeline.DryRun {
		return result
	}

	for index, item := range getMasterBranch(state.branches) {
		if leaf, ok := item.(LeafPipelineItem); ok {
			registeredLeaf, registered := state.pipeline.items[index].(LeafPipelineItem)
			if registered {
				result[registeredLeaf] = leaf.Finalize()
			}
		}
	}

	return result
}

func (state *pipelineRunState) executeStep(plan []runAction, index int, step runAction) error {
	firstItem := step.Items[0]
	switch step.Action {
	case runActionCommit:
		return state.consumeCommit(plan, index, firstItem, step)
	case runActionFork:
		state.fork(firstItem, step.Items)
	case runActionMerge:
		state.merge(step.Items)
	case runActionEmerge:
		state.emerge(firstItem)
	case runActionDelete:
		delete(state.branches, firstItem)
	case runActionHibernate:
		return state.changeHibernation(step.Items, false)
	case runActionBoot:
		return state.changeHibernation(step.Items, true)
	}

	return nil
}

func (state *pipelineRunState) consumeCommit(plan []runAction, index, firstItem int, step runAction) error {
	dependencies := map[string]any{
		DependencyCommit:         step.Commit,
		DependencyIndex:          state.commitIndex,
		DependencyIsMerge:        isMergeAction(plan, index, step.Commit.Hash),
		DependencyIsMergeReplica: step.MergeReplica,
	}
	if state.mergeHashCount >= 0 {
		dependencies[DependencyNextMerge] = step.NextMerge
	}

	for _, item := range state.branches[firstItem] {
		startTime := time.Now()
		update, err := item.Consume(dependencies)

		state.runTimePerItem[item.Name()] += time.Since(startTime).Seconds()
		if err != nil {
			state.pipeline.l.Errorf("%s failed on commit #%d (%d) %s: %v\n",
				item.Name(), state.commitIndex+1, index+1, step.Commit.Hash.String(), err)

			return fmt.Errorf("%s failed to consume commit: %w", item.Name(), err)
		}

		for _, key := range item.Provides() {
			value, ok := update[key]
			if !ok {
				err := fmt.Errorf("%s did not return %s: %w",
					item.Name(), key, errors.New("consume output missing"))
				state.pipeline.l.Critical(err)

				return err
			}

			dependencies[key] = value
		}
	}

	commitTime := step.Commit.Committer.When.Unix()
	if commitTime > state.newestTime {
		state.newestTime = commitTime
	}

	// A replica is the same commit seen again on another parent branch, so it must not advance the
	// index - otherwise DependencyIndex counts merges once per parent and no longer matches the
	// number of commits the run reports.
	if !step.MergeReplica {
		state.commitIndex++
	}

	return nil
}

// isMergeAction reports whether the commit action at index is consuming a merge commit, so that
// LineHistoryAnalyser marks the lines it introduces and leaves Merge() something to resolve.
//
// A merge commit is replayed once per parent branch (see planBuilder.appendMergeReplicas), so the
// action is followed either by the next replica or, for the last one, by the merge action itself.
// Every replica has to answer true: a branch which consumed the merge without marking would offer
// its merge-introduced lines to mergeLineValues as ground truth.
func isMergeAction(plan []runAction, index int, commit plumbing.Hash) bool {
	for i := index + 1; i < len(plan); i++ {
		switch plan[i].Action {
		case runActionHibernate, runActionBoot:
			continue
		case runActionCommit, runActionMerge:
			return plan[i].Commit != nil && plan[i].Commit.Hash == commit
		default:
			return false
		}
	}

	return false
}

func (state *pipelineRunState) fork(firstItem int, items []int) {
	startTime := time.Now()

	for i, clone := range cloneItems(state.branches[firstItem], len(items)-1) {
		state.branches[items[i+1]] = clone
		state.trackItems(clone)
	}

	state.runTimePerItem["*.Fork"] += time.Since(startTime).Seconds()
}

func (state *pipelineRunState) merge(items []int) {
	startTime := time.Now()

	merged := make([][]PipelineItem, len(items))
	for i, branch := range items {
		merged[i] = state.branches[branch]
	}

	mergeItems(merged)

	state.runTimePerItem["*.Merge"] += time.Since(startTime).Seconds()
}

func (state *pipelineRunState) emerge(firstItem int) {
	if firstItem == rootBranchIndex {
		state.branches[firstItem] = state.pipeline.items
		return
	}

	state.branches[firstItem] = cloneItems(state.rootClone, 1)[0]
	state.trackItems(state.branches[firstItem])
}

func (state *pipelineRunState) trackItems(items []PipelineItem) {
	for _, item := range items {
		if disposable, ok := item.(DisposablePipelineItem); ok {
			if _, seen := state.seenDisposable[disposable]; seen {
				continue
			}

			state.seenDisposable[disposable] = struct{}{}
			state.disposables = append(state.disposables, disposable)
		}
	}
}

func (state *pipelineRunState) dispose() {
	if state.disposed {
		return
	}

	state.disposed = true
	for _, disposable := range state.disposables {
		disposable.Dispose()
	}
}

// changeHibernation puts the given branches to sleep, or wakes them up.
//
// The plan schedules hibernation per branch index, on the assumption that each branch owns its
// items. That does not hold: ForkSamePipelineItem hands the same pointer to every branch, which
// is what BurndownAnalysis does because its history is global rather than per-branch. Calling
// Hibernate() for one branch then nils out state a sibling branch is still committing into - the
// nil-map panic of PLAN.md B11.
//
// So the state that matters is per *instance*, not per branch: an instance sleeps only once
// every live branch holding it is asleep, and wakes on the first branch to need it again. An
// instance shared by all branches therefore never hibernates in practice, which is the honest
// outcome - there is no per-branch state in it to swap out.
func (state *pipelineRunState) changeHibernation(items []int, boot bool) error {
	for _, branch := range items {
		if boot {
			delete(state.hibernatedBranches, branch)
		} else {
			state.hibernatedBranches[branch] = struct{}{}
		}
	}

	for _, branch := range items {
		for _, item := range state.branches[branch] {
			if err := state.changeItemHibernation(item, boot); err != nil {
				return err
			}
		}
	}

	return nil
}

func (state *pipelineRunState) changeItemHibernation(item PipelineItem, boot bool) error {
	hibernatable, ok := item.(HibernateablePipelineItem)
	if !ok || !state.shouldChangeHibernation(item, boot) {
		return nil
	}

	startTime := time.Now()

	var err error
	if boot {
		err = hibernatable.Boot()
	} else {
		err = hibernatable.Hibernate()
	}

	if err != nil {
		state.pipeline.l.Errorf("Failed to change hibernation state for %s: %v\n", item.Name(), err)
		return fmt.Errorf("change hibernation state for %s: %w", item.Name(), err)
	}

	if boot {
		delete(state.hibernatedItems, item)
	} else {
		state.hibernatedItems[item] = struct{}{}
	}

	state.runTimePerItem[item.Name()+".Hibernation"] += time.Since(startTime).Seconds()

	return nil
}

// shouldChangeHibernation decides whether this instance actually changes state. Booting is the
// mirror of hibernating: only an instance that was put to sleep is woken, so the calls stay
// balanced and an item never sees a Boot() without a preceding Hibernate().
func (state *pipelineRunState) shouldChangeHibernation(item PipelineItem, boot bool) bool {
	_, asleep := state.hibernatedItems[item]
	if boot {
		return asleep
	}

	return !asleep && state.allHoldersHibernated(item)
}

// allHoldersHibernated reports whether every live branch holding this instance is asleep.
func (state *pipelineRunState) allHoldersHibernated(item PipelineItem) bool {
	for branch, items := range state.branches {
		if _, asleep := state.hibernatedBranches[branch]; asleep {
			continue
		}

		if slices.Contains(items, item) {
			return false
		}
	}

	return true
}
