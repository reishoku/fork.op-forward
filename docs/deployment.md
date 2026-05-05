# Deployment

This page is the operational guide. It walks through installing op-forward on
both sides, wiring up the SSH tunnel, and validating the result. The
quickstart in [`../README.md`](../README.md) is the abridged version of this
page; everything here is meant to compose with the quickstart, not replace
it.

For why the design looks like this, see [`architecture.md`](architecture.md).
For the security implications of each step, see [`security.md`](security.md).

## Host (macOS)

### 1. Install the binary

Pick one. They produce the same binary; choose based on how you maintain the
rest of the host.

- **Homebrew tap**:

  ```bash
  brew install reishoku/tap/op-forward
  ```

  Subsequent updates with `brew upgrade reishoku/tap/op-forward`. The tap is
  refreshed automatically on each release; see
  [`development.md` § Release pipeline](development.md#release-pipeline).

- **Install script**:

  ```bash
  curl -fsSL https://raw.githubusercontent.com/reishoku/fork.op-forward/reishoku/scripts/install.sh | sh
  ```

  Drops `op-forward` into `$OP_FORWARD_INSTALL_DIR` (default
  `~/.local/bin`). See [`configuration.md` § Installer](configuration.md#installer).

- **From source**:

  ```bash
  git clone https://github.com/reishoku/fork.op-forward.git
  cd fork.op-forward
  go build -ldflags="-s -w -X github.com/reishoku/fork.op-forward/cmd.Version=$(git describe --tags --always)" -o op-forward .
  install op-forward ~/.local/bin/op-forward
  ```

  Without the `-X …Version=…` flag the binary self-reports as `dev`, which
  exempts it from version-negotiation enforcement. See
  [`protocol.md` § Version negotiation](protocol.md#version-negotiation).

### 2. Start the daemon

For interactive testing:

```bash
op-forward serve
```

Expected output, in order:

```
Refresh token generated (no valid existing token found)        # first run only
Refresh token at: /Users/you/Library/Caches/op-forward/refresh.token
Access token written to: /Users/you/Library/Caches/op-forward/access.token (expires …)
Starting daemon on unix:///Users/you/Library/Caches/op-forward/op-forward.sock
op-forward daemon listening on unix:///Users/you/Library/Caches/op-forward/op-forward.sock
```

The first time you run it, both `refresh.token` and `access.token` are
created. On every subsequent start, the access token is regenerated and the
refresh token is reused as long as it is unexpired. See
[`tokens.md` § Daemon startup](tokens.md#daemon-startup).

### 3. Persist via launchd

For long-running use:

```bash
op-forward service install
```

This writes `~/Library/LaunchAgents/com.op-forward.daemon.plist` and loads
it. The plist:

- `Label` = `com.op-forward.daemon`
- `ProgramArguments` = the resolved binary path + `serve`
- `EnvironmentVariables.PATH` =
  `/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin` —
  needed so the daemon's `exec.LookPath("op")` finds the real `op` binary on
  both Apple Silicon and Intel macs.
- `EnvironmentVariables.HOME` = the current user's home — so the cache and
  token directories resolve correctly under launchd's reduced env.
- `RunAtLoad`, `KeepAlive` = `true` — auto-starts at login and respawns on
  crash.
- `StandardOutPath` and `StandardErrorPath` = `~/Library/Logs/op-forward.log`.

To uninstall:

```bash
op-forward service uninstall
```

`service install` and `service uninstall` are macOS-only. On other platforms
they error out — there is no built-in systemd equivalent yet.

If `launchctl load -w` fails (newer macOS versions return `Bootstrap failed:
5`), the command falls back to `launchctl bootstrap gui/<uid> <plist>`. The
binary path stored in the plist is `EvalSymlinks`-resolved, so a Homebrew
upgrade that swaps the symlink target keeps the plist valid as long as the
canonical path remains.

## Remote (Linux VM)

### 1. Install the binary

- **APT (Ubuntu/Debian)** — recommended for managed VMs:

  ```bash
  curl -fsSL https://reishoku.github.io/fork.op-forward/key.gpg \
    | sudo gpg --dearmor -o /usr/share/keyrings/op-forward.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/op-forward.gpg] https://reishoku.github.io/fork.op-forward stable main" \
    | sudo tee /etc/apt/sources.list.d/op-forward.list
  sudo apt-get update
  sudo apt-get install op-forward
  ```

  Updates: `sudo apt-get update && sudo apt-get upgrade op-forward`.

- **Manual download**:

  ```bash
  curl -fsSL "https://github.com/reishoku/fork.op-forward/releases/latest/download/op-forward_$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/').tar.gz" \
    | tar -xz -C ~/.local/bin/
  ```

  Make sure `~/.local/bin` is on `PATH` for both interactive shells and
  non-interactive sessions (the daemon-spawned `op` runs in
  non-interactive mode).

### 2. Install the `op` shim

```bash
op-forward install
```

Writes `~/.local/bin/op` (a bash script). The script:

1. Looks for `op-forward` next to itself (or via `os.Executable()` of the
   running `op-forward`).
2. Looks for the **real** `op` by walking `$PATH` and skipping
   `~/.local/bin`. Falls back to `/usr/bin/op` if no real `op` is on PATH.
3. On invocation, `exec`s `op-forward proxy -- "$@"` and uses its exit
   code. Exit code `127` from the proxy means infrastructure is unavailable
   — the script then falls through to the real `op` so that `op` continues
   to work locally if the host daemon is unreachable.

The installer prints the `which op` resolution. If `op` resolves anywhere
other than `~/.local/bin/op`, fix `PATH` so the shim wins.

The shim path is hardcoded; `PATH` ordering is the only knob.

### 3. Token deployment

The proxy needs `refresh.token` (and ideally `access.token`) on the VM.
There is no automated handoff — you copy them yourself once.

```bash
# From the host (macOS):
scp -r ~/Library/Caches/op-forward/{access.token,refresh.token,session.token} \
    vm:~/.cache/op-forward/
```

Notes:

- The destination directory must be the VM's resolved `TokenDir()`. If
  `XDG_STATE_HOME` is set on the VM, target `$XDG_STATE_HOME/op-forward`
  instead. See [`configuration.md` § Tokens](configuration.md#tokens).
- `access.token` is optional — the proxy will refresh on the first 401 if
  only `refresh.token` is present.
- `session.token` is helpful only for old proxy clients (pre-two-tier).
  Including it does no harm.
- File mode is preserved by `scp` for `0600` files only if the source
  filesystem reports it correctly; verify with `stat` after copy.
- Subsequent re-deployments are usually only needed when the refresh token
  has expired (more than 30 days unused) or you have wiped the host token
  directory.

### 4. Bring up the SSH tunnel

```bash
remote_socket="$HOME/.cache/op-forward/op-forward.sock"
ssh -fN -R "$remote_socket:$HOME/Library/Caches/op-forward/op-forward.sock" \
    -o ControlMaster=no \
    -o ControlPath=none \
    user@host
export OP_FORWARD_SOCKET_PATH="$remote_socket"
```

What each flag does:

| Flag                       | Purpose |
|----------------------------|---------|
| `-fN`                      | Background; do not run a remote command. The tunnel persists until the SSH process exits. |
| `-R remote:host`           | Forward remote-side `remote_socket` to host-side `host_socket`. |
| `-o ControlMaster=no`      | Disable creating a master multiplex channel. |
| `-o ControlPath=none`      | Disable joining an existing master channel. |

### Multiplexing pitfall

If `~/.ssh/config` enables `ControlMaster`/`ControlPath` for this host
(common for fast `git`/scp), only the *first* SSH connection sets up
`RemoteForward`. Later connections share the master and **silently** lack
forwarding — the proxy then sees a missing socket file and exits `127`.

The two `-o` flags above bypass this. You can also dedicate a separate
config block for the tunnel:

```sshconfig
Host op-forward-tunnel
    HostName real-host
    ControlMaster no
    ControlPath none
    RemoteForward /home/you/.cache/op-forward/op-forward.sock /Users/you/Library/Caches/op-forward/op-forward.sock
```

### Lima / Colima

For VMs managed by [Lima](https://github.com/lima-vm/lima) or
[Colima](https://github.com/abiosoft/colima), use the per-VM ssh config:

```bash
remote_socket="$HOME/.cache/op-forward/op-forward.sock"
ssh -fN -R "$remote_socket:$HOME/Library/Caches/op-forward/op-forward.sock" \
    -o ControlMaster=no \
    -o ControlPath=none \
    -F ~/.colima/_lima/<vm-profile>/ssh.config \
    lima-<vm-profile>
```

The `-F` argument points at Lima's per-VM SSH config; the `-o` overrides
shadow any multiplexing it might enable.

### 5. Verify the tunnel

The simplest end-to-end check is a `health` request:

```bash
# On the VM:
curl --unix-socket "$OP_FORWARD_SOCKET_PATH" http://unix/health
# expected: {"status":"ok"}
```

If that works, run an actual `op` command. The first invocation will trigger
Touch ID on the host:

```bash
op account list
```

On success, every subsequent `op` call also triggers Touch ID — that is the
PTY-per-call mechanism described in
[`security.md` § Touch ID per call](security.md#6-touch-id-per-call).

## Updating

The host can self-update:

```bash
op-forward update
```

This:

1. Queries the GitHub Releases API for the latest tagged version of
   `reishoku/fork.op-forward`.
2. Downloads the platform-appropriate `op-forward_<version>_<os>_<arch>.tar.gz`.
3. Extracts the binary, writes it as `<exec>.update`, and `Rename`s it over
   the running binary atomically.
4. `pgrep`s for `op-forward serve`, `SIGTERM`s the running daemon. launchd
   re-spawns it with the new binary because of `KeepAlive=true`.

If you installed via Homebrew, prefer `brew upgrade reishoku/tap/op-forward`
so the formula stays in sync — `op-forward update` will replace the binary
contents but Homebrew's records will then be wrong.

The remote (VM) side also accepts `op-forward update`, but APT-managed
installs should prefer `apt-get upgrade op-forward`.

## Troubleshooting

### `op-forward: unix socket tunnel not available at <path>`

The SSH `RemoteForward` is not in place, the path on the VM does not match
`OP_FORWARD_SOCKET_PATH`, or sshd refused to bind the remote socket. Check:

- `ls -l "$OP_FORWARD_SOCKET_PATH"` on the VM — does the socket inode exist?
- `ssh -O check user@host` — is the SSH connection alive?
- Re-run the SSH command without `-fN` to see error output (especially
  `bind: Address already in use`, which means a stale socket file).
- If using SSH multiplexing, see [Multiplexing pitfall](#multiplexing-pitfall).

### `403 Forbidden` from the daemon

The daemon's peer-credential check failed: the connecting UID does not match
the daemon's UID. Plausible causes:

- The SSH `RemoteForward` is bridging *different* user accounts on the two
  ends. Run the daemon and the tunnel as the same user.
- A `setuid` binary on the VM is invoking `op-forward proxy`. Don't do that.

See [`transport.md` § Peer-credential enforcement](transport.md#peer-credential-enforcement).

### `op-forward: token refresh failed`

The refresh token on the VM does not match the one the daemon currently
holds in memory. Causes and fixes:

- Refresh token is past its 30-day expiry → re-deploy from the host.
- Host token directory was wiped → daemon now holds a *new* refresh token
  whose value the VM does not have → re-deploy from the host.
- The proxy is reading a stale token from `session.token` instead of
  `refresh.token` → delete `session.token` on the VM.

See [`tokens.md` § Failure modes](tokens.md#failure-modes).

### Touch ID does not prompt for every call

That is a 1Password setting, not an op-forward setting. Open 1Password →
Settings → Developer → ensure "Use the system authentication service to
unlock" and the per-CLI-call biometric option are both on. op-forward only
forces an interactive PTY; the desktop app decides whether to prompt.

### Daemon respawns in a tight loop

Inspect `~/Library/Logs/op-forward.log`. The most common cause is a
permission problem on the token directory or socket parent directory after a
manual `chown`/`chmod`; check that the user running launchd owns
`~/Library/Caches/op-forward` and it is mode `0700`.

### `op-forward update` on a Homebrew-installed binary

It will succeed but break `brew upgrade` semantics — Homebrew thinks the old
version is installed. Either prefer `brew upgrade reishoku/tap/op-forward`,
or re-link with `brew link --overwrite op-forward` if you have already
self-updated.

## See also

- [`cli.md`](cli.md) — what each subcommand does
- [`configuration.md`](configuration.md) — every variable mentioned above
- [`tokens.md`](tokens.md) — token deployment and rotation
- [`transport.md`](transport.md) — socket and tunnel internals
- [`security.md`](security.md) — what threats this configuration handles
