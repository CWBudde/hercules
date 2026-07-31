package plumbing

import (
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// treeFile is a blob found at a path, together with the tree that holds it -
// the two halves object.ChangeEntry needs.
type treeFile struct {
	tree  *object.Tree
	entry object.TreeEntry
}

// diffTreesDirectly produces the same change set as object.DiffTree for a pair
// of trees go-git refuses to walk (see isRejectedTreePath).
//
// Simply dropping such a commit is not an option: the items downstream of
// TreeDiff carry per-file state across commits, so a missing diff leaves them
// describing a file that no longer looks that way - LineHistory notices and
// fails the run a few commits later with an integrity error. The changes have
// to be produced, so we walk the trees ourselves and skip go-git's path
// validation, which exists to keep unsafe names from reaching a worktree.
// Hercules only ever reads.
//
// This makes no attempt to match object.DiffTree's efficiency: it enumerates
// both trees whole rather than pruning subtrees whose hashes match. That is the
// wrong trade for every commit and the right one here, where it runs on the
// handful that would otherwise abort the entire repository.
func (treediff *TreeDiff) diffTreesDirectly(from, to *object.Tree) (object.Changes, error) {
	fromFiles, err := treediff.collectTreeFiles(from)
	if err != nil {
		return nil, err
	}

	toFiles, err := treediff.collectTreeFiles(to)
	if err != nil {
		return nil, err
	}

	changes := make(object.Changes, 0, len(fromFiles)+len(toFiles))
	for _, path := range sortedUnionOfPaths(fromFiles, toFiles) {
		before, existedBefore := fromFiles[path]
		after, existsAfter := toFiles[path]

		switch {
		case existedBefore && existsAfter:
			// A mode change alone counts as a modification, matching go-git,
			// whose noder folds the mode into the hash it compares.
			if before.entry.Hash == after.entry.Hash && before.entry.Mode == after.entry.Mode {
				continue
			}

			changes = append(changes, &object.Change{
				From: newChangeEntry(path, before),
				To:   newChangeEntry(path, after),
			})
		case existsAfter:
			changes = append(changes, &object.Change{To: newChangeEntry(path, after)})
		default:
			changes = append(changes, &object.Change{From: newChangeEntry(path, before)})
		}
	}

	return changes, nil
}

// collectTreeFiles maps every non-directory entry reachable from tree to its
// full path. A nil tree yields no files, which is how the first commit in a
// history is diffed against nothing.
func (treediff *TreeDiff) collectTreeFiles(tree *object.Tree) (map[string]treeFile, error) {
	files := map[string]treeFile{}
	if tree == nil {
		return files, nil
	}

	return files, treediff.collectTreeFilesUnder(tree, "", files)
}

func (treediff *TreeDiff) collectTreeFilesUnder(
	tree *object.Tree, prefix string, files map[string]treeFile,
) error {
	for _, entry := range tree.Entries {
		path := entry.Name
		if prefix != "" {
			path = prefix + "/" + entry.Name
		}

		if entry.Mode != filemode.Dir {
			files[path] = treeFile{tree: tree, entry: entry}

			continue
		}

		// tree.Tree(name) would route through FindEntry, which applies the very
		// validation we are working around, so load the subtree by hash.
		subtree, err := treediff.repository.TreeObject(entry.Hash)
		if err != nil {
			return fmt.Errorf("load subtree %s at %q: %w", entry.Hash, path, err)
		}

		if err := treediff.collectTreeFilesUnder(subtree, path, files); err != nil {
			return err
		}
	}

	return nil
}

func newChangeEntry(path string, file treeFile) object.ChangeEntry {
	return object.ChangeEntry{Name: path, Tree: file.tree, TreeEntry: file.entry}
}

// sortedUnionOfPaths keeps the change order deterministic and path-sorted, as
// object.DiffTree's depth-first walk produces it.
func sortedUnionOfPaths(from, to map[string]treeFile) []string {
	paths := make([]string, 0, len(from)+len(to))
	for path := range from {
		paths = append(paths, path)
	}

	for path := range to {
		if _, duplicate := from[path]; !duplicate {
			paths = append(paths, path)
		}
	}

	sort.Strings(paths)

	return paths
}
