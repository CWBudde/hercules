package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"

	"github.com/cwbudde/hercules/internal/toposort"
)

func (pipeline *Pipeline) resolve(dumpPath string, priorityFn DependencyPriorityFunc) error {
	sort.Sort(sortablePipelineItems(pipeline.items))

	graph, name2item, dataKeys, err := pipeline.dependencyGraph()
	if err != nil {
		return err
	}

	ambiguousDataKeys, err := pipeline.validateDependencies(graph, dataKeys)
	if err != nil {
		return err
	}

	if len(ambiguousDataKeys) > 0 {
		err = pipeline.resolveAmbiguous(ambiguousDataKeys, graph, name2item, priorityFn)
		if err != nil {
			return err
		}
	}

	pipelinePlan, ok := graph.Toposort()
	if !ok {
		_, _ = fmt.Fprint(os.Stderr, graph.DebugDump())

		pipeline.l.Critical("Failed to resolve pipeline dependencies: unable to topologically sort the items.")

		return errors.New("topological sort failure")
	}

	pipeline.items = pipeline.items[:0]

	for _, key := range pipelinePlan {
		if item, ok := name2item[key]; ok {
			pipeline.items = append(pipeline.items, item)
		}
	}

	pipeline.dumpDependencyGraph(graph, pipelinePlan, dumpPath)

	return nil
}

