package endpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultPort is the loopback TCP port used when the TCP shorthand is
// requested without a port.
const DefaultPort = 18340

// DefaultHost is the dial target used when OP_FORWARD_HOST is unset.
const DefaultHost = "127.0.0.1"

// DefaultSocketName is the file name of the default Unix socket.
const DefaultSocketName = "op-forward.sock"

// Environment variables. OP_FORWARD_ADDR and OP_FORWARD_LISTEN carry a full
// endpoint (tcp://host:port or unix:///path). OP_FORWARD_HOST and
// OP_FORWARD_PORT remain supported as shorthand for a TCP endpoint; they are
// consulted only when the full form is unset. When neither is set, both
// sides use the Unix socket at DefaultSocketPath, whose location
// OP_FORWARD_SOCKET_PATH overrides.
const (
	EnvAddr       = "OP_FORWARD_ADDR"
	EnvListen     = "OP_FORWARD_LISTEN"
	EnvHost       = "OP_FORWARD_HOST"
	EnvPort       = "OP_FORWARD_PORT"
	EnvSocketPath = "OP_FORWARD_SOCKET_PATH"
)

// IntFromEnv reads an integer setting, returning def when the variable is
// unset, not a number, or below min. Every numeric knob in op-forward is a
// port or a duration, for which zero and negative values are never what the
// operator meant, so they fall back to the default instead of being applied.
func IntFromEnv(key string, def, min int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min {
		return def
	}
	return v
}

// PortFromEnv returns OP_FORWARD_PORT, or DefaultPort when unset or invalid.
func PortFromEnv() int {
	return IntFromEnv(EnvPort, DefaultPort, 1)
}

// HostFromEnv returns OP_FORWARD_HOST, or DefaultHost when unset. Setting it
// to host.docker.internal lets a Docker Desktop container reach the host
// daemon without an SSH tunnel.
func HostFromEnv() string {
	if h := os.Getenv(EnvHost); h != "" {
		return h
	}
	return DefaultHost
}

// DefaultSocketPath returns the Unix socket used when no endpoint is
// configured: OP_FORWARD_SOCKET_PATH if set, else op-forward.sock in
// $XDG_RUNTIME_DIR, else op-forward/op-forward.sock in the per-user cache
// directory. XDG_RUNTIME_DIR is ignored unless absolute, as the XDG Base
// Directory Specification requires; a relative OP_FORWARD_SOCKET_PATH is an
// error so that a typo fails at startup instead of binding somewhere else.
func DefaultSocketPath() (string, error) {
	if p := os.Getenv(EnvSocketPath); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("%s=%q: socket path must be absolute", EnvSocketPath, p)
		}
		return filepath.Clean(p), nil
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, DefaultSocketName), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving default socket path: %w", err)
	}
	return filepath.Join(cacheDir, "op-forward", DefaultSocketName), nil
}

// DefaultUnix returns the Unix endpoint at DefaultSocketPath.
func DefaultUnix() (Endpoint, error) {
	path, err := DefaultSocketPath()
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Network: "unix", Address: path}, nil
}

// ForDial resolves the endpoint the proxy connects to. A full endpoint
// string (from --addr or OP_FORWARD_ADDR) wins; otherwise host and port are
// combined into a TCP endpoint when tcp is set (the TCP shorthand was
// requested), and the default Unix socket is used when it is not.
func ForDial(addr, host string, port int, tcp bool) (Endpoint, error) {
	if addr != "" {
		return Parse(addr)
	}
	if tcp {
		return TCP(host, port), nil
	}
	return DefaultUnix()
}

// ForListen resolves the endpoint the daemon binds. A full endpoint string
// (from --listen or OP_FORWARD_LISTEN) wins; otherwise the daemon binds
// loopback TCP on port when tcp is set, and the default Unix socket when it
// is not. There is intentionally no host parameter: the daemon must never
// be reachable off the machine, so OP_FORWARD_HOST, which names where a
// client dials, is not consulted here.
func ForListen(listen string, port int, tcp bool) (Endpoint, error) {
	if listen != "" {
		return Parse(listen)
	}
	if tcp {
		return TCP(DefaultHost, port), nil
	}
	return DefaultUnix()
}
