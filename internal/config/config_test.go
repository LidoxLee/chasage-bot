package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExampleAppliesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if cfg.Workdir != filepath.Join(home, "chasage-bot") {
		t.Errorf("workdir = %q", cfg.Workdir)
	}
	if cfg.Backend.Slug != "LidoxLee/chasage-backend" || cfg.App.Dir != "chasage-app" || cfg.DeployWorkflow != "ecr.yml" {
		t.Errorf("repo defaults = %+v %+v %q", cfg.Backend, cfg.App, cfg.DeployWorkflow)
	}
	if cfg.Agent.Timeout != 60*time.Minute || len(cfg.Agent.ExtraArgs) != 2 {
		t.Errorf("agent = %+v", cfg.Agent)
	}
	if cfg.Release.BuildNumberStart != 3 {
		t.Errorf("build_number_start = %d", cfg.Release.BuildNumberStart)
	}
}

func TestLoadRequiresASCOnceConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("telegram_token: x\nallowed_chat_id: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "asc_key_id") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRequiresASCKeyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("API_PRIVATE_KEYS_DIR", "")
	path := filepath.Join(home, "c.yaml")
	raw := "telegram_token: x\nallowed_chat_id: 5\nrelease:\n  asc_key_id: ABC123\n  asc_issuer_id: issuer\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "AuthKey_ABC123.p8 not found") {
		t.Fatalf("missing key: err = %v", err)
	}
	keyDir := filepath.Join(home, ".appstoreconnect", "private_keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "AuthKey_ABC123.p8"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("with key: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	if got := expandHome("~/x", "/h"); got != "/h/x" {
		t.Errorf("got %q", got)
	}
	if got := expandHome("/abs", "/h"); got != "/abs" {
		t.Errorf("got %q", got)
	}
}
