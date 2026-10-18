package proxy

import (
	"errors"
	"net"
	"slices"
	"strings"

	"github.com/jspdown/aeg/internal/config"
)

// Policy finds the destination of a CONNECT target and checks requests against its routes.
type Policy struct {
	exact     map[string]*config.Destination
	wildcards []*config.Destination
}

// NewPolicy makes a policy from destinations that config.Parse checked.
func NewPolicy(dests []config.Destination) *Policy {
	p := &Policy{exact: make(map[string]*config.Destination)}

	for i := range dests {
		d := &dests[i]
		if d.IsWildcard() {
			p.wildcards = append(p.wildcards, d)
		} else {
			p.exact[net.JoinHostPort(d.Hostname, d.Port)] = d
		}
	}

	// The longest suffix wins, so sort the most specific wildcard first.
	slices.SortFunc(p.wildcards, func(a, b *config.Destination) int { return len(b.Hostname) - len(a.Hostname) })

	return p
}

// Destination returns the destination for host and port, or nil. The host must be normalized.
func (p *Policy) Destination(host, port string) *config.Destination {
	if d, ok := p.exact[net.JoinHostPort(host, port)]; ok {
		return d
	}

	if net.ParseIP(host) != nil {
		return nil
	}

	for _, d := range p.wildcards {
		if d.Port == port && strings.HasSuffix(host, d.Hostname[1:]) {
			return d
		}
	}

	return nil
}

// checkCanonical denies a path that an upstream can read differently from AEG.
// AEG matches the raw path, so it also denies escapes that decode into a separator, a dot or an escape.
func checkCanonical(rawPath string) error {
	if !strings.HasPrefix(rawPath, "/") {
		return errors.New("the path must start with /")
	}

	if strings.ContainsAny(rawPath, `\;`) {
		return errors.New(`the path contains \ or ;`)
	}

	lower := strings.ToLower(rawPath)
	for _, esc := range []string{
		"%2f", // "/". Some upstreams decode it and see more segments than AEG.
		"%2e", // ".". It can hide a . or .. segment.
		"%5c", // "\". Some upstreams read it as "/".
		"%25", // "%". It makes a double encoding, such as %252e for %2e.
		"%00", // NUL. Some upstreams stop the path at it.
	} {
		if strings.Contains(lower, esc) {
			return errors.New("the path contains " + strings.ToUpper(esc))
		}
	}

	segs := strings.Split(rawPath[1:], "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			return errors.New("the path contains a . or .. segment")
		}

		if seg == "" && i != len(segs)-1 {
			return errors.New("the path contains //")
		}
	}

	return nil
}

func routeAllows(d *config.Destination, method, rawPath string) bool {
	if len(d.Routes) == 0 {
		return true
	}

	path := strings.Split(rawPath[1:], "/")

	for _, r := range d.Routes {
		if len(r.Methods) > 0 && !slices.Contains(r.Methods, method) {
			continue
		}

		if len(r.Paths) == 0 {
			return true
		}

		for _, p := range r.Paths {
			if matchPath(strings.Split(p[1:], "/"), path) {
				return true
			}
		}
	}

	return false
}

func matchPath(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}

	switch pattern[0] {
	case "**":
		for i := 0; i <= len(path); i++ {
			if matchPath(pattern[1:], path[i:]) {
				return true
			}
		}

		return false
	case "*":
		return len(path) > 0 && path[0] != "" && matchPath(pattern[1:], path[1:])
	default:
		return len(path) > 0 && path[0] == pattern[0] && matchPath(pattern[1:], path[1:])
	}
}
