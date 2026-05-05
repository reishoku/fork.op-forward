# Configuration

Every knob op-forward exposes is an environment variable. There are no
config files. This page is the canonical reference; the README's
configuration table is a summary of the same fields.

Source of truth:
- [`internal/transport/socket.go`](../internal/transport/socket.go) — socket path resolution
- [`internal/auth/token.go`](../internal/auth/token.go) — token directory and file paths
- [`cmd/proxy.go`](../cmd/proxy.go) — proxy-side timeouts
- [`scripts/install.sh`](../scripts/install.sh) — installer behaviour

## Variables

### Socket path

| Variable                        | Used by         | Effect |
|---------------------------------|-----------------|--------|
| `OP_FORWARD_SOCKET_PATH`        | daemon, proxy   | Absolute path to the Unix socket. If set, takes precedence over everything else. |
| `XDG_RUNTIME_DIR`               | daemon, proxy   | If set and `OP_FORWARD_SOCKET_PATH` is unset, the socket path is `$XDG_RUNTIME_DIR/op-forward.sock` (no extra subdirectory). |

Fallback when neither is set: the cache directory from `os.UserCacheDir()`
followed by `/op-forward/op-forward.sock`. Concretely:

| Platform | Default socket path |
|---|---|
| Linux (typical, no XDG)        | `~/.cache/op-forward/op-forward.sock` |
| Linux (with `XDG_CACHE_HOME=…`) | `$XDG_CACHE_HOME/op-forward/op-forward.sock` |
| Linux (with `XDG_RUNTIME_DIR=…`) | `$XDG_RUNTIME_DIR/op-forward.sock` |
| macOS                          | `~/Library/Caches/op-forward/op-forward.sock` |

The path must be **absolute** after `filepath.Clean`. Relative paths are
rejected (`socket path must be absolute`).

The daemon creates the parent directory at mode `0700` and chmods the socket
inode to `0600` on every start. See [`transport.md`](transport.md).

### Tokens

| Variable                  | Used by         | Effect |
|---------------------------|-----------------|--------|
| `OP_FORWARD_TOKEN_DIR`    | daemon, proxy   | Directory holding `access.token`, `refresh.token`, and `session.token`. Used verbatim if set. |
| `XDG_STATE_HOME`          | daemon, proxy   | If set and `OP_FORWARD_TOKEN_DIR` is unset, the directory is `$XDG_STATE_HOME/op-forward`. |
| `OP_FORWARD_TOKEN_FILE`   | daemon, proxy   | Full path override for **`access.token` only**. Does not affect refresh or legacy paths. Backward-compat hook for the single-token era. |

Fallback when none are set: `os.UserCacheDir()/op-forward`. Concretely:

| Platform | Default token directory |
|---|---|
| Linux (typical, no XDG)         | `~/.cache/op-forward` |
| Linux (with `XDG_CACHE_HOME=…`) | `$XDG_CACHE_HOME/op-forward` |
| Linux (with `XDG_STATE_HOME=…`) | `$XDG_STATE_HOME/op-forward` |
| macOS                           | `~/Library/Caches/op-forward` |

Filenames inside the directory are fixed:

| Constant in `internal/auth/token.go` | Filename       |
|--------------------------------------|----------------|
| `auth.AccessTokenFile`               | `access.token`  |
| `auth.RefreshTokenFile`              | `refresh.token` |
| `auth.LegacyTokenFile`               | `session.token` |
| `auth.CacheDirName`                  | `op-forward`    |

For full token semantics see [`tokens.md`](tokens.md).

### Proxy timeouts

| Variable                          | Default     | Effect |
|-----------------------------------|-------------|--------|
| `OP_FORWARD_PROBE_TIMEOUT_MS`     | `500`       | Timeout for the initial Unix-socket dial that the proxy uses to detect a missing/broken tunnel. On expiry the proxy exits `127` so the bash shim can fall back to the real `op` binary. |
| `OP_FORWARD_FETCH_TIMEOUT_MS`     | `60000`     | Total HTTP round-trip budget for the proxy. Also passed as `timeout_ms` in the request body, where it is clamped to `executor.MaxTimeout` (5 minutes). |

