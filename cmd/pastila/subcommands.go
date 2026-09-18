package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jkaflik/pastila-cli/pkg/auth"
	"github.com/jkaflik/pastila-cli/pkg/config"
	"github.com/jkaflik/pastila-cli/pkg/pastila"
)

const edgeMethodBlockedSnippet = "supports only cachable requests"

var clickHouseURLRegex = regexp.MustCompile(`(?m)clickhouse_url\s*=\s*["']([^"']+)["']`)
var clickHouseURLJSONRegex = regexp.MustCompile(`(?m)["']clickhouse_url["']\s*:\s*["']([^"']+)["']`)
var fetchPostURLRegex = regexp.MustCompile(`(?is)fetch\(\s*["']([^"']+)["']\s*,\s*\{[^}]*method\s*:\s*["']POST["']`)
var scriptSrcRegex = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+)["']`)

type endpointProbe struct {
	URL            string
	StatusCode     int
	Location       string
	BodySnippet    string
	QueryID        string
	Err            error
	AuthRequired   bool
	MethodBlocked  bool
	MissingQueryID bool
	OK             bool
}

var saveAuthCookie = auth.SaveCookie

func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var baseURL string
	var clickHouseURL string
	var noOpen bool
	fs.StringVar(&baseURL, "url", "", "Pastila base URL")
	fs.StringVar(&clickHouseURL, "clickhouse-url", "", "ClickHouse API URL override")
	fs.BoolVar(&noOpen, "no-open", false, "Do not open browser automatically for auth flow")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if strings.TrimSpace(baseURL) == "" {
		input, err := promptLine("Pastila URL: ")
		if err != nil {
			printf("Failed to read input: %v\n", err)
			return 1
		}
		baseURL = input
	}

	normalizedBaseURL, err := normalizePastilaURL(baseURL)
	if err != nil {
		printf("Invalid Pastila URL: %v\n", err)
		return 1
	}

	authCookie := ""
	if cookieFromKeychain, loadErr := loadAuthCookie(normalizedBaseURL); loadErr == nil {
		authCookie = cookieFromKeychain
	}

	detectedClickHouseURL, probes, err := configureSetupEndpoint(normalizedBaseURL, clickHouseURL, authCookie, noOpen)

	if err != nil {
		printf("Failed to detect working ClickHouse endpoint: %v\n", err)
		for _, probe := range probes {
			printf("- %s -> %s\n", probe.URL, formatQueryProbe(&probe))
		}
		printf("Setup aborted (strict mode).\n")
		return 1
	}

	configPath, err := config.DefaultPath()
	if err != nil {
		printf("Failed to resolve config path: %v\n", err)
		return 1
	}

	if err := config.Save(configPath, &config.FileConfig{
		PastilaURL:    normalizedBaseURL,
		ClickHouseURL: detectedClickHouseURL,
	}); err != nil {
		printf("Failed to save config: %v\n", err)
		return 1
	}

	printf("Config saved to %s\n", configPath)
	printf("Pastila URL: %s\n", normalizedBaseURL)
	printf("Detected ClickHouse URL: %s\n", detectedClickHouseURL)
	printf("Setup complete. Future pastila commands will use this destination.\n")

	return 0
}

func configureSetupEndpoint(baseURL, clickHouseURL, authCookie string, noOpen bool) (string, []endpointProbe, error) {
	detectedClickHouseURL, effectiveCookie, probes, err := detectSetupEndpoint(baseURL, clickHouseURL, authCookie, noOpen)
	if err == nil {
		if effectiveCookie != "" && effectiveCookie != authCookie {
			if saveErr := saveAuthCookie(baseURL, effectiveCookie); saveErr != nil {
				return "", probes, fmt.Errorf("failed to save cookie in keychain: %w", saveErr)
			}
		}
		return detectedClickHouseURL, probes, nil
	}

	fallbackURL, fallbackCookie, fallbackErr := tryManualSetupFallback(baseURL, effectiveCookie)
	if fallbackErr != nil {
		return "", probes, err
	}

	detectedClickHouseURL, probes, err = detectClickHouseEndpoint(baseURL, fallbackURL, nil, fallbackCookie)
	if err != nil {
		return "", probes, err
	}

	if strings.TrimSpace(fallbackCookie) != "" {
		if saveErr := saveAuthCookie(baseURL, parseAuthCookieValue(fallbackCookie)); saveErr != nil {
			return "", probes, fmt.Errorf("failed to save cookie in keychain: %w", saveErr)
		}
	}

	return detectedClickHouseURL, probes, nil
}

