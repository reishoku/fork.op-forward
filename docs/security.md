# Security model

This page documents the threat model, the layers of defence, and what the
project explicitly does **not** protect against. The supporting reference
material is in [`transport.md`](transport.md), [`tokens.md`](tokens.md), and
[`protocol.md`](protocol.md).

## Threat model

op-forward assumes:

- The host machine (running `op-forward serve`) is trusted. If the host is
  compromised, all bets are off — the attacker can already run `op` directly
  with full Touch ID approval.
- The remote machine (the VM) is *less* trusted. The design tolerates a
  mildly compromised remote: token disclosure must not give the attacker
  more than the user already has, and Touch ID must remain the gate for
  every privileged operation.
- The SSH tunnel between the two is secure. SSH key management,
  `known_hosts`, and host-key verification are out of scope.
- Clock skew between host and VM is bounded enough that token expiry checks
  (which are time-based) are not subverted in practice.

The primary security boundary is **Touch ID**: every privileged 1Password
operation triggers a fresh biometric prompt on the host. The proxy cannot
bypass this — Touch ID is enforced by the 1Password desktop app, not by
op-forward itself. op-forward is responsible for *not undermining* the
biometric gate.

## Layers of defence

The defences below are listed in order of how a request hits them.

### 1. Filesystem socket permissions

