package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jkaflik/pastila-cli/pkg/auth"
	"github.com/jkaflik/pastila-cli/pkg/config"
	"github.com/jkaflik/pastila-cli/pkg/pastila"
)

const (
	envPastilaURL    = "PASTILA_URL"
	envClickHouseURL = "PASTILA_CLICKHOUSE_URL"
	envPastilaCookie = "PASTILA_COOKIE"
	sourceDefault    = "default"
)

var loadAuthCookie = auth.LoadCookie

type resolvedConfig struct {
	PastilaURL       string
	PastilaURLSource string

	ClickHouseURL       string
	ClickHouseURLSource string

	AuthCookie       string
	AuthCookieSource string

	ConfigPath string
}

func normalizeURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("failed to parse url: %w", err)
	}

	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("invalid url: %s", rawURL)
	}

	return u.String(), nil
}

func normalizePastilaURL(rawURL string) (string, error) {
	normalized, err := normalizeURL(rawURL)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("failed to parse url: %w", err)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("pastila url must not contain a query or fragment")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}

	return u.String(), nil
}

func chooseValue(flagValue, envValue, configValue, fallback string) (value, source string) {
	if flagValue != "" {
		return flagValue, "flag"
	}
	if envValue != "" {
		return envValue, "env"
	}
	if configValue != "" {
		return configValue, "config"
	}
	return fallback, sourceDefault
}

func resolveConfig(flagPastilaURL, flagClickHouseURL, flagCookie string) (*resolvedConfig, error) {
	configPath, err := config.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	pastilaURL, pastilaURLSource := chooseValue(
		flagPastilaURL,
		strings.TrimSpace(os.Getenv(envPastilaURL)),
		strings.TrimSpace(cfg.PastilaURL),
		pastila.DefaultPastilaURL,
	)

	normalizedPastilaURL, err := normalizePastilaURL(pastilaURL)
	if err != nil {
		return nil, fmt.Errorf("invalid pastila url: %w", err)
	}
	pastilaURL = normalizedPastilaURL

	clickHouseURL, clickHouseURLSource := chooseValue(
		flagClickHouseURL,
		strings.TrimSpace(os.Getenv(envClickHouseURL)),
		strings.TrimSpace(cfg.ClickHouseURL),
		pastila.DefaultClickHouseURL,
	)

	normalizedClickHouseURL, err := normalizeURL(clickHouseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid clickhouse url: %w", err)
	}
	clickHouseURL = normalizedClickHouseURL
	if pastilaURL != pastila.DefaultPastilaURL && clickHouseURLSource == sourceDefault {
		return nil, fmt.Errorf("custom Pastila URL requires a ClickHouse endpoint; run pastila setup or set -clickhouse-url")
	}

	authCookie := strings.TrimSpace(flagCookie)
	authCookieSource := ""

	if authCookie != "" {
		authCookieSource = "flag"
	} else {
		authCookie = strings.TrimSpace(os.Getenv(envPastilaCookie))
		if authCookie != "" {
			authCookieSource = "env"
		} else {
			cookieFromKeychain, loadErr := loadAuthCookie(pastilaURL)
			if loadErr == nil {
				authCookie = cookieFromKeychain
				authCookieSource = "keychain"
			}
		}
	}

	return &resolvedConfig{
		PastilaURL:          pastilaURL,
		PastilaURLSource:    pastilaURLSource,
		ClickHouseURL:       clickHouseURL,
		ClickHouseURLSource: clickHouseURLSource,
		AuthCookie:          parseAuthCookieValue(authCookie),
		AuthCookieSource:    authCookieSource,
		ConfigPath:          configPath,
	}, nil
}
