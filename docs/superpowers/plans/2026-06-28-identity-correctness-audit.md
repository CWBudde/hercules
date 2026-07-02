# Identity Correctness And Auditability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Roadmap Milestone 5 with deterministic identity heuristics, JSON audit output, and a generated people-dict template.

**Architecture:** Extend `internal/plumbing/identity.PeopleDetector` so normal analyses, audit mode, and template generation share the same author identity model. Add CLI flags through the existing registry/configuration path and a small root-command pre-analysis branch for audit/template workflows.

**Tech Stack:** Go, go-git commit objects, Cobra flags, standard-library JSON, existing Hercules pipeline configuration.

---

### Task 1: Identity Audit Model And Helpers

**Files:**

- Modify: `internal/plumbing/identity/people.go`
- Test: `internal/plumbing/identity/people_test.go`

- [ ] **Step 1: Write failing tests**

Add tests that construct commits with controlled author names, emails, and messages. Assert that generated audit output includes deterministic identity groups, merge decisions for trailers, and ambiguous entries for below-threshold fuzzy matches.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/plumbing/identity -run 'TestPeopleDetectorIdentityAudit|TestPeopleDetectorCoAuthorTrailers|TestPeopleDetectorFuzzy' -count=1`

Expected: FAIL because the audit model and new heuristic behavior do not exist.

- [ ] **Step 3: Implement audit structures and deterministic helper functions**

Add exported result structs to `people.go`, keep helper functions local unless tests need package access, normalize names/emails consistently, and sort all output slices.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/plumbing/identity -run 'TestPeopleDetectorIdentityAudit|TestPeopleDetectorCoAuthorTrailers|TestPeopleDetectorFuzzy' -count=1`

Expected: PASS.

### Task 2: Threshold Configuration And People-Dict Template

**Files:**

- Modify: `internal/plumbing/identity/people.go`
- Test: `internal/plumbing/identity/people_test.go`

- [ ] **Step 1: Write failing tests**

Add tests for default threshold, invalid threshold rejection, high-threshold ambiguous behavior, and stable people-dict template lines.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/plumbing/identity -run 'TestPeopleDetectorIdentityThreshold|TestPeopleDetectorPeopleDictTemplate' -count=1`

Expected: FAIL because the new options and template method do not exist.

- [ ] **Step 3: Implement configuration and template generation**

Add `ConfigIdentityDetectorMergeThreshold`, default `0.92`, validation for `0 <= threshold <= 1`, and `GeneratePeopleDictTemplate() string`.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/plumbing/identity -run 'TestPeopleDetectorIdentityThreshold|TestPeopleDetectorPeopleDictTemplate' -count=1`

Expected: PASS.

### Task 3: CLI Audit And Template Workflows

**Files:**

- Modify: `cmd/hercules/root.go`
- Test: `cmd/hercules/root_test.go`

- [ ] **Step 1: Write failing CLI tests**

Add tests that assert `--identity-audit`, `--identity-merge-threshold`, and `--people-dict-template` are registered and that audit/template helper functions return JSON/template data for fixture commits.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/hercules -run 'TestIdentity' -count=1`

Expected: FAIL because the CLI workflow helpers do not exist.

- [ ] **Step 3: Implement CLI workflow helpers**

Add root flags through `PeopleDetector.ListConfigurationOptions()` where possible. Add root-command handling that, when audit/template flags are set, configures a `PeopleDetector` with the resolved commit list, writes JSON to stdout for audit, writes the template file when requested, and returns before normal analysis.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./cmd/hercules -run 'TestIdentity' -count=1`

Expected: PASS.

### Task 4: Documentation And Roadmap

**Files:**

- Modify: `README.md`
- Modify: `ROADMAP.md`

- [ ] **Step 1: Update docs**

Document the identity audit and people-dict template workflow next to the existing People section.

- [ ] **Step 2: Mark Milestone 5 items complete where implemented**

Update Roadmap checklist entries for heuristics, audit JSON, and people-dict template.

- [ ] **Step 3: Run focused verification**

Run: `go test ./internal/plumbing/identity ./cmd/hercules`

Expected: PASS.

- [ ] **Step 4: Run full project verification**

Run: `just test`

Expected: PASS.
