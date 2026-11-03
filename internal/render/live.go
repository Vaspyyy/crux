package render

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	livePass = regexp.MustCompile(`^test .* \.\.\. ok$|^\s*--- PASS: |^ok\s+\S`)
	liveFail = regexp.MustCompile(`\.\.\. FAILED$|^\s*--- FAIL: |^FAILED |^FAIL\s+\S`)
	liveErr  = regexp.MustCompile(`^error(\[E\d+\])?: |:\d+(:\d+)?: (fatal )?error: | - error TS\d+|\): error TS\d+`)
	liveWarn = regexp.MustCompile(`^warning: |:\d+(:\d+)?: warning: `)
	// cargo's own summaries repeat the count of diagnostics already seen
	liveSummary = regexp.MustCompile(`^(warning|error): .*(generated \d+ warnings?|warnings? emitted|could not compile|test failed, to rerun|aborting due to)`)
	livePyOK    = regexp.MustCompile(`^\S+\.py ([.FEsxX]+)`)
)

// Live is a one-line status shown while the command runs (people only).
type Live struct {
	w       io.Writer
	command string
	start   time.Time
	mu      sync.Mutex
	last    string
	pass    int
	fail    int
	errs    int
	warns   int
	stop    chan struct{}
	done    sync.WaitGroup
	width   int
}

func NewLive(w io.Writer, command string, width int) *Live {
	l := &Live{w: w, command: command, start: time.Now(), stop: make(chan struct{}), width: width}
	l.done.Add(1)
	go l.loop()
	return l
}

// Line feeds one cleaned output line.
func (l *Live) Line(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case livePass.MatchString(s):
		l.pass++
	case liveFail.MatchString(s):
		l.fail++
	case liveSummary.MatchString(s):
	case liveErr.MatchString(s):
		l.errs++
	case liveWarn.MatchString(s):
		l.warns++
	}
	if m := livePyOK.FindStringSubmatch(s); m != nil {
		l.pass += strings.Count(m[1], ".")
		l.fail += strings.Count(m[1], "F") + strings.Count(m[1], "E")
	}
	if t := strings.TrimSpace(s); t != "" {
		l.last = t
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (l *Live) loop() {
	defer l.done.Done()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	frame := 0
	for {
		select {
		case <-l.stop:
			fmt.Fprint(l.w, "\r\x1b[K")
			return
		case <-t.C:
			l.draw(frame)
			frame++
		}
	}
}

func (l *Live) draw(frame int) {
	l.mu.Lock()
	el := fmtDuration(time.Since(l.start))
	var counts []string
	if l.pass > 0 {
		counts = append(counts, fmt.Sprintf("\x1b[32m✓ %d\x1b[0m", l.pass))
	}
	if l.fail > 0 {
		counts = append(counts, fmt.Sprintf("\x1b[31m✗ %d\x1b[0m", l.fail))
	}
	if l.errs > 0 {
		counts = append(counts, "\x1b[31m"+plural(l.errs, "error")+"\x1b[0m")
	}
	if l.warns > 0 {
		counts = append(counts, "\x1b[33m"+plural(l.warns, "warning")+"\x1b[0m")
	}
	last := l.last
	l.mu.Unlock()

	head := fmt.Sprintf("\x1b[35m%s\x1b[0m \x1b[1m%s\x1b[0m \x1b[2m%s\x1b[0m", spinner[frame%len(spinner)], l.command, el)
	if len(counts) > 0 {
		head += "  " + strings.Join(counts, " ")
	}
	visible := len([]rune(stripANSI(head)))
	room := l.width - visible - 4
	if room > 10 && last != "" {
		r := []rune(last)
		if len(r) > room {
			r = append(r[:room-1], '…')
		}
		head += "  \x1b[2m" + string(r) + "\x1b[0m"
	}
	fmt.Fprint(l.w, "\r\x1b[K"+head)
}

// Stop clears the status line.
func (l *Live) Stop() {
	close(l.stop)
	l.done.Wait()
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }
