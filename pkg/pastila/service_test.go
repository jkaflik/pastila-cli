package pastila

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jkaflik/pastila-cli/pkg/chtest"
)

func TestReadEncrypted(t *testing.T) {
	service := &Service{}
	r, err := service.Read("https://pastila.nl/?ffffffff/52662368cc45b2ad0e9a47faa8582369#2L9DFnYzHu27jLxA9elfyg==")

	require.NoError(t, err)
	actualContent, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)

	assert.Equal(t, "Hello ClickHouse!", string(actualContent))
}

func TestReadUnencrypted(t *testing.T) {
	service := &Service{}
	r, err := service.Read("https://pastila.nl/?c055a950/620234bcb081dcff3cfdf3c3c2806062")

	require.NoError(t, err)
	actualContent, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)

	assert.Equal(t, "Hello ClickHouse! unencrypted :(", string(actualContent))
}

func TestReadInvalidKey(t *testing.T) {
	service := &Service{}
	_, err := service.Read("https://pastila.nl/?ffffffff/52662368cc45b2ad0e9a47faa8582369#invalid")

	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestReadInvalidUrlPath(t *testing.T) {
	service := &Service{}
	_, err := service.Read("https://some.url/invalid/path")

	assert.ErrorIs(t, err, ErrInvalidURL)
}

func TestReadSendsAuthCookie(t *testing.T) {
	cookieValues := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookieValue := ""
		cookie, err := request.Cookie("auth")
		if err == nil {
			cookieValue = cookie.Value
		}
		cookieValues <- cookieValue
		response.Header().Set("X-ClickHouse-Query-Id", "read-query")
		_, _ = io.WriteString(response, `{"is_encrypted":false,"content":"authenticated"}`+"\n")
	}))
	t.Cleanup(server.Close)

	service := &Service{
		ClickHouseURL: server.URL + "/query",
		AuthCookie:    "test-cookie",
	}
	paste, err := service.Read("https://pastila.example.com/?ffffffff/14aa3e22cd6438df3a5808560fe40150")

	require.NoError(t, err)
	require.NoError(t, paste.Close())
	assert.Equal(t, "test-cookie", <-cookieValues)
}

func ensureLocalService(t *testing.T) *Service {
	chURL := chtest.EnsureClickHouseInstance(t)
	return &Service{ClickHouseURL: chURL, PastilaURL: "http://mylocal.pastila.nl/"}
}

func TestWriteUnencrypted(t *testing.T) {
	const expectedContent = "Hello ClickHouse!"

	service := ensureLocalService(t)

	paste, err := service.Write(bytes.NewBufferString(expectedContent))

	require.NoError(t, err)
	assert.NotEmpty(t, paste.QueryID)
	assert.Equal(t, service.PastilaURL+"?ffffffff/fa052372d3a8a5ee87eda55a42ac2338", paste.URL)

	paste, err = service.Read(paste.URL)
	require.NoError(t, err)

	actualContent, err := io.ReadAll(paste)
	require.NoError(t, paste.Close())
	require.NoError(t, err)

	assert.Equal(t, expectedContent, string(actualContent))
}

func TestWriteEncryptedWithKeyMaterial(t *testing.T) {
	service := ensureLocalService(t)

	key := bytes.Repeat([]byte{0x01}, 16)
	url, err := service.Write(bytes.NewBufferString("Hello ClickHouse!"), WithKey(key))

	require.NoError(t, err)
	assert.NotEmpty(t, url.QueryID)
	parsed, err := parseLink(url.URL)
	require.NoError(t, err)
	assert.Equal(t, "ffffffff", parsed.fingerprint)
	assert.Regexp(t, `^[0-9a-f]{32}$`, parsed.hash)
	assert.True(t, parsed.gcm)
	assert.Len(t, parsed.key, 16)
	assert.Equal(t, url.Key, parsed.key)
	assert.NotEqual(t, key, url.Key)
	repeated, err := service.Write(bytes.NewBufferString("Hello ClickHouse!"), WithKey(key))
	require.NoError(t, err)
	assert.NotEqual(t, url.Key, repeated.Key)
	assert.NotEqual(t, url.Hash, repeated.Hash)
	assert.NotEqual(t, url.URL, repeated.URL)
	paste, err := service.Read(url.URL)
	require.NoError(t, err)
	defer func() { require.NoError(t, paste.Close()) }()
	content, err := io.ReadAll(paste)
	require.NoError(t, err)
	assert.Equal(t, "Hello ClickHouse!", string(content))
}
