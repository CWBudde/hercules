package toposort

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Reworked from https://github.com/philopon/go-toposort

// Graph represents a directed acyclic graph.
type Graph struct {
	// Outgoing connections for every node.
	outputs map[string]map[string]struct{}
	// How many parents each node has.
	inputs        map[string]int
	sortIndex     map[string]int
	nextSortIndex int
}

// ErrNodeNotFound indicates that an edge endpoint is absent from the graph.
var ErrNodeNotFound = errors.New("toposort: node not found")

// NewGraph initializes a new Graph.
func NewGraph() *Graph {
	return &Graph{
		inputs:  map[string]int{},
		outputs: map[string]map[string]struct{}{},
	}
}

func NewGraphWithInsertionOrder() *Graph {
	g := NewGraph()
	g.sortIndex = map[string]int{}

	return g
}

type indexedStringSorter struct {
	values []string
	index  map[string]int
}

func (v indexedStringSorter) Len() int {
	return len(v.values)
}

func (v indexedStringSorter) Less(leftIndex, rightIndex int) bool {
	idx0, ok0 := v.index[v.values[leftIndex]]

	idx1, ok1 := v.index[v.values[rightIndex]]
	switch {
	case ok0 && ok1:
		if idx0 == idx1 {
			return v.values[leftIndex] < v.values[rightIndex]
		}

		return idx0 < idx1
	case !ok0 && !ok1:
		return v.values[leftIndex] < v.values[rightIndex]
	default:
		return ok0
	}
}

func (v indexedStringSorter) Swap(i, j int) {
	v.values[j], v.values[i] = v.values[i], v.values[j]
}

func (g *Graph) Sort(values []string) {
	if g.sortIndex == nil {
		sort.Strings(values)
	} else {
		sort.Sort(indexedStringSorter{values: values, index: g.sortIndex})
	}
}

// AddNode inserts a new node into the graph.
func (g *Graph) AddNode(name string) bool {
	if _, exists := g.outputs[name]; exists {
		return false
	}

	g.outputs[name] = map[string]struct{}{}

	g.inputs[name] = 0
	if g.sortIndex != nil {
		g.sortIndex[name] = g.nextSortIndex
		g.nextSortIndex++
	}

	return true
}

// AddNodes inserts multiple nodes into the graph at once.
func (g *Graph) AddNodes(names ...string) bool {
	for _, name := range names {
		if ok := g.AddNode(name); !ok {
			return false
		}
	}

	return true
}

// AddEdge inserts the link from "from" node to "to" node.
// It returns the destination's input count. Adding an existing edge is a no-op.
func (g *Graph) AddEdge(source, destination string) (int, error) {
	edges, ok := g.outputs[source]
	if !ok {
		return 0, fmt.Errorf("%w: source %q", ErrNodeNotFound, source)
	}

	if _, ok := g.outputs[destination]; !ok {
		return 0, fmt.Errorf("%w: destination %q", ErrNodeNotFound, destination)
	}

	if _, exists := edges[destination]; exists {
		return g.inputs[destination], nil
	}

	edges[destination] = struct{}{}
	inputCount := g.inputs[destination] + 1
	g.inputs[destination] = inputCount

	return inputCount, nil
}

func (g *Graph) InputCount(name string) (int, bool) {
	n, ok := g.inputs[name]
	return n, ok
}

// RemoveEdge deletes the link from "from" node to "to" node.
// Removing an edge which is already absent is a no-op.
func (g *Graph) RemoveEdge(source, destination string) (bool, error) {
	edges, ok := g.outputs[source]
	if !ok {
		return false, fmt.Errorf("%w: source %q", ErrNodeNotFound, source)
	}

	if _, ok := g.outputs[destination]; !ok {
		return false, fmt.Errorf("%w: destination %q", ErrNodeNotFound, destination)
	}

	if _, exists := edges[destination]; !exists {
		return false, nil
	}

	delete(edges, destination)
	g.inputs[destination]--

	return true, nil
}

// Toposort sorts the nodes in the graph in topological order.
func (g *Graph) Toposort() ([]string, bool) {
	result := make([]string, 0, len(g.outputs))
	queue := make([]string, 0, len(g.outputs))
	counters := make(map[string]int, len(g.inputs))

	for node := range g.outputs {
		if g.inputs[node] == 0 {
			queue = append(queue, node)
		}
	}

	g.Sort(queue)

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		result = append(result, node)

		queueLen := len(queue)

		for child := range g.outputs[node] {
			switch count, ok := counters[child]; {
			case !ok:
				count = g.inputs[child]
				if count == 1 {
					break
				}

				fallthrough
			case count != 1:
				counters[child] = count - 1
				continue
			}

			counters[child] = 0
			queue = append(queue, child)
		}

		g.Sort(queue[queueLen:])
	}

	return result, len(result) == len(g.inputs)
}

type NodePosition struct {
	// Level is the shortest number of outgoing edges from any graph root.
	Level int
	// Index is the deterministic BFS discovery order.
	Index int
}

type nodePosSorter struct {
	nodes     []string
	positions map[string]NodePosition
}

type cycleEdge struct {
	node   string
	parent string
}

func (v nodePosSorter) Len() int {
	return len(v.nodes)
}

func (v nodePosSorter) Less(i, j int) bool {
	left, right := v.nodes[i], v.nodes[j]
	leftIndex, rightIndex := v.positions[left].Index, v.positions[right].Index

	if leftIndex == rightIndex {
		return left < right
	}

	return leftIndex < rightIndex
}

