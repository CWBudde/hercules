package join

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdentityJoinLiterals(t *testing.T) {
	pa1 := [...]string{testIdentityOne, testIdentityTwo}
	pa2 := [...]string{testIdentityTwo, testIdentityThree}
	people, merged := LiteralIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 3)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people[testIdentityOne])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people[testIdentityTwo])
	assert.Equal(t, JoinedIndex{2, -1, 1}, people[testIdentityThree])
	assert.Equal(t, []string{testIdentityOne, testIdentityTwo, testIdentityThree}, merged)
	pa1 = [...]string{testIdentityTwo, testIdentityOne}
	people, merged = LiteralIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 3)
	assert.Equal(t, JoinedIndex{1, 1, -1}, people[testIdentityOne])
	assert.Equal(t, JoinedIndex{0, 0, 0}, people[testIdentityTwo])
	assert.Equal(t, JoinedIndex{2, -1, 1}, people[testIdentityThree])
	assert.Equal(t, []string{testIdentityTwo, testIdentityOne, testIdentityThree}, merged)
}

func TestIdentityJoinPeoples(t *testing.T) {
	pa1 := [...]string{testIdentityOne, testIdentityTwo}
	pa2 := [...]string{testIdentityTwo, testIdentityThree}
	people, merged := PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 3)
	assert.Len(t, merged, 2)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people[testIdentityOne])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people[testIdentityTwo])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people[testIdentityThree])
	assert.Equal(t, []string{"one|three|one@one", testIdentityTwo}, merged)
}

func TestIdentityJoinReversedDictsIdentitiesStrikeBack(t *testing.T) {
	pa1 := [...]string{testIdentityOne, testIdentityTwo, "three|three@three"}
	pa2 := [...]string{testIdentityTwo, testIdentityThree}
	people, merged := PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 4)
	assert.Len(t, merged, 2)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people[testIdentityOne])
	assert.Equal(t, JoinedIndex{1, 1, 0}, people[testIdentityTwo])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people[testIdentityThree])
	assert.Equal(t, JoinedIndex{0, 2, -1}, people["three|three@three"])
	assert.Equal(t, []string{"one|three|one@one|three@three", testIdentityTwo}, merged)

	pa1 = [...]string{testIdentityOne, testIdentityTwo, "three|aaa@two"}
	people, merged = PeopleIdentities(pa1[:], pa2[:])
	assert.Len(t, people, 4)
	assert.Len(t, merged, 1)
	assert.Equal(t, JoinedIndex{0, 0, -1}, people[testIdentityOne])
	assert.Equal(t, JoinedIndex{0, 1, 0}, people[testIdentityTwo])
	assert.Equal(t, JoinedIndex{0, -1, 1}, people[testIdentityThree])
	assert.Equal(t, JoinedIndex{0, 2, -1}, people["three|aaa@two"])
	assert.Equal(t, []string{"one|three|two|aaa@two|one@one"}, merged)
}
