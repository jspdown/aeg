package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenLog(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "logs", "run.log")

	f, err := openLog(path)
	require.NoError(t, err)

	_, err = f.WriteString("first\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// A second run appends to the same file.
	f, err = openLog(path)
	require.NoError(t, err)

	_, err = f.WriteString("second\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\n", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestOpenLog_default(t *testing.T) {
	tests := []struct {
		name string
		xdg  bool
		dir  string
	}{
		{name: "from XDG", xdg: true, dir: "aeg"},
		{name: "from home", dir: filepath.Join(".local", "state", "aeg")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("XDG_STATE_HOME", "")

			if test.xdg {
				t.Setenv("XDG_STATE_HOME", root)
			}

			f, err := openLog("")
			require.NoError(t, err)
			require.NoError(t, f.Close())

			dir := filepath.Join(root, test.dir)
			assert.Regexp(t, "^"+regexp.QuoteMeta(dir+string(filepath.Separator))+`\d{8}T\d{6}-`+strconv.Itoa(os.Getpid())+`\.log$`, f.Name())
		})
	}
}
