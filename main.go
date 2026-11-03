// crux runs a build, test or lint command and shows only what matters:
// the verdict, the counts, and the errors and failures with their locations.
// The full output is kept in a log file.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/logstore"
	"github.com/Vaspyyy/crux/internal/render"
	"github.com/Vaspyyy/crux/internal/runner"
)

const version = "0.2.0"

const usage = `crux runs a build, test or lint command and shows only what matters.

usage: crux [flags] <command> [args...]

  crux cargo test
  crux go test ./...
  crux npm run build
  crux -v make            also stream the full output while it runs

flags:
  -b, --budget N      roughly how many lines the report may use (default 60)
  -v, --verbose       stream the full output while it runs, then print the report
  -r, --raw           print the whole cleaned output instead of a digest
  -A, --agent         plain report without colors or live status (default when not a terminal)
      --keep-locale   don't switch the command to English messages
      --no-log        don't keep the full output on disk
      --log           print the path of the last run's full log in this directory and exit
  -h, --help          show this help
      --version       show the version

The exit code is the command's own. Commands run with English messages (LC_ALL=C.UTF-8) unless
--keep-locale is given. Full logs are kept in ` + "~/.local/state/crux/logs" + ` for a week.
`

type opts struct {
	budget     int
	verbose    bool
	raw        bool
	agent      bool
	keepLocale bool
	noLog      bool
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	o := opts{budget: 60}
	var argv []string
	// flags only before the command: everything after it belongs to the command
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			argv = args[i:]
			break
		}
		switch {
		case a == "--":
			argv = args[i+1:]
			i = len(args)
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return 0
		case a == "--version":
			fmt.Println("crux", version)
			return 0
		case a == "--log":
			cwd, _ := os.Getwd()
			p, other, err := logstore.Last(cwd)
			if err != nil {
				fmt.Fprintln(os.Stderr, "crux:", err)
				return 1
			}
			if other {
				fmt.Fprintln(os.Stderr, "crux: no run in this directory yet; this is the newest log from anywhere")
			}
			fmt.Println(p)
			return 0
		case a == "-v" || a == "--verbose":
			o.verbose = true
		case a == "-r" || a == "--raw":
			o.raw = true
		case a == "-A" || a == "--agent":
			o.agent = true
		case a == "--keep-locale":
			o.keepLocale = true
		case a == "--no-log":
			o.noLog = true
		case a == "-b" || a == "--budget":
			if i+1 >= len(args) {
				return fail("--budget needs a number")
			}
			i++
			n, msg := budgetValue(args[i])
			if msg != "" {
				return fail(msg)
			}
			o.budget = n
		case strings.HasPrefix(a, "--budget="):
			n, msg := budgetValue(strings.TrimPrefix(a, "--budget="))
			if msg != "" {
				return fail(msg)
			}
			o.budget = n
		default:
			return fail("unknown flag " + a + " (flags go before the command; see crux --help)")
		}
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if code, msg := checkCommand(argv[0]); code != 0 {
		fmt.Fprintf(os.Stderr, "crux: %s: %s\n", argv[0], msg)
		return code
	}

	tty := term.IsTerminal(int(os.Stdout.Fd()))
	human := tty && !o.agent
	color := human && os.Getenv("NO_COLOR") == ""
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	env := os.Environ()
	if !o.keepLocale {
		env = append(env, "LC_ALL=C.UTF-8", "LANG=C.UTF-8", "LANGUAGE=en")
	}
	command := render.Command(argv)
	cwd, _ := os.Getwd()

	var live *render.Live
	errTTY := term.IsTerminal(int(os.Stderr.Fd()))
	if human && !o.verbose && errTTY {
		live = render.NewLive(os.Stderr, shorten(command, 40), width)
	}
	start := time.Now()
	var log *logstore.Log
	if !o.noLog {
		log, _ = logstore.Open(argv, cwd, start, command) // no log is not a reason to fail the run
	}
	var lines []string
	res, err := runner.Run(argv, env, func(raw string) {
		clean := digest.Clean(raw)
		lines = append(lines, clean)
		if log != nil {
			log.Line(clean)
		}
		switch {
		case o.verbose && human:
			os.Stdout.WriteString(raw)
			if !strings.HasSuffix(raw, "\n") {
				os.Stdout.WriteString("\n")
			}
		case o.verbose:
			fmt.Println(clean) // agents get the cleaned stream
		case live != nil:
			live.Line(clean)
		}
	})
	if live != nil {
		live.Stop()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "crux:", err)
		return 1
	}
	logPath := ""
	if log != nil {
		log.Close(res.Exit, res.Duration)
		logPath = log.Path
	}

	digest.Relative = cwd
	digest.GoModule, digest.GoModuleDir = goModule(cwd)
	r := digest.Digest(lines, res.Exit)
	if o.verbose {
		fmt.Println()
	}
	fmt.Print(render.Report(render.Run{
		Argv: argv, Exit: res.Exit, Signal: res.Signal, Duration: res.Duration, LogPath: logPath, Lines: lines,
	}, r, render.Options{Color: color, Budget: o.budget, Raw: o.raw}))
	return res.Exit
}

func budgetValue(s string) (int, string) {
	n, err := strconv.Atoi(s)
	switch {
	case err != nil:
		return 0, "--budget wants a number of lines, got " + strconv.Quote(s)
	case n < 5:
		return 0, "--budget must be at least 5 lines"
	}
	return n, ""
}

// checkCommand explains why a command can't run, with the shell's exit codes.
func checkCommand(name string) (int, string) {
	if _, err := exec.LookPath(name); err == nil {
		return 0, ""
	}
	if strings.Contains(name, "/") {
		st, err := os.Stat(name)
		switch {
		case err != nil:
			return 127, "no such file"
		case st.IsDir():
			return 126, "is a directory"
		default:
			return 126, "permission denied (not executable)"
		}
	}
	return 127, "command not found"
}

// goModule finds the enclosing go.mod: its module path and its directory
// relative to cwd. Go test prints only file names; this gives them a folder.
func goModule(cwd string) (string, string) {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
					rel, err := filepath.Rel(cwd, dir)
					if err != nil {
						return "", ""
					}
					return f[1], filepath.ToSlash(rel)
				}
			}
			return "", ""
		}
		if filepath.Dir(dir) == dir {
			return "", ""
		}
	}
}

func fail(msg string) int {
	fmt.Fprintln(os.Stderr, "crux:", msg)
	return 2
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
