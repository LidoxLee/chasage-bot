package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/task"
)

// shipTask merges the task's PRs (backend first, then waits for the EC2 deploy), then releases the app.
// Every step is idempotent so a retry or restart resumes where it stopped.
func (b *Bot) shipTask(ctx context.Context, id int) {
	log, closeLog := b.openLog(convLog(id))
	defer closeLog()

	t, _ := b.Store.Get(id)
	b.say(ctx, id, fmt.Sprintf("#%d 等待 CI 通過後合併…", id), nil)

	if err := b.waitAllChecks(ctx, log, t); err != nil {
		b.fail(ctx, id, "CI 檢查", err, log)
		return
	}

	if pr, ok := t.PR(task.Backend); ok {
		sha, err := b.ensureMerged(ctx, log, id, pr)
		if err != nil {
			b.fail(ctx, id, "合併 backend", err, log)
			return
		}
		if err := b.setStatus(id, task.Deploying); err != nil {
			b.fail(ctx, id, "更新狀態", err, log)
			return
		}
		b.say(ctx, id, fmt.Sprintf("#%d backend 已合併，等待部署到 EC2…", id), nil)
		url, err := b.GH.WaitWorkflow(ctx, log, b.Cfg.Backend.Slug, b.Cfg.DeployWorkflow, sha, b.Cfg.DeployTimeout)
		if err != nil {
			b.fail(ctx, id, "部署", fmt.Errorf("%w %s", err, url), log)
			return
		}
		b.say(ctx, id, fmt.Sprintf("#%d 部署成功：%s", id, url), nil)
	}

	if pr, ok := t.PR(task.App); ok {
		if _, err := b.ensureMerged(ctx, log, id, pr); err != nil {
			b.fail(ctx, id, "合併 app", err, log)
			return
		}
	}

	n, err := b.buildAndUpload(ctx, log, id)
	if err != nil {
		b.fail(ctx, id, "打包上傳", err, log)
		return
	}
	t, err = b.Store.Update(id, func(x *task.Task) error {
		x.BuildNumber = n
		x.Error = ""
		return x.SetStatus(task.Done)
	})
	if err != nil {
		slog.Error("mark done", "task", id, "err", err)
	}
	b.cleanupWorkspace(ctx, log, t)
	b.say(ctx, id, fmt.Sprintf("#%d 完成：build %d 已上傳 TestFlight。Apple 處理完後，TestFlight App 會再通知你。", id, n), nil)
}

// waitAllChecks requires green checks on every PR that is still open, before anything is merged.
func (b *Bot) waitAllChecks(ctx context.Context, log io.Writer, t task.Task) error {
	for _, p := range t.PRs {
		slug := b.repoCfg(p.Repo).Slug
		info, err := b.GH.ViewPR(ctx, log, slug, p.Number)
		if err != nil {
			return err
		}
		switch info.State {
		case "MERGED":
			continue
		case "CLOSED":
			return fmt.Errorf("%s PR #%d 已被關閉", p.Repo, p.Number)
		}
		if err := b.GH.WaitChecks(ctx, log, slug, p.Number, b.Cfg.ChecksTimeout); err != nil {
			return fmt.Errorf("%s PR #%d：%w", p.Repo, p.Number, err)
		}
	}
	return nil
}

// ensureMerged merges the PR unless it already is, and returns the merge commit sha.
func (b *Bot) ensureMerged(ctx context.Context, log io.Writer, id int, p task.PR) (string, error) {
	slug := b.repoCfg(p.Repo).Slug
	info, err := b.GH.ViewPR(ctx, log, slug, p.Number)
	if err != nil {
		return "", err
	}
	if info.State != "MERGED" {
		if err := b.GH.MergePR(ctx, log, slug, p.Number); err != nil {
			return "", err
		}
		if info, err = b.GH.ViewPR(ctx, log, slug, p.Number); err != nil {
			return "", err
		}
	}
	if info.MergeCommit == nil || info.MergeCommit.OID == "" {
		return "", errors.New("找不到 merge commit")
	}
	p.MergeSHA = info.MergeCommit.OID
	_, err = b.Store.Update(id, func(x *task.Task) error {
		x.SetPR(p)
		return nil
	})
	return p.MergeSHA, err
}