func detectSetupEndpoint(baseURL, clickHouseURL, authCookie string, noOpen bool) (
	detectedClickHouseURL string,
	effectiveCookie string,
	probes []endpointProbe,
	err error,
) {
	currentCookie := authCookie
	authFlowUsed := false
	if setupNeedsAuth(baseURL, currentCookie) {
		cookie, authErr := loginWithGuidance(baseURL, noOpen)
		if authErr != nil {
			return "", currentCookie, nil, fmt.Errorf("failed to set up auth: %w", authErr)
		}
		currentCookie = cookie
		authFlowUsed = true
	}

	discoveredCandidates, discoverErr := discoverClickHouseCandidatesFromUI(baseURL, clickHouseURL, currentCookie)
	if discoverErr != nil && strings.TrimSpace(clickHouseURL) == "" {
		printf("Could not auto-discover ClickHouse URL from UI: %v\n", discoverErr)
	}

	detectedClickHouseURL, probes, err = detectClickHouseEndpoint(baseURL, clickHouseURL, discoveredCandidates, currentCookie)
	if err != nil && !authFlowUsed && shouldRetrySetupWithAuth(probes) {
		printf("Detected an authentication challenge during endpoint probe. Running auth flow...\n")
		cookie, authErr := loginWithGuidance(baseURL, noOpen)
		if authErr != nil {
			return "", currentCookie, probes, fmt.Errorf("failed to set up auth: %w", authErr)
		}
		currentCookie = cookie

		discoveredCandidates, discoverErr = discoverClickHouseCandidatesFromUI(baseURL, clickHouseURL, currentCookie)
		if discoverErr != nil && strings.TrimSpace(clickHouseURL) == "" {
			printf("Could not auto-discover ClickHouse URL from UI: %v\n", discoverErr)
		}
		detectedClickHouseURL, probes, err = detectClickHouseEndpoint(baseURL, clickHouseURL, discoveredCandidates, currentCookie)
	}

	if err == nil && len(discoveredCandidates) > 0 {
		printf("Discovered ClickHouse URL from Pastila UI: %s\n", discoveredCandidates[0])
	}

	if err != nil {
		return "", currentCookie, probes, err
	}

	return detectedClickHouseURL, currentCookie, probes, nil
}

func shouldRetrySetupWithAuth(probes []endpointProbe) bool {
	for _, probe := range probes {
		if probe.AuthRequired || probe.MissingQueryID {
			return true
		}
	}

	return false
}

func tryManualSetupFallback(baseURL, currentCookie string) (
	manualURL string,
	manualCookie string,
	err error,
) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", "", fmt.Errorf("manual fallback requires interactive terminal")
	}

	printf("Automatic endpoint detection failed.\n")
	printf("If your private UI hides query endpoint, you can paste it manually from browser DevTools Network.\n")
	printf("- In browser, perform any paste/read action and copy the Request URL of the POST query request.\n")

	manualURL, err = promptLine("Query request URL (empty to abort): ")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(manualURL) == "" {
		return "", "", fmt.Errorf("manual fallback skipped")
	}

	normalizedURL, err := normalizeURL(manualURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid manual query url: %w", err)
	}

	printf("Optional: paste the auth cookie value accepted by PASTILA_COOKIE.\n")
	printf("If skipped, setup uses previously provided cookie for %s.\n", baseURL)
	manualCookie, err = promptSecretLine("Auth cookie (optional): ")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(manualCookie) == "" {
		return normalizedURL, currentCookie, nil
	}

	return normalizedURL, manualCookie, nil
}

