package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLinkOptions(t *testing.T) {
	for _, tc := range []struct {
		name, filename, previous, want string
		args                           []string
		sandbox, wantError             bool
	}{
		{name: "markdown", filename: "README.md", want: "md"},
		{name: "explicit auto", filename: "README.md", args: []string{"-format", "auto"}, want: "md"},
		{name: "markdown long extension", filename: "README.markdown", want: "markdown"},
		{name: "HTML", filename: "report.html", want: "html", sandbox: true},
		{name: "uppercase HTML", filename: "report.HTM", want: "htm", sandbox: true},
		{name: "terminal", filename: "build.terminal", want: "terminal"},
		{name: "session", filename: "session.claude.jsonl", want: "claude.jsonl"},
		{name: "generic JSONL", filename: "data.jsonl"},
		{name: "PNG is not QR", filename: "image.png"},
		{name: "no redirect inference", filename: "url.link"},
		{name: "no gzip inference", filename: "README.md.gz"},
		{name: "stdin", filename: "-"},
		{name: "override", filename: "README.md", args: []string{"-format", "html"}, want: "html", sandbox: true},
		{name: "disable inference", filename: "report.html", args: []string{"-format="}},
		{name: "explicit QR", filename: "url.txt", args: []string{"-format", "png"}, want: "png"},
		{name: "unsafe HTML", filename: "report.html", args: []string{"-unsafe-html"}, want: "html"},
		{name: "sandbox false", filename: "report.html", args: []string{"-sandbox=false"}, want: "html"},
		{name: "conflicting flags", filename: "report.html", args: []string{"-unsafe-html", "-sandbox"}, wantError: true},
		{name: "unsafe non HTML", filename: "README.md", args: []string{"-unsafe-html"}, wantError: true},
		{name: "sandbox non HTML", filename: "README.md", args: []string{"-sandbox"}, wantError: true},
		{name: "edit auto retains format", previous: "terminal", args: []string{"-format", "auto"}, want: "terminal"},
		{name: "edit HTML", previous: "html", want: "html", sandbox: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.String("format", "auto", "")
			fs.Bool("sandbox", false, "")
			fs.Bool("unsafe-html", false, "")
			require.NoError(t, fs.Parse(tc.args))
			format, sandbox, err := resolveLinkOptions(tc.filename, tc.previous, false, fs)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, format)
			assert.Equal(t, tc.sandbox, sandbox)
		})
	}
}
