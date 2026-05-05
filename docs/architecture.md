# Architecture

This page maps the parts of op-forward to their source files and explains why
each piece exists. For deeper coverage of any subsystem, follow the links into
the rest of the documentation.

See also: [`transport.md`](transport.md), [`tokens.md`](tokens.md),
[`security.md`](security.md), [`protocol.md`](protocol.md).

## The problem op-forward solves

The 1Password CLI (`op`) needs to talk to the local 1Password desktop app to
unlock the vault — that path is what triggers Touch ID. Inside a remote VM,
container, or SSH session, the desktop integration is unavailable, so `op`
commands fail with no biometric chain to walk.

op-forward intercepts every `op` invocation on the remote side and runs the
real `op` command on the host instead, where the desktop app can prompt for
Touch ID.

## Components at a glance

```
host (macOS, where Touch ID works)            remote (Linux VM)
─────────────────────────────────────         ─────────────────────────────────
op-forward serve                              op (the shim from cmd/install.go)
  ├─ /health                                    └─ exec op-forward proxy -- "$@"
  ├─ /op/execute  (PTY-spawned op)                    │
  └─ /token/refresh                                   │ HTTP-over-Unix-socket
       ▲                                              ▼
       │                                         op-forward proxy
       │  ssh -R remote.sock:host.sock ─────────  net.Dial("unix", ...)
       │                                         http://unix/op/execute
1Password app + Touch ID
```

The host runs a long-lived daemon (`op-forward serve`). The remote runs no
daemon at all — every `op` invocation forks a short-lived `op-forward proxy`
process that sends one HTTP request and exits.

## Process layout

### Host (macOS)

| Process | Source | Lifetime | Purpose |
|---|---|---|---|
| `op-forward serve` | [`cmd/serve.go`](../cmd/serve.go) → [`internal/daemon/server.go`](../internal/daemon/server.go) | Long-running (launchd `KeepAlive`) | Accept Unix-socket HTTP requests, run `op` under a PTY, return stdout/stderr/exit |
| `op` (real binary) | external | Per-request | The actual 1Password CLI; spawned with a fresh pseudo-terminal so each call triggers Touch ID |
| 1Password desktop | external | Always running | Holds the unlocked vault, surfaces Touch ID prompts |
| launchd | system | — | Restarts the daemon on crash and after `op-forward update` |

### Remote (VM)

| Process | Source | Lifetime | Purpose |
|---|---|---|---|
| `op` (the shim) | [`cmd/install.go`](../cmd/install.go) | Per invocation | Bash wrapper at `~/.local/bin/op` that delegates to `op-forward proxy` |
| `op-forward proxy` | [`cmd/proxy.go`](../cmd/proxy.go) | Per invocation | Reads tokens, dials the host socket via SSH-forwarded path, sends one HTTP request, relays output and exit code |
| sshd | external | Long-running | Hosts the `RemoteForward`-mapped Unix socket from the host |