func loginWithGuidance(baseURL string, noOpen bool) (string, error) {
	if !noOpen {
		if err := openBrowser(baseURL); err != nil {
			printf("Could not open browser automatically: %v\n", err)
			printf("Open this URL manually: %s\n", baseURL)
		} else {
			printf("Opened browser: %s\n", baseURL)
		}
	}

	printf("1) Sign in to Pastila in your browser if required.\n")
	printf("2) Copy the auth cookie value accepted by PASTILA_COOKIE.\n")
	printf("3) Paste the copied value below.\n")

	input, err := promptSecretLine("Cookie: ")
	if err != nil {
		return "", fmt.Errorf("failed to read cookie: %w", err)
	}

	authCookie := parseAuthCookieValue(input)
	if authCookie == "" {
		return "", fmt.Errorf("cookie is empty")
	}

	return authCookie, nil
}

func setupNeedsAuth(baseURL, authCookie string) bool {
	if authCookie != "" {
		landingWithCookie := probeLanding(baseURL, authCookie)
		if landingWithCookie.OK {
			return false
		}
		if landingWithCookie.AuthRequired {
			return true
		}
	}

	landingWithoutCookie := probeLanding(baseURL, "")
	return landingWithoutCookie.AuthRequired
}

func detectClickHouseEndpoint(
	baseURL,
	explicitURL string,
	discoveredCandidates []string,
	authCookie string,
) (string, []endpointProbe, error) {
	candidates, err := buildClickHouseCandidates(baseURL, explicitURL, discoveredCandidates)
	if err != nil {
		return "", nil, err
	}

	probes := make([]endpointProbe, 0, len(candidates))
	for _, candidate := range candidates {
		// Do not persist an automatically discovered cross-origin backend with
		// a UI credential. An explicit backend override is required to trust it.
		if authCookie != "" && explicitURL == "" && !sameHost(baseURL, candidate) {
			probes = append(probes, endpointProbe{URL: candidate, Err: fmt.Errorf("cross-origin authenticated endpoint requires -clickhouse-url")})
			continue
		}
		cookie := ""
		if sameHost(baseURL, candidate) || (explicitURL != "" && sameHost(explicitURL, candidate)) {
			cookie = authCookie
		}
		probe := probeQueryEndpoint(candidate, cookie)
		probes = append(probes, probe)
		if probe.OK {
			return candidate, probes, nil
		}
	}

	for _, probe := range probes {
		if probe.AuthRequired {
			return "", probes, fmt.Errorf("authentication required for query endpoint")
		}
	}

	return "", probes, fmt.Errorf("no candidate endpoint passed query probe")
}

func buildClickHouseCandidates(baseURL, explicitURL string, discoveredURLs []string) ([]string, error) {
	uniq := make(map[string]struct{})
	candidates := make([]string, 0, 8)

	addCandidate := func(rawURL string) error {
		variants, err := endpointVariants(rawURL)
		if err != nil {
			return err
		}

		for _, variant := range variants {
			if _, exists := uniq[variant]; exists {
				continue
			}
			uniq[variant] = struct{}{}
			candidates = append(candidates, variant)
		}

		return nil
	}

	if strings.TrimSpace(explicitURL) != "" {
		if err := addCandidate(explicitURL); err != nil {
			return nil, fmt.Errorf("invalid explicit clickhouse url: %w", err)
		}

		return candidates, nil
	}

	for _, discoveredURL := range discoveredURLs {
		if strings.TrimSpace(discoveredURL) == "" {
			continue
		}

		if err := addCandidate(discoveredURL); err != nil {
			return nil, fmt.Errorf("invalid discovered clickhouse url: %w", err)
		}
	}

	if err := addCandidate(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base url: %w", err)
	}

	if strings.TrimSpace(explicitURL) == "" && baseURL == pastila.DefaultPastilaURL {
		if err := addCandidate(pastila.DefaultClickHouseURL); err != nil {
			return nil, fmt.Errorf("invalid default clickhouse url: %w", err)
		}
	}

	return candidates, nil
}

