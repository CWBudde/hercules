# Renderer Output Manifest

This is the file-output contract for every concrete `labours --mode` value. It mirrors the
in-process renderer manifest in `internal/render/output.go`; a test requires every implemented mode
to appear here with all of its declared assets.

`<output>` is the path passed with `--output`. `<base>` is that path without its extension and
`<ext>` is the selected `.png`, `.svg`, or `.pdf` extension where the mode supports it. For a
multi-mode invocation, an output file contributes its parent directory and format, while an output
directory receives one mode-named base per mode. File and identity fan-outs use bounded rune-safe
slugs plus a stable hash, so distinct identities cannot overwrite each other after sanitization.
Person fan-outs use only the public canonical name in the readable slug; the complete identity
contributes only the non-reversible hash.

| Mode                      | Output kind                         | Declared assets                                                                                                                                                                                                                                                                                    |
| ------------------------- | ----------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `burndown-file`           | file fan-out                        | `<base>_<rune-safe-file-slug>-<hash><ext>`                                                                                                                                                                                                                                                         |
| `burndown-person`         | person fan-out                      | `<base>_<canonical-person-slug>-<identity-hash><ext>`                                                                                                                                                                                                                                              |
| `burndown-project`        | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `burndown-repository`     | asset directory                     | `burndown-repository_<rune-safe-repository-slug>-<hash>.png`, `burndown-repository_<rune-safe-repository-slug>-<hash>.svg`                                                                                                                                                                         |
| `burndown-repos-combined` | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `bus-factor`              | companion files                     | `<base>_timeline<ext>`, `<base>_gauge<ext>`, `<base>_subsystems<ext>`                                                                                                                                                                                                                              |
| `couples-files`           | asset directory                     | `files_vocabulary.tsv`, `files_vectors.tsv`, `files_metadata.tsv`                                                                                                                                                                                                                                  |
| `couples-people`          | asset directory                     | `people_vocabulary.tsv`, `people_vectors.tsv`, `people_metadata.tsv`                                                                                                                                                                                                                               |
| `couples-shotness`        | asset directory                     | `shotness_coupling_heatmap.png`, `shotness_coupling_heatmap.svg`, `top_shotness_coupling_pairs.png`, `top_shotness_coupling_pairs.svg`                                                                                                                                                             |
| `devs`                    | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `devs-efforts`            | single file with optional companion | `<output>`, `<base>_productivity_ranking<ext> (only with --devs-efforts-detail)`                                                                                                                                                                                                                   |
| `devs-parallel`           | single file with optional companion | `<output>`, `<base>_concurrency_timeline<ext> (only with --devs-parallel-detail)`                                                                                                                                                                                                                  |
| `hotspot-risk`            | primary plus table                  | `<output>`, `<base>_table.tsv`                                                                                                                                                                                                                                                                     |
| `knowledge-diffusion`     | companion files                     | `<base>_distribution<ext>`, `<base>_silos<ext>`, `<base>_lorenz<ext>`, `<base>_trend<ext> (only with --knowledge-diffusion-detail)`                                                                                                                                                                |
| `languages`               | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `old-vs-new`              | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `onboarding`              | companion files                     | `<base>_rampup<ext>`, `<base>_time-to-first<ext>`, `<base>_authors<ext>`                                                                                                                                                                                                                           |
| `overwrites-matrix`       | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `ownership`               | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `ownership-concentration` | companion files                     | `<base>_timeline<ext>`, `<base>_subsystems<ext>`                                                                                                                                                                                                                                                   |
| `refactoring-proxy`       | single file                         | `<output>`                                                                                                                                                                                                                                                                                         |
| `run-times`               | text by default, optional file      | `<output> (only with --run-times-detail)`                                                                                                                                                                                                                                                          |
| `sentiment`               | asset directory                     | `sentiment-overview.png`, `sentiment-overview.svg`, `sentiment-developers.png`, `sentiment-developers.svg`, `sentiment-languages.png`, `sentiment-languages.svg`                                                                                                                                   |
| `shotness`                | asset directory                     | `shotness.png`, `shotness.svg`                                                                                                                                                                                                                                                                     |
| `temporal-activity`       | companion files                     | `<base>_weekdays_commits<ext>`, `<base>_hours_commits<ext>`, `<base>_months_commits<ext>`, `<base>_weeks_commits<ext>`, `<base>_weekdays_lines<ext>`, `<base>_hours_lines<ext>`, `<base>_months_lines<ext>`, `<base>_weeks_lines<ext>`, `<base>_heatmap_commits<ext>`, `<base>_heatmap_lines<ext>` |

The compatibility aliases are not additional output contracts:

- `burndown` expands to `burndown-project`;
- `couples` expands to `couples-files`, `couples-people`, and `couples-shotness`;
- `all` expands to the documented Python-compatible mode set, not every concrete Go mode.

When `<output>` ends in `.json`, Labours writes one JSON document containing extracted data for all
requested modes instead of the image/TSV assets above. JSON is a renderer interchange format, not
the versioned Hercules analysis schema.

## Warnings, failures, and report inventories

A missing analysis is a warning: the affected mode emits no assets, other modes continue, and the
Labours command succeeds if nothing else fails. An unknown or unimplemented mode, processing error,
or output error is a hard failure and produces a nonzero exit status.

`hercules report` inventories the files actually published, rather than assuming every declared
asset exists. Its `manifest.json` `files` array therefore includes every emitted fan-out and
companion asset plus `report.pb`, `index.html`, and the manifest itself. Mode warnings and failures
are recorded separately. See the [README outcome contract](../README.md#exit-status-warnings-and-failures)
for strict and non-strict publication behavior.
