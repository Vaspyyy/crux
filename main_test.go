package main

import (
	"fmt"
	"strings"
	"testing"
)

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
	if plain := helpText(false); strings.Contains(plain, "\x1b") || !strings.Contains(plain, "--budget N") {
		t.Errorf("plain help:\n%s", plain)
	}
	if colored := helpText(true); !strings.Contains(colored, hSection+"USAGE"+hReset) {
		t.Errorf("colored help has no colored section titles:\n%s", colored)
	}
}
