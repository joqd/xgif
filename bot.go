package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	tele "gopkg.in/telebot.v3"
)

const freeDownloads = 3

type Job struct {
	userID  int64
	chatID  int64
	tweetID string
	status  *tele.Message // set once before the job is shared; may be nil
}

// userGate allows only one active request per user.
type userGate struct {
	mu   sync.Mutex
	busy map[int64]bool
}

func (g *userGate) acquire(id int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy == nil {
		g.busy = make(map[int64]bool)
	}
	if g.busy[id] {
		return false
	}
	g.busy[id] = true
	return true
}

func (g *userGate) release(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.busy, id)
}

type App struct {
	cfg *Config
	db  *DB
	bot *tele.Bot

	queue        chan string // tweet IDs waiting for a worker
	wg           sync.WaitGroup
	jobCtx       context.Context
	cancelJobs   context.CancelFunc
	users        userGate
	broadcasting atomic.Bool

	mu       sync.Mutex // guards stopping and inflight
	stopping bool
	inflight map[string][]*Job // tweet ID -> jobs waiting for that download
}

func newApp(cfg *Config, db *DB) (*App, error) {
	bot, err := tele.NewBot(tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: 30 * time.Second},
		Client: &http.Client{Timeout: 2 * time.Minute},
		OnError: func(err error, c tele.Context) {
			slog.Error("telegram handler error", "err", err)
		},
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		cfg:        cfg,
		db:         db,
		bot:        bot,
		queue:      make(chan string, cfg.MaxQueueSize),
		jobCtx:     ctx,
		cancelJobs: cancel,
		inflight:   make(map[string][]*Job),
	}, nil
}

func (a *App) start() {
	a.register()
	for i := 0; i < a.cfg.MaxWorkers; i++ {
		a.wg.Add(1)
		go a.worker()
	}
}

// shutdown stops accepting jobs and waits for active ones.
func (a *App) shutdown(wait time.Duration) {
	a.mu.Lock()
	a.stopping = true
	close(a.queue)
	a.mu.Unlock()

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(wait):
		slog.Warn("active jobs did not finish in time, cancelling")
		a.cancelJobs()
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

func (a *App) isStopping() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopping
}

// accept runs the quota check and then caches, joins or queues the job.
func (a *App) accept(job *Job) {
	if !a.users.acquire(job.userID) {
		a.say(job.chatID, msgBusy)
		return
	}
	ok, err := a.allowed(job.userID)
	switch {
	case err != nil:
		slog.Error("quota check failed", "user", job.userID, "err", err)
		a.say(job.chatID, msgTempError)
	case !ok:
		a.say(job.chatID, msgJoin)
	default:
		a.enqueue(job)
		return
	}
	a.users.release(job.userID)
}

// allowed reports whether the user may start another download.
func (a *App) allowed(userID int64) (bool, error) {
	n, err := a.db.downloads(userID)
	if err != nil {
		return false, err
	}
	if n < freeDownloads {
		return true, nil
	}
	return a.isMember(userID)
}

func (a *App) isMember(userID int64) (bool, error) {
	m, err := a.bot.ChatMemberOf(&tele.Chat{ID: a.cfg.SponsorChannelID}, &tele.User{ID: userID})
	if err != nil {
		return false, err
	}
	switch m.Role {
	case tele.Creator, tele.Administrator, tele.Member, tele.Restricted:
		return true, nil
	}
	return false, nil
}

// enqueue delivers a cached animation or schedules a download.
// Every path ends by releasing the user's slot (directly or via deliver/fail).
func (a *App) enqueue(job *Job) {
	anim, err := a.db.animation(job.tweetID)
	if err != nil {
		slog.Error("cache lookup failed", "tweet", job.tweetID, "err", err)
	} else if anim != nil {
		a.deliver(job, anim)
		return
	}

	job.status = a.say(job.chatID, a.queuedText())

	a.mu.Lock()
	if a.stopping {
		a.mu.Unlock()
		a.fail(job)
		return
	}
	if jobs, ok := a.inflight[job.tweetID]; ok {
		a.inflight[job.tweetID] = append(jobs, job)
		a.mu.Unlock()
		return
	}
	// Re-check the cache under the lock: a worker may have just finished this tweet.
	if anim, err := a.db.animation(job.tweetID); err == nil && anim != nil {
		a.mu.Unlock()
		a.deliver(job, anim)
		return
	}
	a.inflight[job.tweetID] = []*Job{job}
	select {
	case a.queue <- job.tweetID:
		a.mu.Unlock()
	default:
		delete(a.inflight, job.tweetID)
		a.mu.Unlock()
		slog.Warn("queue is full", "user", job.userID)
		a.update(job, msgQueueFull)
		a.users.release(job.userID)
	}
}

func (a *App) queuedText() string {
	if n := len(a.queue); n > 0 {
		return fmt.Sprintf("%s\n\nتعداد درخواست‌های جلوتر از تو: %d", msgQueued, n)
	}
	return msgQueued
}

