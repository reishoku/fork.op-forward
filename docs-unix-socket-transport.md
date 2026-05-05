# Unix Socket Transport Design

## Motivation
Current loopback TCP+HTTP transport relies on bearer tokens and SSH remote port forwarding. This leaves avoidable exposure to local TCP endpoint abuse (CWE-200, CWE-306 defense-in-depth gap). Unix domain sockets reduce reachable surface and allow kernel-verified peer identity.

## Design
- Keep `net/http` protocol handlers and token model unchanged for compatibility.
- Replace the loopback TCP listener with `unix://$OP_FORWARD_SOCKET_PATH` (default: cache dir `op-forward.sock`).
- Enforce `0600` on socket inode and `0700` on containing directory.
- Enable peer credential extraction:
  - Linux: `SO_PEERCRED`.
  - macOS: `LOCAL_PEERCRED` (`Xucred`).
- On request handling, deny mismatched UID with `403 Forbidden`.
- Keep official SDK/stdlib only (`net`, `net/http`, `syscall`, `path/filepath`) and no extra dependencies.
- Path traversal countermeasure (CWE-22): token persistence uses Go's traversal-resistant `os.Root` API (`os.OpenRoot` + `Root.ReadFile`/`Root.WriteFile`/`Root.Rename`) so token file operations are constrained to the configured token directory even in the presence of symlinks and `..` components.
- Directory precedence:
  - Socket path: `OP_FORWARD_SOCKET_PATH` → `$XDG_RUNTIME_DIR/op-forward.sock` (if set) → OS cache dir (`os.UserCacheDir`) + `op-forward/op-forward.sock`.
  - Token directory: `OP_FORWARD_TOKEN_DIR` → `$XDG_STATE_HOME/op-forward` (if set) → OS cache dir (`os.UserCacheDir`) + `op-forward`.

## Environment validation and fallback
- `XDG_RUNTIME_DIR` is ideal for Unix sockets on Linux because the XDG spec requires it to be user-owned, mode `0700`, and local filesystem.
- `XDG_RUNTIME_DIR` is not guaranteed to exist (e.g., non-systemd sessions, some CI, many macOS shells). Fallback therefore remains required.
- `XDG_STATE_HOME` is intended for persistent, user-specific state; if unset, XDG default is `$HOME/.local/state`.
- On macOS, XDG variables are usually unset by default; `os.UserCacheDir` provides stable Darwin defaults (`$HOME/Library/Caches`) without adding dependencies.

## SSH Forwarding
Use OpenSSH remote forwarding of Unix sockets from Linux VM to macOS host:

```bash
# Use the same absolute remote-side path for SSH forwarding and the proxy.
remote_socket="$HOME/.cache/op-forward/op-forward.sock"
ssh -fN -R "$remote_socket:$HOME/Library/Caches/op-forward/op-forward.sock" vm
```

Set `OP_FORWARD_SOCKET_PATH` to the same remote-side socket path on the remote client side.

## Security review (CWE)
- CWE-22 Path Traversal: token persistence is constrained to the configured token directory with `os.Root`; socket paths are canonicalized and must be absolute.
- CWE-284 Improper Access Control: mitigated with socket FS permissions + UID match.
- CWE-306 Missing Authentication for Critical Function: existing bearer token retained.
- CWE-922 Insecure Storage: existing token file permission constraints retained.
- CWE-400 Uncontrolled Resource Consumption: request size and HTTP timeouts retained.

## References
- Go `net` package Unix domain sockets: https://pkg.go.dev/net
- Go `os.Root` traversal-resistant file APIs: https://pkg.go.dev/os#Root
- Go `path/filepath` (`Clean`, `IsAbs`) for canonical path handling: https://pkg.go.dev/path/filepath
- Go `net/http` server/client APIs: https://pkg.go.dev/net/http
- OpenSSH `ssh(1)` forwarding syntax (`-R` with Unix sockets): https://man.openbsd.org/ssh.1
- Linux `unix(7)` and `SO_PEERCRED`: https://linuxman7.org/linux/man-pages/man7/unix.7.html
