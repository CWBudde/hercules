# Release Hygiene Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish Milestone 6 by documenting release reality, marking optional/experimental behavior clearly, and running release hygiene checks.

**Architecture:** Keep user-facing documentation in `README.md`, maintainer release policy in `docs/RELEASE.md`, and the small sentiment help correction in the existing TensorFlow build-tag file. Do not introduce new runtime behavior beyond help text.

**Tech Stack:** Go, Cobra-generated help via Hercules registry descriptions, Markdown documentation, existing `just` recipes.

---

### Task 1: Sentiment Help Text

**Files:**
- Modify: `leaves/comment_sentiment.go`
- Inspect: `leaves/comment_sentiment_stub.go`

- [ ] **Step 1: Confirm default build marker**

Run: `grep -n "EXPERIMENTAL" leaves/comment_sentiment_stub.go`

Expected: the non-TensorFlow stub description already contains `[EXPERIMENTAL]`.

- [ ] **Step 2: Update TensorFlow description**

Change `CommentSentimentAnalysis.Description()` in `leaves/comment_sentiment.go` so the returned string begins with `[EXPERIMENTAL]`.

- [ ] **Step 3: Format the file**

Run: `gofmt -w leaves/comment_sentiment.go`

Expected: command exits 0.

### Task 2: Release Documentation

**Files:**
- Modify: `README.md`
- Create: `docs/RELEASE.md`

- [ ] **Step 1: Update README release-facing sections**

Add or tighten text for Go version, default cgo-free build, optional `tensorflow` and `cgo_lz4` tags, preset examples, limitations for sentiment and embeddings, and a link to `docs/RELEASE.md`.

- [ ] **Step 2: Create release guide**

Create `docs/RELEASE.md` with version policy, default artifact expectations, optional build tags, migration notes from old upstream, and a pre-tag checklist.

- [ ] **Step 3: Check docs for unfinished marker language**

Run: `rg -n "FIXME|unfinished" README.md docs/RELEASE.md`

Expected: no output.

### Task 3: Roadmap And Quality Gates

**Files:**
- Modify: `ROADMAP.md`

- [ ] **Step 1: Run formatting and vet checks**

Run: `go fmt ./...`

Expected: command exits 0.

Run: `go vet ./...`

Expected: command exits 0, or report exact package failures.

- [ ] **Step 2: Run build and focused tests**

Run: `go build ./cmd/hercules`

Expected: command exits 0.

Run: `go test ./internal/plumbing/identity ./cmd/hercules ./leaves -count=1`

Expected: command exits 0.

- [ ] **Step 3: Run smoke commands**

Run: `./hercules --dry-run .`

Expected: command exits 0.

Run: `./hercules --preset quick .`

Expected: command exits 0.

- [ ] **Step 4: Run project test recipe**

Run: `just test`

Expected: command exits 0, or report known `internal/core` fixture failures if they remain.

- [ ] **Step 5: Update roadmap**

Mark Milestone 6 checklist items complete only for verified work. If one quality gate remains blocked by known unrelated failures, record that status explicitly.

- [ ] **Step 6: Commit**

Run: `git add README.md ROADMAP.md docs/RELEASE.md docs/superpowers/specs/2026-06-29-release-hygiene-design.md docs/superpowers/plans/2026-06-29-release-hygiene.md leaves/comment_sentiment.go`

Run: `git commit -m "docs: prepare release hygiene documentation"`
