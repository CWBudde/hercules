package join

import (
	"sort"
	"strings"
)

// JoinedIndex is the result of merging `rd1[First]` and `rd2[Second]`: the index in the final reversed
// dictionary. -1 for `First` or `Second` means that the corresponding string does not exist
// in respectively `rd1` and `rd2`.
// See also:
// * LiteralIdentities()
// * PeopleIdentities().
type JoinedIndex struct {
	Final  int
	First  int
	Second int
}

// LiteralIdentities joins two string lists together, excluding duplicates, in-order.
// The string comparisons are the usual ones.
// The returned mapping's keys are the unique strings in `rd1 ∪ rd2`, and the values are:
// 1. Index after merging.
// 2. Corresponding index in the first array - `rd1`. -1 means that it does not exist.
// 3. Corresponding index in the second array - `rd2`. -1 means that it does not exist.
func LiteralIdentities(rd1, rd2 []string) (map[string]JoinedIndex, []string) {
	people := map[string]JoinedIndex{}
	for i, pid := range rd1 {
		people[pid] = JoinedIndex{len(people), i, -1}
	}

	for i, pid := range rd2 {
		if ptrs, exists := people[pid]; !exists {
			people[pid] = JoinedIndex{len(people), -1, i}
		} else {
			people[pid] = JoinedIndex{ptrs.Final, ptrs.First, i}
		}
	}

	mrd := make([]string, len(people))
	for name, ptrs := range people {
		mrd[ptrs.Final] = name
	}

	return people, mrd
}

type identityPair struct {
	Index1 int
	Index2 int
}

// PeopleIdentities joins two identity lists together, excluding duplicates.
// The strings are split by "|" and we find the connected components..
// The returned mapping's keys are the unique strings in `rd1 ∪ rd2`, and the values are:
// 1. Index after merging.
// 2. Corresponding index in the first array - `rd1`. -1 means that it does not exist.
// 3. Corresponding index in the second array - `rd2`. -1 means that it does not exist.
func PeopleIdentities(rd1, rd2 []string) (map[string]JoinedIndex, []string) {
	vocabulary, vertices1, vertices2 := identityGraph(rd1, rd2)
	walks := identityComponents(vocabulary, vertices1, vertices2)

	return joinedPeopleIndex(rd1, rd2, vocabulary, walks)
}

func identityGraph(
	first, second []string,
) (map[string]identityPair, [][]string, [][]string) {
	vocabulary := map[string]identityPair{}
	firstVertices := identityVertices(first, func(index int, key string) {
		vocabulary[key] = identityPair{index, -1}
	})
	secondVertices := identityVertices(second, func(index int, key string) {
		pair, exists := vocabulary[key]
		if !exists {
			pair.Index1 = -1
		}

		pair.Index2 = index
		vocabulary[key] = pair
	})

	return vocabulary, firstVertices, secondVertices
}

func identityVertices(identities []string, add func(int, string)) [][]string {
	vertices := make([][]string, len(identities))
	for index, identity := range identities {
		vertices[index] = strings.Split(identity, "|")
		for _, key := range vertices[index] {
			add(index, key)
		}
	}

	return vertices
}

func identityComponents(
	vocabulary map[string]identityPair, first, second [][]string,
) []map[string]bool {
	var components []map[string]bool
	visited := map[string]bool{}

	for _, vertices := range [][][]string{first, second} {
		for _, vertex := range vertices {
			if componentVisited(vertex, visited) {
				continue
			}

			component := walkIdentityComponent(vertex, vocabulary, first, second)
			for key := range component {
				visited[key] = true
			}

			components = append(components, component)
		}
	}

	return components
}

func componentVisited(vertex []string, visited map[string]bool) bool {
	for _, key := range vertex {
		if visited[key] {
			return true
		}
	}

	return false
}

func walkIdentityComponent(
	root []string, vocabulary map[string]identityPair, first, second [][]string,
) map[string]bool {
	component, pending := map[string]bool{}, map[string]bool{}
	for _, key := range root {
		pending[key] = true
	}

	for len(pending) > 0 {
		var element string
		for element = range pending {
			delete(pending, element)
			break
		}

		if component[element] {
			continue
		}

		component[element] = true

		pair := vocabulary[element]
		if pair.Index1 >= 0 {
			addPendingIdentities(pending, component, first[pair.Index1])
		}

		if pair.Index2 >= 0 {
			addPendingIdentities(pending, component, second[pair.Index2])
		}
	}

	return component
}

func addPendingIdentities(pending, component map[string]bool, identities []string) {
	for _, identity := range identities {
		if !component[identity] {
			pending[identity] = true
		}
	}
}

func joinedPeopleIndex(
	rd1, rd2 []string, vocabulary map[string]identityPair, walks []map[string]bool,
) (map[string]JoinedIndex, []string) {
	mergedStrings := make([]string, 0, len(walks))
	mergedIndex := map[string]JoinedIndex{}

	for walkIndex, walk := range walks {
		ids := make([]string, 0, len(walk))
		for key := range walk {
			ids = append(ids, key)
		}

		sort.Slice(ids, func(i, j int) bool {
			iid := ids[i]
			jid := ids[j]
			iHasAt := strings.ContainsRune(iid, '@')

			jHasAt := strings.ContainsRune(jid, '@')
			if iHasAt == jHasAt {
				return iid < jid
			}

			return jHasAt
		})

		mergedStrings = append(mergedStrings, strings.Join(ids, "|"))
		indexIdentityComponent(mergedIndex, ids, walkIndex, rd1, rd2, vocabulary)
	}

	return mergedIndex, mergedStrings
}

func indexIdentityComponent(
	merged map[string]JoinedIndex, identities []string, final int,
	first, second []string, vocabulary map[string]identityPair,
) {
	for _, key := range identities {
		pair := vocabulary[key]
		if pair.Index1 >= 0 {
			identity := first[pair.Index1]

			current, exists := merged[identity]
			if !exists {
				current = JoinedIndex{Final: final, First: pair.Index1, Second: -1}
			} else {
				current.First = pair.Index1
			}

			merged[identity] = current
		}

		if pair.Index2 >= 0 {
			identity := second[pair.Index2]

			current, exists := merged[identity]
			if !exists {
				current = JoinedIndex{Final: final, First: -1, Second: pair.Index2}
			} else {
				current.Second = pair.Index2
			}

			merged[identity] = current
		}
	}
}

// RepositoryIdentities joins two repository name lists together, excluding duplicates.
// Repository names are treated as literal strings (unlike PeopleIdentities which handles complex identities).
// This is essentially an alias for LiteralIdentities but provides clearer semantics for repository merging.
// The returned mapping's keys are the unique repository names in `repos1 ∪ repos2`, and the values are:
// 1. Index after merging.
// 2. Corresponding index in the first array - `repos1`. -1 means that it does not exist.
// 3. Corresponding index in the second array - `repos2`. -1 means that it does not exist.
func RepositoryIdentities(repos1, repos2 []string) (map[string]JoinedIndex, []string) {
	return LiteralIdentities(repos1, repos2)
}
