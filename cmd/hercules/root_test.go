package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

const testFileScheme = "file"

func TestLoadRemoteRepositories(t *testing.T) {
	origin, head := createTestRepository(t)

	for _, test := range []struct {
		uri     string
		repoURI string
	}{
		{uri: "https://example.test/hercules.git", repoURI: "https://example.test/hercules.git"},
		{uri: "ssh://git@example.test/hercules.git", repoURI: "ssh://example.test/hercules.git"},
	} {
		endpoint, err := transport.NewEndpoint(test.uri)
		require.NoError(t, err)

		previous := client.Protocols[endpoint.Protocol]
		client.InstallProtocol(endpoint.Protocol, server.NewClient(server.MapLoader{
			endpoint.String(): origin.Storer,
		}))

		repo, repoURI, repoFeature, cloneErr := loadRepositoryWithError(test.uri, "", true, "")
		client.InstallProtocol(endpoint.Protocol, previous)

		require.NoError(t, cloneErr, test.uri)
		assert.Equal(t, test.repoURI, repoURI)
		assert.Equal(t, core.FeatureGitCommits, repoFeature)
		_, err = repo.CommitObject(head)
		require.NoError(t, err)
	}
}

func TestSelectRootCommitsRejectsEmptyCommitFile(t *testing.T) {
	repository, _ := createTestRepository(t)
	commitsPath := filepath.Join(t.TempDir(), "commits")
	require.NoError(t, os.WriteFile(commitsPath, nil, 0o600))

	commits, err := selectRootCommits(
		core.NewPipeline(repository),
		repository,
		"test-repository",
		rootOptions{commitsFile: commitsPath},
		nil,
	)

	assert.Nil(t, commits)
	require.ErrorIs(t, err, core.ErrNoCommits)
}

func TestRemoteCacheCreatesManagedDestination(t *testing.T) {
	origin, head := createTestRepository(t)
	cachePath := filepath.Join(t.TempDir(), "remote-cache")

	repository, _, err := cloneRemoteRepository(
		testRepositoryURI(t, origin), cachePath, true, "",
	)

	require.NoError(t, err)
	requireRemoteCacheMarker(t, cachePath)
	_, err = repository.CommitObject(head)
	require.NoError(t, err)

	reopened, _, feature, err := loadRepositoryWithError(cachePath, "", true, "")
	require.NoError(t, err)
	assert.Equal(t, core.FeatureGitCommits, feature)
	_, err = reopened.CommitObject(head)
	require.NoError(t, err)
}

func TestRemoteCacheAcceptsExistingEmptyTarget(t *testing.T) {
	origin, _ := createTestRepository(t)
	cachePath := filepath.Join(t.TempDir(), "remote-cache")
	require.NoError(t, os.Mkdir(cachePath, 0o755))

	_, _, err := cloneRemoteRepository(
		testRepositoryURI(t, origin), cachePath, true, "",
	)
	if err != nil {
		require.ErrorIs(t, err, errAtomicCacheReplacement)
		entries, readErr := os.ReadDir(cachePath)
		require.NoError(t, readErr)
		assert.Empty(t, entries)
		return
	}
	requireRemoteCacheMarker(t, cachePath)
}

func TestRemoteCacheReplacementRequiresForceAndMarker(t *testing.T) {
	origin, _ := createTestRepository(t)
	uri := testRepositoryURI(t, origin)
	cachePath := filepath.Join(t.TempDir(), "remote-cache")
	_, _, err := cloneRemoteRepository(uri, cachePath, true, "")
	require.NoError(t, err)
	sentinelPath := filepath.Join(cachePath, "stale-sentinel")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("keep until replaced"), 0o600))

	_, _, err = cloneRemoteRepository(uri, cachePath, true, "")
	require.ErrorContains(t, err, "--force-cache-replace")
	assert.FileExists(t, sentinelPath)

	_, _, err = cloneRemoteRepositoryWithPolicy(uri, cachePath, true, "", true)
	if err != nil {
		require.ErrorIs(t, err, errAtomicCacheReplacement)
		assert.FileExists(t, sentinelPath)
		return
	}
	requireRemoteCacheMarker(t, cachePath)
	assert.NoFileExists(t, sentinelPath)
}

func TestRemoteCacheRefusesUnrelatedDirectory(t *testing.T) {
	origin, _ := createTestRepository(t)
	cachePath := filepath.Join(t.TempDir(), "unrelated")
	require.NoError(t, os.Mkdir(cachePath, 0o755))
	sentinelPath := filepath.Join(cachePath, "sentinel")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("do not delete"), 0o600))

	for _, force := range []bool{false, true} {
		_, _, err := cloneRemoteRepositoryWithPolicy(
			testRepositoryURI(t, origin), cachePath, true, "", force,
		)
		require.Error(t, err)
		assert.FileExists(t, sentinelPath)
	}
}

