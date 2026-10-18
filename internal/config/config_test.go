package config_test

import (
	"strings"
	"testing"

	"github.com/jspdown/aeg/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfig = `
version: 1
listen:
  port: 9000
credentials:
  github:
    from:
      command: gh auth token
  anthropic:
    from:
      env: ANTHROPIC_API_KEY
    placeholder:
      env: ANTHROPIC_API_KEY
      prefix: sk-ant-
destinations:
  - host: api.anthropic.com
    inject:
      - header: { name: x-api-key, credential: anthropic }
  - host: api.github.com
    inject:
      - bearer: github
    routes:
      - methods: [GET]
        paths: ["/repos/acme/**"]
  - host: GitHub.com
    inject:
      - basic: { username: x-access-token, credential: github }
  - host: "*.pypi.org"
  - host: registry.internal:8443
`

func TestParse(t *testing.T) {
	t.Parallel()

	cfg, err := config.Parse(strings.NewReader(validConfig))
	require.NoError(t, err)

	want := &config.Config{
		Version: 1,
		Listen:  config.Listen{IP: config.DefaultIP, Port: 9000},
		Credentials: map[string]config.Credential{
			"github": {From: config.Source{Command: "gh auth token"}},
			"anthropic": {
				From:        config.Source{Env: "ANTHROPIC_API_KEY"},
				Placeholder: &config.Placeholder{Env: "ANTHROPIC_API_KEY", Prefix: "sk-ant-"},
			},
		},
		Destinations: []config.Destination{
			{
				Host:     "api.anthropic.com",
				Inject:   []config.Inject{{Header: &config.Header{Name: "x-api-key", Credential: "anthropic"}}},
				Hostname: "api.anthropic.com",
				Port:     "443",
			},
			{
				Host:     "api.github.com",
				Routes:   []config.Route{{Methods: []string{"GET"}, Paths: []string{"/repos/acme/**"}}},
				Inject:   []config.Inject{{Bearer: "github"}},
				Hostname: "api.github.com",
				Port:     "443",
			},
			{
				Host:     "GitHub.com",
				Inject:   []config.Inject{{Basic: &config.Basic{Username: "x-access-token", Credential: "github"}}},
				Hostname: "github.com",
				Port:     "443",
			},
			{Host: "*.pypi.org", Hostname: "*.pypi.org", Port: "443"},
			{Host: "registry.internal:8443", Hostname: "registry.internal", Port: "8443"},
		},
	}
	assert.Equal(t, want, cfg)

	assert.Equal(t, "X-Api-Key", cfg.Destinations[0].Inject[0].HeaderName())
}

// baseConfig declares the credential "a", so that a destination can inject it.
const baseConfig = "version: 1\ncredentials: {a: {from: {env: A}}}\n"

func TestParse_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, yaml, want string
	}{
		{name: "bad version", yaml: "version: 2", want: "version: must be 1, got 2"},
		{
			name: "unknown key",
			yaml: "version: 1\nagent: x",
			want: "decoding: yaml: unmarshal errors:\n  line 2: field agent not found in type config.Config",
		},
		{
			name: "two sources",
			yaml: "version: 1\ncredentials: {a: {from: {env: A, command: b}}}",
			want: "credentials.a.from: set exactly one of env or command",
		},
		{
			name: "listen on a host name",
			yaml: "version: 1\nlisten: {ip: localhost}",
			want: `listen.ip: "localhost" is not an IP`,
		},
		{
			name: "listen on all IPv4 networks",
			yaml: "version: 1\nlisten: {ip: 0.0.0.0}",
			want: "listen.ip: 0.0.0.0 accepts connections from all networks, set the IP of one interface",
		},
		{
			name: "listen on all IPv6 networks",
			yaml: "version: 1\nlisten: {ip: '::'}",
			want: "listen.ip: :: accepts connections from all networks, set the IP of one interface",
		},
		{
			name: "port out of range",
			yaml: "version: 1\nlisten: {port: 70000}",
			want: "listen.port: 70000 is not between 1 and 65535",
		},
		{name: "unused credential", yaml: baseConfig, want: "credentials.a: no destination injects it"},
		{
			name: "duplicate host",
			yaml: "version: 1\ndestinations: [{host: a.com}, {host: a.com}]",
			want: `destinations[1]: host "a.com" is already defined`,
		},
		{
			name: "duplicate host in another form",
			yaml: "version: 1\ndestinations: [{host: a.com}, {host: 'A.com.:443'}]",
			want: `destinations[1]: host "A.com.:443" is already defined`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Parse(strings.NewReader(test.yaml))
			assert.EqualError(t, err, test.want)
		})
	}
}

func TestParse_destinationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, yaml, want string
	}{
		{
			name: "unknown credential",
			yaml: "version: 1\ndestinations: [{host: a.com, inject: [{bearer: x}]}]",
			want: `destinations[0] (a.com): inject[0]: credential "x" is not defined`,
		},
		{
			name: "inject on wildcard",
			yaml: baseConfig + "destinations: [{host: '*.a.com', inject: [{bearer: a}]}]",
			want: "destinations[0] (*.a.com): inject: needs an exact host, a wildcard host can belong to anybody",
		},
		{
			name: "same header twice",
			yaml: baseConfig + "destinations: [{host: a.com, inject: [{bearer: a}, {basic: {username: u, credential: a}}]}]",
			want: "destinations[0] (a.com): inject[1]: header Authorization is already set",
		},
		{
			name: "two injection types",
			yaml: baseConfig + "destinations: [{host: a.com, inject: [{bearer: a, header: {name: X, credential: a}}]}]",
			want: "destinations[0] (a.com): inject[0]: set exactly one of bearer, basic or header",
		},
		{
			name: "partial glob",
			yaml: "version: 1\ndestinations: [{host: a.com, routes: [{paths: ['/a*']}]}]",
			want: `destinations[0] (a.com): routes[0]: paths: "/a*": * and ** must be a full segment`,
		},
		{
			name: "empty routes",
			yaml: "version: 1\ndestinations: [{host: a.com, routes: []}]",
			want: "destinations[0] (a.com): routes: must not be empty, remove it to allow all routes",
		},
		{
			name: "empty methods",
			yaml: "version: 1\ndestinations: [{host: a.com, routes: [{methods: []}]}]",
			want: "destinations[0] (a.com): routes[0]: methods: must not be empty, remove it to allow all methods",
		},
		{
			name: "empty paths",
			yaml: "version: 1\ndestinations: [{host: a.com, routes: [{paths: []}]}]",
			want: "destinations[0] (a.com): routes[0]: paths: must not be empty, remove it to allow all paths",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Parse(strings.NewReader(test.yaml))
			assert.EqualError(t, err, test.want)
		})
	}
}
