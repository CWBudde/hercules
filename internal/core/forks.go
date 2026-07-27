package core

import (
	"fmt"
	"io"
	"log"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/cwbudde/hercules/internal/toposort"
)

// OneShotMergeProcessor provides the convenience method to consume merges only once.
type OneShotMergeProcessor struct {
	merges map[plumbing.Hash]struct{}
}

// Initialize resets OneShotMergeProcessor.
func (proc *OneShotMergeProcessor) Initialize() {
	proc.merges = map[plumbing.Hash]struct{}{}
}

// ShouldConsumeCommit returns true on regular commits. It also returns true upon
// the first occurrence of a particular merge commit.
func (proc *OneShotMergeProcessor) ShouldConsumeCommit(deps map[string]any) bool {
	commit, ok := deps[DependencyCommit].(*object.Commit)
	if !ok {
		panic("commit dependency has an invalid type")
	}

	if commit.NumParents() <= 1 {
		return true
	}

	if _, ok := proc.merges[commit.Hash]; !ok {
		proc.merges[commit.Hash] = struct{}{}
		return true
	}

	return false
}

// NoopMerger provides an empty Merge() method suitable for PipelineItem.
type NoopMerger struct{}

// Merge does nothing.
func (*NoopMerger) Merge([]PipelineItem) {
	// no-op
}

// ForkSamePipelineItem clones items by referencing the same origin.
func ForkSamePipelineItem(origin PipelineItem, n int) []PipelineItem {
	clones := make([]PipelineItem, n)
	for i := range n {
		clones[i] = origin
	}

	return clones
}

// ForkCopyPipelineItem clones items by copying them by value from the origin.
func ForkCopyPipelineItem(origin PipelineItem, n int) []PipelineItem {
	originValue := reflect.Indirect(reflect.ValueOf(origin))
	originType := originValue.Type()

	clones := make([]PipelineItem, n)
	for i := range n {
		cloneValue := reflect.New(originType).Elem()
		cloneValue.Set(originValue)
		clones[i] = mustPipelineItem(cloneValue.Addr().Interface())
	}

	return clones
}

const (
	// runActionCommit corresponds to a regular commit.
	runActionCommit = iota
	// runActionFork splits a branch into several parts.
	runActionFork
	// runActionMerge merges several branches together.
	runActionMerge
	// runActionEmerge starts a root branch.
	runActionEmerge
	// runActionDelete removes the branch as it is no longer needed.
	runActionDelete
	// runActionHibernate preserves the items in the branch.
	runActionHibernate
	// runActionBoot does the opposite to runActionHibernate - recovers the original memory.
	runActionBoot
)

// rootBranchIndex is the minimum branch index in the plan.
const rootBranchIndex = 1

const runActionEmergeName = "emerge"

type runAction struct {
	Action    int
	Commit    *object.Commit
	NextMerge *object.Commit
	Items     []int
}

// DuplicateCommitError describes the repeated hash and its zero-based input positions. Commit
// files use one-based line numbers for these fields.
type DuplicateCommitError struct {
	Hash           plumbing.Hash
	FirstPosition  int
	SecondPosition int
}

func (err *DuplicateCommitError) Error() string {
	return fmt.Sprintf("%v: %s at positions %d and %d",
		ErrDuplicateCommits, err.Hash, err.FirstPosition, err.SecondPosition)
}

// Unwrap allows errors.Is(err, ErrDuplicateCommits).
func (*DuplicateCommitError) Unwrap() error {
	return ErrDuplicateCommits
}

// CommitComponent describes one connected component in an explicit commit input.
type CommitComponent struct {
	Roots []plumbing.Hash
	Size  int
}

// DisconnectedCommitsError describes every disconnected input component in deterministic order.
type DisconnectedCommitsError struct {
	Components []CommitComponent
}

func (err *DisconnectedCommitsError) Error() string {
	components := make([]string, len(err.Components))
	for index, component := range err.Components {
		roots := make([]string, len(component.Roots))
		for rootIndex, root := range component.Roots {
			roots[rootIndex] = root.String()
		}

		components[index] = fmt.Sprintf("roots=[%s] size=%d", strings.Join(roots, ","), component.Size)
	}

	return fmt.Sprintf("%v: %s", ErrDisconnectedCommits, strings.Join(components, "; "))
}

