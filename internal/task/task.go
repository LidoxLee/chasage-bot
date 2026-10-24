// Package task defines the conversation model and its state machine.
package task

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is a conversation's lifecycle state. Agent turns do not change it; see Task.Turn.
type Status string

// Conversation states. See transitions for the allowed moves.
const (
	Idle        Status = "idle"
	PROpen      Status = "pr_open"
	Merging     Status = "merging"
	Deploying   Status = "deploying"
	Building    Status = "building"
	Uploading   Status = "uploading"
	Done        Status = "done"
	Failed      Status = "failed"
	Cancelled   Status = "cancelled"
	Closed      Status = "closed"
	Interrupted Status = "interrupted"
)

var transitions = map[Status][]Status{
	Idle:        {PROpen, Closed, Cancelled},
	PROpen:      {Merging, Cancelled},
	Merging:     {Deploying, Building, Failed, Interrupted},
	Deploying:   {Building, Failed, Interrupted},
	Building:    {Uploading, Failed, Interrupted},
	Uploading:   {Done, Failed, Interrupted},
	Failed:      {Merging, Cancelled},
	Interrupted: {Merging, Cancelled},
}

// CanTransition reports whether moving from one status to another is allowed.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Terminal reports whether the status is final.
func (s Status) Terminal() bool {
	return s == Done || s == Cancelled || s == Closed
}

// Chatting reports whether the conversation still accepts agent turns.
func (s Status) Chatting() bool {
	return s == Idle || s == PROpen
}

// Shipping reports whether the conversation is in (or stopped inside) the release pipeline.
func (s Status) Shipping() bool {
	switch s {
	case Merging, Deploying, Building, Uploading, Failed, Interrupted:
		return true
	}
	return false
}

// Label is the Chinese label shown in Telegram.
func (s Status) Label() string {
	switch s {
	case Idle:
		return "對話中"
	case PROpen:
		return "PR 等待確認"
	case Merging:
		return "合併中"
	case Deploying:
		return "等待部署"
	case Building:
		return "打包中"
	case Uploading:
		return "上傳中"
	case Done:
		return "完成"
	case Failed:
		return "發版失敗"
	case Cancelled:
		return "已取消"
	case Closed:
		return "已結束"
	case Interrupted:
		return "發版中斷"
	}
	return string(s)
}

// Mode is how the agent handles a turn, mirroring Cursor's ask / plan / agent modes.
type Mode string

// Agent modes. Ask and plan are read-only; only agent mode edits code and can open PRs.
const (
	Ask   Mode = "ask"
	Plan  Mode = "plan"
	Agent Mode = "agent"
)

// ParseMode returns the mode named s, if valid.
func ParseMode(s string) (Mode, bool) {
	switch m := Mode(s); m {
	case Ask, Plan, Agent:
		return m, true
	}
	return "", false
}

// RepoKind identifies which repository a PR belongs to.
type RepoKind string

// Repository kinds. Backend PRs are merged and deployed before app PRs.
const (
	Backend RepoKind = "backend"
	App     RepoKind = "app"
)

// PR is a pull request opened for a conversation.
type PR struct {
	Repo     RepoKind `json:"repo"`
	Number   int      `json:"number"`
	URL      string   `json:"url"`
	MergeSHA string   `json:"merge_sha,omitempty"`
}

// Task is one conversation with the agent, from first message to (optionally) a TestFlight build.
type Task struct {
	ID int `json:"id"`
	// Prompt is the first message; it names the conversation.
	Prompt string `json:"prompt"`
	Status Status `json:"status"`
	Mode   Mode   `json:"mode"`
	// Turn is the mode of the agent turn currently running, empty when idle.
	Turn      Mode   `json:"turn,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Dir       string `json:"dir,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	PRs       []PR   `json:"prs,omitempty"`
	// Approved is set once the user asked to merge (or merged on GitHub); retries then resume shipping.
	Approved    bool      `json:"approved,omitempty"`
	MessageIDs  []int     `json:"message_ids,omitempty"`
	BuildNumber int       `json:"build_number,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SetStatus moves the conversation to a new status, enforcing the state machine.
func (t *Task) SetStatus(to Status) error {
	if t.Status == to {
		return nil
	}
	if !CanTransition(t.Status, to) {
		return fmt.Errorf("對話 #%d：不能從「%s」變成「%s」", t.ID, t.Status.Label(), to.Label())
	}
	t.Status = to
	t.UpdatedAt = time.Now()
	return nil
}

// Title is a short single-line summary of the first message.
func (t *Task) Title() string {
	line := strings.TrimSpace(strings.SplitN(t.Prompt, "\n", 2)[0])
	const maxRunes = 50
	if utf8.RuneCountInString(line) <= maxRunes {
		return line
	}
	return string([]rune(line)[:maxRunes]) + "…"
}

// PR returns the conversation's PR for a repo, if any.
func (t *Task) PR(kind RepoKind) (PR, bool) {
	for _, p := range t.PRs {
		if p.Repo == kind {
			return p, true
		}
	}
	return PR{}, false
}

// SetPR inserts or replaces the PR for its repo.
func (t *Task) SetPR(p PR) {
	for i := range t.PRs {
		if t.PRs[i].Repo == p.Repo {
			t.PRs[i] = p
			return
		}
	}
	t.PRs = append(t.PRs, p)
}

// Clone returns a deep copy safe to use outside the store lock.
func (t Task) Clone() Task {
	t.PRs = slices.Clone(t.PRs)
	t.MessageIDs = slices.Clone(t.MessageIDs)
	return t
}

// Migrate upgrades a task persisted by the one-task-per-message version of the bot.
func (t *Task) Migrate() {
	if t.Mode == "" {
		t.Mode = Agent
	}
	switch t.Status {
	case "queued", "coding":
		t.Status = Idle
	case Failed, Interrupted:
		if !t.Approved {
			// Previously agent failures were Failed; now only release failures are.
			t.Status = Closed
		}
	}
}
