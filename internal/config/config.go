// Package config loads chasage-bot settings from a YAML file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Repo describes one GitHub repository the bot works on.
type Repo struct {
	// Slug is the GitHub owner/name, e.g. LidoxLee/chasage-backend.
	Slug string `yaml:"slug"`
	// Dir is the directory name used inside clones and task workspaces.
	Dir string `yaml:"dir"`
	// Base is the branch PRs target.
	Base string `yaml:"base"`
}

// Agent configures the Cursor CLI.
type Agent struct {
	Bin       string        `yaml:"bin"`
	Model     string        `yaml:"model"`
	ExtraArgs []string      `yaml:"extra_args"`
	Timeout   time.Duration `yaml:"timeout"`
}

// Release configures the iOS build and TestFlight upload.
type Release struct {
	ASCKeyID         string        `yaml:"asc_key_id"`
	ASCIssuerID      string        `yaml:"asc_issuer_id"`
	BuildNumberStart int           `yaml:"build_number_start"`
	Timeout          time.Duration `yaml:"timeout"`
}

// Config is the full bot configuration.
type Config struct {
	TelegramToken string `yaml:"telegram_token"`
	// AllowedChatID is the only Telegram chat the bot obeys. 0 means setup mode:
	// the bot replies to /start with the sender's chat id and ignores everything else.
	AllowedChatID int64 `yaml:"allowed_chat_id"`

	Workdir   string `yaml:"workdir"`
	StatePath string `yaml:"state_path"`
	LogDir    string `yaml:"log_dir"`
	// Path, when set, replaces PATH for every subprocess.
	Path string `yaml:"path"`

	Backend Repo `yaml:"backend"`
	App     Repo `yaml:"app"`
	// DeployWorkflow is the backend workflow file that deploys main to EC2.
	DeployWorkflow string `yaml:"deploy_workflow"`

	ChecksTimeout time.Duration `yaml:"checks_timeout"`
	DeployTimeout time.Duration `yaml:"deploy_timeout"`

	Agent   Agent   `yaml:"agent"`
	Release Release `yaml:"release"`
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg.applyDefaults(home)
	if err := cfg.validate(home); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ascKeyDirs mirrors where the app Makefile's upload-testflight target looks for AuthKey_<id>.p8.
func ascKeyDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, "private_keys"),
		filepath.Join(home, ".private_keys"),
		filepath.Join(home, ".appstoreconnect", "private_keys"),
	}
	if d := os.Getenv("API_PRIVATE_KEYS_DIR"); d != "" {
		dirs = append([]string{d}, dirs...)
	}
	return dirs
}

func ascKeyExists(home, keyID string) bool {
	for _, d := range ascKeyDirs(home) {
		if _, err := os.Stat(filepath.Join(d, "AuthKey_"+keyID+".p8")); err == nil {
			return true
		}
	}
	return false
}

func (c *Config) applyDefaults(home string) {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	defDur := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	def(&c.Workdir, filepath.Join(home, "chasage-bot"))
	def(&c.StatePath, filepath.Join(home, "Library", "Application Support", "chasage-bot", "state.json"))
	def(&c.LogDir, filepath.Join(home, "Library", "Logs", "chasage-bot"))
	def(&c.Backend.Slug, "LidoxLee/chasage-backend")
	def(&c.Backend.Dir, "chasage-backend")
	def(&c.Backend.Base, "main")
	def(&c.App.Slug, "LidoxLee/chasage")
	def(&c.App.Dir, "chasage-app")
	def(&c.App.Base, "main")
	def(&c.DeployWorkflow, "ecr.yml")
	def(&c.Agent.Bin, "agent")
	defDur(&c.ChecksTimeout, 30*time.Minute)
	defDur(&c.DeployTimeout, 30*time.Minute)
	defDur(&c.Agent.Timeout, 60*time.Minute)
	defDur(&c.Release.Timeout, 60*time.Minute)
	if c.Agent.ExtraArgs == nil {
		c.Agent.ExtraArgs = []string{"--force", "--trust"}
	}
	if c.Release.BuildNumberStart == 0 {
		c.Release.BuildNumberStart = 1
	}
	c.Workdir = expandHome(c.Workdir, home)
	c.StatePath = expandHome(c.StatePath, home)
	c.LogDir = expandHome(c.LogDir, home)
}

func (c *Config) validate(home string) error {
	var errs []error
	if c.TelegramToken == "" {
		errs = append(errs, errors.New("telegram_token is required"))
	}
	if c.AllowedChatID == 0 {
		return errors.Join(errs...)
	}
	if c.Release.ASCKeyID == "" || c.Release.ASCIssuerID == "" {
		errs = append(errs, errors.New("release.asc_key_id and release.asc_issuer_id are required"))
	} else if !ascKeyExists(home, c.Release.ASCKeyID) {
		errs = append(errs, fmt.Errorf("release.asc_key_id: AuthKey_%s.p8 not found in %s",
			c.Release.ASCKeyID, strings.Join(ascKeyDirs(home), ", ")))
	}
	return errors.Join(errs...)
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
