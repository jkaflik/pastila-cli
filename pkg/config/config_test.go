package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	expected := &FileConfig{
		PastilaURL:    "https://pastila.example.com/",
		ClickHouseURL: "https://pastila.example.com/play",
	}

	require.NoError(t, Save(path, expected))
	actual, err := Load(path)

	require.NoError(t, err)
	assert.Equal(t, expected, actual)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadMissingFile(t *testing.T) {
	actual, err := Load(filepath.Join(t.TempDir(), "missing.json"))

	require.NoError(t, err)
	assert.Equal(t, &FileConfig{}, actual)
}
