package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

const (
	maxCookieSize  = 1 << 20
	broadcastDelay = 50 * time.Millisecond
)

func (a *App) isAdmin(c tele.Context) bool {
	sender := c.Sender()
	if sender == nil || sender.ID != a.cfg.AdminID {
		if sender != nil {
			slog.Warn("unauthorized admin command", "user", sender.ID)
		}
		return false
	}
	return true
}

func (a *App) onCookies(c tele.Context) error {
	if !a.isAdmin(c) {
		return nil
	}
	reply := c.Message().ReplyTo
	if reply == nil || reply.Document == nil {
		return c.Send(msgCookiesReply)
	}
	if err := a.saveCookies(reply.Document); err != nil {
		slog.Error("cookie update failed", "err", err)
		return c.Send(msgCookiesFailed + err.Error())
	}
	slog.Info("admin: cookies updated")
	return c.Send(msgCookiesOK)
}

// saveCookies downloads, validates and atomically installs the cookies file.
func (a *App) saveCookies(doc *tele.Document) error {
	if doc.FileSize > maxCookieSize {
		return errors.New("file is too large")
	}
	tmp, err := os.CreateTemp(filepath.Dir(cookiesPath), ".cookies-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	file := doc.File
	if err := a.bot.Download(&file, tmpPath); err != nil {
		return fmt.Errorf("download from telegram: %w", err)
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	if err := validateCookies(data); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, cookiesPath)
}

// validateCookies checks that data looks like a Netscape cookies file.
// Errors never include file contents.
func validateCookies(data []byte) error {
	if len(data) == 0 || len(data) > maxCookieSize {
		return errors.New("file is empty or too large")
	}
	valid := 0
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimPrefix(strings.TrimRight(line, "\r"), "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(strings.Split(line, "\t")) != 7 {
			return fmt.Errorf("line %d is not in Netscape cookie format", i+1)
		}
		valid++
	}
	if valid == 0 {
		return errors.New("no cookies found in file")
	}
	return nil
}

func (a *App) onBroadcast(c tele.Context) error {
	if !a.isAdmin(c) {
		return nil
	}
	reply := c.Message().ReplyTo
	if reply == nil {
		return c.Send(msgBroadcastReply)
	}
	if !a.broadcasting.CompareAndSwap(false, true) {
		return c.Send(msgBroadcastBusy)
	}
	defer a.broadcasting.Store(false)

	ids, err := a.db.userIDs()
	if err != nil {
		slog.Error("broadcast: list users failed", "err", err)
		return c.Send(msgTempError)
	}
	slog.Info("admin: broadcast started", "recipients", len(ids))
	if err := c.Send(msgBroadcastStart); err != nil {
		return err
	}
	sent, failed := a.broadcast(reply, ids)
	slog.Info("admin: broadcast finished", "sent", sent, "failed", failed)
	return c.Send(fmt.Sprintf("✅ ارسال همگانی تمام شد.\n\nموفق: %d\nناموفق: %d", sent, failed))
}

// broadcast copies msg to every user at a gentle pace and continues after failures.
func (a *App) broadcast(msg *tele.Message, ids []int64) (sent, failed int) {
	for _, id := range ids {
		err := a.copyTo(id, msg)
		if err == nil {
			sent++
		} else {
			failed++
		}
		time.Sleep(broadcastDelay)
	}
	return sent, failed
}

func (a *App) copyTo(userID int64, msg *tele.Message) error {
	_, err := a.bot.Copy(tele.ChatID(userID), msg)
	if err == nil || errors.Is(err, tele.ErrBlockedByUser) || errors.Is(err, tele.ErrUserIsDeactivated) {
		return err
	}
	// One retry for transient errors such as rate limits.
	time.Sleep(2 * time.Second)
	_, err = a.bot.Copy(tele.ChatID(userID), msg)
	return err
}
