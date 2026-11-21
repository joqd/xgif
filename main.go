package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const shutdownWait = 60 * time.Second

func main() {
	os.Exit(run())
}

// run returns exit code 2 for configuration problems (systemd does not restart on 2).
func run() int {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		return 2
	}
	slog.SetDefault(newLogger(cfg.BotToken))

	if err := checkYtDlp(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: yt-dlp is not installed or not available in PATH.")
		fmt.Fprintln(os.Stderr, "Please install yt-dlp before starting XGIF.")
		slog.Error("yt-dlp check failed", "err", err)
		return 2
	}

	db, err := openDB(dbPath)
	if err != nil {
		slog.Error("database error", "err", err)
		return 1
	}
	defer db.Close()

	app, err := newApp(cfg, db)
	if err != nil {
		slog.Error("cannot create telegram bot", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app.start()
	go app.bot.Start()
	slog.Info("xgif started", "workers", cfg.MaxWorkers, "queue", cfg.MaxQueueSize)

	<-ctx.Done()
	slog.Info("shutting down")
	app.bot.Stop()
	app.shutdown(shutdownWait)
	slog.Info("stopped")
	return 0
}

// newLogger writes to stderr (journald) and strips the bot token from all output.
func newLogger(token string) *slog.Logger {
	redact := func(s string) string { return strings.ReplaceAll(s, token, "[redacted]") }
	opts := &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch v := a.Value.Any().(type) {
			case error:
				a.Value = slog.StringValue(redact(v.Error()))
			case string:
				a.Value = slog.StringValue(redact(v))
			}
			return a
		},
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
