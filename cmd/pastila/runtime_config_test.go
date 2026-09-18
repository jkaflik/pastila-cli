package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jkaflik/pastila-cli/pkg/config"
)

func TestNormalizePastilaURLAddsTrailingSlashToPath(t *testing.T) {
	normalized, err := normalizePastilaURL("https://pastila.example.com/team")

	require.NoError(t, err)
	assert.Equal(t, "https://pastila.example.com/team/", normalized)
}

func TestNormalizePastilaURLRejectsQuery(t *testing.T) {
	_, err := normalizePastilaURL("https://pastila.example.com/?unexpected=value")

	assert.Error(t, err)
}

func TestResolveConfigLoadsFileAndKeychain(t *testing.T) {
	setTestConfigHome(t)
	t.Setenv(envPastilaURL, "")
	t.Setenv(envClickHouseURL, "")
	t.Setenv(envPastilaCookie, "")

	configPath, err := config.DefaultPath()
	require.NoError(t, err)
	require.NoError(t, config.Save(configPath, &config.FileConfig{
		PastilaURL:    "https://pastila.example.com/",
		ClickHouseURL: "https://pastila.example.com/play",
	}))

	originalLoadCookie := loadAuthCookie
	t.Cleanup(func() { loadAuthCookie = originalLoadCookie })
	loadAuthCookie = func(rawURL string) (string, error) {
		assert.Equal(t, "https://pastila.example.com/", rawURL)
		return "keychain-cookie", nil
	}

	resolved, err := resolveConfig("", "", "")

	require.NoError(t, err)
	assert.Equal(t, "https://pastila.example.com/", resolved.PastilaURL)
	assert.Equal(t, "config", resolved.PastilaURLSource)
	assert.Equal(t, "https://pastila.example.com/play", resolved.ClickHouseURL)
	assert.Equal(t, "config", resolved.ClickHouseURLSource)
	assert.Equal(t, "keychain-cookie", resolved.AuthCookie)
	assert.Equal(t, "keychain", resolved.AuthCookieSource)
}

func TestResolveConfigEnvironmentOverridesFileAndKeychain(t *testing.T) {
	setTestConfigHome(t)
	t.Setenv(envPastilaURL, "https://env.example.com/")
	t.Setenv(envClickHouseURL, "https://env.example.com/play")
	t.Setenv(envPastilaCookie, "env-cookie")

	originalLoadCookie := loadAuthCookie
	t.Cleanup(func() { loadAuthCookie = originalLoadCookie })
	loadAuthCookie = func(string) (string, error) {
		t.Fatal("keychain should not be read when PASTILA_COOKIE is set")
		return "", nil
	}

	resolved, err := resolveConfig("", "", "")

	require.NoError(t, err)
	assert.Equal(t, "https://env.example.com/", resolved.PastilaURL)
	assert.Equal(t, "env", resolved.PastilaURLSource)
	assert.Equal(t, "https://env.example.com/play", resolved.ClickHouseURL)
	assert.Equal(t, "env", resolved.ClickHouseURLSource)
	assert.Equal(t, "env-cookie", resolved.AuthCookie)
	assert.Equal(t, "env", resolved.AuthCookieSource)
}
