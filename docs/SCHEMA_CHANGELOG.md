# Hercules Schema Changelog

This file records intentional output schema changes. Add entries when
`internal/pb/pb.schema.json`, `internal/pb/pb.proto`, or documented YAML output
contracts change.

## Format

Each entry should include:

- Date and commit/PR reference when known.
- Affected format: YAML, PB, or both.
- Affected analysis or message.
- Compatibility classification: compatible, breaking, or documentation-only.
- Required user action, if any.

## Unreleased

- PB and YAML: `KnowledgeDiffusionFileData` gains `lines` (field 5), `churn` (field 6),
  `recent_churn` (field 7) and `ticks_since_last_edit` (field 8), all `int32`; the YAML output
  gains the matching per-file keys. The silo chart ranked its files by editor count tie-broken
  alphabetically, which at organisation scale is no ranking at all — almost every file has exactly
  one editor, so the tie-break was the ordering. Ranking them by risk needs size, churn and
  recency, none of which this analysis collected. The factors are stored **raw**; the renderer
  normalises and scores them over the whole file set, so unlike `HotspotRiskResults` there is
  nothing per-run-normalised to rescale at merge time. `KnowledgeDiffusion` now also requires
  `DependencyCommit` and `DependencyLineStats`, and reads the last commit's tree for line counts.
  Compatibility: compatible — the new fields are optional and all-zero decodes as "unset", which is
  what every file written so far carries. No `SchemaVersion` bump.
  User action: none required. A stored result written before this change still renders; its silo
  chart keeps the old editor-count-then-path ordering, because the renderer falls back to it when
  every risk score ties at zero. Recompute to get the ranking.
- PB and YAML: `OnboardingResults` gains `trail_days` (field 7, int32);
  `AuthorOnboardingData` gains `first_commit_unix` (field 4, int64) and `trail` (field 5,
  repeated `OnboardingTrailEntry`, a new message). The YAML output gains the matching
  `trail_days` and per-author `first_commit_unix` keys but **not** the trail, which is
  PB-only. Onboarding snapshots are anchored at each repository's own first commit by that
  author, so they cannot be summed across repositories; the bounded trail keeps the commits
  the snapshots were computed from, which lets `hercules combine` re-anchor on the
  organisation-wide first commit and recompute the windows exactly. `Onboarding` is now
  mergeable and no longer dropped by combine. Combining runs whose tick size, meaningful
  threshold, or window days disagree is an error, as is combining a result written before
  the trail existed.
  Compatibility: compatible — the new fields are optional and all-zero decodes as "unset",
  which is what every file written so far carries. No `SchemaVersion` bump.
  User action: recompute stored Onboarding results before combining them; older files are
  rejected by the merge (per input, so one stale file excludes itself rather than failing
  the run) and older readers ignore the new fields.
- PB and YAML: `HotspotRiskResults` gains `top_n` (field 3, int32) and `weight_size`,
  `weight_churn`, `weight_coupling`, `weight_ownership` (fields 4-7, float); the YAML output
  gains the matching `top_n` and `weights` keys. They record the configuration the run was
  produced with, because `hercules combine` summons the analysis zero-valued and could not
  otherwise honour `--hotspot-risk-top` or rescale the merged scores. Combining runs whose
  weights disagree is now an error, mirroring the existing churn-window check.
  Compatibility: compatible — all-zero is read as "unset" and falls back to the defaults,
  which is exactly what every file written so far carries. No `SchemaVersion` bump.
  User action: none; older readers ignore the fields. Note that combined Hotspot Risk
  rankings change, because the merge now rescales both runs onto one scale and deduplicates
  equal paths instead of concatenating.
- PB: `Metadata` gains `qualified_paths` (field 9, bool). It records whether the path keys inside
  the contents already name the repository they belong to (`repository:path`), which is what
  `hercules combine` now writes so that the same path in two repositories does not fuse into one
  key. Combine sets it on its output and skips qualification for inputs which carry it.
  Compatibility: compatible — the field is optional and absent means "not qualified", which is
  exactly what every file written so far is. No `SchemaVersion` bump. User action: none; older
  readers ignore the field.

## 0.2.0 - 2026-07-28

- PB and YAML: readers now require a valid metadata header and enforce the
  documented schema compatibility matrix. PB schema 1 is migrated
  content-by-content to schema 2; legacy YAML `version: 0` is normalized only
  after full schema-2 validation. Missing, malformed, unsupported, and future
  versions are rejected, and `hercules combine` cannot emit schema-2 metadata
  for unvalidated legacy contents. Compatibility: supported historical PB v1
  Burndown, Couples, and UAST payloads and known YAML v0 files remain readable;
  previously accepted unversioned or falsely versioned inputs now fail. User
  action: regenerate or explicitly migrate rejected files before rendering or
  combining them.
- YAML: completed `LineDumper` replay without changing fields or types.
  `--history-line-load` now preserves loaded metadata through initialization,
  reconstructs deterministic live-line ownership for `ScanFile`, and rejects
  malformed hashes, ranges, inconsistent deltas, truncation, and trailing YAML
  documents. Original source positions remain unavailable because the format
  stores aggregate deltas. Compatibility: compatible for valid dumps; invalid
  dumps that were previously accepted now fail with typed errors. User action:
  fix or regenerate any rejected dump.
