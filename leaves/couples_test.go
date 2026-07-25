package leaves

import (
	"bytes"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"

	gitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/test"
)

func fixtureCouples(t *testing.T) *CouplesAnalysis {
	t.Helper()
	c := CouplesAnalysis{PeopleNumber: 3}
	assert.NoError(t, c.Initialize(test.Repository))
	return &c
}

func consumeCouples(t *testing.T, c *CouplesAnalysis, deps map[string]any) {
	t.Helper()
	_, err := c.Consume(deps)
	assert.NoError(t, err)
}

func TestCouplesMeta(t *testing.T) {
	c := fixtureCouples(t)
	assert.Equal(t, "Couples", c.Name())
	assert.Empty(t, c.Provides())
	assert.Len(t, c.Requires(), 2)
	assert.Equal(t, identity.DependencyAuthor, c.Requires()[0])
	assert.Equal(t, plumbing.DependencyTreeChanges, c.Requires()[1])
	assert.Equal(t, "couples", c.Flag())
	assert.Empty(t, c.ListConfigurationOptions())
	logger := core.NewLogger()
	assert.NoError(t, c.Configure(map[string]any{
		core.ConfigLogger: logger,
	}))
	assert.Equal(t, logger, c.l)
}

func TestCouplesRegistration(t *testing.T) {
	summoned := core.Registry.Summon((&CouplesAnalysis{}).Name())
	assert.Len(t, summoned, 1)
	assert.Equal(t, "Couples", summoned[0].Name())
	leaves := core.Registry.GetLeaves()
	matched := false
	for _, tp := range leaves {
		if tp.Flag() == (&CouplesAnalysis{}).Flag() {
			matched = true
			break
		}
	}
	assert.True(t, matched)
}

func generateChanges(names ...string) object.Changes {
	changes := make(object.Changes, 0, len(names))
	for _, name := range names {
		action := name[:1]
		name = name[1:]
		var change object.Change
		switch action {
		case "+":
			change = object.Change{
				From: object.ChangeEntry{},
				To:   object.ChangeEntry{Name: name},
			}
		case "-":
			change = object.Change{
				From: object.ChangeEntry{Name: name},
				To:   object.ChangeEntry{},
			}
		case "=":
			change = object.Change{
				From: object.ChangeEntry{Name: name},
				To:   object.ChangeEntry{Name: name},
			}
		default:
			if action != ">" {
				panic("Invalid action.")
			}
			parts := strings.Split(name, ">")
			change = object.Change{
				From: object.ChangeEntry{Name: parts[0]},
				To:   object.ChangeEntry{Name: parts[1]},
			}
		}
		changes = append(changes, &change)
	}
	return changes
}

