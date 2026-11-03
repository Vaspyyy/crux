<div align="center">

# crux

Run a build or test command and see only what matters.

[![Go](https://img.shields.io/github/go-mod/go-version/Vaspyyy/crux?style=flat-square&labelColor=1e1e2e&color=89b4fa&logo=go&logoColor=89b4fa)](go.mod)
[![Release](https://img.shields.io/github/v/tag/Vaspyyy/crux?style=flat-square&labelColor=1e1e2e&color=cba6f7&label=release)](https://github.com/Vaspyyy/crux/tags)
[![License](https://img.shields.io/github/license/Vaspyyy/crux?style=flat-square&labelColor=1e1e2e&color=a6e3a1)](LICENSE)

<img src="assets/hero.gif" alt="cargo test printing 440 lines, then crux cargo test printing a short report" width="900">

</div>

crux is a wrapper for build, test and lint commands. `crux cargo test` runs `cargo test`, waits for it, and prints the verdict, the test counts, and each error or failed test with its file, line and the few lines that explain it. Compile progress, hundreds of passing tests and standard-library stack frames are left out of the report but kept in a log file, so nothing is lost. The exit code is the command's own.

It was written for AI coding agents, which pay for every line of output they read, but it's just as usable at a terminal: there it shows a live status line while the command runs and prints the report in color.

## Install

```sh
go install github.com/Vaspyyy/crux@latest
```

You'll need Go 1.26 or newer (older versions fetch the right toolchain on their own). crux is a single binary with no runtime dependencies. It's developed on Linux and should work on macOS and the BSDs; Windows isn't supported.

## Usage

Put `crux` in front of the command:

```sh
crux cargo test
crux go test ./...
crux python3 -m pytest
crux npx tsc --noEmit
crux make
```

Flags go before the command; everything after the command's name belongs to the command.

| Flag | Effect |
| --- | --- |
| `-b`, `--budget N` | Roughly how many lines the report may use (default 60). |
| `-v`, `--verbose` | Stream the output while the command runs, then print the report. |
| `-r`, `--raw` | Print the whole output, cleaned of escape codes and progress bars, instead of a report. The verdict line goes to stderr. |
| `-A`, `--agent` | Plain report without colors or live status. This is the default when stdout isn't a terminal. |
| `--keep-locale` | Don't switch the command to English messages. |
| `--no-log` | Don't keep the full output on disk. |
| `--log` | Print the path of the latest log from the current directory and exit. |

Short flags can be combined (`crux -rv make`).

## The report

<img src="assets/agent.png" alt="crux report for a failing pytest run in plain agent mode" width="900">

The first line is the verdict, the command, its exit code (or the signal that killed it), how long it took and how much output it produced. For test runs, a `tests:` line follows with the counts from the tool's own summary: passed, failed, errors, skipped and so on. Go without `-v` only reports packages, so crux shows `3/4 packages ok` then rather than inventing a test count.

Errors, failed tests and warnings come next, each with its location. Errors carry the code frame or the frames of a stack trace that are in your code, and failed tests carry the assertion or panic message. When three or more errors are the same (same message, and for Rust the same expected and found types) they're folded into one entry that lists every location, and warnings are folded by kind with every name listed (`unused variable [-Wunused-variable] ×2: ‘unused’, ‘y’`). Tools print paths relative to their own root, so crux looks a location's file up from the current directory, the directories make entered and the parent directories, and rewrites the path so it works from where you are. A location whose file can't be found anywhere is marked, which catches stale caches.

A `notes:` section says when a run did less than it seems to have done: cargo stopping at the first failing test target, a Go test panic (also one in a goroutine) ending the rest of its package, a `-run` filter that matched no test, pytest stopping after a collection error, after `-x` or on Ctrl+C, a package that didn't build.

The budget is soft and has two guarantees: the first error or failed test always keeps its explanation, and every error and failed test gets a line (beyond 30 of them, as many as the budget allows). Code frames of the others and warnings get what's left, and anything cut is announced with a count. Output crux doesn't recognise is shown whole if it's short, or summarised by its error-looking lines and its last lines if it isn't.

The full output is written to `~/.local/state/crux/logs` while the command runs, so it survives a timeout or Ctrl+C. Every run gets its own file (readable only by you), the report ends with its path, and logs are deleted after a week. `crux --log` prints the path of the latest log from the current directory and fails if there isn't one, so `less "$(crux --log)"` never opens the wrong file.

Commands run with `LC_ALL=C.UTF-8` so compiler messages are in English and look the same on every machine. `--keep-locale` turns that off.

## Recognised tools

crux understands the output of cargo and rustc, `go build`, `go test` and `go vet`, pytest and Python tracebacks, the TypeScript compiler, gcc and clang (also through make and ninja), javac, eslint, Node.js errors and npm. Anything else falls back to error-looking lines plus the end of the output, which works reasonably for most tools.

## For coding agents

A ready-made Claude Code skill is in [`skill/SKILL.md`](skill/SKILL.md). Install it with `mkdir -p ~/.claude/skills/crux && cp skill/SKILL.md ~/.claude/skills/crux/`.

An agent can use crux without any setup: when its output isn't a terminal, crux prints the plain report. To make it the default, tell the agent in its instructions file (`AGENTS.md`, `CLAUDE.md` or similar), for example:

```markdown
- Run builds, tests, type checkers and linters through `crux <command>` (for example `crux cargo test`,
  `crux go test ./...`, `crux python3 -m pytest`). It prints the verdict, test counts and the errors or
  failures with their locations, keeps the exit code, and saves the full output to a log file whose path
  it prints. Don't use it for commands that never exit or whose full output you need.
```

crux isn't meant for commands whose whole output is the point (`git diff`, `cat`, `--help`), for interactive programs, or for anything that never exits, such as dev servers and watch modes.

On a set of small broken projects in Rust, Go, Python, TypeScript and C, the raw output of the build and test commands came to about 14,000 tokens and crux's reports to about 1,500, roughly 89% less:

| Command | Raw | crux | Saved |
| --- | ---: | ---: | ---: |
| `cargo test` (400 tests, 2 failing) | 3,993 | 167 | 96% |
| `go test -v ./...` (250 tests, 1 failing) | 7,802 | 98 | 99% |
| `go test ./...` (build error, 2 failing tests) | 487 | 188 | 61% |
| `python3 -m pytest` (3 failing) | 539 | 308 | 43% |
| `tsc --noEmit --pretty` (3 errors) | 435 | 225 | 48% |
| `cargo build` (2 errors, 1 warning) | 320 | 195 | 39% |
| `make` with gcc (3 errors, 2 warnings) | 332 | 294 | 11% |

The savings are large when most of the output is passing tests or progress, and small when a tool already prints little besides its errors. Before release, crux went through three review rounds in which several models diagnosed and fixed the same broken projects with and without it, checked its reports against the raw output and tried to break it; what they found is covered by regression tests built from real tool output.

## License

GPL-3.0. See [LICENSE](LICENSE).
