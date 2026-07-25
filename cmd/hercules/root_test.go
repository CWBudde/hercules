package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(worktree.Filesystem.Root())}).String()

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
