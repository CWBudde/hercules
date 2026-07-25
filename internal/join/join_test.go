package join

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiteralIdentities(t *testing.T) {
	first := []string{testIdentityOne, testIdentityTwo}
	second := []string{testIdentityTwo, testIdentityThree}

	mapping, merged := LiteralIdentities(first, second)

	assert.Equal(t, IdentityMapping{First: []int{0, 1}, Second: []int{1, 2}}, mapping)
	assert.Equal(t, []string{testIdentityOne, testIdentityTwo, testIdentityThree}, merged)
}

func TestLiteralIdentitiesMapsDuplicates(t *testing.T) {
	const (
		alpha = "alpha"
		beta  = "beta"
	)

	first := []string{alpha, alpha, beta}
	second := []string{beta, alpha, beta, "gamma"}

	mapping, merged := LiteralIdentities(first, second)

	assert.Equal(t, IdentityMapping{
		First:  []int{0, 0, 1},
		Second: []int{1, 0, 1, 2},
	}, mapping)
	assert.Equal(t, []string{alpha, beta, "gamma"}, merged)
}

func TestPeopleIdentities(t *testing.T) {
	first := []string{testIdentityOne, testIdentityTwo}
	second := []string{testIdentityTwo, testIdentityThree}

	mapping, merged := PeopleIdentities(first, second)

	assert.Equal(t, IdentityMapping{First: []int{0, 1}, Second: []int{1, 0}}, mapping)
	assert.Equal(t, []string{"one|three|one@one", testIdentityTwo}, merged)
}

func TestPeopleIdentitiesMapsDuplicatesWithinAndAcrossInputs(t *testing.T) {
	first := []string{testIdentityOne, testIdentityOne, testIdentityTwo}
	second := []string{testIdentityOne, testIdentityTwo, testIdentityTwo}

	mapping, merged := PeopleIdentities(first, second)

	assert.Equal(t, IdentityMapping{
		First:  []int{0, 0, 1},
		Second: []int{0, 1, 1},
	}, mapping)
	assert.Equal(t, []string{testIdentityOne, testIdentityTwo}, merged)
}

func TestPeopleIdentitiesMapsTransitiveOverlap(t *testing.T) {
	first := []string{"alice|alice@example.com", "bob|bob@example.com"}
	second := []string{"ally|alice@example.com|bob@example.com"}

	mapping, merged := PeopleIdentities(first, second)

	assert.Equal(t, IdentityMapping{First: []int{0, 0}, Second: []int{0}}, mapping)
	assert.Equal(t,
		[]string{"alice|ally|bob|alice@example.com|bob@example.com"},
		merged,
	)
}

func TestPeopleIdentitiesMapsNameEmailAndMixedRecords(t *testing.T) {
	first := []string{"Alice", "alice@example.com"}
	second := []string{"Alice|alice@example.com", "Bob"}

	mapping, merged := PeopleIdentities(first, second)

	assert.Equal(t, IdentityMapping{First: []int{0, 0}, Second: []int{0, 1}}, mapping)
	assert.Equal(t, []string{"Alice|alice@example.com", "Bob"}, merged)
}

func TestPeopleIdentitiesDoesNotCollideUnrelatedRecords(t *testing.T) {
	first := []string{"same-name@example.com", "same-name"}
	second := []string{"other", "other@example.com"}

	mapping, merged := PeopleIdentities(first, second)

	assert.Equal(t, IdentityMapping{First: []int{0, 1}, Second: []int{2, 3}}, mapping)
	assert.Equal(t,
		[]string{"same-name@example.com", "same-name", "other", "other@example.com"},
		merged,
	)
}

func TestPeopleIdentitiesMergeAssociative(t *testing.T) {
	first := []string{"zara|zara@example.com", "unrelated"}
	second := []string{"alice|shared@example.com", "zara"}
	third := []string{"ally|shared@example.com|zara@example.com", "third"}

	firstSecond := mergePeopleOnly(t, first, second)
	left := mergePeopleOnly(t, firstSecond, third)
	secondThird := mergePeopleOnly(t, second, third)
	right := mergePeopleOnly(t, first, secondThird)

	assert.Equal(t, left, right)
	assert.Equal(t, []string{
		"alice|ally|zara|shared@example.com|zara@example.com",
		"unrelated",
		"third",
	}, left)
}

func TestLiteralIdentitiesMergeAssociative(t *testing.T) {
	first := []string{"z", "a"}
	second := []string{"a", "b"}
	third := []string{"z", "c"}

	_, firstSecond := LiteralIdentities(first, second)
	_, left := LiteralIdentities(firstSecond, third)
	_, secondThird := LiteralIdentities(second, third)
	_, right := LiteralIdentities(first, secondThird)

	assert.Equal(t, left, right)
}

func mergePeopleOnly(t *testing.T, first, second []string) []string {
	t.Helper()

	mapping, merged := PeopleIdentities(first, second)
	require.Len(t, mapping.First, len(first))
	require.Len(t, mapping.Second, len(second))

	return merged
}
