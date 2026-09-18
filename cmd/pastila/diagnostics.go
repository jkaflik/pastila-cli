package main

import (
	"errors"
	"flag"
	"os"

	"github.com/zalando/go-keyring"

	"github.com/jkaflik/pastila-cli/pkg/auth"
)

var deleteAuthCookie = auth.DeleteCookie

//nolint:funlen,gocyclo // Authentication actions have distinct validation and keychain outcomes.
func runAuth(args []string) int {
	if len(args) == 0 {
		printf("Usage: pastila auth login|status|clear [-url URL] [-no-open]\n")
		return 2
	}
	action := args[0]
	if action != "login" && action != "status" && action != "clear" {
		printf("Unknown auth command: %s\n", action)
		return 2
	}
	fs := flag.NewFlagSet("auth "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := fs.String("url", "", "Pastila base URL")
	noOpen := fs.Bool("no-open", false, "Do not open the browser")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		printf("Unexpected arguments\n")
		return 2
	}
	baseURL := *base
	if baseURL == "" {
		resolved, err := resolveConfig("", "", "")
		if err != nil {
			printf("%v\n", err)
			return 1
		}
		baseURL = resolved.PastilaURL
	}
	baseURL, err := normalizePastilaURL(baseURL)
	if err != nil {
		printf("%v\n", err)
		return 1
	}
	switch action {
	case "login":
		cookie, err := loginWithGuidance(baseURL, *noOpen)
		if err != nil {
			printf("%v\n", err)
			return 1
		}
		if err := saveAuthCookie(baseURL, cookie); err != nil {
			printf("%v\n", err)
			return 1
		}
		printf("Authentication saved in system keychain for %s\n", baseURL)
	case "status":
		cookie, err := loadAuthCookie(baseURL)
		if errors.Is(err, keyring.ErrNotFound) || (err == nil && cookie == "") {
			printf("No stored authentication for %s\n", baseURL)
			return 0
		}
		if err != nil {
			printf("Failed to read keychain: %v\n", err)
			return 1
		}
		printf("Authentication stored for %s (not a server validation)\n", baseURL)
	case "clear":
		if err := deleteAuthCookie(baseURL); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			printf("%v\n", err)
			return 1
		}
		printf("Stored authentication cleared for %s; PASTILA_COOKIE overrides are unaffected\n", baseURL)
	}
	return 0
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := fs.String("url", "", "Pastila base URL override")
	backend := fs.String("clickhouse-url", "", "ClickHouse API URL override")
	cookie := fs.String("cookie", "", "Authentication cookie override")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		printf("Unexpected arguments\n")
		return 2
	}
	cfg, err := resolveConfig(*base, *backend, *cookie)
	if err != nil {
		printf("%v\n", err)
		return 1
	}
	printf("Config: %s\n", cfg.ConfigPath)
	printf("Pastila: %s (%s)\n", cfg.PastilaURL, cfg.PastilaURLSource)
	printf("ClickHouse: %s (%s)\n", cfg.ClickHouseURL, cfg.ClickHouseURLSource)
	if cfg.AuthCookie == "" {
		printf("Authentication: none\n")
	} else {
		printf("Authentication source: %s\n", cfg.AuthCookieSource)
	}
	landing := probeLanding(cfg.PastilaURL, cfg.AuthCookie)
	probe := probeQueryEndpoint(cfg.ClickHouseURL, cfg.AuthCookie)
	printf("UI: %s\nQuery endpoint: %s\n", formatQueryProbe(&landing), formatQueryProbe(&probe))
	if !landing.OK || !probe.OK {
		return 1
	}
	return 0
}