// Unwrap allows errors.Is(err, ErrDisconnectedCommits).
func (*DisconnectedCommitsError) Unwrap() error {
	return ErrDisconnectedCommits
}

func (ra runAction) String() string {
	switch ra.Action {
	case runActionCommit:
		return ra.Commit.Hash.String()[:7]
	case runActionFork:
		return fmt.Sprintf("fork^%d", len(ra.Items))
	case runActionMerge:
		return fmt.Sprintf("merge^%d", len(ra.Items))
	case runActionEmerge:
		return runActionEmergeName
	case runActionDelete:
		return "delete"
	case runActionHibernate:
		return "hibernate"
	case runActionBoot:
		return "boot"
	}

	return ""
}

type orderer = func(reverse, direction bool) []string

func cloneItems(origin []PipelineItem, cloneCount int) [][]PipelineItem {
	clones := make([][]PipelineItem, cloneCount)
	for j := range cloneCount {
		clones[j] = make([]PipelineItem, len(origin))
	}

	for i, item := range origin {
		itemClones := item.Fork(cloneCount)
		for j := range cloneCount {
			clones[j][i] = itemClones[j]
		}
	}

	return clones
}

func mergeItems(branches [][]PipelineItem) {
	buffer := make([]PipelineItem, len(branches)-1)
	for i, item := range branches[0] {
		for j := range len(branches) - 1 {
			buffer[j] = branches[j+1][i]
		}

		item.Merge(buffer)
	}
}

// getMasterBranch returns the branch with the smallest index.
func getMasterBranch(branches map[int][]PipelineItem) []PipelineItem {
	minKey := 1 << 31
	var minVal []PipelineItem

	for key, val := range branches {
		if key < minKey {
			minKey = key
			minVal = val
		}
	}

	return minVal
}

// prepareRunPlan schedules the actions for Pipeline.Run().
func prepareRunPlan(commits []*object.Commit, hibernationDistance int, traceback bool,
) ([]runAction, int, error) {
	err := validateCommitInput(commits)
	if err != nil {
		return nil, 0, err
	}

	hashes, dag := buildDag(commits)
	mergedDag, mergedSeq := mergeDag(hashes, dag)
	orderNodes := bindOrderNodes(mergedDag)
	collapseFastForwards(orderNodes, hashes, mergedDag, dag, mergedSeq)
	/*fmt.Printf("digraph Hercules {\n")
	for i, c := range orderNodes(false, false) {
		commit := hashes[c]
		fmt.Printf("  \"%s\"[label=\"[%d] %s\"]\n", commit.Hash.String(), i, commit.Hash.String()[:6])
		for _, child := range mergedDag[commit.Hash] {
			fmt.Printf("  \"%s\" -> \"%s\"\n", commit.Hash.String(), child.Hash.String())
		}
	}
	fmt.Printf("}\n")*/
	plan := generatePlan(orderNodes, hashes, mergedDag, dag, mergedSeq)
	mergeHashCount := 0

	plan = collectGarbage(plan)
	if traceback {
		mergeHashCount = tracebackMerges(plan)
	}

	if hibernationDistance > 0 {
		plan = insertHibernateBoot(plan, hibernationDistance)
	}

	return plan, mergeHashCount, nil
}

func validateCommitInput(commits []*object.Commit) error {
	if len(commits) == 0 {
		return ErrNoCommits
	}

	seen := make(map[plumbing.Hash]int, len(commits))
	for index, commit := range commits {
		if commit == nil || commit.Hash == plumbing.ZeroHash {
			return fmt.Errorf("%w at position %d", ErrInvalidCommit, index)
		}

		if firstIndex, exists := seen[commit.Hash]; exists {
			return &DuplicateCommitError{
				Hash:           commit.Hash,
				FirstPosition:  firstIndex,
				SecondPosition: index,
			}
		}

		seen[commit.Hash] = index
	}

	hashes, dag := buildDag(commits)

	components := commitComponents(hashes, dag)
	if len(components) > 1 {
		return &DisconnectedCommitsError{Components: components}
	}

	return nil
}

