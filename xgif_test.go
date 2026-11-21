package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestParseTweetURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string // empty means an error is expected
	}{
		{"https://x.com/user/status/1234567890", "1234567890"},
		{"https://twitter.com/user/status/123?s=20", "123"},
		{"https://www.x.com/i/status/42", "42"},
		{"http://www.twitter.com/user/status/12345/video/1", "12345"},
		{"https://evil.com/user/status/123", ""},
		{"https://x.com.evil.com/u/status/1", ""},
		{"ftp://x.com/u/status/1", ""},
		{"https://x.com/user", ""},
		{"https://x.com/user/status/abc", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got, err := parseTweetURL(tt.raw)
		if tt.want == "" {
			if err == nil {
				t.Errorf("parseTweetURL(%q) = %q, want error", tt.raw, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseTweetURL(%q) = %q, %v; want %q", tt.raw, got, err, tt.want)
		}
	}
}

func TestFindURL(t *testing.T) {
	got := findURL("look at this https://x.com/u/status/1, nice")
	if got != "https://x.com/u/status/1" {
		t.Errorf("findURL = %q", got)
	}
	if findURL("no link here") != "" {
		t.Error("expected empty result")
	}
}

func TestValidateCookies(t *testing.T) {
	valid := "# Netscape HTTP Cookie File\n" +
		".x.com\tTRUE\t/\tTRUE\t0\tauth_token\tabc\n" +
		"#HttpOnly_.x.com\tTRUE\t/\tTRUE\t0\tct0\tdef\n"
	if err := validateCookies([]byte(valid)); err != nil {
		t.Errorf("valid cookies rejected: %v", err)
	}
	for _, bad := range []string{"", "hello world", "# only a comment\n"} {
		if err := validateCookies([]byte(bad)); err == nil {
			t.Errorf("validateCookies(%q) should fail", bad)
		}
	}
}

func TestUserGateAllowsOneActiveRequest(t *testing.T) {
	var g userGate
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.acquire(1) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d goroutines acquired the gate, want 1", wins.Load())
	}
	g.release(1)
	if !g.acquire(1) {
		t.Error("gate should be free after release")
	}
}

func TestDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	d, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.upsertUser(1, "alice", "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := d.upsertUser(1, "alice2", "Alice"); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.downloads(1); n != 0 {
		t.Errorf("new user has %d downloads", n)
	}
	for want := 1; want <= 2; want++ {
		if n, err := d.incDownloads(1); err != nil || n != want {
			t.Errorf("incDownloads = %d, %v; want %d", n, err, want)
		}
	}
	if a, err := d.animation("99"); err != nil || a != nil {
		t.Errorf("missing animation = %v, %v", a, err)
	}
	first := &Animation{TweetID: "99", URL: "https://x.com/i/status/99", StorageMessageID: 10, FileID: "file-a"}
	second := &Animation{TweetID: "99", URL: "https://x.com/i/status/99", StorageMessageID: 11, FileID: "file-b"}
	for _, a := range []*Animation{first, second} {
		if err := d.saveAnimation(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Data must survive a reopen, and the first cache entry must win.
	d, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n, _ := d.downloads(1); n != 2 {
		t.Errorf("downloads after reopen = %d, want 2", n)
	}
	got, err := d.animation("99")
	if err != nil || got == nil || got.FileID != "file-a" || got.StorageMessageID != 10 {
		t.Errorf("animation after reopen = %+v, %v", got, err)
	}
	ids, err := d.userIDs()
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Errorf("userIDs = %v, %v", ids, err)
	}
}
