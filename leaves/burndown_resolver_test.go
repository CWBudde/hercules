package leaves

import (
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
	"github.com/cwbudde/hercules/internal/linehistory"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

// newForkedMainlineFixture builds the merge shape that disqualifies the root branch from
// consuming a merge: A -> {B, C}; M = merge(B, C); D on B; E = merge(M, D). At M the branch
// holding B has two children (M and D), so the planner consumes M on C's branch and the root
// branch's line-history instance is synchronized and then frozen (PLAN.md B14).
//
// Text lines at HEAD (E): file.txt 5 (Alice), other.txt 3 (Alice) + 1 (Carol), third.txt 2 (Bob).
func newForkedMainlineFixture(t *testing.T) (*git.Repository, []*object.Commit) {
	t.Helper()

	storage := memory.NewStorage()
	fs := memfs.New()
	repository, err := git.Init(storage, fs)
	require.NoError(t, err)
	worktree, err := repository.Worktree()
	require.NoError(t, err)

	start := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	alice := object.Signature{Name: "Alice", Email: "alice@example.com", When: start}
	bob := object.Signature{Name: "Bob", Email: "bob@example.com", When: start.Add(time.Hour)}
	carol := object.Signature{Name: "Carol", Email: "carol@example.com", When: start.Add(2 * time.Hour)}

	writeFixtureFile(t, fs, divergentFilePath, divergentTenLines)
	writeFixtureFile(t, fs, divergentOtherPath, "o0\no1\no2\n")
	addFixturePaths(t, worktree, divergentFilePath, divergentOtherPath)
	base := commitHotspotFixture(t, repository, worktree, "base", alice)

	writeFixtureFile(t, fs, divergentFilePath, divergentFiveLines)
	addFixturePaths(t, worktree, divergentFilePath)
	left := commitHotspotFixture(t, repository, worktree, "left shortens file.txt", bob)

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: base.Hash, Force: true}))
	writeFixtureFile(t, fs, divergentOtherPath, "o0\no1\no2\no3\n")
	addFixturePaths(t, worktree, divergentOtherPath)
	right := commitHotspotFixture(t, repository, worktree, "right extends other.txt", carol)

	writeFixtureFile(t, fs, divergentFilePath, divergentFiveLines)
	addFixturePaths(t, worktree, divergentFilePath)
	alice.When = start.Add(3 * time.Hour)
	merge := commitFixtureMerge(t, repository, worktree, "merge left into right", alice, left.Hash, right.Hash)

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: left.Hash, Force: true}))
	writeFixtureFile(t, fs, "third.txt", "t0\nt1\n")
	addFixturePaths(t, worktree, "third.txt")
	bob.When = start.Add(4 * time.Hour)
	after := commitHotspotFixture(t, repository, worktree, "left continues", bob)

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: merge.Hash, Force: true}))
	writeFixtureFile(t, fs, "third.txt", "t0\nt1\n")
	addFixturePaths(t, worktree, "third.txt")
	alice.When = start.Add(5 * time.Hour)
	head := commitFixtureMerge(t, repository, worktree, "merge left again", alice, merge.Hash, after.Hash)

	return repository, []*object.Commit{base, left, right, merge, after, head}
}

func commitFixtureMerge(
	t *testing.T,
	repository *git.Repository,
	worktree *git.Worktree,
	message string,
	signature object.Signature,
	parents ...plumbing.Hash,
) *object.Commit {
	t.Helper()

	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &signature, Committer: &signature,
		Parents:           parents,
		AllowEmptyCommits: true,
	})
	require.NoError(t, err)
	commit, err := repository.CommitObject(hash)
	require.NoError(t, err)

	return commit
}

// TestBurndownFileOutputsFollowTheAuthoritativeBranch pins PLAN.md B14: burndown resolved file
// names and per-file ownership through the pipeline's original line-history instance, which stops
// tracking HEAD once the planner consumes a merge on another branch. The outputs then described a
// frozen branch - on a real repository 305 file histories of which 29 existed at HEAD.
func TestBurndownFileOutputsFollowTheAuthoritativeBranch(t *testing.T) {
	repository, commits := newForkedMainlineFixture(t)
	pipeline := core.NewPipeline(repository)
	pipeline.SetFeature(core.FeatureGitCommits)
	burndown := pipeline.DeployItem(&BurndownAnalysis{}).(*BurndownAnalysis)
	busFactor := pipeline.DeployItem(&BusFactorAnalysis{}).(*BusFactorAnalysis)

	require.NoError(t, pipeline.Initialize(map[string]any{
		core.ConfigPipelineCommits:          commits,
		items.ConfigTicksSinceStartTickSize: 24,
		ConfigBurndownTrackFiles:            true,
		ConfigBurndownTrackPeople:           true,
	}))

	results, err := pipeline.Run(commits)
	require.NoError(t, err)

	burndownResult, ok := results[burndown].(BurndownResult)
	require.True(t, ok)
	busResult, ok := results[busFactor].(BusFactorResult)
	require.True(t, ok)

	assert.ElementsMatch(t,
		[]string{divergentFilePath, divergentOtherPath, "third.txt"},
		sortedKeys(burndownResult.FileHistories),
		"per-file histories must name the files live at HEAD")

	var ownedTotal int64
	for _, owned := range burndownResult.FileOwnership {
		for author, lines := range owned {
			if author >= 0 {
				ownedTotal += int64(lines)
			}
		}
	}

	assert.Equal(t, int64(11), ownedTotal, "per-file ownership must add up to HEAD's text lines")

	lastTick := -1
	for tick := range busResult.Snapshots {
		lastTick = max(lastTick, tick)
	}

	require.GreaterOrEqual(t, lastTick, 0)
	assert.Equal(t, int64(11), busResult.Snapshots[lastTick].TotalLines)
}

// TestBurndownFinalizeUsesTheLastAuthoritativeResolver is the unit-level statement of the same
// contract: the resolver handed over by the last non-replica commit names the files, not the one
// registered at Configure and not a merge replica's.
func TestBurndownFinalizeUsesTheLastAuthoritativeResolver(t *testing.T) {
	registered := ownershipTestResolver{7: ownershipFile("stale.txt", 0)}
	authoritative := ownershipTestResolver{7: ownershipFile("file.txt", 0), 8: ownershipFile("new.txt", 0)}
	replica := ownershipTestResolver{}

	burndown := &BurndownAnalysis{
		TrackFiles:      true,
		Granularity:     1,
		Sampling:        1,
		peopleResolver:  core.NewIdentityResolver([]string{"first"}, nil),
		primaryResolver: registered,
	}
	require.NoError(t, burndown.Initialize(nil))

	consume := func(resolver core.FileIdResolver, replicaCommit bool, changes ...core.LineHistoryChange) {
		t.Helper()

		_, err := burndown.Consume(map[string]any{
			linehistory.DependencyLineHistory: core.LineHistoryChanges{
				Changes:  changes,
				Resolver: resolver,
			},
			core.DependencyIsMergeReplica: replicaCommit,
		})
		require.NoError(t, err)
	}

	consume(authoritative, false,
		core.LineHistoryChange{FileId: 7, CurrTick: 0, PrevTick: 0, Delta: 2},
		core.LineHistoryChange{FileId: 8, CurrTick: 0, PrevTick: 0, Delta: 3},
	)
	consume(replica, true)

	assert.ElementsMatch(t, []string{"file.txt", "new.txt"}, sortedKeys(burndown.finalizeFileHistories(0)))
}
