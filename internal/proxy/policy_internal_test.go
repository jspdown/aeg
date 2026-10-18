package proxy

import (
	"strings"
	"testing"

	"github.com/jspdown/aeg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		ok   bool
	}{
		{path: "/", ok: true},
		{path: "/repos/acme/app", ok: true},
		{path: "/simple/requests/", ok: true},
		{path: "/a%20b/c%40d", ok: true},
		{path: "//a"},
		{path: "/a//b"},
		{path: "/a/./b"},
		{path: "/a/../b"},
		{path: "/a/.."},
		{path: `/a\b`},
		{path: "/admin;x"},
		{path: "/a%2Fb"},
		{path: "/a%2fb"},
		{path: "/a/%2e%2e/b"},
		{path: "/a%5cb"},
		{path: "/a%252fb"},
		{path: "/a%00"},
		{path: "a"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()

			err := checkCanonical(test.path)
			if test.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestRouteAllows(t *testing.T) {
	t.Parallel()

	d := &config.Destination{Routes: []config.Route{
		{Methods: []string{"GET"}, Paths: []string{"/repos/acme/**"}},
		{Paths: []string{"/acme/*/info/refs", "/acme/*/git-upload-pack"}},
	}}

	tests := []struct {
		method, path string
		ok           bool
	}{
		{method: "GET", path: "/repos/acme", ok: true},
		{method: "GET", path: "/repos/acme/app/issues", ok: true},
		{method: "POST", path: "/repos/acme/app"},
		{method: "GET", path: "/repos/other/app"},
		{method: "POST", path: "/acme/app/git-upload-pack", ok: true},
		{method: "POST", path: "/acme/app/git-receive-pack"},
		{method: "GET", path: "/acme//info/refs"},
		{method: "GET", path: "/acme/a/b/info/refs"},
	}

	for _, test := range tests {
		t.Run(test.method+test.path, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.ok, routeAllows(d, test.method, test.path))
		})
	}
}

func TestPolicy_Destination(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(`
version: 1
destinations:
  - host: "*.pypi.org"
  - host: "*.files.pypi.org"
  - host: api.github.com
  - host: registry.internal:8443
  - host: 10.0.0.1
`))
	require.NoError(t, err)

	p := NewPolicy(cfg.Destinations)

	tests := []struct {
		host, port, want string
	}{
		{host: "api.github.com", port: "443", want: "api.github.com"},
		{host: "api.github.com", port: "22"},
		{host: "github.com", port: "443"},
		{host: "pypi.org", port: "443"},
		{host: "test.pypi.org", port: "443", want: "*.pypi.org"},
		{host: "a.files.pypi.org", port: "443", want: "*.files.pypi.org"},
		{host: "evilpypi.org", port: "443"},
		{host: "registry.internal", port: "8443", want: "registry.internal:8443"},
		{host: "registry.internal", port: "443"},
		{host: "10.0.0.1", port: "443", want: "10.0.0.1"},
	}

	for _, test := range tests {
		t.Run(test.host+":"+test.port, func(t *testing.T) {
			t.Parallel()

			var got string
			if d := p.Destination(test.host, test.port); d != nil {
				got = d.Host
			}

			assert.Equal(t, test.want, got)
		})
	}
}