func TestCouplesConsumeFinalize(t *testing.T) {
	c := fixtureCouples(t)
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
	deps[plumbing.DependencyTreeChanges] = generateChanges("+LICENSE2", "+file2.go", "+rbtree2.go")
	consumeCouples(t, c, deps)
	deps[plumbing.DependencyTreeChanges] = generateChanges(
		"+README.md",
		"-LICENSE2",
		"=analyser.go",
		">file2.go>file_test.go",
	)
	consumeCouples(t, c, deps)
	deps[identity.DependencyAuthor] = 1
	deps[plumbing.DependencyTreeChanges] = generateChanges("=README.md", "=analyser.go", "-rbtree2.go")
	consumeCouples(t, c, deps)
	deps[identity.DependencyAuthor] = 2
	deps[plumbing.DependencyTreeChanges] = generateChanges("=file_test.go")
	consumeCouples(t, c, deps)
	assert.Len(t, c.people[0], 6)
	assert.Equal(t, 1, c.people[0][testReadmePath])
	assert.Equal(t, 2, c.people[0][testLicensePath])
	assert.Equal(t, 1, c.people[0][testAnalyserPath])
	assert.Equal(t, 1, c.people[0][testFileTwoPath])
	assert.Equal(t, 1, c.people[0][testFileTestPath])
	assert.Equal(t, 1, c.people[0][testRBTreeTwoPath])
	assert.Len(t, c.people[1], 3)
	assert.Equal(t, 1, c.people[1][testReadmePath])
	assert.Equal(t, 1, c.people[1][testAnalyserPath])
	assert.Equal(t, 1, c.people[1][testRBTreeTwoPath])
	assert.Len(t, c.people[2], 1)
	assert.Equal(t, 1, c.people[2][testFileTestPath])
	assert.Len(t, c.files[testReadmePath], 3)
	assert.Equal(t, map[string]int{
		testReadmePath:   2,
		testAnalyserPath: 2,
		testFileTestPath: 1,
	}, c.files[testReadmePath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testLicensePath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testFileTwoPath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testRBTreeTwoPath])
	assert.Equal(t, map[string]int{
		testAnalyserPath: 2,
		testReadmePath:   2,
		testFileTestPath: 1,
	}, c.files[testAnalyserPath])
	assert.Equal(t, map[string]int{
		testFileTestPath: 2,
		testReadmePath:   1,
		testAnalyserPath: 1,
	}, c.files[testFileTestPath])
	assert.Equal(t, 2, c.peopleCommits[0])
	assert.Equal(t, 1, c.peopleCommits[1])
	assert.Equal(t, 1, c.peopleCommits[2])
	cr := c.Finalize().(CouplesResult)
	assert.Len(t, cr.Files, 3)
	assert.Equal(t, testReadmePath, cr.Files[0])
	assert.Equal(t, testAnalyserPath, cr.Files[1])
	assert.Equal(t, testFileTestPath, cr.Files[2])
	assert.Len(t, cr.FilesLines, 3)
	assert.Equal(t, 15, cr.FilesLines[0])
	assert.Equal(t, 252, cr.FilesLines[1])
	assert.Equal(t, 238, cr.FilesLines[2])
	assert.Len(t, cr.PeopleFiles[0], 3)
	assert.Equal(t, 0, cr.PeopleFiles[0][0])
	assert.Equal(t, 1, cr.PeopleFiles[0][1])
	assert.Equal(t, 2, cr.PeopleFiles[0][2])
	assert.Len(t, cr.PeopleFiles[1], 2)
	assert.Equal(t, 0, cr.PeopleFiles[1][0])
	assert.Equal(t, 1, cr.PeopleFiles[1][1])
	assert.Len(t, cr.PeopleFiles[2], 1)
	assert.Equal(t, 2, cr.PeopleFiles[2][0])
	assert.Len(t, cr.PeopleMatrix[0], 3)
	assert.Equal(t, int64(7), cr.PeopleMatrix[0][0])
	assert.Equal(t, int64(3), cr.PeopleMatrix[0][1])
	assert.Equal(t, int64(1), cr.PeopleMatrix[0][2])
	assert.Len(t, cr.PeopleMatrix[1], 2)
	assert.Equal(t, int64(3), cr.PeopleMatrix[1][0])
	assert.Equal(t, int64(3), cr.PeopleMatrix[1][1])
	assert.Len(t, cr.PeopleMatrix[2], 2)
	assert.Equal(t, int64(1), cr.PeopleMatrix[2][0])
	assert.Equal(t, int64(1), cr.PeopleMatrix[2][2])
	assert.Len(t, cr.FilesMatrix, 3)
	assert.Len(t, cr.FilesMatrix[0], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[0][2])
	assert.Equal(t, int64(2), cr.FilesMatrix[0][0])
	assert.Equal(t, int64(2), cr.FilesMatrix[0][1])
	assert.Len(t, cr.FilesMatrix[1], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[1][2])
	assert.Equal(t, int64(2), cr.FilesMatrix[1][0])
	assert.Equal(t, int64(2), cr.FilesMatrix[1][1])
	assert.Len(t, cr.FilesMatrix[2], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[2][0])
	assert.Equal(t, int64(1), cr.FilesMatrix[2][1])
	assert.Equal(t, int64(3), cr.FilesMatrix[2][2])
}

