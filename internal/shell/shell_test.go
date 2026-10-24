package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecRun(t *testing.T) {
	tail := NewTailWriter(nil, 1024)
	out, err := Exec{}.Run(context.Background(), tail, Cmd{Name: "sh", Args: []string{"-c", "echo out; echo err >&2"}})
	if err != nil || out != "out" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if got := tail.Tail(10); !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("tail = %q", got)
	}
}

func TestExecRunFailureIncludesStderr(t *testing.T) {
	_, err := Exec{}.Run(context.Background(), nil, Cmd{Name: "sh", Args: []string{"-c", "echo boom >&2; exit 3"}})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunCancelKillsChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Exec{}.Run(ctx, nil, Cmd{Name: "sh", Args: []string{"-c", "sleep 30 & wait"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancel did not kill the process group promptly")
	}
}

func TestTailWriterKeepsEnd(t *testing.T) {
	tw := NewTailWriter(nil, 8)
	_, _ = tw.Write([]byte("0123456789\nab"))
	if got := tw.Tail(5); got != "56789\nab" {
		t.Errorf("tail = %q", got)
	}
}
