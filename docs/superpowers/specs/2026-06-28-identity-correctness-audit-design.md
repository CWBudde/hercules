# Identity Correctness And Auditability Design

## Goal

Complete Roadmap Milestone 5 by making identity resolution decisions visible, configurable, and testable while preserving the existing author-index contract used by analyses.

## Architecture

Identity resolution remains owned by `internal/plumbing/identity.PeopleDetector`. The detector will still emit `identity.DependencyAuthor` and `FactIdentityDetectorReversedPeopleDict`, but it will also retain an audit model describing the identities it detected, the automatic merge decisions it made, and candidates it intentionally left ambiguous.

The CLI will expose two workflow flags:

- `--identity-audit`: write the audit report as JSON and exit without running analyses.
- `--people-dict-template <path>`: write the detected identity groups in people-dict format for manual refinement.

Both workflows use the same commit list and detector configuration as normal analysis, so users audit exactly the identities Hercules would use.

## Identity Heuristics

The current exact email/name merging remains the baseline. Two additional heuristic sources are added:

- `Co-authored-by:` commit trailers add aliases for GitHub-style collaboration metadata. The trailer parser records name and email values and merges them with the commit author when confidence is above threshold.
- Fuzzy name matching compares normalized names with Jaro-Winkler similarity. Automatic fuzzy merges require matching email domain or a strong similarity score and must pass a configurable confidence threshold.

Automatic merge decisions are deterministic: commit order is stable, candidate comparisons sort by canonical identity, and tie cases become ambiguous audit entries instead of arbitrary merges.

## Configuration

New `PeopleDetector` options:

- `--identity-merge-threshold`: floating point confidence threshold for automatic heuristic merges. Default: `0.92`.
- `--identity-audit`: boolean flag for JSON audit mode.
- `--people-dict-template`: output path for a generated people-dict template.

`--exact-signatures` disables heuristic identity merging, because exact signature mode exists specifically to avoid opportunistic identity grouping.

## Output

The audit JSON contains:

- `identities`: canonical ID, primary display value, names, emails, signatures, and source count.
- `merge_decisions`: deterministic automatic merge records with confidence and reason.
- `ambiguous`: candidate pairs that were similar but below threshold or tied.
- `threshold`: the configured automatic merge threshold.

The people-dict template contains one line per identity. Each line uses the existing `people-dict` format: `primary|alias|alias@example.com`.

## Error Handling

Invalid thresholds fail configuration before analysis. Audit/template workflows return I/O errors directly. Empty repositories keep existing pipeline behavior and do not invent identity output.

## Testing

Add deterministic unit tests around:

- `Co-authored-by:` trailer parsing and merge decisions.
- fuzzy name matching, confidence threshold behavior, and ambiguous candidates.
- generated people-dict template ordering.
- audit JSON shape and deterministic ordering.
- CLI flag registration for the new workflow flags.

Focused verification uses:

```bash
go test ./internal/plumbing/identity ./cmd/hercules
```

Broader verification uses:

```bash
just test
```