The shim is a bash script so it can fall back to a real `op` binary if the
proxy infrastructure is unavailable. See [`cli.md` § install](cli.md#install).

## Request flow

A typical `op item get <uuid>` from inside a VM:

1. The shell resolves `op` to `~/.local/bin/op` (the shim).
2. The shim execs `op-forward proxy -- item get <uuid>`.
3. The proxy resolves the socket path with `transport.SocketPath()` (see
   [`configuration.md` § Socket path](configuration.md#socket-path)).
4. The proxy probes the socket with `net.DialTimeout("unix", ...)` (timeout =
   `OP_FORWARD_PROBE_TIMEOUT_MS`, default `500`). On failure it exits **127**
   so the shim can fall back to the real `op` binary.
5. The proxy reads `access.token` (or `session.token` as fallback). If the
   on-disk expiry is in the past, it skips straight to step 9.
6. The proxy POSTs to `http://unix/op/execute` with `Authorization: Bearer
   <access>`, `X-Client-Version: <version>`, body `{"args":[…],"timeout_ms":…}`.
   The HTTP transport's `DialContext` is rebound to dial the Unix socket
   (see [`transport.md` § HTTP-over-Unix client](transport.md#http-over-unix-client)).
7. SSH `RemoteForward` shuttles the bytes from the VM-side socket inode to the
   host-side socket inode.
8. The daemon's `ConnContext` extracts the peer UID via `SO_PEERCRED` (Linux)
   or `LOCAL_PEERCRED` (macOS); a UID mismatch returns `403 Forbidden`. The
   handler then verifies the Bearer token in constant time. If it matches the
   access token and is unexpired, execution proceeds.
9. On `401`, the proxy re-reads `refresh.token` and POSTs to `/token/refresh`.
   On `200`, it persists both new tokens to disk and retries `/op/execute`
   once. On non-`200`, it prints an actionable error and exits **127**.
10. The daemon validates the request body (max args, max arg length, blocked
    subcommands, shell metacharacters — see
    [`security.md` § Argument validation](security.md#5-argument-validation)),
    then `pty.Start`s `op` with a fresh PTY so 1Password sees an interactive
    session and prompts Touch ID.
11. The user approves on the host. The daemon collects PTY output, returns
    `{"stdout":…,"stderr":"","exit_code":…}`. The proxy prints `stdout` and
    exits with `exit_code`.

The Touch ID prompt is the one piece of this flow that is *not* automated. It
is the entire point of the design — every privileged `op` operation triggers a
fresh biometric prompt because the daemon allocates a new PTY for every call.
See [`security.md` § Touch ID per call](security.md#6-touch-id-per-call).

## Where each subsystem lives

### `cmd/`
Subcommand routing and per-command logic. `cmd/root.go` does the dispatch.

- [`cmd/serve.go`](../cmd/serve.go) — startup. Migrates legacy `session.token`,
  loads or generates the refresh token, mints a fresh access token on every
  start, resolves the socket path, then calls `daemon.New(...).Start()`.
- [`cmd/install.go`](../cmd/install.go) — writes the bash shim to
  `~/.local/bin/op`. The shim's `REAL_OP` fallback is the first `op` on `$PATH`
  that is not the shim's own directory; if none exists, falls back to
  `/usr/bin/op`.
- [`cmd/proxy.go`](../cmd/proxy.go) — the per-invocation client. Implements
  the access/refresh dance described in [`tokens.md`](tokens.md).
- [`cmd/service.go`](../cmd/service.go) — installs/uninstalls the launchd
  LaunchAgent (`com.op-forward.daemon`). macOS only; errors on other platforms.
- [`cmd/update.go`](../cmd/update.go) — fetches the latest GitHub release,
  atomically replaces the running binary, then `SIGTERM`s the daemon so
  launchd respawns it with the new code.

### `internal/auth/`
Token model and persistence. See [`tokens.md`](tokens.md) for the full story.

- 32-byte random tokens, stored as 64 hex chars.
- Two TTLs: `AccessTokenTTL = 1h`, `RefreshTokenTTL = 30d`.
- Persistence uses `os.OpenRoot` + `Root.WriteFile`/`Root.Rename`, constraining
  all token I/O to the resolved token directory even in the presence of
  symlinks or `..` components (CWE-22 mitigation).

### `internal/daemon/`
HTTP server. Three handlers:

- `GET  /health`         — unauthenticated liveness check.
- `POST /op/execute`     — peer-UID-checked, access-token-authenticated.
- `POST /token/refresh`  — refresh-token-authenticated, returns a rotated pair.

See [`protocol.md`](protocol.md) for the wire format.

The server uses a `ConnContext` hook to attach the peer UID to each request's
context; handlers read it back via the unexported `peerUIDKey{}` type. This
avoids per-handler syscalls.

### `internal/transport/`
Socket path resolution and platform-specific peer-credential extraction.

- [`socket.go`](../internal/transport/socket.go) — `SocketPath()` precedence
  and absolute-path validation.
- [`peercred_linux.go`](../internal/transport/peercred_linux.go) — `SO_PEERCRED`.
- [`peercred_darwin.go`](../internal/transport/peercred_darwin.go) — `LOCAL_PEERCRED`.
- [`peercred_other.go`](../internal/transport/peercred_other.go) — fallback
  returning `-1`. The daemon treats `< 0` as "skip the check"; on platforms
  without peer-cred support the bearer token is the only authentication.

### `internal/executor/`
- [`op.go`](../internal/executor/op.go) — request validation and PTY-based
  execution. `MaxArgs = 64`, `MaxArgLength = 4096`, `DefaultTimeout = 60s`,
  `MaxTimeout = 5m`.
- [`pty_darwin.go`](../internal/executor/pty_darwin.go),
  [`pty_linux.go`](../internal/executor/pty_linux.go) — `Setsid + Setctty +
  Ctty=0` so the spawned `op` becomes its own session leader, plus an `OPOST`
  clear so the line discipline does not rewrite `\n` to `\r\n` and corrupt the
  output.

### `internal/version/`
Semantic version comparison and the `CheckCompatibility` function the daemon
uses to decide whether to set `X-Update-Available` or return `426 Upgrade
Required`. See [`protocol.md` § Version negotiation](protocol.md#version-negotiation).

## Why this shape

A few decisions that may not be obvious from the code:

- **Per-call subprocess on the remote.** Running a long-lived agent on the VM
  would mean another moving part to keep alive across SSH disconnects and
  another place to leak tokens. The shim+proxy pattern is stateless on the
  remote.
- **HTTP over a Unix socket, not a custom protocol.** Using `net/http` keeps
  framing, timeouts, status codes, and observability for free. The socket file
  with `0600` permissions is the access boundary; HTTP is just the wire format.
- **PTY allocation per call.** 1Password caches a single biometric approval
  for non-interactive `op` calls. Allocating a fresh pseudo-terminal makes
  every call look interactive, so Touch ID prompts every time. See the
  comment block in [`internal/executor/op.go`](../internal/executor/op.go).
- **Two-tier tokens.** A short-lived access token limits exposure if it leaks;
  a long-lived sliding refresh token avoids forcing daily redeploys. See
  [`tokens.md`](tokens.md) for the trade-offs.
- **Bash shim, not a Go binary at `op`.** The shim needs to be unconditionally
  exec-able on the user's PATH and able to fall back when the proxy itself is
  broken — bash is the simplest dependency that already exists on every VM.

## Cross-references

- Wire protocol: [`protocol.md`](protocol.md)
- Transport details: [`transport.md`](transport.md)
- Token model: [`tokens.md`](tokens.md)
- Threat model: [`security.md`](security.md)
- CLI reference: [`cli.md`](cli.md)
- Configuration: [`configuration.md`](configuration.md)
- Deployment: [`deployment.md`](deployment.md)
- Build & release: [`development.md`](development.md)
