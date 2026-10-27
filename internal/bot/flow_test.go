package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
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

// fakeRunner simulates git, gh, agent, fvm and make. Agent-mode turns commit to the backend
// worktree when commit is true; ask and plan turns never change anything.
type fakeRunner struct {
	mu        sync.Mutex
	calls     []string
	heads     map[string]string // worktree dir -> HEAD
	merged    map[string]bool   // "slug#n"
	commit    bool
	failCheck bool
	deployOK  bool
	agentRuns int
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{heads: map[string]string{}, merged: map[string]bool{}, commit: true, deployOK: true}
}

func (f *fakeRunner) Run(_ context.Context, _ io.Writer, c shell.Cmd) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c.Name+" "+strings.Join(c.Args, " "))
	switch c.Name {
	case "git":
		return f.git(c)
	case "agent":
		f.agentRuns++
		if !slices.Contains(c.Args, "--mode") && f.commit {
			wt := filepath.Join(c.Dir, "chasage-backend")
			f.heads[wt] = fmt.Sprintf("h%d", f.agentRuns)
		}
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-1"}`,
			`{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"lib/app/app.dart"}}}}`,
			fmt.Sprintf(`{"type":"result","is_error":false,"result":"回覆 %d","session_id":"sess-1"}`, f.agentRuns),
		}
		if c.OnLine != nil {
			for _, l := range lines {
				c.OnLine(l)
			}
		}
		return strings.Join(lines, "\n"), nil
	case "gh":
		return f.gh(c.Args)
	case "make":
		if c.Args[0] == "build-ipa" {
			dir := filepath.Join(c.Dir, "build", "ios", "ipa")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			return "", os.WriteFile(filepath.Join(dir, "chasage.ipa"), []byte("ipa"), 0o644)
		}
	}
	return "", nil
}

func (f *fakeRunner) head(dir string) string {
	if h, ok := f.heads[dir]; ok {
		return h
	}
	return "h0"
}

func (f *fakeRunner) git(c shell.Cmd) (string, error) {
	switch c.Args[0] {
	case "rev-parse":
		return f.head(c.Dir), nil
	case "rev-list":
		if f.head(c.Dir) != "h0" {
			return "1", nil
		}
		return "0", nil
	}
	return "", nil
}

func (f *fakeRunner) gh(args []string) (string, error) {
	key := func() string { return args[4] + "#" + args[2] } // pr <verb> N -R slug
	switch args[0] + " " + args[1] {
	case "pr create":
		return "https://github.com/" + args[3] + "/pull/7", nil
	case "pr view":
		if f.merged[key()] {
			return `{"state":"MERGED","mergeCommit":{"oid":"abc123"}}`, nil
		}
		return `{"state":"OPEN","mergeCommit":null}`, nil
	case "pr checks":
		if f.failCheck {
			return `[{"name":"test","bucket":"fail"}]`, nil
		}
		return `[{"name":"test","bucket":"pass"},{"name":"lint","bucket":"pass"}]`, nil
	case "pr merge":
		f.merged[key()] = true
	case "run list":
		conclusion := "success"
		if !f.deployOK {
			conclusion = "failure"
		}
		return fmt.Sprintf(`[{"databaseId":1,"status":"completed","conclusion":%q,"url":"https://run/1"}]`, conclusion), nil
	}
	return "", nil
}

func (f *fakeRunner) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeRunner) lastAgentCall() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.calls[i], "agent ") {
			return f.calls[i]
		}
	}
	return ""
}

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
	next int
}

func (n *fakeNotifier) Send(_ context.Context, text string, _ int, _ [][]telegram.Button) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.next++
	n.msgs = append(n.msgs, text)
	return n.next, nil
}

func (n *fakeNotifier) Typing(context.Context) error { return nil }

func (n *fakeNotifier) contains(s string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, m := range n.msgs {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func newTestBot(t *testing.T, r *fakeRunner) (*Bot, *fakeNotifier) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		AllowedChatID:  1,
		Workdir:        filepath.Join(dir, "work"),
		LogDir:         filepath.Join(dir, "logs"),
		Backend:        config.Repo{Slug: "o/chasage-backend", Dir: "chasage-backend", Base: "main"},
		App:            config.Repo{Slug: "o/chasage", Dir: "chasage-app", Base: "main"},
		DeployWorkflow: "ecr.yml",
		ChecksTimeout:  time.Minute,
		DeployTimeout:  time.Minute,
		Release:        config.Release{Timeout: time.Minute},
	}
	for _, d := range []string{
		filepath.Join(cfg.Workdir, "repos", "chasage-backend", ".git"),
		filepath.Join(cfg.Workdir, "repos", "chasage-app", ".git"),
		filepath.Join(cfg.Workdir, "release", "chasage-app", ".git"),
		cfg.LogDir,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dir, "state.json"), 3)
	if err != nil {
		t.Fatal(err)
	}
	out := &fakeNotifier{}
	git := gitx.Git{R: r}
	b := New(Deps{
		Activity: activity.New(100),
		Cfg:      cfg,
		Store:    st,
		Out:      out,
		Git:      git,
		GH:       gh.GH{R: r, Dir: cfg.Workdir, Poll: time.Millisecond, ChecksAppearTimeout: time.Millisecond},
		Agent:    agent.Agent{R: r, Bin: "agent"},
		Release: release.Releaser{
			R: r, Git: git, Slug: cfg.App.Slug, Base: "main",
			Dir: filepath.Join(cfg.Workdir, "release", "chasage-app"),
		},
	})
	return b, out
}

