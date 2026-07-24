package identity

import (
	"errors"
	"io"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/test"
)

func fixturePeopleDetector() *PeopleDetector {
	peopleDict := map[string]int{}
	peopleDict["vadim@sourced.tech"] = 0
	peopleDict["gmarkhor@gmail.com"] = 0
	reversePeopleDict := make([]string, 1)
	reversePeopleDict[0] = "Vadim"
	id := PeopleDetector{
		PeopleDict:         peopleDict,
		ReversedPeopleDict: reversePeopleDict,
	}
	_ = id.Initialize(test.Repository)
	return &id
}

func TestPeopleDetectorMeta(t *testing.T) {
	id := fixturePeopleDetector()
	assert.Equal(t, "PeopleDetector", id.Name())
	assert.Empty(t, id.Requires())
	assert.Len(t, id.Provides(), 1)
	assert.Equal(t, DependencyAuthor, id.Provides()[0])
	opts := id.ListConfigurationOptions()
	assert.Len(t, opts, 4)
	assert.Equal(t, ConfigIdentityDetectorPeopleDictPath, opts[0].Name)
	assert.Equal(t, ConfigIdentityDetectorExactSignatures, opts[1].Name)
	assert.Equal(t, ConfigIdentityDetectorAnonymity, opts[2].Name)
	assert.Equal(t, ConfigIdentityDetectorMergeThreshold, opts[3].Name)
	logger := core.NewLogger()
	assert.NoError(t, id.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, id.l)
}

func TestPeopleDetectorConfigure(t *testing.T) {
	id := fixturePeopleDetector()
	facts := map[string]any{}
	m1 := map[string]int{"one": 0}
	m2 := []string{"one"}
	facts[FactIdentityDetectorReversedPeopleDict] = m2
	assert.NoError(t, id.Configure(facts))
	assert.Equal(t, m2, facts[FactIdentityDetectorReversedPeopleDict])
	assert.Equal(t, m1, id.PeopleDict)
	assert.Equal(t, m2, id.ReversedPeopleDict)

	tmpf, err := os.CreateTemp(t.TempDir(), "hercules-test-")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpf.Name()) }()
	_, err = tmpf.WriteString("Egor|egor@sourced.tech\nVadim|vadim@sourced.tech")
	assert.NoError(t, err)
	assert.NoError(t, tmpf.Close())
	delete(facts, FactIdentityDetectorReversedPeopleDict)
	facts[ConfigIdentityDetectorPeopleDictPath] = tmpf.Name()
	assert.NoError(t, id.Configure(facts))
	assert.Len(t, id.PeopleDict, 4)
	assert.Len(t, id.ReversedPeopleDict, 2)
	assert.Equal(t, "Vadim", id.ReversedPeopleDict[1])
	delete(facts, FactIdentityDetectorReversedPeopleDict)

	id = fixturePeopleDetector()
	id.PeopleDict = nil
	assert.NoError(t, id.Configure(facts))
	assert.Equal(t, id.ReversedPeopleDict, facts[FactIdentityDetectorReversedPeopleDict])
	assert.Len(t, id.PeopleDict, 4)
	assert.Len(t, id.ReversedPeopleDict, 2)
	assert.Equal(t, "Egor", id.ReversedPeopleDict[0])
	delete(facts, FactIdentityDetectorReversedPeopleDict)
	id = fixturePeopleDetector()
	id.ReversedPeopleDict = nil
	assert.NoError(t, id.Configure(facts))
	assert.Equal(t, id.ReversedPeopleDict, facts[FactIdentityDetectorReversedPeopleDict])
	assert.Len(t, id.PeopleDict, 4)
	assert.Len(t, id.ReversedPeopleDict, 2)
	assert.Equal(t, "Egor", id.ReversedPeopleDict[0])
	delete(facts, FactIdentityDetectorReversedPeopleDict)
	delete(facts, ConfigIdentityDetectorPeopleDictPath)
	commits := make([]*object.Commit, 0)
	iter, err := test.Repository.CommitObjects()
	assert.NoError(t, err)
	commit, err := iter.Next()
	for ; !errors.Is(err, io.EOF); commit, err = iter.Next() {
		if err != nil {
			panic(err)
		}
		commits = append(commits, commit)
	}
	facts[core.ConfigPipelineCommits] = commits
	id = fixturePeopleDetector()
	id.PeopleDict = nil
	id.ReversedPeopleDict = nil
	assert.NoError(t, id.Configure(facts))
	assert.Equal(t, id.ReversedPeopleDict, facts[FactIdentityDetectorReversedPeopleDict])
	assert.GreaterOrEqual(t, len(id.PeopleDict), 3)
	assert.GreaterOrEqual(t, len(id.ReversedPeopleDict), 4)
}

func TestPeopleDetectorRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&PeopleDetector{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "PeopleDetector", summoned[0].Name())
	summoned = core.Registry.Summon((&PeopleDetector{}).Provides()[0])
	assert.Equal(t, "PeopleDetector", summoned[0].Name())
}

func TestPeopleDetectorConfigureEmpty(t *testing.T) {
	id := PeopleDetector{}
	assert.Panics(t, func() { _ = id.Configure(map[string]any{}) })
}

func TestPeopleDetectorConsume(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"5c0e755dd85ac74584d9988cc361eccf02ce1a48",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	res, err := fixturePeopleDetector().Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, 0, res[DependencyAuthor].(int))
	commit, _ = test.Repository.CommitObject(plumbing.NewHash(
		"8a03b5620b1caa72ec9cb847ea88332621e2950a",
	))
	deps[core.DependencyCommit] = commit
	res, err = fixturePeopleDetector().Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, core.AuthorMissing, res[DependencyAuthor].(int))
}

func TestPeopleDetectorConsumeExact(t *testing.T) {
	commit, _ := test.Repository.CommitObject(plumbing.NewHash(
		"5c0e755dd85ac74584d9988cc361eccf02ce1a48",
	))
	deps := map[string]any{}
	deps[core.DependencyCommit] = commit
	id := fixturePeopleDetector()
	id.ExactSignatures = true
	id.PeopleDict = map[string]int{
		"vadim markovtsev <gmarkhor@gmail.com>": 0,
		"vadim markovtsev <vadim@sourced.tech>": 1,
	}
	res, err := id.Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, 1, res[DependencyAuthor].(int))
	commit, _ = test.Repository.CommitObject(plumbing.NewHash(
		"8a03b5620b1caa72ec9cb847ea88332621e2950a",
	))
	deps[core.DependencyCommit] = commit
	res, err = id.Consume(deps)
	assert.NoError(t, err)
	assert.Equal(t, core.AuthorMissing, res[DependencyAuthor].(int))
}

func TestPeopleDetectorLoadPeopleDict(t *testing.T) {
	id := fixturePeopleDetector()
	err := id.LoadPeopleDict(path.Join("..", "..", "test_data", "identities"))
	assert.NoError(t, err)
	assert.Len(t, id.PeopleDict, 10)
	assert.Contains(t, id.PeopleDict, "linus torvalds")
	assert.Contains(t, id.PeopleDict, "torvalds@linux-foundation.org")
	assert.Contains(t, id.PeopleDict, "vadim markovtsev")
	assert.Contains(t, id.PeopleDict, "vadim@sourced.tech")
	assert.Contains(t, id.PeopleDict, "another@one.com")
	assert.Contains(t, id.PeopleDict, "máximo cuadros")
	assert.Contains(t, id.PeopleDict, "maximo@sourced.tech")
	assert.Contains(t, id.PeopleDict, "duplicate")
	assert.Contains(t, id.PeopleDict, "first@example.com")
	assert.Contains(t, id.PeopleDict, "second@example.com")

	assert.Len(t, id.ReversedPeopleDict, 4)
	assert.Equal(t, "Linus Torvalds", id.ReversedPeopleDict[0])
	assert.Equal(t, "Vadim Markovtsev", id.ReversedPeopleDict[1])
	assert.Equal(t, "Máximo Cuadros", id.ReversedPeopleDict[2])
	assert.Equal(t, "Duplicate", id.ReversedPeopleDict[3])

	assert.Equal(t, id.PeopleDict["duplicate"], id.PeopleDict["first@example.com"])
	assert.Equal(t, id.PeopleDict["duplicate"], id.PeopleDict["second@example.com"])

	assert.Equal(t, id.PeopleDict["vadim markovtsev"], id.PeopleDict["vadim@sourced.tech"])
	assert.Equal(t, id.PeopleDict["vadim markovtsev"], id.PeopleDict["another@one.com"])

	assert.NotEqual(t, id.PeopleDict["duplicate"], id.PeopleDict["vadim markovtsev"])
}