- YAML and PB: defined and corrected the experimental `CodeChurn` semantics
  without changing wire fields or types. Awareness and memorability are bounded
  scores with documented 30-tick and 180-tick half-lives, affected-line
  reinforcement, and other-author disruption. YAML now emits the existing
  deletion-history data, matching PB, and counter overflow returns an error
  instead of wrapping. Compatibility: semantic breaking change (existing
  consumers parse the output unchanged, but scores and YAML content can
  differ). User action: recompute Code Churn output before comparing scores
  across this release boundary.
- YAML and PB: corrected Burndown resampling when a still-forming age band
  decreases, without changing wire fields or types. Existing cohorts now decay
  proportionally instead of being represented as negative new cohorts.
  Project, file, person, and repository matrices are validated before merge or
  serialization, and invalid state returns a typed diagnostic instead of being
  clamped in YAML. Compatibility: semantic breaking change (existing consumers
  parse valid output unchanged, but corrected histories can differ and invalid
  histories now fail). User action: recompute stored Burndown results; callers
  that supplied negative matrices must handle the returned error.
- YAML and PB: corrected `FileHistoryAnalysis` rename handling without changing
  wire fields or types. Renames now conserve the source file's complete commit
  and per-author line-statistics history; destination collisions merge both
  histories instead of dropping one side. Compatibility: semantic breaking
  change (existing consumers parse the output unchanged, but corrected history
  values can differ). User action: recompute results containing file renames,
  especially renames onto a path with existing history.
- YAML and PB: corrected `Couples` partial-result identity mapping without
  changing wire fields or types. File and developer indices are now translated
  through their respective dictionaries, and valid merges are associative.
  Compatibility: semantic breaking change (existing consumers parse the output
  unchanged, but combined matrix values can differ). User action: recompute
  results assembled across pipeline branches.
- YAML and PB: corrected `Shotness` stable identities and `couples-shotness`
  matrix construction without changing wire fields or types. Entities retain
  identity across coordinate moves and explicit file renames by path, kind, and
  qualified name. Coupling charts use entity-profile dot products; diagonals
  are squared norms and are excluded from distinct-pair rankings. YAML and PB
  readers share this construction. Compatibility: semantic breaking change
  (existing consumers parse the data unchanged, but names, counters, matrix
  values, and rankings can differ). User action: recompute Shotness data and
  rerender Shotness coupling charts.
- YAML and PB: corrected the repository-filtered tree view used by analyses
  without changing any analysis wire fields or types. Old and new tree sides
  are filtered independently, so included-to-excluded transitions become
  deletions and excluded-to-included transitions become insertions. Filter
  errors no longer advance the diff baseline. Compatibility: semantic breaking
  change (existing consumers parse the output unchanged, but downstream values
  can differ). User action: recompute analyses using vendor, blacklist,
  whitelist, or language filters.
- YAML and PB: corrected `BusFactor` and `OwnershipConcentration` snapshot
  semantics without changing fields or wire types. Occupied ticks now contain
  only ownership state from commits at or before the labeled tick, including
  the final tick. Both analyses share incremental global and per-file
  ownership accounting, so their final subsystem metrics reconcile with the
  global snapshot. Bus Factor now uses exact ceiling coverage for the
  configured decimal threshold instead of truncating fractional required
  lines. Compatibility: semantic breaking change (existing consumers will
  parse the output unchanged, but corrected per-tick and subsystem values can
  differ). User action: recompute stored Bus Factor and Ownership
  Concentration results before comparing them across this release boundary.
- YAML and PB: corrected `Onboarding` cohort and snapshot semantics without
  changing fields or wire types. Join cohorts now use each author's actual
  earliest author timestamp and offset. Day windows use exact 24-hour durations
  and include only commits at or before the inclusive boundary; they never
  select later activity, even when ticks are coarse or do not divide a day.
  Compatibility: semantic breaking change (existing consumers will parse the
  output unchanged, but corrected cohort keys and snapshot values can differ).
  User action: recompute stored Onboarding results before comparing cohorts or
  snapshots across this release boundary.
- YAML and PB: corrected the `HotspotRisk` metric semantics without changing
  its fields or wire types. Churn now counts real text-line edits from the
  producer's full change-entry keys and uses duration-based tick windows;
  ownership comes from current line history; renames preserve churn and
  coupling history; binary files do not affect text-file coupling; and the
  score is a weighted normalized arithmetic mean in which a `0.0` weight
  disables its factor. Compatibility: semantic breaking change (existing
  consumers will parse the output unchanged, but should expect corrected
  values and rankings). User action: recompute stored Hotspot Risk results
  before comparing scores across this release boundary.
- Both: the schema version is now emitted from the single constant
  `pb.SchemaVersion` (`internal/pb/version.go`, value 2). This fixes the YAML
  header (`hercules.version`) and `hercules combine` PB output, which had
  emitted `0` since the module rename broke the package-suffix-derived
  `BinaryVersion`; `--pb` output already emitted 2 and is unchanged.
  Compatibility: compatible (bug fix; no field/type changes). User action:
  consumers that pinned `version: 0` from the YAML header should expect 2.
- PB: extended `internal/pb/pb.schema.json` with the top-level `version` and
  per-message `reserved_numbers`/`reserved_names`; added `cmd/schema-guard`
  and the `test-schema` CI workflow, which require a changelog entry for every
  schema change and a `pb.SchemaVersion` bump for breaking changes. Documented
  the retired `UASTChange`/`UASTChangesSaverResults` message names in
  `pb.proto`. Compatibility: documentation/tooling only. User action: none.
- PB: added `internal/pb/pb.schema.json` as the checked-in compatibility
  snapshot and `internal/pb/schema_snapshot_test.go` as the CI guardrail.
  Compatibility: documentation/tooling only. User action: none.
