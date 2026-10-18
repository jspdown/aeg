//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jspdown/aeg/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upstreamHost resolves to 127.0.0.1 in the VM, see lima.yaml.
// It is not in NO_PROXY, so the clients send it through AEG.
const upstreamHost = "upstream.test"

const token = "e2e-secret"

type upstream struct {
	port   string
	caFile string
}

func TestRun(t *testing.T) {
	t.Parallel()

	bin := os.Getenv("AEG_BIN")
	require.NotEmpty(t, bin, "AEG_BIN must be the path of the aeg binary")

	up := startUpstream(t)

	tests := []struct {
		name string
		// sandbox returns the command that runs the client in a sandbox.
		sandbox func(t *testing.T) []string
	}{
		{name: "without sandbox", sandbox: func(*testing.T) []string { return nil }},
		{name: "in srt", sandbox: srt},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			port := freePort(t)
			cfg := writeConfig(t, port, up.port)

			url := "https://" + net.JoinHostPort(upstreamHost, up.port) + "/"
			client := append(test.sandbox(t), "curl", "--silent", "--show-error", "--fail", url)

			t.Logf("Run aeg on 127.0.0.1:%s, with the command: %s", port, strings.Join(client, " "))
			out := runAEG(t, bin, cfg, up, client)

			t.Log("Check that the upstream got the token that aeg injected")
			assert.Equal(t, "Bearer "+token, out)
		})
	}
}

// startUpstream starts an HTTPS server on 127.0.0.1 that returns the Authorization header of the request.
func startUpstream(t *testing.T) upstream {
	t.Helper()

	ca, err := proxy.NewCA()
	require.NoError(t, err)

	cert, err := ca.Leaf(upstreamHost)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("Authorization")) //nolint:gosec // Only curl reads the response.
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	caFile := filepath.Join(t.TempDir(), "upstream-ca.pem")
	require.NoError(t, os.WriteFile(caFile, ca.PEM(), 0o600))

	t.Logf("Started the upstream on 127.0.0.1:%s, with a certificate for %s", port, upstreamHost)

	return upstream{port: port, caFile: caFile}
}

// srt returns the srt command, with settings that allow only the upstream.
func srt(t *testing.T) []string {
	t.Helper()

	settings := map[string]any{
		"network":    map[string]any{"allowedDomains": []string{upstreamHost}, "deniedDomains": []string{}},
		"filesystem": map[string]any{"denyRead": []string{}, "allowWrite": []string{}, "denyWrite": []string{}},
	}

	data, err := json.Marshal(settings)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "srt.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	t.Logf("Wrote the srt settings: %s", data)

	return []string{"srt", "--settings", path}
}

func writeConfig(t *testing.T, port, upstreamPort string) string {
	t.Helper()

	cfg := fmt.Sprintf(`version: 1
listen:
  port: %s
credentials:
  upstream:
    from:
      env: E2E_TOKEN
destinations:
  - host: %s
    inject:
      - bearer: upstream
`, port, net.JoinHostPort(upstreamHost, upstreamPort))

	path := filepath.Join(t.TempDir(), "aeg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))

	t.Logf("Wrote the aeg configuration:\n%s", cfg)

	return path
}

// runAEG runs "aeg run -- client" and returns the standard output of the client.
// If the test fails, it logs the standard error and the request log of AEG.
func runAEG(t *testing.T, bin, cfg string, up upstream, client []string) string {
	t.Helper()

	logFile := filepath.Join(t.TempDir(), "requests.log")
	args := append([]string{"run", "--config", cfg, "--log", logFile, "--"}, client...)

	cmd := exec.CommandContext(t.Context(), bin, args...) //nolint:gosec // The test selects the command.
	// AEG trusts the upstream CA through SSL_CERT_FILE. It gives its own CA to the client.
	cmd.Env = append(os.Environ(), "E2E_TOKEN="+token, "SSL_CERT_FILE="+up.caFile)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}

		requests, _ := os.ReadFile(logFile) //nolint:gosec // The test selects the file.
		t.Logf("Standard error:\n%s", stderr.String())
		t.Logf("Request log of aeg:\n%s", requests)
	})

	require.NoError(t, err)

	return stdout.String()
}

func freePort(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	_, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	require.NoError(t, l.Close())

	return port
}