func (v nodePosSorter) Swap(i, j int) {
	v.nodes[i], v.nodes[j] = v.nodes[j], v.nodes[i]
}

func SortByNodeIndex(nodes []string, positions map[string]NodePosition) {
	sort.Sort(nodePosSorter{nodes: nodes, positions: positions})
}

// BreadthSort returns positions for nodes reachable from a root in deterministic BFS order.
// Roots and each node's children follow the graph's ordering policy. Nodes are marked when
// enqueued, so Level is always the shortest distance from any root.
func (g *Graph) BreadthSort() map[string]NodePosition {
	queue := make([]string, 0, len(g.outputs))
	result := map[string]NodePosition{}

	for node := range g.outputs {
		if g.inputs[node] == 0 {
			queue = append(queue, node)
		}
	}

	g.Sort(queue)

	for index, node := range queue {
		result[node] = NodePosition{Level: 0, Index: index}
	}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		level := result[node].Level + 1

		children := g.FindChildren(node)
		for _, child := range children {
			if _, enqueued := result[child]; enqueued {
				continue
			}

			result[child] = NodePosition{
				Level: level,
				Index: len(result),
			}
			queue = append(queue, child)
		}
	}

	return result
}

// FindCycle returns the cycle in the graph which contains "seed" node.
func (g *Graph) FindCycle(seed string) []string {
	queue := make([]cycleEdge, 0, len(g.outputs))
	queue = append(queue, cycleEdge{seed, ""})
	visited := map[string]string{}

	for len(queue) > 0 {
		currentEdge := queue[0]
		queue = queue[1:]

		if shouldVisitCycleEdge(currentEdge, visited) {
			visited[currentEdge.node] = currentEdge.parent
			for _, child := range g.FindChildren(currentEdge.node) {
				queue = append(queue, cycleEdge{child, currentEdge.node})
			}
		}

		if currentEdge.node == seed && currentEdge.parent != "" {
			return buildCycle(seed, currentEdge.parent, visited)
		}
	}

	return []string{}
}

func shouldVisitCycleEdge(edge cycleEdge, visited map[string]string) bool {
	parent, exists := visited[edge.node]

	return !exists || parent == ""
}

func buildCycle(seed, parent string, visited map[string]string) []string {
	var result []string

	for node := parent; node != seed; node = visited[node] {
		result = append(result, node)
	}

	result = append(result, seed)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

// FindParents returns the other ends of incoming edges.
func (g *Graph) FindParents(destination string) []string {
	parents := []string{}

	for node, children := range g.outputs {
		if _, exists := children[destination]; exists {
			parents = append(parents, node)
		}
	}

	g.Sort(parents)

	return parents
}

// FindChildren returns the other ends of outgoing edges.
func (g *Graph) FindChildren(source string) []string {
	children := make([]string, 0, len(g.outputs[source]))
	for child := range g.outputs[source] {
		children = append(children, child)
	}

	g.Sort(children)

	return children
}

// Serialize outputs the graph in Graphviz format.
func (g *Graph) Serialize(sorted []string) string {
	node2index := map[string]int{}
	for index, node := range sorted {
		node2index[node] = index
	}
	var buffer bytes.Buffer
	buffer.WriteString("digraph Hercules {\n")

	nodesFrom := make([]string, 0, len(g.outputs))
	for nodeFrom := range g.outputs {
		nodesFrom = append(nodesFrom, nodeFrom)
	}

	g.Sort(nodesFrom)

	for _, nodeFrom := range nodesFrom {
		links := make([]string, 0, len(g.outputs[nodeFrom]))
		for nodeTo := range g.outputs[nodeFrom] {
			links = append(links, nodeTo)
		}

		g.Sort(links)

		for _, nodeTo := range links {
			fmt.Fprintf(&buffer, "  \"%d %s\" -> \"%d %s\"\n",
				node2index[nodeFrom], nodeFrom, node2index[nodeTo], nodeTo)
		}
	}

	buffer.WriteString("}")

	return buffer.String()
}

// DebugDump converts the graph to a string. As the name suggests, useful for debugging.
func (g *Graph) DebugDump() string {
	roots := make([]string, 0, len(g.outputs))
	for node := range g.outputs {
		if g.inputs[node] == 0 {
			roots = append(roots, node)
		}
	}

	g.Sort(roots)
	var buffer bytes.Buffer
	buffer.WriteString(strings.Join(roots, " ") + "\n")

	keys := make([]string, 0, len(g.outputs))
	vals := map[string][]string{}

	for key, val1 := range g.outputs {
		val2 := make([]string, 0, len(val1))
		for name := range val1 {
			val2 = append(val2, name)
		}

		keys = append(keys, key)
		vals[key] = val2
	}

	g.Sort(keys)

	for _, key := range keys {
		fmt.Fprintf(&buffer, "%s %d = ", key, g.inputs[key])
		outs := vals[key]
		g.Sort(outs)
		buffer.WriteString(strings.Join(outs, " ") + "\n")
	}

	return buffer.String()
}

func (g *Graph) HasChildren(name string) bool {
	return len(g.outputs[name]) > 0
}

func (g *Graph) RemoveNode(name string) bool {
	if _, ok := g.outputs[name]; !ok {
		return false
	}

	for child := range g.outputs[name] {
		g.inputs[child]--
	}

	for _, children := range g.outputs {
		delete(children, name)
	}

	delete(g.inputs, name)
	delete(g.outputs, name)
	delete(g.sortIndex, name)

	return true
}