// buildAndUpload releases the current app main to TestFlight and returns the build number used.
func (b *Bot) buildAndUpload(ctx context.Context, log io.Writer, id int) (int, error) {
	n := b.nextBuildNumber(ctx, log)
	if err := b.setStatus(id, task.Building); err != nil {
		return 0, err
	}
	b.say(ctx, id, fmt.Sprintf("#%d 開始打包 iOS（build %d）…", id, n), nil)
	ctx, cancel := context.WithTimeout(ctx, b.Cfg.Release.Timeout)
	defer cancel()
	ipa, err := b.Release.Build(ctx, log, n)
	if err != nil {
		return 0, err
	}
	if err := b.setStatus(id, task.Uploading); err != nil {
		return 0, err
	}
	b.say(ctx, id, fmt.Sprintf("#%d 打包完成，上傳 TestFlight…", id), nil)
	if err := b.Release.Upload(ctx, log, ipa); err != nil {
		return 0, err
	}
	return n, b.Store.AdvanceBuildNumber(n)
}

// nextBuildNumber is the stored next build number, moved past the highest build
// on App Store Connect. A failed lookup falls back to the stored number.
func (b *Bot) nextBuildNumber(ctx context.Context, log io.Writer) int {
	n := b.Store.BuildNumber()
	if b.Builds == nil {
		return n
	}
	latest, err := b.Builds.LatestBuildNumber(ctx)
	if err != nil {
		slog.Warn("App Store Connect build lookup", "err", err)
		fmt.Fprintf(log, "查詢 App Store Connect 的 build number 失敗，沿用 build %d：%v\n", n, err)
		return n
	}
	if latest < n {
		return n
	}
	fmt.Fprintf(log, "App Store Connect 已有 build %d，改用 build %d\n", latest, latest+1)
	if err := b.Store.SetBuildNumber(latest + 1); err != nil {
		slog.Error("set build number", "err", err)
	}
	return latest + 1
}

// manualRelease builds and uploads app main without a task (the /release command).
func (b *Bot) manualRelease(ctx context.Context) {
	name := fmt.Sprintf("release-%s.log", time.Now().Format("20060102-150405"))
	log, closeLog := b.openLog(name)
	defer closeLog()
	n := b.nextBuildNumber(ctx, log)
	b.notify(ctx, fmt.Sprintf("手動發版：開始打包 iOS（build %d）…", n))
	rctx, cancel := context.WithTimeout(ctx, b.Cfg.Release.Timeout)
	defer cancel()
	ipa, err := b.Release.Build(rctx, log, n)
	if err == nil {
		b.notify(ctx, "手動發版：打包完成，上傳 TestFlight…")
		err = b.Release.Upload(rctx, log, ipa)
	}
	if err == nil {
		err = b.Store.AdvanceBuildNumber(n)
	}
	if err != nil {
		b.notify(ctx, fmt.Sprintf("手動發版失敗：%s\n\n最後的 log：\n%s\n\n完整 log：%s", err, log.Tail(30), name))
		return
	}
	b.notify(ctx, fmt.Sprintf("手動發版完成：build %d 已上傳 TestFlight。", n))
}

func (b *Bot) setStatus(id int, s task.Status) error {
	_, err := b.Store.Update(id, func(t *task.Task) error { return t.SetStatus(s) })
	return err
}

// watchExternalMerges notices PRs merged or closed directly on GitHub.
func (b *Bot) watchExternalMerges(ctx context.Context) {
	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, t := range b.Store.List() {
				if t.Status == task.PROpen {
					b.checkExternal(ctx, t)
				}
			}
		}
	}
}