func discoverClickHouseCandidatesFromUI(baseURL, explicitURL, authCookie string) ([]string, error) {
	if strings.TrimSpace(explicitURL) != "" {
		return nil, nil
	}

	body, err := fetchURLWithAuth(baseURL, authCookie)
	if err != nil {
		return nil, err
	}

	collected := make([]string, 0, 8)
	seen := make(map[string]struct{})
	addCollected := func(rawURL string) {
		if strings.TrimSpace(rawURL) == "" {
			return
		}
		normalized, normalizeErr := normalizeAndResolveURL(baseURL, rawURL)
		if normalizeErr != nil {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		collected = append(collected, normalized)
	}

	for _, discovered := range extractClickHouseURLsFromSource(body) {
		addCollected(discovered)
	}

	for _, scriptURL := range extractScriptURLs(baseURL, body) {
		scriptCookie := ""
		if sameHost(baseURL, scriptURL) {
			scriptCookie = authCookie
		}
		scriptBody, fetchErr := fetchURLWithAuth(scriptURL, scriptCookie)
		if fetchErr != nil {
			continue
		}

		for _, discovered := range extractClickHouseURLsFromSource(scriptBody) {
			addCollected(discovered)
		}
	}

	if len(collected) == 0 {
		return nil, fmt.Errorf("clickhouse_url not found in UI or scripts")
	}

	return collected, nil
}

func fetchURLWithAuth(rawURL, authCookie string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return "", err
	}
	applyAuthCookie(req, authCookie)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d while fetching %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

func extractClickHouseURLsFromSource(source string) []string {
	matchesSet := make(map[string]struct{})
	results := make([]string, 0, 6)

	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, exists := matchesSet[v]; exists {
			return
		}
		matchesSet[v] = struct{}{}
		results = append(results, v)
	}

	for _, match := range clickHouseURLRegex.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}
	for _, match := range clickHouseURLJSONRegex.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}
	for _, match := range fetchPostURLRegex.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			add(match[1])
		}
	}

	return results
}

func extractScriptURLs(baseURL, html string) []string {
	results := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, match := range scriptSrcRegex.FindAllStringSubmatch(html, -1) {
		if len(match) != 2 {
			continue
		}
		normalized, err := normalizeAndResolveURL(baseURL, match[1])
		if err != nil {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		results = append(results, normalized)
	}

	return results
}

func normalizeAndResolveURL(baseURL, candidate string) (string, error) {
	baseParsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse base url: %w", err)
	}

	parsedCandidate, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return "", fmt.Errorf("failed to parse candidate url: %w", err)
	}

	if !parsedCandidate.IsAbs() {
		parsedCandidate = baseParsed.ResolveReference(parsedCandidate)
	}

	return normalizeURL(parsedCandidate.String())
}

