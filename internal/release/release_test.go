package release

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/LidoxLee/chasage-bot/internal/gitx"
	"github.com/LidoxLee/chasage-bot/internal/shell"
)

type recordingRunner struct {
	cmds []shell.Cmd
}

func (r *recordingRunner) Run(_ context.Context, _ io.Writer, c shell.Cmd) (string, error) {
	r.cmds = append(r.cmds, c)
	if c.Name == "make" && c.Args[0] == "build-ipa" {
		dir := filepath.Join(c.Dir, "build", "ios", "ipa")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return "", os.WriteFile(filepath.Join(dir, "chasage.ipa"), []byte("ipa"), 0o644)
	}
	return "", nil
}

func (r *recordingRunner) make(target string) (shell.Cmd, bool) {
	for _, c := range r.cmds {
		if c.Name == "make" && c.Args[0] == target {
			return c, true
		}
	}
	return shell.Cmd{}, false
}

func TestBuildAndUploadSignWithASCKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &recordingRunner{}
	r := Releaser{
		R:           run,
		Git:         gitx.Git{R: run},
		Slug:        "o/app",
		Dir:         dir,
		Base:        "main",
		ASCKeyID:    "KEY123",
		ASCIssuerID: "issuer-1",
	}
	ipa, err := r.Build(context.Background(), io.Discard, 11)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Upload(context.Background(), io.Discard, ipa); err != nil {
		t.Fatal(err)
	}

	want := []string{"ASC_KEY_ID=KEY123", "ASC_ISSUER_ID=issuer-1"}
	for _, target := range []string{"build-ipa", "upload-testflight"} {
		c, ok := run.make(target)
		if !ok {
			t.Fatalf("make %s not run", target)
		}
		for _, kv := range want {
			if !slices.Contains(c.Env, kv) {
				t.Fatalf("make %s env %v missing %s", target, c.Env, kv)
			}
		}
	}
}