func TestCouplesConsumeFinalizeAuthorMissing(t *testing.T) {
	c := fixtureCouples(t)
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
	deps[plumbing.DependencyTreeChanges] = generateChanges("+LICENSE2", "+file2.go", "+rbtree2.go")
	consumeCouples(t, c, deps)
	deps[plumbing.DependencyTreeChanges] = generateChanges(
		"+README.md",
		"-LICENSE2",
		"=analyser.go",
		">file2.go>file_test.go",
	)
	consumeCouples(t, c, deps)
	deps[identity.DependencyAuthor] = 1
	deps[plumbing.DependencyTreeChanges] = generateChanges("=README.md", "=analyser.go", "-rbtree2.go")
	consumeCouples(t, c, deps)
	deps[identity.DependencyAuthor] = core.AuthorMissing
	deps[plumbing.DependencyTreeChanges] = generateChanges("=file_test.go")
	consumeCouples(t, c, deps)
	assert.Len(t, c.people[0], 6)
	assert.Equal(t, 1, c.people[0][testReadmePath])
	assert.Equal(t, 2, c.people[0][testLicensePath])
	assert.Equal(t, 1, c.people[0][testAnalyserPath])
	assert.Equal(t, 1, c.people[0][testFileTwoPath])
	assert.Equal(t, 1, c.people[0][testFileTestPath])
	assert.Equal(t, 1, c.people[0][testRBTreeTwoPath])
	assert.Len(t, c.people[1], 3)
	assert.Equal(t, 1, c.people[1][testReadmePath])
	assert.Equal(t, 1, c.people[1][testAnalyserPath])
	assert.Equal(t, 1, c.people[1][testRBTreeTwoPath])
	assert.Empty(t, c.people[2])
	assert.Len(t, c.files[testReadmePath], 3)
	assert.Equal(t, map[string]int{
		testReadmePath:   2,
		testAnalyserPath: 2,
		testFileTestPath: 1,
	}, c.files[testReadmePath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testLicensePath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testFileTwoPath])
	assert.Equal(t, map[string]int{
		testLicensePath:   1,
		testFileTwoPath:   1,
		testRBTreeTwoPath: 1,
	}, c.files[testRBTreeTwoPath])
	assert.Equal(t, map[string]int{
		testAnalyserPath: 2,
		testReadmePath:   2,
		testFileTestPath: 1,
	}, c.files[testAnalyserPath])
	assert.Equal(t, map[string]int{
		testFileTestPath: 2,
		testReadmePath:   1,
		testAnalyserPath: 1,
	}, c.files[testFileTestPath])
	assert.Equal(t, 2, c.peopleCommits[0])
	assert.Equal(t, 1, c.peopleCommits[1])
	assert.Equal(t, 0, c.peopleCommits[2])
	cr := c.Finalize().(CouplesResult)
	assert.Len(t, cr.Files, 3)
	assert.Equal(t, testReadmePath, cr.Files[0])
	assert.Equal(t, testAnalyserPath, cr.Files[1])
	assert.Equal(t, testFileTestPath, cr.Files[2])
	assert.Len(t, cr.FilesLines, 3)
	assert.Equal(t, 15, cr.FilesLines[0])
	assert.Equal(t, 252, cr.FilesLines[1])
	assert.Equal(t, 238, cr.FilesLines[2])
	assert.Len(t, cr.PeopleFiles[0], 3)
	assert.Equal(t, 0, cr.PeopleFiles[0][0])
	assert.Equal(t, 1, cr.PeopleFiles[0][1])
	assert.Equal(t, 2, cr.PeopleFiles[0][2])
	assert.Len(t, cr.PeopleFiles[1], 2)
	assert.Equal(t, 0, cr.PeopleFiles[1][0])
	assert.Equal(t, 1, cr.PeopleFiles[1][1])
	assert.Empty(t, cr.PeopleFiles[2])
	assert.Len(t, cr.PeopleMatrix[0], 3)
	assert.Equal(t, int64(7), cr.PeopleMatrix[0][0])
	assert.Equal(t, int64(3), cr.PeopleMatrix[0][1])
	assert.Equal(t, int64(0), cr.PeopleMatrix[0][2])
	assert.Len(t, cr.PeopleMatrix[1], 2)
	assert.Equal(t, int64(3), cr.PeopleMatrix[1][0])
	assert.Equal(t, int64(3), cr.PeopleMatrix[1][1])
	assert.Empty(t, cr.PeopleMatrix[2])
	assert.Len(t, cr.FilesMatrix, 3)
	assert.Len(t, cr.FilesMatrix[0], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[0][2])
	assert.Equal(t, int64(2), cr.FilesMatrix[0][0])
	assert.Equal(t, int64(2), cr.FilesMatrix[0][1])
	assert.Len(t, cr.FilesMatrix[1], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[1][2])
	assert.Equal(t, int64(2), cr.FilesMatrix[1][0])
	assert.Equal(t, int64(2), cr.FilesMatrix[1][1])
	assert.Len(t, cr.FilesMatrix[2], 3)
	assert.Equal(t, int64(1), cr.FilesMatrix[2][0])
	assert.Equal(t, int64(1), cr.FilesMatrix[2][1])
	assert.Equal(t, int64(3), cr.FilesMatrix[2][2])
}