func TestPeopleDetectorLoadPeopleDictWrongPath(t *testing.T) {
	id := fixturePeopleDetector()
	err := id.LoadPeopleDict(path.Join("identities"))
	assert.Error(t, err)
}

func TestPeopleDetectorGeneratePeopleDict(t *testing.T) {
	id := fixturePeopleDetector()
	commits := make([]*object.Commit, 0)
	iter, err := test.Repository.CommitObjects()
	assert.NoError(t, err)
	commit, err := iter.Next()
	for ; !errors.Is(err, io.EOF); commit, err = iter.Next() {
		if err != nil {
			panic(err)
		}
		commits = append(commits, commit)
	}
	{
		i := 0
		for ; commits[i].Author.Name != "Vadim Markovtsev"; i++ {
		}
		if i > 0 {
			commits[0], commits[i] = commits[i], commits[0]
		}
		i = 1
		for ; commits[i].Author.Name != "Alexander Bezzubov"; i++ {
		}
		if i > 0 {
			commits[1], commits[i] = commits[i], commits[1]
		}
		i = 2
		for ; commits[i].Author.Name != "Máximo Cuadros"; i++ {
		}
		if i > 0 {
			commits[2], commits[i] = commits[i], commits[2]
		}
	}
	id.GeneratePeopleDict(commits)
	assert.GreaterOrEqual(t, len(id.PeopleDict), 7)
	assert.GreaterOrEqual(t, len(id.ReversedPeopleDict), 3)
	assert.Equal(t, 0, id.PeopleDict["vadim markovtsev"])
	assert.Equal(t, 0, id.PeopleDict["vadim@sourced.tech"])
	assert.Equal(t, 0, id.PeopleDict["gmarkhor@gmail.com"])
	assert.Equal(t, 1, id.PeopleDict["alexander bezzubov"])
	assert.Equal(t, 1, id.PeopleDict["bzz@apache.org"])
	assert.Equal(t, 2, id.PeopleDict["máximo cuadros"])
	assert.Equal(t, 2, id.PeopleDict["mcuadros@gmail.com"])
	assert.Equal(t, "vadim markovtsev|gmarkhor@gmail.com|vadim@athenian.co|vadim@sourced.tech", id.ReversedPeopleDict[0])
	assert.Equal(t, "alexander bezzubov|bzz@apache.org", id.ReversedPeopleDict[1])
	assert.Equal(t, "máximo cuadros|mcuadros@gmail.com", id.ReversedPeopleDict[2])
	assert.NotEqual(t, core.AuthorMissingName, id.ReversedPeopleDict[len(id.ReversedPeopleDict)-1])
}

func TestPeopleDetectorGeneratePeopleDictExact(t *testing.T) {
	id := fixturePeopleDetector()
	id.ExactSignatures = true
	commits := make([]*object.Commit, 0)
	iter, err := test.Repository.CommitObjects()
	assert.NoError(t, err)
	commit, err := iter.Next()
	for ; !errors.Is(err, io.EOF); commit, err = iter.Next() {
		if err != nil {
			panic(err)
		}
		commits = append(commits, commit)
	}
	id.GeneratePeopleDict(commits)
	ass := assert.New(t)
	ass.Len(id.ReversedPeopleDict, len(id.PeopleDict))
	ass.GreaterOrEqual(len(id.ReversedPeopleDict), 24)
	ass.Contains(id.PeopleDict, "vadim markovtsev <vadim@sourced.tech>")
	ass.Contains(id.PeopleDict, "vadim markovtsev <vadim@athenian.co>")
	ass.NotEqual(core.AuthorMissingName, id.ReversedPeopleDict[len(id.ReversedPeopleDict)-1])
}

func TestPeopleDetectorCoAuthorTrailers(t *testing.T) {
	id := PeopleDetector{}
	id.GeneratePeopleDict([]*object.Commit{
		getFakeCommitWithSignature(
			"Alice Example", "alice@users.noreply.github.com",
			"Pairing work\n\nCo-authored-by: Alice Example <alice@example.com>",
		),
	})

	audit := id.IdentityAudit()
	assert.Len(t, audit.Identities, 1)
	assert.Equal(t, "alice example|alice@example.com|alice@users.noreply.github.com", audit.Identities[0].PeopleDictLine)
	assert.Len(t, audit.MergeDecisions, 1)
	assert.Equal(t, "co-authored-by", audit.MergeDecisions[0].Reason)
	assert.InDelta(t, 1.0, audit.MergeDecisions[0].Confidence, 0.00001)
	assert.Empty(t, audit.Ambiguous)
}

