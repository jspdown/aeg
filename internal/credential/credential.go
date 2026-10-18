// Package credential reads the secrets that AEG injects, and makes the placeholders
// that the agent holds in their place.
package credential

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jspdown/aeg/internal/config"
)

// ErrUnavailable is returned when a credential has no valid value.
var ErrUnavailable = errors.New("credential unavailable")

// Secret is the value of a credential. A zero Expires means that it does not expire.
type Secret struct {
	Value   string
	Expires time.Time
}

// Store holds the secrets of a run and their placeholders.
type Store struct {
	secrets map[string]Secret
	// placeholders maps a credential name to its placeholder value.
	placeholders map[string]string
}

// Resolve reads all the secrets. It runs before the harness starts, while AEG owns the
// terminal, so a command source can ask the user for input.
func Resolve(ctx context.Context, creds map[string]config.Credential) (*Store, error) {
	s := &Store{
		secrets:      make(map[string]Secret, len(creds)),
		placeholders: make(map[string]string),
	}

	for name, cred := range creds {
		value, err := read(ctx, cred.From)
		if err != nil {
			return nil, fmt.Errorf("credential %s: %w", name, err)
		}

		s.secrets[name] = Secret{Value: value}

		if cred.Placeholder != nil {
			s.placeholders[name] = cred.Placeholder.Prefix + "aeg_" + rand.Text()
		}
	}

	return s, nil
}

func read(ctx context.Context, src config.Source) (string, error) {
	if src.Env != "" {
		value := os.Getenv(src.Env)
		if value == "" {
			return "", fmt.Errorf("variable %s is empty or not set", src.Env)
		}

		return value, nil
	}

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, "sh", "-c", src.Command) //nolint:gosec // The user selects this command in the configuration.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command %q: %w", src.Command, err)
	}

	value := strings.TrimSpace(out.String())
	if value == "" {
		return "", fmt.Errorf("command %q: empty output", src.Command)
	}

	return value, nil
}

// Get returns the value of a credential.
func (s *Store) Get(name string, now time.Time) (string, error) {
	sec, ok := s.secrets[name]
	if !ok || (!sec.Expires.IsZero() && !now.Before(sec.Expires)) {
		return "", fmt.Errorf("%w: %s", ErrUnavailable, name)
	}

	return sec.Value, nil
}

// Placeholder returns the placeholder of a credential, or "" if it has none.
func (s *Store) Placeholder(name string) string {
	return s.placeholders[name]
}

// FindPlaceholder returns the credential whose placeholder is in text, or "".
func (s *Store) FindPlaceholder(text string) string {
	for name, p := range s.placeholders {
		if strings.Contains(text, p) {
			return name
		}
	}

	return ""
}

// Static makes a store from known values.
func Static(secrets map[string]Secret, placeholders map[string]string) *Store {
	return &Store{secrets: secrets, placeholders: placeholders}
}