func TestCouplesConsumeManyFiles(t *testing.T) {
	c := fixtureCouples(t)
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
	changes := make(object.Changes, CouplesMaximumMeaningfulContextSize+1)
	for i := range changes {
		changes[i] = &object.Change{
			From: object.ChangeEntry{},
			To:   object.ChangeEntry{Name: strconv.Itoa(i)},
		}
	}
	deps[plumbing.DependencyTreeChanges] = changes
	_, err := c.Consume(deps)
	assert.NoError(t, err)
	assert.Len(t, changes, len(c.people[0]))
	assert.Empty(t, c.files)
}

func TestCouplesFork(t *testing.T) {
	couples1 := fixtureCouples(t)
	clones := couples1.Fork(1)
	assert.Len(t, clones, 1)
	couples2 := clones[0].(*CouplesAnalysis)
	assert.NotSame(t, couples1, couples2)
	assert.Equal(t, *couples1, *couples2)
	couples1.Merge([]core.PipelineItem{couples2})
}

func TestCouplesSerialize(t *testing.T) {
	c := fixtureCouples(t)
	result := CouplesResult{
		PeopleMatrix: []map[int]int64{
			{0: 7, 1: 3, 2: 1}, {0: 3, 1: 3}, {0: 1, 2: 1}, {},
		},
		PeopleFiles: [][]int{
			{0, 1, 2}, {1, 2}, {0}, {},
		},
		FilesMatrix: []map[int]int64{
			{1: 1, 2: 1, 0: 3}, {1: 2, 2: 2, 0: 1}, {2: 2, 0: 1, 1: 2},
		},
		Files:              []string{"five", testPersonOne, testPersonThree},
		FilesLines:         []int{9, 8, 7},
		reversedPeopleDict: []string{"p1", "p2", "p3"},
	}
	buffer := &bytes.Buffer{}
	assert.NoError(t, c.Serialize(result, false, buffer))
	assert.Equal(t, `  files_coocc:
    index:
      - "five"
      - "one"
      - "three"
    lines:
      - 9
      - 8
      - 7
    matrix:
      - {0: 3, 1: 1, 2: 1}
      - {0: 1, 1: 2, 2: 2}
      - {0: 1, 1: 2, 2: 2}
  people_coocc:
    index:
      - "p1"
      - "p2"
      - "p3"
    matrix:
      - {0: 7, 1: 3, 2: 1}
      - {0: 3, 1: 3}
      - {0: 1, 2: 1}
      - {}
    author_files:
      - "p3":
        - "five"
      - "p2":
        - "one"
        - "three"
      - "p1":
        - "five"
        - "one"
        - "three"
`, buffer.String())
	buffer = &bytes.Buffer{}
	assert.NoError(t, c.Serialize(result, true, buffer))
	msg := pb.CouplesAnalysisResults{}
	assert.NoError(t, proto.Unmarshal(buffer.Bytes(), &msg))
	assert.Equal(t, []int32{9, 8, 7}, msg.GetFilesLines())
	assert.Len(t, msg.GetPeopleFiles(), 3)
	tmp1 := [...]int32{0, 1, 2}
	assert.Equal(t, msg.GetPeopleFiles()[0].GetFiles(), tmp1[:])
	tmp2 := [...]int32{1, 2}
	assert.Equal(t, msg.GetPeopleFiles()[1].GetFiles(), tmp2[:])
	tmp3 := [...]int32{0}
	assert.Equal(t, msg.GetPeopleFiles()[2].GetFiles(), tmp3[:])
	assert.Equal(t, msg.GetPeopleCouples().GetIndex(), result.reversedPeopleDict)
	assert.Equal(t, int32(4), msg.GetPeopleCouples().GetMatrix().GetNumberOfRows())
	assert.Equal(t, int32(4), msg.GetPeopleCouples().GetMatrix().GetNumberOfColumns())
	data := [...]int64{7, 3, 1, 3, 3, 1, 1}
	assert.Equal(t, msg.GetPeopleCouples().GetMatrix().GetData(), data[:])
	indices := [...]int32{0, 1, 2, 0, 1, 0, 2}
	assert.Equal(t, msg.GetPeopleCouples().GetMatrix().GetIndices(), indices[:])
	indptr := [...]int64{0, 3, 5, 7, 7}
	assert.Equal(t, msg.GetPeopleCouples().GetMatrix().GetIndptr(), indptr[:])
	files := [...]string{"five", testPersonOne, testPersonThree}
	assert.Equal(t, msg.GetFileCouples().GetIndex(), files[:])
	assert.Equal(t, int32(3), msg.GetFileCouples().GetMatrix().GetNumberOfRows())
	assert.Equal(t, int32(3), msg.GetFileCouples().GetMatrix().GetNumberOfColumns())
	data2 := [...]int64{3, 1, 1, 1, 2, 2, 1, 2, 2}
	assert.Equal(t, msg.GetFileCouples().GetMatrix().GetData(), data2[:])
	indices2 := [...]int32{0, 1, 2, 0, 1, 2, 0, 1, 2}
	assert.Equal(t, msg.GetFileCouples().GetMatrix().GetIndices(), indices2[:])
	indptr2 := [...]int64{0, 3, 6, 9}
	assert.Equal(t, msg.GetFileCouples().GetMatrix().GetIndptr(), indptr2[:])
}

