// Package gitx wraps the git and gh commands the bot needs for clones and worktrees.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/LidoxLee/chasage-bot/internal/shell"
)

// Git runs git operations through a shell.Runner.
type Git struct {
	R shell.Runner
}

func (g Git) git(ctx context.Context, log io.Writer, dir string, args ...string) (string, error) {
	return g.R.Run(ctx, log, shell.Cmd{Dir: dir, Name: "git", Args: args})
}

// EnsureClone clones slug into dir if missing, then fetches.
func (g Git) EnsureClone(ctx context.Context, log io.Writer, slug, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		_, err := g.R.Run(ctx, log, shell.Cmd{Dir: filepath.Dir(dir), Name: "gh", Args: []string{"repo", "clone", slug, dir}})
		if err != nil {
			return err
		}
	}
	_, err := g.git(ctx, log, dir, "fetch", "origin", "--prune")
	return err
}

// AddWorktree creates wtDir on a new branch from origin/base. An existing worktree is reused.
func (g Git) AddWorktree(ctx context.Context, log io.Writer, repoDir, wtDir, branch, base string) error {
	if _, err := os.Stat(wtDir); err == nil {
		return nil
	}
	_, err := g.git(ctx, log, repoDir, "worktree", "add", "-B", branch, wtDir, "origin/"+base)
	return err
}

// RemoveWorktree deletes a worktree and its local branch. Errors are ignored on purpose: cleanup is best effort.
func (g Git) RemoveWorktree(ctx context.Context, log io.Writer, repoDir, wtDir, branch string) {
	_, _ = g.git(ctx, log, repoDir, "worktree", "remove", "--force", wtDir)
	_, _ = g.git(ctx, log, repoDir, "worktree", "prune")
	_, _ = g.git(ctx, log, repoDir, "branch", "-D", branch)
}

// CommitLeftovers commits anything the agent left uncommitted. It reports whether a commit was made.
func (g Git) CommitLeftovers(ctx context.Context, log io.Writer, wtDir, msg string) (bool, error) {
	status, err := g.git(ctx, log, wtDir, "status", "--porcelain")
	if err != nil || status == "" {
		return false, err
	}
	if _, err := g.git(ctx, log, wtDir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := g.git(ctx, log, wtDir, "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// CommitsAhead counts commits on HEAD that are not on origin/base.
func (g Git) CommitsAhead(ctx context.Context, log io.Writer, wtDir, base string) (int, error) {
	out, err := g.git(ctx, log, wtDir, "rev-list", "--count", "origin/"+base+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("rev-list count %q: %w", out, err)
	}
	return n, nil
}

// Push pushes the current branch to origin.
func (g Git) Push(ctx context.Context, log io.Writer, wtDir, branch string) error {
	_, err := g.git(ctx, log, wtDir, "push", "-u", "origin", branch)
	return err
}

// ResetToBase hard-resets a clone's working tree to origin/base, dropping untracked (but not ignored) files.
func (g Git) ResetToBase(ctx context.Context, log io.Writer, dir, base string) error {
	steps := [][]string{
		{"fetch", "origin", "--prune"},
		{"checkout", "-B", base, "origin/" + base},
		{"reset", "--hard", "origin/" + base},
		{"clean", "-fd"},
	}
	for _, args := range steps {
		if _, err := g.git(ctx, log, dir, args...); err != nil {
			return err
		}
	}
	return nil
}
