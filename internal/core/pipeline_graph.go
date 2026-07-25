package core

import (
	"fmt"
	"slices"

	"github.com/pkg/errors"

	"github.com/cwbudde/hercules/internal/toposort"
)

func addGraphEdge(graph *toposort.Graph, source, destination string) error {
	_, err := graph.AddEdge(source, destination)
	if err != nil {
		return errors.Wrapf(err, "add graph edge %q -> %q", source, destination)
	}

	return nil
}

func removeGraphEdge(graph *toposort.Graph, source, destination string) error {
	removed, err := graph.RemoveEdge(source, destination)
	if err != nil {
		return errors.Wrapf(err, "remove graph edge %q -> %q", source, destination)
	}

	if !removed {
		return fmt.Errorf("%w: %q -> %q", errGraphEdgeNotFound, source, destination)
	}

	return nil
}

func connectAmbiguousInput(
	key string,
	input string,
	reverseIndex int,
	graph *toposort.Graph,
) (string, bool, error) {
	err := addGraphEdge(graph, input, key)
	if err != nil {
		return "", false, err
	}

	cycle := graph.FindCycle(input)
	nextNode := input

	switch {
	case len(cycle) == 0:
		if reverseIndex != 0 {
			return "", true, nil
		}

		nextNode = key
	case len(cycle) == 1 || cycle[1] != key:
		panic("unexpected")
	default:
		if len(cycle) > 2 {
			nextNode = cycle[2]
		}

		err = removeGraphEdge(graph, key, nextNode)
	}

	return nextNode, false, err
}

func rewireAmbiguousInput(
	input string,
	key string,
	nextCycleNode string,
	graph *toposort.Graph,
) error {
	err := removeGraphEdge(graph, input, key)
	if err != nil {
		return err
	}

	return addGraphEdge(graph, input, nextCycleNode)
}

func wireAmbiguousInputs(
	key string, inputs []string, graph *toposort.Graph,
) (map[string]string, error) {
	replacements := map[string]string{}

	nextCycleNode := key
	for reverseIndex, input := range slices.Backward(inputs) {
		nextNode, skip, err := connectAmbiguousInput(key, input, reverseIndex, graph)
		if err != nil {
			return nil, err
		}

		if skip {
			continue
		}

		if nextCycleNode != key {
			err = rewireAmbiguousInput(input, key, nextCycleNode, graph)
			if err != nil {
				return nil, err
			}

			replacements[nextCycleNode] = input
		}

		nextCycleNode = nextNode
	}

	return replacements, nil
}

func repairAmbiguousChildren(
	key string, replacements map[string]string, graph *toposort.Graph,
) error {
	for _, child := range graph.FindChildren(key) {
		cycle := graph.FindCycle(child)
		if len(cycle) == 0 {
			continue
		}

		if len(cycle) < 3 || cycle[len(cycle)-1] != key {
			panic("unexpected")
		}

		replacement := replacements[cycle[len(cycle)-2]]
		if replacement == "" {
			panic("unexpected")
		}

		err := removeGraphEdge(graph, key, child)
		if err != nil {
			return err
		}

		err = addGraphEdge(graph, replacement, child)
		if err != nil {
			return err
		}
	}

	return nil
}
