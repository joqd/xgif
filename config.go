package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	configPath  = "/etc/xgif/config.yaml"
	dbPath      = "/etc/xgif/db"
	cookiesPath = "/etc/xgif/cookies.txt"
)

type Config struct {
	BotToken         string `yaml:"bot_token"`
	AdminID          int64  `yaml:"admin_id"`
	SponsorChannelID int64  `yaml:"sponsor_channel_id"`
	StorageChannelID int64  `yaml:"storage_channel_id"`
	MaxWorkers       int    `yaml:"max_workers"`
	MaxQueueSize     int    `yaml:"max_queue_size"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config %s: %w", path, err)
	}
	cfg := &Config{MaxWorkers: 3, MaxQueueSize: 100}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config %s is empty", path)
		}
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch {
	case c.BotToken == "" || c.BotToken == "TELEGRAM_BOT_TOKEN":
		return errors.New("bot_token is missing")
	case c.AdminID == 0:
		return errors.New("admin_id is missing")
	case c.SponsorChannelID == 0:
		return errors.New("sponsor_channel_id is missing")
	case c.StorageChannelID == 0:
		return errors.New("storage_channel_id is missing")
	case c.MaxWorkers < 1:
		return errors.New("max_workers must be at least 1")
	case c.MaxQueueSize < 1:
		return errors.New("max_queue_size must be at least 1")
	}
	return nil
}
