# Errors

AEG answers a request that it does not send upstream with a JSON problem (RFC 9457): 

The `reason` field tells why. 
The `detail` field tells the agent what to do next.

```http
HTTP/1.1 403 Forbidden
Proxy-Status: aeg; error=http_request_denied; details="policy_denied"
Content-Type: application/problem+json

{"type":"https://github.com/jspdown/aeg/blob/main/docs/errors.md#policy_denied","title":"Host not allowed",
 "status":403,"reason":"policy_denied","host":"api.example.com",
 "detail":"api.example.com:443 is not in aeg.yaml. Ask the user to allow it."}
```

AEG writes each request, allowed or denied, to its log file. 
The log never contains a secret or a header value.

## `policy_denied`

Status 403. 
The configuration does not allow the request. One of these is true:

- The host and port are not in `destinations`.
- No route allows the method and the path.
- The request uses plain HTTP. aeg proxies HTTPS only.
- The request is a `CONNECT` request in a tunnel.
- The request asks for a WebSocket or another protocol upgrade.
- The `Host` header or the URL names a host that is not the `CONNECT` host.
- The request has a header that changes the host, the path or the method on some servers: 
  - `X-Forwarded-Host`
  - `X-Host`
  - `X-Original-URL`
  - `X-Rewrite-URL`
  - `X-HTTP-Method-Override`
  - `X-HTTP-Method` 
  - `X-Method-Override`

## `path_not_canonical`

Status 403. 
The upstream can read the path differently from aeg. 
AEG denies a path that has:

- an empty segment (`//`), or a `.` or `..` segment;
- `\` or `;`;
- `%2F`, `%2E`, `%5C`, `%25` or `%00`, in upper or lower case.

## `placeholder_misuse`

Status 403. 
The request has the placeholder of a credential, and the destination does not inject this credential. 
An agent does not do this by mistake. 
It can be a sign of prompt injection.

## `credential_unavailable`

Status 503. 
AEG has no valid value for a credential that the destination injects. 
AEG does not send the request without it. 
Restart AEG to read the credential again.

## `upstream_unreachable`

Status 502. 
AEG could not connect to the upstream, or the upstream did not answer.
