# ciblame examples

Two runnable examples, both offline and self-contained.

## make-demo-archive

Fabricates `demo/base.zip` and `demo/head.zip`, shaped exactly like real
"Download log archive" zips: combined `N_job.txt` logs, per-step
`job/N_step.txt` files, seven-digit RFC 3339 timestamps, `##[group]` folds,
`[command]` echoes, and runner metadata. The head run recreates the classic
mystery — CI got about four minutes slower — via a bloated module cache, a
slower test step, and a new coverage step. The source doubles as a compact,
executable description of the archive format.

```bash
go run ./examples/make-demo-archive demo
ciblame report demo/base.zip
ciblame diff demo/base.zip demo/head.zip
```

## diff-gate.sh

Shows `ciblame diff --fail-over` as a local regression gate: it exits
non-zero when total job time between two archives grows past the budget
your team agreed on, so it can back a release checklist or any local
automation.

```bash
bash examples/diff-gate.sh demo/base.zip demo/head.zip 60s; echo "exit: $?"
```

Both examples pin every timestamp, so their output is identical on every
machine.
