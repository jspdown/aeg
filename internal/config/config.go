// Package config reads and checks the AEG configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Default proxy address, when the configuration does not set one.
// The IP is a loopback literal: no other machine can use the credentials, and the client cannot resolve it to the other IP family.
const (
	DefaultIP   = "127.0.0.1"
	DefaultPort = 18080
)

// Config is the content of an AEG configuration file.
type Config struct {
	Version      int                   `yaml:"version"`
	Listen       Listen                `yaml:"listen"`
	Credentials  map[string]Credential `yaml:"credentials"`
	Destinations []Destination         `yaml:"destinations"`
}

// Listen is the address of the proxy.
type Listen struct {
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

// Addr returns the address in the "ip:port" form.
func (l Listen) Addr() string {
	return net.JoinHostPort(l.IP, strconv.Itoa(l.Port))
}

// Credential tells where AEG gets a secret, and what the agent gets in its place.
type Credential struct {
	From        Source       `yaml:"from"`
	Placeholder *Placeholder `yaml:"placeholder"`
}

// Source is where AEG reads the real value of a credential. Exactly one field is set.
type Source struct {
	Env     string `yaml:"env"`
	Command string `yaml:"command"`
}

// Placeholder is a dummy value for clients that do not start without a credential.
type Placeholder struct {
	Env    string `yaml:"env"`
	Prefix string `yaml:"prefix"`
}

// Destination is a host that the agent can call. A destination without routes allows
// all requests to the host.
type Destination struct {
	Host   string   `yaml:"host"`
	Routes []Route  `yaml:"routes"`
	Inject []Inject `yaml:"inject"`

	// Hostname and Port are the parts of Host, set by Parse.
	Hostname string `yaml:"-"`
	Port     string `yaml:"-"`
}

// Route allows a set of methods and paths.
// A route without methods allows all methods.
// A route without paths allows all paths.
type Route struct {
	Methods []string `yaml:"methods"`
	Paths   []string `yaml:"paths"`
}

// Inject sets one header from a credential. Exactly one field is set.
type Inject struct {
	Bearer string  `yaml:"bearer"`
	Basic  *Basic  `yaml:"basic"`
	Header *Header `yaml:"header"`
}

// Basic sets "Authorization: Basic base64(username:secret)".
type Basic struct {
	Username   string `yaml:"username"`
	Credential string `yaml:"credential"`
}

// Header sets "Name: Prefix<secret>".
type Header struct {
	Name       string `yaml:"name"`
	Prefix     string `yaml:"prefix"`
	Credential string `yaml:"credential"`
}

// HeaderName returns the header that the injection sets.
func (i Inject) HeaderName() string {
	if i.Header != nil {
		return http.CanonicalHeaderKey(i.Header.Name)
	}

	return "Authorization"
}

// CredentialName returns the credential that the injection uses.
func (i Inject) CredentialName() string {
	switch {
	case i.Basic != nil:
		return i.Basic.Credential
	case i.Header != nil:
		return i.Header.Credential
	default:
		return i.Bearer
	}
}

// IsWildcard reports whether the destination matches the subdomains of a domain.
func (d *Destination) IsWildcard() bool {
	return strings.HasPrefix(d.Hostname, "*.")
}

// Parse reads a configuration and checks it.
func Parse(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err = dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding: %w", err)
	}

	if cfg.Listen.IP == "" {
		cfg.Listen.IP = DefaultIP
	}

	if cfg.Listen.Port == 0 {
		cfg.Listen.Port = DefaultPort
	}

	if err = cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("version: must be 1, got %d", c.Version)
	}

	if err := c.Listen.validate(); err != nil {
		return fmt.Errorf("listen.%w", err)
	}

	if err := c.validateCredentials(); err != nil {
		return err
	}

	used := make(map[string]bool)
	hosts := make(map[string]bool)

	for i := range c.Destinations {
		d := &c.Destinations[i]
		if err := d.validate(c.Credentials, used); err != nil {
			return fmt.Errorf("destinations[%d] (%s): %w", i, d.Host, err)
		}

		hostport := net.JoinHostPort(d.Hostname, d.Port)
		if hosts[hostport] {
			return fmt.Errorf("destinations[%d]: host %q is already defined", i, d.Host)
		}

		hosts[hostport] = true
	}

	for name := range c.Credentials {
		if !used[name] {
			return fmt.Errorf("credentials.%s: no destination injects it", name)
		}
	}

	return nil
}

