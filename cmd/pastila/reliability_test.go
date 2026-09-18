package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestReadURLFromStdin(t *testing.T) {
	const url = "https://pastila.nl/?ffffffff/02dee605a703d3515f44aaaa4943402d#AAECAwQFBgcICQoLDA0ODw==GCM&nowrap"
	for _, input := range []string{url, url + "\n", "  " + url + "\r\n"} {
		got, err := readURL(strings.NewReader(input))
		require.NoError(t, err)
		assert.Equal(t, url, got)
	}
	_, err := readURL(strings.NewReader("\n"))
	require.Error(t, err)
}

func TestWatchFileFinalEdits(t *testing.T) {
	for _, value := range []string{"other", "", "longer content"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "paste")
			require.NoError(t, os.WriteFile(path, []byte("first"), 0o600))
			f, err := os.Open(path)
			require.NoError(t, err)
			defer func() { require.NoError(t, f.Close()) }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var observed []string
			done := watchFile(ctx, f, func(_ os.FileInfo) {
				data, err := os.ReadFile(path)
				assert.NoError(t, err)
				observed = append(observed, string(data))
			})
			// Atomic replacement covers editors which save via rename.
			require.NoError(t, os.WriteFile(path+".new", []byte(value), 0o600))
			require.NoError(t, os.Rename(path+".new", path))
			cancel()
			<-done
			assert.Equal(t, []string{value}, observed)
		})
	}
}

func TestDiscoveryDoesNotTrustCrossOriginWithCookie(t *testing.T) {
	var requests int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, "auth=secret", r.Header.Get("Cookie"))
		w.Header().Set("X-ClickHouse-Query-Id", "test")
		_, _ = io.WriteString(w, `{"1":1}`)
	}))
	defer remote.Close()
	base := httptest.NewServer(http.NotFoundHandler())
	defer base.Close()
	_, _, err := detectClickHouseEndpoint(base.URL, "", []string{remote.URL}, "secret")
	require.Error(t, err)
	assert.Zero(t, requests)
	_, _, err = detectClickHouseEndpoint(base.URL, remote.URL, nil, "secret")
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
	assert.False(t, sameHost("https://example.com", "http://example.com"))
}

func TestAuthCommandsDoNotExposeCookie(t *testing.T) {
	keyring.MockInit()
	setTestConfigHome(t)
	setTestOutput(t)
	var output bytes.Buffer
	printWriter = &output
	const base = "https://auth.example.com/"
	require.NoError(t, saveAuthCookie(base, "secret-cookie"))
	assert.Equal(t, 0, runAuth([]string{"status", "-url", base}))
	assert.Equal(t, 0, runAuth([]string{"clear", "-url", base}))
	_, err := loadAuthCookie(base)
	assert.ErrorIs(t, err, keyring.ErrNotFound)
	assert.Equal(t, 0, runAuth([]string{"status", "-url", base}))
	assert.NotContains(t, output.String(), "secret-cookie")
}

func TestDoctor(t *testing.T) {
	keyring.MockInit()
	setTestConfigHome(t)
	setTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Query-Id", "test")
		_, _ = io.WriteString(w, `{"1":1}`)
	}))
	defer server.Close()
	assert.Equal(t, 0, runDoctor([]string{"-url", server.URL, "-clickhouse-url", server.URL}))
}
