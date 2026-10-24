// Package gh wraps the GitHub CLI for PRs, checks and workflow runs.
package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/shell"
)

// GH runs gh commands. Dir should be a directory outside any git repo so -R is authoritative.
type GH struct {
	R   shell.Runner
	Dir string
	// Poll is the interval between status polls.
	Poll time.Duration
	// ChecksAppearTimeout is how long to wait for a PR's first check to show up.
	ChecksAppearTimeout time.Duration
}

func (g GH) run(ctx context.Context, log io.Writer, args ...string) (string, error) {
	return g.R.Run(ctx, log, shell.Cmd{Dir: g.Dir, Name: "gh", Args: args})
}

// CreatePR opens a PR and returns its number and URL.
func (g GH) CreatePR(ctx context.Context, log io.Writer, slug, base, head, title, body string) (int, string, error) {
	out, err := g.R.Run(ctx, log, shell.Cmd{
		Dir:   g.Dir,
		Name:  "gh",
		Args:  []string{"pr", "create", "-R", slug, "--base", base, "--head", head, "--title", title, "--body-file", "-"},
		Stdin: body,
	})
	if err != nil {
		return 0, "", err
	}
	lines := shell.Lines(out)
	if len(lines) == 0 {
		return 0, "", errors.New("gh pr create: no URL in output")
	}
	url := lines[len(lines)-1]
	n, err := PRNumberFromURL(url)
	return n, url, err
}

// PRNumberFromURL extracts the number from https://github.com/o/r/pull/N.
func PRNumberFromURL(url string) (int, error) {
	i := strings.LastIndex(url, "/pull/")
	if i < 0 {
		return 0, fmt.Errorf("not a PR URL: %q", url)
	}
	return strconv.Atoi(strings.TrimSpace(url[i+len("/pull/"):]))
}

// PRInfo is the subset of `gh pr view` the bot uses.
type PRInfo struct {
	State       string `json:"state"` // OPEN, CLOSED, MERGED
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// ViewPR returns the PR's state and merge commit.
func (g GH) ViewPR(ctx context.Context, log io.Writer, slug string, n int) (PRInfo, error) {
	out, err := g.run(ctx, log, "pr", "view", strconv.Itoa(n), "-R", slug, "--json", "state,mergeCommit")
	if err != nil {
		return PRInfo{}, err
	}
	var info PRInfo
	return info, json.Unmarshal([]byte(out), &info)
}

// Check is one PR status check.
type Check struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"` // pass, fail, pending, skipping, cancel
}

// ChecksVerdict summarizes a set of checks.
type ChecksVerdict int

// Verdicts returned by EvaluateChecks.
const (
	ChecksPending ChecksVerdict = iota
	ChecksPassed
	ChecksFailed
	ChecksNone
)

// EvaluateChecks decides whether checks are done and green. It also returns failing check names.
func EvaluateChecks(checks []Check) (ChecksVerdict, []string) {
	if len(checks) == 0 {
		return ChecksNone, nil
	}
	var failed []string
	pending := false
	for _, c := range checks {
		switch c.Bucket {
		case "fail", "cancel":
			failed = append(failed, c.Name)
		case "pending":
			pending = true
		}
	}
	switch {
	case len(failed) > 0:
		return ChecksFailed, failed
	case pending:
		return ChecksPending, nil
	}
	return ChecksPassed, nil
}

// WaitChecks polls until every check on the PR passed. It fails on any failing check, or when no
// check appears within ChecksAppearTimeout.
func (g GH) WaitChecks(ctx context.Context, log io.Writer, slug string, n int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	for {
		// gh pr checks exits non-zero while checks are pending or failing; the JSON is still printed.
		out, runErr := g.run(ctx, log, "pr", "checks", strconv.Itoa(n), "-R", slug, "--json", "name,bucket")
		var checks []Check
		if out != "" {
			if err := json.Unmarshal([]byte(out), &checks); err != nil {
				return fmt.Errorf("parse checks: %w", err)
			}
		} else if runErr != nil && !strings.Contains(runErr.Error(), "no checks reported") {
			return runErr
		}
		verdict, failed := EvaluateChecks(checks)
		switch verdict {
		case ChecksPassed:
			return nil
		case ChecksFailed:
			return fmt.Errorf("checks 失敗：%s", strings.Join(failed, ", "))
		case ChecksNone:
			if time.Since(start) > g.ChecksAppearTimeout {
				return errors.New("等了很久都沒有任何 check，拒絕合併")
			}
		}
		if err := sleep(ctx, g.Poll); err != nil {
			return fmt.Errorf("等待 checks 逾時：%w", err)
		}
	}
}

// MergePR squash-merges the PR and deletes its remote branch.
func (g GH) MergePR(ctx context.Context, log io.Writer, slug string, n int) error {
	_, err := g.run(ctx, log, "pr", "merge", strconv.Itoa(n), "-R", slug, "--squash", "--delete-branch")
	return err
}

// ClosePR closes the PR and deletes its remote branch.
func (g GH) ClosePR(ctx context.Context, log io.Writer, slug string, n int) error {
	_, err := g.run(ctx, log, "pr", "close", strconv.Itoa(n), "-R", slug, "--delete-branch")
	return err
}

type run struct {
	ID         int64  `json:"databaseId"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// WaitWorkflow waits for the workflow run triggered by sha to finish successfully and returns its URL.
func (g GH) WaitWorkflow(ctx context.Context, log io.Writer, slug, workflow, sha string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		out, err := g.run(ctx, log, "run", "list", "-R", slug, "--workflow", workflow, "--commit", sha,
			"--json", "databaseId,status,conclusion,url", "--limit", "1")
		if err != nil {
			return "", err
		}
		var runs []run
		if err := json.Unmarshal([]byte(out), &runs); err != nil {
			return "", fmt.Errorf("parse runs: %w", err)
		}
		if len(runs) == 1 && runs[0].Status == "completed" {
			if runs[0].Conclusion != "success" {
				return runs[0].URL, fmt.Errorf("部署 workflow 結果是 %s", runs[0].Conclusion)
			}
			return runs[0].URL, nil
		}
		if err := sleep(ctx, g.Poll); err != nil {
			return "", fmt.Errorf("等待部署逾時：%w", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
