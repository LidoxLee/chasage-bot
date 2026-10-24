// Package store persists bot state as a JSON file.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/task"
)

type state struct {
	NextID         int `json:"next_id"`
	TelegramOffset int `json:"telegram_offset"`
	BuildNumber    int `json:"build_number"`
	// ActiveID is the conversation plain messages go to; 0 means none.
	ActiveID int                `json:"active_id"`
	Tasks    map[int]*task.Task `json:"tasks"`
}

// Store is a goroutine-safe, file-backed state container.
type Store struct {
	mu   sync.Mutex
	path string
	s    state
}

// Open loads state from path, creating an empty state if the file does not exist.
func Open(path string, buildNumberStart int) (*Store, error) {
	st := &Store{path: path, s: state{NextID: 1, Tasks: map[int]*task.Task{}}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read state: %w", err)
	default:
		if err := json.Unmarshal(raw, &st.s); err != nil {
			return nil, fmt.Errorf("parse state: %w", err)
		}
		if st.s.Tasks == nil {
			st.s.Tasks = map[int]*task.Task{}
		}
		for _, t := range st.s.Tasks {
			t.Migrate()
		}
	}
	if st.s.BuildNumber < buildNumberStart {
		st.s.BuildNumber = buildNumberStart
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return st, st.saveLocked()
}

func (st *Store) saveLocked() error {
	raw, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// Create starts a new conversation, makes it the active one and returns a copy.
func (st *Store) Create(prompt string, mode task.Mode) (task.Task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	t := &task.Task{
		ID:        st.s.NextID,
		Prompt:    prompt,
		Status:    task.Idle,
		Mode:      mode,
		CreatedAt: now,
		UpdatedAt: now,
	}
	st.s.NextID++
	st.s.Tasks[t.ID] = t
	st.s.ActiveID = t.ID
	return t.Clone(), st.saveLocked()
}

// Active returns the active conversation, if any.
func (st *Store) Active() (task.Task, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	t, ok := st.s.Tasks[st.s.ActiveID]
	if !ok {
		return task.Task{}, false
	}
	return t.Clone(), true
}

// ClearActive forgets the active conversation if it is id.
func (st *Store) ClearActive(id int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.ActiveID != id {
		return nil
	}
	st.s.ActiveID = 0
	return st.saveLocked()
}

// Get returns a copy of task id.
func (st *Store) Get(id int) (task.Task, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	t, ok := st.s.Tasks[id]
	if !ok {
		return task.Task{}, false
	}
	return t.Clone(), true
}

// Update mutates task id under the lock and persists it. If fn returns an error nothing is saved.
func (st *Store) Update(id int, fn func(*task.Task) error) (task.Task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	cur, ok := st.s.Tasks[id]
	if !ok {
		return task.Task{}, fmt.Errorf("找不到對話 #%d", id)
	}
	work := cur.Clone()
	if err := fn(&work); err != nil {
		return cur.Clone(), err
	}
	work.UpdatedAt = time.Now()
	st.s.Tasks[id] = &work
	return work.Clone(), st.saveLocked()
}

// List returns copies of all tasks ordered by id.
func (st *Store) List() []task.Task {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]task.Task, 0, len(st.s.Tasks))
	for _, t := range st.s.Tasks {
		out = append(out, t.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// FindByMessage returns the task that owns a Telegram message id.
func (st *Store) FindByMessage(msgID int) (task.Task, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, t := range st.s.Tasks {
		for _, m := range t.MessageIDs {
			if m == msgID {
				return t.Clone(), true
			}
		}
	}
	return task.Task{}, false
}

// TelegramOffset returns the next update offset to request.
func (st *Store) TelegramOffset() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.TelegramOffset
}

// SetTelegramOffset persists the update offset.
func (st *Store) SetTelegramOffset(off int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.TelegramOffset = off
	return st.saveLocked()
}

// BuildNumber returns the build number the next upload will use.
func (st *Store) BuildNumber() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.BuildNumber
}

// SetBuildNumber overrides the next build number.
func (st *Store) SetBuildNumber(n int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.BuildNumber = n
	return st.saveLocked()
}

// AdvanceBuildNumber moves past used, which must be the number just uploaded.
func (st *Store) AdvanceBuildNumber(used int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.BuildNumber <= used {
		st.s.BuildNumber = used + 1
	}
	return st.saveLocked()
}
