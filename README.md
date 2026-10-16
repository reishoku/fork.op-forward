# op-forward

[![CI](https://github.com/reishoku/fork.op-forward/actions/workflows/ci.yml/badge.svg)](https://github.com/reishoku/fork.op-forward/actions/workflows/ci.yml)
[![Report Card](https://img.shields.io/endpoint?url=https%3A%2F%2Freishoku.github.io%2Ffork.op-forward%2Freport%2Fbadge.json)](https://reishoku.github.io/fork.op-forward/report/)
[![Release](https://img.shields.io/github/v/release/reishoku/fork.op-forward)](https://github.com/reishoku/fork.op-forward/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/reishoku/fork.op-forward.svg)](https://pkg.go.dev/github.com/reishoku/fork.op-forward)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/reishoku/fork.op-forward)](go.mod)

Forward 1Password CLI (`op`) commands across SSH boundaries with biometric authentication.

## The Problem

The 1Password CLI requires desktop integration for biometric unlock (Touch ID on macOS). Inside headless VMs, containers, or remote SSH sessions, `op` commands fail because the biometric chain is broken — there's no display server, no security framework, no Touch ID sensor.

## How It Works

op-forward runs a small HTTP daemon on the host machine (where Touch ID works) and installs a transparent `op` shim on the remote side. Every `op` command in the VM is intercepted by the shim, forwarded through an SSH tunnel to the host daemon, and executed locally — triggering Touch ID for each privileged operation.

```
Remote VM: op shim → HTTP over Unix socket → SSH RemoteForward → Host daemon → op CLI → Touch ID
```

The developer experience is transparent: run `op account list` or `op item get <uuid> --fields username` inside any VM, and it works exactly as if `op` were running locally.

## Quick Start

### Install on macOS (host)

Via Homebrew:

```bash
brew install reishoku/tap/op-forward
```

Via the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/reishoku/fork.op-forward/reishoku/scripts/install.sh | sh
```

Or build from source:

```bash
git clone https://github.com/reishoku/fork.op-forward.git
cd fork.op-forward
go build -ldflags="-s -w" -o op-forward .
cp op-forward ~/.local/bin/
```

### Start the daemon

```bash
# Foreground (for testing)
op-forward serve

# Or install as a persistent launchd service (macOS)
op-forward service install
```

The daemon listens on the Unix domain socket `~/Library/Caches/op-forward/op-forward.sock` by default (`$XDG_RUNTIME_DIR/op-forward.sock` when `XDG_RUNTIME_DIR` is set; override with `OP_FORWARD_SOCKET_PATH`) and generates bearer tokens under `~/Library/Caches/op-forward/`. To listen on loopback TCP instead, see [Transports](#transports).

### Set up the remote side (VM / Linux)

Packages are published for every major distribution family from one signed
pipeline; pick the one that matches the VM or container, then run
`op-forward install` to set up the `op` shim.

Debian / Ubuntu (APT):

```bash
curl -fsSL https://reishoku.github.io/fork.op-forward/key.gpg | sudo gpg --dearmor -o /usr/share/keyrings/op-forward.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/op-forward.gpg] https://reishoku.github.io/fork.op-forward stable main" | sudo tee /etc/apt/sources.list.d/op-forward.list
sudo apt-get update && sudo apt-get install op-forward
```

Fedora / RHEL / openSUSE (dnf, yum, zypper):

```bash
sudo curl -fsSL https://reishoku.github.io/fork.op-forward/rpm/op-forward.repo -o /etc/yum.repos.d/op-forward.repo
sudo dnf install op-forward
```

Alpine (apk):

```bash
sudo curl -fsSL https://reishoku.github.io/fork.op-forward/apk/op-forward.rsa.pub -o /etc/apk/keys/op-forward.rsa.pub
echo "https://reishoku.github.io/fork.op-forward/apk" | sudo tee -a /etc/apk/repositories
sudo apk add op-forward
```

Arch Linux: download the `.pkg.tar.zst` for your architecture from the
[latest release](https://github.com/reishoku/fork.op-forward/releases/latest)
and install it with `sudo pacman -U <file>`.

Any other Linux (static binary, no package manager):

```bash
curl -fsSL https://raw.githubusercontent.com/reishoku/fork.op-forward/reishoku/scripts/install.sh | sh
```

Then, on every remote system:

```bash
op-forward install
```

Packages install the binary to `/usr/bin/op-forward`. Upgrading from a
release before 0.7.2, which used `/usr/local/bin`, requires re-running
`op-forward install` once so the shim picks up the new location.
Upgrades afterwards go through the package manager
(`apt-get upgrade`, `dnf upgrade`, `apk upgrade`).

After installing, deploy the auth token and start the SSH tunnel:

```bash
# Deploy the refresh token (from host). The remote fetches its own
# short-lived access token from the daemon on first use.
scp ~/Library/Caches/op-forward/refresh.token vm:~/.cache/op-forward/refresh.token
```

Start the SSH tunnel. `ssh -R remote_socket:local_socket destination`
listens on `remote_socket` on the SSH **server** (the VM) and forwards to
`local_socket` on the SSH **client** (the macOS host); both paths are
expanded once by the host's shell, so write the VM-side path explicitly.

```bash
# Run on the macOS host. <vm> is the SSH destination (alias / hostname / user@vm).
HOST_SOCKET="$HOME/Library/Caches/op-forward/op-forward.sock"
VM_SOCKET="/home/<vm-user>/.cache/op-forward/op-forward.sock"
ssh -R "$VM_SOCKET:$HOST_SOCKET" <vm>
```

Then on the VM, point the shim at the forwarded socket. Without this, the
shim uses `$XDG_RUNTIME_DIR/op-forward.sock` when `XDG_RUNTIME_DIR` is set
(typical for SSH logins on systemd hosts) and
`~/.cache/op-forward/op-forward.sock` otherwise, so set it explicitly to
match `VM_SOCKET`:

```bash
export OP_FORWARD_SOCKET_PATH="$HOME/.cache/op-forward/op-forward.sock"
```

Now `op` commands inside the VM are forwarded to the host. Things to know
about socket forwarding:

- `sshd` on the VM does not remove a forwarded socket when the connection
  drops, so the next `ssh -R` fails with `bind: Address already in use`. Set
  `StreamLocalBindUnlink yes` in the VM's `/etc/ssh/sshd_config` to have it
  replaced automatically.
- The socket's directory must be owned by you and mode `0700`; the daemon
  refuses shared locations such as `/tmp`. `~/Library/Caches/op-forward` on
  macOS and `$XDG_RUNTIME_DIR` on Linux both qualify.
- Unix socket paths are limited to about 104 bytes on macOS; keep the host
  socket path short.

### TCP instead of a socket

The daemon can listen on loopback TCP instead, for SSH port forwarding or
for Docker Desktop below. Setting `OP_FORWARD_PORT` (or `OP_FORWARD_HOST` on
the shim side) selects TCP; `OP_FORWARD_LISTEN` / `OP_FORWARD_ADDR` take the
full `tcp://` form.

```bash
# Host: listen on 127.0.0.1:18340 instead of the socket
export OP_FORWARD_PORT=18340
op-forward serve        # or: op-forward service install (the endpoint is written into the plist)

# Host: forward the port into the VM
ssh -R 18340:127.0.0.1:18340 vm

# VM: dial the forwarded port
export OP_FORWARD_PORT=18340
```

### Docker Desktop containers (no SSH tunnel)

For a local Docker Desktop container (e.g. a VS Code dev container) you don't need
an SSH reverse tunnel — the container can reach the host daemon directly over
`host.docker.internal`. Run the daemon on TCP (see above), then point the
shim at it:

```bash
# in the container (after deploying the token as above)
export OP_FORWARD_HOST=host.docker.internal
op-forward install
```

The daemon still binds loopback only; Docker Desktop routes `host.docker.internal`
to the host, so no tunnel or extra proxy is needed.
`OP_FORWARD_ADDR=tcp://host.docker.internal:18340` is the equivalent full form.
Use TCP here: a host Unix socket cannot be bind-mounted into a Docker Desktop
container (`docker run` fails at mount time with `operation not supported`).

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `OP_FORWARD_LISTEN` | `unix://<default socket>` | Endpoint the daemon binds: `unix:///absolute/path.sock` or `tcp://127.0.0.1:PORT` (loopback only) |
| `OP_FORWARD_ADDR` | `unix://<default socket>` | Endpoint the shim dials: `unix:///absolute/path.sock` or `tcp://host:port`. Overrides `OP_FORWARD_HOST`/`OP_FORWARD_PORT` when set. |
| `OP_FORWARD_SOCKET_PATH` | `$XDG_RUNTIME_DIR/op-forward.sock`, else `<cache dir>/op-forward/op-forward.sock` | Default socket path, used by both sides when neither the full endpoint form nor the TCP shorthand is set. Must be absolute. |
| `OP_FORWARD_PORT` | unset (`18340` when TCP is selected) | TCP port shorthand. Setting it selects loopback TCP on both sides when the full endpoint form is unset. |
| `OP_FORWARD_HOST` | unset (`127.0.0.1` when TCP is selected) | TCP host shorthand for the shim; setting it selects TCP. Set to `host.docker.internal` to reach the host from a Docker Desktop container without an SSH tunnel. Never used by the daemon. |
| `OP_FORWARD_TOKEN_DIR` | `$XDG_STATE_HOME/op-forward` if `XDG_STATE_HOME` is set, else `~/Library/Caches/op-forward` (macOS) / `~/.cache/op-forward` (Linux) | Token storage directory. Token files are read and written through Go's traversal-resistant `os.Root` API, confined to this directory. |
| `OP_FORWARD_TOKEN_FILE` | `$TOKEN_DIR/access.token` | Full path to the access token file (applies to the access token only) |
| `OP_FORWARD_PROBE_TIMEOUT_MS` | `500` | How long the shim waits for the daemon to accept a connection before falling back to the local `op` |
| `OP_FORWARD_FETCH_TIMEOUT_MS` | `60000` | Shim HTTP request timeout |

## Commands

```
op-forward serve [--listen EP]    Start the host daemon (EP: unix:///path.sock (default) or tcp://127.0.0.1:PORT)
op-forward install                Install the op shim on the remote side
op-forward proxy [args...]        Forward one op command to the daemon (used by the shim)
op-forward service install        Install as a launchd daemon (macOS)
op-forward service uninstall      Remove the launchd daemon
op-forward update                 Update to the latest release
op-forward version                Print version
```

## Security Model

op-forward is designed for environments where the host is trusted and the remote side connects over a secure SSH tunnel.

**Touch ID is the primary security boundary.** Every privileged 1Password operation triggers biometric approval on the host. The proxy cannot bypass this.

Additional layers:

- **Local-only binding**: TCP endpoints must be loopback; the daemon refuses to bind anything else, so it is unreachable from the network. Unix socket endpoints are created `0600` inside a `0700` directory, and the daemon additionally checks the connecting process's uid (`SO_PEERCRED` / `LOCAL_PEERCRED`) and refuses other users even if the socket file is exposed.
- **Bearer token authentication**: Two 32-byte random hex tokens stored with 0600 permissions: a 1-hour access token presented on every request, and a 30-day refresh token that is rotated each time it is used to obtain a new access token.
- **No shell execution**: Commands are executed via `os/exec` (direct exec), not through a shell. Shell injection is structurally impossible.
- **Argument sanitization**: Arguments containing shell metacharacters (`` ` ``, `$`, `|`, `;`, `&`, newlines) are rejected before execution.
- **Blocked subcommands**: `signin`, `signout`, `update`, and `completion` are blocked — they either require interactive input or would modify the host's op configuration.
- **Audit logging**: All commands are logged with sensitive arguments (`--password`, `--reveal`) redacted.

### What this does NOT protect against

- A compromised VM with access to the token file can execute any non-blocked `op` command, subject to Touch ID approval.
- If Touch ID is configured to not require approval for every `op` invocation (unusual but possible), the proxy would execute commands without biometric gates.

The threat model assumes: SSH tunnels are secure, the host machine is not compromised, and Touch ID provides the authorization boundary.

## Transports

Both sides accept a single endpoint value that selects the transport:

| Form | Reachable by | Use when |
|---|---|---|
| `unix:///absolute/path.sock` (default) | SSH socket forwarding (`ssh -R remote.sock:local.sock`) | Standard setups: the VM-side endpoint is private to your user, and several VMs never fight over one port |
| `tcp://127.0.0.1:18340` | SSH port forwarding (`ssh -R 18340:127.0.0.1:18340`), Docker Desktop's `host.docker.internal` | Docker Desktop containers (a host Unix socket cannot be bind-mounted into the container, so `unix://` cannot serve them), or a VM whose sshd forbids socket forwarding |

The HTTP protocol, bearer tokens, and command validation are identical over both transports.

## Use with VMs (Colima, Lima, etc.)

op-forward works with any SSH-accessible VM. For VMs managed by [Colima](https://github.com/abiosoft/colima) or [Lima](https://github.com/lima-vm/lima), use the VM's SSH config directly. Resolving `VM_HOME` once via SSH sidesteps username differences between macOS and the VM:

```bash
# Run on the macOS host. Discover the VM-side $HOME (one-time).
VM_HOME=$(ssh -F ~/.colima/_lima/<vm-profile>/ssh.config lima-<vm-profile> 'printenv HOME')

HOST_SOCKET="$HOME/Library/Caches/op-forward/op-forward.sock"
VM_SOCKET="$VM_HOME/.cache/op-forward/op-forward.sock"

# Start tunnel (ControlMaster disabled to avoid SSH multiplexing conflicts)
ssh -fN -R "$VM_SOCKET:$HOST_SOCKET" \
    -o ControlMaster=no \
    -o ControlPath=none \
    -F ~/.colima/_lima/<vm-profile>/ssh.config \
    lima-<vm-profile>
```

For standard SSH hosts:

```bash
HOST_SOCKET="$HOME/Library/Caches/op-forward/op-forward.sock"
VM_SOCKET="/home/<remote-user>/.cache/op-forward/op-forward.sock"
ssh -fN -R "$VM_SOCKET:$HOST_SOCKET" user@remote-host
```

The `ControlMaster=no` flag is important when using SSH multiplexing — multiplexed connections only establish `RemoteForward` on the first connection. A dedicated tunnel connection avoids this.

## Updating

Self-update to the latest release:

```bash
op-forward update
```

This downloads the latest binary from GitHub Releases for your platform, replaces the running binary in-place, and the launchd service restarts automatically.

If installed via Homebrew:

```bash
brew upgrade reishoku/tap/op-forward
```

If installed from a package repository, use the package manager
(`apt-get upgrade op-forward`, `dnf upgrade op-forward`, `apk upgrade op-forward`).

The Homebrew formula and every package repository are updated automatically
on each release.

## Building

```bash
make build          # Build for current platform
make build-all      # Cross-compile for darwin/linux × arm64/amd64
make test           # Run tests
make clean          # Remove build artifacts
```

Linux packages are defined once in `nfpm.yaml` and built with
[nfpm](https://nfpm.goreleaser.com):

```bash
make build-all VERSION=1.0.0
scripts/build-packages.sh 1.0.0 dist pkg-out      # deb, rpm, apk, Arch for amd64 and arm64
scripts/test-packages.sh 1.0.0 pkg-out            # install each in its distribution container
scripts/build-aur.sh 1.0.0 aur-out --verify       # PKGBUILD + .SRCINFO for op-forward-bin, built in an Arch container
```

The release workflow runs all of these; with `AUR_SSH_PRIVATE_KEY` configured it
also pushes the generated `PKGBUILD` to the AUR as `op-forward-bin`.

## Documentation

Detailed reference documentation is in [`docs/`](docs/README.md):

- [Architecture](docs/architecture.md): components, request flow, and why each piece exists
- [Transport](docs/transport.md): `tcp://` and `unix://` endpoints, bind rules, peer credentials, SSH forwarding, Docker Desktop
- [Tokens](docs/tokens.md): access and refresh tokens, rotation, expiry, file locations, legacy migration
- [Security](docs/security.md): threat model, defense layers, blocked subcommands, and what is not protected
- [Protocol](docs/protocol.md): HTTP API, status codes, headers, exit codes, timeouts, version negotiation
- [Configuration](docs/configuration.md): every environment variable and flag, with precedence rules
- [Deployment](docs/deployment.md): host and remote setup, token deployment, SSH tunnels, troubleshooting
- [CLI](docs/cli.md): subcommand reference for `serve`, `install`, `proxy`, `service`, `update`, and `version`
- [Development](docs/development.md): build, test, packaging, repositories, release and republish workflows

## License

MIT

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community expectations. Report security issues privately as described in [SECURITY.md](SECURITY.md).