func sameHost(firstURL, secondURL string) bool {
	first, firstErr := url.Parse(firstURL)
	second, secondErr := url.Parse(secondURL)
	if firstErr != nil || secondErr != nil {
		return false
	}

	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func endpointVariants(rawURL string) ([]string, error) {
	normalizedURL, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(normalizedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}

	if u.Path == "" {
		u.Path = "/"
	}

	variants := []string{u.String()}
	trimmedPath := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(trimmedPath, "/query") && !strings.HasSuffix(trimmedPath, "/play") {
		for _, path := range []string{"/query", "/play"} {
			candidate := *u
			candidate.Path = trimmedPath + path
			variants = append(variants, candidate.String())
		}
	}

	return variants, nil
}

func probeLanding(rawURL, authCookie string) endpointProbe {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return endpointProbe{URL: rawURL, Err: err}
	}
	applyAuthCookie(req, authCookie)

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return endpointProbe{URL: rawURL, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	location := resp.Header.Get("Location")
	probe := endpointProbe{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		Location:     location,
		AuthRequired: isAuthResponse(resp),
		OK:           resp.StatusCode == http.StatusOK,
	}

	if !probe.OK {
		probe.BodySnippet = readBodySnippet(resp.Body)
	}

	return probe
}

func probeQueryEndpoint(rawURL, authCookie string) endpointProbe {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, http.NoBody)
	if err != nil {
		return endpointProbe{URL: rawURL, Err: err}
	}
	applyAuthCookie(req, authCookie)

	query := req.URL.Query()
	query.Add("query", "SELECT 1 FORMAT JSONEachRow")
	req.URL.RawQuery = query.Encode()

	resp, err := noRedirectHTTPClient().Do(req)
	if err != nil {
		return endpointProbe{URL: rawURL, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	bodySnippet := readBodySnippet(resp.Body)
	location := resp.Header.Get("Location")
	queryID := resp.Header.Get("X-ClickHouse-Query-Id")
	missingQueryID := resp.StatusCode == http.StatusOK && queryID == ""

	probe := endpointProbe{
		URL:            rawURL,
		StatusCode:     resp.StatusCode,
		Location:       location,
		BodySnippet:    bodySnippet,
		QueryID:        queryID,
		Err:            nil,
		AuthRequired:   isAuthResponse(resp),
		MethodBlocked:  isEdgeMethodBlocked(resp, bodySnippet),
		MissingQueryID: missingQueryID,
		OK:             resp.StatusCode == http.StatusOK && queryID != "",
	}

	return probe
}

func formatQueryProbe(probe *endpointProbe) string {
	if probe.Err != nil {
		return fmt.Sprintf("FAIL (%v)", probe.Err)
	}

	if probe.OK {
		return "OK"
	}

	if probe.AuthRequired {
		return "WARN (authentication required)"
	}

	if probe.MethodBlocked {
		return "FAIL (request method blocked by edge; endpoint likely not query API)"
	}

	if probe.MissingQueryID {
		return "FAIL (missing X-ClickHouse-Query-Id header)"
	}

	if probe.StatusCode != 0 {
		return fmt.Sprintf("FAIL (status %d)", probe.StatusCode)
	}

	return "FAIL"
}

func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readBodySnippet(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, 256))
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return ""
	}

	return strings.ReplaceAll(trimmed, "\n", " ")
}

func isAuthResponse(resp *http.Response) bool {
	return resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		(resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest)
}

func isEdgeMethodBlocked(resp *http.Response, bodySnippet string) bool {
	if resp.StatusCode != http.StatusForbidden {
		return false
	}

	if strings.Contains(strings.ToLower(bodySnippet), edgeMethodBlockedSnippet) {
		return true
	}

	xCache := strings.ToLower(resp.Header.Get("X-Cache"))
	server := strings.ToLower(resp.Header.Get("Server"))
	return strings.Contains(xCache, "error from cloudfront") && strings.Contains(server, "cloudfront")
}

func applyAuthCookie(req *http.Request, rawCookie string) {
	value := parseAuthCookieValue(rawCookie)
	if value == "" {
		return
	}

	req.AddCookie(&http.Cookie{Name: "auth", Value: value})
}

func promptLine(prompt string) (string, error) {
	printf("%s", prompt)

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimSpace(input), nil
}

func promptSecretLine(prompt string) (string, error) {
	printf("%s", prompt)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		secret, err := term.ReadPassword(fd)
		printf("\n")
		if err != nil {
			return "", err
		}

		return strings.TrimSpace(string(secret)), nil
	}

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimSpace(input), nil
}

func parseAuthCookieValue(input string) string {
	v := strings.TrimSpace(input)
	v = strings.TrimPrefix(v, "Cookie:")
	v = strings.TrimPrefix(v, "cookie:")
	v = strings.TrimSpace(v)

	if strings.HasPrefix(v, "auth=") {
		return strings.TrimPrefix(v, "auth=")
	}

	if strings.Contains(v, ";") {
		for _, part := range strings.Split(v, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "auth=") {
				return strings.TrimPrefix(part, "auth=")
			}
		}

		return ""
	}

	return v
}

func openBrowser(rawURL string) error {
	if _, err := url.Parse(rawURL); err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL) // #nosec G204
	case "linux":
		cmd = exec.Command("xdg-open", rawURL) // #nosec G204
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL) // #nosec G204
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start browser command: %w", err)
	}

	return nil
}
