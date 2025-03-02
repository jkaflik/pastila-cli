package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/jkaflik/pastila-cli/pkg/pastila"
)

// These variables are set during build by goreleaser
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	fileName         string
	showSummary      bool
	teeFlag          bool
	launchEditorFlag bool
	plain            bool
	key              string
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
	printf("\nRead data goes into output, anything else goes into stderr.\n")
	printf("When writing to pastila, URL will be printed to stdout.\n")
}

func stdinWithTimeout(timeout time.Duration) (io.Reader, error) {
	if os.Stdin == nil {
		return nil, nil
	}

	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) != 0 {
		return nil, nil
	}

	dataCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 1024)
		n, err := os.Stdin.Read(buf)
		if err != nil && err != io.EOF {
			errCh <- err
			return
		}

		dataCh <- buf[:n]
	}()

	select {
	case data := <-dataCh:
		if len(data) == 0 {
			return nil, nil
		}

		return io.MultiReader(bytes.NewReader(data), os.Stdin), nil

	case err := <-errCh:
		return nil, err

	case <-time.After(timeout):
		return nil, nil
	}
}

func main() {
	setupFlags()

	stdin, err := stdinWithTimeout(time.Millisecond)
	if err != nil {
		printf("Failed to read from stdin: %v\n", err)
		os.Exit(1)
	}

	pasteURL := flag.Arg(0)

	if pasteURL == "-" {
		pasteURL, err = readURL(stdin)
		if err != nil {
			printf("%v\n", err)
			os.Exit(1)
		}
	}

	service := pastila.Service{
		PastilaURL:    os.Getenv("PASTILA_URL"),
		ClickHouseURL: os.Getenv("PASTILA_CLICKHOUSE_URL"),
	}

	if pasteURL != "" {
		if readErr := readPaste(service, pasteURL); readErr != nil {
			printf("%v\n", readErr)
			os.Exit(1)
		}

		return
	}

	var reader io.Reader
	if fileName != "" && fileName != "-" {
		reader, err = os.Open(fileName)
		if err != nil {
			printf("failed to open file %s: %v\n", fileName, err)
			os.Exit(1)
		}
	} else {
		reader = stdin
	}

	if reader == nil {
		printUsage()
		os.Exit(1)
	}

	if writeErr := writePaste(service, reader); writeErr != nil {
		printf("%v\n", writeErr)
		os.Exit(1)
	}
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

	result, err := service.Write(reader, pastila.WithKey(k))
	if err != nil {
		return fmt.Errorf("failed to write paste: %w", err)
	}

	printf("%s\n", result.URL)
	return nil
}

func readURL(r io.Reader) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no URL provided in stdin, but \"-\" was passed as URL")
	}

	buf := make([]byte, 1024)
	_, readErr := r.Read(buf)
	if readErr != nil {
		return "", fmt.Errorf("failed to read pastila URL from stdin: %w", readErr)
	}
	return string(buf), nil
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
		"Key to encrypt content. Provide a file path to read key from a file.  If not provided, a random 64bit key will be generated.",
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
	flag.Parse()

	if versionFlag := flag.Lookup("version"); versionFlag != nil && versionFlag.Value.String() == "true" {
		fmt.Printf("Pastila CLI v%s (%s) - %s\n", version, commit, date)
		return
	}
}

func readPaste(service pastila.Service, urlToRead string) error {
	pasteRes, readErr := service.Read(urlToRead)
	if readErr != nil {
		return readErr
	}
	defer pasteRes.Close()

	if launchEditorFlag {
		if _, editErr := editPaste(service, pasteRes); editErr != nil {
			return fmt.Errorf("failed to edit paste: %w", editErr)
		}
		return nil
	}

	if _, err := io.Copy(os.Stdout, pasteRes); err != nil {
		return fmt.Errorf("failed to write paste to stdout: %w", err)
	}

	return nil
}

func editPaste(service pastila.Service, paste *pastila.Paste) (*pastila.Paste, error) {
	editorFile, fileErr := pasteToTemp(paste)
	if fileErr != nil {
		printf("%v\n", fileErr)
		os.Exit(1)
	}

	defer func() {
		if closeErr := editorFile.Close(); closeErr != nil {
			printf("Failed to close temporary file: %v\n", closeErr)
		}

		if removeErr := os.Remove(editorFile.Name()); removeErr != nil {
			printf("Failed to remove temporary file: %v\n", removeErr)
		}
	}()

	processStartAt := time.Now()

	// #nosec G204 -- This is intended behavior to launch the user's editor
	cmd := exec.Command(getEditor(), editorFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if startErr := cmd.Start(); startErr != nil {
		return nil, fmt.Errorf("failed to start editor: %w", startErr)
	}

	currentPrintWriter := printWriter
	printBuffer := &bytes.Buffer{}
	printWriter = printBuffer
	dismissPrintBuffer := func() {
		if printBuffer == nil {
			return
		}

		printWriter = currentPrintWriter
		_, _ = io.Copy(printWriter, printBuffer)
		printBuffer = nil
	}

	fileWatchCtx, cancelFileWatch := context.WithCancel(context.Background())
	fileWatchDone := watchFile(fileWatchCtx, editorFile, func(_ os.FileInfo) {
		if _, seekErr := editorFile.Seek(0, io.SeekStart); seekErr != nil {
			printf("Failed to seek to the beginning of the file: %v\n", seekErr)
			return
		}

		paste, fileErr = service.Write(editorFile, pastila.WithPreviousPaste(paste))
		if fileErr != nil {
			printf("%v\n", fileErr)
			return
		}

		printf("%s\n", paste.URL)
	})

	go func() {
		defer dismissPrintBuffer()

		if waitErr := cmd.Wait(); waitErr != nil {
			printf("Failed to wait for editor: %v\n", waitErr)
		}
	}()

	for {
		if cmd.ProcessState != nil {
			// There are editors like "code" (VSCode launcher) that immediately exit
			// leaving forked process running in background.
			if cmd.ProcessState.ExitCode() == 0 && time.Since(processStartAt) < 1*time.Second {
				dismissPrintBuffer()

				printf("Your editor exited too quickly. Does it run in background? Press any key to continue\n")
				_, _ = os.Stdin.Read(make([]byte, 1))
			}

			break
		}
	}

	cancelFileWatch()
	<-fileWatchDone
	return paste, nil
}

func pasteToTemp(paste *pastila.Paste) (*os.File, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("pastila-%x", paste.Hash))
	if err != nil {
		return f, fmt.Errorf("failed to create temporary file: %w", err)
	}

	if _, err := io.Copy(f, paste); err != nil {
		return f, fmt.Errorf("failed to write paste to temporary file: %w", err)
	}

	return f, nil
}

func watchFile(ctx context.Context, f *os.File, changeHandler func(os.FileInfo)) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		stat, err := f.Stat()
		if err != nil {
			return
		}

		execChangeHandlerIfFileChanged := func() {
			actualStat, err := f.Stat()
			if err != nil {
				return
			}

			if actualStat.Size() == 0 || actualStat.Size() == stat.Size() || actualStat.ModTime() == stat.ModTime() {
				return
			}

			stat = actualStat
			changeHandler(stat)
		}

		for {
			select {
			case <-ctx.Done():
				execChangeHandlerIfFileChanged()
				return
			default:
			}

			execChangeHandlerIfFileChanged()
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
