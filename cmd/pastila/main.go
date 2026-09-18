package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jkaflik/pastila-cli/pkg/pastila"
)

// These variables are set during build by goreleaser
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const trueFlagValue = "true"

var (
	fileName                                         string
	showSummary                                      bool
	teeFlag                                          bool
	launchEditorFlag                                 bool
	plain                                            bool
	key                                              string
	compressFlag, nowrapFlag, sandboxFlag            bool
	formatFlag, baseURLFlag, backendFlag, cookieFlag string
)

var printWriter io.Writer = os.Stdout

func printf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(printWriter, format, args...)
}

var printUsage = func() {
	printf("Pastila CLI is a command line utility to read and write from pastila.nl copy-paste service.\n")
	printf("See a GitHub repository for more information: https://github.com/ClickHouse/pastila\n\n")
	printf("Usage: %s [options] [URL]\n\n", os.Args[0])
	printf("\t[URL] can be a pastila URL or \"-\" to read from URL stdin.\n\nAvailable options:\n\n")
	flag.PrintDefaults()
	printf("\nAvailable commands:\n\n")
	printf("  setup\tConfigure a custom Pastila deployment.\n")
	printf("  auth\tLogin, inspect or clear stored authentication.\n")
	printf("  doctor\tCheck configuration and endpoint connectivity.\n")
	printf("  skill\tInstall the Pastila agent skill.\n")
	printf("\nRead data goes into output, anything else goes into stderr.\n")
	printf("When writing to pastila, URL will be printed to stdout.\n")
}

func main() {
	os.Exit(runCLI())
}

//nolint:funlen,gocyclo // The CLI entrypoint coordinates independent command, read, write, and editor modes.
func runCLI() int {
	printWriter = os.Stderr
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			return runSetup(os.Args[2:])
		case "auth":
			return runAuth(os.Args[2:])
		case "doctor":
			return runDoctor(os.Args[2:])
		case "skill":
			return runSkill(os.Args[2:])
		}
	}

	setupFlags()
	if flag.Lookup("version").Value.String() == trueFlagValue {
		return 0
	}
	if flag.NArg() > 1 {
		printf("expected at most one URL\n")
		return 2
	}
	if plain && key != "" {
		printf("-plain and -key cannot be combined\n")
		return 2
	}

	var stdin io.Reader
	var err error
	if info, statErr := os.Stdin.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice == 0 {
		stdin = os.Stdin
	}
	pasteURL := flag.Arg(0)

	if pasteURL == "-" {
		pasteURL, err = readURL(stdin)
		if err != nil {
			printf("%v\n", err)
			return 1
		}
	}

	resolved, err := resolveConfig(baseURLFlag, backendFlag, cookieFlag)
	if err != nil {
		printf("Failed to resolve configuration: %v\n", err)
		return 1
	}

	service := pastila.Service{
		PastilaURL:    resolved.PastilaURL,
		ClickHouseURL: resolved.ClickHouseURL,
		AuthCookie:    resolved.AuthCookie,
	}

	if pasteURL != "" {
		if readErr := readPaste(service, pasteURL); readErr != nil {
			printf("%v\n", readErr)
			return 1
		}

		return 0
	}

	formatFlag, sandboxFlag, err = resolveLinkOptions(fileName, "", false, flag.CommandLine)
	if err != nil {
		printf("%v\n", err)
		return 2
	}
	var reader io.Reader
	if fileName != "" && fileName != "-" {
		reader, err = os.Open(fileName)
		if err != nil {
			printf("failed to open file %s: %v\n", fileName, err)
			return 1
		}
		defer func() { _ = reader.(*os.File).Close() }()
	} else {
		reader = stdin
	}

	if launchEditorFlag {
		if reader == nil {
			reader = strings.NewReader("")
		}
		p := &pastila.Paste{
			ReadCloser: io.NopCloser(reader),
			Format:     formatFlag,
			Compressed: compressFlag,
			NoWrap:     nowrapFlag,
			Sandbox:    sandboxFlag,
		}
		if !plain {
			p.Key, err = generateRandomKey()
			if err != nil {
				printf("%v\n", err)
				return 1
			}
		}
		if err := editPaste(service, p); err != nil {
			printf("%v\n", err)
			return 1
		}
		return 0
	}
	if reader == nil {
		printUsage()
		return 1
	}

	if writeErr := writePaste(service, reader); writeErr != nil {
		printf("%v\n", writeErr)
		return 1
	}

	return 0
}

