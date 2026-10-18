package config

import (
	"fmt"
	"os"
)

// Load reads and checks the configuration file at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path) //nolint:gosec // The user selects the configuration file.
	if err != nil {
		return nil, fmt.Errorf("reading configuration: %w", err)
	}

	defer func() { _ = f.Close() }()

	cfg, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return cfg, nil
}