// printAction prints the specified action.
func printAction(writer io.Writer, action runAction) {
	firstItem := action.Items[0]
	switch action.Action {
	case runActionCommit:
		_, _ = fmt.Fprintln(writer, "C", firstItem, action.Commit.Hash.String())
	case runActionFork:
		_, _ = fmt.Fprintln(writer, "F", action.Items)
	case runActionMerge:
		_, _ = fmt.Fprintln(writer, "M", action.Items, action.Commit.Hash.String())
	case runActionEmerge:
		_, _ = fmt.Fprintln(writer, "E", action.Items)
	case runActionDelete:
		_, _ = fmt.Fprintln(writer, "D", action.Items)
	case runActionHibernate:
		_, _ = fmt.Fprintln(writer, "H", firstItem)
	case runActionBoot:
		_, _ = fmt.Fprintln(writer, "B", firstItem)
	}
}

// getCommitParents returns the list of *unique* commit parents.
// Yes, it *is* possible to have several identical parents, and Hercules used to crash because of that.
func getCommitParents(commit *object.Commit) []plumbing.Hash {
	result := make([]plumbing.Hash, 0, len(commit.ParentHashes))

	var parents map[plumbing.Hash]bool
	if len(commit.ParentHashes) > 1 {
		parents = map[plumbing.Hash]bool{}
	}

	for _, parent := range commit.ParentHashes {
		if _, exists := parents[parent]; !exists {
			if parents != nil {
				parents[parent] = true
			}

			result = append(result, parent)
		}
	}

	return result
}

// buildDag generates the raw commit DAG and the commit hash map.
func buildDag(commits []*object.Commit) (
	map[string]*object.Commit, map[plumbing.Hash][]*object.Commit,
) {
	hashes := map[string]*object.Commit{}
	for _, commit := range commits {
		hashes[commit.Hash.String()] = commit
	}

	dag := map[plumbing.Hash][]*object.Commit{}
	for _, commit := range commits {
		if _, exists := dag[commit.Hash]; !exists {
			dag[commit.Hash] = make([]*object.Commit, 0, 1)
		}

		for _, parent := range getCommitParents(commit) {
			if _, exists := hashes[parent.String()]; !exists {
				continue
			}

			children := dag[parent]
			if children == nil {
				children = make([]*object.Commit, 0, 1)
			}

			dag[parent] = append(children, commit)
		}
	}

	return hashes, dag
}

func commitComponents(
	hashes map[string]*object.Commit,
	dag map[plumbing.Hash][]*object.Commit,
) []CommitComponent {
	visited := map[plumbing.Hash]bool{}
	sets := make([][]plumbing.Hash, 0, 1)

	for _, key := range sortedCommitHashes(dag) {
		if visited[key] {
			continue
		}

		sets = append(sets, walkCommitComponent(key, hashes, dag, visited))
	}

	components := make([]CommitComponent, len(sets))
	for index, set := range sets {
		components[index] = describeCommitComponent(set, hashes)
	}

	sort.Slice(components, func(i, j int) bool {
		return components[i].Roots[0].String() < components[j].Roots[0].String()
	})

	return components
}

func describeCommitComponent(
	hashes []plumbing.Hash,
	commits map[string]*object.Commit,
) CommitComponent {
	members := make(map[plumbing.Hash]struct{}, len(hashes))
	for _, hash := range hashes {
		members[hash] = struct{}{}
	}

	roots := commitComponentRoots(hashes, commits, members)
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].String() < roots[j].String()
	})

	if len(roots) == 0 {
		sort.Slice(hashes, func(i, j int) bool {
			return hashes[i].String() < hashes[j].String()
		})
		roots = append(roots, hashes[0])
	}

	return CommitComponent{Roots: roots, Size: len(hashes)}
}

