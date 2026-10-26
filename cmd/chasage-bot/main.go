// Command chasage-bot is a Telegram-driven local dev assistant: it runs the Cursor CLI on this machine,
// opens PRs, merges on approval, waits for the backend deploy and uploads the iOS app to TestFlight.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/LidoxLee/chasage-bot/internal/activity"
	"github.com/LidoxLee/chasage-bot/internal/agent"
	"github.com/LidoxLee/chasage-bot/internal/bot"
	"github.com/LidoxLee/chasage-bot/internal/config"
	"github.com/LidoxLee/chasage-bot/internal/gh"
	"github.com/LidoxLee/chasage-bot/internal/gitx"
	"github.com/LidoxLee/chasage-bot/internal/release"
	"github.com/LidoxLee/chasage-bot/internal/shell"
	"github.com/LidoxLee/chasage-bot/internal/store"
	"github.com/LidoxLee/chasage-bot/internal/telegram"
	"github.com/LidoxLee/chasage-bot/internal/web"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".config", "chasage-bot", "config.yaml"), "config file")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := run(*cfgPath); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Path != "" {
		if err := os.Setenv("PATH", cfg.Path); err != nil {
			return err
		}
	}
	st, err := store.Open(cfg.StatePath, cfg.Release.BuildNumberStart)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runner := shell.Exec{}
	git := gitx.Git{R: runner}
	tg := telegram.New(cfg.TelegramToken)
	hub := activity.New(500)
	if cfg.DashboardAddr != "off" {
		dash := &web.Server{Store: st, Hub: hub, LogDir: cfg.LogDir}
		go func() {
			if err := web.Serve(ctx, cfg.DashboardAddr, dash.Handler()); err != nil {
				slog.Error("dashboard", "addr", cfg.DashboardAddr, "err", err)
			}
		}()
	}
	b := bot.New(bot.Deps{
		Activity: hub,
		Cfg:      cfg,
		Store:    st,
		TG:       tg,
		Out:      bot.ChatNotifier{TG: tg, ChatID: cfg.AllowedChatID},
		Git:      git,
		GH: gh.GH{
			R:                   runner,
			Dir:                 cfg.Workdir,
			Poll:                20 * time.Second,
			ChecksAppearTimeout: 2 * time.Minute,
		},
		Agent: agent.Agent{R: runner, Bin: cfg.Agent.Bin, Model: cfg.Agent.Model, ExtraArgs: cfg.Agent.ExtraArgs},
		Release: release.Releaser{
			R:           runner,
			Git:         git,
			Slug:        cfg.App.Slug,
			Dir:         filepath.Join(cfg.Workdir, "release", cfg.App.Dir),
			Base:        cfg.App.Base,
			ASCKeyID:    cfg.Release.ASCKeyID,
			ASCIssuerID: cfg.Release.ASCIssuerID,
		},
	})
	slog.Info("chasage-bot started", "config", cfgPath, "workdir", cfg.Workdir, "setup_mode", cfg.AllowedChatID == 0,
		"dashboard", "http://"+cfg.DashboardAddr)
	return b.Run(ctx)
}
