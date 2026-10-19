package proxy_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jspdown/aeg/internal/config"
	"github.com/jspdown/aeg/internal/credential"
	"github.com/jspdown/aeg/internal/proxy"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	githubPlaceholder = "ghp_aeg_GITHUB"
	otherPlaceholder  = "aeg_OTHER"
)

type env struct {
	client   *http.Client
	upstream string
}

// setup starts an upstream that echoes the request, and a proxy in front of it.
func setup(t *testing.T) *env {
	t.Helper()

	upstream := newUpstream(t)
	host := strings.TrimPrefix(upstream.URL, "https://")

	cfg, err := config.Parse(strings.NewReader(`
version: 1
credentials:
  github: {from: {env: UNUSED}}
  other: {from: {env: UNUSED}}
destinations:
  - host: "` + host + `"
    inject:
      - bearer: github
    routes:
      - methods: [GET]
        paths: ["/repos/acme/**"]
  - host: other.example
    inject:
      - bearer: other
`))
	require.NoError(t, err)

	store := credential.Static(
		map[string]credential.Secret{"github": {Value: "real-token"}, "other": {Value: "other-token"}},
		map[string]string{"github": githubPlaceholder, "other": otherPlaceholder},
	)

	ca, err := proxy.NewCA()
	require.NoError(t, err)

	p := proxy.New(cfg.Destinations, store, ca, zerolog.Nop())
	p.SetTransport(upstream.Client().Transport)

	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM())

	proxyURL, _ := url.Parse(srv.URL)

	return &env{
		upstream: upstream.URL,
		client: &http.Client{Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: true,
		}},
	}
}

func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()

	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization": r.Header.Get("Authorization"),
			"host":          r.Host,
			"proto":         r.Proto,
		})
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	t.Cleanup(upstream.Close)

	return upstream
}

func (e *env) do(t *testing.T, req *http.Request) (int, map[string]any) {
	t.Helper()

	resp, err := e.client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out), "decoding %q", body)

	return resp.StatusCode, out
}

func TestProxy_injects(t *testing.T) {
	t.Parallel()

	e := setup(t)

	req, _ := http.NewRequest(http.MethodGet, e.upstream+"/repos/acme/app", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+githubPlaceholder)

	status, body := e.do(t, req)
	require.Equal(t, http.StatusOK, status, "body: %v", body)

	assert.Equal(t, "Bearer real-token", body["authorization"])
	assert.Equal(t, "HTTP/2.0", body["proto"])
}

func TestProxy_denies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		header http.Header
		host   string
		opaque bool
		reason string
	}{
		{name: "route", method: http.MethodPost, path: "/repos/acme/app", reason: proxy.ReasonPolicyDenied},
		{name: "path", method: http.MethodGet, path: "/repos/other/app", reason: proxy.ReasonPolicyDenied},
		{name: "dot segment", method: http.MethodGet, path: "/repos/acme/%2e%2e/x", reason: proxy.ReasonPathNotCanonical},
		{name: "double slash", method: http.MethodGet, path: "/repos//acme/app", reason: proxy.ReasonPathNotCanonical},
		{
			// The upstream gets "/repos/acme/..;/x%7B", and Tomcat reads "..;" as "..".
			name: "escaped semicolon", method: http.MethodGet, path: "/repos/acme/..%3B/x{", opaque: true,
			reason: proxy.ReasonPathNotCanonical,
		},
		{name: "host header", method: http.MethodGet, path: "/repos/acme/app", host: "evil.example", reason: proxy.ReasonPolicyDenied},
		{
			name: "method override", method: http.MethodGet, path: "/repos/acme/app",
			header: http.Header{"X-Http-Method-Override": {"DELETE"}}, reason: proxy.ReasonPolicyDenied,
		},
		{
			name: "placeholder misuse", method: http.MethodGet, path: "/repos/acme/app",
			header: http.Header{"X-Token": {otherPlaceholder}}, reason: proxy.ReasonPlaceholderMisuse,
		},
		{
			name: "placeholder in query", method: http.MethodGet, path: "/repos/acme/app?k=" + otherPlaceholder,
			reason: proxy.ReasonPlaceholderMisuse,
		},
	}

	e := setup(t)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequest(test.method, e.upstream+test.path, http.NoBody)
			require.NoError(t, err)

			maps.Copy(req.Header, test.header)

			if test.host != "" {
				req.Host = test.host
			}

			// The client sends an opaque path as is, and does not encode it again.
			if test.opaque {
				req.URL.Opaque = test.path
			}

			status, body := e.do(t, req)
			assert.Equal(t, http.StatusForbidden, status, "body: %v", body)
			assert.Equal(t, test.reason, body["reason"])
		})
	}
}

func TestProxy_deniesUnknownHost(t *testing.T) {
	t.Parallel()

	e := setup(t)

	req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/", http.NoBody)

	resp, err := e.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}

	assert.Error(t, err, "want a CONNECT error")
}
