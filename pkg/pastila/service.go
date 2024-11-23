package pastila

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/frifox/siphash128"
	"io"
	"net/http"
	"regexp"
)

var HTTPClient = http.DefaultClient
var DefaultClickHouseURL = "https://play.clickhouse.com/?user=paste"
var DefaultPastilaURL = "https://pastila.nl/"

var (
	ErrInvalidUrl  = fmt.Errorf("invalid pastila url")
	ErrNotFound    = fmt.Errorf("data not found")
	ErrKeyRequired = fmt.Errorf("key is required for encrypted data")
	ErrInvalidKey  = fmt.Errorf("invalid key")
)

var QueryMatchRegex = regexp.MustCompile(`([a-f0-9]+)/([a-f0-9]+)(?:#(.+))?$`)

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
		return nil, fmt.Errorf("%w: %s", ErrInvalidUrl, url)
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

	req, err := s.clickHouseRequest(requestDataQuery, nil)
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

	buf := make([]byte, 2)
	_, err = res.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("%w for calcFingerprint %s and hash %s", ErrNotFound, fingerprintHex, hashHex)
		}

		return nil, fmt.Errorf("failed to read ClickHouse response: %w", err)
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
	if buf[0] == '0' {
		return &Paste{
			URL:         url,
			Key:         key,
			Fingerprint: fingerprint,
			Hash:        hash,
			ReadCloser:  res.Body,
			QueryID:     res.Header.Get("X-ClickHouse-Query-Id"),
		}, nil
	}

	defer res.Body.Close()

	if len(key) == 0 {
		return nil, ErrKeyRequired
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w, failed to create AES cipher: %w", ErrInvalidKey, err)
	}

	decoder := base64.NewDecoder(base64.StdEncoding, res.Body)
	ciphertext, err := io.ReadAll(decoder)
	if err != nil {
		return nil, fmt.Errorf("%w, failed to read ciphertext: %w", ErrInvalidKey, err)
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

const requestDataQuery = `SELECT is_encrypted, content FROM data WHERE fingerprint = reinterpretAsUInt32(unhex({fingerprintHex:String})) AND hash = reinterpretAsUInt128(unhex({hashHex:String})) ORDER BY time LIMIT 1 FORMAT Raw`
const insertDataQuery = `INSERT INTO data (hash_hex, fingerprint_hex, prev_hash_hex, prev_fingerprint_hex, is_encrypted, content) FORMAT Raw`

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

	content, err := io.ReadAll(input)

	var isEncrypted = []byte{'0'}
	if opts.key != nil {
		block, err := aes.NewCipher(opts.key)
		if err != nil {
			return nil, fmt.Errorf("%w, failed to create AES cipher: %w", ErrInvalidKey, err)
		}

		iv := make([]byte, aes.BlockSize)
		stream := cipher.NewCTR(block, iv)
		encrypted := make([]byte, len(content))
		stream.XORKeyStream(encrypted, content)

		content = encrypted
		isEncrypted = []byte{'1'}
	}

	hash := siphash128.SipHash128(content)
	fingerprint := bytes.Repeat([]byte{0xff}, 4)

	var buf bytes.Buffer

	separator := []byte{'\t'}
	buf.WriteString(fmt.Sprintf("%x", hash))
	buf.Write(separator)
	buf.WriteString(fmt.Sprintf("%x", fingerprint))
	buf.Write(separator)
	buf.WriteString(fmt.Sprintf("%x", opts.previousHash))
	buf.Write(separator)
	buf.WriteString(fmt.Sprintf("%x", opts.previousFingerprint))
	buf.Write(separator)
	buf.Write(isEncrypted)
	buf.Write(separator)
	buf.Write(content)

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
		pastilaURL = DefaultPastilaURL
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
		return nil, fmt.Errorf("%w, missing query id", ErrInvalidUrl)
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
	clickHouseUrl := s.ClickHouseURL
	if clickHouseUrl == "" {
		clickHouseUrl = DefaultClickHouseURL
	}

	req, err := http.NewRequest(http.MethodPost, clickHouseUrl, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse request: %w", err)
	}

	urlQuery := req.URL.Query()
	urlQuery.Add("query", query)

	req.URL.RawQuery = urlQuery.Encode()
	req.Header.Set("User-Agent", "PastilaCLI/1.0")

	return req, nil
}
