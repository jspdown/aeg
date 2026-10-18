package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jspdown/aeg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "aeg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	return path
}

func TestLoad(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(writeFile(t, "version: 1"))
	require.NoError(t, err)

	want := &config.Config{Version: 1, Listen: config.Listen{IP: config.DefaultIP, Port: config.DefaultPort}}
	assert.Equal(t, want, cfg)
}

func TestLoad_invalid(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "version: 2")

	_, err := config.Load(path)
	assert.EqualError(t, err, path+": version: must be 1, got 2")
}

func TestLoad_missing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.yaml")

	_, err := config.Load(path)
	assert.EqualError(t, err, "reading configuration: open "+path+": no such file or directory")
}
