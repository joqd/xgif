package main

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,
    downloads INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS animations (
    tweet_id TEXT PRIMARY KEY,
    original_url TEXT NOT NULL,
    storage_message_id INTEGER NOT NULL,
    file_id TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`

type DB struct {
	sql *sql.DB
}

type Animation struct {
	TweetID          string
	URL              string
	StorageMessageID int64
	FileID           string
}

func openDB(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps writes serialized and avoids lock errors.
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init database schema: %w", err)
	}
	return &DB{sql: conn}, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) upsertUser(id int64, username, firstName string) error {
	_, err := d.sql.Exec(`
		INSERT INTO users (id, username, first_name) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			updated_at = CURRENT_TIMESTAMP`, id, username, firstName)
	return err
}

func (d *DB) downloads(userID int64) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT downloads FROM users WHERE id = ?`, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// incDownloads records one successful download and returns the new total.
func (d *DB) incDownloads(userID int64) (int, error) {
	var n int
	err := d.sql.QueryRow(`
		UPDATE users SET downloads = downloads + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? RETURNING downloads`, userID).Scan(&n)
	return n, err
}

func (d *DB) animation(tweetID string) (*Animation, error) {
	a := &Animation{TweetID: tweetID}
	err := d.sql.QueryRow(`
		SELECT original_url, storage_message_id, file_id FROM animations WHERE tweet_id = ?`,
		tweetID).Scan(&a.URL, &a.StorageMessageID, &a.FileID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (d *DB) saveAnimation(a *Animation) error {
	_, err := d.sql.Exec(`
		INSERT OR IGNORE INTO animations (tweet_id, original_url, storage_message_id, file_id)
		VALUES (?, ?, ?, ?)`, a.TweetID, a.URL, a.StorageMessageID, a.FileID)
	return err
}

func (d *DB) userIDs() ([]int64, error) {
	rows, err := d.sql.Query(`SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
