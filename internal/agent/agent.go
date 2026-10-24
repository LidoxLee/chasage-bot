// Package agent drives the Cursor CLI in headless mode.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/LidoxLee/chasage-bot/internal/shell"
	"github.com/LidoxLee/chasage-bot/internal/task"
)

// Result is the outcome of one agent turn.
type Result struct {
	Text      string
	SessionID string
}

// Agent runs `agent -p --output-format json`.
type Agent struct {
	R         shell.Runner
	Bin       string
	Model     string
	ExtraArgs []string
}

// Run sends prompt in dir using mode. A non-empty sessionID resumes that conversation;
// the same session can be resumed under a different mode.
func (a Agent) Run(ctx context.Context, log io.Writer, dir, sessionID string, mode task.Mode, prompt string) (Result, error) {
	args := []string{"-p", "--output-format", "json"}
	if mode == task.Ask || mode == task.Plan {
		args = append(args, "--mode", string(mode))
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	args = append(args, a.ExtraArgs...)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	args = append(args, prompt)
	out, err := a.R.Run(ctx, log, shell.Cmd{Dir: dir, Name: a.Bin, Args: args})
	res, perr := ParseOutput(out)
	if res.SessionID == "" {
		res.SessionID = sessionID
	}
	if err != nil {
		return res, err
	}
	return res, perr
}

type output struct {
	Type      string `json:"type"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// ParseOutput reads the final JSON object printed by the CLI.
func ParseOutput(out string) (Result, error) {
	lines := shell.Lines(out)
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "{") {
			continue
		}
		var o output
		if err := json.Unmarshal([]byte(lines[i]), &o); err != nil {
			continue
		}
		res := Result{Text: strings.TrimSpace(o.Result), SessionID: o.SessionID}
		if o.IsError {
			return res, fmt.Errorf("agent 回報錯誤：%s", res.Text)
		}
		return res, nil
	}
	if out == "" {
		return Result{}, errors.New("agent 沒有任何輸出")
	}
	return Result{Text: out}, errors.New("無法解析 agent 的輸出")
}
