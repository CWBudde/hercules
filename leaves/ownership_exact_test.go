package leaves

import (
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

const (
	divergentFilePath  = "file.txt"
	divergentOtherPath = "other.txt"
	divergentTenLines  = "l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"
	divergentFiveLines = "l5\nl6\nl7\nl8\nl9\n"
)

// newDivergentRemovalFixture builds the history behind PLAN.md B1c/B3 in miniature: two branches
// fork from base, each deletes the same five lines of file.txt, and a merge joins them. The
// right branch also appends one line to other.txt so the merge has ownership to resolve. With
// extraCommit a further commit lands on the merge in a later tick.
//
// Text lines at HEAD: 5 (file.txt, Alice) + 3 (other.txt, Alice) + 1 (other.txt, Carol)
// [+ 1 (other.txt, Bob) with extraCommit].
func newDivergentRemovalFixture(t *testing.T, extraCommit bool) (*git.Repository, []*object.Commit) {
	t.Helper()

	storage := memory.NewStorage()
	fs := memfs.New()
	repository, err := git.Init(storage, fs)
	require.NoError(t, err)
	worktree, err := repository.Worktree()
	require.NoError(t, err)

	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	alice := object.Signature{Name: "Alice", Email: "alice@example.com", When: start}
	bob := object.Signature{Name: "Bob", Email: "bob@example.com", When: start.Add(time.Hour)}
	carol := object.Signature{Name: "Carol", Email: "carol@example.com", When: start.Add(2 * time.Hour)}

	writeFixtureFile(t, fs, divergentFilePath, divergentTenLines)
	writeFixtureFile(t, fs, divergentOtherPath, "o0\no1\no2\n")
	addFixturePaths(t, worktree, divergentFilePath, divergentOtherPath)
	base := commitHotspotFixture(t, repository, worktree, "base", alice)

	writeFixtureFile(t, fs, divergentFilePath, divergentFiveLines)
	addFixturePaths(t, worktree, divergentFilePath)
	left := commitHotspotFixture(t, repository, worktree, "left removes the head of file.txt", bob)

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: base.Hash, Force: true}))
	writeFixtureFile(t, fs, divergentFilePath, divergentFiveLines)
	writeFixtureFile(t, fs, divergentOtherPath, "o0\no1\no2\no3\n")
	addFixturePaths(t, worktree, divergentFilePath, divergentOtherPath)
	right := commitHotspotFixture(t, repository, worktree, "right removes the same head", carol)

	// The worktree holds right's tree, which is also the merged tree.
	alice.When = start.Add(3 * time.Hour)
	mergeHash, err := worktree.Commit("merge left into right", &git.CommitOptions{
		Author: &alice, Committer: &alice,
		Parents:           []plumbing.Hash{left.Hash, right.Hash},
		AllowEmptyCommits: true,
	})
	require.NoError(t, err)
	merge, err := repository.CommitObject(mergeHash)
	require.NoError(t, err)

	commits := []*object.Commit{base, left, right, merge}

	if extraCommit {
		bob.When = start.Add(27 * time.Hour)
		writeFixtureFile(t, fs, divergentOtherPath, "o0\no1\no2\no3\no4\n")
		addFixturePaths(t, worktree, divergentOtherPath)
		commits = append(commits, commitHotspotFixture(t, repository, worktree, "after the merge", bob))
	}

	return repository, commits
}

// ownershipAuthorIndex finds the identity index whose dictionary entry carries the e-mail.
func ownershipAuthorIndex(t *testing.T, people []string, email string) int {
	t.Helper()

	for index, entry := range people {
		if strings.Contains(entry, email) {
			return index
		}
	}

	require.Failf(t, "unknown author", "%s not in %v", email, people)

	return -1
}

func sumOwnershipLines(authorLines map[int]int64) int64 {
	var total int64
	for _, lines := range authorLines {
		total += lines
	}

	return total
}

