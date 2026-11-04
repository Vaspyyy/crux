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

const version = "0.3.1"

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
	args = splitShortFlags(args)
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
			fmt.Print(helpText(wantsColor(os.Stdout)))
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
				// never hand a script another directory's log
				fmt.Fprintln(os.Stderr, "crux: no crux run in this directory yet")
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
		fmt.Fprint(os.Stderr, helpText(wantsColor(os.Stderr)))
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
		log, _ = logstore.Open(argv, cwd, start, render.CommandFull(argv)) // no log is not a reason to fail the run
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
	digest.CheckFiles = true
	digest.GoModule, digest.GoModuleDir = goModule(cwd)
	r := digest.Digest(lines, res.Exit)
	if o.verbose {
		fmt.Println()
	}
	rr := render.Run{Argv: argv, Exit: res.Exit, Signal: res.Signal, Duration: res.Duration, LogPath: logPath, Lines: lines}
	if o.raw { // stdout is the cleaned output and nothing else
		fmt.Fprint(os.Stderr, render.Header(rr, color && term.IsTerminal(int(os.Stderr.Fd()))))
	}
	fmt.Print(render.Report(rr, r, render.Options{Color: color, Budget: o.budget, Raw: o.raw}))
	return res.Exit
}

// splitShortFlags turns "-rv" into "-r", "-v" (before the command only).
func splitShortFlags(args []string) []string {
	var out []string
	for i, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			return append(out, args[i:]...)
		}
		// -b takes a value, so it may only come last in a group ("-vb 30")
		if strings.HasPrefix(a, "--") || len(a) < 3 || strings.Trim(a[1:], "vrAhb") != "" || strings.Contains(a[1:len(a)-1], "b") {
			out = append(out, a)
			if a == "-b" || a == "--budget" { // its value isn't the command
				if i+1 < len(args) {
					out = append(out, args[i+1])
					return append(out, splitShortFlags(args[i+2:])...)
				}
			}
			continue
		}
		for _, c := range a[1:] {
			out = append(out, "-"+string(c))
		}
		if strings.HasSuffix(a, "b") && i+1 < len(args) {
			out = append(out, args[i+1])
			return append(out, splitShortFlags(args[i+2:])...)
		}
	}
	return out
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

// wantsColor reports whether text written to f may be colored.
func wantsColor(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""
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