The daemon's listening socket is a `0600` inode under a `0700` directory. A
process running as a different UID on the same host cannot `connect()` to
the socket — the kernel returns `EACCES` before any byte flows. Details and
implementation in [`transport.md` § Filesystem permissions](transport.md#filesystem-permissions).

### 2. Peer-credential check

After the kernel allows the connection, the daemon reads the peer's UID via
`SO_PEERCRED` (Linux) or `LOCAL_PEERCRED` (macOS) and refuses anything but
its own UID with `403 Forbidden`. Implementation:
[`internal/transport/peercred_*.go`](../internal/transport/) and
[`server.go` `handleExecute`](../internal/daemon/server.go).

This is redundant with the filesystem-mode check above on Linux/macOS — but
not on platforms where peer credentials are unavailable, where the FS check
is the only same-host barrier. Defence in depth.

#### Why `/token/refresh` skips the UID check

Only `/op/execute` enforces the peer-UID match. `/health` is unauthenticated
on purpose and `/token/refresh` is gated by the refresh token, which is itself
a secret. A same-host adversary who somehow held a valid refresh token would
already have escaped the FS-mode boundary. Adding a UID check there would
not increase the bar — it would only make recovery from a forwarded-tunnel
configuration error harder.

If you need to lock `/token/refresh` to the local UID as well, that is a
single-line change in `handleTokenRefresh`. Reach out before doing so; you
will also break the SSH-forwarded refresh path that works today.

### 3. Bearer-token authentication

The `Authorization: Bearer <token>` header is required for `/op/execute`
(access token) and `/token/refresh` (refresh token). Tokens are 32 random
bytes (64 hex chars) from `crypto/rand`. Comparison uses `hmac.Equal` for
constant-time equality, which closes the timing side-channel that a naïve
string `==` would leave.

Token files are mode `0600` and writes go through `os.OpenRoot` (Go's
traversal-resistant API) so a tampered symlink cannot redirect token I/O
outside the configured directory. Full details in [`tokens.md`](tokens.md).

### 4. No shell execution

`op` is launched via `exec.CommandContext(ctx, opPath, req.Args...)` — direct
`execve(2)`, not via `/bin/sh -c`. There is no shell to inject into.
Argument quoting, glob expansion, command substitution, redirection, `$IFS`
tricks — none apply. Source: [`internal/executor/op.go`](../internal/executor/op.go).

### 5. Argument validation

Even though shell injection is structurally impossible, the executor still
rejects suspicious arguments early:

- **Empty args** → error.
- **More than `MaxArgs` (64)** → error.
- **Argument longer than `MaxArgLength` (4096) bytes** → error.
- **Disallowed character set**: any `\``, `$`, `|`, `;`, `&`, `\n`, `\r` in
  any argument fails validation. The `op` CLI uses structured arguments;
  none of these characters appear in legitimate inputs (UUIDs, paths under
  `op://`, item names, vault names, `--flag=value` forms, addresses).

Test coverage: [`internal/executor/op_test.go`](../internal/executor/op_test.go).
The "legitimate args" table there is the canonical list of patterns we
guarantee to *not* reject.

### Blocked subcommands

Four `op` subcommands are refused regardless of arguments:

| Subcommand   | Why blocked |
|--------------|-------------|
| `signin`     | Interactive authentication — must happen on the host with a real terminal. |
| `signout`    | Would clear the host's session that everyone else is sharing. |
| `update`     | Would update the host's `op` binary; out of band of op-forward releases. |
| `completion` | Shell completion script — pointless to forward. |

A blocked subcommand returns HTTP `200` with `exit_code = 1` and an
explanatory `stderr`, not `400`/`403`, because that is what `op` itself does
for argument errors and we want the contract uniform from the proxy's
perspective. Source: `executor.Request.Validate` in
[`internal/executor/op.go`](../internal/executor/op.go).

### Audit logging with redaction

The daemon logs every executed request through `log.Printf("[op-forward]
executing: op %s", sanitizeArgsForLog(args))`. `sanitizeArgsForLog`:

- Redacts the value following `--password` or `-p` (the next argument is
  replaced with `[REDACTED]`).
- Does **not** redact `--reveal` itself (it is a flag, not a value-bearing
  option).

The token in `Authorization` is never logged. The audit line goes to the
launchd `StandardOutPath` / `StandardErrorPath` — by default
`~/Library/Logs/op-forward.log`.

Test coverage: `TestSanitizeArgsForLog` in
[`internal/daemon/server_test.go`](../internal/daemon/server_test.go).

### 6. Touch ID per call

Without intervention, the 1Password CLI caches its biometric approval and
skips Touch ID for follow-up calls within the same session. op-forward
defeats that caching by allocating a *new* pseudo-terminal for every `op`
invocation:

- `cmd.SysProcAttr` sets `Setsid + Setctty + Ctty=0` so the spawned `op`
  becomes its own session leader.
- `pty.Start(cmd)` opens a fresh PTY pair and wires stdin/stdout/stderr to
  the slave.
- The `op` process therefore sees an interactive terminal on every fd, which
  is the heuristic 1Password uses to decide "this is a fresh session, prompt
  again".

Implementation: [`internal/executor/op.go`](../internal/executor/op.go),
[`internal/executor/pty_*.go`](../internal/executor).

This is the mechanism that turns Touch ID into the *primary* security
boundary: even an attacker holding a current access token cannot smuggle
through more than one operation per biometric prompt.

### 7. Path traversal resistance

Token persistence uses `os.OpenRoot(dir)` plus `Root.WriteFile`,
`Root.ReadFile`, and `Root.Rename`. These APIs are documented to resist
symlink traversal and `..` traversal even when the target directory contains
hostile content. The basename is also checked with `filepath.IsLocal` before
the call. Source: `auth.SaveToPath` and `auth.LoadFromPath` in
[`internal/auth/token.go`](../internal/auth/token.go).

Socket paths are validated with `filepath.Clean` and `filepath.IsAbs` —
relative paths are rejected.

### 8. Resource limits

| Limit                          | Value          | Where set |
|--------------------------------|----------------|-----------|
| HTTP request body              | 1 MiB          | `http.MaxBytesReader` in `handleExecute` |
| Read timeout (request line + headers + body) | 30s | `http.Server.ReadTimeout` in `Server.Start` |
| Write timeout                  | 5m 10s         | `http.Server.WriteTimeout` in `Server.Start` |
| Idle (keep-alive) timeout      | 2m             | `http.Server.IdleTimeout` in `Server.Start` |
| Per-`op`-call execution timeout (default) | 60s | `executor.DefaultTimeout` |
| Per-`op`-call execution timeout (max)     | 5m  | `executor.MaxTimeout` (clamps client-supplied values) |
| Argument count                 | 64             | `executor.MaxArgs` |
| Argument byte length           | 4096           | `executor.MaxArgLength` |
| Probe timeout (proxy → daemon) | 500ms (override `OP_FORWARD_PROBE_TIMEOUT_MS`) | `cmd/proxy.go` |
| Fetch timeout (proxy HTTP)     | 60000ms (override `OP_FORWARD_FETCH_TIMEOUT_MS`) | `cmd/proxy.go` |

These are not security guarantees against a determined denial-of-service —
the same-UID assumption already covers that — but they put bounds on the
worst case under buggy clients or stuck `op` calls.

### 9. Version negotiation

The daemon advertises its version and refuses clients below `MinClientVersion`
(default `0.1.0`, currently). Bump it when shipping a release that fixes a
client-side security bug or changes the wire protocol in a way that older
clients would silently misuse. See [`protocol.md` § Version negotiation](protocol.md#version-negotiation).

The current default is permissive on purpose; it is here as a kill switch for
a future security release.

## What this does NOT protect against

- **Compromised VM with a token.** An attacker on the VM who holds a current
  access token can run any non-blocked `op` command — subject to a Touch ID
  prompt on the host. The user must approve. If the user habitually approves
  every prompt without reading, the prompt's value is reduced.
- **Compromised host.** If the host is compromised the attacker can run `op`
  directly. op-forward adds nothing in that scenario.
- **Touch ID misconfiguration.** If 1Password is configured to *not* require
  biometric approval for every `op` invocation (uncommon but possible), the
  daemon will execute requests without any biometric gate. op-forward does
  not enforce 1Password's policy; it only ensures the CLI sees an interactive
  TTY so the policy can fire.
- **SSH tunnel hijack.** If an attacker has the SSH credentials they have
  the tunnel; the bearer token plus peer-cred checks become moot. Use
  hardware-backed SSH keys.
- **Side-channel inference of secrets via `op` output.** The proxy returns
  whatever `op` writes to its PTY. If a downstream tool logs that output, it
  may end up in places you did not intend. Configure your shell history and
  log rotation accordingly.
- **Detection-evasion.** Audit logs go to disk on the host, but op-forward
  does not have a centralized telemetry sink. Add one if your environment
  needs it.

## Hardening checklist

Reasonable opt-in steps for production-ish deployments:

- [ ] 1Password set to require biometric approval per CLI invocation.
- [ ] Host token directory on a filesystem with `noexec,nosuid` if it is
  separate from the OS root.
- [ ] launchd plist not world-readable: the default is `0644`, which is
  fine in single-user contexts but adjust if multiple users share the
  machine.
- [ ] SSH `AllowAgentForwarding no` and `PermitTunnel no` server-wide;
  RemoteForward does not require those.
- [ ] On the VM: `chmod 700` of the parent directory of the
  forwarded-socket path, even though SSH sets it for you in
  `XDG_RUNTIME_DIR`.
- [ ] `MinClientVersion` bumped to the current release before each new
  rollout if the previous release had a client-side bug.

## Security boundaries summary

| Boundary | Enforced by |
|---|---|
| Same-host cross-UID isolation | Filesystem socket permissions (kernel) + peer-cred check (op-forward) |
| Cross-host authentication | SSH (out of scope) + Bearer token (op-forward) |
| Per-operation authorization | Touch ID (1Password) — assumed |
| Argument safety | `os/exec` direct exec + argument validation (op-forward) |
| Long-lived secret hygiene | Two-tier tokens with rotation (op-forward) |

## See also

- [`transport.md`](transport.md) — wire transport details and CWE mapping
- [`tokens.md`](tokens.md) — token storage and rotation
- [`protocol.md`](protocol.md) — HTTP request/response shapes
- [`internal/executor/op.go`](../internal/executor/op.go) — argument validation
- [`internal/daemon/server.go`](../internal/daemon/server.go) — handler logic
