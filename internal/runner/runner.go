// Package runner executes a command and captures its combined output.
package runner

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

type Result struct {
	Lines    []string // raw lines (stdout and stderr interleaved as written)
	Exit     int
	Duration time.Duration
	Signal   syscall.Signal // non-zero if the command was killed by a signal
}

// Run starts argv, calls onLine for every line as it arrives, and waits.
// stdout and stderr share one pipe, so their order is preserved.
func Run(argv []string, env []string, onLine func(string)) (*Result, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()

	// Ctrl+C goes to the whole process group (cargo → rustc, npm → node, …)
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		for sig := range sigs {
			syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
		}
	}()

	res := &Result{}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			res.Lines = append(res.Lines, line)
			if onLine != nil {
				onLine(line)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
			break
		}
	}
	r.Close()
	err = cmd.Wait()
	res.Duration = time.Since(start)
	res.Exit = 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = ws.Signal()
			res.Exit = 128 + int(ws.Signal())
		} else {
			res.Exit = ee.ExitCode()
		}
	} else if err != nil {
		return nil, err
	}
	return res, nil
}
