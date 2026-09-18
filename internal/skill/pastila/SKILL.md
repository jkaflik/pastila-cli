---
name: pastila
description: Share text, files, logs, Markdown, HTML reports, terminal output, and Claude sessions through Pastila. Use when the user asks to upload, paste, publish, or create a shareable link for an artifact, especially when they mention Pastila.
license: MIT
compatibility: Requires the pastila executable and network access to the configured Pastila service.
---

# Share artifacts with Pastila

Use the `pastila` CLI to upload an artifact and return its shareable URL.

## Safety

Treat an upload as disclosure to an external service.

- Do not open or inspect an artifact solely to check it for sensitive content when its contents are not already known. This would unnecessarily expose potentially sensitive data to the agent.
- If known context indicates that the content may contain credentials, private keys, authentication cookies, `.env` values, or other secrets, or if the user's intent to upload is unclear, pause and ask whether to redact it or proceed.
- Keep Pastila's default encryption enabled. Do not use `-plain` unless the user explicitly asks for an unencrypted paste.
- Never put a custom deployment's authentication cookie in the artifact or in the response.
- Anyone with the complete encrypted URL can read the paste because the URL fragment contains the decryption key.

## Upload workflow

1. Confirm which artifact should be shared and choose the closest rendering format.
2. Run one of the commands below. Prefer `-f` for an existing file and stdin for generated text.
3. Capture the URL printed to stdout exactly. Do not remove its fragment or query options.
4. Return the URL and briefly identify what was shared. Mention that the complete URL grants access when that context is useful.

Pastila encrypts uploads by default. File extensions select Markdown, HTML, terminal, and Claude-session rendering automatically where supported.

```sh
# Existing file; infer the format from its name.
pastila -f report.md

# Large terminal output.
pastila -gzip -format terminal -nowrap -f build.log

# HTML is sandboxed by default.
pastila -format html -f report.html

# Claude session export.
pastila -gzip -format claude.jsonl -f session.jsonl

# Generated Markdown from stdin.
generate-report | pastila -format md
```

Use `-gzip` for large text artifacts such as logs and session exports. Use `-format terminal` for ANSI terminal output, `-format md` for Markdown from stdin, and `-format html` for HTML. Do not use `-unsafe-html` unless the user explicitly requires unsandboxed HTML and understands the risk.

## Read an existing paste

When the user asks to retrieve a Pastila URL, quote the complete URL so shell characters such as `&` and `#` are preserved:

```sh
pastila 'https://pastila.nl/?fingerprint/hash#key'
```

If an upload or read fails because of configuration or connectivity, run `pastila doctor`. Do not expose authentication values while reporting diagnostics.
