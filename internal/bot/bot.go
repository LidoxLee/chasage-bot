// Package bot wires Telegram conversations to the agent and the ship pipeline.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/activity"
	"github.com/LidoxLee/chasage-bot/internal/agent"
	"github.com/LidoxLee/chasage-bot/internal/config"
	"github.com/LidoxLee/chasage-bot/internal/gh"
	"github.com/LidoxLee/chasage-bot/internal/gitx"
	"github.com/LidoxLee/chasage-bot/internal/release"
	"github.com/LidoxLee/chasage-bot/internal/shell"
	"github.com/LidoxLee/chasage-bot/internal/store"
	"github.com/LidoxLee/chasage-bot/internal/task"
	"github.com/LidoxLee/chasage-bot/internal/telegram"
)

// Notifier sends messages to the owner's chat.
type Notifier interface {
	Send(ctx context.Context, text string, replyTo int, buttons [][]telegram.Button) (int, error)
	Typing(ctx context.Context) error
}

// Deps are the collaborators the bot needs.
type Deps struct {
	Cfg     *config.Config
	Store   *store.Store
	TG      *telegram.Client
	Out     Notifier
	Git     gitx.Git
	GH      gh.GH
	Agent   agent.Agent
	Release release.Releaser
	// Activity, when set, receives each conversation's timeline for the dashboard.
	Activity *activity.Hub
}

// turnJob is one user message for the agent.
type turnJob struct {
	id   int
	mode task.Mode
	text string
}

// Bot owns the work queues. Agent turns and ship jobs run on separate single workers,
// so at most one agent turn and one release run at a time.
type Bot struct {
	Deps
	turns chan turnJob
	ship  chan int // conversation id, or 0 for a manual release

	// gitMu serializes operations on the shared base clones (fetch, worktree add/remove).
	gitMu sync.Mutex

	mu      sync.Mutex
	running map[int]context.CancelFunc
	pending map[int]int // queued or running turns per conversation
}

// New returns a Bot.
func New(d Deps) *Bot {
	return &Bot{
		Deps:    d,
		turns:   make(chan turnJob, 100),
		ship:    make(chan int, 100),
		running: map[int]context.CancelFunc{},
		pending: map[int]int{},
	}
}

type repoRef struct {
	kind task.RepoKind
	cfg  config.Repo
}

func (b *Bot) repos() []repoRef {
	return []repoRef{{task.Backend, b.Cfg.Backend}, {task.App, b.Cfg.App}}
}

func (b *Bot) repoCfg(kind task.RepoKind) config.Repo {
	if kind == task.Backend {
		return b.Cfg.Backend
	}
	return b.Cfg.App
}

func (b *Bot) baseDir(r config.Repo) string {
	return filepath.Join(b.Cfg.Workdir, "repos", r.Dir)
}

func (b *Bot) convDir(id int) string {
	return filepath.Join(b.Cfg.Workdir, "tasks", strconv.Itoa(id))
}

// Run starts the workers, recovers persisted conversations and polls Telegram until ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	for _, d := range []string{b.Cfg.Workdir, b.Cfg.LogDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	go b.turnWorker(ctx)
	go b.shipWorker(ctx)
	go b.watchExternalMerges(ctx)
	b.recover(ctx)
	if b.Cfg.AllowedChatID != 0 {
		b.notify(ctx, "chasage-bot 已啟動。直接傳訊息開始對話（預設 plan 模式），/help 查看指令。")
	}
	return b.pollTelegram(ctx)
}