func (l Listen) validate() error {
	// A host name is refused, because a resolution can give more than one IP, or an IP that is not the expected one.
	ip := net.ParseIP(l.IP)
	if ip == nil {
		return fmt.Errorf("ip: %q is not an IP", l.IP)
	}

	// 0.0.0.0 and :: accept connections from all networks, and each client on them can use the credentials.
	if ip.IsUnspecified() {
		return fmt.Errorf("ip: %s accepts connections from all networks, set the IP of one interface", l.IP)
	}

	if l.Port < 1 || l.Port > 65535 {
		return fmt.Errorf("port: %d is not between 1 and 65535", l.Port)
	}

	return nil
}

func (c *Config) validateCredentials() error {
	envs := make(map[string]string)

	for name, cred := range c.Credentials {
		if (cred.From.Env == "") == (cred.From.Command == "") {
			return fmt.Errorf("credentials.%s.from: set exactly one of env or command", name)
		}

		if cred.Placeholder == nil {
			continue
		}

		env := cred.Placeholder.Env
		if env == "" {
			return fmt.Errorf("credentials.%s.placeholder.env: must be set", name)
		}

		if other, ok := envs[env]; ok {
			return fmt.Errorf("credentials.%s.placeholder.env: %s is already used by %s", name, env, other)
		}

		envs[env] = name
	}

	return nil
}

func (d *Destination) validate(creds map[string]Credential, used map[string]bool) error {
	if err := d.parseHost(); err != nil {
		return err
	}

	// A missing list allows all, but an empty list is an error, because a user can read it as "none".
	// YAML decodes a missing list to nil, and [] to an empty list that is not nil.
	if d.Routes != nil && len(d.Routes) == 0 {
		return errors.New("routes: must not be empty, remove it to allow all routes")
	}

	for i, r := range d.Routes {
		if err := r.validate(); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
	}

	if len(d.Inject) > 0 && d.IsWildcard() {
		return errors.New("inject: needs an exact host, a wildcard host can belong to anybody")
	}

	headers := make(map[string]bool)

	for i, inj := range d.Inject {
		if err := inj.validate(creds); err != nil {
			return fmt.Errorf("inject[%d]: %w", i, err)
		}

		name := inj.HeaderName()
		if headers[name] {
			return fmt.Errorf("inject[%d]: header %s is already set", i, name)
		}

		headers[name] = true
		used[inj.CredentialName()] = true
	}

	return nil
}

func (d *Destination) parseHost() error {
	host, port := d.Host, "443"
	if strings.Contains(host, ":") {
		var err error
		if host, port, err = net.SplitHostPort(host); err != nil {
			return fmt.Errorf("host: %w", err)
		}
	}

	host = NormalizeHost(host)

	// A wildcard is allowed only as a leading "*.", so that it matches names in one DNS subtree.
	// A "*" in another place, such as "*github.com" or "github.*", can match names that other parties control.
	if host == "" || strings.Contains(strings.TrimPrefix(host, "*."), "*") {
		return errors.New("host: must be a name, an IP or *.domain")
	}

	d.Hostname, d.Port = host, port

	return nil
}

// NormalizeHost lets a host such as "GitHub.com." match "github.com".
func NormalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (r Route) validate() error {
	// An empty list is an error for the same reason as empty routes.
	if r.Methods != nil && len(r.Methods) == 0 {
		return errors.New("methods: must not be empty, remove it to allow all methods")
	}

	if r.Paths != nil && len(r.Paths) == 0 {
		return errors.New("paths: must not be empty, remove it to allow all paths")
	}

	for _, m := range r.Methods {
		if m == "" || m != strings.ToUpper(m) {
			return fmt.Errorf("methods: %q must be upper case", m)
		}
	}

	for _, p := range r.Paths {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("paths: %q must start with /", p)
		}

		for seg := range strings.SplitSeq(p[1:], "/") {
			if strings.Contains(seg, "*") && seg != "*" && seg != "**" {
				return fmt.Errorf("paths: %q: * and ** must be a full segment", p)
			}
		}
	}

	return nil
}

func (i Inject) validate(creds map[string]Credential) error {
	set := 0

	for _, ok := range []bool{i.Bearer != "", i.Basic != nil, i.Header != nil} {
		if ok {
			set++
		}
	}

	if set != 1 {
		return errors.New("set exactly one of bearer, basic or header")
	}

	if i.Header != nil && (i.Header.Name == "" || i.HeaderName() == "Host") {
		return errors.New("header.name: must be set, and must not be Host")
	}

	name := i.CredentialName()
	if _, ok := creds[name]; !ok {
		return fmt.Errorf("credential %q is not defined", name)
	}

	return nil
}
