// Package web serves a local, read-only dashboard of conversations and their live activity.
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/activity"
	"github.com/LidoxLee/chasage-bot/internal/store"
	"github.com/LidoxLee/chasage-bot/internal/task"
)

//go:embed index.html
var indexHTML []byte

// logTailBytes bounds how much of a conversation log the dashboard fetches.
const logTailBytes = 64 * 1024

// Server exposes the bot's state over HTTP.
type Server struct {
	Store  *store.Store
	Hub    *activity.Hub
	LogDir string
}

type convView struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	StatusLabel string    `json:"status_label"`
	Mode        string    `json:"mode"`
	Turn        string    `json:"turn"`
	Active      bool      `json:"active"`
	PRs         []task.PR `json:"prs"`
	BuildNumber int       `json:"build_number,omitempty"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Handler returns the dashboard routes, restricted to loopback Host headers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/conversations", s.conversations)
	mux.HandleFunc("GET /api/conversations/{id}/activity", s.activity)
	mux.HandleFunc("GET /api/conversations/{id}/log", s.log)
	return localOnly(mux)
}

// localOnly rejects requests whose Host is not loopback, which blocks DNS-rebinding pages
// in the browser from reading the dashboard.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) conversations(w http.ResponseWriter, _ *http.Request) {
	activeID := 0
	if a, ok := s.Store.Active(); ok {
		activeID = a.ID
	}
	tasks := s.Store.List()
	out := make([]convView, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, convView{
			ID:          t.ID,
			Title:       t.Title(),
			Status:      string(t.Status),
			StatusLabel: t.Status.Label(),
			Mode:        string(t.Mode),
			Turn:        string(t.Turn),
			Active:      t.ID == activeID,
			PRs:         t.PRs,
			BuildNumber: t.BuildNumber,
			Error:       t.Error,
			UpdatedAt:   t.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	writeJSON(w, map[string]any{"conversations": out, "next_build_number": s.Store.BuildNumber()})
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	writeJSON(w, map[string]any{"entries": s.Hub.Since(id, since)})
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	text, err := tail(filepath.Join(s.LogDir, fmt.Sprintf("conv-%d.log", id)), logTailBytes)
	if errors.Is(err, os.ErrNotExist) {
		text, err = "（還沒有 log）", nil
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, text)
}

func tail(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if off := st.Size() - n; off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	return string(b), err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("dashboard encode", "err", err)
	}
}

// Serve runs the dashboard on addr until ctx is done.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
