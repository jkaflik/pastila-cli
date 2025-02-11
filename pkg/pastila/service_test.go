package pastila

import (
	"bytes"
	"github.com/jkaflik/pastila-cli/pkg/chtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
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

	assert.ErrorIs(t, err, ErrInvalidUrl)
}

func ensureLocalService(t *testing.T) *Service {
	chURL = chtest.EnsureClickHouseInstance(t)
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

func TestWriteEncryptedOwnKey(t *testing.T) {
	service := ensureLocalService(t)

	key := bytes.Repeat([]byte{0x01}, 16)
	url, err := service.Write(bytes.NewBufferString("Hello ClickHouse!"), WithKey(key))

	require.NoError(t, err)
	assert.NotEmpty(t, url.QueryID)
	assert.Equal(t, "http://mylocal.pastila.nl/?ffffffff/f7dfa9488fcbea210ff70e44d0566245#AQEBAQEBAQEBAQEBAQEBAQ==", url.URL)
}