func writePaste(service pastila.Service, contentReader io.Reader) error {
	var reader = contentReader
	if teeFlag {
		printWriter = os.Stderr
		reader = io.TeeReader(reader, os.Stdout)
	}

	var err error
	var k []byte
	if !plain {
		if key == "" {
			k, err = generateRandomKey()
			if err != nil {
				return fmt.Errorf("failed to generate random key: %w", err)
			}
		} else {
			if _, statErr := os.Stat(key); statErr == nil {
				k, err = os.ReadFile(key)
				if err != nil {
					return fmt.Errorf("failed to read key from file %s: %w", key, err)
				}
			} else {
				k = []byte(key)
			}
		}
	}

	result, err := service.Write(
		reader,
		pastila.WithKey(k),
		pastila.WithCompression(compressFlag),
		pastila.WithFormat(formatFlag),
		pastila.WithNoWrap(nowrapFlag),
		pastila.WithSandbox(sandboxFlag),
	)
	if err != nil {
		return fmt.Errorf("failed to write paste: %w", err)
	}

	out := io.Writer(os.Stdout)
	if teeFlag {
		out = os.Stderr
	}
	_, _ = fmt.Fprintln(out, result.URL)
	return nil
}

func readURL(r io.Reader) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no URL provided in stdin, but \"-\" was passed as URL")
	}

	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read pastila URL from stdin: %w", err)
		}
		return "", fmt.Errorf("no URL provided in stdin")
	}
	value := strings.TrimSpace(scanner.Text())
	if value == "" {
		return "", fmt.Errorf("no URL provided in stdin")
	}
	return value, nil
}

func setupFlags() {
	flag.StringVar(
		&fileName,
		"f",
		"",
		"Content file path. Use \"-\" to read from stdin. If not provided, content will be read from stdin.",
	)
	flag.BoolVar(
		&plain,
		"plain",
		false,
		"Do not encrypt content. Default is to encrypt content.",
	)
	flag.StringVar(
		&key,
		"key",
		"",
		"16/24/32-byte key material or file path; derives a fresh per-paste key. Default: random 128-bit key.",
	)
	flag.BoolVar(
		&showSummary,
		"s",
		false,
		"Show query summary after reading from pastila",
	)
	flag.BoolVar(
		&launchEditorFlag,
		"e",
		false,
		`Launch editor to edit content. If URL is provided, editor will be launched with a content read from pastila.
				Use EDITOR environment variable to set editor. Otherwise, vi will be used.`,
	)
	flag.BoolVar(
		&teeFlag,
		"teeFlag",
		false,
		"Write to output and to pastila. URL will be printed to stderr.",
	)
	flag.Bool(
		"version",
		false,
		"Print version information and exit",
	)
	flag.BoolVar(&compressFlag, "gzip", false, "Compress content with gzip before uploading")
	flag.StringVar(
		&formatFlag,
		"format",
		"auto",
		"Link format: auto (infer from filename), md, markdown, html, htm, link, png, claude.jsonl, terminal; use -format= for plain view",
	)
	flag.BoolVar(&nowrapFlag, "nowrap", false, "Disable browser text wrapping")
	flag.BoolVar(&sandboxFlag, "sandbox", false, "Render HTML in the browser sandbox (default for HTML)")
	flag.Bool("unsafe-html", false, "Disable the default HTML sandbox")
	flag.StringVar(&baseURLFlag, "url", "", "Pastila base URL override")
	flag.StringVar(&backendFlag, "clickhouse-url", "", "ClickHouse API URL override")
	flag.StringVar(&cookieFlag, "cookie", "", "Authentication cookie override")
	flag.Usage = printUsage
	flag.Parse()

	if versionFlag := flag.Lookup("version"); versionFlag != nil && versionFlag.Value.String() == trueFlagValue {
		fmt.Printf("Pastila CLI v%s (%s) - %s\n", version, commit, date)
		return
	}
}

func readPaste(service pastila.Service, urlToRead string) error {
	pasteRes, readErr := service.Read(urlToRead)
	if readErr != nil {
		return readErr
	}
	defer func() { _ = pasteRes.Close() }()
	if showSummary {
		printf("Query ID: %s\n", pasteRes.QueryID)
	}

	if launchEditorFlag {
		if editErr := editPaste(service, pasteRes); editErr != nil {
			return fmt.Errorf("failed to edit paste: %w", editErr)
		}
		return nil
	}

	if _, err := io.Copy(os.Stdout, pasteRes); err != nil {
		return fmt.Errorf("failed to write paste to stdout: %w", err)
	}

	return nil
}

