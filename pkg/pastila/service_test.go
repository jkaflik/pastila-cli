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

	assert.Equal(t, "Hello ClickHouse! unencrypted :(\n", string(actualContent))
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

func TestWriteUnencrypted(t *testing.T) {
	DefaultPastilaURL = chtest.EnsureClickHouseInstance(t)
	service := &Service{ClickHouseURL: DefaultPastilaURL, PastilaURL: "http://mylocal.pastila.nl/"}

	url, err := service.Write(bytes.NewBufferString("Hello ClickHouse!"))

	require.NoError(t, err)
	assert.NotEmpty(t, url.QueryID)
	assert.Equal(t, "http://mylocal.pastila.nl/?ffffffff/fa052372d3a8a5ee87eda55a42ac2338", url.URL)
}

func TestWriteEncryptedOwnKey(t *testing.T) {
	DefaultPastilaURL = chtest.EnsureClickHouseInstance(t)
	service := &Service{ClickHouseURL: DefaultPastilaURL, PastilaURL: "http://mylocal.pastila.nl/"}

	key := bytes.Repeat([]byte{0x01}, 16)
	url, err := service.Write(bytes.NewBufferString("Hello ClickHouse!"), WithKey(key))

	require.NoError(t, err)
	assert.NotEmpty(t, url.QueryID)
	assert.Equal(t, "http://mylocal.pastila.nl/?ffffffff/bf4b6a128f333a6ebfb67a5c371c38c4#AQEBAQEBAQEBAQEBAQEBAQ==", url.URL)
}
