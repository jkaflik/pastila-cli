package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DirectoryName = "pastila"
	FileName      = "config.json"
)

type FileConfig struct {
	PastilaURL    string `json:"pastila_url"`
	ClickHouseURL string `json:"clickhouse_url"`
}

func DefaultPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}

	return filepath.Join(configDir, DirectoryName, FileName), nil
}

func Load(path string) (*FileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &FileConfig{}, nil
		}

		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg FileConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

func Save(path string, cfg *FileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
