package bot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/agent"
	"github.com/LidoxLee/chasage-bot/internal/shell"
	"github.com/LidoxLee/chasage-bot/internal/task"
)

// runTurn sends one user message to the agent in the job's mode. Only agent-mode turns that
// produce commits push and open or update PRs; every other outcome is just a reply.
func (b *Bot) runTurn(ctx context.Context, job turnJob) {
	log, closeLog := b.openLog(convLog(job.id))
	defer closeLog()

	t, err := b.Store.Update(job.id, func(t *task.Task) error {
		if !t.Status.Chatting() {
			return fmt.Errorf("對話 #%d 目前是「%s」，這則訊息沒有送出。直接傳訊息會開新對話。", t.ID, t.Status.Label())
		}
		if t.Branch == "" {
			t.Branch = fmt.Sprintf("bot/conv-%d", t.ID)
		}
		if t.Dir == "" {
			t.Dir = b.convDir(t.ID)
		}
		t.Turn = job.mode
		return nil
	})
	if err != nil {
		b.notify(ctx, err.Error())
		return
	}
	defer func() {
		if _, err := b.Store.Update(job.id, func(x *task.Task) error {
			x.Turn = ""
			return nil
		}); err != nil {
			slog.Error("clear turn", "conv", job.id, "err", err)
		}
	}()
	stopTyping := b.keepTyping(ctx)
	defer stopTyping()

	if err := b.prepareWorkspace(ctx, log, t); err != nil {
		b.turnError(ctx, job, "準備 worktree", err, log)
		return
	}
	heads := b.heads(ctx, log, t)

	ws := agent.Workspace{BackendDir: b.Cfg.Backend.Dir, AppDir: b.Cfg.App.Dir, Branch: t.Branch}
	prompt := agent.TurnPrompt(ws, job.mode, t.SessionID == "", job.text)
	res, runErr := b.Agent.Run(ctx, log, t.Dir, t.SessionID, job.mode, prompt)
	if res.SessionID != "" {
		if _, err := b.Store.Update(job.id, func(x *task.Task) error {
			x.SessionID = res.SessionID
			return nil
		}); err != nil {
			slog.Error("save session", "conv", job.id, "err", err)
		}
	}
	if runErr != nil {
		b.turnError(ctx, job, "agent", runErr, log)
		return
	}

	switch job.mode {
	case task.Plan:
		b.sayLong(ctx, job.id, res.Text, planButtons(job.id))
		return
	case task.Ask:
		b.sayLong(ctx, job.id, res.Text, nil)
		return
	}

	changed, err := b.publish(ctx, log, job.id, heads, res.Text)
	if err != nil {
		b.turnError(ctx, job, "push／開 PR", err, log)
		return
	}
	if !changed {
		b.sayLong(ctx, job.id, res.Text+"\n\n（這一輪沒有程式碼變更）", nil)
		return
	}
	t, err = b.Store.Update(job.id, func(x *task.Task) error { return x.SetStatus(task.PROpen) })
	if err != nil {
		b.turnError(ctx, job, "更新狀態", err, log)
		return
	}
	b.sayLong(ctx, job.id, fmt.Sprintf("%s\n\n#%d PR 已更新：\n%s\n按「合併」會等 CI 通過後合併、部署、打包上傳 TestFlight。"+
		"要修改就繼續傳訊息。", res.Text, job.id, prLines(t)), prButtons(job.id))
}