// send dispatches a Telegram message and runs every turn it queued, like the turn worker would.
func send(b *Bot, text string) {
	ctx := context.Background()
	b.dispatch(ctx, parseCommand(text))
	drainTurns(b)
}

func drainTurns(b *Bot) {
	for {
		select {
		case job := <-b.turns:
			b.runTurn(context.Background(), job)
			b.turnDone(job.id)
		default:
			return
		}
	}
}

func ship(t *testing.T, b *Bot) {
	t.Helper()
	select {
	case id := <-b.ship:
		b.shipTask(context.Background(), id)
	default:
		t.Fatal("nothing queued for shipping")
	}
}

func TestPlanGoMergeShip(t *testing.T) {
	r := newFakeRunner()
	b, out := newTestBot(t, r)

	send(b, "幫我規劃新用戶引導")
	conv, ok := b.Store.Active()
	if !ok || conv.Mode != task.Plan || conv.Status != task.Idle || conv.SessionID != "sess-1" {
		t.Fatalf("after plan turn: %+v", conv)
	}
	if !strings.Contains(r.lastAgentCall(), "--mode plan") {
		t.Errorf("plan turn call = %q", r.lastAgentCall())
	}
	if r.count("git push") != 0 || len(conv.PRs) != 0 {
		t.Error("plan turn must not push or open PRs")
	}
	if !out.contains("回覆 1") {
		t.Error("plan reply not sent")
	}
	var kinds []string
	for _, e := range b.Activity.Since(conv.ID, 0) {
		kinds = append(kinds, e.Kind+":"+e.Text)
	}
	want := []string{"bot:已開新對話 #1（plan 模式）。", "user:（plan）幫我規劃新用戶引導", "tool:read lib/app/app.dart", "bot:回覆 1"}
	if !slices.Equal(kinds, want) {
		t.Errorf("activity = %q, want %q", kinds, want)
	}

	b.goImplement(context.Background(), conv.ID)
	drainTurns(b)
	conv, _ = b.Store.Get(conv.ID)
	if conv.Mode != task.Agent || conv.Status != task.PROpen || len(conv.PRs) != 1 || conv.PRs[0].Repo != task.Backend {
		t.Fatalf("after /go: %+v", conv)
	}
	if call := r.lastAgentCall(); strings.Contains(call, "--mode") || !strings.Contains(call, "--resume sess-1") {
		t.Errorf("agent turn call = %q", call)
	}
	if !out.contains("PR 已更新") {
		t.Error("missing PR message")
	}

	send(b, "/merge")
	ship(t, b)
	conv, _ = b.Store.Get(conv.ID)
	if conv.Status != task.Done || conv.BuildNumber != 3 || b.Store.BuildNumber() != 4 {
		t.Fatalf("after ship: status %s build %d next %d err %q", conv.Status, conv.BuildNumber, b.Store.BuildNumber(), conv.Error)
	}
	if r.count("make build-ipa BUILD_NUMBER=3") != 1 || r.count("make upload-testflight IPA=") != 1 {
		t.Error("release commands not run")
	}

	send(b, "下一個想法")
	next, _ := b.Store.Active()
	if next.ID == conv.ID || next.Mode != task.Plan || next.Prompt != "下一個想法" {
		t.Errorf("message after shipping should open a new plan conversation, got %+v", next)
	}
}

func TestAgentTurnWithoutCommitsIsNotFailure(t *testing.T) {
	r := newFakeRunner()
	r.commit = false
	b, out := newTestBot(t, r)

	send(b, "/agent 現在有 agent 在 work 嗎？")
	conv, _ := b.Store.Active()
	if conv.Status != task.Idle || conv.Mode != task.Agent || conv.Turn != "" {
		t.Fatalf("conv = %+v", conv)
	}
	if !out.contains("沒有程式碼變更") {
		t.Errorf("messages = %v", out.msgs)
	}
	if r.count("gh pr create") != 0 {
		t.Error("opened a PR without commits")
	}
}

