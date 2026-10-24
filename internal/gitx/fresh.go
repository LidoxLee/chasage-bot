package gitx

import (
	"context"
	"io"
)

// Head returns the commit sha HEAD points to.
func (g Git) Head(ctx context.Context, log io.Writer, wtDir string) (string, error) {
	return g.git(ctx, log, wtDir, "rev-parse", "HEAD")
}

// RefreshIfPristine moves a worktree to the latest origin/base when it has no commits and no local
// changes, so read-only conversations see current code. It reports whether it refreshed.
func (g Git) RefreshIfPristine(ctx context.Context, log io.Writer, wtDir, base string) (bool, error) {
	if _, err := g.git(ctx, log, wtDir, "fetch", "origin", base); err != nil {
		return false, err
	}
	ahead, err := g.CommitsAhead(ctx, log, wtDir, base)
	if err != nil || ahead > 0 {
		return false, err
	}
	status, err := g.git(ctx, log, wtDir, "status", "--porcelain")
	if err != nil || status != "" {
		return false, err
	}
	_, err = g.git(ctx, log, wtDir, "reset", "--hard", "origin/"+base)
	return err == nil, err
}
