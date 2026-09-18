package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jkaflik/pastila-cli/internal/skill"
)

func TestRunSkillInstallDefaultsToAllTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	output := captureSkillOutput(t)

	exitCode := runSkill([]string{"install"})

	assert.Equal(t, 0, exitCode)
	assert.Contains(t, output.String(), filepath.Join(home, ".agents", "skills", skill.Name, "SKILL.md"))
	assert.Contains(t, output.String(), filepath.Join(home, ".claude", "skills", skill.Name, "SKILL.md"))
}

func TestRunSkillInstallSelectsTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	captureSkillOutput(t)

	exitCode := runSkill([]string{"install", "-target", "claude"})

	assert.Equal(t, 0, exitCode)
	_, err := os.Stat(filepath.Join(home, ".claude", "skills", skill.Name, "SKILL.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(home, ".agents", "skills", skill.Name, "SKILL.md"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunSkillRejectsInvalidInput(t *testing.T) {
	testCases := map[string][]string{
		"missing action": nil,
		"unknown action": {"remove"},
		"invalid target": {"install", "-target", "other"},
		"extra argument": {"install", "extra"},
	}

	for name, args := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			captureSkillOutput(t)

			assert.Equal(t, 2, runSkill(args))
		})
	}
}

func captureSkillOutput(t *testing.T) *bytes.Buffer {
	t.Helper()

	var output bytes.Buffer
	originalWriter := printWriter
	printWriter = &output
	t.Cleanup(func() { printWriter = originalWriter })
	return &output
}
