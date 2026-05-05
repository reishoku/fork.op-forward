# op-forward Documentation

This directory contains the detailed reference for op-forward — a transparent
forwarder that lets `op` commands inside a remote VM trigger Touch ID on the
host. The top-level [`README.md`](../README.md) is the quickstart; the pages
here go deeper.

## Reading order

If you have never run op-forward before, read the quickstart in
[`../README.md`](../README.md) first, then come back here.

| Page | What it covers |
|---|---|
| [`architecture.md`](architecture.md) | Component layout, request flow, why each piece exists |
| [`transport.md`](transport.md) | HTTP-over-Unix-socket transport, peer credentials, SSH `RemoteForward` of Unix sockets |
| [`tokens.md`](tokens.md) | Two-tier access/refresh token model, rotation, sliding expiry, legacy migration |
| [`security.md`](security.md) | Threat model, defense layers, what is and is not protected |
| [`protocol.md`](protocol.md) | HTTP API reference: endpoints, request/response shapes, status codes, headers |
| [`configuration.md`](configuration.md) | Environment variables, path precedence, platform defaults |
| [`deployment.md`](deployment.md) | Host setup, remote setup, SSH tunnel patterns, launchd, troubleshooting |
| [`cli.md`](cli.md) | Subcommand reference: `serve`, `install`, `proxy`, `service`, `update`, `version` |
| [`development.md`](development.md) | Build, test, release, branch model, Homebrew tap, APT repo |

## Quick links

- I'm getting `unix socket tunnel not available` → [`deployment.md` § Troubleshooting](deployment.md#troubleshooting)
- I'm getting `403 forbidden` → [`transport.md` § Peer credentials](transport.md#peer-credential-enforcement)
- I'm getting `refresh_token_expired` → [`tokens.md` § Failure modes](tokens.md#failure-modes)
- I want to know which subcommands are blocked → [`security.md` § Blocked subcommands](security.md#blocked-subcommands)
- I want to bump the wire-protocol minimum client version → [`protocol.md` § Version negotiation](protocol.md#version-negotiation)

## Fork relationship

This repository is a fork of `ekovshilovsky/op-forward`. The `reishoku` branch
is the active branch in this fork and is what the CI/release pipelines target.
The `main` branch tracks upstream. Two patches distinguish the fork:

- **PR #1** — Switch transport from loopback TCP (`127.0.0.1:18340`) to a Unix
  domain socket with peer-credential enforcement. See [`transport.md`](transport.md).
- **PR #3** — Update repository references throughout (binary release URLs,
  Homebrew tap, APT repo, install script) to point at this fork.

If you are reading this in the upstream repository, the documentation here may
not apply.

## Source-of-truth pointers

When the docs and the code disagree, the code wins. These are the files the
docs reference most heavily:

- Routing entry point: [`cmd/root.go`](../cmd/root.go)
- Daemon: [`internal/daemon/server.go`](../internal/daemon/server.go)
- Transport (socket path, peer creds): [`internal/transport/`](../internal/transport)
- Tokens: [`internal/auth/token.go`](../internal/auth/token.go)
- Executor (validation, PTY, op invocation): [`internal/executor/`](../internal/executor)
- Version comparison: [`internal/version/compare.go`](../internal/version/compare.go)
- Proxy client: [`cmd/proxy.go`](../cmd/proxy.go)
- Shim template: [`cmd/install.go`](../cmd/install.go)