func (b *Bot) checkExternal(ctx context.Context, t task.Task) {
	merged, closed := 0, 0
	for _, p := range t.PRs {
		info, err := b.GH.ViewPR(ctx, nil, b.repoCfg(p.Repo).Slug, p.Number)
		if err != nil {
			slog.Warn("view PR", "task", t.ID, "err", err)
			return
		}
		switch info.State {
		case "MERGED":
			merged++
		case "CLOSED":
			closed++
		}
	}
	switch {
	case merged > 0:
		b.say(ctx, t.ID, fmt.Sprintf("#%d 偵測到你在 GitHub 上合併了 PR，接著處理部署與打包。", t.ID), nil)
		b.approve(ctx, t.ID)
	case closed == len(t.PRs) && closed > 0:
		b.say(ctx, t.ID, fmt.Sprintf("#%d 的 PR 都在 GitHub 上被關閉了，取消對話。", t.ID), nil)
		b.cancel(ctx, t.ID)
	}
}

// approve records the owner's go-ahead and queues shipping.
func (b *Bot) approve(ctx context.Context, id int) {
	if b.hasPendingTurns(id) {
		b.notify(ctx, fmt.Sprintf("#%d agent 還在處理訊息，等它回覆後再合併。", id))
		return
	}
	if _, err := b.Store.Update(id, func(t *task.Task) error {
		if t.Status != task.PROpen {
			return fmt.Errorf("#%d 目前是「%s」，不能合併", t.ID, t.Status.Label())
		}
		t.Approved = true
		return t.SetStatus(task.Merging)
	}); err != nil {
		b.notify(ctx, err.Error())
		return
	}
	b.say(ctx, id, fmt.Sprintf("#%d 進入發版流程。之後直接傳訊息會開新對話。", id), nil)
	b.ship <- id
}

func (b *Bot) hasPendingTurns(id int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[id] > 0
}

// closeConv ends a conversation that has no PR and removes its worktrees.
func (b *Bot) closeConv(ctx context.Context, id int) error {
	if b.hasPendingTurns(id) {
		return fmt.Errorf("#%d agent 還在處理訊息，請等它回覆，或用 /cancel 中止", id)
	}
	t, err := b.Store.Update(id, func(t *task.Task) error {
		if t.Status == task.PROpen {
			return fmt.Errorf("#%d 還有尚未合併的 PR，請先合併（/merge）或取消（/cancel）", t.ID)
		}
		return t.SetStatus(task.Closed)
	})
	if err != nil {
		return err
	}
	if err := b.Store.ClearActive(id); err != nil {
		return err
	}
	log, closeLog := b.openLog(convLog(id))
	defer closeLog()
	b.cleanupWorkspace(ctx, log, t)
	return nil
}

// cancel stops any running turn or job, closes open PRs and removes the worktrees.
func (b *Bot) cancel(ctx context.Context, id int) {
	t, err := b.Store.Update(id, func(t *task.Task) error { return t.SetStatus(task.Cancelled) })
	if err != nil {
		b.notify(ctx, fmt.Sprintf("#%d 無法取消：%s", id, err))
		return
	}
	b.stopRunning(id)
	if err := b.Store.ClearActive(id); err != nil {
		slog.Error("clear active", "conv", id, "err", err)
	}
	go func() {
		log, closeLog := b.openLog(convLog(id))
		defer closeLog()
		for _, p := range t.PRs {
			slug := b.repoCfg(p.Repo).Slug
			if info, err := b.GH.ViewPR(ctx, log, slug, p.Number); err == nil && info.State == "OPEN" {
				if err := b.GH.ClosePR(ctx, log, slug, p.Number); err != nil {
					slog.Warn("close PR", "conv", id, "err", err)
				}
			}
		}
		b.cleanupWorkspace(ctx, log, t)
		b.say(ctx, id, fmt.Sprintf("#%d 已取消，PR 已關閉、worktree 已清除。", id), nil)
	}()
}
