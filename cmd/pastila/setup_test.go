package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/jkaflik/pastila-cli/pkg/config"
)

func TestRunSetupWithURLStoresCookieAndDetectedEndpoint(t *testing.T) {
	const cookie = "test-cookie"

	server := newSetupServer(t, cookie)
	t.Cleanup(server.Close)
	setTestConfigHome(t)
	setTestStdin(t, "auth="+cookie+"\n")
	setTestOutput(t)

	originalLoadCookie := loadAuthCookie
	originalSaveCookie := saveAuthCookie
	t.Cleanup(func() {
		loadAuthCookie = originalLoadCookie
		saveAuthCookie = originalSaveCookie
	})

	loadAuthCookie = func(string) (string, error) {
		return "", keyring.ErrNotFound
	}
	var savedURL, savedCookie string
	saveAuthCookie = func(rawURL, value string) error {
		savedURL = rawURL
		savedCookie = value
		return nil
	}

	exitCode := runSetup([]string{"--url", server.URL, "--no-open"})

	assert.Equal(t, 0, exitCode)
	assert.Equal(t, server.URL+"/", savedURL)
	assert.Equal(t, cookie, savedCookie)

	configPath, err := config.DefaultPath()
	require.NoError(t, err)
	savedConfig, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/", savedConfig.PastilaURL)
	assert.Equal(t, server.URL+"/play", savedConfig.ClickHouseURL)
}

func TestRunSetupPromptsForURL(t *testing.T) {
	server := newSetupServer(t, "")
	t.Cleanup(server.Close)
	setTestConfigHome(t)
	setTestStdin(t, server.URL+"\n")
	setTestOutput(t)

	originalLoadCookie := loadAuthCookie
	t.Cleanup(func() { loadAuthCookie = originalLoadCookie })
	loadAuthCookie = func(string) (string, error) {
		return "", keyring.ErrNotFound
	}

	exitCode := runSetup([]string{"--no-open"})

	assert.Equal(t, 0, exitCode)
	configPath, err := config.DefaultPath()
	require.NoError(t, err)
	savedConfig, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/", savedConfig.PastilaURL)
	assert.Equal(t, server.URL+"/play", savedConfig.ClickHouseURL)
}

func TestRunSetupProbesQueryEndpointWhenUIDoesNotExposeURL(t *testing.T) {
	const cookie = "test-cookie"

	server := newOpaqueSetupServer(t, cookie)
	t.Cleanup(server.Close)
	setTestConfigHome(t)
	setTestStdin(t, cookie+"\n")
	setTestOutput(t)

	originalLoadCookie := loadAuthCookie
	originalSaveCookie := saveAuthCookie
	t.Cleanup(func() {
		loadAuthCookie = originalLoadCookie
		saveAuthCookie = originalSaveCookie
	})
	loadAuthCookie = func(string) (string, error) {
		return "", keyring.ErrNotFound
	}
	var savedCookie string
	saveAuthCookie = func(_ string, value string) error {
		savedCookie = value
		return nil
	}

	exitCode := runSetup([]string{"--url", server.URL, "--no-open"})

	assert.Equal(t, 0, exitCode)
	assert.Equal(t, cookie, savedCookie)
	configPath, err := config.DefaultPath()
	require.NoError(t, err)
	savedConfig, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/query", savedConfig.ClickHouseURL)
}

func TestParseAuthCookieValue(t *testing.T) {
	testCases := map[string]string{
		"raw value":          "secret",
		"raw padded value":   "c2VjcmV0==",
		"cookie pair":        "auth=secret",
		"cookie header":      "Cookie: auth=secret",
		"full cookie header": "other=value; auth=secret; another=value",
	}

	for name, input := range testCases {
		t.Run(name, func(t *testing.T) {
			expected := "secret"
			if name == "raw padded value" {
				expected = "c2VjcmV0=="
			}
			assert.Equal(t, expected, parseAuthCookieValue(input))
		})
	}
}

func newSetupServer(t *testing.T, requiredCookie string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if requiredCookie != "" {
			cookie, err := request.Cookie("auth")
			if err != nil || cookie.Value != requiredCookie {
				response.Header().Set("Location", "https://login.example.com/")
				response.WriteHeader(http.StatusFound)
				return
			}
		}

		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/":
			_, _ = io.WriteString(response, `<script>const clickhouse_url = "/play";</script>`)
		case request.Method == http.MethodPost && request.URL.Path == "/play":
			assert.Contains(t, request.URL.Query().Get("query"), "SELECT 1")
			response.Header().Set("X-ClickHouse-Query-Id", "setup-query")
			_, _ = io.WriteString(response, "{}\n")
		default:
			http.NotFound(response, request)
		}
	}))
}

func newOpaqueSetupServer(t *testing.T, requiredCookie string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/" {
			_, _ = io.WriteString(response, "<html><body>Pastila</body></html>")
			return
		}

		cookie, err := request.Cookie("auth")
		if err != nil || cookie.Value != requiredCookie {
			response.WriteHeader(http.StatusForbidden)
			return
		}

		if request.Method == http.MethodPost && request.URL.Path == "/query" {
			response.Header().Set("X-ClickHouse-Query-Id", "setup-query")
			_, _ = io.WriteString(response, "{}\n")
			return
		}

		http.NotFound(response, request)
	}))
}

func setTestConfigHome(t *testing.T) {
	t.Helper()

	directory := t.TempDir()
	t.Setenv("HOME", directory)
	t.Setenv("XDG_CONFIG_HOME", directory)
}

func setTestStdin(t *testing.T, input string) {
	t.Helper()

	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	_, err = io.Copy(writePipe, strings.NewReader(input))
	require.NoError(t, err)
	require.NoError(t, writePipe.Close())

	originalStdin := os.Stdin
	os.Stdin = readPipe
	t.Cleanup(func() {
		os.Stdin = originalStdin
		require.NoError(t, readPipe.Close())
	})
}

func setTestOutput(t *testing.T) {
	t.Helper()

	originalWriter := printWriter
	printWriter = io.Discard
	t.Cleanup(func() { printWriter = originalWriter })
}
