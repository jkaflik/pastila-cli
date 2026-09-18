package skill

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const Name = "pastila"

type Target string

const (
	TargetAll      Target = "all"
	TargetPortable Target = "portable"
	TargetClaude   Target = "claude"
)

type Status string

const (
	StatusInstalled Status = "installed"
	StatusUpdated   Status = "updated"
	StatusUnchanged Status = "unchanged"
)

type Result struct {
	Path   string
	Status Status
}

// Content contains the Agent Skills-compatible Pastila skill bundled with the CLI.
//
//go:embed pastila/SKILL.md
var Content []byte

func (target Target) Valid() bool {
	switch target {
	case TargetAll, TargetPortable, TargetClaude:
		return true
	default:
		return false
	}
}

func Install(home string, target Target) ([]Result, error) {
	if home == "" {
		return nil, fmt.Errorf("home directory is empty")
	}
	if !target.Valid() {
		return nil, fmt.Errorf("unsupported skill target %q", target)
	}

	paths := installPaths(home, target)
	results := make([]Result, 0, len(paths))
	installErrors := make([]error, 0)
	for _, path := range paths {
		status, err := installFile(path)
		if err != nil {
			installErrors = append(installErrors, fmt.Errorf("%s: %w", path, err))
			continue
		}
		results = append(results, Result{Path: path, Status: status})
	}

	return results, errors.Join(installErrors...)
}

func installPaths(home string, target Target) []string {
	portablePath := filepath.Join(home, ".agents", "skills", Name, "SKILL.md")
	claudePath := filepath.Join(home, ".claude", "skills", Name, "SKILL.md")

	switch target {
	case TargetPortable:
		return []string{portablePath}
	case TargetClaude:
		return []string{claudePath}
	default:
		return []string{portablePath, claudePath}
	}
}

func installFile(path string) (Status, error) {
	status := StatusInstalled
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, Content) {
			return StatusUnchanged, nil
		}
		status = StatusUpdated
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to read existing skill: %w", err)
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("failed to create skill directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".SKILL.md-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary skill: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("failed to set skill permissions: %w", err)
	}
	if _, err := temporary.Write(Content); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("failed to write skill: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("failed to close skill: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("failed to replace skill: %w", err)
	}

	return status, nil
}
