package pastila

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frifox/siphash128"
)

var HTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}
var DefaultPastilaURL = "https://pastila.nl/"
var DefaultClickHouseURL = "https://uzg8q0g12h.eu-central-1.aws.clickhouse.cloud/?user=paste"

var (
	ErrInvalidURL  = fmt.Errorf("invalid pastila url")
	ErrNotFound    = fmt.Errorf("pastila not found")
	ErrKeyRequired = fmt.Errorf("key is required for encrypted data")
	ErrInvalidKey  = fmt.Errorf("invalid key")
)

type Paste struct {
	io.ReadCloser

	URL string

	Fingerprint         []byte
	Hash                []byte
	PreviousFingerprint []byte
	PreviousHash        []byte

	Key        []byte
	Format     string
	Compressed bool
	NoWrap     bool
	Sandbox    bool

	QueryID string
}

type Service struct {
	// PastilaURL is the URL of the pastila service. Used to generate URLs for writing data.
	PastilaURL string

	// ClickHouseURL is the URL of the ClickHouse service. Used to read and write data.
	ClickHouseURL string

	// Auth cookie for pastila with auth
	AuthCookie string
}

func (s *Service) Read(url string) (*Paste, error) {
	l, err := parseLink(url)
	if err != nil {
		return nil, err
	}
	fingerprintHex, hashHex := l.fingerprint, l.hash
	key := l.key

	req, err := s.clickHouseRequest(selectDataQuery, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse request: %w", err)
	}

	res, err := s.executeRequestWithParams(req, map[string]string{
		"fingerprintHex": fingerprintHex,
		"hashHex":        hashHex,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute ClickHouse request: %w", err)
	}

	defer func() { _ = res.Body.Close() }()

	var row selectRow
	if decodeErr := json.NewDecoder(io.LimitReader(res.Body, 6*MaxContentSize+4096)).Decode(&row); decodeErr != nil {
		if decodeErr == io.EOF {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
		}

		return nil, fmt.Errorf("failed to decode ClickHouse response: %w", decodeErr)
	}

	fingerprint, err := hex.DecodeString(fingerprintHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode fingerprint: %w", err)
	}
	hash, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hash: %w", err)
	}

	plaintext := []byte(row.Content)
	if row.Encrypted {
		plaintext, err = decryptContent(row.Content, key, l.gcm)
	} else {
		key = nil
		if l.compressed {
			plaintext, err = base64.StdEncoding.DecodeString(row.Content)
		}
	}
	if err != nil {
		return nil, err
	}
	if l.compressed {
		plaintext, err = decompress(plaintext, MaxDecompressedSize)
		if err != nil {
			return nil, err
		}
	}
	previousFingerprint, err := hex.DecodeString(row.PreviousFingerprint)
	if err != nil {
		return nil, fmt.Errorf("invalid previous fingerprint: %w", err)
	}
	previousHash, err := hex.DecodeString(row.PreviousHash)
	if err != nil {
		return nil, fmt.Errorf("invalid previous hash: %w", err)
	}

	return &Paste{
		URL:                 url,
		Key:                 key,
		Fingerprint:         fingerprint,
		Hash:                hash,
		ReadCloser:          io.NopCloser(bytes.NewReader(plaintext)),
		QueryID:             res.Header.Get("X-ClickHouse-Query-Id"),
		PreviousFingerprint: previousFingerprint, PreviousHash: previousHash,
		Format: l.format, Compressed: l.compressed, NoWrap: l.nowrap, Sandbox: l.sandbox,
	}, nil
}

const selectDataQuery = `
SELECT
	toBool(is_encrypted) as is_encrypted,
	content,
	lower(hex(reinterpretAsFixedString(prev_hash))) AS prev_hash,
	lower(hex(reinterpretAsFixedString(prev_fingerprint))) AS prev_fingerprint
FROM data_view(fingerprint = {fingerprintHex:String}, hash = {hashHex:String})
FORMAT JSONEachRow`
const insertDataQuery = `
INSERT INTO data (hash_hex, fingerprint_hex, prev_hash_hex, prev_fingerprint_hex, is_encrypted, content)
FORMAT JSONEachRow`

type selectRow struct {
	Encrypted           bool   `json:"is_encrypted"`
	Content             string `json:"content"`
	PreviousHash        string `json:"prev_hash"`
	PreviousFingerprint string `json:"prev_fingerprint"`
}

type insertRow struct {
	Encrypted          bool   `json:"is_encrypted"`
	Content            string `json:"content"`
	HashHex            string `json:"hash_hex"`
	FingerprintHex     string `json:"fingerprint_hex"`
	PrevHashHex        string `json:"prev_hash_hex"`
	PrevFingerprintHex string `json:"prev_fingerprint_hex"`
}

type writeOptions struct {
	key                         []byte
	previousFingerprint         []byte
	previousHash                []byte
	encrypt                     bool
	format                      string
	compressed, nowrap, sandbox bool
}

type WriteOption func(*writeOptions)

func WithKey(key []byte) WriteOption {
	return func(o *writeOptions) {
		o.key = key
		o.encrypt = key != nil
	}
}

func WithEncryption() WriteOption { return func(o *writeOptions) { o.encrypt = true } }
func WithCompression(enabled bool) WriteOption {
	return func(o *writeOptions) { o.compressed = enabled }
}
func WithFormat(format string) WriteOption {
	return func(o *writeOptions) { o.format = strings.TrimPrefix(format, ".") }
}
func WithNoWrap(enabled bool) WriteOption  { return func(o *writeOptions) { o.nowrap = enabled } }
func WithSandbox(enabled bool) WriteOption { return func(o *writeOptions) { o.sandbox = enabled } }