Both expect integer milliseconds. Non-integer values are silently ignored
and the default is used. Source: `getProbeTimeoutMs` and `getProxyTimeout`
in [`cmd/proxy.go`](../cmd/proxy.go).

### Installer

| Variable                  | Used by                | Default                | Effect |
|---------------------------|------------------------|------------------------|--------|
| `OP_FORWARD_INSTALL_DIR`  | `scripts/install.sh`   | `$HOME/.local/bin`     | Where the install script drops the `op-forward` binary. |

The shim installer (`op-forward install`) does **not** read this variable. It
hardcodes `~/.local/bin/op` for the shim itself and resolves `op-forward`
relative to the running binary's location. See [`cli.md` § install](cli.md#install).

## Path precedence — at a glance

```
socket path:
  $OP_FORWARD_SOCKET_PATH
    └── (else) $XDG_RUNTIME_DIR/op-forward.sock
        └── (else) <UserCacheDir>/op-forward/op-forward.sock

token directory:
  $OP_FORWARD_TOKEN_DIR
    └── (else) $XDG_STATE_HOME/op-forward
        └── (else) <UserCacheDir>/op-forward

access token file:
  $OP_FORWARD_TOKEN_FILE
    └── (else) <token directory>/access.token

refresh token file:
  <token directory>/refresh.token       # not affected by OP_FORWARD_TOKEN_FILE

legacy token file:
  <token directory>/session.token       # not affected by OP_FORWARD_TOKEN_FILE
```

The asymmetry — `OP_FORWARD_TOKEN_FILE` only affects access — is intentional
and is covered by [`cmd/proxy_test.go`](../cmd/proxy_test.go)
(`TestProxyTokenPathAccessTokenFileOverrideOnlyAppliesToAccess`).

## Worked examples

### Default macOS host

No env vars set. The daemon resolves to:

| | |
|---|---|
| socket path           | `~/Library/Caches/op-forward/op-forward.sock` |
| token directory       | `~/Library/Caches/op-forward` |
| access token file     | `~/Library/Caches/op-forward/access.token` |
| refresh token file    | `~/Library/Caches/op-forward/refresh.token` |
| legacy token file     | `~/Library/Caches/op-forward/session.token` |

The launchd plist generated by `op-forward service install` writes log lines
to `~/Library/Logs/op-forward.log`. See [`deployment.md`](deployment.md).

### Typical Linux VM

`XDG_RUNTIME_DIR=/run/user/1000` set by systemd-logind, `XDG_STATE_HOME`
unset:

| | |
|---|---|
| socket path           | `/run/user/1000/op-forward.sock` |
| token directory       | `~/.cache/op-forward` |
| access token file     | `~/.cache/op-forward/access.token` |

If you SSH `RemoteForward` from the host's `~/Library/Caches/op-forward/op-forward.sock`
to the VM's `/run/user/1000/op-forward.sock`, no further configuration is
needed: both sides pick the right path automatically.

### Custom paths for a CI runner

Setting both is sometimes useful when you want all op-forward state under a
single, easy-to-clean directory:

```bash
export OP_FORWARD_SOCKET_PATH="$RUNNER_TEMP/op-forward/sock"
export OP_FORWARD_TOKEN_DIR="$RUNNER_TEMP/op-forward/state"
```

The socket parent and the token directory are both `MkdirAll`'d at mode
`0700` by their respective consumers, so neither has to exist beforehand.

## Versioning configuration

The client and server version comparisons are not user-configurable at
runtime. The values come from build-time `ldflags`:

```bash
go build -ldflags="-X github.com/reishoku/fork.op-forward/cmd.Version=0.3.0" .
```

Without this flag (e.g. `go build .`) the binary self-reports as `dev`,
which the version-negotiation code treats as "always allow, never advertise
update". See [`protocol.md` § Version negotiation](protocol.md#version-negotiation).

The `Makefile` and the GitHub Actions release workflow set `Version`
correctly. Local `go run .` builds are `dev`.

## Related documents

- [`transport.md`](transport.md) — what the socket path is used for
- [`tokens.md`](tokens.md) — what the token directory holds
- [`deployment.md`](deployment.md) — how to set these in practice
- [`cli.md`](cli.md) — CLI flags vs env vars
