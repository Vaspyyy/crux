package digest

import (
	"regexp"
	"strings"
)

// Lines that are progress or ceremony, never the reason something failed.
var noiseRes = []*regexp.Regexp{
	// cargo / rustc
	regexp.MustCompile(`^\s+(Compiling|Checking|Downloading|Downloaded|Fresh|Updating|Locking|Adding|Blocking|Packaging|Verifying|Archiving|Documenting|Installing|Installed|Replacing|Removing|Running|Finished|Doc-tests|Waiting|Building|Scraping|Unpacking)\b`),
	regexp.MustCompile(`^running \d+ tests?$`),
	regexp.MustCompile(`^(Some errors have detailed explanations|For more information about (this|an) error)`),
	regexp.MustCompile("^note: run with `RUST_BACKTRACE"),
	// go
	regexp.MustCompile(`^=== (RUN|PAUSE|CONT|NAME)\s`),
	regexp.MustCompile(`^go: downloading `),
	// python / pip
	regexp.MustCompile(`^(Requirement already satisfied|Collecting|Using cached|Downloading|Installing collected packages|Successfully installed|Obtaining|Building wheel|Created wheel|Stored in directory|Preparing metadata|Getting requirements)\b`),
	// js
	regexp.MustCompile(`^(added|removed|changed|audited|up to date)\b.*\bpackages?\b`),
	regexp.MustCompile(`^(found 0 vulnerabilities|\d+ packages? (are|is) looking for funding|  run .npm fund. for details)`),
	regexp.MustCompile(`^Node\.js v\d`),
	regexp.MustCompile(`^> \S+@\S+ \S+`), // npm run banner
	// make
	regexp.MustCompile(`^g?make(\[\d+\])?: (Entering|Leaving) directory`),
	// separators
	regexp.MustCompile(`^[=\-_*~#.]{4,}$`),
}

func isNoise(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return true
	}
	for _, re := range noiseRes {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}