//nolint:funlen,gocyclo // Editing coordinates the editor process, file watcher, and optional upload overrides.
func editPaste(service pastila.Service, paste *pastila.Paste) error {
	format, sandbox, err := resolveLinkOptions("", paste.Format, paste.Sandbox, flag.CommandLine)
	if err != nil {
		return err
	}
	editorFile, fileErr := pasteToTemp(paste)
	if fileErr != nil {
		return fileErr
	}

	defer func() {
		if closeErr := editorFile.Close(); closeErr != nil {
			printf("Failed to close temporary file: %v\n", closeErr)
		}

		if removeErr := os.Remove(editorFile.Name()); removeErr != nil {
			printf("Failed to remove temporary file: %v\n", removeErr)
		}
	}()

	editor := strings.Fields(getEditor())
	if len(editor) == 0 {
		return fmt.Errorf("EDITOR is empty")
	}
	// #nosec G204 -- This is intended behavior to launch the user's configured editor.
	cmd := exec.Command(editor[0], append(editor[1:], editorFile.Name())...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	var uploadErr error
	var urls []string
	fileWatchCtx, cancelFileWatch := context.WithCancel(context.Background())
	saveEdit := func(_ os.FileInfo) {
		// Editors commonly save by replacing the file, so reopen its path.
		content, err := os.Open(editorFile.Name())
		if err != nil {
			uploadErr = err
			return
		}
		defer func() { _ = content.Close() }()
		opts := []pastila.WriteOption{pastila.WithPreviousPaste(paste), pastila.WithFormat(format), pastila.WithSandbox(sandbox)}
		flag.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "plain":
				if plain {
					opts = append(opts, pastila.WithKey(nil))
				}
			case "gzip":
				opts = append(opts, pastila.WithCompression(compressFlag))
			case "nowrap":
				opts = append(opts, pastila.WithNoWrap(nowrapFlag))
			}
		})
		if key != "" {
			seed := []byte(key)
			if _, statErr := os.Stat(key); statErr == nil {
				seed, err = os.ReadFile(key)
				if err != nil {
					uploadErr = err
					return
				}
			}
			opts = append(opts, pastila.WithKey(seed))
		}
		updated, err := service.Write(content, opts...)
		if err != nil {
			uploadErr = err
			return
		}
		paste = updated
		uploadErr = nil
		urls = append(urls, paste.URL)
	}
	fileWatchDone := watchFile(fileWatchCtx, editorFile, saveEdit)
	if startErr := cmd.Start(); startErr != nil {
		cancelFileWatch()
		<-fileWatchDone
		return fmt.Errorf("failed to start editor: %w", startErr)
	}
	waitErr := cmd.Wait()
	cancelFileWatch()
	<-fileWatchDone
	if waitErr == nil && (len(paste.Hash) == 0 || uploadErr != nil) {
		saveEdit(nil)
	}
	for _, url := range urls {
		_, _ = fmt.Fprintln(os.Stdout, url)
	}
	if waitErr != nil {
		return fmt.Errorf("editor failed: %w", waitErr)
	}
	if uploadErr != nil {
		return fmt.Errorf("failed to upload edit: %w", uploadErr)
	}
	return nil
}

func pasteToTemp(paste *pastila.Paste) (*os.File, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("pastila-%x", paste.Hash))
	if err != nil {
		return f, fmt.Errorf("failed to create temporary file: %w", err)
	}

	if _, err := io.Copy(f, paste); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return f, fmt.Errorf("failed to write paste to temporary file: %w", err)
	}

	return f, nil
}

func watchFile(ctx context.Context, f *os.File, changeHandler func(os.FileInfo)) chan struct{} {
	done := make(chan struct{})
	previous, initialErr := os.ReadFile(f.Name())
	go func() {
		defer close(done)
		if initialErr != nil {
			return
		}

		execChangeHandlerIfFileChanged := func() {
			actualStat, err := os.Stat(f.Name())
			if err != nil {
				return
			}

			content, err := os.ReadFile(f.Name())
			if err != nil || bytes.Equal(previous, content) {
				return
			}
			previous = content
			changeHandler(actualStat)
		}

		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				execChangeHandlerIfFileChanged()
				return
			case <-ticker.C:
				execChangeHandlerIfFileChanged()
			}
		}
	}()
	return done
}

const (
	defaultEditor = "vi"
	editorEnv     = "EDITOR"
)

func getEditor() string {
	if v, ok := os.LookupEnv(editorEnv); ok {
		return v
	}
	return defaultEditor
}

func generateRandomKey() ([]byte, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}
