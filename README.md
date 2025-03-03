# Pastila CLI

A command line client for the [pastila.nl](https://pastila.nl) pastebin service.
Pastila CLI lets you easily read from and write to the pastila service from your terminal.

## Features

- Read pastes from pastila.nl
- Write content to pastila.nl
- Encrypt content with AES
- Support for editor integration
- Pipe content to/from stdin/stdout
- Custom pastila service deployment support

## Installation

### macOS with Homebrew

The easiest way to install on macOS is through Homebrew:

```bash
brew tap jkaflik/tap
brew install pastila-cli
```

### Linux and macOS (using pre-built binaries)

Download the latest pre-built binary from the [releases page](https://github.com/jkaflik/pastila-cli/releases).

### Build from source

If you have Go installed, you can also build from source:

```bash
go install github.com/jkaflik/pastila-cli/cmd/pastila@latest
```

## Usage

```
Pastila CLI is a command line utility to read and write from pastila.nl copy-paste service.
See a GitHub repository for more information: https://github.com/ClickHouse/pastila

Usage: pastila [options] [URL]

	[URL] can be a pastila URL or "-" to read from URL stdin.

Available options:

  -e	Launch editor to edit content. If URL is provided, editor will be launched with a content read from pastila. Use EDITOR environment variable to set editor. Otherwise, vi will be used.
  -f string
    	Content file path. Use "-" to read from stdin. If not provided, content will be read from stdin.
  -key string
    	Key to encrypt content. Provide a file path to read key from a file.  If not provided, a random 64bit key will be generated.
  -plain
    	Do not encrypt content. Default is to encrypt content.
  -s	Show query summary after reading from pastila
  -teeFlag
    	Write to output and to pastila. URL will be printed to stderr.

Read data goes into output, anything else goes into stderr.
When writing to pastila, URL will be printed to stdout.
```

### Examples

**Reading an encrypted paste:**
```bash
pastila https://pastila.nl/?b2d0e349/41c7ddfc538be8bca56bff2d523ad176#PCzfMCI06OLQD+OA3D94qA==
```

**Reading a paste into macOS clipboard:**
```bash
pastila https://pastila.nl/?b2d0e349/41c7ddfc538be8bca56bff2d523ad176#PCzfMCI06OLQD+OA3D94qA== | pbcopy
```

**Reading an unencrypted paste:**
```bash
pastila https://pastila.nl/?ffffffff/14aa3e22cd6438df3a5808560fe40150
```

**Creating a paste from a file:**
```bash
pastila -f path/to/file.txt
```

**Creating a paste from stdin:**
```bash
echo "Hello, world!" | pastila
```

**Creating a paste from macOS clipboard:**
```bash
pbpaste | pastila
```

**Editing an existing paste with the editor:**
```bash
pastila -e https://pastila.nl/?b2d0e349/41c7ddfc538be8bca56bff2d523ad176#PCzfMCI06OLQD+OA3D94qA==
```

**Editing an existing paste with the VS Code:**
```bash
EDITOR=code pastila -e https://pastila.nl/?b2d0e349/41c7ddfc538be8bca56bff2d523ad176#PCzfMCI06OLQD+OA3D94qA==
```

**Creating an unencrypted paste:**
```bash
echo "Hello, world!" | pastila -plain
```

## Environment Variables

- `PASTILA_URL`: Custom pastila service URL (default: https://pastila.nl/)
- `PASTILA_CLICKHOUSE_URL`: Custom ClickHouse backend URL (default: https://play.clickhouse.com/?user=paste)
- `EDITOR`: Editor to use with `-e` flag (default: vi)

## License

This project is open source. See the repository for license details.