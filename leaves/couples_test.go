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

func fixtureCouples() *CouplesAnalysis {
	c := CouplesAnalysis{PeopleNumber: 3}
	c.Initialize(test.Repository)
	return &c
}

func TestCouplesMeta(t *testing.T) {
	c := fixtureCouples()
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
	c := fixtureCouples()
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
	deps[plumbing.DependencyTreeChanges] = generateChanges("+LICENSE2", "+file2.go", "+rbtree2.go")
	c.Consume(deps)
	deps[plumbing.DependencyTreeChanges] = generateChanges(
		"+README.md",
		"-LICENSE2",
		"=analyser.go",
		">file2.go>file_test.go",
	)
	c.Consume(deps)
	deps[identity.DependencyAuthor] = 1
	deps[plumbing.DependencyTreeChanges] = generateChanges("=README.md", "=analyser.go", "-rbtree2.go")
	c.Consume(deps)
	deps[identity.DependencyAuthor] = 2
	deps[plumbing.DependencyTreeChanges] = generateChanges("=file_test.go")
	c.Consume(deps)
	assert.Len(t, c.people[0], 6)
	assert.Equal(t, 1, c.people[0]["README.md"])
	assert.Equal(t, 2, c.people[0]["LICENSE2"])
	assert.Equal(t, 1, c.people[0]["analyser.go"])
	assert.Equal(t, 1, c.people[0]["file2.go"])
	assert.Equal(t, 1, c.people[0]["file_test.go"])
	assert.Equal(t, 1, c.people[0]["rbtree2.go"])
	assert.Len(t, c.people[1], 3)
	assert.Equal(t, 1, c.people[1]["README.md"])
	assert.Equal(t, 1, c.people[1]["analyser.go"])
	assert.Equal(t, 1, c.people[1]["rbtree2.go"])
	assert.Len(t, c.people[2], 1)
	assert.Equal(t, 1, c.people[2]["file_test.go"])
	assert.Len(t, c.files["README.md"], 3)
	assert.Equal(t, map[string]int{
		"README.md":    2,
		"analyser.go":  2,
		"file_test.go": 1,
	}, c.files["README.md"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["LICENSE2"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["file2.go"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["rbtree2.go"])
	assert.Equal(t, map[string]int{
		"analyser.go":  2,
		"README.md":    2,
		"file_test.go": 1,
	}, c.files["analyser.go"])
	assert.Equal(t, map[string]int{
		"file_test.go": 2,
		"README.md":    1,
		"analyser.go":  1,
	}, c.files["file_test.go"])
	assert.Equal(t, 2, c.peopleCommits[0])
	assert.Equal(t, 1, c.peopleCommits[1])
	assert.Equal(t, 1, c.peopleCommits[2])
	cr := c.Finalize().(CouplesResult)
	assert.Len(t, cr.Files, 3)
	assert.Equal(t, "README.md", cr.Files[0])
	assert.Equal(t, "analyser.go", cr.Files[1])
	assert.Equal(t, "file_test.go", cr.Files[2])
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
	c := fixtureCouples()
	deps := map[string]any{}
	deps[identity.DependencyAuthor] = 0
	deps[core.DependencyCommit], _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3",
	))
	deps[plumbing.DependencyTreeChanges] = generateChanges("+LICENSE2", "+file2.go", "+rbtree2.go")
	c.Consume(deps)
	deps[plumbing.DependencyTreeChanges] = generateChanges(
		"+README.md",
		"-LICENSE2",
		"=analyser.go",
		">file2.go>file_test.go",
	)
	c.Consume(deps)
	deps[identity.DependencyAuthor] = 1
	deps[plumbing.DependencyTreeChanges] = generateChanges("=README.md", "=analyser.go", "-rbtree2.go")
	c.Consume(deps)
	deps[identity.DependencyAuthor] = core.AuthorMissing
	deps[plumbing.DependencyTreeChanges] = generateChanges("=file_test.go")
	c.Consume(deps)
	assert.Len(t, c.people[0], 6)
	assert.Equal(t, 1, c.people[0]["README.md"])
	assert.Equal(t, 2, c.people[0]["LICENSE2"])
	assert.Equal(t, 1, c.people[0]["analyser.go"])
	assert.Equal(t, 1, c.people[0]["file2.go"])
	assert.Equal(t, 1, c.people[0]["file_test.go"])
	assert.Equal(t, 1, c.people[0]["rbtree2.go"])
	assert.Len(t, c.people[1], 3)
	assert.Equal(t, 1, c.people[1]["README.md"])
	assert.Equal(t, 1, c.people[1]["analyser.go"])
	assert.Equal(t, 1, c.people[1]["rbtree2.go"])
	assert.Empty(t, c.people[2])
	assert.Len(t, c.files["README.md"], 3)
	assert.Equal(t, map[string]int{
		"README.md":    2,
		"analyser.go":  2,
		"file_test.go": 1,
	}, c.files["README.md"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["LICENSE2"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["file2.go"])
	assert.Equal(t, map[string]int{
		"LICENSE2":   1,
		"file2.go":   1,
		"rbtree2.go": 1,
	}, c.files["rbtree2.go"])
	assert.Equal(t, map[string]int{
		"analyser.go":  2,
		"README.md":    2,
		"file_test.go": 1,
	}, c.files["analyser.go"])
	assert.Equal(t, map[string]int{
		"file_test.go": 2,
		"README.md":    1,
		"analyser.go":  1,
	}, c.files["file_test.go"])
	assert.Equal(t, 2, c.peopleCommits[0])
	assert.Equal(t, 1, c.peopleCommits[1])
	assert.Equal(t, 0, c.peopleCommits[2])
	cr := c.Finalize().(CouplesResult)
	assert.Len(t, cr.Files, 3)
	assert.Equal(t, "README.md", cr.Files[0])
	assert.Equal(t, "analyser.go", cr.Files[1])
	assert.Equal(t, "file_test.go", cr.Files[2])
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
	c := fixtureCouples()
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
	couples1 := fixtureCouples()
	clones := couples1.Fork(1)
	assert.Len(t, clones, 1)
	couples2 := clones[0].(*CouplesAnalysis)
	assert.NotSame(t, couples1, couples2)
	assert.Equal(t, *couples1, *couples2)
	couples1.Merge([]core.PipelineItem{couples2})
}

func TestCouplesSerialize(t *testing.T) {
	c := fixtureCouples()
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
		Files:              []string{"five", "one", "three"},
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
	files := [...]string{"five", "one", "three"}
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
}

func TestCouplesMerge(t *testing.T) {
	r1, r2 := CouplesResult{}, CouplesResult{}
	people1 := [...]string{"one", "two"}
	people2 := [...]string{"two", "three"}
	r1.reversedPeopleDict = people1[:]
	r2.reversedPeopleDict = people2[:]
	r1.Files = people1[:]
	r2.Files = people2[:]
	r1.FilesLines = []int{1, 2}
	r2.FilesLines = []int{2, 3}
	r1.PeopleFiles = make([][]int, 2)
	r1.PeopleFiles[0] = make([]int, 2)
	r1.PeopleFiles[0][0] = 0
	r1.PeopleFiles[0][1] = 1
	r1.PeopleFiles[1] = make([]int, 1)
	r1.PeopleFiles[1][0] = 0
	r2.PeopleFiles = make([][]int, 2)
	r2.PeopleFiles[0] = make([]int, 1)
	r2.PeopleFiles[0][0] = 1
	r2.PeopleFiles[1] = make([]int, 2)
	r2.PeopleFiles[1][0] = 0
	r2.PeopleFiles[1][1] = 1
	r1.FilesMatrix = make([]map[int]int64, 2)
	r1.FilesMatrix[0] = map[int]int64{}
	r1.FilesMatrix[1] = map[int]int64{}
	r1.FilesMatrix[0][1] = 100
	r1.FilesMatrix[1][0] = 100
	r2.FilesMatrix = make([]map[int]int64, 2)
	r2.FilesMatrix[0] = map[int]int64{}
	r2.FilesMatrix[1] = map[int]int64{}
	r2.FilesMatrix[0][1] = 200
	r2.FilesMatrix[1][0] = 200
	r1.PeopleMatrix = make([]map[int]int64, 3)
	r1.PeopleMatrix[0] = map[int]int64{}
	r1.PeopleMatrix[1] = map[int]int64{}
	r1.PeopleMatrix[2] = map[int]int64{}
	r1.PeopleMatrix[0][1] = 100
	r1.PeopleMatrix[1][0] = 100
	r1.PeopleMatrix[2][0] = 300
	r1.PeopleMatrix[2][1] = 400
	r2.PeopleMatrix = make([]map[int]int64, 3)
	r2.PeopleMatrix[0] = map[int]int64{}
	r2.PeopleMatrix[1] = map[int]int64{}
	r2.PeopleMatrix[2] = map[int]int64{}
	r2.PeopleMatrix[0][1] = 10
	r2.PeopleMatrix[1][0] = 10
	r2.PeopleMatrix[2][0] = 30
	r2.PeopleMatrix[2][1] = 40
	couples := CouplesAnalysis{}
	merged := couples.MergeResults(r1, r2, nil, nil).(CouplesResult)
	mergedPeople := [...]string{"one", "two", "three"}
	assert.Equal(t, merged.reversedPeopleDict, mergedPeople[:])
	assert.Equal(t, merged.Files, mergedPeople[:])
	assert.Equal(t, []int{1, 4, 3}, merged.FilesLines)
	assert.Len(t, merged.PeopleFiles, 3)
	assert.Equal(t, merged.PeopleFiles[0], getSlice(0, 1))
	assert.Equal(t, merged.PeopleFiles[1], getSlice(0, 2))
	assert.Equal(t, merged.PeopleFiles[2], getSlice(1, 2))
	assert.Len(t, merged.PeopleMatrix, 4)
	assert.Equal(t, merged.PeopleMatrix[0], getCouplesMap(1, 100))
	assert.Equal(t, merged.PeopleMatrix[1], getCouplesMap(0, 100, 2, 10))
	assert.Equal(t, merged.PeopleMatrix[2], getCouplesMap(1, 10))
	assert.Equal(t, merged.PeopleMatrix[3], getCouplesMap(0, 300, 1, 430, 2, 40))
	assert.Len(t, merged.FilesMatrix, 3)
	assert.Equal(t, merged.FilesMatrix[0], getCouplesMap(1, 100))
	assert.Equal(t, merged.FilesMatrix[1], getCouplesMap(0, 100, 2, 200))
	assert.Equal(t, merged.FilesMatrix[2], getCouplesMap(1, 200))
}

func TestCouplesCurrentFiles(t *testing.T) {
	c := fixtureCouples()
	c.lastCommit, _ = test.Repository.CommitObject(gitplumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1",
	))
	files := c.currentFiles()
	assert.Equal(t, map[string]bool{".gitignore": true, "LICENSE": true}, files)
}

func TestCouplesPropagateRenames(t *testing.T) {
	c := fixtureCouples()
	c.files["one"] = map[string]int{
		"one":   1,
		"two":   2,
		"three": 3,
	}
	c.files["two"] = map[string]int{
		"one":   2,
		"two":   10,
		"three": 1,
		"four":  7,
	}
	c.files["three"] = map[string]int{
		"one":   3,
		"two":   1,
		"three": 3,
		"four":  2,
	}
	c.files["four"] = map[string]int{
		"two":   7,
		"three": 3,
		"four":  1,
	}
	c.PeopleNumber = 1
	c.people = make([]map[string]int, 1)
	c.people[0] = map[string]int{}
	c.people[0]["one"] = 1
	c.people[0]["two"] = 2
	c.people[0]["three"] = 3
	c.people[0]["four"] = 4
	*c.renames = []rename{{ToName: "four", FromName: "one"}}
	files, people := c.propagateRenames(map[string]bool{"two": true, "three": true, "four": true})
	assert.Len(t, files, 3)
	assert.Len(t, people, 1)
	assert.Equal(t, map[string]int{"two": 10, "three": 1, "four": 9}, files["two"])
	assert.Equal(t, map[string]int{"two": 1, "three": 3, "four": 6}, files["three"])
	assert.Equal(t, map[string]int{"two": 9, "three": 6, "four": 2}, files["four"])
	assert.Equal(t, map[string]int{"two": 2, "three": 3, "four": 5}, people[0])
}

func getSlice(vals ...int) []int {
	return vals
}

func getCouplesMap(vals ...int) map[int]int64 {
	res := map[int]int64{}
	for i := 0; i < len(vals); i += 2 {
		res[vals[i]] = int64(vals[i+1])
	}
	return res
}
