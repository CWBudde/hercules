# Hercules - Plan

Date: 2026-07-06

The Go-renderer integration (former Part A) and the carried-over roadmap work (former Part B) are
**complete**. The detailed execution log has been compacted away; the full history lives in git and in
the merged PRs (#1–#4 in this repo, and the cross-repo PRs noted below).

## Remaining work

Only one item is still open, and it lives in another repository:

- **Merge `ewws-statistics` PR #1** — repoints that repo off the retired `labours-go` binary onto the
  in-repo `labours`. Branch `claude/repoint-labours`, pushed and green; awaiting merge.
  (The former Phase 10a. Everything else in Phase 10 is done: the old `labours-go` repo is archived
  with a superseded notice pointing here; `matplotlib-go` was tagged `v0.3.1` and this repo bumped to
  it, removing the init-time rcParam stderr noise.)

Once PR #1 is merged, the plan is fully complete and this file can be deleted.

## Deferred / Not Planned

Decisions recorded so they are not re-litigated; revisit only if a concrete trigger appears:

- **Native JSON export mode** — deferred. Revisit only when a concrete downstream JSON consumer or
  schema-validation need appears; the path is to add a `--json` branch marshaling the per-leaf PB
  messages with `github.com/gogo/protobuf/jsonpb` and generate JSON Schemas from `internal/pb/pb.proto`.
- **Code review metrics** — needs GitHub/GitLab API integration (Git history alone is insufficient).
  Revisit when an `internal/platform/` abstraction exists.
- **Remote repository cloning (HTTPS/SSH)** — handle externally by cloning locally for now.
- **Caching** — deferred until repeated real-world workloads show a stable bottleneck.

## Verification reference

Standard subset for future changes:

```bash
just                 # build hercules + labours
just test            # go test ./...
just check-tidy
just check-formatted
just test-visual
just test-visual-parity
./hercules --burndown --pb <repo> | ./labours -f pb -m burndown-project -o /tmp/b.png
./hercules report --all --strict -o /tmp/report <repo>
```
