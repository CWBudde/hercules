package toposort

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func index(s []string, v string) int {
	for i, s := range s {
		if s == v {
			return i
		}
	}
	return -1
}

type Edge struct {
	From string
	To   string
}

func addEdge(tb testing.TB, graph *Graph, source, destination string) {
	tb.Helper()
	_, err := graph.AddEdge(source, destination)
	require.NoError(tb, err)
}

func TestToposortDuplicatedNode(t *testing.T) {
	graph := NewGraph()
	graph.AddNode("a")
	if graph.AddNode("a") {
		t.Error("not raising duplicated node error")
	}
}

func TestToposortRemoveNotExistEdge(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("a", "b")

	removed, err := graph.RemoveEdge("a", "b")
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestToposortEdgesRequireExistingEndpoints(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("a", "b")

	_, err := graph.AddEdge("missing", "b")
	require.ErrorIs(t, err, ErrNodeNotFound)
	_, err = graph.AddEdge("a", "missing")
	require.ErrorIs(t, err, ErrNodeNotFound)
	_, err = graph.RemoveEdge("missing", "b")
	require.ErrorIs(t, err, ErrNodeNotFound)
	_, err = graph.RemoveEdge("a", "missing")
	require.ErrorIs(t, err, ErrNodeNotFound)
}

func TestToposortDuplicateEdgeUpdatesInputCountOnce(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("a", "b")

	count, err := graph.AddEdge("a", "b")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	count, err = graph.AddEdge("a", "b")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assertInputCount(t, graph, "b", 1)

	removed, err := graph.RemoveEdge("a", "b")
	require.NoError(t, err)
	assert.True(t, removed)
	removed, err = graph.RemoveEdge("a", "b")
	require.NoError(t, err)
	assert.False(t, removed)
	assertInputCount(t, graph, "b", 0)
}

func assertInputCount(tb testing.TB, graph *Graph, node string, expected int) {
	tb.Helper()
	count, exists := graph.InputCount(node)
	require.True(tb, exists)
	assert.Equal(tb, expected, count)
}

func TestToposortWikipedia(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("2", "3", "5", "7", "8", "9", "10", "11")

	edges := []Edge{
		{"7", "8"},
		{"7", "11"},

		{"5", "11"},

		{"3", "8"},
		{"3", "10"},

		{"11", "2"},
		{"11", "9"},
		{"11", "10"},

		{"8", "9"},
	}

	for _, e := range edges {
		addEdge(t, graph, e.From, e.To)
	}

	result, ok := graph.Toposort()
	if !ok {
		t.Error("closed path detected in no closed pathed graph")
	}

	for _, e := range edges {
		if i, j := index(result, e.From), index(result, e.To); i > j {
			t.Errorf("dependency failed: not satisfy %v(%v) > %v(%v)", e.From, i, e.To, j)
		}
	}
}

func TestToposortCycle(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("1", "2", "3")

	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "3", "1")

	_, ok := graph.Toposort()
	if ok {
		t.Error("closed path not detected in closed pathed graph")
	}
}

func TestToposortBreadthSort(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("0", "1", "2", "3", "4")

	addEdge(t, graph, "0", "1")
	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "1", "3")
	addEdge(t, graph, "3", "4")
	addEdge(t, graph, "4", "1")
	order := graph.BreadthSort()
	expected := map[string]NodePosition{
		"0": {0, 0},
		"1": {1, 1},
		"2": {2, 2},
		"3": {2, 3},
		"4": {3, 4},
	}
	assert.Equal(t, expected, order)
}

func TestToposortBreadthSortUsesGraphOrder(t *testing.T) {
	graph := NewGraphWithInsertionOrder()
	graph.AddNodes("root-b", "child-b", "root-a", "child-a")
	addEdge(t, graph, "root-b", "child-a")
	addEdge(t, graph, "root-b", "child-b")

	assert.Equal(t, map[string]NodePosition{
		"root-b":  {Level: 0, Index: 0},
		"root-a":  {Level: 0, Index: 1},
		"child-b": {Level: 1, Index: 2},
		"child-a": {Level: 1, Index: 3},
	}, graph.BreadthSort())
}

func TestToposortInsertionOrderSurvivesNodeRemoval(t *testing.T) {
	graph := NewGraphWithInsertionOrder()
	graph.AddNodes("a", "b", "c")
	require.True(t, graph.RemoveNode("b"))
	require.True(t, graph.AddNode("aa"))

	nodes := []string{"aa", "c", "a"}
	graph.Sort(nodes)
	assert.Equal(t, []string{"a", "c", "aa"}, nodes)
}

