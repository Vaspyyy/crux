// Package logstore keeps the full output of recent runs on disk.
package logstore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const keep = 50

func Dir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "crux", "logs")
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Save writes the cleaned output with a small header and returns the path.
func Save(argv []string, lines []string, exit int, d time.Duration, start time.Time) (string, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// a readable name: the command and its first plain words, no flags or paths
	var words []string
	for i, w := range argv {
		if i > 0 && (strings.HasPrefix(w, "-") || strings.ContainsAny(w, "/.")) {
			continue
		}
		words = append(words, filepath.Base(w))
		if len(words) == 3 {
			break
		}
	}
	slug := strings.Trim(unsafe.ReplaceAllString(strings.Join(words, "-"), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	path := filepath.Join(dir, start.Format("20060102-150405")+"-"+slug+".log")
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\n# exit %d after %s, %s\n\n", strings.Join(argv, " "), exit, d.Round(time.Millisecond), start.Format(time.RFC3339))
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	prune(dir)
	return path, nil
}

func prune(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var logs []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	sort.Strings(logs) // names start with a timestamp
	for len(logs) > keep {
		os.Remove(filepath.Join(dir, logs[0]))
		logs = logs[1:]
	}
}

// Last returns the most recent log's path.
func Last() (string, error) {
	ents, err := os.ReadDir(Dir())
	if err != nil {
		return "", err
	}
	var logs []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) == 0 {
		return "", fmt.Errorf("no logs yet in %s", Dir())
	}
	sort.Strings(logs)
	return filepath.Join(Dir(), logs[len(logs)-1]), nil
}
