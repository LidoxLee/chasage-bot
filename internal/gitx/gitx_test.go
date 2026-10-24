package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/LidoxLee/chasage-bot/internal/shell"
)

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setupOrigin creates a bare origin with one commit, a "seed" clone that can push to it, and a "base" clone.
func setupOrigin(t *testing.T) (root, seed, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	root = t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed = filepath.Join(root, "seed")
	base = filepath.Join(root, "base")
	gitCmd(t, root, "init", "--bare", "-b", "main", origin)
	gitCmd(t, root, "clone", origin, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "main")
	gitCmd(t, root, "clone", origin, base)
	return root, seed, base
}

func TestRefreshIfPristine(t *testing.T) {
	root, seed, base := setupOrigin(t)
	ctx := context.Background()
	g := Git{R: shell.Exec{}}
	wt := filepath.Join(root, "wt")
	if err := g.AddWorktree(ctx, nil, base, wt, "bot/conv-1", "main"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(seed, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "upstream change")
	gitCmd(t, seed, "push", "origin", "main")

	if ok, err := g.RefreshIfPristine(ctx, nil, wt, "main"); err != nil || !ok {
		t.Fatalf("pristine worktree: refreshed=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "new.txt")); err != nil {
		t.Fatal("worktree did not pick up the upstream change")
	}

	if err := os.WriteFile(filepath.Join(wt, "mine.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitLeftovers(ctx, nil, wt, "feat: mine"); err != nil {
		t.Fatal(err)
	}
	before, _ := g.Head(ctx, nil, wt)
	if ok, err := g.RefreshIfPristine(ctx, nil, wt, "main"); err != nil || ok {
		t.Fatalf("worktree with commits: refreshed=%v err=%v", ok, err)
	}
	if after, _ := g.Head(ctx, nil, wt); after != before {
		t.Error("refresh moved a worktree that has commits")
	}
}

// TestWorktreeLifecycle runs real git against a local bare "origin".
func TestWorktreeLifecycle(t *testing.T) {
	root, _, base := setupOrigin(t)
	ctx := context.Background()
	g := Git{R: shell.Exec{}}
	wt := filepath.Join(root, "tasks", "1", "repo")
	if err := g.AddWorktree(ctx, nil, base, wt, "bot/task-1", "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddWorktree(ctx, nil, base, wt, "bot/task-1", "main"); err != nil {
		t.Fatalf("re-adding existing worktree: %v", err)
	}

	if n, err := g.CommitsAhead(ctx, nil, wt, "main"); err != nil || n != 0 {
		t.Fatalf("ahead before change = %d %v", n, err)
	}
	if made, err := g.CommitLeftovers(ctx, nil, wt, "x"); err != nil || made {
		t.Fatalf("clean tree: made=%v err=%v", made, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if made, err := g.CommitLeftovers(ctx, nil, wt, "chore: leftovers"); err != nil || !made {
		t.Fatalf("dirty tree: made=%v err=%v", made, err)
	}
	if n, err := g.CommitsAhead(ctx, nil, wt, "main"); err != nil || n != 1 {
		t.Fatalf("ahead after commit = %d %v", n, err)
	}
	if err := g.Push(ctx, nil, wt, "bot/task-1"); err != nil {
		t.Fatal(err)
	}

	g.RemoveWorktree(ctx, nil, base, wt, "bot/task-1")
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}

	if err := os.WriteFile(filepath.Join(base, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.ResetToBase(ctx, nil, base, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "junk.txt")); !os.IsNotExist(err) {
		t.Error("ResetToBase kept untracked file")
	}
}