func (b *Bot) pollTelegram(ctx context.Context) error {
	offset := b.Store.TelegramOffset()
	for ctx.Err() == nil {
		ups, err := b.TG.GetUpdates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if strings.Contains(err.Error(), "Unauthorized") {
				return fmt.Errorf("telegram_token 無效（Telegram 回應 Unauthorized），請檢查設定檔: %w", err)
			}
			slog.Warn("getUpdates failed", "err", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		for _, u := range ups {
			b.handleUpdate(ctx, u)
			offset = u.UpdateID + 1
			if err := b.Store.SetTelegramOffset(offset); err != nil {
				slog.Error("save offset", "err", err)
			}
		}
	}
	return ctx.Err()
}

// recover resumes or flags work that was in flight when the bot stopped.
func (b *Bot) recover(ctx context.Context) {
	for _, t := range b.Store.List() {
		if t.Turn != "" {
			if _, err := b.Store.Update(t.ID, func(x *task.Task) error {
				x.Turn = ""
				return nil
			}); err != nil {
				slog.Error("clear turn", "conv", t.ID, "err", err)
			}
			b.say(ctx, t.ID, fmt.Sprintf("#%d 上一則（%s 模式）處理到一半時 bot 重新啟動，請重新傳送。", t.ID, t.Turn), nil)
		}
		switch t.Status {
		case task.Merging, task.Deploying:
			// Merging and waiting for deploy are idempotent, so they resume on their own.
			b.ship <- t.ID
		case task.Building, task.Uploading:
			// Re-running these automatically could double-upload; let the owner decide.
			if _, err := b.Store.Update(t.ID, func(x *task.Task) error { return x.SetStatus(task.Interrupted) }); err != nil {
				slog.Error("mark interrupted", "conv", t.ID, "err", err)
				continue
			}
			b.say(ctx, t.ID, fmt.Sprintf("#%d 在「%s」階段時 bot 重新啟動，已中斷。要重跑請輸入 /retry %d", t.ID, t.Status.Label(), t.ID), nil)
		}
	}
}

func (b *Bot) track(id int, cancel context.CancelFunc) {
	b.mu.Lock()
	b.running[id] = cancel
	b.mu.Unlock()
}

func (b *Bot) untrack(id int) {
	b.mu.Lock()
	delete(b.running, id)
	b.mu.Unlock()
}

func (b *Bot) stopRunning(id int) {
	b.mu.Lock()
	cancel := b.running[id]
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// enqueueTurn queues a message for the agent and reports whether earlier turns are still pending.
func (b *Bot) enqueueTurn(job turnJob) (waiting bool) {
	b.mu.Lock()
	waiting = b.pending[job.id] > 0
	b.pending[job.id]++
	b.mu.Unlock()
	b.turns <- job
	return waiting
}

func (b *Bot) turnDone(id int) {
	b.mu.Lock()
	if b.pending[id]--; b.pending[id] <= 0 {
		delete(b.pending, id)
	}
	b.mu.Unlock()
}

func (b *Bot) turnWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-b.turns:
			jobCtx, cancel := context.WithTimeout(ctx, b.Cfg.Agent.Timeout+10*time.Minute)
			b.track(job.id, cancel)
			b.runTurn(jobCtx, job)
			b.untrack(job.id)
			cancel()
			b.turnDone(job.id)
		}
	}
}

func (b *Bot) shipWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-b.ship:
			if id == 0 {
				b.manualRelease(ctx)
				continue
			}
			t, ok := b.Store.Get(id)
			if !ok || (t.Status != task.Merging && t.Status != task.Deploying) {
				continue
			}
			jobCtx, cancel := context.WithCancel(ctx)
			b.track(id, cancel)
			b.shipTask(jobCtx, id)
			b.untrack(id)
			cancel()
		}
	}
}

// openLog opens an append-only log file and returns a writer that also keeps the recent tail.
func (b *Bot) openLog(name string) (*shell.TailWriter, func()) {
	f, err := os.OpenFile(filepath.Join(b.Cfg.LogDir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("open log", "name", name, "err", err)
		return shell.NewTailWriter(nil, 64*1024), func() {}
	}
	fmt.Fprintf(f, "\n===== %s =====\n", time.Now().Format(time.RFC3339))
	return shell.NewTailWriter(f, 64*1024), func() { _ = f.Close() }
}

func convLog(id int) string { return fmt.Sprintf("conv-%d.log", id) }

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