func TestAskWhilePROpenKeepsPR(t *testing.T) {
	r := newFakeRunner()
	b, _ := newTestBot(t, r)

	send(b, "/agent 加欄位")
	send(b, "/ask 這個欄位會影響舊版 app 嗎？")
	conv, _ := b.Store.Active()
	if conv.Status != task.PROpen || conv.Mode != task.Ask {
		t.Fatalf("conv = %+v", conv)
	}
	if !strings.Contains(r.lastAgentCall(), "--mode ask") {
		t.Errorf("ask call = %q", r.lastAgentCall())
	}
	if r.count("git push") != 1 {
		t.Errorf("pushes = %d, want 1", r.count("git push"))
	}

	// A follow-up in agent mode adds commits to the same PR instead of opening another.
	send(b, "/agent 欄位改名")
	if r.count("git push") != 2 || r.count("gh pr create") != 1 {
		t.Errorf("pushes %d, PRs created %d", r.count("git push"), r.count("gh pr create"))
	}
}

func TestNewAndEnd(t *testing.T) {
	r := newFakeRunner()
	b, out := newTestBot(t, r)

	send(b, "/plan 想法 A")
	first, _ := b.Store.Active()
	send(b, "/new 想法 B")
	second, _ := b.Store.Active()
	if second.ID == first.ID || second.Prompt != "想法 B" {
		t.Fatalf("second = %+v", second)
	}
	if got, _ := b.Store.Get(first.ID); got.Status != task.Closed {
		t.Errorf("first status = %s", got.Status)
	}

	send(b, "/agent 實作")
	send(b, "/new")
	if a, _ := b.Store.Active(); a.ID != second.ID {
		t.Error("/new must refuse while a PR is open")
	}
	if !out.contains("尚未合併的 PR") {
		t.Errorf("messages = %v", out.msgs)
	}

	send(b, "/end")
	if a, _ := b.Store.Active(); a.ID != second.ID {
		t.Error("/end must refuse while a PR is open")
	}
}

func TestFailedChecksBlockMerge(t *testing.T) {
	r := newFakeRunner()
	r.failCheck = true
	b, out := newTestBot(t, r)

	send(b, "/agent x")
	send(b, "/merge")
	ship(t, b)
	conv, _ := b.Store.Active()
	if conv.Status != task.Failed || !conv.Approved {
		t.Fatalf("status %s approved %v", conv.Status, conv.Approved)
	}
	if r.count("gh pr merge") != 0 {
		t.Error("merged despite failing checks")
	}
	if !out.contains("checks 失敗：test") {
		t.Errorf("messages = %v", out.msgs)
	}
}

func TestDeployFailureThenRetry(t *testing.T) {
	r := newFakeRunner()
	r.deployOK = false
	b, _ := newTestBot(t, r)

	send(b, "/agent x")
	send(b, "/merge")
	ship(t, b)
	conv, _ := b.Store.Active()
	if conv.Status != task.Failed || r.count("make build-ipa") != 0 {
		t.Fatalf("status %s, builds %d", conv.Status, r.count("make build-ipa"))
	}

	r.deployOK = true
	send(b, "/retry")
	ship(t, b)
	if conv, _ = b.Store.Get(conv.ID); conv.Status != task.Done {
		t.Fatalf("after retry: %s %q", conv.Status, conv.Error)
	}
}

func TestRecoverClearsInterruptedTurn(t *testing.T) {
	r := newFakeRunner()
	b, out := newTestBot(t, r)
	conv, _ := b.Store.Create("x", task.Plan)
	if _, err := b.Store.Update(conv.ID, func(x *task.Task) error {
		x.Turn = task.Plan
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b.recover(context.Background())
	if got, _ := b.Store.Get(conv.ID); got.Turn != "" {
		t.Error("turn not cleared")
	}
	if !out.contains("請重新傳送") {
		t.Errorf("messages = %v", out.msgs)
	}
}

type fakeBuilds struct {
	latest int
	err    error
}

func (f fakeBuilds) LatestBuildNumber(context.Context) (int, error) { return f.latest, f.err }

func TestManualReleaseSkipsPastAppStoreConnectBuilds(t *testing.T) {
	cases := []struct {
		name      string
		builds    fakeBuilds
		used      int
		nextAfter int
	}{
		{"behind App Store Connect", fakeBuilds{latest: 11}, 12, 13},
		{"ahead of App Store Connect", fakeBuilds{latest: 1}, 3, 4},
		{"lookup fails", fakeBuilds{err: errors.New("offline")}, 3, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRunner()
			b, out := newTestBot(t, r)
			b.Builds = tc.builds
			b.manualRelease(context.Background())
			if r.count(fmt.Sprintf("make build-ipa BUILD_NUMBER=%d", tc.used)) != 1 {
				t.Fatalf("calls = %v", r.calls)
			}
			if got := b.Store.BuildNumber(); got != tc.nextAfter {
				t.Fatalf("next build number = %d, want %d", got, tc.nextAfter)
			}
			if !out.contains(fmt.Sprintf("build %d 已上傳", tc.used)) {
				t.Errorf("messages = %v", out.msgs)
			}
		})
	}
}
