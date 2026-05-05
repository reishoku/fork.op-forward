# Token model

This page documents the access/refresh token model added in v0.3.0 and the
on-disk format the proxy and daemon agree on. The HTTP shape of the refresh
endpoint lives in [`protocol.md` § /token/refresh](protocol.md#post-tokenrefresh);
the security framing lives in [`security.md`](security.md).

Source of truth: [`internal/auth/token.go`](../internal/auth/token.go).

## Two-tier design

op-forward stores two distinct tokens:

| Name            | Filename          | TTL                              | Lifetime characteristics |
|-----------------|-------------------|----------------------------------|--------------------------|
| Access token    | `access.token`    | 1 hour (`AccessTokenTTL`)        | Regenerated on every `op-forward serve` startup. Used for `/op/execute`. |
| Refresh token   | `refresh.token`   | 30 days sliding (`RefreshTokenTTL`) | Persists across daemon restarts. Used to mint new access tokens via `/token/refresh`. Rotated to a new value on every successful refresh. |
| Legacy token    | `session.token`   | (mirrors access TTL)             | Kept in sync with `access.token` for backward compatibility with old proxy clients. Migrated to `refresh.token` once on first run via `auth.MigrateLegacyToken`. |

Why two? A single 30-day token used directly for execution would mean any leak
gives the attacker a month of access. Splitting into a short-lived access
token plus a long-lived refresh token keeps the high-value secret out of the
hot path: only `/token/refresh` ever sees the refresh token, and a successful
refresh rotates it.

## Format on disk

Each token file is two lines, written atomically:

```
<64-hex-character-token-value>
<RFC3339 expiry timestamp>
```

Example:

```
6f7d9c3b1e0a4d2f8e5b1a0c9f7e6d4b2a1c8f5d3b7e9a0c1d2e4f5a6b7c8d9e
2026-12-04T12:34:56+09:00
```

Properties:

- 32 random bytes from `crypto/rand`, hex-encoded → 64 ASCII chars
  (`auth.TokenLength = 32`).
- Mode `0600`, written as `<basename>.tmp` then `Rename`d into place.
- Both write and read go through `os.OpenRoot(dir)` so symlinks pointing
  outside `dir` and `..` components in the basename are refused. This is the
  CWE-22 mitigation referenced in [`security.md`](security.md).

## Directory and file precedence

Resolution lives in `auth.TokenDir` and the `*Path` helpers.

**Token directory** (`auth.TokenDir`):

1. `OP_FORWARD_TOKEN_DIR` — used verbatim, just `filepath.Clean`ed.
2. `XDG_STATE_HOME` — yields `$XDG_STATE_HOME/op-forward`.
3. `os.UserCacheDir()` — yields `<cache>/op-forward`. Linux falls back to
   `~/.cache`; macOS uses `~/Library/Caches`.

**Per-file paths**:

| File              | Function              | Notes |
|-------------------|-----------------------|-------|
| `access.token`    | `auth.AccessTokenPath`  | Honors `OP_FORWARD_TOKEN_FILE` as a *full path* override. |
| `refresh.token`   | `auth.RefreshTokenPath` | **Does not** honor `OP_FORWARD_TOKEN_FILE`. Always inside `TokenDir()`. |
| `session.token`   | `auth.LegacyTokenPath`  | Same — always inside `TokenDir()`. |

The `OP_FORWARD_TOKEN_FILE` override is a backward-compat hook for the
single-token era. It only redirects the access token; covered by
[`cmd/proxy_test.go`](../cmd/proxy_test.go).

For the full table see [`configuration.md` § Tokens](configuration.md#tokens).

## Daemon startup

`runServe` ([`cmd/serve.go`](../cmd/serve.go)) does the following on every
start:

1. **Migrate legacy.** `auth.MigrateLegacyToken` looks for `session.token`. If
   `refresh.token` does not yet exist *and* the legacy file is still valid, it
   copies the legacy value into `refresh.token` (with `RefreshTokenTTL`) so
   users upgrading from the single-token era keep working without
   redeployment. If `refresh.token` already exists, it is left alone — the
   legacy file is never allowed to overwrite a current refresh token.
2. **Load or generate refresh.** `auth.LoadOrGenerateRefresh` reads
   `refresh.token` and, if it parses and is unexpired, reuses it. Otherwise a
   fresh 30-day token is minted and written.
3. **Generate a new access token.** `auth.GenerateAccess` always produces a
   fresh access token on startup; it is not loaded from disk. The new token
   is written to `access.token`.
4. **Mirror to legacy.** The same access value is also written to
   `session.token` so older proxy clients that still read the legacy path keep
   working until upgraded.

The daemon then constructs `Server` with both tokens and calls `Start`.

## Proxy request flow

[`cmd/proxy.go`](../cmd/proxy.go) handles the access/refresh dance:

1. Read the *value* and the *on-disk expiry* of `access.token`. If missing,
   try `session.token`. If both are missing, error out with a "no token"
   message and exit `127`.
2. Probe the Unix socket. If it does not respond, exit `127` (lets the bash
   shim fall back to a real `op`).
3. If the access token's on-disk expiry is in the past, skip directly to step
   5. This avoids burning a round-trip and a Touch ID prompt just to discover
   what we already know.
4. Otherwise, send `POST /op/execute` with `Authorization: Bearer <access>`.
   On `200`, relay output and exit. On `401`, fall through.
5. Read `refresh.token` (falling back to `session.token`). If neither exists,
   exit `127` with the "no token" message.
6. Send `POST /token/refresh` with `Authorization: Bearer <refresh>`. On
   `200`, parse the new pair, persist both files atomically, retry step 4
   once with the new access token. On non-`200`, print an actionable error
   and exit `127`.

The retry happens *once*. A second `401` after a successful refresh is
treated as a hard failure rather than a refresh loop.

## Refresh endpoint behaviour

[`Server.handleTokenRefresh`](../internal/daemon/server.go):

- Method must be `POST`; otherwise `405`.
- `Authorization` must carry a Bearer token; otherwise `401`.
- The Bearer value is compared against the in-memory refresh token using
  `hmac.Equal` (constant-time). The refresh token must also be unexpired.
- On mismatch or expiry, returns `401` with body
  ```json
  { "error": "refresh_token_expired",
    "message": "op-forward: authentication expired — …" }
  ```
  The message text is verbose on purpose: it tells the user the failure is
  *not* a 1Password authentication problem and points at the redeploy fix.
- On success:
  - Mints a new access token (1 hour TTL).
  - Mints a *new* refresh token (30 days TTL) — the old refresh token's value
    is no longer accepted from this point.
  - Persists both files via `auth.SaveToPath`.
  - Mirrors the new access token into `session.token` for old clients.
  - Atomically swaps the in-memory tokens under `Server.mu` so concurrent
    `/op/execute` requests see the new access token immediately.
  - Returns 200 with body
    ```json
    { "access_token": "...", "access_expires": "RFC3339",
      "refresh_token": "...", "refresh_expires": "RFC3339" }
    ```

The mutex (`Server.mu`) is held for the duration of validation, generation,
disk write, and the in-memory swap. This makes refresh atomic with respect to
authentication checks, but it also means a slow disk write briefly serializes
incoming `/op/execute` calls. In practice the disk write is microseconds.

## Sliding expiry

The phrase "sliding 30-day expiry" means: as long as the refresh token is
used at least once every 30 days, you stay authenticated. The mechanism is
*rotation*: each successful `/token/refresh` returns a brand-new refresh
token whose own 30-day window starts now.

This is intentionally different from `Token.Renew()`, which extends the
existing token's expiry in place. `Renew()` exists in the API but is not
called by the daemon's `/token/refresh` path. The daemon always rotates;
`Renew()` is retained for callers that want pre-emptive in-place renewal
without changing the token value (currently none in tree).

`Token.ShouldRenew` returns true when less than half the TTL is remaining
(`RenewalFactor = 0.5`). It is also unused in the production code path
today; the proxy is purely 401-driven. Treat both as future-API hooks.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `op-forward: no authentication token found.` | Neither `access.token` / `session.token` nor `refresh.token` exists on the VM. | Re-run your token deployment script. The error message includes the canonical `scp` command. |
| `op-forward: token refresh failed — refresh_token_expired` | Refresh token has not been used in over 30 days *or* the daemon was restarted with a new refresh token while the VM still holds the old one. | Re-deploy `refresh.token` from the host. Restarting the daemon does **not** invalidate an existing refresh token, but generating a new one from scratch (e.g. the host token directory was wiped) does. |
| `401 Unauthorized` on `/op/execute` followed by another `401` after refresh | Clock skew between host and VM, or the refresh token is good but the access token write raced with the retry. | Inspect `access.token` content vs the expected expiry. The most common cause is a stale token cached in `session.token` taking precedence — delete `session.token` so the proxy falls back to `access.token`. |
| `tunnel not available` on `op-forward proxy` | The Unix socket forwarding is not in place. | See [`deployment.md` § Troubleshooting](deployment.md#troubleshooting). |
| Old client still logs in fine even after access token expires | The `session.token` mirror is still being written by the daemon. The path is intentional. Update the client to pick up the two-tier flow when convenient. | n/a (compat path). |

## Security boundaries

- A token file with mode other than `0600` should be treated as compromised —
  rotate by restarting the daemon (regenerates access; preserves refresh) or
  by deleting `refresh.token` (forces a full re-mint on next start).
- Tokens are never logged. The daemon's audit log redacts `--password` /
  `-p` *values* but the token itself only appears in the `Authorization`
  header, which is not logged.
- The proxy's local cache writes (`saveTokenFile` in `cmd/proxy.go`) use a
  `.tmp` + `Rename` pattern with mode `0600`. They do not go through
  `os.Root` because the proxy is the simpler client side and only writes to
  files it just resolved itself.

## Test coverage

Token behaviour is covered by:

- [`internal/auth/token_test.go`](../internal/auth/token_test.go) — generation,
  TTL, save/load, atomic write, env-var overrides, legacy migration, refresh
  generation.
- [`internal/daemon/server_test.go`](../internal/daemon/server_test.go) — the
  refresh endpoint, including: expired refresh, wrong refresh, refresh-token
  rejected for `/op/execute`, sliding-expiry rotation.
- [`cmd/proxy_test.go`](../cmd/proxy_test.go) — `OP_FORWARD_TOKEN_FILE`
  override semantics (access only).

## See also

- [`protocol.md` § /token/refresh](protocol.md#post-tokenrefresh) — wire format
- [`security.md` § Bearer-token authentication](security.md#3-bearer-token-authentication)
- [`configuration.md` § Tokens](configuration.md#tokens)
- [`deployment.md` § Token deployment](deployment.md#3-token-deployment)