func commitComponentRoots(
	hashes []plumbing.Hash,
	commits map[string]*object.Commit,
	members map[plumbing.Hash]struct{},
) []plumbing.Hash {
	var roots []plumbing.Hash

	for _, hash := range hashes {
		hasParent := false

		for _, parent := range getCommitParents(commits[hash.String()]) {
			if _, exists := members[parent]; exists {
				hasParent = true

				break
			}
		}

		if !hasParent {
			roots = append(roots, hash)
		}
	}

	return roots
}

func walkCommitComponent(
	root plumbing.Hash,
	hashes map[string]*object.Commit,
	dag map[plumbing.Hash][]*object.Commit,
	visited map[plumbing.Hash]bool,
) []plumbing.Hash {
	var component []plumbing.Hash

	for queue := []plumbing.Hash{root}; len(queue) > 0; {
		head := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if visited[head] {
			continue
		}

		visited[head] = true
		component = append(component, head)
		queue = appendCommitNeighbors(queue, head, hashes, dag, visited)
	}

	return component
}

func appendCommitNeighbors(
	queue []plumbing.Hash,
	head plumbing.Hash,
	hashes map[string]*object.Commit,
	dag map[plumbing.Hash][]*object.Commit,
	visited map[plumbing.Hash]bool,
) []plumbing.Hash {
	for _, child := range dag[head] {
		if !visited[child.Hash] {
			queue = append(queue, child.Hash)
		}
	}

	commit := hashes[head.String()]
	if commit == nil {
		return queue
	}

	for _, parent := range getCommitParents(commit) {
		if !visited[parent] && hashes[parent.String()] != nil {
			queue = append(queue, parent)
		}
	}

	return queue
}

// bindOrderNodes returns curried "orderNodes" function.
func bindOrderNodes(mergedDag map[plumbing.Hash][]*object.Commit) orderer {
	return func(reverse, direction bool) []string {
		return orderCommitNodes(mergedDag, reverse, direction)
	}
}

func orderCommitNodes(
	mergedDag map[plumbing.Hash][]*object.Commit,
	reverse bool,
	direction bool,
) []string {
	graph := toposort.NewGraph()
	keys := sortedCommitHashes(mergedDag)

	for _, key := range keys {
		graph.AddNode(key.String())
	}

	addCommitEdges(graph, keys, mergedDag, direction)

	order, ok := graph.Toposort()
	if !ok {
		// should never happen
		panic("Could not topologically sort the DAG of commits")
	}

	if reverse != direction {
		slices.Reverse(order)
	}

	return order
}

