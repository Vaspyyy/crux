---
name: crux
description: Run builds, tests and linters through `crux <command>` (cargo, go test, pytest, tsc, make, gcc, javac, npm …) to get only the verdict, test counts, and errors/failures with file:line locations instead of hundreds of lines of output. The full output is saved to a log file. Use for any command whose output you only need to judge pass/fail and find problems.
---

# crux for agents

`crux <command> [args…]` runs the command, waits, and prints a short report. The exit code is the command's own, so `crux cargo test && …` behaves like `cargo test && …`.

```
FAILED  cargo test  (exit 101, 3.2s, 440 lines of output)
tests: 400 passed, 2 failed

2 failed tests:
  tests::sq_overflow  src/lib.rs:406:32
    in src/lib.rs:tests.sq_overflow
    assertion `left == right` failed: 3 squared
      left: 9
     right: 10

1 warning:
  src/lib.rs:2:46  unused variable: `unused`

full log: /home/you/.local/state/crux/logs/20261103-092646.120-4711-cargo-test.log
```

## When to use it

Use it for builds, test suites, type checkers, linters and scripts where you mainly need to know whether things passed and what failed: `crux cargo test`, `crux go test ./...`, `crux python3 -m pytest`, `crux npx tsc --noEmit`, `crux make`, `crux npm run build`.

Don't use it for commands whose whole output is the point (`cat`, `git diff`, `git log -p`, `jq`), for interactive programs, or for things that never exit (dev servers, `--watch` modes). It waits for the command to finish.

## Shell syntax

crux execs the command directly, with no shell. Verified:

- `crux sh -c 'make && ./run'` for `&&`, `;`, redirects and globs inside the wrapped command. The exit code is the shell's.
- `crux env FOO=1 cargo test`, not `crux FOO=1 cargo test` (that fails with `FOO=1: command not found`, exit 127).
- `crux cargo test | tail` pipes crux's short report, not the raw output. Use `crux -r cargo test | tail` for the raw output.
- Output goes to the report when stdout is not a terminal, so `-A` is not needed.

## Reading the report

- `context: …` as the first line (only in a git repo, only when context marked something): what the marks below are based on, e.g. `context: 1 changed symbol vs HEAD` or `context: focus: commit b681ef1 (recap, 4 min ago)`.
- Then `OK` or `FAILED`, the command, exit code, duration and how long the raw output was.
- `tests:` appears for every recognised test run and sums passed, failed, errors, xpassed, skipped and xfailed, using the tool's own summary. `no tests ran, 1 error` means collection failed. `no tests ran` alone means nothing was collected or a `-run` filter matched nothing. Go without `-v` only reports packages (`3/3 packages ok, 1 package without tests`), so there's no per-test count then.
- `errors:` compiler and runtime errors with `file:line:col` and the code frame (or the frames of a stack trace that are in your code). The count is the true total. When three or more errors are the same (message and, for Rust, the expected/found detail) they are folded: `E0308: mismatched types ×12`, the first one's frame, and `also at` with every other location. Errors outside tests (pytest collection or fixture errors) are listed here, not as failed tests.
- `failed tests:` test name, location, and the assertion or panic message. A Go test that panicked, also from a goroutine it started, is located at its line in the `_test.go` file, with `panicked at file:line` in the message. Go locations include the package folder.
- `in <ref>` under an error or failure: the innermost function, method or type containing its location, as a ref (`internal/group/group.go:multiline`; `Repo.Open` for a method, `parse#2` for the second of two definitions with one name, paths relative to the repo root). It means the line lies inside that definition (outline rules, not a compiler). `test <ref>` names the failing test's own definition when it's a different symbol (the assertion is in a helper); a failure without a location gets `in <test ref>`. Both go as is to seek (`seek <ref>`: definition and callers) and recap (`recap --show <ref>`). No line for files outside the repo, lines outside any definition, or warnings.
- Marks after an `in` or `test` ref: `[changed]` (the symbol has uncommitted changes), `[branch]` (changed in a commit on this branch), `[focus]` (changed in the commit or range recap last showed). An error inside your own change is usually the place to look first. No mark only means the line isn't inside a changed symbol; a change elsewhere can still be behind it.
- `next:` lines at the bottom (at most three): commands to run exactly as printed, with the reason. `recap --show <ref>` shows the change to a marked symbol an error or failure is in, a failed test that changed, or a changed symbol whose name appears in the failed test or in the symbol an error is in ("mentions": the name, and for a method its type's name, is in the text, which doesn't prove it's called there). `seek <test ref>` shows the first failed test's definition. A clean pass has none.
- `warnings:` folded by kind, with every name and location: `unused variable [-Wunused-variable] ×2: ‘unused’, ‘y’` / `also at main.c:4:9`.
- Locations work from the current directory: paths a tool printed relative to its own root (a cargo workspace, `make -C`) are rewritten (`../main.c:4:20`). `(file not found from here)` means the file exists nowhere crux looked, usually a stale cache.
- `notes:` things that make a run less complete than it looks: cargo stopping at the first failing test target, a Go panic ending the rest of its package, a `-run` filter matching nothing, pytest stopping after collection errors, `-x` or Ctrl+C, or a package that failed to build. If there are notes, read them before trusting the counts.
- The budget is soft. The first error or failure always keeps its explanation and `in` line, and every error and failure gets a line (beyond 30, as many as fit); anything cut is announced as `… and N more errors not shown`.
- `last lines:` the true end of the output, shown when nothing structured explains a failure. For successful non-test commands the output (or its end) is shown.
- Unrecognised output of 20 lines or fewer is printed whole under the verdict line. Lines longer than 300 characters are clipped with `…[+N chars]`, keeping a location at the end of the line.
- Recognised: cargo/rustc, go build/test/vet, pytest and Python tracebacks, tsc, gcc/clang, make, javac, eslint, Node errors, npm. Anything else falls back to error-looking lines plus the end of the output.

## Getting more

- The full output is in the log printed at the bottom (an absolute path; omitted when the whole output is already shown). It's written while the command runs, so it survives a timeout. `crux --log` prints the path of the latest run started in the current directory, and fails (exit 1, nothing on stdout) if there is none. Logs are kept for a week.
- `crux -b 150 <cmd>` allows a longer report; `crux -r <cmd>` prints only the whole cleaned output on stdout (no escape codes, progress bars resolved; the verdict line goes to stderr); `crux -v <cmd>` streams the cleaned output while it runs and then prints the report. Short flags combine: `crux -rv …`.
- `crux --json <cmd>` prints one JSON document instead (Prism's `report.Crux`, https://github.com/Vaspyyy/prism/blob/main/docs/output.md) for scripts and CI. Read the text report yourself: it's shorter and says the same.
- Every run, pass or fail, is recorded in the repo's git dir (command, exit code, failed tests and the symbols errors are in), per worktree and agent session, for an hour. A run that was interrupted (Ctrl+C) or killed by a signal isn't, so the previous record stays. seek and recap read it and mark those symbols `[failing]`, so running tests through crux tells the other tools what's broken. `--no-context` (or `PRISM_NO_CONTEXT=1`) reads and records nothing, and drops the marks, the `context:` line and the `recap` suggestions; nothing else in the report changes.
- crux runs the command with an English locale (`LC_ALL=C.UTF-8`) so messages are consistent; `--keep-locale` turns that off.
