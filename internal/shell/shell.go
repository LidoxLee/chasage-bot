// Package shell runs external commands with logging and process-group cancellation.
package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Cmd is a single command invocation.
type Cmd struct {
	Dir  string
	Env  []string // appended to the current environment
	Name string
	Args []string
	// Stdin, when non-empty, is fed to the process.
	Stdin string
}

// Runner executes commands. It exists so tests can substitute a fake.
type Runner interface {
	// Run executes c and returns its trimmed stdout. Stdout and stderr are both copied to log.
	Run(ctx context.Context, log io.Writer, c Cmd) (string, error)
}

// Exec is the real Runner backed by os/exec.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, log io.Writer, c Cmd) (string, error) {
	if log == nil {
		log = io.Discard
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	// Kill the whole process group on cancel: agent, flutter and xcodebuild all spawn children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, log)
	cmd.Stderr = io.MultiWriter(&stderr, log)

	fmt.Fprintf(log, "\n$ (%s) %s %s\n", c.Dir, c.Name, strings.Join(c.Args, " "))
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, fmt.Errorf("%s %s: %w: %s", c.Name, firstArg(c.Args), err, lastLines(stderr.String(), 5))
	}
	return out, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
