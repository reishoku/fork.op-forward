# HTTP protocol

The daemon speaks HTTP/1.1 over a Unix domain socket. This page is the
reference for the wire format. The transport layer it sits on is in
[`transport.md`](transport.md); the auth tokens are in [`tokens.md`](tokens.md);
the security framing is in [`security.md`](security.md).

Source of truth: [`internal/daemon/server.go`](../internal/daemon/server.go),
[`internal/executor/op.go`](../internal/executor/op.go),
[`cmd/proxy.go`](../cmd/proxy.go).

## Endpoints

| Method | Path             | Auth                   | Peer-UID enforced | Body |
|--------|------------------|------------------------|-------------------|------|
| `GET`  | `/health`        | none                   | no                | none |
| `POST` | `/op/execute`    | Bearer access token    | yes               | `executor.Request` JSON |
| `POST` | `/token/refresh` | Bearer refresh token   | no                | none |

The base URL is `http://unix` — the host literal `unix` is a placeholder; the
underlying connection is always the Unix socket dialed by the proxy. See
[`transport.md` § HTTP-over-Unix client](transport.md#http-over-unix-client).

## Common headers

Sent by the proxy on every request:

| Header             | Value                              | Notes |
|--------------------|------------------------------------|-------|
| `Authorization`    | `Bearer <token>`                   | Required for `/op/execute` and `/token/refresh`. |
| `User-Agent`       | `op-forward/<client version>`      | `<client version>` is the value of `cmd.Version` (`dev` for unstamped builds). |
| `X-Client-Version` | `<client version>`                 | Used by the daemon's compatibility check. Same value as `User-Agent`'s suffix. |
| `Content-Type`     | `application/json`                 | Sent on `/op/execute`. Optional but recommended. |

Returned by the daemon when relevant:

| Header                | Value             | Sent on |
|-----------------------|-------------------|---------|
| `Content-Type`        | `application/json` | All JSON responses (success and error). |
| `X-Update-Available`  | server version    | `/op/execute` whenever the client is older than the server. The client's version is *not* below `MinClientVersion`; this is a soft notice. |

The proxy treats `X-Update-Available` as advisory and prints
`op-forward: update available …` to stderr. It does not block.

---

## `GET /health`

Liveness check. No authentication, no peer-UID check, no body.

Request:

```http
GET /health HTTP/1.1
Host: unix
```

Response:

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

Use this when scripting tunnel verification — for example, after starting an
SSH `RemoteForward` you can curl the socket:

```bash
curl --unix-socket "$OP_FORWARD_SOCKET_PATH" http://unix/health
```

---

## `POST /op/execute`

Run an `op` command on the host and stream the result back as a single JSON
blob.

### Request

```http
POST /op/execute HTTP/1.1
Host: unix
Authorization: Bearer <access-token>
Content-Type: application/json
X-Client-Version: 0.3.0

{"args":["item","get","abc123","--fields","username"],"timeout_ms":60000}
```

Body schema (`executor.Request`):

```jsonc
{
  "args":      ["item", "get", "abc123"],   // required, non-empty, length ≤ 64
  "timeout_ms": 60000                        // optional; clamped to executor.MaxTimeout (5 min)
}
```

Validation, performed in this order:

1. Body size ≤ 1 MiB (enforced by `http.MaxBytesReader`); over → `400`.
2. JSON well-formed; if not → `400`.
3. `args` non-empty; ≤ 64 entries; each entry ≤ 4096 bytes; no `\``, `$`, `|`,
   `;`, `&`, `\n`, `\r` characters; if any fail → `200` with
   `exit_code = 1`, `stderr` describing the violation.
4. `args[0]` not in the blocked set (`signin`, `signout`, `update`,
   `completion`); if blocked → same `200` + `exit_code = 1` shape.

### Successful response

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Update-Available: 0.4.0      ; only when client < server

{"stdout":"alice\n","stderr":"","exit_code":0}
```

Body schema (`executor.Result`):

```jsonc
{
  "stdout":    "...",   // captured combined PTY output (op's stdout + stderr)
  "stderr":    "",      // empty on successful exec; populated on validation errors / timeouts
  "exit_code": 0        // op's exit code, 1 on validation rejection, 124 on timeout
}
```

**About `stderr`.** The daemon allocates a single PTY for the spawned `op`,
which merges what `op` writes to fd 1 and fd 2 into one stream. That stream
is returned in `stdout`. The `stderr` field is empty on a successful spawn
and is only used to surface daemon-level failures that do not correspond to
an HTTP error: validation rejections (blocked subcommand, shell metacharacter,
oversize argument) and execution timeouts. The proxy prints `stdout` to its
own stdout and `stderr` to its own stderr.

**About `exit_code`.** Three cases:

| Outcome | `exit_code` | `stderr` |
|---|---|---|
| `op` ran and exited           | op's native exit code | empty |
| Validation rejected the request | `1` | description of the violation |
| Execution timed out (PTY-bound) | `124` (matches `coreutils timeout(1)`) | `command timed out` |

### Error responses

| Status | Body                          | Cause |
|--------|-------------------------------|-------|
| `400 Bad Request` | `invalid request: ...` | Malformed JSON or oversized body. |
| `401 Unauthorized` | `unauthorized` | Missing/invalid/expired access token. The proxy attempts `/token/refresh` automatically. |
| `403 Forbidden` | `forbidden` | Peer UID does not match the daemon's UID. See [`security.md` § Peer-credential check](security.md#2-peer-credential-check). |
| `405 Method Not Allowed` | `method not allowed` | Non-`POST`. |
| `426 Upgrade Required` | `{"error":"client version <c> is below minimum required version <m> — please update with: op-forward update"}` | `X-Client-Version` < `MinClientVersion`. The proxy prints the message and exits `127`. |
| `500 Internal Server Error` | `execution error: ...` | The daemon failed to spawn `op` (PATH lookup failure, exec error, etc.). |

The proxy treats every non-`200`, non-`401`, non-`426` as a generic infra
failure and exits `127` with a stderr message including the status code.

---

## `POST /token/refresh`

Mint a new access token and rotate the refresh token. Authenticated with the
*refresh* token (not the access token).

### Request

```http
POST /token/refresh HTTP/1.1
Host: unix
Authorization: Bearer <refresh-token>
X-Client-Version: 0.3.0
```

No body. The handler ignores any body.

### Successful response

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "access_token":    "abc...64hex",
  "access_expires":  "2026-05-05T19:34:56+09:00",
  "refresh_token":   "def...64hex",
  "refresh_expires": "2026-06-04T18:34:56+09:00"
}
```

Both expiries are RFC3339. The previous refresh token's value is invalid
from this point forward. The new access token is also written to
`session.token` on the host for backward compatibility — see
[`tokens.md` § Daemon startup](tokens.md#daemon-startup).

### Error responses

| Status | Body | Cause |
|---|---|---|
| `401 Unauthorized` | `{"error":"refresh_token_expired","message":"…"}` | The Bearer token did not match the refresh token, or the refresh token is past its 30-day expiry. |
| `401 Unauthorized` | `unauthorized` (text) | No `Authorization` header at all. |
| `405 Method Not Allowed` | `method not allowed` | Non-`POST`. |
| `500 Internal Server Error` | `internal error` | Random number generation for the new token pair failed. Extremely unlikely. |

The descriptive `refresh_token_expired` message is verbose on purpose: the
expected reader is a confused user trying to figure out why their VM cannot
talk to 1Password. The text states explicitly that the issue is *not* a
1Password authentication problem and points at the redeploy fix.

---

## Version negotiation

The daemon evaluates `X-Client-Version` against two boundaries on every
`/op/execute`:

1. **`MinClientVersion`** (default `0.1.0`, defined in
   [`internal/daemon/server.go`](../internal/daemon/server.go)): hard floor.
   Clients below this version receive `426 Upgrade Required`.
2. **Server version** (the daemon's own `cmd.Version`, threaded through
   `daemon.New`): soft notice. Clients below this version receive
   `X-Update-Available: <server version>`.

Special cases:

- Empty `X-Client-Version` (header not sent) → no upgrade enforcement, no
  notice. This is the backward-compat path for callers that pre-date version
  negotiation.
- `X-Client-Version: dev` → same as empty. Local dev builds are never
  rejected.

Comparison is strict semver-like (`major.minor.patch`, integers only).
A leading `v` is stripped. Non-numeric components reduce to `0.0.0`. Source:
[`internal/version/compare.go`](../internal/version/compare.go).

The notice and the hard block are independent — a client below
`MinClientVersion` gets `426` regardless of `X-Update-Available`. The
matching test cases live in
[`internal/daemon/server_test.go`](../internal/daemon/server_test.go) under
`TestExecute_VersionNegotiation_*` and
[`internal/version/compare_test.go`](../internal/version/compare_test.go).

When to bump `MinClientVersion`:

- The previous release had a client-side validation bug that the daemon
  cannot retroactively close.
- The wire protocol changed in a way that an older client would
  *silently* misuse (a structurally incompatible change normally produces
  `400`s, which are loud — that does not need a bump).

---

## Timeouts

Three layers of timeout cooperate. From innermost to outermost:

| Layer | Default | Source |
|---|---|---|
| Proxy probe (Unix dial) | 500 ms | `OP_FORWARD_PROBE_TIMEOUT_MS` |
| Proxy HTTP request total | 60000 ms + 5 s slack | `OP_FORWARD_FETCH_TIMEOUT_MS` |
| Server read timeout | 30 s | `http.Server.ReadTimeout` |
| Server write timeout | `MaxTimeout + 10s` (5 m 10 s) | `http.Server.WriteTimeout` |
| Per-`op` execution | `timeout_ms` from request when `> 0` (capped at 5 m), otherwise `DefaultTimeout` (60 s) | `executor.Execute` |

If `op` exceeds its execution timeout, the daemon kills it via context
cancellation and returns `200` with `exit_code = 124`. Clients should treat
124 as "command timed out" — same convention as `coreutils timeout(1)`.

---

## Sequence diagrams

### Successful execute, valid access token

```
proxy                                    daemon
  |                                        |
  |-- TCP-ish dial via Unix socket ------->|
  |<-- (open) ---------------------------- |
  |                                        |
  |-- POST /op/execute ------------------->|
  |   Authorization: Bearer <access>       |
  |   X-Client-Version: 0.3.0              |
  |   {"args":[...]}                       |
  |                                        | peer-UID OK
  |                                        | bearer matches access token
  |                                        | spawn op under PTY
  |                                        | wait for op to finish
  |<------------------ 200 OK --------------|
  |   {"stdout":..., "stderr":"", "exit_code":0}
  |                                        |
print stdout, exit(0)
```

### Self-healing refresh after access-token expiry

```
proxy                                    daemon
  |-- POST /op/execute  (Bearer access) -->|
  |<------------------ 401 Unauthorized ---|
  |                                        |
  |-- POST /token/refresh (Bearer refresh)>|
  |                                        | mint access' + refresh'
  |                                        | rotate in-memory state
  |                                        | persist both files
  |<--- 200 + {access',refresh',expires} --|
  | save access.token and refresh.token    |
  |                                        |
  |-- POST /op/execute (Bearer access') -->|
  |<------------------ 200 OK --------------|
print stdout, exit(0)
```

### Expired refresh

```
proxy                                    daemon
  |-- POST /op/execute (Bearer access) --->|
  |<------------------ 401 Unauthorized ---|
  |-- POST /token/refresh (Bearer refresh)>|
  |<-- 401 {error:"refresh_token_expired"} |
print actionable message, exit 127
```

Exit `127` is the bash shim's fallback signal — it then `exec`s the real
`op` binary (the one the shim resolved at install time). On a VM with a
locally installed `op`, that fallback runs and may surface a different,
less actionable error from `op` itself. If the actionable redeploy message
is what you want users to see, ensure the VM does not have a usable real
`op` binary on `PATH` outside `~/.local/bin`. See
[`cli.md` § install](cli.md#install) for the shim's resolution logic.

---

## See also

- [`transport.md`](transport.md) — what HTTP runs on top of
- [`tokens.md`](tokens.md) — Bearer token semantics
- [`security.md`](security.md) — argument validation, blocked subcommands,
  redaction
- [`cli.md`](cli.md) — how the proxy and daemon are invoked
- [`internal/daemon/server.go`](../internal/daemon/server.go) — handler implementations
- [`internal/executor/op.go`](../internal/executor/op.go) — request validation and PTY execution
