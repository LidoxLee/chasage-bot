package task

import (
	"strings"
	"testing"
)

func TestSetStatus(t *testing.T) {
	cases := []struct {
		from, to Status
		ok       bool
	}{
		{Idle, PROpen, true},
		{Idle, Closed, true},
		{PROpen, Merging, true},
		{PROpen, Cancelled, true},
		{Merging, Deploying, true},
		{Merging, Building, true},
		{Deploying, Building, true},
		{Building, Uploading, true},
		{Uploading, Done, true},
		{Failed, Merging, true},
		{Interrupted, Merging, true},
		{Idle, Merging, false},
		{PROpen, Closed, false},
		{PROpen, Idle, false},
		{Done, Idle, false},
		{Closed, Idle, false},
		{Deploying, Cancelled, false},
	}
	for _, c := range cases {
		tk := &Task{ID: 1, Status: c.from}
		err := tk.SetStatus(c.to)
		if (err == nil) != c.ok {
			t.Errorf("%s -> %s: err=%v, want ok=%v", c.from, c.to, err, c.ok)
		}
		if c.ok && tk.Status != c.to {
			t.Errorf("%s -> %s: status=%s", c.from, c.to, tk.Status)
		}
		if !c.ok && tk.Status != c.from {
			t.Errorf("%s -> %s: status changed on rejected transition", c.from, c.to)
		}
	}
}

func TestSetStatusSameIsNoop(t *testing.T) {
	tk := &Task{Status: Done}
	if err := tk.SetStatus(Done); err != nil {
		t.Fatal(err)
	}
}

func TestStatusGroups(t *testing.T) {
	if !Idle.Chatting() || !PROpen.Chatting() || Merging.Chatting() {
		t.Error("Chatting")
	}
	if !Failed.Shipping() || !Deploying.Shipping() || PROpen.Shipping() || Done.Shipping() {
		t.Error("Shipping")
	}
	if !Closed.Terminal() || Failed.Terminal() {
		t.Error("Terminal")
	}
}

func TestParseMode(t *testing.T) {
	if m, ok := ParseMode("plan"); !ok || m != Plan {
		t.Errorf("plan: %v %v", m, ok)
	}
	if _, ok := ParseMode("yolo"); ok {
		t.Error("yolo accepted")
	}
}

func TestMigrate(t *testing.T) {
	cases := []struct {
		in       Task
		want     Status
		wantMode Mode
	}{
		{Task{Status: "queued"}, Idle, Agent},
		{Task{Status: "coding"}, Idle, Agent},
		{Task{Status: Failed}, Closed, Agent},
		{Task{Status: Failed, Approved: true}, Failed, Agent},
		{Task{Status: PROpen, Mode: Plan}, PROpen, Plan},
	}
	for _, c := range cases {
		tk := c.in
		tk.Migrate()
		if tk.Status != c.want || tk.Mode != c.wantMode {
			t.Errorf("Migrate(%+v) = %s %s", c.in, tk.Status, tk.Mode)
		}
	}
}

func TestTitle(t *testing.T) {
	tk := &Task{Prompt: "  做兒童模式\n細節很多"}
	if got := tk.Title(); got != "做兒童模式" {
		t.Errorf("Title() = %q", got)
	}
	tk.Prompt = strings.Repeat("字", 60)
	if got := tk.Title(); !strings.HasSuffix(got, "…") || len([]rune(got)) != 51 {
		t.Errorf("long Title() = %q", got)
	}
}

func TestSetPRAndClone(t *testing.T) {
	tk := Task{}
	tk.SetPR(PR{Repo: Backend, Number: 1})
	tk.SetPR(PR{Repo: App, Number: 2})
	tk.SetPR(PR{Repo: Backend, Number: 3})
	if len(tk.PRs) != 2 {
		t.Fatalf("PRs = %v", tk.PRs)
	}
	if p, _ := tk.PR(Backend); p.Number != 3 {
		t.Errorf("backend PR = %d", p.Number)
	}
	c := tk.Clone()
	c.PRs[0].Number = 99
	if tk.PRs[0].Number == 99 {
		t.Error("Clone shares PR slice")
	}
}
