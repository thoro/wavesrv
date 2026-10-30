# wavesrv

The Go backend of [Wave Terminal](https://github.com/wavetermdev/waveterm), with the Electron frontend removed. It builds two binaries:

- **wavesrv** — the long-running backend process. It runs terminal blocks (local shells on a pty, SSH and WSL connections), keeps durable shell sessions alive across reconnects and restarts, persists terminal scrollback, stores workspaces/tabs/blocks in SQLite, loads config, and serves the `wshrpc` RPC and `wps` event API that clients talk to.
- **wsh** — the CLI helper wavesrv installs into local and remote shells; it talks back to wavesrv over `wshrpc`.

The client is the native macOS app, [thoro/wave-native](https://github.com/thoro/wave-native), which bundles and launches wavesrv.

## Releases

Each [release](https://github.com/thoro/wavesrv/releases) attaches the Go binaries:

| Asset                             | Platforms                                           |
| --------------------------------- | --------------------------------------------------- |
| `wavesrv.arm64`, `wavesrv.x64`    | macOS                                               |
| `wsh-<version>-<os>.<arch>[.exe]` | darwin, linux, windows (x64, arm64); linux mips, mips64 |

Releases are cut from `main`: release-drafter keeps a draft release up to date from merged PRs (a `feat/` branch or `feat:` title bumps minor, `major`/`breaking` labels bump major, everything else is a patch), and publishing the draft runs [`release.yml`](.github/workflows/release.yml), which builds and uploads the binaries.

## Building

Requires Go (see `go.mod` for the version) and, for wavesrv, a C toolchain for CGO/SQLite (Xcode Command Line Tools on macOS).

```sh
# wavesrv
CGO_ENABLED=1 go build -tags "osusergo,sqlite_omit_load_extension" \
    -ldflags "-X main.WaveVersion=0.0.0-dev" \
    -o dist/bin/wavesrv.arm64 cmd/server/main-server.go

# wsh for one target (wavesrv expects the wsh-<version>-<os>.<arch> naming)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
    -ldflags "-s -w -X main.WaveVersion=0.0.0-dev" \
    -o dist/bin/wsh-0.0.0-dev-darwin.arm64 cmd/wsh/main-wsh.go

# tests
go test -race ./pkg/...
```

[`release.yml`](.github/workflows/release.yml) has the full cross-compile matrix. With wave-native checked out at `./wave-native`, `make wavesrv` there rebuilds its bundled server from this tree.

## Running

wavesrv is normally started by the client. It reads its environment once at startup:

| Variable                   | Meaning                                                                                                   |
| -------------------------- | --------------------------------------------------------------------------------------------------------- |
| `WAVETERM_DATA_HOME`       | **Required.** Data dir: SQLite databases, terminal scrollback, sockets.                                    |
| `WAVETERM_CONFIG_HOME`     | **Required.** Config dir (`settings.json`, connections, widgets, …).                                       |
| `WAVETERM_APP_PATH`        | App dir; wsh binaries are looked up in its `bin/` as `wsh-<version>-<os>.<arch>[.exe]`.                     |
| `WAVETERM_WEB_UNIX_SOCKET` | If set, serve the web API and websocket on `web.sock` / `ws.sock` in the data dir instead of loopback TCP. |

Once it is ready it prints `WAVESRV-ESTART ws:<addr> web:<addr> version:<version> buildtime:<time>` on stderr.

## Layout

- `cmd/server`: wavesrv entry point
- `cmd/wsh`: wsh entry point
- `pkg/`: backend packages (`blockcontroller`, `remote/conncontroller`, `jobcontroller`, `filestore`, `wstore`, `wshrpc`, `wps`, `wconfig`, …)
- `db/`: embedded SQLite migrations
- `schema/`: JSON schemas for the config files

## License

Apache-2.0. Based on Wave Terminal by Command Line Inc.; see [LICENSE](LICENSE), [NOTICE](NOTICE) and [ACKNOWLEDGEMENTS.md](ACKNOWLEDGEMENTS.md).
