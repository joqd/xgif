package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	downloadTimeout  = 2 * time.Minute
	downloadAttempts = 2
	maxFileSize      = "50M"
)

var xHosts = map[string]bool{
	"x.com":           true,
	"www.x.com":       true,
	"twitter.com":     true,
	"www.twitter.com": true,
}

var urlPattern = regexp.MustCompile(`https?://\S+`)

// findURL returns the first http(s) URL in text, or an empty string.
func findURL(text string) string {
	return strings.TrimRight(urlPattern.FindString(text), `.,;:!?)"'`)
}

// parseTweetURL validates an X/Twitter status URL and returns the tweet ID.
func parseTweetURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid URL")
	}
	if !xHosts[strings.ToLower(u.Hostname())] {
		return "", errors.New("not an X/Twitter URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "status" && isTweetID(parts[i+1]) {
			return parts[i+1], nil
		}
	}
	return "", errors.New("no tweet ID in URL")
}

func isTweetID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func canonicalURL(tweetID string) string {
	return "https://x.com/i/status/" + tweetID
}

func checkYtDlp() error {
	path, err := exec.LookPath("yt-dlp")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput(); err != nil {
		return fmt.Errorf("yt-dlp failed to run: %w: %s", err, lastLine(out))
	}
	return nil
}

// download fetches the tweet media into dir, retrying once on failure.
func download(ctx context.Context, tweetID, dir string) (string, error) {
	var err error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		var path string
		if path, err = runYtDlp(ctx, canonicalURL(tweetID), dir); err == nil {
			return path, nil
		}
		slog.Warn("download attempt failed", "tweet", tweetID, "attempt", attempt, "err", err)
		if attempt == downloadAttempts || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
		}
	}
	return "", err
}

func runYtDlp(ctx context.Context, tweetURL, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	args := []string{
		"--no-playlist", "--no-warnings", "--no-progress", "--no-cache-dir", "--no-part",
		"--max-filesize", maxFileSize,
		"-f", "best[ext=mp4]/best",
		"-o", filepath.Join(dir, "anim.%(ext)s"),
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
	}
	args = append(args, "--", tweetURL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("yt-dlp: %w: %s", err, lastLine(out))
	}
	files, err := filepath.Glob(filepath.Join(dir, "anim.*"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("yt-dlp produced no file: %s", lastLine(out))
	}
	return files[0], nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	line := lines[len(lines)-1]
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}
