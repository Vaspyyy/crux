package main

import (
	"fmt"
	"strings"
)

// help colors, the same 256-color palette as glint's help
const (
	hTitle   = "\x1b[38;5;183;1m"
	hDim     = "\x1b[38;5;59m"
	hTagline = "\x1b[38;5;146;3m"
	hSection = "\x1b[38;5;111;1m"
	hShort   = "\x1b[38;5;151m"
	hLong    = "\x1b[38;5;117m"
	hText    = "\x1b[38;5;189m"
	hReset   = "\x1b[0m"
)

type helpFlag struct{ short, long, arg, text string }

type helpPair struct{ name, text string }

var helpSections = []struct {
	title string
	flags []helpFlag
}{
	{"REPORT", []helpFlag{
		{"-b", "--budget", "N", "roughly how many lines the report may use (default 60)"},
		{"-v", "--verbose", "", "stream the full output while it runs, then print the report"},
		{"-r", "--raw", "", "print the whole cleaned output instead of a digest"},
		{"-A", "--agent", "", "plain report without colors or live status (default when not a terminal)"},
	}},
	{"RUNNING", []helpFlag{
		{"", "--keep-locale", "", "don't switch the command to English messages"},
		{"", "--no-log", "", "don't keep the full output on disk"},
		{"", "--log", "", "print the path of the last run's full log in this directory and exit"},
	}},
	{"OTHER", []helpFlag{
		{"-h", "--help", "", "show this help"},
		{"", "--version", "", "show the version"},
	}},
}

var helpExamples = []helpPair{
	{"crux cargo test", "only the failing tests, with file:line"},
	{"crux go test ./...", "counts, failures and build errors"},
	{"crux npm run build", "compiler errors without the noise"},
	{"crux -v make", "also stream the full output while it runs"},
	{"crux --log", "where the last full log in this folder is"},
}

var helpNotes = []helpPair{
	{"exit code", "the command's own"},
	{"locale", "commands run with LC_ALL=C.UTF-8 unless --keep-locale is given"},
	{"logs", "~/.local/state/crux/logs, kept for a week"},
	{"NO_COLOR", "set it to turn colors off"},
}

// helpText renders the help, colored like glint's when color is true.
func helpText(color bool) string {
	paint := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return code + s + hReset
	}
	const flagWidth = 24 // "-b, --budget N" and friends, padded to one column
	var b strings.Builder
	section := func(title string) {
		fmt.Fprintf(&b, "\n  %s\n", paint(hSection, title))
	}
	pairs := func(ps []helpPair, w int, nameColor, textColor string) {
		for _, p := range ps {
			w = max(w, len(p.name))
		}
		for _, p := range ps {
			pad := strings.Repeat(" ", w-len(p.name)+2)
			fmt.Fprintf(&b, "    %s%s%s\n", paint(nameColor, p.name), pad, paint(textColor, p.text))
		}
	}

	fmt.Fprintf(&b, "\n  %s %s  %s\n", paint(hTitle, "✦ crux"), paint(hDim, "v"+version),
		paint(hTagline, "builds and tests, minus the noise"))

	section("USAGE")
	fmt.Fprintf(&b, "    crux %s %s %s\n", paint(hDim, "[flags]"), paint(hLong, "<command>"), paint(hDim, "[args...]"))
	fmt.Fprintf(&b, "    %s\n", paint(hDim, "flags go before the command; everything after it belongs to the command"))

	for _, s := range helpSections {
		section(s.title)
		for _, f := range s.flags {
			left, plain := "    ", "    "
			if f.short != "" {
				left = paint(hShort, f.short) + paint(hDim, ", ")
			}
			left += paint(hLong, f.long)
			plain += f.long
			if f.arg != "" {
				left += " " + paint(hDim, f.arg)
				plain += " " + f.arg
			}
			fmt.Fprintf(&b, "    %s%s%s\n", left, strings.Repeat(" ", max(flagWidth-len(plain), 2)), paint(hText, f.text))
		}
	}

	section("EXAMPLES")
	pairs(helpExamples, flagWidth-2, hLong, hDim) // descriptions line up with the flags'
	section("GOOD TO KNOW")
	pairs(helpNotes, 0, hLong, hDim)
	b.WriteString("\n")
	return b.String()
}
