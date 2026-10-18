// Package proxy is the HTTP proxy that applies the egress policy and injects credentials.
package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/jspdown/aeg/internal/config"
	"github.com/jspdown/aeg/internal/credential"
	"github.com/rs/zerolog"
)

// Proxy handles CONNECT requests, terminates TLS with a certificate from the run CA and
// sends each allowed request upstream.
type Proxy struct {
	policy    *Policy
	creds     *credential.Store
	ca        *CA
	transport http.RoundTripper
	log       zerolog.Logger
	errLog    *log.Logger
}

// New makes a proxy. It sends requests upstream directly, and ignores the proxy variables.
func New(dests []config.Destination, creds *credential.Store, ca *CA, logger zerolog.Logger) *Proxy {
	return &Proxy{
		policy: NewPolicy(dests),
		creds:  creds,
		ca:     ca,
		transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
		},
		log: logger,
		// The harness owns the terminal, so net/http errors go to the log file and not to stderr.
		errLog: log.New(logger, "", 0),
	}
}

// Server returns an HTTP server that serves the proxy.
func (p *Proxy) Server() *http.Server {
	return &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second, ErrorLog: p.errLog}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		p.deny(w, r, deny(ReasonPolicyDenied, r.URL.Hostname(), "Plain HTTP not supported", "aeg only proxies HTTPS. Use an https:// URL."))

		return
	}

	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		p.deny(w, r, deny(ReasonPolicyDenied, r.Host, "Invalid CONNECT target", "The CONNECT target must be host:port."))

		return
	}

	host = config.NormalizeHost(host)

	d := p.policy.Destination(host, port)
	if d == nil {
		p.deny(w, r, deny(ReasonPolicyDenied, host, "Host not allowed", net.JoinHostPort(host, port)+" is not in aeg.yaml. Ask the user to allow it."))

		return
	}

	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		p.deny(w, r, deny(ReasonPolicyDenied, host, "Tunnel failed", err.Error()))

		return
	}

	if _, err = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()

		return
	}

	p.tunnel(&bufferedConn{Conn: conn, r: buf.Reader}, d, host, port)
}

// tunnel serves the HTTP requests that the client sends inside the TLS tunnel.
func (p *Proxy) tunnel(conn net.Conn, d *config.Destination, host, port string) {
	tlsConn := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		// The certificate names the CONNECT host, whatever the client sends in SNI.
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return p.ca.Leaf(host) },
	})

	t := &tunnelHandler{p: p, dest: d, host: host, port: port}
	t.upstream = &httputil.ReverseProxy{
		Rewrite:        t.rewrite,
		Transport:      p.transport,
		FlushInterval:  -1,
		ErrorLog:       p.errLog,
		ModifyResponse: t.logResponse,
		ErrorHandler:   t.upstreamError,
	}

	l := &oneConnListener{conn: tlsConn, done: make(chan struct{})}
	srv := &http.Server{
		Handler:           t,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          p.errLog,
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				_ = l.Close()
			}
		},
	}

	_ = srv.Serve(l)
}

func (p *Proxy) deny(w http.ResponseWriter, r *http.Request, d *denial) {
	p.log.Warn().
		Str("method", r.Method).
		Str("host", d.Host).
		Str("path", r.URL.Path).
		Int("status", d.Status).
		Str("reason", d.Reason).
		Str("detail", d.Detail).
		Msg("denied")

	d.write(w)
}

type tunnelHandler struct {
	p        *Proxy
	dest     *config.Destination
	host     string
	port     string
	upstream *httputil.ReverseProxy
}

func (t *tunnelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d := t.check(r); d != nil {
		t.p.deny(w, r, d)

		return
	}

	for _, inj := range t.dest.Inject {
		value, err := t.p.creds.Get(inj.CredentialName(), time.Now())
		if err != nil {
			t.p.deny(w, r, deny(ReasonCredentialUnavailable, t.host, "Credential unavailable",
				"aeg has no valid value for credential "+inj.CredentialName()+". Ask the user to restart aeg."))

			return
		}

		r.Header.Set(inj.HeaderName(), headerValue(inj, value))
	}

	t.upstream.ServeHTTP(w, r)
}

func headerValue(inj config.Inject, secret string) string {
	switch {
	case inj.Basic != nil:
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(inj.Basic.Username+":"+secret))
	case inj.Header != nil:
		return inj.Header.Prefix + secret
	default:
		return "Bearer " + secret
	}
}

