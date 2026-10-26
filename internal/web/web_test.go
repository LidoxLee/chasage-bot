package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LidoxLee/chasage-bot/internal/activity"
	"github.com/LidoxLee/chasage-bot/internal/store"
	"github.com/LidoxLee/chasage-bot/internal/task"
)

func newServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.json"), 3)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, Hub: activity.New(100), LogDir: dir}, st
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRejectsNonLoopbackHost(t *testing.T) {
	s, _ := newServer(t)
	if rec := get(t, s.Handler(), "evil.example:8787", "/api/conversations"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d", rec.Code)
	}
	for _, host := range []string{"127.0.0.1:8787", "localhost:8787"} {
		if rec := get(t, s.Handler(), host, "/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chasage-bot") {
			t.Errorf("%s: status = %d", host, rec.Code)
		}
	}
}

func TestConversationsAndActivity(t *testing.T) {
	s, st := newServer(t)
	old, _ := st.Create("舊想法", task.Plan)
	cur, _ := st.Create("新用戶引導", task.Agent)
	if _, err := st.Update(cur.ID, func(x *task.Task) error {
		x.Turn = task.Agent
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Add(cur.ID, activity.KindUser, "做導覽")
	s.Hub.Add(cur.ID, "tool", "read lib/app/app.dart")

	rec := get(t, s.Handler(), "127.0.0.1:8787", "/api/conversations")
	var list struct {
		Conversations []convView `json:"conversations"`
		Next          int        `json:"next_build_number"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 2 || list.Conversations[0].ID != cur.ID || list.Next != 3 {
		t.Fatalf("list = %+v", list)
	}
	first := list.Conversations[0]
	if !first.Active || first.Turn != "agent" || first.StatusLabel != "對話中" || list.Conversations[1].ID != old.ID {
		t.Errorf("first = %+v", first)
	}

	rec = get(t, s.Handler(), "127.0.0.1:8787", "/api/conversations/2/activity?since=1")
	var act struct {
		Entries []activity.Entry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &act); err != nil {
		t.Fatal(err)
	}
	if len(act.Entries) != 1 || act.Entries[0].Text != "read lib/app/app.dart" {
		t.Errorf("activity = %+v", act.Entries)
	}
}

func TestLogTail(t *testing.T) {
	s, _ := newServer(t)
	if rec := get(t, s.Handler(), "localhost", "/api/conversations/5/log"); !strings.Contains(rec.Body.String(), "還沒有 log") {
		t.Errorf("missing log body = %q", rec.Body.String())
	}
	big := strings.Repeat("x", logTailBytes) + "THE END"
	if err := os.WriteFile(filepath.Join(s.LogDir, "conv-5.log"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := get(t, s.Handler(), "localhost", "/api/conversations/5/log")
	if body := rec.Body.String(); len(body) != logTailBytes || !strings.HasSuffix(body, "THE END") {
		t.Errorf("tail len %d", len(body))
	}
	if rec := get(t, s.Handler(), "localhost", "/api/conversations/x/log"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id status = %d", rec.Code)
	}
}
