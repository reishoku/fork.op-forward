# CLI reference

The op-forward binary is a single executable with several subcommands.
Routing happens in [`cmd/root.go`](../cmd/root.go); each subcommand lives in
its own file.

## Top-level form

```
op-forward <subcommand> [args...]
```

Aliases for help: `op-forward help`, `op-forward --help`, `op-forward -h`.
With no subcommand, the binary prints usage and exits `0`.

A subcommand the binary does not recognise prints usage and exits `1`.

## Exit codes

Two distinct meanings, depending on which subcommand:

- For `op-forward proxy`: `127` is the **infrastructure failure** sentinel.
  It signals "tunnel down, no token, daemon unreachable, refresh failed".
  The bash shim from `op-forward install` checks this code to decide whether
  to fall back to a real `op` binary. Any other exit code (including `0`) is
  treated as op's own exit code and used as-is.
- For every other subcommand: a non-zero exit code is a normal Go error.

## `serve`

Start the host daemon. Long-running.

```
op-forward serve
```

Behaviour:

1. Migrate `session.token` → `refresh.token` if needed (see
   [`tokens.md` § Daemon startup](tokens.md#daemon-startup)).
2. Load or generate the refresh token.
3. Always generate a fresh access token; write it to `access.token` and to
   `session.token` (for older clients).
4. Resolve the socket path, `MkdirAll` its parent at `0700`, remove any
   stale inode, `Listen("unix", …)`, `Chmod(0600)`.
5. Register `/health`, `/op/execute`, `/token/refresh`.
6. Block in `Server.Serve(ln)`. The process exits when `Serve` returns
   (typically only on `SIGTERM` from `op-forward update`).

The daemon prints token paths and the socket URL to stdout on startup, so
the launchd log file becomes the deployment record.

No flags. Configuration is via environment variables — see
[`configuration.md`](configuration.md).

## `install`

Install the `op` shim on the remote side (VM).

```
op-forward install
```

Behaviour:

- Resolves the **real** `op` binary by walking `$PATH` and skipping
  `~/.local/bin` (so it does not pick up the shim it is about to write).
  Falls back to `/usr/bin/op` if no real `op` is found anywhere on PATH.
- Resolves the running `op-forward` binary's path (preferring `op-forward`
  in the same directory as the planned shim, then `os.Executable()`).
- Writes `~/.local/bin/op` (mode `0755`) with both paths embedded.
- Verifies that `which op` resolves to the new shim; warns otherwise.

The shim itself is short bash:

```bash
#!/bin/bash
REAL_OP="<real op path>"
OP_FORWARD_BIN="<op-forward path>"

if [ -x "$OP_FORWARD_BIN" ]; then
  "$OP_FORWARD_BIN" proxy -- "$@"
  EXIT=$?
  if [ $EXIT -ne 127 ]; then
    exit $EXIT
  fi
fi

if [ -n "$REAL_OP" ] && [ -x "$REAL_OP" ]; then
  exec "$REAL_OP" "$@"
fi

echo "op-forward: proxy unavailable and no fallback op binary" >&2
exit 1
```

The fallback path is what makes the shim safe to install proactively: a VM
without a tunnel still has a working `op` for local commands.

No flags. The shim location is hardcoded; `PATH` ordering is the only knob.

## `proxy`

The per-invocation client. Normally invoked by the bash shim, but also a
fully usable CLI for testing.

```
op-forward proxy [--timeout MILLISECONDS] -- <op-args...>
```

Flags:

| Flag        | Default                              | Effect |
|-------------|--------------------------------------|--------|
| `--timeout` | `OP_FORWARD_FETCH_TIMEOUT_MS` or `60000` | Per-request budget in milliseconds. Forwarded to the daemon as `timeout_ms` and clamped server-side to 5 minutes. |

The `--` separator is not required by Go's `flag` package, but the shim
always passes it so subsequent `op` flags (which start with `--`) do not get
interpreted as proxy flags. Pass it yourself when invoking by hand.

Examples:

```bash
op-forward proxy -- account list
op-forward proxy --timeout 5000 -- item get my-item --fields username
```

Behaviour summary (full description in
[`tokens.md` § Proxy request flow](tokens.md#proxy-request-flow)):

1. Resolve socket path; probe with a Unix dial.
2. Read access token and on-disk expiry.
3. POST `/op/execute` with the access token.
4. On `401`, POST `/token/refresh` with the refresh token; on success retry
   `/op/execute` once with the new access token.
5. Relay the JSON response: print `stdout`, print `stderr`, exit with
   `exit_code`.

Notable status handling:

- `426 Upgrade Required` → print the daemon's message, exit `127`.
- `X-Update-Available` header → emit `op-forward: update available …` to
  stderr and proceed normally.

## `service install` / `service uninstall`

Manage the launchd LaunchAgent for the daemon. macOS only — running on
another OS errors with `service management is only supported on macOS`.

```
op-forward service install
op-forward service uninstall
```

`service install`:

- Creates `~/Library/LaunchAgents/com.op-forward.daemon.plist` with the
  current binary path, the user's home, a static PATH covering both Apple
  Silicon and Intel Homebrew, log files at `~/Library/Logs/op-forward.log`,
  and `RunAtLoad` + `KeepAlive` set to `true`.
- Tries `launchctl unload && launchctl load -w`. If that fails (newer
  macOS), falls back to `launchctl bootout gui/<uid>` then `launchctl
  bootstrap gui/<uid>`.

`service uninstall`:

- Runs `launchctl unload` (best-effort).
- Removes the plist file.

Either command is idempotent.

The launchd label is `com.op-forward.daemon`. To kickstart manually:

```bash
launchctl kickstart -k gui/$(id -u)/com.op-forward.daemon
```

## `update`

Self-update the binary from the latest GitHub release.

```
op-forward update
```

Behaviour:

1. `GET https://api.github.com/repos/reishoku/fork.op-forward/releases/latest`.
2. Compare the returned `tag_name` (with the leading `v` stripped) against
   `cmd.Version`. If equal → "Already up to date", exit `0`.
3. Find the asset named
   `op-forward_<version>_<runtime.GOOS>_<runtime.GOARCH>.tar.gz`. If none
   matches, error with "no release binary found for `<os>/<arch>`".
4. Download the tarball; extract the `op-forward` entry into memory.
5. Write the binary contents to `<exec>.update` and `os.Rename` over the
   live executable. `EvalSymlinks` is used so a Homebrew install sees the
   real path, not the bin shim.
6. `pgrep -f "<binPath> serve"` to find the running daemon. If found,
   `SIGTERM` it. launchd respawns it with the new binary.

Caveats:

- Running this on a Homebrew install desyncs the formula's metadata. Prefer
  `brew upgrade reishoku/tap/op-forward`.
- On the VM (Linux), prefer `apt-get upgrade op-forward` if you installed
  via the APT repo.
- `update` does not verify a signature or checksum; it trusts GitHub's
  TLS connection. The release pipeline does sign the APT repo, so APT users
  benefit from GPG verification automatically.

## `version`

Print the version string the binary was built with.

```
op-forward version
```

Output:

```
op-forward 0.3.0
```

The version comes from `cmd.Version`, set by the build via `-ldflags="-X
github.com/reishoku/fork.op-forward/cmd.Version=<version>"`. Builds without
that flag report `dev`.

## `help`, `--help`, `-h`

Print the usage message and exit `0`.

```
op-forward help
op-forward --help
op-forward -h
```

The usage message is generated by `cmd.printUsage` and includes a one-line
architecture diagram. It is also printed when no subcommand is given.

## See also

- [`architecture.md`](architecture.md) — how these subcommands fit together
- [`protocol.md`](protocol.md) — what `proxy` and `serve` send each other
- [`tokens.md`](tokens.md) — what `serve` writes and `proxy` reads
- [`deployment.md`](deployment.md) — when to call which subcommand
- [`configuration.md`](configuration.md) — every env var these subcommands consult