func (t *tunnelHandler) check(r *http.Request) *denial {
	if r.Method == http.MethodConnect {
		return deny(ReasonPolicyDenied, t.host, "Nested CONNECT not supported", "AEG does not support a CONNECT request in a tunnel.")
	}

	if r.Header.Get("Upgrade") != "" {
		return deny(ReasonPolicyDenied, t.host, "Upgrade not supported", "AEG does not support WebSocket or other protocol upgrades.")
	}

	if !t.sameHost(r.Host) || (r.URL.Host != "" && !t.sameHost(r.URL.Host)) {
		return deny(ReasonPolicyDenied, t.host, "Host mismatch", "The request host must be the CONNECT host "+t.host+".")
	}

	// These headers change the host, the path or the method on some upstreams.
	// AEG checks the request line, so it denies a request that has one.
	for _, h := range []string{
		"X-Forwarded-Host", "X-Host", "X-Original-Url", "X-Rewrite-Url",
		"X-Http-Method-Override", "X-Http-Method", "X-Method-Override",
	} {
		if _, ok := r.Header[h]; ok {
			return deny(ReasonPolicyDenied, t.host, "Header not allowed", "aeg does not allow the "+h+" header.")
		}
	}

	// EscapedPath can change the encoding that the client sent, and checkCanonical must see that encoding.
	// RequestURI has it only when it starts with "/".
	rawPath := r.URL.EscapedPath()
	if strings.HasPrefix(r.RequestURI, "/") {
		rawPath, _, _ = strings.Cut(r.RequestURI, "?")
	}

	if err := checkCanonical(rawPath); err != nil {
		return deny(ReasonPathNotCanonical, t.host, "Path not canonical", err.Error()+". Send a canonical path.")
	}

	if !routeAllows(t.dest, r.Method, rawPath) {
		return deny(ReasonPolicyDenied, t.host, "Route not allowed",
			r.Method+" "+rawPath+" is not allowed on "+t.host+" by aeg.yaml. Ask the user to allow it.")
	}

	if name := t.misusedPlaceholder(r); name != "" {
		return deny(ReasonPlaceholderMisuse, t.host, "Placeholder sent to the wrong host",
			"The placeholder of credential "+name+" is not for "+t.host+". Do not send it to this host.")
	}

	return nil
}

func (t *tunnelHandler) sameHost(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, t.port
	}

	return config.NormalizeHost(host) == t.host && port == t.port
}

// misusedPlaceholder returns the credential whose placeholder is in the request but that this destination does not inject.
// It looks at the request target and the header values.
func (t *tunnelHandler) misusedPlaceholder(r *http.Request) string {
	texts := []string{r.RequestURI}

	for _, values := range r.Header {
		for _, v := range values {
			texts = append(texts, v)

			if scheme, enc, ok := strings.Cut(v, " "); ok && strings.EqualFold(scheme, "Basic") {
				if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc)); err == nil {
					texts = append(texts, string(dec))
				}
			}
		}
	}

	for _, text := range texts {
		if name := t.p.creds.FindPlaceholder(text); name != "" && !t.injects(name) {
			return name
		}
	}

	return ""
}

func (t *tunnelHandler) injects(name string) bool {
	for _, inj := range t.dest.Inject {
		if inj.CredentialName() == name {
			return true
		}
	}

	return false
}

func (t *tunnelHandler) rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = "https"
	pr.Out.URL.Host = net.JoinHostPort(t.host, t.port)

	pr.Out.Host = t.host
	if t.port != "443" {
		pr.Out.Host = pr.Out.URL.Host
	}
}

func (t *tunnelHandler) logResponse(resp *http.Response) error {
	creds := make([]string, 0, len(t.dest.Inject))
	for _, inj := range t.dest.Inject {
		creds = append(creds, inj.CredentialName())
	}

	t.p.log.Info().
		Str("method", resp.Request.Method).
		Str("host", t.host).
		Str("path", resp.Request.URL.Path).
		Int("status", resp.StatusCode).
		Strs("credentials", creds).
		Msg("allowed")

	return nil
}

func (t *tunnelHandler) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	t.p.deny(w, r, deny(ReasonUpstreamUnreachable, t.host, "Upstream unreachable", err.Error()))
}

// bufferedConn reads the bytes that the HTTP server buffered before the hijack first.
type bufferedConn struct {
	net.Conn

	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// oneConnListener gives one connection to http.Server.Serve, then blocks until closed.
type oneConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
	used bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true

		return l.conn, nil
	}

	<-l.done

	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.done) })

	return nil
}

func (l *oneConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