func TestToposortDeterministicAcrossInsertionOrder(t *testing.T) {
	nodes := []string{"a", "b", "c", "d", "e", "f", "g"}
	edges := []Edge{
		{"a", "c"},
		{"a", "d"},
		{"b", "c"},
		{"b", "e"},
		{"c", "f"},
		{"d", "f"},
		{"e", "g"},
	}

	var expectedPlan []string
	var expectedBreadth map[string]NodePosition
	var expectedSerialization string
	for iteration := range 100 {
		//nolint:gosec // A reproducible pseudorandom permutation is intentional in this test.
		random := rand.New(rand.NewSource(int64(iteration)))
		shuffledNodes := append([]string(nil), nodes...)
		shuffledEdges := append([]Edge(nil), edges...)
		random.Shuffle(len(shuffledNodes), func(i, j int) {
			shuffledNodes[i], shuffledNodes[j] = shuffledNodes[j], shuffledNodes[i]
		})
		random.Shuffle(len(shuffledEdges), func(i, j int) {
			shuffledEdges[i], shuffledEdges[j] = shuffledEdges[j], shuffledEdges[i]
		})

		graph := NewGraph()
		require.True(t, graph.AddNodes(shuffledNodes...))
		for _, edge := range shuffledEdges {
			addEdge(t, graph, edge.From, edge.To)
		}

		plan, ok := graph.Toposort()
		require.True(t, ok)
		if iteration == 0 {
			expectedPlan = plan
			expectedBreadth = graph.BreadthSort()
			expectedSerialization = graph.Serialize(plan)
			continue
		}
		assert.Equal(t, expectedPlan, plan)
		assert.Equal(t, expectedBreadth, graph.BreadthSort())
		assert.Equal(t, expectedSerialization, graph.Serialize(plan))
	}
}

func TestToposortFindCycle(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("1", "2", "3", "4", "5")

	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "2", "4")
	addEdge(t, graph, "3", "1")
	addEdge(t, graph, "5", "1")

	cycle := graph.FindCycle("2")
	expected := [...]string{"2", "3", "1"}
	assert.Equal(t, expected[:], cycle)
	cycle = graph.FindCycle("5")
	assert.Empty(t, cycle)
}

func TestToposortFindParents(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("1", "2", "3", "4", "5")

	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "2", "4")
	addEdge(t, graph, "3", "1")
	addEdge(t, graph, "5", "1")

	parents := graph.FindParents("2")
	expected := [...]string{"1"}
	assert.Equal(t, expected[:], parents)
	parents = graph.FindParents("1")
	assert.Len(t, parents, 2)
	checks := [2]bool{}
	for _, p := range parents {
		switch p {
		case "3":
			checks[0] = true
		case "5":
			checks[1] = true
		}
	}
	assert.Equal(t, [2]bool{true, true}, checks)
}

func TestToposortFindChildren(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("1", "2", "3", "4", "5")

	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "2", "4")
	addEdge(t, graph, "3", "1")
	addEdge(t, graph, "5", "1")

	children := graph.FindChildren("1")
	sort.Strings(children)

	expected := [...]string{"2"}
	assert.Equal(t, expected[:], children)
	children = graph.FindChildren("2")
	sort.Strings(children)

	assert.Len(t, children, 2)
	checks := [2]bool{}
	for _, p := range children {
		switch p {
		case "3":
			checks[0] = true
		case "4":
			checks[1] = true
		}
	}
	assert.Equal(t, [2]bool{true, true}, checks)
}

func TestToposortSerialize(t *testing.T) {
	graph := NewGraph()
	graph.AddNodes("1", "2", "3", "4", "5")

	addEdge(t, graph, "1", "2")
	addEdge(t, graph, "2", "3")
	addEdge(t, graph, "2", "4")
	addEdge(t, graph, "3", "1")
	addEdge(t, graph, "5", "1")

	order := [...]string{"5", "4", "3", "2", "1"}
	gv := graph.Serialize(order[:])
	assert.Equal(t, `digraph Hercules {
  "4 1" -> "3 2"
  "3 2" -> "2 3"
  "3 2" -> "1 4"
  "2 3" -> "4 1"
  "0 5" -> "4 1"
}`, gv)
}
