package plumbing

import (
	"errors"
	"sort"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
)

// Upstream Git commits these names without complaint, and we have repositories
// that contain them, but go-git refuses to walk a tree that carries one.
var rejectedTreePaths = []struct {
	name string
	path string
}{
	{"lone backslash", `\`},
	{"embedded newline", "service-frps-control\nfrps-vhost.yaml"},
}

// The changes still have to come out: dropping them leaves the items downstream
// of TreeDiff describing files that have since moved on, and LineHistory fails
// the run on the mismatch a few commits later.
func TestTreeDiffProducesChangesForTreesGoGitWillNotWalk(t *testing.T) {
	for _, testCase := range rejectedTreePaths {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newMemoryRepository(t)
			blob := storeBlob(t, repository, "package main\n")
			treeHash := storeTree(t, repository,
				object.TreeEntry{Name: testCase.path, Mode: filemode.Regular, Hash: blob},
				object.TreeEntry{Name: "ordinary.go", Mode: filemode.Regular, Hash: blob},
			)
			commit := storeCommit(t, repository, treeHash)

			// Confirm the premise: go-git really does reject this tree, so a
			// green test cannot come from the entry having become walkable.
			tree, err := commit.Tree()
			require.NoError(t, err)
			_, err = object.DiffTree(&object.Tree{}, tree)
			require.Error(t, err, "go-git accepted %q - this test no longer proves anything", testCase.path)

			treediff := &TreeDiff{}
			require.NoError(t, treediff.Configure(nil))
			require.NoError(t, treediff.Initialize(repository))

			result, err := treediff.Consume(map[string]any{core.DependencyCommit: commit})
			require.NoError(t, err)

			changes := result[DependencyTreeChanges].(object.Changes)
			inserted := map[string]merkletrie.Action{}
			for _, change := range changes {
				action, err := change.Action()
				require.NoError(t, err)
				inserted[change.To.Name] = action
			}

			assert.Equal(t, map[string]merkletrie.Action{
				testCase.path: merkletrie.Insert,
				"ordinary.go": merkletrie.Insert,
			}, inserted, "both files must be reported, not just the walkable one")

			// The commit still has to become the baseline for the next one.
			assert.Equal(t, tree.Hash, treediff.previousTree.Hash)
			assert.Equal(t, commit.Hash, treediff.previousCommit)
		})
	}
}

// The direct walk has to agree with go-git wherever go-git is willing to answer,
// including inside subdirectories and for modifications and deletions.
func TestDiffTreesDirectlyMatchesGoGit(t *testing.T) {
	repository := newMemoryRepository(t)
	before := storeBlob(t, repository, "before\n")
	after := storeBlob(t, repository, "after\n")

	nestedBefore := storeTree(t, repository,
		object.TreeEntry{Name: "kept.go", Mode: filemode.Regular, Hash: before},
		object.TreeEntry{Name: "removed.go", Mode: filemode.Regular, Hash: before},
	)
	nestedAfter := storeTree(t, repository,
		object.TreeEntry{Name: "kept.go", Mode: filemode.Regular, Hash: before},
	)

	fromHash := storeTree(t, repository,
		object.TreeEntry{Name: "changed.go", Mode: filemode.Regular, Hash: before},
		object.TreeEntry{Name: "nested", Mode: filemode.Dir, Hash: nestedBefore},
	)
	toHash := storeTree(t, repository,
		object.TreeEntry{Name: "added.go", Mode: filemode.Regular, Hash: after},
		object.TreeEntry{Name: "changed.go", Mode: filemode.Regular, Hash: after},
		object.TreeEntry{Name: "nested", Mode: filemode.Dir, Hash: nestedAfter},
	)

	from, err := repository.TreeObject(fromHash)
	require.NoError(t, err)
	to, err := repository.TreeObject(toHash)
	require.NoError(t, err)

	expected, err := object.DiffTree(from, to)
	require.NoError(t, err)

	treediff := &TreeDiff{}
	require.NoError(t, treediff.Configure(nil))
	require.NoError(t, treediff.Initialize(repository))

	actual, err := treediff.diffTreesDirectly(from, to)
	require.NoError(t, err)

	assert.Equal(t, describeChanges(t, expected), describeChanges(t, actual))
}

// Everything else - a missing object, a corrupt pack - must still be fatal.
func TestTreeDiffStillFailsOnRealErrors(t *testing.T) {
	repository := newMemoryRepository(t)
	blob := storeBlob(t, repository, "package main\n")
	treeHash := storeTree(t, repository,
		object.TreeEntry{Name: "readable.go", Mode: filemode.Regular, Hash: blob})
	commit := storeCommit(t, repository, treeHash)
	commit.TreeHash = plumbing.NewHash("0000000000000000000000000000000000000000")

	treediff := &TreeDiff{}
	require.NoError(t, treediff.Configure(nil))
	require.NoError(t, treediff.Initialize(repository))

	_, err := treediff.Consume(map[string]any{core.DependencyCommit: commit})
	assert.Error(t, err)
}

func TestIsRejectedTreePath(t *testing.T) {
	assert.False(t, isRejectedTreePath(nil))
	assert.False(t, isRejectedTreePath(plumbing.ErrObjectNotFound))
	assert.False(t, isRejectedTreePath(object.ErrEntryNotFound))

	// The two shapes go-git actually produces, as seen in the wild.
	assert.True(t, isRejectedTreePath(errors.New(`to: invalid path: "\\"`)))
	assert.True(t, isRejectedTreePath(
		errors.New(`invalid path "service-frps-control\nfrps-vhost.yaml": contains control character`)))
}

// describeChanges reduces changes to what TreeDiff's consumers actually read off
// them: the path, the action, and the blob on either side.
func describeChanges(t *testing.T, changes object.Changes) []string {
	t.Helper()

	described := make([]string, 0, len(changes))
	for _, change := range changes {
		action, err := change.Action()
		require.NoError(t, err)

		described = append(described, action.String()+" "+change.From.Name+" -> "+change.To.Name+
			" "+change.From.TreeEntry.Hash.String()+" "+change.To.TreeEntry.Hash.String())
	}

	return described
}

func newMemoryRepository(t *testing.T) *git.Repository {
	t.Helper()

	repository, err := git.Init(memory.NewStorage(), memfs.New())
	require.NoError(t, err)

	return repository
}

func storeBlob(t *testing.T, repository *git.Repository, content string) plumbing.Hash {
	t.Helper()

	blob := &plumbing.MemoryObject{}
	blob.SetType(plumbing.BlobObject)
	_, err := blob.Write([]byte(content))
	require.NoError(t, err)

	hash, err := repository.Storer.SetEncodedObject(blob)
	require.NoError(t, err)

	return hash
}

// storeTree writes the entries as a tree object directly, bypassing the
// porcelain that would refuse the names this test is about.
func storeTree(t *testing.T, repository *git.Repository, entries ...object.TreeEntry) plumbing.Hash {
	t.Helper()

	// Git requires tree entries in name order, and so does go-git's encoder.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	return storeObject(t, repository, &object.Tree{Entries: entries})
}

func storeCommit(t *testing.T, repository *git.Repository, tree plumbing.Hash) *object.Commit {
	t.Helper()

	hash := storeObject(t, repository, &object.Commit{TreeHash: tree, Message: "test"})

	commit, err := repository.CommitObject(hash)
	require.NoError(t, err)

	return commit
}

func storeObject(t *testing.T, repository *git.Repository, encoder interface {
	Encode(plumbing.EncodedObject) error
},
) plumbing.Hash {
	t.Helper()

	obj := &plumbing.MemoryObject{}
	require.NoError(t, encoder.Encode(obj))

	hash, err := repository.Storer.SetEncodedObject(obj)
	require.NoError(t, err)

	return hash
}
