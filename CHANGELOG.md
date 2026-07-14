# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Log-archive loading from both shapes GitHub serves: the run page's
  "Download log archive" zip and an already-extracted directory, with zip
  detection by magic bytes (extension-less API downloads still open) and
  macOS/AppleDouble litter filtering.
- Line parser for the runner's log grammar: RFC 3339 timestamps (seven
  fractional digits, plus lenient variants), `##[group]`/`##[endgroup]`
  fold timing with lost-endgroup recovery, `##[error]` / `##[warning]` /
  `##[notice]` / `##[command]` counting, step-failure detection from the
  runner's exit-code epitaph, and runner version/image metadata.
- `report` subcommand: a per-job step waterfall with offset/duration
  columns, proportional track bars, failed-step markers, between-step
  overhead accounting, and a `--groups` drill-down into the slowest folds;
  text, stable JSON (`schema_version: 1`), and PR-ready Markdown output.
- `diff` subcommand comparing two runs: name-based job and step matching
  that survives renumbering and duplicate step names, added/removed
  detection, impact-sorted output with a `--min-delta` noise fold, both
  billable job-time and wall-clock totals, and a `--fail-over DUR` budget
  gate that exits 1 on breach.
- `slow` subcommand ranking the slowest steps across all jobs with their
  share of total job time.
- `--job` substring filtering, `--width` waterfall sizing, combined-log
  fallback for archives without per-step files, and documented exit codes
  (0 ok, 1 breach, 2 usage, 3 runtime).
- Runnable example (`examples/make-demo-archive`) that fabricates a
  deterministic base/head archive pair, a CI-gate script
  (`examples/diff-gate.sh`), and a log-format reference
  (`docs/log-format.md`).
- 87 deterministic offline tests (unit + in-process CLI integration against
  fabricated zip archives) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/ciblame/releases/tag/v0.1.0