func TestCouplesDeserialize(t *testing.T) {
	message, err := os.ReadFile(path.Join("..", "internal", "test_data", "couples.pb"))
	assert.NoError(t, err)
	couples := CouplesAnalysis{}
	iresult, err := couples.Deserialize(message)
	assert.NoError(t, err)
	result := iresult.(CouplesResult)
	assert.Len(t, result.reversedPeopleDict, 2)
	assert.Len(t, result.PeopleFiles, 2)
	assert.Len(t, result.PeopleMatrix, 3)
	assert.Len(t, result.Files, 74)
	assert.Len(t, result.FilesLines, 74)
	for _, v := range result.FilesLines {
		assert.Positive(t, v)
	}
	assert.Len(t, result.FilesMatrix, 74)

	merged, ok := couples.MergeResults(result, CouplesResult{}, nil, nil).(CouplesResult)
	assert.True(t, ok)
	assert.Equal(t, result, merged)
}

func TestCouplesMergeAcceptsEmptyAndDeserializedEmptyResults(t *testing.T) {
	couples := &CouplesAnalysis{}
	decoded, err := couples.Deserialize(nil)
	assert.NoError(t, err)
	decodedResult, ok := decoded.(CouplesResult)
	assert.True(t, ok)
	assert.True(t, isEmptyCouplesResult(decodedResult))

	r1, _, _ := couplesMergeFixtures()
	for name, empty := range map[string]any{
		"zero value":         CouplesResult{},
		"deserialized empty": decoded,
	} {
		t.Run(name, func(t *testing.T) {
			merged, ok := couples.MergeResults(empty, r1, nil, nil).(CouplesResult)
			assert.True(t, ok)
			assert.Equal(t, r1, merged)

			merged, ok = couples.MergeResults(r1, empty, nil, nil).(CouplesResult)
			assert.True(t, ok)
			assert.Equal(t, r1, merged)
		})
	}
}

func TestCouplesInitializeResetsLastCommit(t *testing.T) {
	couples := fixtureCouples(t)
	couples.lastCommit = &object.Commit{}

	assert.NoError(t, couples.Initialize(test.Repository))
	assert.Nil(t, couples.lastCommit)

	result, ok := couples.Finalize().(CouplesResult)
	assert.True(t, ok)
	assert.Empty(t, result.Files)
	assert.Len(t, result.PeopleMatrix, couples.PeopleNumber+1)
}

