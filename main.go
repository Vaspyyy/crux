// crux runs a build, test or lint command and shows only what matters:
// the verdict, the counts, and the errors and failures with their locations.
// The full output is kept in a log file.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/logstore"
	"github.com/Vaspyyy/crux/internal/render"
	"github.com/Vaspyyy/crux/internal/runner"
)

const version = "0.1.0"

const usage = `crux runs a build, test or lint command and shows only what matters.

usage: crux [flags] <command> [args...]

  crux cargo test
  crux go test ./...
  crux npm run build
  crux -v make            also stream the full output while it runs

flags:
  -b, --budget N      roughly how many lines the report may use (default 60)
  -v, --verbose       stream the full output live, then print the report
  -r, --raw           print the whole cleaned output instead of a digest
  -A, --agent         plain report without colors or live status (default when not a terminal)
      --keep-locale   don't switch the command to English messages
      --no-log        don't keep the full output on disk
      --log           print the path of the last run's full log and exit
  -h, --help          show this help
      --version       show the version

The exit code is the command's own. Full logs are kept in ` + "~/.local/state/crux/logs" + ` (last 50 runs).
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
			p, err := logstore.Last()
			if err != nil {
				fmt.Fprintln(os.Stderr, "crux:", err)
				return 1
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
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 5 {
				return fail("--budget wants a number of at least 5")
			}
			o.budget = n
		case strings.HasPrefix(a, "--budget="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--budget="))
			if err != nil || n < 5 {
				return fail("--budget wants a number of at least 5")
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
	if _, err := exec.LookPath(argv[0]); err != nil {
		fmt.Fprintf(os.Stderr, "crux: %s: command not found\n", argv[0])
		return 127
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
	command := strings.Join(argv, " ")

	var live *render.Live
	errTTY := term.IsTerminal(int(os.Stderr.Fd()))
	if human && !o.verbose && errTTY {
		live = render.NewLive(os.Stderr, shorten(command, 40), width)
	}
	start := time.Now()
	res, err := runner.Run(argv, env, func(raw string) {
		switch {
		case o.verbose && human:
			os.Stdout.WriteString(raw)
			if !strings.HasSuffix(raw, "\n") {
				os.Stdout.WriteString("\n")
			}
		case live != nil:
			live.Line(digest.Clean(raw))
		}
	})
	if live != nil {
		live.Stop()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "crux:", err)
		return 1
	}

	lines := make([]string, len(res.Lines))
	for i, l := range res.Lines {
		lines[i] = digest.Clean(l)
	}
	logPath := ""
	if !o.noLog {
		if p, err := logstore.Save(argv, lines, res.Exit, res.Duration, start); err == nil {
			logPath = p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		digest.Relative = wd
	}
	r := digest.Digest(lines, res.Exit)
	if o.verbose && human {
		fmt.Println()
	}
	fmt.Print(render.Report(render.Run{
		Command: command, Exit: res.Exit, Signal: res.Signal, Duration: res.Duration, LogPath: logPath, Lines: lines,
	}, r, render.Options{Color: color, Budget: o.budget, Raw: o.raw}))
	return res.Exit
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