// TestOwnershipAnalysesAreExactAcrossDivergentBranches pins the fix for the bus factor and
// ownership concentration inheriting PLAN.md B1c: two branches removing the same lines used to be
// summed into one accumulator, so those lines vanished twice and the final distribution matched
// neither git blame nor the analysis' own total. Ownership is derived from the line-history trees
// now, so the final snapshot has to equal the text lines at HEAD, author by author, and agree with
// burndown's tree-derived per-file ownership.
func TestOwnershipAnalysesAreExactAcrossDivergentBranches(t *testing.T) {
	cases := []struct {
		name        string
		extraCommit bool
		hibernation int
	}{
		{name: "head is the merge", extraCommit: false, hibernation: 0},
		{name: "head follows the merge", extraCommit: true, hibernation: 0},
		{name: "hibernating branches", extraCommit: true, hibernation: 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, commits := newDivergentRemovalFixture(t, testCase.extraCommit)
			pipeline := core.NewPipeline(repository)
			pipeline.SetFeature(core.FeatureGitCommits)
			busFactor := pipeline.DeployItem(&BusFactorAnalysis{}).(*BusFactorAnalysis)
			concentration := pipeline.DeployItem(
				&OwnershipConcentrationAnalysis{},
			).(*OwnershipConcentrationAnalysis)
			burndown := pipeline.DeployItem(&BurndownAnalysis{}).(*BurndownAnalysis)

			require.NoError(t, pipeline.Initialize(map[string]any{
				core.ConfigPipelineCommits:             commits,
				core.ConfigPipelineHibernationDistance: testCase.hibernation,
				items.ConfigTicksSinceStartTickSize:    24,
				ConfigBurndownTrackFiles:               true,
				ConfigBurndownTrackPeople:              true,
			}))

			results, err := pipeline.Run(commits)
			require.NoError(t, err)

			busResult, ok := results[busFactor].(BusFactorResult)
			require.True(t, ok)
			concentrationResult, ok := results[concentration].(OwnershipConcentrationResult)
			require.True(t, ok)
			burndownResult, ok := results[burndown].(BurndownResult)
			require.True(t, ok)

			expectedTotal := int64(9)
			expectedAuthors := map[string]int64{"alice@example.com": 8, "carol@example.com": 1}

			if testCase.extraCommit {
				expectedTotal = 10
				expectedAuthors["bob@example.com"] = 1
			}

			lastTick := -1
			for tick := range busResult.Snapshots {
				lastTick = max(lastTick, tick)
			}

			require.GreaterOrEqual(t, lastTick, 0)
			final := busResult.Snapshots[lastTick]

			assert.Equal(t, expectedTotal, final.TotalLines, "bus factor total must match HEAD")
			assert.Equal(t, final.TotalLines, sumOwnershipLines(final.AuthorLines),
				"the total must be the sum of the per-author counts")

			for email, lines := range expectedAuthors {
				author := ownershipAuthorIndex(t, busResult.reversedPeopleDict, email)
				assert.Equal(t, lines, final.AuthorLines[author], email)
			}

			assert.Len(t, final.AuthorLines, len(expectedAuthors))
			assert.Equal(t, 1, busResult.SubsystemBusFactor["/"])

			finalConcentration := concentrationResult.Snapshots[lastTick]
			require.NotNil(t, finalConcentration)
			assert.Equal(t, expectedTotal, finalConcentration.TotalLines)
			assert.Equal(t, final.AuthorLines, finalConcentration.AuthorLines)

			// Burndown derives FileOwnership from the same trees, so the two must agree.
			var fileOwnershipTotal int64

			perAuthor := map[int]int64{}
			for _, owned := range burndownResult.FileOwnership {
				for author, lines := range owned {
					if author < 0 {
						continue
					}

					perAuthor[author] += int64(lines)
					fileOwnershipTotal += int64(lines)
				}
			}

			assert.Equal(t, expectedTotal, fileOwnershipTotal)
			alice := ownershipAuthorIndex(t, busResult.reversedPeopleDict, "alice@example.com")
			assert.Equal(t, 5, burndownResult.FileOwnership[divergentFilePath][alice])
			assert.Equal(t, final.AuthorLines, perAuthor)
		})
	}
}
