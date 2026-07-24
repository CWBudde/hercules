package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/stretchr/testify/assert"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
)

func TestLoadGitRepository(t *testing.T) {
	repo, repoUri, repoFeature := loadRepository("https://github.com/src-d/hercules", "", true, "")
	assert.NotNil(t, repo)
	assert.Equal(t, repoFeature, core.FeatureGitCommits)
	assert.Equal(t, repoUri, "https://github.com/src-d/hercules")
}

func TestLoadGitRepositoryWithCreds(t *testing.T) {
	_, repoUri, repoFeature, err := loadRepositoryWithError("https://user:user@github.com/src-d/hercules", "", true, "")
	assert.NotNil(t, err)
	assert.Equal(t, repoFeature, core.FeatureGitCommits)
	assert.Equal(t, repoUri, "https://github.com/src-d/hercules")
}

func TestLoadLocalRepository(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "hercules-")
	assert.Nil(t, err)
	defer func() { _ = os.RemoveAll(tempdir) }()

	backend := filesystem.NewStorage(osfs.New(tempdir), cache.NewObjectLRUDefault())
	cloneOptions := &git.CloneOptions{URL: "https://github.com/src-d/hercules"}
	_, err = git.Clone(backend, nil, cloneOptions)
	assert.Nil(t, err)
	if err != nil {
		assert.FailNow(t, "filesystem.NewStorage")
	}

	repo, repoUri, repoFeature := loadRepository(tempdir, "", true, "")
	assert.NotNil(t, repo)
	assert.Equal(t, repoUri, tempdir)
	assert.Equal(t, repoFeature, core.FeatureGitCommits)
}

func TestLoadSivaRepository(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	sivafile := filepath.Join(filepath.Dir(filename), "test_data", "hercules.siva")
	repo, _, repoFeature := loadRepository(sivafile, "", true, "")
	assert.NotNil(t, repo)
	assert.Equal(t, repoFeature, core.FeatureGitCommits)

	assert.Panics(t, func() { loadRepository("https://github.com/src-d/porn", "", true, "") })
	assert.Panics(t, func() { loadRepository(filepath.Dir(filename), "", true, "") })
	assert.Panics(t, func() { loadRepository("/xxx", "", true, "") })
}

func TestLoadStubRepository(t *testing.T) {
	repo, repoUri, repoFeature := loadRepository("-", "", true, "")
	assert.NotNil(t, repo)
	assert.Equal(t, repoUri, "-")
	assert.Equal(t, repoFeature, core.FeatureGitStub)
}

func TestFormatProgressEventLines(t *testing.T) {
	line, err := formatProgressEvent(progressEvent{
		Event:  "commit",
		Commit: 12,
		Total:  90,
		Action: "TreeDiff",
	}, progressModeLines)

	assert.NoError(t, err)
	assert.Equal(t, "commit commit=12 total=90 action=TreeDiff\n", line)
}

func TestFormatProgressEventJSON(t *testing.T) {
	line, err := formatProgressEvent(progressEvent{
		Event:  "write-start",
		Output: "protobuf",
	}, progressModeJSON)

	assert.NoError(t, err)
	var decoded progressEvent
	assert.NoError(t, json.Unmarshal([]byte(line), &decoded))
	assert.Equal(t, "write-start", decoded.Event)
	assert.Equal(t, "protobuf", decoded.Output)
}

func TestParseProgressModeRejectsUnknownValue(t *testing.T) {
	_, err := parseProgressMode("loud")
	assert.Error(t, err)
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
		Facts: map[string]interface{}{},
		Audit: true,
		Out:   &out,
	})

	assert.NoError(t, err)
	var audit identity.IdentityAudit
	assert.NoError(t, json.Unmarshal(out.Bytes(), &audit))
	assert.Len(t, audit.Identities, 1)
	assert.Equal(t, "co-authored-by", audit.MergeDecisions[0].Reason)
}

func TestIdentityTemplateWorkflowWritesPeopleDictFile(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "hercules-identity-")
	assert.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempdir) }()
	templatePath := filepath.Join(tempdir, "people.txt")

	err = runIdentityWorkflow(identityWorkflowOptions{
		Commits: []*object.Commit{
			{
				Author:  object.Signature{Name: "Alice Example", Email: "alice@example.com"},
				Message: "Initial",
			},
		},
		Facts:        map[string]interface{}{},
		TemplatePath: templatePath,
	})

	assert.NoError(t, err)
	data, err := os.ReadFile(templatePath)
	assert.NoError(t, err)
	assert.Equal(t, "alice example|alice@example.com\n", string(data))
}
