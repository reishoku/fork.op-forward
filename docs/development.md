# Development & release

This page covers how the codebase is built, tested, released, and how the
fork's branch model relates to upstream.

## Repository layout

```
.
├── cmd/                         # CLI subcommands; routing in cmd/root.go
│   ├── install.go               # writes the bash op shim
│   ├── proxy.go                 # the per-call client
│   ├── proxy_test.go
│   ├── root.go                  # subcommand dispatch + usage text
│   ├── serve.go                 # daemon startup
│   ├── service.go               # launchd plist install/uninstall (macOS only)
│   └── update.go                # self-update from GitHub Releases
├── internal/
│   ├── auth/                    # token model and persistence
│   │   ├── token.go
│   │   └── token_test.go
│   ├── daemon/                  # HTTP server, three handlers
│   │   ├── server.go
│   │   └── server_test.go
│   ├── executor/                # request validation + PTY-based op execution
│   │   ├── op.go
│   │   ├── op_test.go
│   │   ├── pty_darwin.go        # Darwin-specific termios + Setctty
│   │   └── pty_linux.go         # Linux-specific termios + Setctty
│   ├── transport/               # socket path + peer-credential extraction
│   │   ├── peercred_darwin.go   # LOCAL_PEERCRED via Xucred
│   │   ├── peercred_linux.go    # SO_PEERCRED via Ucred
│   │   ├── peercred_other.go    # returns -1 (skip)
│   │   └── socket.go            # SocketPath() resolution
│   └── version/                 # semantic-version comparison
│       ├── compare.go
│       └── compare_test.go
├── scripts/
│   ├── build-apt-repo.sh        # APT Packages/Release index for gh-pages
│   ├── build-deb.sh             # .deb assembly from the release tarballs
│   └── install.sh               # one-shot installer (curl | sh)
├── .github/workflows/
│   ├── ci.yml                   # go vet + go test on push/PR to reishoku
│   └── release.yml              # tag-triggered release pipeline
├── docs/                        # this directory
├── main.go                      # tiny shim that calls cmd.Execute
├── go.mod                       # module = github.com/reishoku/fork.op-forward
├── Makefile                     # build, build-all, test, clean
├── README.md                    # quickstart + summary
└── LICENSE                      # MIT
```

The package boundaries map cleanly to documentation pages:

