package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

func resolveLinkOptions(filename, currentFormat string, currentSandbox bool, fs *flag.FlagSet) (string, bool, error) {
	explicit := make(map[string]string)
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = f.Value.String() })
	format := currentFormat
	value, formatExplicit := explicit["format"]
	if formatExplicit && value != "auto" {
		format = strings.TrimPrefix(value, ".")
	} else if filename != "" && filename != "-" {
		name := strings.ToLower(filepath.Base(filename))
		if strings.HasSuffix(name, ".claude.jsonl") {
			format = "claude.jsonl"
		} else {
			switch ext := filepath.Ext(name); ext {
			case ".md", ".markdown", ".html", ".htm", ".terminal":
				format = strings.TrimPrefix(ext, ".")
			}
		}
	}
	isHTML := format == "html" || format == "htm"
	sandbox := currentSandbox
	if (formatExplicit && value != "auto") || filename != "" {
		sandbox = false
	}
	if isHTML {
		sandbox = true
	}
	if value, ok := explicit["sandbox"]; ok {
		sandbox = value == "true"
	}
	if explicit["unsafe-html"] == "true" {
		if !isHTML {
			return "", false, fmt.Errorf("-unsafe-html requires HTML format")
		}
		if explicit["sandbox"] == "true" {
			return "", false, fmt.Errorf("-unsafe-html and -sandbox cannot be combined")
		}
		sandbox = false
	}
	if sandbox && !isHTML {
		return "", false, fmt.Errorf("-sandbox requires HTML format")
	}
	return format, sandbox, nil
}
