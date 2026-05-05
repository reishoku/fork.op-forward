# Transport

This page covers the wire transport: how bytes get from the proxy in the VM to
the daemon on the host. The wire format on top of that transport is HTTP/1.1,
documented in [`protocol.md`](protocol.md).

## Why a Unix domain socket

Until PR #1 in this fork, the daemon listened on `127.0.0.1:18340` (loopback
TCP) and SSH forwarded that port. Loopback TCP has two properties that Unix
sockets fix:

1. **Any local process can `connect()` to a loopback port.** The bearer token
   was the only thing keeping a co-tenant's process out. With a Unix socket
   the kernel checks file permissions on `connect()`; a `0600` socket inode
   under a `0700` directory cannot be opened by any other UID.
2. **Local TCP carries no kernel-verified peer identity.** Unix sockets expose
   the peer's UID via `SO_PEERCRED` (Linux) and `LOCAL_PEERCRED` (macOS), so
   the daemon can refuse same-machine cross-user access even if a token leaks.

These map to CWE-200 (information exposure to an unintended actor) and CWE-306
(missing authentication, defence-in-depth) — see [`security.md`](security.md)
for the full mapping.

## Socket path resolution

The proxy and the daemon agree on the socket path through the same resolution
function: [`transport.SocketPath`](../internal/transport/socket.go).

Precedence, in order:

1. `OP_FORWARD_SOCKET_PATH` — used verbatim if set. Must be absolute.
2. `XDG_RUNTIME_DIR` — if set, the path is `$XDG_RUNTIME_DIR/op-forward.sock`
   (no `op-forward/` subdirectory). Linux distros that ship systemd typically
   set this to `/run/user/<uid>` with the right mode and ownership.
3. `os.UserCacheDir()` — falls through to `<cache>/op-forward/op-forward.sock`.
   On Linux the cache dir respects `XDG_CACHE_HOME` and otherwise defaults to
   `~/.cache`. On macOS it is `~/Library/Caches`.
4. If `os.UserCacheDir()` errors, the resolver tries `~/.cache` directly. If
   even that yields the empty string, it returns an error and the caller exits.

Sanitization rules (`sanitizeSocketPath`):

- Empty → error.
- Cleaned via `filepath.Clean` (collapses `..`, duplicate slashes).
- Must be absolute (`filepath.IsAbs`); a relative path errors out.

The same rules apply on both sides, so as long as the *post-clean* paths match
between VM and host (after SSH `RemoteForward`), the proxy and daemon agree.

## Filesystem permissions

When the daemon starts, [`internal/daemon/server.go`](../internal/daemon/server.go)
makes the chain of permission checks deterministic:

1. `os.MkdirAll(filepath.Dir(socketPath), 0o700)` — parent directory `0700`.
2. `os.Remove(socketPath)` — clears any stale inode left by a previous run.
3. `net.Listen("unix", socketPath)` — binds.
4. `os.Chmod(socketPath, 0o600)` — narrows the inode mode.

Net effect: only the owning UID can `open()` the directory or the socket file.
A different UID's `connect()` fails with `EACCES` before any bytes flow.

The `0600` step is explicit because `net.Listen` honours the process `umask`
when creating the inode, and the default `umask` of `022` would otherwise
yield `0644`. Do not rely on inheriting the directory mode — the kernel does
not.

## Peer credential enforcement

Even with the filesystem checks above, the daemon verifies the peer's UID on
every connection. `Server.Start` registers a `ConnContext` hook on the
`http.Server`:

```go
server.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
    if uc, ok := c.(*net.UnixConn); ok {
        if uid, err := transport.PeerUID(uc); err == nil && uid >= 0 {
            ctx = context.WithValue(ctx, peerUIDKey{}, uid)
        }
    }
    return ctx
}
```

`PeerUID` is platform-specific:

| Platform | Mechanism | File |
|---|---|---|
| Linux   | `getsockopt(SOL_SOCKET, SO_PEERCRED)` returning `Ucred{Uid,…}`     | [`peercred_linux.go`](../internal/transport/peercred_linux.go) |
| macOS   | `getsockopt(SOL_LOCAL, LOCAL_PEERCRED)` returning `Xucred{Uid,…}`  | [`peercred_darwin.go`](../internal/transport/peercred_darwin.go) |
| other   | returns `-1, nil`                                                  | [`peercred_other.go`](../internal/transport/peercred_other.go) |

The handler for `/op/execute` rejects mismatches:

```go
if uid := peerUIDFromRequest(r); uid >= 0 {
    if uid != os.Getuid() {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }
}
```

A few subtleties:

- A `uid < 0` is treated as "credentials unavailable" and the check is
  skipped. This happens on platforms without peer-cred support and (in
  principle) when the SSH forwarder rebinds across user boundaries in ways
  Linux/macOS don't expose. The Bearer token is still required.
