package proxy

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Reasons for a request that AEG does not send upstream.
const (
	ReasonPolicyDenied          = "policy_denied"
	ReasonPathNotCanonical      = "path_not_canonical"
	ReasonPlaceholderMisuse     = "placeholder_misuse"
	ReasonCredentialUnavailable = "credential_unavailable" //nolint:gosec // A reason name, not a credential.
	ReasonUpstreamUnreachable   = "upstream_unreachable"
)

const errorsDoc = "https://github.com/jspdown/aeg/blob/main/docs/errors.md#"

// denial is an answer that AEG gives in place of the upstream. The agent reads its detail,
// so the detail says what to do next.
type denial struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
	Host   string `json:"host"`
	Detail string `json:"detail"`
}

func deny(reason, host, title, detail string) *denial {
	status := http.StatusForbidden

	switch reason {
	case ReasonCredentialUnavailable:
		status = http.StatusServiceUnavailable
	case ReasonUpstreamUnreachable:
		status = http.StatusBadGateway
	}

	return &denial{
		Type:   errorsDoc + reason,
		Title:  title,
		Status: status,
		Reason: reason,
		Host:   host,
		Detail: detail,
	}
}

func (d *denial) write(w http.ResponseWriter) {
	proxyErr := "http_request_denied"

	switch d.Status {
	case http.StatusServiceUnavailable:
		proxyErr = "proxy_internal_error"
	case http.StatusBadGateway:
		proxyErr = "destination_unavailable"
	}

	body, _ := json.Marshal(d) //nolint:errchkjson // The struct has only strings and an int.

	h := w.Header()
	h.Set("Proxy-Status", "aeg; error="+proxyErr+"; details="+strconv.Quote(d.Reason))
	h.Set("Content-Type", "application/problem+json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(d.Status)
	_, _ = w.Write(body)
}