func TestPeopleDetectorFuzzyIdentityMerges(t *testing.T) {
	id := PeopleDetector{}
	id.GeneratePeopleDict([]*object.Commit{
		getFakeCommitWithSignature("Katherine Johnson", "kat@example.com", "Initial"),
		getFakeCommitWithSignature("Kathryn Johnson", "kathryn@example.com", "Follow-up"),
	})

	audit := id.IdentityAudit()
	assert.Len(t, audit.Identities, 1)
	assert.Equal(t, "katherine johnson|kathryn johnson|kat@example.com|kathryn@example.com", audit.Identities[0].PeopleDictLine)
	assert.Len(t, audit.MergeDecisions, 1)
	assert.Equal(t, "fuzzy-name", audit.MergeDecisions[0].Reason)
	assert.GreaterOrEqual(t, audit.MergeDecisions[0].Confidence, id.MergeThreshold)
}

func TestPeopleDetectorIdentityThresholdKeepsAmbiguousCandidates(t *testing.T) {
	id := PeopleDetector{MergeThreshold: 0.99}
	id.GeneratePeopleDict([]*object.Commit{
		getFakeCommitWithSignature("Katherine Johnson", "kat@example.com", "Initial"),
		getFakeCommitWithSignature("Kathryn Johnson", "kathryn@example.com", "Follow-up"),
	})

	audit := id.IdentityAudit()
	assert.Len(t, audit.Identities, 2)
	assert.Empty(t, audit.MergeDecisions)
	assert.Len(t, audit.Ambiguous, 1)
	assert.Equal(t, "fuzzy-name", audit.Ambiguous[0].Reason)
	assert.Less(t, audit.Ambiguous[0].Confidence, id.MergeThreshold)
}

func TestPeopleDetectorPeopleDictTemplate(t *testing.T) {
	id := PeopleDetector{}
	id.GeneratePeopleDict([]*object.Commit{
		getFakeCommitWithSignature("Bob Example", "bob@example.com", "Initial"),
		getFakeCommitWithSignature("Alice Example", "alice@example.com", "Initial"),
		getFakeCommitWithSignature("Alice Example", "alice@work.example.com", "Work"),
	})

	assert.Equal(t,
		"bob example|bob@example.com\nalice example|alice@example.com|alice@work.example.com\n",
		id.GeneratePeopleDictTemplate())
}

func TestPeopleDetectorIdentityThresholdConfiguration(t *testing.T) {
	id := PeopleDetector{}
	facts := map[string]any{
		ConfigIdentityDetectorMergeThreshold: 1.5,
		core.ConfigPipelineCommits: []*object.Commit{
			getFakeCommitWithSignature("Alice Example", "alice@example.com", "Initial"),
		},
	}
	assert.Error(t, id.Configure(facts))

	facts[ConfigIdentityDetectorMergeThreshold] = 0.98
	assert.NoError(t, id.Configure(facts))
	assert.InDelta(t, 0.98, id.MergeThreshold, 0.00001)
}

func TestPeopleDetectorLoadPeopleDictInvalidPath(t *testing.T) {
	id := fixturePeopleDetector()
	ipath := "/xxxyyyzzzInvalidPath!hehe"
	err := id.LoadPeopleDict(ipath)
	assert.Error(t, err)
	assert.Equal(t, func() *os.PathError {
		target := &os.PathError{}
		_ = errors.As(err, &target)
		return target
	}().Path, ipath)
}

type fakeBlobEncodedObject struct {
	Contents string
}

func (obj fakeBlobEncodedObject) Hash() plumbing.Hash {
	return plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff")
}

func (obj fakeBlobEncodedObject) Type() plumbing.ObjectType {
	return plumbing.BlobObject
}

func (obj fakeBlobEncodedObject) SetType(plumbing.ObjectType) {}

func (obj fakeBlobEncodedObject) Size() int64 {
	return int64(len(obj.Contents))
}

func (obj fakeBlobEncodedObject) SetSize(int64) {}

func (obj fakeBlobEncodedObject) Reader() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(obj.Contents)), nil
}

func (obj fakeBlobEncodedObject) Writer() (io.WriteCloser, error) {
	return nil, nil
}

type fakeTreeEncodedObject struct {
	Name string
}