- Only `/op/execute` enforces the UID match. `/health` is unauthenticated by
  design, and `/token/refresh` relies on the refresh token being a secret —
  see [`security.md` § Why /token/refresh skips the UID check](security.md#why-tokenrefresh-skips-the-uid-check).
- The check is done once per *connection*, not per *request*. For a Unix
  socket that distinction is irrelevant because the kernel ties credentials
  to the socket pair at `connect()` time.

## SSH `RemoteForward` of Unix sockets

OpenSSH 6.7+ supports forwarding Unix sockets (`man ssh.1`, `-R` syntax). The
daemon listens on a host-side socket; the proxy dials a remote-side socket
that SSH ties to that host-side socket.

Recommended pattern:

```bash
remote_socket="$HOME/.cache/op-forward/op-forward.sock"
ssh -fN -R "$remote_socket:$HOME/Library/Caches/op-forward/op-forward.sock" \
    -o ControlMaster=no \
    -o ControlPath=none \
    user@host
export OP_FORWARD_SOCKET_PATH="$remote_socket"
```

Notes:

- The two paths in `-R remote:host` are absolute on their respective
  filesystems. Using `$HOME` resolves on each side separately.
- `OP_FORWARD_SOCKET_PATH` on the remote must equal the *remote-side* path you
  passed to `-R`.
- `ControlMaster=no` plus `ControlPath=none` disables SSH connection
  multiplexing. With multiplexing, only the *first* SSH connection sets up
  `RemoteForward`; later connections share the master channel and do **not**
  re-establish forwarding. A dedicated tunnel connection avoids this surprise
  entirely. See [`deployment.md` § Multiplexing pitfall](deployment.md#multiplexing-pitfall).
- `-fN` puts the tunnel in the background and runs no remote command, so the
  connection lives until you kill it.

## HTTP-over-Unix client

Standard `net/http` clients dial TCP. The proxy rebinds the dialer:

```go
transportRT := &http.Transport{
    DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
        return (&net.Dialer{Timeout: 1 * time.Second}).DialContext(ctx, "unix", socketPath)
    },
}
client := &http.Client{Timeout: httpTimeout, Transport: transportRT}
```

Requests use a synthetic `Host` header — the proxy POSTs to URLs of the form
`http://unix/op/execute` and `http://unix/token/refresh`. The host literal
`unix` is arbitrary and only there because the URL parser requires *something*.

The proxy does a separate dial-and-close probe before constructing the HTTP
client. This catches "tunnel down" failures without sending an authenticated
request and lets the proxy exit `127` (so the bash shim can fall back to a
real `op`) without spending a Touch ID approval. The probe timeout is
`OP_FORWARD_PROBE_TIMEOUT_MS` (default 500 ms).

## Connection lifecycle

The daemon's HTTP server uses these timeouts (set in `Server.Start`):

| Timeout       | Value                    | Why |
|---------------|--------------------------|-----|
| `ReadTimeout`  | `30 * time.Second`        | Caps slow request bodies. The body is also bounded to 1 MiB by `http.MaxBytesReader`. |
| `WriteTimeout` | `executor.MaxTimeout + 10s` (`5m10s`) | Long enough to outlast a maximum-length `op` invocation plus framing overhead. |
| `IdleTimeout`  | `120 * time.Second`       | Keep-alive cap; SSH-forwarded sockets reuse connections across short bursts of `op` calls. |

The PTY-spawned `op` itself is bounded by `executor.MaxTimeout = 5m`. A
client-supplied `timeout_ms` shorter than that is honored; a longer one is
clamped down. See [`protocol.md` § Timeouts](protocol.md#timeouts).

## CWE alignment

The original design note that became this page mapped the changes to CWE
identifiers:

| CWE | Issue | Mitigation |
|---|---|---|
| CWE-22  | Path traversal | Token persistence uses `os.Root` (`os.OpenRoot` + `Root.WriteFile`/`Root.Rename`). Socket paths are `Clean`ed and must be absolute. |
| CWE-200 | Information exposure to unintended actor | Filesystem-mode socket; only same-UID can connect. |
| CWE-284 | Improper access control | Mode `0700`/`0600` plus peer-UID check. |
| CWE-306 | Missing authentication | Bearer token retained on top of FS controls. |
| CWE-400 | Uncontrolled resource consumption | `MaxBytesReader(1 MiB)`, request/idle timeouts, PTY-output bounded by `executor.MaxTimeout`. |
| CWE-922 | Insecure storage | `0600` token files via atomic temp-rename through `os.Root`. |

## See also

- [`internal/transport/socket.go`](../internal/transport/socket.go),
  [`peercred_linux.go`](../internal/transport/peercred_linux.go),
  [`peercred_darwin.go`](../internal/transport/peercred_darwin.go),
  [`peercred_other.go`](../internal/transport/peercred_other.go)
- [`protocol.md`](protocol.md) — what flows on top of the socket
- [`tokens.md`](tokens.md) — what's in the `Authorization` header
- [`deployment.md`](deployment.md) — concrete SSH commands

External references:

- Go `net` package, Unix-domain support — <https://pkg.go.dev/net>
- Go `os.Root` traversal-resistant API — <https://pkg.go.dev/os#Root>
- OpenSSH `ssh(1)` man page (`-R` Unix-socket syntax) — <https://man.openbsd.org/ssh.1>
- Linux `unix(7)` and `SO_PEERCRED` — <https://man7.org/linux/man-pages/man7/unix.7.html>