func TestRemoteCacheRefusesUnsafeReplacementTargets(t *testing.T) {
	currentDirectory, err := os.Getwd()
	require.NoError(t, err)
	homeDirectory, err := os.UserHomeDir()
	require.NoError(t, err)
	filesystemRoot := filepath.VolumeName(currentDirectory) + string(os.PathSeparator)

	for name, path := range map[string]string{
		"filesystem root":           filesystemRoot,
		"current working directory": currentDirectory,
		"home directory":            homeDirectory,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := inspectRemoteCacheDestination(path, true)
			require.Error(t, err)
			assert.ErrorContains(t, err, "refusing")
		})
	}
}

func TestFailedRemoteCloneLeavesManagedCacheIntact(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "remote-cache")
	require.NoError(t, os.Mkdir(cachePath, 0o755))
	require.NoError(t, writeRemoteCacheMarker(cachePath))
	sentinelPath := filepath.Join(cachePath, "sentinel")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("original"), 0o600))
	missingURI := (&url.URL{
		Scheme: testFileScheme,
		Path:   filepath.ToSlash(filepath.Join(t.TempDir(), "missing-origin")),
	}).String()

	_, _, err := cloneRemoteRepositoryWithPolicy(missingURI, cachePath, true, "", true)

	require.Error(t, err)
	assert.FileExists(t, sentinelPath)
	requireRemoteCacheMarker(t, cachePath)
}

func TestRemoteCacheReplacementFailureIsSurfaced(t *testing.T) {
	origin, _ := createTestRepository(t)
	cachePath := filepath.Join(t.TempDir(), "remote-cache")
	require.NoError(t, os.Mkdir(cachePath, 0o755))
	require.NoError(t, writeRemoteCacheMarker(cachePath))
	sentinelPath := filepath.Join(cachePath, "sentinel")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("original"), 0o600))

	previousSwap := swapRemoteCacheDirectories
	swapRemoteCacheDirectories = func(_, _ string) error {
		return errors.New("injected atomic replacement failure")
	}
	t.Cleanup(func() {
		swapRemoteCacheDirectories = previousSwap
	})

	_, _, err := cloneRemoteRepositoryWithPolicy(
		testRepositoryURI(t, origin), cachePath, true, "", true,
	)

	require.ErrorIs(t, err, errAtomicCacheReplacement)
	assert.ErrorContains(t, err, "injected atomic replacement failure")
	assert.FileExists(t, sentinelPath)
	requireRemoteCacheMarker(t, cachePath)
}

func TestRemoteCacheStatErrorsAreHardFailures(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(parentFile, []byte("fixture"), 0o600))

	_, err := inspectRemoteCacheDestination(filepath.Join(parentFile, "cache"), false)

	require.Error(t, err)
}

func TestRepositoryCloneOptionsHTTPSCredentials(t *testing.T) {
	rawURI := (&url.URL{
		Scheme: "https",
		User:   url.UserPassword("user", "test-password"),
		Host:   "example.test",
		Path:   "/hercules.git",
	}).String()
	options, repoURI, err := repositoryCloneOptions(rawURI, "")

	require.NoError(t, err)
	assert.Equal(t, rawURI, options.URL)
	assert.Equal(t, "https://example.test/hercules.git", repoURI)
	assert.Nil(t, options.Auth)
}

func TestLoadLocalRepository(t *testing.T) {
	origin, head := createTestRepository(t)
	worktree, err := origin.Worktree()
	require.NoError(t, err)
	root := worktree.Filesystem.Root()

	repo, repoURI, repoFeature, err := loadRepositoryWithError(root, "", true, "")

	require.NoError(t, err)
	assert.Equal(t, root, repoURI)
	assert.Equal(t, core.FeatureGitCommits, repoFeature)
	_, err = repo.CommitObject(head)
	require.NoError(t, err)
}

func TestLoadFileRepository(t *testing.T) {
	origin, head := createTestRepository(t)
	worktree, err := origin.Worktree()
	require.NoError(t, err)
	uri := (&url.URL{
		Scheme: testFileScheme,
		Path:   filepath.ToSlash(worktree.Filesystem.Root()),
	}).String()

	repo, repoURI, repoFeature, err := loadRepositoryWithError(uri, "", true, "")

	require.NoError(t, err)
	assert.Equal(t, uri, repoURI)
	assert.Equal(t, core.FeatureGitCommits, repoFeature)
	_, err = repo.CommitObject(head)
	require.NoError(t, err)
}

func TestRepositoryCloneOptionsSSHIdentity(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "id_rsa")
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	const uri = "git@example.test:org/hercules.git"
	options, repoURI, err := repositoryCloneOptions(uri, keyPath)

	require.NoError(t, err)
	assert.Equal(t, uri, options.URL)
	assert.Equal(t, uri, repoURI)
	_, ok := options.Auth.(*gitssh.PublicKeys)
	assert.True(t, ok)
}

