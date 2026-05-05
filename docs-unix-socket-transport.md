# Unix Socket Transport Design

## Motivation
Current loopback TCP+HTTP transport relies on bearer tokens and SSH remote port forwarding. This leaves avoidable exposure to local TCP endpoint abuse (CWE-200, CWE-306 defense-in-depth gap). Unix domain sockets reduce reachable surface and allow kernel-verified peer identity.

## Design
- Keep `net/http` protocol handlers and token model unchanged for compatibility.
- Replace daemon listener from `127.0.0.1:18340` to `unix://$OP_FORWARD_SOCKET_PATH` (default: cache dir `op-forward.sock`).
- Enforce `0600` on socket inode and `0700` on containing directory.
- Enable peer credential extraction:
  - Linux: `SO_PEERCRED`.
  - macOS: `LOCAL_PEERCRED` (`Xucred`).
- On request handling, deny mismatched UID with `403 Forbidden`.
- Keep official SDK/stdlib only (`net`, `net/http`, `syscall`) and no extra dependencies.

## SSH Forwarding
Use OpenSSH remote forwarding of Unix sockets from Linux VM to macOS host:

```bash
ssh -fN -R /tmp/op-forward.sock:$HOME/Library/Caches/op-forward/op-forward.sock vm
```

Set `OP_FORWARD_SOCKET_PATH=/tmp/op-forward.sock` on the remote client side.

## Security review (CWE)
- CWE-284 Improper Access Control: mitigated with socket FS permissions + UID match.
- CWE-306 Missing Authentication for Critical Function: existing bearer token retained.
- CWE-922 Insecure Storage: existing token file permission constraints retained.
- CWE-400 Uncontrolled Resource Consumption: request size and HTTP timeouts retained.

## References
- Go `net` package Unix domain sockets: https://pkg.go.dev/net
- Go `net/http` server/client APIs: https://pkg.go.dev/net/http
- OpenSSH `ssh(1)` forwarding syntax (`-R` with Unix sockets): https://man.openbsd.org/ssh.1
- Linux `unix(7)` and `SO_PEERCRED`: https://linuxman7.org/linux/man-pages/man7/unix.7.html
