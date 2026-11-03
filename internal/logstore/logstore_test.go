package logstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParallelRunsGetTheirOwnLogs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now()
	a, err := Open([]string{"sh", "-c", "x"}, "/a", now, "sh -c x")
	if err != nil {
		t.Fatal(err)
	}
	// same pid, same millisecond, same command: still two files
	b, err := Open([]string{"sh", "-c", "x"}, "/b", now, "sh -c x")
	if err != nil || b.Path == a.Path {
		t.Fatalf("second run: %v, paths %s / %v", err, a.Path, b)
	}
	a.Line("first")
	a.Close(0, time.Second)
	data, _ := os.ReadFile(a.Path)
	if !strings.Contains(string(data), "first") || !strings.Contains(string(data), "# cwd: /a") {
		t.Errorf("log content: %q", data)
	}
	if info, _ := os.Stat(a.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLastIsPerDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	mine, _ := Open([]string{"make"}, "/mine", time.Now(), "make")
	mine.Close(0, 0)
	time.Sleep(5 * time.Millisecond)
	other, _ := Open([]string{"make"}, "/other", time.Now(), "make")
	other.Close(0, 0)
	if p, isOther, _ := Last("/mine"); p != mine.Path || isOther {
		t.Errorf("Last(/mine) = %s other=%v, want %s", p, isOther, mine.Path)
	}
	if p, isOther, _ := Last("/elsewhere"); p != other.Path || !isOther {
		t.Errorf("Last(/elsewhere) = %s other=%v, want newest with other=true", p, isOther)
	}
}

func TestLogNames(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for argv, want := range map[string]string{
		"python3 -m pytest":               "-python3-pytest.log",
		"go test -timeout 1s ./...":       "-go-test.log",
		"cargo test --lib -- --nocapture": "-cargo-test.log",
	} {
		l, err := Open(strings.Fields(argv), "/x", time.Now(), argv)
		if err != nil {
			t.Fatal(err)
		}
		l.Close(0, 0)
		if !strings.HasSuffix(l.Path, want) {
			t.Errorf("%s: %s", argv, l.Path)
		}
	}
}