| Package         | Page |
|-----------------|------|
| `cmd/`          | [`cli.md`](cli.md) |
| `internal/auth` | [`tokens.md`](tokens.md) |
| `internal/daemon` | [`protocol.md`](protocol.md), [`architecture.md`](architecture.md) |
| `internal/executor` | [`security.md`](security.md), [`architecture.md`](architecture.md) |
| `internal/transport` | [`transport.md`](transport.md) |
| `internal/version` | [`protocol.md` § Version negotiation](protocol.md#version-negotiation) |

## Toolchain

| Component | Version | Source of truth |
|---|---|---|
| Go         | `1.25.0`              | [`go.mod`](../go.mod) |
| `creack/pty`        | `v1.1.24`     | [`go.mod`](../go.mod) |
| `golang.org/x/sys`  | `v0.42.0`     | [`go.mod`](../go.mod) |

CI also drives `go-version-file: go.mod`, so the Go toolchain on the
runner tracks the module declaration automatically.

## Building

The Makefile is the entry point.

```
make build            # native build (current GOOS/GOARCH)
make build-all        # cross-compile darwin/linux × arm64/amd64 + tar.gz
make test             # go test ./...
make clean            # rm -rf op-forward dist/
```

Notes on `make build`:

- Output binary is named `op-forward` in the repo root.
- `LDFLAGS = -s -w -X github.com/reishoku/fork.op-forward/cmd.Version=$(VERSION)`.
  `VERSION` defaults to `0.3.0` in the Makefile; override on the command
  line for local snapshots: `make build VERSION=0.3.1-dev`.
- `-s -w` strips the symbol table and DWARF; the binary drops from
  ≈10 MB to ≈6 MB without affecting behaviour.

For local development, `go run . version` is faster than going through
`make` — but it reports `dev` because the ldflags are not set.

## Testing

```
go test ./...
```

The suite is hermetic — every test creates its own `t.TempDir` and
`t.Setenv`s the relevant overrides (`OP_FORWARD_TOKEN_DIR`,
`OP_FORWARD_TOKEN_FILE`, `XDG_STATE_HOME`). It does not start the daemon
on the real default socket path, does not write to the user's real cache
directory, and does not depend on `op` being installed.

What is covered:

- **`internal/auth`** — token generation, TTL math, save/load, atomic
  write semantics, `OP_FORWARD_TOKEN_FILE`/`OP_FORWARD_TOKEN_DIR` precedence,
  legacy migration, refresh-token uniqueness and rotation.
- **`internal/daemon`** — handler authentication paths, peer-UID is *not*
  exercised in unit tests (would require a real Unix socket); refresh
  endpoint rotation; version-negotiation outcomes.
- **`internal/executor`** — argument validation matrix (legitimate vs
  shell-metachar vs blocked), PTY allocation, OPOST stripping.
- **`internal/version`** — `Compare` with edge cases (`dev`, leading `v`,
  multi-digit minor).
- **`cmd`** — proxy token-path resolution under the various env-var
  combinations.

`go vet ./...` runs in CI; treat its output as a build error.

The PTY tests in [`internal/executor/op_test.go`](../internal/executor/op_test.go)
do require a working PTY subsystem. They run on both macOS and Linux CI
runners.

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on push and
pull requests against `reishoku`:

```yaml
on:
  push:
    branches: [reishoku]
  pull_request:
    branches: [reishoku]
```

The job is `ubuntu-latest`, checks out, sets up Go from `go.mod`, and runs
`go vet ./...` followed by `go test ./...`. There is no separate macOS leg
in CI today; macOS-specific code paths are exercised at release time when
the cross-compile leg builds the Darwin binaries (the build itself
type-checks them).

## Release pipeline

[`.github/workflows/release.yml`](../.github/workflows/release.yml) runs on
tag push (`v*`). It does the following, in order:

1. **Test gate** — repeat `go test ./...` on the tagged commit.
2. **Cross-compile** — builds `op-forward_<version>_<os>_<arch>` for
   `darwin/{amd64,arm64}` and `linux/{amd64,arm64}`. Each artifact is
   tarred to `dist/<name>.tar.gz` with the binary at the archive root.
3. **GitHub Release** — `gh release create` against the pushed tag,
   attaching all four tarballs and using `--generate-notes` for the body.
4. **Homebrew tap** — generates a fresh `op-forward.rb` with the four SHA
   digests, pushes it to `reishoku/homebrew-tap/Formula/op-forward.rb`. The
   formula's `bin.install "op-forward"` is the only install step; `test`
   asserts that `op-forward version` contains the version string.
5. **`.deb` packages** — `scripts/build-deb.sh` produces architecture-specific
   `.deb`s under `deb-out/`. The package name is `op-forward`, version is the
   release version, the binary lands at `/usr/local/bin/op-forward`.
6. **APT repository** — `scripts/build-apt-repo.sh` builds (or rebuilds) the
   APT metadata under `dists/stable/main/binary-{amd64,arm64}/Packages` and
   commits the result to the `gh-pages` branch. If `APT_GPG_PRIVATE_KEY`
   and `APT_GPG_KEY_ID` secrets are present, the `Release` file is signed
   (producing `Release.gpg` and `InRelease`) and the public key is exported
   to `key.gpg`. The `gh-pages` branch is what serves
   `https://reishoku.github.io/fork.op-forward`.

The release pipeline depends on these secrets:

| Secret                  | Purpose |
|-------------------------|---------|
| `GITHUB_TOKEN`          | Default, sufficient for `gh release create` and `gh-pages` push. |
| `HOMEBREW_TAP_TOKEN`    | Token with `contents: write` on `reishoku/homebrew-tap`. |
| `APT_GPG_PRIVATE_KEY`   | ASCII-armored private key for signing the APT `Release`. Optional but recommended. |
| `APT_GPG_KEY_ID`        | Fingerprint of the above. |

If the GPG secrets are absent, the APT repo is published unsigned and
clients need `[trusted=yes]` in their sources.list. The recommended path is
to set both secrets so the repo is signed end-to-end.

## Branch model

This is a fork. The conventions:

- **`reishoku`** is the active branch and the GitHub default. CI runs
  against it; releases are tagged on it.
- **`main`** tracks upstream `ekovshilovsky/op-forward`. Do not commit to
  `main` directly; it exists to resolve diffs from upstream.
- Topic branches (`docs/...`, `feat/...`, `fix/...`) merge into
  `reishoku` via PR.
- Releases are tagged `v<x>.<y>.<z>` on the `reishoku` branch.

The fork-specific changes against upstream `main` are:

| PR | Title | Effect |
|----|-------|--------|
| #1 | Switch transport to Unix domain socket with peer-credential enforcement | Replaces loopback TCP with HTTP-over-Unix-socket; adds `internal/transport`; tightens token persistence with `os.Root`. See [`transport.md`](transport.md). |
| #3 | Update fork repository references | Changes `ekovshilovsky/op-forward` → `reishoku/fork.op-forward` throughout (badges, releases, install script, Homebrew tap, APT repo, Go module path). |

These two PRs are the entirety of the fork's divergence from upstream as of
this writing. Anything else is unchanged from upstream.

## Local development workflow

A typical iteration:

```bash
# Pull and rebase on the active branch
git checkout reishoku
git pull --rebase origin reishoku

# Branch
git checkout -b feat/<short-description>

# Code, test, repeat
go test ./...
make build VERSION=local-dev
./op-forward version

# Push and PR
git push -u origin feat/<short-description>
gh pr create --base reishoku --assignee reishoku
```

Open PRs against `reishoku`, not `main`. Set `assignee: reishoku` on every
PR and Issue (project rule).

## Cutting a release

1. Land all desired changes on `reishoku`.
2. Update `Makefile`'s `VERSION` to the new value (purely cosmetic — the
   release workflow uses the tag).
3. Tag and push:
   ```bash
   git tag -s v<x>.<y>.<z> -m "v<x>.<y>.<z>"
   git push origin v<x>.<y>.<z>
   ```
4. Watch the run at <https://github.com/reishoku/fork.op-forward/actions>.
5. Verify:
   - The release page lists four tarballs.
   - `brew install reishoku/tap/op-forward` picks up the new version.
   - `apt-get update && apt-cache policy op-forward` from a Debian/Ubuntu
     box shows the new version.
   - The Dependabot/automation noise has settled.

If the release workflow fails partway, prefer to roll forward (push a
`v<x>.<y>.<z+1>` tag) over recovering a half-published release.

## Adding a new endpoint

The minimum viable change to add an HTTP endpoint:

1. Implement the handler in [`internal/daemon/server.go`](../internal/daemon/server.go).
   Decide explicitly whether peer-UID is required and which token (access or
   refresh) authenticates it.
2. Register it in `Server.Start`'s `mux.HandleFunc`.
3. Add a request flow to [`cmd/proxy.go`](../cmd/proxy.go) if the proxy
   needs to call it; otherwise document that callers must use `curl
   --unix-socket` directly.
4. Add tests to
   [`internal/daemon/server_test.go`](../internal/daemon/server_test.go),
   covering the no-auth, wrong-method, and wrong-token cases at minimum.
5. Update [`protocol.md`](protocol.md) — every wire-visible behaviour must
   be documented before merge.
6. If the change is breaking, bump `MinClientVersion` in the same PR. See
   [`protocol.md` § Version negotiation](protocol.md#version-negotiation).

## See also

- [`architecture.md`](architecture.md) — what each package does
- [`protocol.md`](protocol.md) — wire-visible contract
- [`cli.md`](cli.md) — what each subcommand does
- Upstream: <https://github.com/ekovshilovsky/op-forward>