func (a *App) worker() {
	defer a.wg.Done()
	for tweetID := range a.queue {
		a.process(tweetID)
	}
}

func (a *App) process(tweetID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("job panicked", "tweet", tweetID, "panic", r)
			a.finish(tweetID, nil)
		}
	}()
	if a.isStopping() {
		a.finish(tweetID, nil)
		return
	}
	a.editAll(tweetID, msgDownloading)
	anim, err := a.fetch(tweetID)
	if err != nil {
		slog.Error("download failed", "tweet", tweetID, "err", err)
		a.finish(tweetID, nil)
		return
	}
	// Cache only after the upload to the storage channel succeeded.
	if err := a.db.saveAnimation(anim); err != nil {
		slog.Error("cache save failed", "tweet", tweetID, "err", err)
	}
	a.finish(tweetID, anim)
}

// fetch downloads the media into a private temp dir and uploads it to the storage channel.
func (a *App) fetch(tweetID string) (*Animation, error) {
	dir, err := os.MkdirTemp("", "xgif-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	slog.Info("download start", "tweet", tweetID)
	path, err := download(a.jobCtx, tweetID, dir)
	if err != nil {
		return nil, err
	}
	a.editAll(tweetID, msgUploading)
	anim, err := a.upload(tweetID, path)
	if err == nil {
		slog.Info("download complete", "tweet", tweetID)
	}
	return anim, err
}

func (a *App) upload(tweetID, path string) (*Animation, error) {
	file := &tele.Animation{File: tele.FromDisk(path), FileName: filepath.Base(path)}
	msg, err := a.bot.Send(tele.ChatID(a.cfg.StorageChannelID), file)
	if err != nil {
		return nil, fmt.Errorf("upload to storage channel: %w", err)
	}
	fileID := ""
	switch {
	case msg.Animation != nil:
		fileID = msg.Animation.FileID
	case msg.Document != nil:
		fileID = msg.Document.FileID
	}
	if fileID == "" {
		return nil, errors.New("telegram returned no file id")
	}
	return &Animation{
		TweetID:          tweetID,
		URL:              canonicalURL(tweetID),
		StorageMessageID: int64(msg.ID),
		FileID:           fileID,
	}, nil
}

// finish answers every job waiting on the tweet. A nil animation means failure.
func (a *App) finish(tweetID string, anim *Animation) {
	a.mu.Lock()
	jobs := a.inflight[tweetID]
	delete(a.inflight, tweetID)
	a.mu.Unlock()

	for _, job := range jobs {
		if anim != nil {
			a.deliver(job, anim)
		} else {
			a.fail(job)
		}
	}
}

func (a *App) deliver(job *Job, anim *Animation) {
	defer a.users.release(job.userID)
	if err := a.sendAnimation(job.chatID, anim); err != nil {
		slog.Error("send animation failed", "user", job.userID, "err", err)
		a.update(job, msgFailed)
		return
	}
	a.remove(job.status)

	n, err := a.db.incDownloads(job.userID)
	if err != nil {
		slog.Error("download count update failed", "user", job.userID, "err", err)
		return
	}
	if n == freeDownloads {
		if member, err := a.isMember(job.userID); err == nil && !member {
			a.say(job.chatID, msgThirdDone)
		}
	}
}

func (a *App) fail(job *Job) {
	defer a.users.release(job.userID)
	if a.isStopping() {
		a.update(job, msgShutdown)
		return
	}
	a.update(job, msgFailed)
}

// sendAnimation prefers the stored file ID and falls back to copying the storage message.
func (a *App) sendAnimation(chatID int64, anim *Animation) error {
	to := tele.ChatID(chatID)
	_, err := a.bot.Send(to, &tele.Animation{File: tele.File{FileID: anim.FileID}})
	if err == nil {
		return nil
	}
	slog.Warn("send by file id failed, copying instead", "err", err)
	stored := tele.StoredMessage{
		MessageID: strconv.FormatInt(anim.StorageMessageID, 10),
		ChatID:    a.cfg.StorageChannelID,
	}
	_, err = a.bot.Copy(to, stored)
	return err
}

func (a *App) say(chatID int64, text string) *tele.Message {
	msg, err := a.bot.Send(tele.ChatID(chatID), text)
	if err != nil {
		slog.Warn("send message failed", "err", err)
		return nil
	}
	return msg
}

// update edits the job's status message, or sends a new one if there is none.
func (a *App) update(job *Job, text string) {
	if job.status == nil {
		a.say(job.chatID, text)
		return
	}
	if _, err := a.bot.Edit(job.status, text); err != nil {
		slog.Warn("edit status failed", "err", err)
	}
}

func (a *App) editAll(tweetID, text string) {
	a.mu.Lock()
	jobs := append([]*Job(nil), a.inflight[tweetID]...)
	a.mu.Unlock()
	for _, job := range jobs {
		a.update(job, text)
	}
}

func (a *App) remove(msg *tele.Message) {
	if msg == nil {
		return
	}
	if err := a.bot.Delete(msg); err != nil {
		slog.Debug("delete status failed", "err", err)
	}
}
