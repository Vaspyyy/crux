// Package logstore keeps the full output of recent runs on disk.
package logstore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	keepFor = 7 * 24 * time.Hour
	keepMax = 500
)

func Dir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "crux", "logs")
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Log is a run's output, written while the command runs, so a run that is
// killed still leaves its output behind.
type Log struct {
	Path string
	f    *os.File
	w    *bufio.Writer
}

// Open creates a new, uniquely named log for argv run in cwd.
func Open(argv []string, cwd string, start time.Time, command string) (*Log, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	prune(dir)
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
	// milliseconds and the pid keep parallel runs from sharing a file
	base := fmt.Sprintf("%s-%d-%s", start.Format("20060102-150405.000"), os.Getpid(), slug)
	var f *os.File
	var err error
	for k := 1; k < 100; k++ {
		name := base + ".log"
		if k > 1 {
			name = fmt.Sprintf("%s-%d.log", base, k)
		}
		f, err = os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if !os.IsExist(err) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	l := &Log{Path: f.Name(), f: f, w: bufio.NewWriterSize(f, 64<<10)}
	fmt.Fprintf(l.w, "$ %s\n# cwd: %s\n# started: %s\n\n", command, cwd, start.Format(time.RFC3339))
	return l, nil
}

func (l *Log) Line(s string) {
	l.w.WriteString(s)
	l.w.WriteByte('\n')
}

func (l *Log) Close(exit int, d time.Duration) {
	fmt.Fprintf(l.w, "\n# exit %d after %s\n", exit, d.Round(time.Millisecond))
	l.w.Flush()
	l.f.Close()
}

func logs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".log") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out) // names start with a timestamp
	return out
}

// prune removes logs older than a week, and the oldest beyond keepMax.
func prune(dir string) {
	names := logs(dir)
	cutoff := time.Now().Add(-keepFor)
	for i, n := range names {
		info, err := os.Stat(filepath.Join(dir, n))
		if err == nil && (info.ModTime().Before(cutoff) || len(names)-i > keepMax) {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// Last returns the most recent log of a run started in cwd. If there is
// none, it returns the most recent log overall and other=true.
func Last(cwd string) (path string, other bool, err error) {
	dir := Dir()
	names := logs(dir)
	if len(names) == 0 {
		return "", false, fmt.Errorf("no crux logs yet (they go to %s)", dir)
	}
	for i := len(names) - 1; i >= 0; i-- {
		p := filepath.Join(dir, names[i])
		if logCwd(p) == cwd {
			return p, false, nil
		}
	}
	return filepath.Join(dir, names[len(names)-1]), true, nil
}

func logCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 3 && sc.Scan(); i++ {
		if c, ok := strings.CutPrefix(sc.Text(), "# cwd: "); ok {
			return c
		}
	}
	return ""
}
