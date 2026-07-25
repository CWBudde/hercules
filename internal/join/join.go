package join

import (
	"sort"
	"strings"
)

// IdentityMapping maps every position in the two source dictionaries to its
// position in the merged dictionary.
type IdentityMapping struct {
	First  []int
	Second []int
}

// LiteralIdentities joins two literal identity lists in first-seen order.
// Duplicate source records are retained in the mapping but emitted only once.
func LiteralIdentities(first, second []string) (IdentityMapping, []string) {
	mapping := IdentityMapping{
		First:  make([]int, len(first)),
		Second: make([]int, len(second)),
	}
	merged := make([]string, 0, len(first)+len(second))
	destinations := make(map[string]int, len(first)+len(second))

	indexLiterals(first, mapping.First, destinations, &merged)
	indexLiterals(second, mapping.Second, destinations, &merged)

	return mapping, merged
}

func indexLiterals(
	identities []string, mapping []int, destinations map[string]int, merged *[]string,
) {
	for source, identity := range identities {
		destination, exists := destinations[identity]
		if !exists {
			destination = len(*merged)
			destinations[identity] = destination
			*merged = append(*merged, identity)
		}

		mapping[source] = destination
	}
}

// PeopleIdentities joins two identity lists by treating "|" separated alias
// tokens as vertices in a graph. Every source record is mapped, including exact
// duplicates and records connected through transitive aliases. Components are
// emitted in first-record order; within each canonical identity, names precede
// email addresses and each group is sorted lexically.
func PeopleIdentities(first, second []string) (IdentityMapping, []string) {
	vertices, sets := identityGraph(first, second)
	roots := orderedIdentityRoots(vertices, sets)
	destinations, merged := mergedPeople(roots, sets)

	return mapPeopleRecords(vertices, len(first), sets, destinations), merged
}

func identityGraph(first, second []string) ([][]int, *disjointSet) {
	identities := make([]string, 0, len(first)+len(second))
	identities = append(identities, first...)
	identities = append(identities, second...)

	vertices := make([][]int, len(identities))
	sets := newDisjointSet()
	tokenIndexes := make(map[string]int)

	for record, identity := range identities {
		tokens := uniqueIdentityTokens(identity)

		vertices[record] = make([]int, len(tokens))
		for index, token := range tokens {
			vertices[record][index] = sets.indexToken(token, tokenIndexes)
		}

		for index := 1; index < len(vertices[record]); index++ {
			sets.union(vertices[record][0], vertices[record][index])
		}
	}

	return vertices, sets
}

func uniqueIdentityTokens(identity string) []string {
	tokens := strings.Split(identity, "|")
	unique := make([]string, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))

	for _, token := range tokens {
		if seen[token] {
			continue
		}

		seen[token] = true
		unique = append(unique, token)
	}

	return unique
}

func orderedIdentityRoots(vertices [][]int, sets *disjointSet) []int {
	roots := make([]int, 0, len(vertices))
	seen := make(map[int]bool, len(vertices))

	for _, vertex := range vertices {
		root := sets.find(vertex[0])
		if seen[root] {
			continue
		}

		seen[root] = true
		roots = append(roots, root)
	}

	return roots
}

func mergedPeople(roots []int, sets *disjointSet) (map[int]int, []string) {
	tokensByRoot := make(map[int][]string, len(roots))

	for index, token := range sets.tokens {
		root := sets.find(index)
		tokensByRoot[root] = append(tokensByRoot[root], token)
	}

	destinations := make(map[int]int, len(roots))

	merged := make([]string, len(roots))
	for destination, root := range roots {
		destinations[root] = destination
		merged[destination] = canonicalIdentity(tokensByRoot[root])
	}

	return destinations, merged
}

func canonicalIdentity(tokens []string) string {
	sort.Slice(tokens, func(first, second int) bool {
		firstEmail := strings.ContainsRune(tokens[first], '@')

		secondEmail := strings.ContainsRune(tokens[second], '@')
		if firstEmail != secondEmail {
			return !firstEmail
		}

		return tokens[first] < tokens[second]
	})

	return strings.Join(tokens, "|")
}

func mapPeopleRecords(
	vertices [][]int, firstLength int, sets *disjointSet, destinations map[int]int,
) IdentityMapping {
	mapping := IdentityMapping{
		First:  make([]int, firstLength),
		Second: make([]int, len(vertices)-firstLength),
	}

	for source, vertex := range vertices {
		destination := destinations[sets.find(vertex[0])]
		if source < firstLength {
			mapping.First[source] = destination
		} else {
			mapping.Second[source-firstLength] = destination
		}
	}

	return mapping
}

type disjointSet struct {
	parents []int
	ranks   []int
	tokens  []string
}

func newDisjointSet() *disjointSet {
	return &disjointSet{}
}

func (sets *disjointSet) indexToken(token string, indexes map[string]int) int {
	if index, exists := indexes[token]; exists {
		return index
	}

	index := len(sets.parents)
	indexes[token] = index
	sets.parents = append(sets.parents, index)
	sets.ranks = append(sets.ranks, 0)
	sets.tokens = append(sets.tokens, token)

	return index
}

func (sets *disjointSet) find(index int) int {
	if sets.parents[index] != index {
		sets.parents[index] = sets.find(sets.parents[index])
	}

	return sets.parents[index]
}

func (sets *disjointSet) union(first, second int) {
	firstRoot := sets.find(first)

	secondRoot := sets.find(second)
	if firstRoot == secondRoot {
		return
	}

	if sets.ranks[firstRoot] < sets.ranks[secondRoot] {
		firstRoot, secondRoot = secondRoot, firstRoot
	}

	sets.parents[secondRoot] = firstRoot
	if sets.ranks[firstRoot] == sets.ranks[secondRoot] {
		sets.ranks[firstRoot]++
	}
}

// RepositoryIdentities joins repository names as literal strings.
func RepositoryIdentities(first, second []string) (IdentityMapping, []string) {
	return LiteralIdentities(first, second)
}
