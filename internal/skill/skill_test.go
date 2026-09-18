package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentUsesPortableSkillMetadata(t *testing.T) {
	content := string(Content)

	assert.True(t, strings.HasPrefix(content, "---\n"))
	assert.Contains(t, content, "\nname: pastila\n")
	assert.Contains(t, content, "\ndescription: ")
	assert.Contains(t, content, "\ncompatibility: ")
	assert.Contains(t, content, "\n---\n")
}

func TestInstallAllTargets(t *testing.T) {
	home := t.TempDir()

	results, err := Install(home, TargetAll)

	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, result := range results {
		assert.Equal(t, StatusInstalled, result.Status)
		installed, readErr := os.ReadFile(result.Path)
		require.NoError(t, readErr)
		assert.Equal(t, Content, installed)
	}
	assert.Equal(t, filepath.Join(home, ".agents", "skills", Name, "SKILL.md"), results[0].Path)
	assert.Equal(t, filepath.Join(home, ".claude", "skills", Name, "SKILL.md"), results[1].Path)
}

func TestInstallTargetSelection(t *testing.T) {
	testCases := map[string]struct {
		target Target
		path   string
	}{
		"portable": {target: TargetPortable, path: filepath.Join(".agents", "skills", Name, "SKILL.md")},
		"claude":   {target: TargetClaude, path: filepath.Join(".claude", "skills", Name, "SKILL.md")},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()

			results, err := Install(home, testCase.target)

			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, filepath.Join(home, testCase.path), results[0].Path)
		})
	}
}

func TestInstallOverwritesChangedSkillAndThenRemainsUnchanged(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", Name, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("custom skill\n"), 0o600))

	results, err := Install(home, TargetPortable)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusUpdated, results[0].Status)
	installed, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, Content, installed)

	results, err = Install(home, TargetPortable)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusUnchanged, results[0].Status)
}

func TestInstallRejectsInvalidTarget(t *testing.T) {
	_, err := Install(t.TempDir(), Target("other"))

	assert.ErrorContains(t, err, "unsupported skill target")
}
