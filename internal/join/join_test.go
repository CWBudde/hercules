package join

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdentityJoinLiterals(t *testing.T) {
	pa1 := [...]string{"one|one@one", "two|aaa@two"}
	pa2 := [...]string{"two|aaa@two", "three|one@one"}
	people, merged := LiteralIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 3)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people["one|one@one"])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people["two|aaa@two"])
	assert.Equal(t, JoinedIndex{2, -1, 1}, people["three|one@one"])
	assert.Equal(t, []string{"one|one@one", "two|aaa@two", "three|one@one"}, merged)
	pa1 = [...]string{"two|aaa@two", "one|one@one"}
	people, merged = LiteralIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 3)
	assert.Equal(t, JoinedIndex{1, 1, -1}, people["one|one@one"])
	assert.Equal(t, JoinedIndex{0, 0, 0}, people["two|aaa@two"])
	assert.Equal(t, JoinedIndex{2, -1, 1}, people["three|one@one"])
	assert.Equal(t, []string{"two|aaa@two", "one|one@one", "three|one@one"}, merged)
}

func TestIdentityJoinPeoples(t *testing.T) {
	pa1 := [...]string{"one|one@one", "two|aaa@two"}
	pa2 := [...]string{"two|aaa@two", "three|one@one"}
	people, merged := PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 2)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people["one|one@one"])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people["two|aaa@two"])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people["three|one@one"])
	assert.Equal(t, []string{"one|three|one@one", "two|aaa@two"}, merged)
}

func TestIdentityJoinReversedDictsIdentitiesStrikeBack(t *testing.T) {
	pa1 := [...]string{"one|one@one", "two|aaa@two", "three|three@three"}
	pa2 := [...]string{"two|aaa@two", "three|one@one"}
	people, merged := PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 4)
	assert.Len(t, merged, 2)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people["one|one@one"])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people["two|aaa@two"])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people["three|one@one"])
	assert.Equal(t, JoinedIndex{0, 2, -1}, people["three|three@three"])
	assert.Equal(t, []string{"one|three|one@one|three@three", "two|aaa@two"}, merged)

	pa1 = [...]string{"one|one@one", "two|aaa@two", "three|aaa@two"}
	people, merged = PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 4)
	assert.Len(t, merged, 1)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people["one|one@one"])
	assert.Equal(t, JoinedIndex{0, 1, 0}, people["two|aaa@two"])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people["three|one@one"])
	assert.Equal(t, JoinedIndex{0, 2, -1}, people["three|aaa@two"])
	assert.Equal(t, []string{"one|three|two|aaa@two|one@one"}, merged)
}
