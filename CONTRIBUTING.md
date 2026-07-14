# Contributing to ciblame

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — no services, no tokens, no network.

```bash
git clone https://github.com/JaydenCJ/ciblame && cd ciblame
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, fabricates two deterministic demo log
archives in a temp dir, and asserts on real CLI output across every
subcommand and both archive shapes (zip and extracted directory); it must
finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (87 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsers and diffing never touch the filesystem — only
   `internal/archive` does).

## Ground rules

- Keep dependencies at zero — ciblame is standard library only, and adding
  one needs strong justification in the PR.
- No network calls, ever. ciblame reads files you already downloaded; it
  must keep working on an air-gapped machine. No telemetry.
- The log-archive format knowledge lives in two places only:
  `internal/run/names.go` (filename grammar) and `internal/logparse`
  (line grammar). New observations about the format go there, with a test
  reproducing the real log shape and a note in `docs/log-format.md`.
- Never break `schema_version: 1` JSON output; additive fields only.
- Code comments and doc comments are written in English.
- Determinism first: identical archives must produce byte-identical
  reports, including all orderings.

## Reporting bugs

Include the output of `ciblame version`, the full command you ran, and —
for parse problems — the entry names inside the archive
(`unzip -l run.zip`) plus a few sanitized lines of the affected step log,
since that is exactly what the parser sees.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
