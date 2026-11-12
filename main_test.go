package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain turns context off: crux's reports then depend only on their
// arguments and files, as the golden tests expect. The context tests turn
// it back on.
func TestMain(m *testing.M) {
	os.Setenv("PRISM_NO_CONTEXT", "1")
	os.Exit(m.Run())
}

func TestSplitShortFlags(t *testing.T) {
	for in, want := range map[string]string{
		"-rv make -rv":     "[-r -v make -rv]",
		"-vb 30 go test":   "[-v -b 30 go test]",
		"-b 30 -rA ls":     "[-b 30 -r -A ls]",
		"--raw -- -rv":     "[--raw -- -rv]",
		"-x make":          "[-x make]",
		"--budget 9 -v ls": "[--budget 9 -v ls]",
	} {
		if got := fmt.Sprint(splitShortFlags(fields(in))); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

func fields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + " " {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	return out
}

func TestBudgetFlagMustComeLastInAGroup(t *testing.T) {
	if got := fmt.Sprint(splitShortFlags(fields("-bv 10 echo"))); got != "[-bv 10 echo]" {
		t.Errorf("got %s", got)
	}
}

func TestHelpColorsOnlyWhenAsked(t *testing.T) {
	if plain := helpText(false); strings.Contains(plain, "\x1b") || !strings.Contains(plain, "--budget N") || !strings.Contains(plain, "--no-context") {
		t.Errorf("plain help:\n%s", plain)
	}
	if colored := helpText(true); !strings.Contains(colored, hSection+"USAGE"+hReset) {
		t.Errorf("colored help has no colored section titles:\n%s", colored)
	}
}

// The tools agree on -V for the version; -v is crux's --verbose, which
// needs a command, and says so instead of printing the help.
func TestVersionAndVerboseWithoutCommand(t *testing.T) {
	for _, flag := range []string{"-V", "--version"} {
		if code, out, _ := capture(t, flag); code != 0 || string(out) != "crux "+version+"\n" {
			t.Errorf("crux %s: exit %d, out %q", flag, code, out)
		}
	}
	code, out, errOut := capture(t, "-v")
	if want := "crux: -v needs a command: crux -v <command> (the version is crux -V)\n"; code != 2 || len(out) != 0 || errOut != want {
		t.Errorf("crux -v: exit %d, out %q, err %q, want %q", code, out, errOut, want)
	}
	if code, _, errOut := capture(t, "--bogus"); code != 2 || errOut != "crux: unknown flag --bogus (flags go before the command; see crux --help)\n" {
		t.Errorf("crux --bogus: exit %d, err %q", code, errOut)
	}
}
