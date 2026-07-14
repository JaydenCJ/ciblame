# The GitHub Actions log archive format

Everything ciblame knows about the archive, in one place. This is
reverse-engineered from real downloads; GitHub does not document the layout.
When the runner changes something, this file and the parsers
(`internal/run/names.go`, `internal/logparse`) change together.

## Getting an archive

Any of these produce the same zip, no marketplace app or dashboard needed:

```bash
# Web UI: run page → ⋯ menu → "Download log archive"

# GitHub CLI (needs repo read access only):
gh api /repos/OWNER/REPO/actions/runs/RUN_ID/logs > run.zip

# Plain REST:
curl -sL -H "Authorization: Bearer $TOKEN" \
  https://api.github.com/repos/OWNER/REPO/actions/runs/RUN_ID/logs > run.zip
```

From that point on, ciblame is fully offline. It accepts the zip itself or
the directory you get from `unzip run.zip -d rundir`. Zip detection is by
the `PK\x03\x04` magic, so an extension-less `> logs` download also opens.

## Layout

```text
run.zip
├── 0_build.txt                        # combined log, one per job, index-prefixed
├── 1_lint (ubuntu-latest).txt
├── build/                             # one directory per job
│   ├── 1_Set up job.txt               # one file per step, step-number-prefixed
│   ├── 2_Run actions_checkout@v4.txt
│   └── 9_Post Run actions_checkout@v4.txt
└── lint (ubuntu-latest)/
    └── …
```

Rules ciblame applies:

- **`N_name.txt`** — leading digits, one underscore, then the display name.
  Only the *first* underscore separates; the name keeps its own.
- Characters illegal in filenames (`/`, `:`, `"`, …) were replaced with `_`
  by GitHub, which is why `actions/checkout` reads `actions_checkout`.
  ciblame keeps sanitized names verbatim — reversing would be guessing.
- Top-level files are combined per-job logs; directories hold per-step
  files. Steps are the better clock, so the combined log is used only when
  a job has no step directory (`log_only` in JSON output).
- `__MACOSX/`, `.DS_Store`, `._*` and paths nested deeper than one
  directory are ignored; anything else unrecognized is counted in
  `skipped_entries` rather than failing the load.

## Line grammar

Every line the runner writes is timestamp-prefixed:

```text
2026-07-01T10:00:12.3456789Z ##[group]Run actions/checkout@v4
2026-07-01T10:00:12.4456789Z [command]/usr/bin/git version
2026-07-01T10:00:14.1234567Z ##[endgroup]
```

- Timestamps are RFC 3339 UTC with seven fractional digits. ciblame also
  accepts fewer digits and numeric offsets (re-serialized logs), and
  ignores lines without a leading timestamp (re-flowed multi-line output).
- A step's duration is **last timestamp minus first timestamp** of its
  file. The runner writes a line at step start and step end, so this
  matches the UI's per-step timing to sub-second precision.
- `##[group]…##[endgroup]` folds are timed individually (the `--groups`
  drill-down). An unclosed fold ends at the file's last timestamp; a lost
  `endgroup` is closed by the next fold so time is never double-counted.
- `##[error]`, `##[warning]`, `##[notice]`, and `##[command]` (also the
  bare `[command]` echo) are counted per step. Only the runner's
  `##[error]Process completed with exit code N.` marks a step *failed* —
  annotation errors alone do not.
- The `Set up job` step leaks useful metadata ciblame surfaces per job:
  `Current runner version: '…'` and the `Image:` / `Version:` lines inside
  the `Runner Image` fold.

## What the archive cannot tell you

Honesty section. The archive has no queue times (time before `Set up job`
is invisible), no per-job billing multipliers (macOS minutes cost more —
ciblame reports raw seconds), and skipped steps simply have no file. For
those you need the run metadata API — which is exactly the kind of
online dependency ciblame refuses to take in v0.1.0.
