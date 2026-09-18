package auth

import (
	"fmt"
	"net/url"

	"github.com/zalando/go-keyring"
)

const keychainService = "pastila-cli"

func accountForURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse url: %w", err)
	}

	host := parsed.Host
	if host == "" {
		return "", fmt.Errorf("url host is empty")
	}

	return host, nil
}

func SaveCookie(rawURL, cookie string) error {
	account, err := accountForURL(rawURL)
	if err != nil {
		return err
	}

	if err := keyring.Set(keychainService, account, cookie); err != nil {
		return fmt.Errorf("failed to save cookie in keychain: %w", err)
	}

	return nil
}

func LoadCookie(rawURL string) (string, error) {
	account, err := accountForURL(rawURL)
	if err != nil {
		return "", err
	}

	v, err := keyring.Get(keychainService, account)
	if err != nil {
		return "", err
	}

	return v, nil
}

func DeleteCookie(rawURL string) error {
	account, err := accountForURL(rawURL)
	if err != nil {
		return err
	}

	if err := keyring.Delete(keychainService, account); err != nil {
		return fmt.Errorf("failed to delete cookie from keychain: %w", err)
	}

	return nil
}