func (obj fakeTreeEncodedObject) Hash() plumbing.Hash {
	return plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff")
}

func (obj fakeTreeEncodedObject) Type() plumbing.ObjectType {
	return plumbing.TreeObject
}

func (obj fakeTreeEncodedObject) SetType(plumbing.ObjectType) {}

func (obj fakeTreeEncodedObject) Size() int64 {
	return 1
}

func (obj fakeTreeEncodedObject) SetSize(int64) {}

func (obj fakeTreeEncodedObject) Reader() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(
		"100644 " + obj.Name + "\x00ffffffffffffffffffffffffffffffffffffffff",
	)), nil
}

func (obj fakeTreeEncodedObject) Writer() (io.WriteCloser, error) {
	return nil, nil
}

type fakeEncodedObjectStorer struct {
	Name     string
	Contents string
}

func (strr fakeEncodedObjectStorer) NewEncodedObject() plumbing.EncodedObject {
	return nil
}

func (strr fakeEncodedObjectStorer) HasEncodedObject(plumbing.Hash) error {
	return nil
}

func (strr fakeEncodedObjectStorer) SetEncodedObject(plumbing.EncodedObject) (plumbing.Hash, error) {
	return plumbing.NewHash("0000000000000000000000000000000000000000"), nil
}

func (strr fakeEncodedObjectStorer) EncodedObject(objType plumbing.ObjectType, _ plumbing.Hash) (plumbing.EncodedObject, error) {
	switch objType {
	case plumbing.TreeObject:
		return fakeTreeEncodedObject{Name: strr.Name}, nil
	case plumbing.BlobObject:
		return fakeBlobEncodedObject{Contents: strr.Contents}, nil
	}
	return nil, nil
}

func (strr fakeEncodedObjectStorer) IterEncodedObjects(plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	return nil, nil
}

func (strr fakeEncodedObjectStorer) EncodedObjectSize(plumbing.Hash) (int64, error) {
	return 0, nil
}

func getFakeCommitWithFile(name, contents string) *object.Commit {
	c := object.Commit{
		Hash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
		Author: object.Signature{
			Name:  "Vadim Markovtsev",
			Email: "vadim@sourced.tech",
		},
		Committer: object.Signature{
			Name:  "Vadim Markovtsev",
			Email: "vadim@sourced.tech",
		},
		Message:  "Virtual file " + name,
		TreeHash: plumbing.NewHash("ffffffffffffffffffffffffffffffffffffffff"),
	}
	voc := reflect.ValueOf(&c)
	voc = voc.Elem()
	f := voc.FieldByName("s")
	ptr := unsafe.Pointer(f.UnsafeAddr())
	strr := fakeEncodedObjectStorer{Name: name, Contents: contents}
	*(*storer.EncodedObjectStorer)(ptr) = strr
	return &c
}

func getFakeCommitWithSignature(name, email, message string) *object.Commit {
	c := getFakeCommitWithFile("not-mailmap", "")
	c.Author = object.Signature{Name: name, Email: email}
	c.Committer = object.Signature{Name: name, Email: email}
	c.Message = message
	return c
}

func TestPeopleDetectorGeneratePeopleDictMailmap(t *testing.T) {
	id := fixturePeopleDetector()
	commits := make([]*object.Commit, 0)
	iter, err := test.Repository.CommitObjects()
	assert.NoError(t, err)
	commit, err := iter.Next()
	for ; !errors.Is(err, io.EOF); commit, err = iter.Next() {
		if err != nil {
			panic(err)
		}
		commits = append(commits, commit)
	}
	fake := getFakeCommitWithFile(
		".mailmap",
		"Strange Guy <vadim@sourced.tech>\nVadim Markovtsev <vadim@sourced.tech> Strange Guy <vadim@sourced.tech>",
	)
	commits = append(commits, fake)
	id.GeneratePeopleDict(commits)
	assert.Contains(t, id.ReversedPeopleDict,
		"strange guy|vadim markovtsev|gmarkhor@gmail.com|vadim@athenian.co|vadim@sourced.tech")
}

func TestPeopleDetectorFork(t *testing.T) {
	id1 := fixturePeopleDetector()
	clones := id1.Fork(1)
	assert.Len(t, clones, 1)
	id2 := clones[0].(*PeopleDetector)
	assert.Same(t, id1, id2)
	id1.Merge([]core.PipelineItem{id2})
}
