package chtest

import (
	"context"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func EnsureClickHouseInstance(t *testing.T) string {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:latest",
		ExposedPorts: []string{"8123/tcp"},
		WaitingFor:   wait.ForHTTP("/"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		testcontainers.CleanupContainer(t, container)
	})

	url, err := container.Endpoint(context.Background(), "http")
	t.Logf("ClickHouse URL: %s/play", url)
	require.NoError(t, err)
	url += "/?user=default"

	EnsureClickHousePastila(t, url)

	return url
}

func EnsureClickHousePastila(t *testing.T, url string) {
	pastilaSchema, err := os.Open(AssetPath(t, "table.ddl.sql"))
	require.NoError(t, err)

	ClickHouseQuery(t, url, pastilaSchema)
}

func AssetPath(t *testing.T, path string) string {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filename[:len(filename)-len("clickhouse.go")] + path
}

func ClickHouseQuery(t *testing.T, url string, query io.Reader) {
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "POST", url, query)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
