package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LidoxLee/chasage-bot/internal/task"
)

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	st, err := Open(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := st.Create("做兒童模式", task.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if tk.ID != 1 || tk.Status != task.Idle || tk.Mode != task.Plan || tk.Prompt != "做兒童模式" {
		t.Fatalf("created %+v", tk)
	}
	if a, ok := st.Active(); !ok || a.ID != tk.ID {
		t.Fatalf("active = %+v %v", a, ok)
	}
	if _, err := st.Update(tk.ID, func(x *task.Task) error {
		x.MessageIDs = append(x.MessageIDs, 42)
		return x.SetStatus(task.PROpen)
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTelegramOffset(7); err != nil {
		t.Fatal(err)
	}
	if err := st.AdvanceBuildNumber(3); err != nil {
		t.Fatal(err)
	}

	re, err := Open(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := re.Get(1)
	if !ok || got.Status != task.PROpen {
		t.Fatalf("reloaded %+v", got)
	}
	if a, ok := re.Active(); !ok || a.ID != 1 {
		t.Error("active not persisted")
	}
	if _, ok := re.FindByMessage(42); !ok {
		t.Error("FindByMessage(42) not found")
	}
	if re.TelegramOffset() != 7 {
		t.Errorf("offset = %d", re.TelegramOffset())
	}
	if re.BuildNumber() != 4 {
		t.Errorf("build number = %d", re.BuildNumber())
	}
	if next, _ := re.Create("x", task.Ask); next.ID != 2 {
		t.Errorf("next id = %d", next.ID)
	}
	if a, _ := re.Active(); a.ID != 2 {
		t.Errorf("active after create = %d", a.ID)
	}
	if err := re.ClearActive(1); err != nil {
		t.Fatal(err)
	}
	if a, _ := re.Active(); a.ID != 2 {
		t.Error("ClearActive cleared a different conversation")
	}
	if err := re.ClearActive(2); err != nil {
		t.Fatal(err)
	}
	if _, ok := re.Active(); ok {
		t.Error("still active")
	}
}

func TestOpenMigratesOldState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"next_id":3,"telegram_offset":5,"build_number":3,"tasks":{
		"1":{"id":1,"prompt":"q","status":"failed"},
		"2":{"id":2,"prompt":"x","status":"failed","approved":true},
		"3":{"id":3,"prompt":"y","status":"coding"}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]task.Status{1: task.Closed, 2: task.Failed, 3: task.Idle}
	for id, s := range want {
		if got, _ := st.Get(id); got.Status != s || got.Mode != task.Agent {
			t.Errorf("#%d = %s %s, want %s agent", id, got.Status, got.Mode, s)
		}
	}
	if _, ok := st.Active(); ok {
		t.Error("old state should have no active conversation")
	}
}

func TestUpdateErrorDoesNotPersist(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := st.Create("x", task.Plan)
	_, err = st.Update(tk.ID, func(x *task.Task) error {
		x.Prompt = "changed"
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	if got, _ := st.Get(tk.ID); got.Prompt != "x" {
		t.Errorf("prompt = %q", got.Prompt)
	}
}

func TestBuildNumberStartRaisesFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if _, err := Open(path, 1); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.BuildNumber() != 10 {
		t.Errorf("build number = %d", st.BuildNumber())
	}
}