const (
	couplesAliceIdentity = "alice|alice@example.com"
	couplesBobIdentity   = "bob|bob@example.com"
	couplesCarolIdentity = "carol|carol@example.com"
	couplesDaveIdentity  = "dave|dave@example.com"
	couplesSharedPath    = "shared.go"
	couplesOmegaPath     = "omega.go"
	couplesZetaPath      = "zeta.go"
)

func TestCouplesMergeHandComputed(t *testing.T) {
	r1, r2, _ := couplesMergeFixtures()

	merged, ok := (&CouplesAnalysis{}).MergeResults(r1, r2, nil, nil).(CouplesResult)
	assert.True(t, ok)
	assert.Equal(t, CouplesResult{
		reversedPeopleDict: []string{
			couplesAliceIdentity,
			couplesBobIdentity,
			couplesCarolIdentity,
		},
		Files:      []string{testAlphaPath, couplesSharedPath, couplesOmegaPath},
		FilesLines: []int{10, 25, 30},
		PeopleFiles: [][]int{
			{0, 1},
			{1, 2},
			{2},
		},
		PeopleMatrix: []map[int]int64{
			{0: 10, 1: 2, 3: 1},
			{0: 2, 1: 25, 2: 6, 3: 10},
			{1: 6, 2: 8, 3: 9},
			{0: 1, 1: 10, 2: 9, 3: 14},
		},
		FilesMatrix: []map[int]int64{
			{0: 2, 1: 3},
			{0: 3, 1: 9, 2: 6},
			{1: 6, 2: 7},
		},
	}, merged)
}

func TestCouplesMergeAssociative(t *testing.T) {
	r1, r2, r3 := couplesMergeFixtures()
	couples := &CouplesAnalysis{}

	r1r2, ok := couples.MergeResults(r1, r2, nil, nil).(CouplesResult)
	assert.True(t, ok)
	left, ok := couples.MergeResults(r1r2, r3, nil, nil).(CouplesResult)
	assert.True(t, ok)

	r2r3, ok := couples.MergeResults(r2, r3, nil, nil).(CouplesResult)
	assert.True(t, ok)
	right, ok := couples.MergeResults(r1, r2r3, nil, nil).(CouplesResult)
	assert.True(t, ok)

	assert.Equal(t, left, right)
}

func TestCouplesMergeRejectsInvalidSourceIndices(t *testing.T) {
	tests := map[string]func(*CouplesResult){
		"file lines row": func(result *CouplesResult) {
			result.FilesLines = result.FilesLines[:1]
		},
		"file matrix row": func(result *CouplesResult) {
			result.FilesMatrix = result.FilesMatrix[:1]
		},
		"file matrix column": func(result *CouplesResult) {
			result.FilesMatrix[0][len(result.Files)] = 1
		},
		"people files row": func(result *CouplesResult) {
			result.PeopleFiles = result.PeopleFiles[:1]
		},
		"people files file": func(result *CouplesResult) {
			result.PeopleFiles[0] = []int{len(result.Files)}
		},
		"people matrix row": func(result *CouplesResult) {
			result.PeopleMatrix = result.PeopleMatrix[:1]
		},
		"people matrix column": func(result *CouplesResult) {
			result.PeopleMatrix[0][-1] = 1
		},
	}

	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			r1, r2, _ := couplesMergeFixtures()
			corrupt(&r1)

			merged := (&CouplesAnalysis{}).MergeResults(r1, r2, nil, nil)
			err, ok := merged.(error)
			assert.True(t, ok)
			assert.ErrorIs(t, err, errCouplesMergeIntegrity)
		})
	}
}

func TestAddMergedFilesMatrixRejectsInvalidDestination(t *testing.T) {
	r1, _, _ := couplesMergeFixtures()
	files := []int{0, 2}

	err := addMergedFilesMatrix(make([]map[int]int64, 2), r1, files)
	assert.ErrorIs(t, err, errCouplesMergeIntegrity)
}

