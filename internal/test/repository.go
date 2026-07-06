package test

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	sivafs "github.com/cyraxred/go-billy-siva"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Repository is a boilerplate sample repository (Hercules itself).
var Repository *git.Repository

// FakeChangeForName creates an artificial Git Change from a file name and two arbitrary hashes.
func FakeChangeForName(name string, hashFrom string, hashTo string) *object.Change {
	return &object.Change{
		From: object.ChangeEntry{Name: name, TreeEntry: object.TreeEntry{
			Name: name, Hash: plumbing.NewHash(hashFrom),
		}},
		To: object.ChangeEntry{Name: name, TreeEntry: object.TreeEntry{
			Name: name, Hash: plumbing.NewHash(hashTo),
		}},
	}
}

func init() {
	cwd, err := os.Getwd()
	if err == nil {
		for true {
			files, err := ioutil.ReadDir(cwd)
			if err != nil {
				break
			}
			found := false
			for _, f := range files {
				if f.Name() == "README.md" {
					found = true
					break
				}
			}
			if found {
				break
			}
			oldCwd := cwd
			cwd = path.Dir(cwd)
			if oldCwd == cwd {
				break
			}
		}
		Repository, err = git.PlainOpen(cwd)
		if err == nil {
			iter, err := Repository.CommitObjects()
			if err == nil {
				commits := -1
				for ; err != io.EOF; _, err = iter.Next() {
					if err != nil {
						panic(err)
					}
					commits++
					if commits >= 100 {
						return
					}
				}
			}
		}
	}
	Repository, err = git.Clone(memory.NewStorage(), nil, &git.CloneOptions{
		URL: "https://github.com/src-d/hercules",
	})
	if err != nil {
		panic(err)
	}
}

// FixtureRepository returns a deterministic, network-free sample repository
// loaded from the checked-in cmd/hercules/test_data/hercules.siva fixture.
//
// Unlike Repository (which opens the ambient working tree or clones
// src-d/hercules), this fixture has a fixed commit DAG, so tests that assert
// specific commit hashes and pipeline plans are hermetic: they no longer
// depend on the checkout depth or on upstream drift. The fixture's history
// ends at its master commit 014493bed229e27d8a18b8d104e9ac062ef799e1
// (authored 2018-01-25); tests must not reference commits newer than that.
//
// It is loaded lazily so that only the tests that need it pay the cost and
// take on a hard dependency on the fixture file.
func FixtureRepository() *git.Repository {
	fixtureOnce.Do(func() {
		repo, err := openFixtureRepository()
		if err != nil {
			panic(fmt.Sprintf("failed to load the siva test fixture: %v", err))
		}
		fixtureRepo = repo
	})
	return fixtureRepo
}

var (
	fixtureOnce sync.Once
	fixtureRepo *git.Repository
)

// fixtureStorer serves objects from the read-only siva fixture while keeping
// references in a writable in-memory store. This lets us expose the fixture's
// master branch as HEAD (so Head() and Log(From: ZeroHash) work like a normal
// clone) without mutating the committed siva file — the default read-write
// siva filesystem rewrites the archive's index footer on open.
type fixtureStorer struct {
	storage.Storer
	refs storer.ReferenceStorer
}

func (s *fixtureStorer) SetReference(ref *plumbing.Reference) error {
	return s.refs.SetReference(ref)
}

func (s *fixtureStorer) CheckAndSetReference(nw, old *plumbing.Reference) error {
	return s.refs.CheckAndSetReference(nw, old)
}

func (s *fixtureStorer) Reference(n plumbing.ReferenceName) (*plumbing.Reference, error) {
	return s.refs.Reference(n)
}

func (s *fixtureStorer) IterReferences() (storer.ReferenceIter, error) {
	return s.refs.IterReferences()
}

func (s *fixtureStorer) RemoveReference(n plumbing.ReferenceName) error {
	return s.refs.RemoveReference(n)
}

func (s *fixtureStorer) CountLooseRefs() (int, error) {
	return s.refs.CountLooseRefs()
}

func (s *fixtureStorer) PackRefs() error {
	return s.refs.PackRefs()
}

// openFixtureRepository opens the checked-in hercules.siva fixture read-only and
// exposes its master branch as HEAD so callers can use Head() and
// Log(From: plumbing.ZeroHash) exactly as with a normally cloned repository.
func openFixtureRepository() (*git.Repository, error) {
	sivaPath, err := locateFixtureSiva()
	if err != nil {
		return nil, err
	}
	localFs := osfs.New(filepath.Dir(sivaPath))
	fs, err := sivafs.NewFilesystemReadOnly(localFs, filepath.Base(sivaPath), 0)
	if err != nil {
		return nil, fmt.Errorf("siva filesystem for %s: %w", sivaPath, err)
	}
	sivaStorage := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())

	// Mirror the siva's references into a writable in-memory store and add a
	// HEAD pointing at master (the siva stores refs as refs/heads/master/<uuid>
	// and has no symbolic HEAD).
	refStorer := memory.NewStorage()
	refs, err := sivaStorage.IterReferences()
	if err != nil {
		return nil, fmt.Errorf("read siva references: %w", err)
	}
	master := plumbing.ZeroHash
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if err := refStorer.SetReference(ref); err != nil {
			return err
		}
		if master == plumbing.ZeroHash && strings.HasPrefix(ref.Name().String(), "refs/heads/master/") {
			master = ref.Hash()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if master == plumbing.ZeroHash {
		return nil, fmt.Errorf("no master branch found in siva fixture %s", sivaPath)
	}
	if err := refStorer.SetReference(plumbing.NewHashReference(plumbing.HEAD, master)); err != nil {
		return nil, err
	}

	st := &fixtureStorer{Storer: sivaStorage, refs: refStorer}
	return git.Open(st, memfs.New())
}

// locateFixtureSiva resolves the absolute path to cmd/hercules/test_data/hercules.siva.
// It first derives the repository root from this source file's location and, as a
// fallback, walks up from the working directory.
func locateFixtureSiva() (string, error) {
	const rel = "cmd/hercules/test_data/hercules.siva"
	if _, self, _, ok := runtime.Caller(0); ok {
		// self == <root>/internal/test/repository.go
		root := filepath.Dir(filepath.Dir(filepath.Dir(self)))
		candidate := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if dir, err := os.Getwd(); err == nil {
		for {
			candidate := filepath.Join(dir, filepath.FromSlash(rel))
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not locate the %s fixture", rel)
}
