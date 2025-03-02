package pastila

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/frifox/siphash128"
)

var HTTPClient = http.DefaultClient
var DefaultClickHouseURL = "https://play.clickhouse.com/?user=paste"
var chURL = "https://pastila.nl/"

var (
	ErrInvalidURL  = fmt.Errorf("invalid pastila url")
	ErrNotFound    = fmt.Errorf("pastila not found")
	ErrKeyRequired = fmt.Errorf("key is required for encrypted data")
	ErrInvalidKey  = fmt.Errorf("invalid key")
)

var QueryMatchRegex = regexp.MustCompile(`(?m)([a-f0-9]+)/([a-f0-9]+)(?:#(.+))?$`)

type Paste struct {
	io.ReadCloser

	URL string

	Fingerprint         []byte
	Hash                []byte
	PreviousFingerprint []byte
	PreviousHash        []byte

	Key []byte

	QueryID string
}

type Service struct {
	// PastilaURL is the URL of the pastila service. Used to generate URLs for writing data.
	PastilaURL string

	// ClickHouseURL is the URL of the ClickHouse service. Used to read and write data.
	ClickHouseURL string
}

func (s *Service) Read(url string) (*Paste, error) {
	matches := QueryMatchRegex.FindStringSubmatch(url)
	if matches == nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, url)
	}

	fingerprintHex := matches[1]
	hashHex := matches[2]

	var key []byte
	if len(matches) == 4 {
		var err error
		key, err = base64.StdEncoding.DecodeString(matches[3])
		if err != nil {
			return nil, fmt.Errorf("%w, failed to base64 decode: %w", ErrInvalidKey, err)
		}
	}

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

	defer res.Body.Close()

	var row selectRow
	if decodeErr := json.NewDecoder(res.Body).Decode(&row); decodeErr != nil {
		if decodeErr == io.EOF {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
		}

		return nil, fmt.Errorf("failed to decode ClickHouse response: %w", err)
	}

	fingerprint, err := hex.DecodeString(fingerprintHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode fingerprint: %w", err)
	}
	hash, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hash: %w", err)
	}

	// data is not encrypted, return as is
	if !row.Encrypted {
		return &Paste{
			URL:         url,
			Key:         key,
			Fingerprint: fingerprint,
			Hash:        hash,
			ReadCloser:  io.NopCloser(bytes.NewBufferString(row.Content)),
			QueryID:     res.Header.Get("X-ClickHouse-Query-Id"),
		}, nil
	}

	if len(key) == 0 {
		return nil, ErrKeyRequired
	}

	ciphertext, err := base64.StdEncoding.DecodeString(row.Content)
	if err != nil {
		return nil, fmt.Errorf("%w, failed to decode base64 ciphertext: %w", ErrInvalidKey, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w, failed to create AES cipher: %w", ErrInvalidKey, err)
	}
	iv := make([]byte, aes.BlockSize)
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCTR(block, iv).XORKeyStream(plaintext, ciphertext)

	return &Paste{
		URL:         url,
		Key:         key,
		Fingerprint: fingerprint,
		Hash:        hash,
		ReadCloser:  io.NopCloser(bytes.NewReader(plaintext)),
		QueryID:     res.Header.Get("X-ClickHouse-Query-Id"),
	}, nil
}

const selectDataQuery = `
SELECT
	toBool(is_encrypted) as is_encrypted,
	content
FROM data
WHERE
    fingerprint = reinterpretAsUInt32(unhex({fingerprintHex:String})) AND
    hash = reinterpretAsUInt128(unhex({hashHex:String}))
ORDER BY time LIMIT 1 FORMAT JSONEachRow`
const insertDataQuery = `
INSERT INTO data (hash_hex, fingerprint_hex, prev_hash_hex, prev_fingerprint_hex, is_encrypted, content)
FORMAT JSONEachRow`

type selectRow struct {
	Encrypted bool   `json:"is_encrypted"`
	Content   string `json:"content"`
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
	key                 []byte
	previousFingerprint []byte
	previousHash        []byte
}

type WriteOption func(*writeOptions)

func WithKey(key []byte) WriteOption {
	return func(o *writeOptions) {
		o.key = key
	}
}

func WithPreviousPaste(p *Paste) WriteOption {
	return func(o *writeOptions) {
		if p == nil {
			return
		}

		o.previousFingerprint = p.Fingerprint
		o.previousHash = p.Hash
		o.key = p.Key
	}
}

func (s *Service) Write(input io.Reader, opt ...WriteOption) (*Paste, error) {
	opts := &writeOptions{}
	for _, o := range opt {
		o(opts)
	}

	var isEncrypted bool
	var content string
	b, readErr := io.ReadAll(input)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read input: %w", readErr)
	}

	if opts.key != nil {
		block, err := aes.NewCipher(opts.key)
		if err != nil {
			return nil, fmt.Errorf("%w, failed to create AES cipher: %w", ErrInvalidKey, err)
		}

		iv := make([]byte, aes.BlockSize)
		stream := cipher.NewCTR(block, iv)
		encrypted := make([]byte, len(b))
		stream.XORKeyStream(encrypted, b)

		content = base64.StdEncoding.EncodeToString(encrypted)
		isEncrypted = true
	} else {
		content = string(b)
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
	defer res.Body.Close()

	var keyAppend string
	if opts.key != nil {
		keyAppend = "#" + base64.StdEncoding.EncodeToString(opts.key)
	}

	pastilaURL := s.PastilaURL
	if pastilaURL == "" {
		pastilaURL = chURL
	}

	return &Paste{
		URL: fmt.Sprintf("%s?%x/%x%s", pastilaURL, fingerprint, hash, keyAppend),

		Hash:                hash[:],
		Fingerprint:         fingerprint,
		PreviousHash:        opts.previousHash,
		PreviousFingerprint: opts.previousFingerprint,

		Key:     opts.key,
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

	if resp.Header.Get("X-ClickHouse-Query-Id") == "" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w, missing query id", ErrInvalidURL)
	}

	if resp.StatusCode != http.StatusOK {
		responseBody := new(bytes.Buffer)
		_, _ = responseBody.ReadFrom(resp.Body)
		_ = resp.Body.Close()

		return nil, fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, responseBody.String())
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

	urlQuery := req.URL.Query()
	urlQuery.Add("query", query)

	req.URL.RawQuery = urlQuery.Encode()
	req.Header.Set("User-Agent", "PastilaCLI/1.0")

	return req, nil
}