// keepTyping shows Telegram's typing indicator until the returned func is called.
func (b *Bot) keepTyping(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			if err := b.Out.Typing(ctx); err != nil && ctx.Err() == nil {
				slog.Debug("typing", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return cancel
}

func (b *Bot) turnError(ctx context.Context, job turnJob, stage string, err error, log *shell.TailWriter) {
	if ctx.Err() != nil {
		if t, ok := b.Store.Get(job.id); ok && t.Status == task.Cancelled {
			return
		}
	}
	slog.Error("turn failed", "conv", job.id, "mode", job.mode, "stage", stage, "err", err)
	var sb strings.Builder
	fmt.Fprintf(&sb, "#%d（%s 模式）%s出錯：%s\n", job.id, job.mode, stage, err)
	if tail := log.Tail(20); tail != "" {
		fmt.Fprintf(&sb, "\n最後的 log：\n%s\n", tail)
	}
	fmt.Fprintf(&sb, "\n完整 log：%s。對話還在，可以直接重傳或換個說法。", convLog(job.id))
	b.sayLong(ctx, job.id, sb.String(), nil)
}

// prepareWorkspace makes sure both repos have a worktree for the conversation branch, and moves
// worktrees without commits or edits to the latest base so answers reflect current code.
func (b *Bot) prepareWorkspace(ctx context.Context, log io.Writer, t task.Task) error {
	b.gitMu.Lock()
	defer b.gitMu.Unlock()
	for _, r := range b.repos() {
		base := b.baseDir(r.cfg)
		if err := b.Git.EnsureClone(ctx, log, r.cfg.Slug, base); err != nil {
			return err
		}
		wt := filepath.Join(t.Dir, r.cfg.Dir)
		if err := b.Git.AddWorktree(ctx, log, base, wt, t.Branch, r.cfg.Base); err != nil {
			return err
		}
		if _, err := b.Git.RefreshIfPristine(ctx, log, wt, r.cfg.Base); err != nil {
			return err
		}
	}
	return nil
}

// heads records each worktree's HEAD so publish can tell which repos got new commits this turn.
func (b *Bot) heads(ctx context.Context, log io.Writer, t task.Task) map[task.RepoKind]string {
	out := map[task.RepoKind]string{}
	for _, r := range b.repos() {
		if h, err := b.Git.Head(ctx, log, filepath.Join(t.Dir, r.cfg.Dir)); err == nil {
			out[r.kind] = h
		}
	}
	return out
}

// publish pushes worktrees that gained commits this turn and opens PRs that do not exist yet.
// It reports whether anything changed.
func (b *Bot) publish(ctx context.Context, log io.Writer, id int, before map[task.RepoKind]string, summary string) (bool, error) {
	t, _ := b.Store.Get(id)
	changed := false
	for _, r := range b.repos() {
		wt := filepath.Join(t.Dir, r.cfg.Dir)
		if _, err := b.Git.CommitLeftovers(ctx, log, wt, fmt.Sprintf("chore: bot conversation #%d leftover changes", id)); err != nil {
			return changed, err
		}
		head, err := b.Git.Head(ctx, log, wt)
		if err != nil {
			return changed, err
		}
		if head == before[r.kind] {
			continue
		}
		changed = true
		if err := b.Git.Push(ctx, log, wt, t.Branch); err != nil {
			return changed, err
		}
		if _, exists := t.PR(r.kind); exists {
			continue
		}
		title := fmt.Sprintf("[bot #%d] %s", id, t.Title())
		body := fmt.Sprintf("%s\n\n---\n由 chasage-bot 建立（對話 #%d）。第一則訊息：\n\n> %s\n",
			summary, id, strings.ReplaceAll(strings.TrimSpace(t.Prompt), "\n", "\n> "))
		n, url, err := b.GH.CreatePR(ctx, log, r.cfg.Slug, r.cfg.Base, t.Branch, title, body)
		if err != nil {
			return changed, err
		}
		if t, err = b.Store.Update(id, func(x *task.Task) error {
			x.SetPR(task.PR{Repo: r.kind, Number: n, URL: url})
			return nil
		}); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// cleanupWorkspace removes the conversation's worktrees and local branches.
func (b *Bot) cleanupWorkspace(ctx context.Context, log io.Writer, t task.Task) {
	if t.Dir == "" {
		return
	}
	b.gitMu.Lock()
	defer b.gitMu.Unlock()
	for _, r := range b.repos() {
		b.Git.RemoveWorktree(ctx, log, b.baseDir(r.cfg), filepath.Join(t.Dir, r.cfg.Dir), t.Branch)
	}
}