func (pipeline *Pipeline) dependencyGraph() (
	*toposort.Graph, map[string]PipelineItem, map[string]struct{}, error,
) {
	graph := toposort.NewGraphWithInsertionOrder()
	items := make(map[string]PipelineItem, len(pipeline.items))
	dataKeys := make(map[string]struct{})

	itemUsages := make(map[string]int, len(pipeline.items))
	for _, item := range pipeline.items {
		itemUsages[item.Name()]++
		name := fmt.Sprintf("%s_%d", item.Name(), itemUsages[item.Name()])
		items[name] = item
		graph.AddNode(name)

		for _, key := range item.Requires() {
			dataKey := "[" + key + "]"
			dataKeys[dataKey] = struct{}{}
			graph.AddNode(dataKey)

			err := addGraphEdge(graph, dataKey, name)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		for _, key := range item.Provides() {
			dataKey := "[" + key + "]"
			dataKeys[dataKey] = struct{}{}
			graph.AddNode(dataKey)

			err := addGraphEdge(graph, name, dataKey)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	}

	return graph, items, dataKeys, nil
}

func (pipeline *Pipeline) validateDependencies(
	graph *toposort.Graph, dataKeys map[string]struct{},
) ([]string, error) {
	var ambiguous []string

	for name := range dataKeys {
		parentCount, _ := graph.InputCount(name)
		if parentCount == 0 {
			children := graph.FindChildren(name)
			sort.Strings(children)
			pipeline.l.Criticalf("Unsatisfied dependency: %s -> %s", name, children)

			return nil, errors.New("unsatisfied dependency")
		}

		if parentCount > 1 {
			ambiguous = append(ambiguous, name)
		}
	}

	return ambiguous, nil
}

func (pipeline *Pipeline) dumpDependencyGraph(
	graph *toposort.Graph, plan []string, dumpPath string,
) {
	if dumpPath == "" {
		return
	}

	serialized := graph.Serialize(plan)
	if dumpPath == "-" {
		_, _ = fmt.Fprint(os.Stderr, serialized)
		return
	}

	_ = os.WriteFile(dumpPath, []byte(serialized), 0o600)
	absPath, _ := filepath.Abs(dumpPath)
	pipeline.l.Infof("Wrote the DAG to %s\n", absPath)
}

// break cycles - unwinds sequential processing of same facts.
func (pipeline *Pipeline) resolveAmbiguous(ambiguousDataKeys []string,
	graph *toposort.Graph, name2item map[string]PipelineItem, priorityFn DependencyPriorityFunc,
) error {
	graph.Sort(ambiguousDataKeys)
	bfsIndex := graph.BreadthSort()

	for _, key := range ambiguousDataKeys {
		err := pipeline.resolveAmbiguousKey(key, graph, name2item, priorityFn, bfsIndex)
		if err != nil {
			return err
		}
	}

	return nil
}

func (pipeline *Pipeline) resolveAmbiguousKey(
	key string,
	graph *toposort.Graph,
	name2item map[string]PipelineItem,
	priorityFn DependencyPriorityFunc,
	bfsIndex map[string]toposort.NodePosition,
) error {
	inputs := graph.FindParents(key)
	toposort.SortByNodeIndex(inputs, bfsIndex)

	inputs = pipeline.removeEquivalentInputs(inputs, graph, name2item, priorityFn, bfsIndex)
	if len(inputs) < 2 {
		return nil
	}

	for _, input := range inputs {
		err := removeGraphEdge(graph, input, key)
		if err != nil {
			return err
		}
	}

	replacements, err := wireAmbiguousInputs(key, inputs, graph)
	if err != nil {
		return err
	}

	return repairAmbiguousChildren(key, replacements, graph)
}

func (pipeline *Pipeline) removeEquivalentInputs(
	inputs []string,
	graph *toposort.Graph,
	name2item map[string]PipelineItem,
	priorityFn DependencyPriorityFunc,
	bfsIndex map[string]toposort.NodePosition,
) []string {
	excludes := map[string]struct{}{}

	last, lastLevel := len(inputs), 0
	for inputIndex := last - 1; inputIndex >= -1; inputIndex-- {
		level := -1
		if inputIndex >= 0 {
			level = bfsIndex[inputs[inputIndex]].Level
		}

		if level != lastLevel {
			if alternatives := inputs[inputIndex+1 : last]; len(alternatives) > 1 {
				graph.Sort(alternatives)
				pipeline.resolveAlternatives(graph, alternatives, name2item, priorityFn, excludes)
			}

			lastLevel, last = level, inputIndex+1
		}
	}

	kept := inputs[:0]
	for _, input := range inputs {
		if _, excluded := excludes[input]; excluded {
			graph.RemoveNode(input)
		} else {
			kept = append(kept, input)
		}
	}

	return kept
}

func (pipeline *Pipeline) resolveAlternatives(graph *toposort.Graph, nodes []string, itemMap map[string]PipelineItem,
	priorityFn DependencyPriorityFunc, excludes map[string]struct{},
) {
	dataKeys := groupAlternativeNodes(graph, nodes, itemMap)
	for _, altNodes := range dataKeys {
		if len(altNodes) >= 2 {
			resolveAlternativeGroup(altNodes, itemMap, priorityFn, excludes)
		}
	}
}

func groupAlternativeNodes(
	graph *toposort.Graph,
	nodes []string,
	itemMap map[string]PipelineItem,
) map[string][]string {
	dataKeys := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		item := itemMap[node]
		key := strings.Join(item.Requires(), ",") + " => " + alternativeChildren(graph, node)
		dataKeys[key] = append(dataKeys[key], node)
	}

	return dataKeys
}

func alternativeChildren(graph *toposort.Graph, node string) string {
	childList := strings.Builder{}

	for _, child := range graph.FindChildren(node) {
		if !graph.HasChildren(child) {
			continue
		}

		if childList.Len() != 0 {
			childList.WriteString(",")
		}

		childList.WriteString(child)
	}

	return childList.String()
}

func resolveAlternativeGroup(
	nodes []string,
	itemMap map[string]PipelineItem,
	priorityFn DependencyPriorityFunc,
	excludes map[string]struct{},
) {
	items := make([]PipelineItem, 0, len(nodes))

	for _, node := range nodes {
		items = append(items, itemMap[node])
	}

	priorityItem := priorityFn(items)
	if priorityItem == nil {
		panic("unexpected")
	}

	priorityNode := ""

	for _, node := range nodes {
		if priorityItem == itemMap[node] {
			priorityNode = setPriorityNode(priorityNode, node)
		} else {
			excludes[node] = struct{}{}
		}
	}

	if priorityNode == "" {
		panic("unexpected")
	}
}

func setPriorityNode(current, selected string) string {
	if current != "" {
		panic("unexpected")
	}

	return selected
}