func WithPreviousPaste(p *Paste) WriteOption {
	return func(o *writeOptions) {
		if p == nil {
			return
		}

		o.previousFingerprint = p.Fingerprint
		o.previousHash = p.Hash
		o.encrypt = len(p.Key) > 0
		o.format, o.compressed, o.nowrap, o.sandbox = p.Format, p.Compressed, p.NoWrap, p.Sandbox
	}
}

//nolint:funlen,gocyclo // Writing validates and transforms several independent format, compression, and encryption options.
func (s *Service) Write(input io.Reader, opt ...WriteOption) (*Paste, error) {
	opts := &writeOptions{}
	for _, o := range opt {
		o(opts)
	}

	if !validFormat(opts.format) {
		return nil, fmt.Errorf("unsupported format %q", opts.format)
	}
	if opts.sandbox && opts.format != "html" && opts.format != "htm" {
		return nil, fmt.Errorf("sandbox requires html or htm format")
	}
	var isEncrypted = opts.encrypt
	var content string
	b, readErr := io.ReadAll(io.LimitReader(input, MaxDecompressedSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("failed to read input: %w", readErr)
	}

	if len(b) > MaxDecompressedSize {
		return nil, fmt.Errorf("input exceeds %d bytes", MaxDecompressedSize)
	}
	if opts.compressed {
		var compressed bytes.Buffer
		w := gzip.NewWriter(&compressed)
		if _, err := w.Write(b); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		b = compressed.Bytes()
	}
	var actualKey []byte
	switch {
	case opts.encrypt:
		var err error
		actualKey, err = freshKey(opts.key)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(actualKey)
		if err != nil {
			return nil, fmt.Errorf("%w, failed to create AES cipher: %w", ErrInvalidKey, err)
		}

		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		encrypted := aead.Seal(nil, actualKey[:aead.NonceSize()], b, nil)

		content = base64.StdEncoding.EncodeToString(encrypted)
		isEncrypted = true
	case opts.compressed:
		content = base64.StdEncoding.EncodeToString(b)
	default:
		if !utf8.Valid(b) {
			return nil, fmt.Errorf("plaintext must be UTF-8; use encryption or gzip for binary content")
		}
		content = string(b)
	}
	if len(content) >= MaxContentSize {
		return nil, fmt.Errorf("stored content must be smaller than %d bytes", MaxContentSize)
	}

	hash := siphash128.SipHash128([]byte(content))
	fingerprint := bytes.Repeat([]byte{0xff}, 4)

	var buf bytes.Buffer

	if err := json.NewEncoder(&buf).Encode(insertRow{
		Encrypted:          isEncrypted,
		Content:            content,
		HashHex:            hex.EncodeToString(hash[:]),
		FingerprintHex:     hex.EncodeToString(fingerprint),
		PrevHashHex:        hex.EncodeToString(opts.previousHash),
		PrevFingerprintHex: hex.EncodeToString(opts.previousFingerprint),
	}); err != nil {
		return nil, fmt.Errorf("failed to encode insert row: %w", err)
	}

	req, err := s.clickHouseRequest(insertDataQuery, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse request: %w", err)
	}

	res, err := s.executeRequestWithParams(req, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute ClickHouse request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var keyAppend string
	if actualKey != nil {
		keyAppend = base64.StdEncoding.EncodeToString(actualKey) + "GCM"
	}
	if opts.nowrap {
		keyAppend += "&nowrap"
	}
	if opts.sandbox {
		keyAppend += "&sandbox"
	}
	if keyAppend != "" {
		keyAppend = "#" + keyAppend
	}
	extension := ""
	if opts.format != "" {
		extension = "." + opts.format
	}
	if opts.compressed {
		extension += ".gz"
	}

	pastilaURL := s.PastilaURL
	if pastilaURL == "" {
		pastilaURL = DefaultPastilaURL
	}

	return &Paste{
		URL: fmt.Sprintf("%s?%x/%x%s%s", pastilaURL, fingerprint, hash, extension, keyAppend),

		Hash:                hash[:],
		Fingerprint:         fingerprint,
		PreviousHash:        opts.previousHash,
		PreviousFingerprint: opts.previousFingerprint,

		Key:    actualKey,
		Format: opts.format, Compressed: opts.compressed, NoWrap: opts.nowrap, Sandbox: opts.sandbox,
		QueryID: res.Header.Get("X-ClickHouse-Query-Id"),
	}, nil
}

func (s *Service) executeRequestWithParams(request *http.Request, params map[string]string) (*http.Response, error) {
	reqQuery := request.URL.Query()
	for key, value := range params {
		reqQuery.Add("param_"+key, value)
	}
	request.URL.RawQuery = reqQuery.Encode()

	resp, err := HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to execute ClickHouse request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		responseBody := new(bytes.Buffer)
		_, _ = responseBody.ReadFrom(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		return nil, fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, responseBody.String())
	}
	if resp.Header.Get("X-ClickHouse-Query-Id") == "" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w, missing ClickHouse query id (check endpoint or authentication)", ErrInvalidURL)
	}

	return resp, nil
}

func (s *Service) clickHouseRequest(query string, body io.Reader) (*http.Request, error) {
	clickHouseURL := s.ClickHouseURL
	if clickHouseURL == "" {
		clickHouseURL = DefaultClickHouseURL
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, clickHouseURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse request: %w", err)
	}

	if s.AuthCookie != "" {
		req.AddCookie(&http.Cookie{Name: "auth", Value: s.AuthCookie})
	}

	urlQuery := req.URL.Query()
	urlQuery.Add("query", query)

	req.URL.RawQuery = urlQuery.Encode()
	req.Header.Set("User-Agent", "PastilaCLI/1.0")

	return req, nil
}
