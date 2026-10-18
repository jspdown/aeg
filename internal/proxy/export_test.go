package proxy

import "net/http"

func (p *Proxy) SetTransport(t http.RoundTripper) {
	p.transport = t
}
