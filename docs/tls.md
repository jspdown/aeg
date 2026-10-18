# TLS

AEG reads each HTTPS request, so it terminates TLS. 
It signs a certificate for each host with a certificate authority (CA) that it makes for the run. 
The CA key exists only in the memory of AEG. It is never written to disk.

AEG writes the CA certificate to a temporary file and sets these variables in the environment of the command:

- `SSL_CERT_FILE`
- `REQUESTS_CA_BUNDLE`
- `CURL_CA_BUNDLE`
- `NODE_EXTRA_CA_CERTS`
- `GIT_SSL_CAINFO`
- `PIP_CERT`.

The file contains only the run CA. A client that uses one of these variables and does not use the proxy cannot connect to a real server.

## Go programs on macOS

Go on macOS ignores `SSL_CERT_FILE` and trusts only the keychain. 
A Go program, for example `gh` or `terraform`, then fails with a certificate error.

Go 1.27 and later can read `SSL_CERT_FILE` on macOS. To enable it, set this variable before you run AEG:

```sh
export GODEBUG=x509sslcertoverrideplatform=1
```

A program built with Go 1.26 or earlier cannot trust the run CA.

## Clients that read none of these variables

These clients fail with a certificate error:

- **Clients with their own variable.** The AWS CLI and boto3 read `AWS_CA_BUNDLE`, and Deno reads `DENO_CERT`. In the command, set the variable to `$SSL_CERT_FILE`.
- **Java programs.** The JVM reads only its trust store. AEG makes a new CA for each run, so it is not practical to import the CA into the trust store.
- **Native macOS programs.** Programs that use the TLS of macOS, for example programs in Swift or .NET, trust only the keychain. The keychain has the same problem as the Java trust store.
- **Programs with a built-in root list.** For example, a Rust program that uses `webpki-roots` does not read a file.
- **Programs that pin a certificate.** These programs accept only a known certificate or key, so no setting can make them trust the run CA.

## Local HTTPS servers

AEG sets `NO_PROXY=localhost,127.0.0.1`, so the command connects to local servers directly. 
A local server that uses HTTPS fails verification, because the bundle contains only the run CA. 
A local server that uses plain HTTP is not affected.
