package runner

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestRunKeepsOrderAndExitCode(t *testing.T) {
	res, err := Run([]string{"sh", "-c", "echo one; echo two >&2; echo three; exit 3"}, os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Lines, ""); got != "one\ntwo\nthree\n" {
		t.Errorf("lines = %q", got)
	}
	if res.Exit != 3 {
		t.Errorf("exit = %d, want 3", res.Exit)
	}
}

func TestRunReportsSignals(t *testing.T) {
	res, err := Run([]string{"sh", "-c", "kill -TERM $$"}, os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 128+15 || res.Signal != syscall.SIGTERM {
		t.Errorf("exit = %d signal = %q", res.Exit, res.Signal)
	}
}

func TestRunPartialLastLine(t *testing.T) {
	res, _ := Run([]string{"printf", "no newline"}, os.Environ(), nil)
	if len(res.Lines) != 1 || res.Lines[0] != "no newline" {
		t.Errorf("lines = %q", res.Lines)
	}
}

// Ctrl+C reaches crux (here: this process), which passes it on. A command
// that catches it and exits normally still ran only partly.
func TestRunReportsInterrupts(t *testing.T) {
	res, err := Run([]string{"sh", "-c", `trap 'exit 2' INT; sleep 0.3; kill -INT $PPID; sleep 5`}, os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted || res.Signal != 0 || res.Exit != 2 {
		t.Errorf("interrupted %v, signal %v, exit %d", res.Interrupted, res.Signal, res.Exit)
	}
	if res, _ := Run([]string{"true"}, os.Environ(), nil); res.Interrupted {
		t.Error("a plain run counts as interrupted")
	}
}
