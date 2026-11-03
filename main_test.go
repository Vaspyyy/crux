package main

import (
	"fmt"
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
