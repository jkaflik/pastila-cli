package pastila

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frifox/siphash128"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Generated using encrypt() and sipHash128() on pastila.nl in WebCrypto,
// independently of the Go implementation. The fixed key is fixture-only.
const browserCiphertext = "ww3Uug93lnQIniiqVcwdeNJSc4Q6gZSCnwGXRGww1x7gnvuUleZ0SmAElmpSg3Dow859ucIc4NLJgWp2KHM="
const browserKey = "AAECAwQFBgcICQoLDA0ODw=="
const browserText = "Pastila CLI compatibility fixture: café 🍒\n"

func TestBrowserGCMCompatibility(t *testing.T) {
	key, err := base64.StdEncoding.DecodeString(browserKey)
	require.NoError(t, err)
	content, err := decryptContent(browserCiphertext, key, true)
	require.NoError(t, err)
	assert.Equal(t, browserText, string(content))
	hash := siphash128.SipHash128([]byte(browserCiphertext))
	assert.Equal(t, "02dee605a703d3515f44aaaa4943402d", hex.EncodeToString(hash[:]))
	key[0] ^= 1
	_, err = decryptContent(browserCiphertext, key, true)
	assert.ErrorIs(t, err, ErrInvalidKey)
	key[0] ^= 1
	ciphertext, err := base64.StdEncoding.DecodeString(browserCiphertext)
	require.NoError(t, err)
	ciphertext[len(ciphertext)-1] ^= 1
	_, err = decryptContent(base64.StdEncoding.EncodeToString(ciphertext), key, true)
	assert.ErrorIs(t, err, ErrInvalidKey)
	_, err = decryptContent("AA==", key, true)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

func TestModernLinks(t *testing.T) {
	base := "https://pastila.nl/?ffffffff/02dee605a703d3515f44aaaa4943402d"
	for _, ext := range []string{"", ".md", ".markdown", ".html", ".htm", ".link", ".png", ".claude.jsonl", ".terminal"} {
		for _, compression := range []string{"", ".gz"} {
			t.Run(ext+compression, func(t *testing.T) {
				l, err := parseLink(base + ext + compression + "#" + browserKey + "GCM&nowrap&sandbox")
				require.NoError(t, err)
				assert.True(t, l.gcm && l.nowrap && l.sandbox)
				assert.Equal(t, compression != "", l.compressed)
				assert.Equal(t, strings.TrimPrefix(ext, "."), l.format)
			})
		}
	}
	for _, bad := range []string{base + ".exe", base + "junk", "https://example.com/not-a-paste", base + "#invalid", base + "\nnot-a-link"} {
		_, err := parseLink(bad)
		assert.Error(t, err, bad)
	}
	l, err := parseLink(base + "#&nowrap")
	require.NoError(t, err)
	assert.Empty(t, l.key)
	assert.True(t, l.nowrap)
}

func memoryService(t *testing.T) *Service {
	t.Helper()
	rows := map[string]insertRow{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ClickHouse-Query-Id", "test-query")
		if strings.Contains(r.URL.Query().Get("query"), "INSERT") {
			var row insertRow
			if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			hash := siphash128.SipHash128([]byte(row.Content))
			assert.Equal(t, hex.EncodeToString(hash[:]), row.HashHex)
			rows[row.HashHex] = row
			return
		}
		row, ok := rows[r.URL.Query().Get("param_hashHex")]
		if !ok {
			return
		}
		_ = json.NewEncoder(w).Encode(selectRow{Encrypted: row.Encrypted, Content: row.Content, PreviousHash: row.PrevHashHex, PreviousFingerprint: row.PrevFingerprintHex})
	}))
	t.Cleanup(server.Close)
	return &Service{ClickHouseURL: server.URL, PastilaURL: "https://pastila.nl/"}
}

func TestModernRoundTripsAndRevisions(t *testing.T) {
	for _, encrypt := range []bool{false, true} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmtCase(encrypt, compressed), func(t *testing.T) {
				s := memoryService(t)
				opts := []WriteOption{WithCompression(compressed), WithFormat("html"), WithNoWrap(true), WithSandbox(true)}
				if encrypt {
					opts = append(opts, WithKey(bytes.Repeat([]byte{1}, 16)))
				}
				p, err := s.Write(strings.NewReader(browserText), opts...)
				require.NoError(t, err)
				read, err := s.Read(p.URL)
				require.NoError(t, err)
				defer func() { require.NoError(t, read.Close()) }()
				content, err := io.ReadAll(read)
				require.NoError(t, err)
				assert.Equal(t, browserText, string(content))
				assert.Equal(t, "html", read.Format)
				assert.Equal(t, compressed, read.Compressed)
				revision, err := s.Write(strings.NewReader("revised"), WithPreviousPaste(read))
				require.NoError(t, err)
				if encrypt {
					assert.NotEqual(t, p.Key, revision.Key)
				}
				r, err := s.Read(revision.URL)
				require.NoError(t, err)
				defer func() { require.NoError(t, r.Close()) }()
				assert.Equal(t, p.Hash, r.PreviousHash)
				assert.Equal(t, p.Fingerprint, r.PreviousFingerprint)
				revisedContent, err := io.ReadAll(r)
				require.NoError(t, err)
				assert.Equal(t, "revised", string(revisedContent))
			})
		}
	}
}

func fmtCase(encrypted, compressed bool) string {
	if encrypted {
		if compressed {
			return "gcm-gzip"
		}
		return "gcm"
	}
	if compressed {
		return "plain-gzip"
	}
	return "plain"
}

func TestCustomKeyNeverReused(t *testing.T) {
	s := memoryService(t)
	seed := bytes.Repeat([]byte{1}, 16)
	a, err := s.Write(strings.NewReader("first"), WithKey(seed))
	require.NoError(t, err)
	b, err := s.Write(strings.NewReader("second"), WithKey(seed))
	require.NoError(t, err)
	assert.NotEqual(t, seed, a.Key)
	assert.NotEqual(t, a.Key, b.Key)
}

func TestDecompressionLimitAndCorruption(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(bytes.Repeat([]byte("a"), 1000))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = decompress(buf.Bytes(), 100)
	require.ErrorContains(t, err, "exceeds")
	_, err = decompress(buf.Bytes()[:buf.Len()-3], 2000)
	require.Error(t, err)
}

func TestHTTPErrorWithoutQueryID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "authentication required", http.StatusForbidden)
	}))
	defer server.Close()
	s := &Service{ClickHouseURL: server.URL}
	_, err := s.Read("https://pastila.nl/?ffffffff/02dee605a703d3515f44aaaa4943402d")
	require.ErrorContains(t, err, "403")
	require.ErrorContains(t, err, "authentication required")
}
