package pastila

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const MaxContentSize = 50 * 1024 * 1024
const MaxDecompressedSize = 256*1024*1024 - 16

var pasteQuery = regexp.MustCompile(`^([a-f0-9]{8})/([a-f0-9]{32})(\.md|\.markdown|\.html?|\.link|\.png|\.claude\.jsonl|\.terminal)?(\.gz)?$`)

type link struct {
	fingerprint, hash                string
	format                           string
	compressed, gcm, nowrap, sandbox bool
	key                              []byte
}

func parseLink(raw string) (*link, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, ErrInvalidURL
	}
	query := u.RawQuery
	if u.Scheme == "" && u.Host == "" && query == "" {
		query = u.Path
	}
	if u.Scheme != "" && (u.Scheme != "http" && u.Scheme != "https" || u.Host == "") {
		return nil, ErrInvalidURL
	}
	m := pasteQuery.FindStringSubmatch(query)
	if m == nil {
		return nil, ErrInvalidURL
	}
	parts := strings.Split(u.Fragment, "&")
	l := &link{fingerprint: m[1], hash: m[2], format: strings.TrimPrefix(m[3], "."), compressed: m[4] != ""}
	encodedKey := parts[0]
	l.gcm = strings.HasSuffix(encodedKey, "GCM")
	if l.gcm {
		encodedKey = strings.TrimSuffix(encodedKey, "GCM")
	}
	if encodedKey != "" {
		l.key, err = base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed base64", ErrInvalidKey)
		}
		if _, err = aes.NewCipher(l.key); err != nil {
			return nil, fmt.Errorf("%w: expected 16, 24 or 32 bytes", ErrInvalidKey)
		}
	}
	for _, option := range parts[1:] {
		switch option {
		case "nowrap":
			l.nowrap = true
		case "sandbox":
			l.sandbox = true
		}
	}
	return l, nil
}

func validFormat(format string) bool {
	switch format {
	case "", "md", "markdown", "html", "htm", "link", "png", "claude.jsonl", "terminal":
		return true
	}
	return false
}

// freshKey derives a one-use AES key from custom key material and random salt.
// The resulting key, not the seed, is shared in the URL. Pastila's GCM nonce is
// derived from its key, so a caller-supplied key must never be reused directly.
func freshKey(seed []byte) ([]byte, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	if seed == nil {
		return random[:16], nil
	}
	if _, err := aes.NewCipher(seed); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}
	return hkdf.Key(sha256.New, seed, random, "pastila-cli one-use AES-GCM key", 16)
}

func decryptContent(content string, key []byte, gcm bool) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrKeyRequired
	}
	ciphertext, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}
	if gcm {
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		plaintext, err := aead.Open(nil, key[:aead.NonceSize()], ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: wrong key or corrupted data", ErrInvalidKey)
		}
		return plaintext, nil
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCTR(block, make([]byte, aes.BlockSize)).XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}

func decompress(content []byte, limit int64) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid gzip content: %w", err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("decompression failed: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("decompressed content exceeds %d bytes", limit)
	}
	return b, nil
}