func couplesMergeFixtures() (CouplesResult, CouplesResult, CouplesResult) {
	r1 := CouplesResult{
		reversedPeopleDict: []string{
			couplesAliceIdentity,
			couplesBobIdentity,
		},
		Files:      []string{testAlphaPath, couplesSharedPath},
		FilesLines: []int{10, 20},
		PeopleFiles: [][]int{
			{0, 1},
			{1},
		},
		PeopleMatrix: []map[int]int64{
			{0: 10, 1: 2, 2: 1},
			{0: 2, 1: 20, 2: 3},
			{0: 1, 1: 3, 2: 4},
		},
		FilesMatrix: []map[int]int64{
			{0: 2, 1: 3},
			{0: 3, 1: 4},
		},
	}
	r2 := CouplesResult{
		reversedPeopleDict: []string{
			couplesBobIdentity,
			couplesCarolIdentity,
		},
		Files:      []string{couplesSharedPath, couplesOmegaPath},
		FilesLines: []int{5, 30},
		PeopleFiles: [][]int{
			{0, 1},
			{1},
		},
		PeopleMatrix: []map[int]int64{
			{0: 5, 1: 6, 2: 7},
			{0: 6, 1: 8, 2: 9},
			{0: 7, 1: 9, 2: 10},
		},
		FilesMatrix: []map[int]int64{
			{0: 5, 1: 6},
			{0: 6, 1: 7},
		},
	}
	r3 := CouplesResult{
		reversedPeopleDict: []string{
			couplesCarolIdentity,
			couplesDaveIdentity,
		},
		Files:      []string{couplesOmegaPath, couplesZetaPath},
		FilesLines: []int{11, 40},
		PeopleFiles: [][]int{
			{0, 1},
			{1},
		},
		PeopleMatrix: []map[int]int64{
			{0: 11, 1: 12, 2: 13},
			{0: 12, 1: 14, 2: 15},
			{0: 13, 1: 15, 2: 16},
		},
		FilesMatrix: []map[int]int64{
			{0: 8, 1: 9},
			{0: 9, 1: 10},
		},
	}

	return r1, r2, r3
}

func TestCouplesCurrentFiles(t *testing.T) {
	c := fixtureCouples(t)
	c.lastCommit, _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	files := c.currentFiles()
	assert.Equal(t, map[string]bool{".gitignore": true, "LICENSE": true}, files)
}

func TestCouplesPropagateRenames(t *testing.T) {
	c := fixtureCouples(t)
	c.files[testPersonOne] = map[string]int{
		testPersonOne:   1,
		testPersonTwo:   2,
		testPersonThree: 3,
	}
	c.files[testPersonTwo] = map[string]int{
		testPersonOne:   2,
		testPersonTwo:   10,
		testPersonThree: 1,
		testPersonFour:  7,
	}
	c.files[testPersonThree] = map[string]int{
		testPersonOne:   3,
		testPersonTwo:   1,
		testPersonThree: 3,
		testPersonFour:  2,
	}
	c.files[testPersonFour] = map[string]int{
		testPersonTwo:   7,
		testPersonThree: 3,
		testPersonFour:  1,
	}
	c.PeopleNumber = 1
	c.people = make([]map[string]int, 1)
	c.people[0] = map[string]int{}
	c.people[0][testPersonOne] = 1
	c.people[0][testPersonTwo] = 2
	c.people[0][testPersonThree] = 3
	c.people[0][testPersonFour] = 4
	*c.renames = []rename{{ToName: testPersonFour, FromName: testPersonOne}}
	files, people := c.propagateRenames(map[string]bool{testPersonTwo: true, testPersonThree: true, testPersonFour: true})
	assert.Len(t, files, 3)
	assert.Len(t, people, 1)
	assert.Equal(t, map[string]int{testPersonTwo: 10, testPersonThree: 1, testPersonFour: 9}, files[testPersonTwo])
	assert.Equal(t, map[string]int{testPersonTwo: 1, testPersonThree: 3, testPersonFour: 6}, files[testPersonThree])
	assert.Equal(t, map[string]int{testPersonTwo: 9, testPersonThree: 6, testPersonFour: 2}, files[testPersonFour])
	assert.Equal(t, map[string]int{testPersonTwo: 2, testPersonThree: 3, testPersonFour: 5}, people[0])
}