func sortedCommitHashes(mergedDag map[plumbing.Hash][]*object.Commit) []plumbing.Hash {
	keys := make([]plumbing.Hash, 0, len(mergedDag))
	for key := range mergedDag {
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	return keys
}

func addCommitEdges(
	graph *toposort.Graph,
	keys []plumbing.Hash,
	mergedDag map[plumbing.Hash][]*object.Commit,
	direction bool,
) {
	for _, key := range keys {
		children := mergedDag[key]
		sort.Slice(children, func(i, j int) bool {
			return children[i].Hash.String() < children[j].Hash.String()
		})

		for _, child := range children {
			if direction {
				_, _ = graph.AddEdge(child.Hash.String(), key.String())
			} else {
				_, _ = graph.AddEdge(key.String(), child.Hash.String())
			}
		}
	}
}

// inverts `dag`.
func buildParents(dag map[plumbing.Hash][]*object.Commit) map[plumbing.Hash]map[plumbing.Hash]bool {
	parents := map[plumbing.Hash]map[plumbing.Hash]bool{}

	for key, vals := range dag {
		for _, val := range vals {
			myps := parents[val.Hash]
			if myps == nil {
				myps = map[plumbing.Hash]bool{}
				parents[val.Hash] = myps
			}

			myps[key] = true
		}
	}

	return parents
}

// mergeDag turns sequences of consecutive commits into single nodes.
func mergeDag(
	hashes map[string]*object.Commit,
	dag map[plumbing.Hash][]*object.Commit,
) (map[plumbing.Hash][]*object.Commit, map[plumbing.Hash][]*object.Commit) {
	parents := buildParents(dag)
	mergedDag := map[plumbing.Hash][]*object.Commit{}
	mergedSeq := map[plumbing.Hash][]*object.Commit{}

	visited := map[plumbing.Hash]bool{}
	for head := range dag {
		if visited[head] {
			continue
		}

		head = mergedSequenceHead(head, parents, dag)
		seq := collectMergedSequence(head, hashes, parents, dag, visited)

		mergedSeq[head] = seq
		mergedDag[head] = dag[seq[len(seq)-1].Hash]
	}

	return mergedDag, mergedSeq
}

func mergedSequenceHead(
	head plumbing.Hash,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
	dag map[plumbing.Hash][]*object.Commit,
) plumbing.Hash {
	for {
		nextParents := parents[head]
		next := firstCommitHash(nextParents)

		if len(nextParents) != 1 || len(dag[next]) != 1 {
			return head
		}

		head = next
	}
}

func firstCommitHash(hashes map[plumbing.Hash]bool) plumbing.Hash {
	for hash := range hashes {
		return hash
	}

	return plumbing.ZeroHash
}

func collectMergedSequence(
	head plumbing.Hash,
	hashes map[string]*object.Commit,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
	dag map[plumbing.Hash][]*object.Commit,
	visited map[plumbing.Hash]bool,
) []*object.Commit {
	var sequence []*object.Commit

	for {
		visited[head] = true
		sequence = append(sequence, hashes[head.String()])

		if len(dag[head]) != 1 {
			return sequence
		}

		head = dag[head][0].Hash
		if len(parents[head]) != 1 {
			return sequence
		}
	}
}

// collapseFastForwards removes the fast forward merges.
func collapseFastForwards(
	orderNodes orderer, hashes map[string]*object.Commit,
	mergedDag, dag, mergedSeq map[plumbing.Hash][]*object.Commit,
) {
	parents := buildParents(mergedDag)
	processed := map[plumbing.Hash]bool{}

	for _, strkey := range orderNodes(false, true) {
		key := hashes[strkey].Hash
		processed[key] = true

		for {
			vals, exists := mergedDag[key]
			if !exists || len(vals) < 2 {
				break
			}

			toRemove := findFastForwardChildren(
				key, vals, processed, parents, mergedDag, mergedSeq,
			)
			if len(toRemove) == 0 {
				break
			}

			node := mergedSeq[key][len(mergedSeq[key])-1].Hash
			dag[node] = childrenWithout(dag[node], toRemove)
			newVals := childrenWithout(vals, toRemove)
			merged := mergeOnlyChild(key, newVals, parents, mergedDag, mergedSeq)
			removeParentEdges(key, toRemove, parents)

			if !merged {
				mergedDag[key] = newVals
				break
			}
		}
	}
}

func findFastForwardChildren(
	key plumbing.Hash,
	children []*object.Commit,
	processed map[plumbing.Hash]bool,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
	mergedDag, mergedSeq map[plumbing.Hash][]*object.Commit,
) map[plumbing.Hash]bool {
	sort.Slice(children, func(i, j int) bool {
		return children[i].Hash.String() < children[j].Hash.String()
	})

	toRemove := map[plumbing.Hash]bool{}

	for _, child := range children {
		otherParents := parentHashesExcept(parents[child.Hash], key)

		var immediateParent plumbing.Hash
		if len(otherParents) == 1 {
			immediateParent = otherParents[0]
		}

		if !ancestorPathReaches(key, child.Hash, otherParents, processed, parents) {
			continue
		}

		toRemove[child.Hash] = true
		if len(otherParents) == 1 && len(mergedDag[immediateParent]) == 1 {
			mergeCommitSequence(immediateParent, child.Hash, parents, mergedDag, mergedSeq)
		}
	}

	return toRemove
}

func parentHashesExcept(
	parentSet map[plumbing.Hash]bool, excluded plumbing.Hash,
) []plumbing.Hash {
	result := make([]plumbing.Hash, 0, len(parentSet))
	for parent := range parentSet {
		if parent != excluded {
			result = append(result, parent)
		}
	}

	return result
}

func ancestorPathReaches(
	target, child plumbing.Hash,
	queue []plumbing.Hash,
	processed map[plumbing.Hash]bool,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
) bool {
	visited := map[plumbing.Hash]bool{child: true}
	for _, parent := range queue {
		visited[parent] = true
	}

	for len(queue) > 0 {
		head := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if processed[head] {
			if head == target {
				return true
			}

			continue
		}

		for parent := range parents[head] {
			if !visited[parent] {
				visited[head] = true

				queue = append(queue, parent)
			}
		}
	}

	return false
}

func mergeCommitSequence(
	parent, child plumbing.Hash,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
	mergedDag, mergedSeq map[plumbing.Hash][]*object.Commit,
) {
	mergedSeq[parent] = append(mergedSeq[parent], mergedSeq[child]...)
	delete(mergedSeq, child)
	mergedDag[parent] = mergedDag[child]
	delete(mergedDag, child)
	parents[child] = parents[parent]
	replaceParent(child, parent, parents)
}

func childrenWithout(
	children []*object.Commit, removed map[plumbing.Hash]bool,
) []*object.Commit {
	result := make([]*object.Commit, 0, len(children))
	for _, child := range children {
		if !removed[child.Hash] {
			result = append(result, child)
		}
	}

	return result
}

func mergeOnlyChild(
	key plumbing.Hash,
	children []*object.Commit,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
	mergedDag, mergedSeq map[plumbing.Hash][]*object.Commit,
) bool {
	if len(children) != 1 || len(parents[children[0].Hash]) != 1 {
		return false
	}

	mergeCommitSequence(key, children[0].Hash, parents, mergedDag, mergedSeq)

	return true
}

func replaceParent(
	oldParent, newParent plumbing.Hash,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
) {
	for _, parentSet := range parents {
		if parentSet[oldParent] {
			delete(parentSet, oldParent)
			parentSet[newParent] = true
		}
	}
}

func removeParentEdges(
	parent plumbing.Hash,
	children map[plumbing.Hash]bool,
	parents map[plumbing.Hash]map[plumbing.Hash]bool,
) {
	for child := range children {
		delete(parents[child], parent)
	}
}

// generatePlan creates the list of actions from the commit DAG.
func generatePlan(
	orderNodes orderer, hashes map[string]*object.Commit,
	mergedDag, dag, mergedSeq map[plumbing.Hash][]*object.Commit,
) []runAction {
	builder := planBuilder{
		hashes: hashes, mergedDag: mergedDag, dag: dag, mergedSeq: mergedSeq,
		parents: buildParents(dag), branches: map[plumbing.Hash]int{},
		branchers: map[plumbing.Hash]map[plumbing.Hash]int{}, counter: rootBranchIndex,
	}
	for _, name := range orderNodes(false, true) {
		builder.process(hashes[name])
	}

	return builder.plan
}

type planBuilder struct {
	hashes                    map[string]*object.Commit
	mergedDag, dag, mergedSeq map[plumbing.Hash][]*object.Commit
	parents                   map[plumbing.Hash]map[plumbing.Hash]bool
	branches                  map[plumbing.Hash]int
	branchers                 map[plumbing.Hash]map[plumbing.Hash]int
	counter                   int
	plan                      []runAction
}

func (builder *planBuilder) process(commit *object.Commit) {
	if len(builder.parents[commit.Hash]) == 0 {
		builder.branches[commit.Hash] = builder.counter
		builder.plan = append(builder.plan, runAction{
			Action: runActionEmerge, Commit: commit, Items: []int{builder.counter},
		})
		builder.counter++
	}

	branch := builder.branches[commit.Hash]
	if _, exists := builder.branches[commit.Hash]; !exists {
		branch = -1
	}

	head, branch := builder.appendCommitSequence(commit, branch)
	builder.appendFork(commit, head, branch)
}

func (builder *planBuilder) appendCommitSequence(
	commit *object.Commit, branch int,
) (plumbing.Hash, int) {
	sequence, exists := builder.mergedSeq[commit.Hash]
	if !exists {
		return commit.Hash, branch
	}

	for index, offspring := range sequence {
		if index != 0 {
			if branchExists(branch) {
				builder.appendCommit(offspring, branch)
			}

			continue
		}

		mergeBranch, items := builder.mergeBranches(commit, branch)
		if mergeBranch != branch {
			builder.branches[commit.Hash], branch = mergeBranch, mergeBranch
		} else if !branchExists(branch) {
			log.Panicf("head of the sequence does not have an assigned branch: %s", commit.Hash.String())
		}

		builder.appendCommit(offspring, mergeBranch)

		if len(items) > 0 {
			builder.plan = append(builder.plan, runAction{
				Action: runActionMerge, Commit: commit, Items: items,
			})
		}
	}

	head := sequence[len(sequence)-1].Hash
	builder.branches[head] = branch

	return head, branch
}

func (builder *planBuilder) mergeBranches(commit *object.Commit, branch int) (int, []int) {
	parents := builder.parents[commit.Hash]
	if len(parents) < 2 {
		return branch, nil
	}

	items := make([]int, 0, len(parents))
	minBranch, minIndex := math.MaxInt, 0

	for parent := range parents {
		parentBranch := builder.branchers[commit.Hash][parent]
		if !branchExists(parentBranch) {
			parentBranch = builder.branches[parent]
			if !branchExists(parentBranch) {
				log.Panicf("parent %s => %s does not have a branch assigned", parent, commit.Hash)
			}
		}

		if minBranch > parentBranch && len(builder.dag[parent]) == 1 {
			minBranch, minIndex = parentBranch, len(items)
		}

		items = append(items, parentBranch)
	}

	if minBranch < math.MaxInt {
		branch = minBranch
		items[minIndex], items[0] = items[0], items[minIndex]
	}

	return branch, items
}

func branchExists(branch int) bool {
	return branch >= rootBranchIndex
}

func (builder *planBuilder) appendCommit(commit *object.Commit, branch int) {
	if branch == 0 {
		log.Panicf("setting a zero branch for %s", commit.Hash)
	}

	builder.plan = append(builder.plan, runAction{
		Action: runActionCommit, Commit: commit, Items: []int{branch},
	})
}

func (builder *planBuilder) appendFork(commit *object.Commit, head plumbing.Hash, branch int) {
	if len(builder.mergedDag[commit.Hash]) <= 1 {
		return
	}

	children := []int{branch}

	for index, child := range builder.mergedDag[commit.Hash] {
		if index == 0 {
			builder.branches[child.Hash] = branch
			continue
		}

		if _, exists := builder.branches[child.Hash]; !exists {
			builder.branches[child.Hash] = builder.counter
		}

		if builder.branchers[child.Hash] == nil {
			builder.branchers[child.Hash] = map[plumbing.Hash]int{}
		}

		builder.branchers[child.Hash][head] = builder.counter
		children = append(children, builder.counter)
		builder.counter++
	}

	builder.plan = append(builder.plan, runAction{
		Action: runActionFork, Commit: builder.hashes[head.String()], Items: children,
	})
}

// collectGarbage inserts `runActionDelete` disposal steps.
func collectGarbage(plan []runAction) []runAction {
	lastMentioned := lastBranchMentions(plan)
	lastMentionedArr := collectibleBranches(lastMentioned, len(plan))

	if len(lastMentionedArr) == 0 {
		// early return - we have nothing to collect
		return plan
	}

	sort.Slice(lastMentionedArr, func(i, j int) bool {
		return lastMentionedArr[i][0] < lastMentionedArr[j][0]
	})
	lastMentionedArr = append(lastMentionedArr, [2]int{len(plan) - 1, -1})

	return appendGarbageCollectionActions(plan, lastMentionedArr)
}

func lastBranchMentions(plan []runAction) map[int]int {
	// lastMentioned maps branch index to the index inside `plan` when that branch was last used.
	lastMentioned := map[int]int{}

	for actionIndex, action := range plan {
		firstItem := action.Items[0]

		switch action.Action {
		case runActionCommit:
			lastMentioned[firstItem] = actionIndex
			if firstItem < rootBranchIndex {
				log.Panicf("commit %s does not have an assigned branch",
					action.Commit.Hash.String())
			}
		case runActionFork, runActionEmerge:
			lastMentioned[firstItem] = actionIndex
		case runActionMerge:
			for _, item := range action.Items {
				lastMentioned[item] = actionIndex
			}
		}
	}

	return lastMentioned
}

func collectibleBranches(lastMentioned map[int]int, planLength int) [][2]int {
	branches := make([][2]int, 0, len(lastMentioned)+1)

	for branch, actionIndex := range lastMentioned {
		if actionIndex != planLength-1 {
			branches = append(branches, [2]int{actionIndex, branch})
		}
	}

	return branches
}

func appendGarbageCollectionActions(plan []runAction, branches [][2]int) []runAction {
	var garbageCollectedPlan []runAction

	prevpi := -1
	for _, pair := range branches {
		for pi := prevpi + 1; pi <= pair[0]; pi++ {
			garbageCollectedPlan = append(garbageCollectedPlan, plan[pi])
		}

		if pair[1] >= 0 {
			prevpi = pair[0]
			garbageCollectedPlan = append(garbageCollectedPlan, runAction{
				Action: runActionDelete,
				Commit: nil,
				Items:  []int{pair[1]},
			})
		}
	}

	return garbageCollectedPlan
}

type hbAction struct {
	Branch    int
	Hibernate bool
}

func insertHibernateBoot(plan []runAction, hibernationDistance int) []runAction {
	addons, addonsCount := hibernationAddons(plan, hibernationDistance)

	newPlan := make([]runAction, 0, len(plan)+addonsCount)
	for x, action := range plan {
		boots, hibernates := splitHibernateAddons(addons[x])
		if len(boots) > 0 {
			newPlan = append(newPlan, runAction{
				Action: runActionBoot, Commit: action.Commit, Items: boots,
			})
		}

		newPlan = append(newPlan, action)
		if len(hibernates) > 0 {
			newPlan = append(newPlan, runAction{
				Action: runActionHibernate, Commit: action.Commit, Items: hibernates,
			})
		}
	}

	return newPlan
}

func hibernationAddons(plan []runAction, distance int) (map[int][]hbAction, int) {
	addons := map[int][]hbAction{}
	lastUsed, count := map[int]int{}, 0

	for index, action := range plan {
		if action.Action == runActionDelete {
			continue
		}

		for _, branch := range action.Items {
			if previous, exists := lastUsed[branch]; exists && index-previous-1 > distance {
				addons[index] = append(addons[index], hbAction{branch, false})
				addons[previous] = append(addons[previous], hbAction{branch, true})
				count += 2
			}

			lastUsed[branch] = index
		}
	}

	return addons, count
}

func splitHibernateAddons(addons []hbAction) ([]int, []int) {
	var boots, hibernates []int

	for _, addon := range addons {
		if addon.Hibernate {
			hibernates = append(hibernates, addon.Branch)
		} else {
			boots = append(boots, addon.Branch)
		}
	}

	return boots, hibernates
}

func tracebackMerges(plan []runAction) int {
	lastMerges := map[int]*object.Commit{}
	uniqueMerges := 0

	for index := range slices.Backward(plan) {
		step := &plan[index]
		if tracebackMergeStep(step, lastMerges) {
			uniqueMerges++
		}
	}

	return uniqueMerges
}

func tracebackMergeStep(step *runAction, lastMerges map[int]*object.Commit) bool {
	switch step.Action {
	case runActionMerge:
		if step.Commit == nil {
			return false
		}

		updateLastMerges(step, lastMerges)

		return true
	case runActionEmerge:
		deleteLastMerges(step.Items, lastMerges)
	case runActionFork:
		deleteLastMerges(step.Items[1:], lastMerges)
	case runActionCommit:
		step.NextMerge = lastMerges[step.Items[0]]
	}

	return false
}

func updateLastMerges(step *runAction, lastMerges map[int]*object.Commit) {
	for _, branch := range step.Items {
		if lastMerges[branch] != nil && step.Items[0] > branch {
			continue
		}

		lastMerges[branch] = step.Commit
	}
}

func deleteLastMerges(branches []int, lastMerges map[int]*object.Commit) {
	for _, branch := range branches {
		delete(lastMerges, branch)
	}
}
