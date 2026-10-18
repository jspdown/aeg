package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jspdown/aeg/internal/config"
	"github.com/jspdown/aeg/internal/credential"
	"github.com/jspdown/aeg/internal/proxy"
	"github.com/rs/zerolog"
)

// run starts the proxy, runs the command and returns its exit code.
func run(ctx context.Context, configPath, logPath string, args []string) (int, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return 0, err
	}

	if !net.ParseIP(cfg.Listen.IP).IsLoopback() {
		zerolog.Ctx(ctx).Warn().
			Str("ip", cfg.Listen.IP).
			Msg("The proxy is not on a loopback IP. Each client that can connect to this IP can send requests with your credentials")
	}

	store, err := credential.Resolve(ctx, cfg.Credentials)
	if err != nil {
		return 0, err
	}

	ca, err := proxy.NewCA()
	if err != nil {
		return 0, err
	}

	tmp, err := os.MkdirTemp("", "aeg-")
	if err != nil {
		return 0, fmt.Errorf("creating temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	caPath := filepath.Join(tmp, "ca.pem")
	if err = os.WriteFile(caPath, ca.PEM(), 0o600); err != nil {
		return 0, fmt.Errorf("writing CA bundle: %w", err)
	}

	logFile, err := openLog(logPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = logFile.Close() }()

	l, err := listen(ctx, cfg.Listen.Addr())
	if err != nil {
		return 0, err
	}

	logger := zerolog.New(logFile).With().Timestamp().Logger()
	srv := proxy.New(cfg.Destinations, store, ca, logger).Server()
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	return execute(ctx, args, harnessEnv(os.Environ(), cfg, store, caPath))
}

// openLog opens the log file at path.
// Without a path, it opens a new file in $XDG_STATE_HOME/aeg, or ~/.local/state/aeg.
func openLog(path string) (*os.File, error) {
	if path == "" {
		dir := os.Getenv("XDG_STATE_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("finding home directory: %w", err)
			}

			dir = filepath.Join(home, ".local", "state")
		}

		path = filepath.Join(dir, "aeg", time.Now().Format("20060102T150405")+"-"+strconv.Itoa(os.Getpid())+".log")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // The user selects the log file.
		return nil, fmt.Errorf("creating log directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // The user selects the log file.
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}

	return f, nil
}

func listen(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig

	l, err := lc.Listen(ctx, "tcp", addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, fmt.Errorf("listen: %s is in use, set another port with listen.port in the configuration", addr)
	}

	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return l, nil
}

// harnessEnv returns the environment of the harness: the proxy and CA variables, the placeholders, and no variable that holds a real secret.
func harnessEnv(base []string, cfg *config.Config, store *credential.Store, caPath string) []string {
	// Local servers are not in the scope of AEG, and they never get a credential, so local traffic goes to them directly.
	// A destination on localhost or 127.0.0.1 has no effect.
	// Tools read either the upper case or the lower case name, so AEG sets both.
	set := map[string]string{"NO_PROXY": "localhost,127.0.0.1", "no_proxy": "localhost,127.0.0.1"}

	// These variables make the clients send their requests to AEG, which applies the policy and injects the credentials.
	// The value is http:// also for HTTPS_PROXY: the client sends CONNECT to AEG in plain HTTP, then starts TLS inside the tunnel.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		set[name] = "http://" + cfg.Listen.Addr()
	}

	// AEG ends the TLS of each request with a certificate from the run CA, so the clients must trust this CA.
	// There is no common variable for this: OpenSSL, Python requests, curl, Node.js, git and pip each read their own.
	// docs/tls.md lists the clients that read none of these variables.
	for _, name := range []string{
		"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "GIT_SSL_CAINFO", "PIP_CERT",
	} {
		set[name] = caPath
	}

	remove := make(map[string]bool)

	for name, cred := range cfg.Credentials {
		// The variable that holds the real secret must not reach the agent.
		if cred.From.Env != "" {
			remove[cred.From.Env] = true
		}

		// A placeholder can use the same variable as the secret.
		// It still gets to the agent, because AEG adds the set variables after it removes the others.
		if cred.Placeholder != nil {
			set[cred.Placeholder.Env] = store.Placeholder(name)
		}
	}

	env := make([]string, 0, len(base)+len(set))

	// Drop the variables that AEG sets, so that each name has one value.
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := set[name]; !ok && !remove[name] {
			env = append(env, kv)
		}
	}

	for name, value := range set {
		env = append(env, name+"="+value)
	}

	return env
}

// execute runs the command in the foreground process group of the terminal.
// The terminal sends SIGINT and SIGQUIT to the command directly, so AEG ignores them.
// SIGTERM and SIGHUP usually come only to AEG, so AEG forwards them.
func execute(ctx context.Context, args, env []string) (int, error) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // The user selects the command.
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", args[0], err)
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM || s == syscall.SIGHUP {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, err
	}

	return exitCode(exitErr.ProcessState), nil
}

// exitCode returns the exit code of the process, and uses the shell convention when a signal killed the process.
// For a killed process, Go gives -1, which the OS truncates to 255, and the caller cannot know what occurred.
// A shell gives 128 plus the signal number, for example 143 for SIGTERM, and scripts, CI systems and Kubernetes read these values.
// With the same values, the caller gets the same code from AEG as from the command alone.
func exitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}

	return ps.ExitCode()
}
