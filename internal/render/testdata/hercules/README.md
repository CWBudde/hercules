# Hercules-Generated Protobuf Fixtures

These fixtures are generated from the neighboring `../hercules` checkout and are intended for reader/schema compatibility tests.

Regenerate the fixture set with:

```bash
just fixtures
```

The script behind that target is `scripts/generate_hercules_fixtures.sh`. It accepts an optional output directory and supports:

- `HERCULES_BIN=/path/to/hercules`
- `HERCULES_FIXTURE_REPO=/path/to/repo-or-siva`

## `report_default.pb`

Generated on 2026-04-26 from:

```bash
../hercules/hercules \
  --pb --quiet \
  --burndown --burndown-files --burndown-people \
  --couples \
  --devs \
  --temporal-activity \
  --bus-factor \
  --ownership-concentration \
  --knowledge-diffusion \
  --hotspot-risk \
  ../hercules/cmd/hercules/test_data/hercules.siva \
  > test/testdata/hercules/report_default.pb
```

The source repository fixture is `../hercules/cmd/hercules/test_data/hercules.siva`.
This file represents the default analysis flag set used by `../hercules/cmd/hercules/report.go`.

## `shotness.pb`

Generated on 2026-04-26 from:

```bash
../hercules/hercules \
  --pb --quiet \
  --shotness \
  ../hercules/cmd/hercules/test_data/hercules.siva \
  > test/testdata/hercules/shotness.pb
```

The source repository fixture is `../hercules/cmd/hercules/test_data/hercules.siva`.
This file covers the `ShotnessAnalysisResults` payload used by `shotness` and `couples-shotness` in report-all workflows.

## `sentiment.pb`

Generated on 2026-04-28 as a small synthetic `CommentSentimentResults` protobuf
fixture for reader and plot parity. The header repository is
`synthetic-sentiment-parity`; the payload contains eight non-empty
`sentiment_by_tick` entries.

A TensorFlow-enabled neighboring Hercules binary is now available locally, but
the small checked fixture and local repository runs produced an empty
`sentiment_by_tick` payload. The attempted real generation command was:

```bash
../hercules/hercules \
  --pb --quiet \
  --sentiment \
  ../hercules/cmd/hercules/test_data/hercules.siva \
  > test/testdata/hercules/sentiment.pb
```

Keep the synthetic fixture until a compact real repository fixture produces
non-empty sentiment ticks. Then replace this file with the real output, update
this provenance note, and refresh extraction or parity coverage as needed.

## Extraction Goldens

`report_default_summary.golden.json` and `shotness_summary.golden.json` are stable reader extraction summaries for the checked-in real fixtures. Regenerate them after intentionally replacing the `.pb` fixtures with:

```bash
LABOURS_GO_UPDATE_GOLDENS=1 go test ./internal/readers -run ExtractionGolden
```
