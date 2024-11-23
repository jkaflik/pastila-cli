package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"github.com/jkaflik/pastila-cli/pkg/pastila"
	"io"
	"os"
	"os/exec"
	"time"
)

var (
	fileName         string
	showSummary      bool
	teeFlag          bool
	launchEditorFlag bool
	plain            bool
	key              string
)

var printWriter io.Writer = os.Stderr

func printf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(printWriter, format, args...)
}

var printUsage = func() {
	printf("Pastila CLI is a command line utility to read and write from pastila.nl copy-paste service.\n")
	printf("See a GitHub repository for more information: https://github.com/ClickHouse/pastila\n\n")
	printf("Usage: %s [options] [URL]\n\n", os.Args[0])
	printf("\t[URL] can be a pastila URL or \"-\" to read from URL stdin.\n\nAvailable options:\n\n")
	flag.PrintDefaults()
	printf("\nRead data goes into output, anything else goes into printf\n")
	printf("When writing to pastila, URL will be printed to output\n")
}

func init() {
	flag.StringVar(&fileName, "f", "", "Content file path")

	flag.BoolVar(&plain, "plain", false, "Do not encrypt content. Default is to encrypt content.")
	flag.StringVar(&key, "key", "", "Key to encrypt content. Provide a file path to read key from a file.  If not provided, a random 64bit key will be generated.")

	flag.BoolVar(&showSummary, "s", false, "Show query summary after reading from pastila")

	flag.BoolVar(&launchEditorFlag, "e", false, "Launch editor to write content. If URL is provided, editor will be launched after reading from pastila. Use EDITOR environment variable to set editor. Otherwise, vi will be used.")

	flag.BoolVar(&teeFlag, "teeFlag", false, "Write to output and to pastila. URL will be printed to printf")
}

func stdinIfAvailable() *os.File {
	if os.Stdin == nil {
		return nil
	}

	fi, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}

	if fi.Mode()&os.ModeNamedPipe == 0 {
		return nil
	}

	return os.Stdin
}

func main() {
	flag.Parse()

	var err error

	urlToRead := flag.Arg(0)
	contentToWrite := stdinIfAvailable()

	if fileName != "" {
		contentToWrite, err = os.Open(fileName)
		if err != nil {
			printf("Failed to open file %s: %v\n", fileName, err)
			os.Exit(1)
		}
		defer contentToWrite.Close()
	}

	service := pastila.Service{
		PastilaURL:    os.Getenv("PASTILA_URL"),
		ClickHouseURL: os.Getenv("PASTILA_CLICKHOUSE_URL"),
	}

	// If no URL is provided,
	// we write to pastila
	if urlToRead == "" && contentToWrite == nil {
		printUsage()
		os.Exit(1)
	}

	if urlToRead == "-" {
		stdin := stdinIfAvailable()

		if stdin == nil {
			printf("No URL provided in stdin, but \"-\" was passed as URL")
			os.Exit(1)
		}

		buf := make([]byte, 1024)
		_, err := stdin.Read(buf)
		if err != nil {
			printf("Failed to read pastila URL from stdin: %v\n", err)
			os.Exit(1)
		}

		urlToRead = string(buf)
	}

	if urlToRead != "" {
		paste, err := service.Read(urlToRead)
		if err != nil {
			printf("%v\n", err)
			os.Exit(1)
		}
		defer paste.Close()

		if launchEditorFlag {
			editPaste(service, paste)
			return
		}

		if _, err := io.Copy(os.Stdout, paste); err != nil {
			printf("Failed to write to output: %v\n", err)
			os.Exit(1)
		}

		return
	}

	var reader io.Reader = contentToWrite
	if teeFlag {
		reader = io.TeeReader(reader, os.Stdout)
	}

	var k []byte
	if !plain {
		if key == "" {
			k, err = generateRandomKey()
			if err != nil {
				printf("Failed to generate random key\n")
				os.Exit(1)
			}
		} else {
			if _, err := os.Stat(key); err == nil {
				k, err = os.ReadFile(key)
				if err != nil {
					printf("Failed to read key from file %s: %v\n", key, err)
					os.Exit(1)
				}
			} else {
				k = []byte(key)
			}
		}
	}

	result, err := service.Write(reader, pastila.WithKey(k))
	if err != nil {
		printf("%v\n", err)
		os.Exit(1)
	}

	printf("%s\n", result.URL)

	// todo: editable paste. This requires paste ReadCloser to be Seekable as well
}

func editPaste(service pastila.Service, paste *pastila.Paste) *pastila.Paste {
	editorFile, err := pasteToTemp(paste)
	if err != nil {
		printf("%v\n", err)
		os.Exit(1)
	}

	defer func() {
		if err := editorFile.Close(); err != nil {
			printf("Failed to close temporary file: %v\n", err)
		}

		if err := os.Remove(editorFile.Name()); err != nil {
			printf("Failed to remove temporary file: %v\n", err)
		}
	}()

	processStartAt := time.Now()

	cmd := exec.Command(getEditor(), editorFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		printf("Failed to start editor: %v\n", err)
		os.Exit(1)
	}

	printBuffer := &bytes.Buffer{}
	printWriter = printBuffer

	ctx, cancel := context.WithCancel(context.Background())
	fileWatchDone := watchFile(ctx, editorFile, func(_ os.FileInfo) {
		if _, err := editorFile.Seek(0, io.SeekStart); err != nil {
			printf("Failed to seek to the beginning of the file: %v\n", err)
			return
		}

		paste, err = service.Write(editorFile, pastila.WithPreviousPaste(paste))
		if err != nil {
			printf("%v\n", err)
			return
		}

		printf("%s\n", paste.URL)
	})

	go func() {
		defer func() {
			printWriter = os.Stderr
			_, _ = io.Copy(os.Stderr, printBuffer)
			printBuffer = nil
		}()

		if err := cmd.Wait(); err != nil {
			printf("Failed to wait for editor: %v\n", err)
			os.Exit(1)
		}
	}()

	for {
		if cmd.ProcessState != nil {
			// There are editors like "code" (VSCode launcher) that immediately exit
			// leaving forked process running in background.
			if cmd.ProcessState.ExitCode() == 0 && time.Now().Sub(processStartAt) < 1*time.Second {
				printf("Your editor exited too quickly. Does it run in background? Press any key to continue\n")
				_, _ = os.Stdin.Read(make([]byte, 1))
			}

			break
		}
	}

	cancel()
	<-fileWatchDone
	return paste
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

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			actualStat, err := f.Stat()
			if err != nil {
				return
			}

			if actualStat.Size() == 0 || actualStat.Size() == stat.Size() || actualStat.ModTime() == stat.ModTime() {
				continue
			}

			stat = actualStat
			changeHandler(stat)
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