func TestLoadSivaRepository(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	assert.True(t, ok)
	sivafile := filepath.Join(filepath.Dir(filename), "test_data", "hercules.siva")
	repo, _, repoFeature := loadRepository(sivafile)
	assert.NotNil(t, repo)
	assert.Equal(t, core.FeatureGitCommits, repoFeature)

	assert.Panics(t, func() { loadRepository("unsupported://example.test/repository.git") })
	assert.Panics(t, func() { loadRepository(filepath.Dir(filename)) })
	assert.Panics(t, func() { loadRepository("/xxx") })
}

func TestLoadStubRepository(t *testing.T) {
	repo, repoUri, repoFeature := loadRepository("-")
	assert.NotNil(t, repo)
	assert.Equal(t, "-", repoUri)
	assert.Equal(t, core.FeatureGitStub, repoFeature)
}

func TestFormatProgressEventLines(t *testing.T) {
	line, err := formatProgressEvent(progressEvent{
		Event:  "commit",
		Commit: 12,
		Total:  90,
		Action: "TreeDiff",
	}, progressModeLines)

	require.NoError(t, err)
	assert.Equal(t, "commit commit=12 total=90 action=TreeDiff\n", line)
}

func TestFormatProgressEventJSON(t *testing.T) {
	line, err := formatProgressEvent(progressEvent{
		Event:  "write-start",
		Output: "protobuf",
	}, progressModeJSON)

	require.NoError(t, err)
	var decoded progressEvent
	require.NoError(t, json.Unmarshal([]byte(line), &decoded))
	assert.Equal(t, "write-start", decoded.Event)
	assert.Equal(t, "protobuf", decoded.Output)
}

func TestParseProgressModeRejectsUnknownValue(t *testing.T) {
	_, err := parseProgressMode("loud")
	require.Error(t, err)
}

func TestIdentityWorkflowFlagsRegistered(t *testing.T) {
	assert.NotNil(t, rootCmd.Flags().Lookup("identity-audit"))
	assert.NotNil(t, rootCmd.Flags().Lookup("identity-merge-threshold"))
	assert.NotNil(t, rootCmd.Flags().Lookup("people-dict-template"))
	assert.NotNil(t, rootCmd.Flags().Lookup("force-cache-replace"))
}

func TestIdentityAuditWorkflowWritesJSON(t *testing.T) {
	var out bytes.Buffer
	err := runIdentityWorkflow(identityWorkflowOptions{
		Commits: []*object.Commit{
			{
				Author:  object.Signature{Name: "Alice Example", Email: "alice@users.noreply.github.com"},
				Message: "Pairing\n\nCo-authored-by: Alice Example <alice@example.com>",
			},
		},
		Facts: map[string]any{},
		Audit: true,
		Out:   &out,
	})

	require.NoError(t, err)
	var audit identity.IdentityAudit
	require.NoError(t, json.Unmarshal(out.Bytes(), &audit))
	assert.Len(t, audit.Identities, 1)
	assert.Equal(t, "co-authored-by", audit.MergeDecisions[0].Reason)
}

func TestIdentityTemplateWorkflowWritesPeopleDictFile(t *testing.T) {
	tempdir := t.TempDir()
	templatePath := filepath.Join(tempdir, "people.txt")

	err := runIdentityWorkflow(identityWorkflowOptions{
		Commits: []*object.Commit{
			{
				Author:  object.Signature{Name: "Alice Example", Email: "alice@example.com"},
				Message: "Initial",
			},
		},
		Facts:        map[string]any{},
		TemplatePath: templatePath,
	})

	require.NoError(t, err)
	data, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	assert.Equal(t, "alice example|alice@example.com\n", string(data))
}

func createTestRepository(t *testing.T) (*git.Repository, plumbing.Hash) {
	t.Helper()

	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0o600))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add("README.md")
	require.NoError(t, err)

	signature := &object.Signature{
		Name:  "Hercules Test",
		Email: "hercules@example.test",
		When:  time.Unix(1_700_000_000, 0),
	}
	head, err := worktree.Commit("fixture", &git.CommitOptions{
		Author:    signature,
		Committer: signature,
	})
	require.NoError(t, err)
	return repo, head
}

func testRepositoryURI(t *testing.T, repository *git.Repository) string {
	t.Helper()

	worktree, err := repository.Worktree()
	require.NoError(t, err)
	return (&url.URL{
		Scheme: testFileScheme,
		Path:   filepath.ToSlash(worktree.Filesystem.Root()),
	}).String()
}

func requireRemoteCacheMarker(t *testing.T, cachePath string) {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(cachePath, remoteCacheMarkerName))
	require.NoError(t, err)
	assert.Equal(t, remoteCacheMarkerContent, string(content))
}
