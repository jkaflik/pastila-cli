# Pastila CLI

A command line client for the [pastila.nl](https://pastila.nl) pastebin service.
Pastila CLI lets you easily read from and write to the pastila service from your terminal.

## Features

- Read pastes from pastila.nl
- Write content to pastila.nl
- Encrypt new pastes with AES-GCM; read both current GCM and legacy AES-CTR links
- Upload and read gzip-compressed content
- Generate Markdown, HTML, terminal-output and Claude-session links
- Support for editor integration
- Pipe content to/from stdin/stdout
- Custom pastila service deployment support

## Installation

### macOS with Homebrew

The easiest way to install on macOS is through Homebrew:

```bash
brew tap jkaflik/tap
brew install jkaflik/tap/pastila
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
        16/24/32-byte key material or file path. Derives a fresh per-paste key; default is a random 128-bit key.
  -plain
    	Do not encrypt content. Default is to encrypt content.
  -s	Show query summary after reading from pastila
  -gzip
        Compress content before uploading; downloads decompress automatically.
  -format string
        Link format: auto (default), md, markdown, html, htm, link, png, claude.jsonl, terminal.
  -nowrap
        Disable browser text wrapping.
  -sandbox
        Render HTML in a browser sandbox (default for HTML).
  -unsafe-html
        Disable the default HTML sandbox.
  -url string
        Pastila base URL override.
  -clickhouse-url string
        ClickHouse backend URL override.
  -cookie string
        Authentication cookie override.
  -teeFlag
    	Write to output and to pastila. URL will be printed to stderr.

Available commands:

  setup    Configure a custom Pastila deployment.
  auth     Login, inspect or clear stored authentication.
  doctor   Check configuration and endpoint connectivity.

Read data goes into output, anything else goes into stderr.
When writing to pastila, URL will be printed to stdout.
```

### Examples

**Configuring a custom deployment:**
```bash
pastila setup
```

To skip the URL prompt:
```bash
pastila setup --url https://pastila.example.com/
```

If the deployment requires an auth cookie, setup opens the destination in your browser
and prompts for the same cookie value accepted by `PASTILA_COOKIE`. The cookie is stored
in the system keychain; the Pastila and ClickHouse URLs are stored in the user
configuration directory.

Configuration precedence is **flags → environment → saved configuration → defaults**.
Authentication precedence is **`-cookie` → `PASTILA_COOKIE` → system keychain**.
A custom Pastila URL requires a configured ClickHouse backend, to avoid sending
custom-deployment content to the public backend accidentally.

```sh
pastila auth login --url https://pastila.example.com/
pastila auth status
pastila auth clear
pastila doctor
```

`auth status` reports whether a cookie is stored, without revealing it or validating
it against the server. `doctor` checks connectivity using a read-only query.
Setup sends cookies only to same-origin discovered endpoints and scripts. For an
authenticated backend on another origin, configure it explicitly with
`setup --clickhouse-url URL`. HTTP redirects are not followed for API requests.

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
EDITOR='code --wait' pastila -e 'https://pastila.nl/?b2d0e349/41c7ddfc538be8bca56bff2d523ad176#PCzfMCI06OLQD+OA3D94qA=='
```

**Creating an unencrypted paste:**
```bash
echo "Hello, world!" | pastila -plain
```

**Sharing compressed terminal output or a Claude session:**
```sh
pastila -gzip -format terminal -nowrap -f build.log
pastila -gzip -format claude.jsonl -f session.jsonl
```

**Sharing sandboxed HTML:**
```sh
pastila -format html -sandbox -f report.html
```

`-format` defaults to **`auto`**: filenames ending in `.md`, `.markdown`, `.html`,
`.htm`, `.terminal`, or `.claude.jsonl` select the matching rendering format.
For example, `pastila -f README.md` generates a `.md` link, and
`pastila -f report.html` generates a `.html` link with `&sandbox`.

Use `-format terminal` (or another supported format) to override inference,
`-format auto` to explicitly request it, or `-format=` to disable it.
Stdin and unknown extensions default to plain text view; encryption remains
enabled. `.png` and `.link` require explicit format selection because they mean
QR-code generation and browser redirection. `.gz` filenames do not automatically
enable compression or decompression.

HTML defaults to sandboxed rendering, including when selected with `-format html`.
Use `-unsafe-html` or `-sandbox=false` to omit the sandbox option.
`-unsafe-html` and `-sandbox` cannot be combined. When editing an existing paste,
`auto` keeps its current format; HTML revisions default to sandboxing as well.

**Creating a new paste in an editor:**
```sh
pastila -e
```

Downloads return the underlying content, even for rendering links. Quote paste
URLs in your shell, especially links with `&nowrap` or `&sandbox`. Compression
is indicated by `.gz` after the rendering extension and before `#`.

### Encryption and limits

New encrypted uploads use Pastila's AES-GCM format (`#BASE64KEYGCM`), with a
fresh key for every upload and revision. Old CTR links remain readable.
GCM rejects wrong keys and corrupted ciphertext; legacy CTR cannot authenticate
content.

`-key` accepts 16, 24 or 32 bytes of key material, directly or from a file.
It now derives a fresh 128-bit key using HKDF-SHA256 and a random salt for each
upload. **The URL contains the derived key, not the supplied material.** This
intentionally replaces the old reusable-key behavior because Pastila derives
the GCM nonce from the key. Keep the returned URL to read the paste.

The Go service API defaults to plaintext unless `WithEncryption()` or
`WithKey(...)` is supplied; the CLI defaults to encryption. `WithKey(nil)` selects
plaintext. `WithPreviousPaste(...)` preserves format/compression/options and
encryption status while rotating the encryption key.

Stored content must be smaller than 50 MiB, including Base64 and encryption
overhead. Input and decompressed output are bounded at 256 MiB minus 16 bytes,
matching the browser's decompression limit. Plain, uncompressed content must be
UTF-8; use encryption or gzip for binary data. Deployments may impose additional
limits. API requests time out after 30 seconds.

Editor commands may include arguments (for example `code --wait`); use a wrapper
script if the executable path or arguments contain spaces requiring shell quoting.
The editor must stay open until editing is complete. Saves, including same-size
edits, empty files and atomic replacements, create revisions; their URLs are
printed after the editor exits.

## Environment Variables

- `PASTILA_URL`: Custom pastila service URL (default: https://pastila.nl/)
- `PASTILA_CLICKHOUSE_URL`: Custom ClickHouse backend URL (default: https://uzg8q0g12h.eu-central-1.aws.clickhouse.cloud/?user=paste)
- `PASTILA_COOKIE`: Auth cookie override. This takes precedence over the system keychain.
- `EDITOR`: Editor to use with `-e` flag (default: vi)

## License

This project is open source. See the repository for license details.
