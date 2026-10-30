# wavesrv - High Level Architecture Overview

## Project Description

wavesrv is the Go backend of Wave Terminal. It owns the workspace / tab / block model, runs local and remote (SSH, WSL) shells, keeps durable shell jobs alive across client restarts, stores terminal output, and serves all of it to clients over `wshrpc`. The client is the native macOS app (thoro/wave-native), which launches wavesrv and talks to it over Unix domain sockets. `wsh` is the CLI that runs inside terminals, locally and on remote hosts.

## Top-Level Directory Structure

```
wavesrv/
├── cmd/                # Go programs: server (wavesrv), wsh, test/debug tools
├── pkg/                # Go packages/modules
├── db/                 # Database migrations
├── schema/             # JSON schemas for the config files (served by wavesrv)
├── tsunami/            # Tsunami module (replace => ./tsunami, required by the server)
├── aiprompts/          # Design notes for backend subsystems
├── tests/              # Test files
└── .github/            # GitHub workflows and configuration
```

## Go Backend Server (`cmd/server/`)

The Go backend server handles all heavy lifting operations:

**Entry Point:** [`main-server.go`](cmd/server/main-server.go)

## Go Packages (`pkg/`)

The Go codebase is organized into modular packages:

**Key Packages:**

- `wstore/` - Database and storage layer
- `wconfig/` - Configuration management
- `wcore/` - Core business logic
- `wshrpc/` - RPC communication system
- `wshutil/` - WSH (Wave Shell) utilities
- `blockcontroller/` - Block execution management
- `jobcontroller/`, `jobmanager/` - Durable shell jobs that outlive the client
- `remote/` - Remote connection handling
- `wslconn/` - WSL connection handling
- `filestore/` - File storage system (terminal output, block files)
- `web/` - Web server and WebSocket handling
- `telemetry/` - Usage analytics and telemetry
- `waveobj/` - Core data objects
- `service/` - Service layer
- `wps/` - Wave PubSub event system
- `aiusechat/` - AI functionality
- `taskapi/` - Loopback HTTP tasks API for containers
- `shellexec/` - Shell execution
- `util/` - Common utilities

## Command Line Tools (`cmd/`)

- `wsh/` - Wave Shell command-line tool
- `server/` - Main backend server
- `test*/`, `packfiles/` - Development and debugging tools

## Communication Architecture

The core communication system is built around the **WSH RPC (Wave Shell RPC)** system, which provides a unified interface for all inter-process communication: client ↔ Go backend, `wsh` ↔ backend, and backend ↔ remote systems (SSH, WSL).

### WSH RPC System (`pkg/wshrpc/`)

The WSH RPC system is the backbone of Wave Terminal's communication architecture:

**Key Components:**

- [`wshrpctypes.go`](pkg/wshrpc/wshrpctypes.go) - Core RPC interface and type definitions (source of truth for all RPC commands)
- [`wshserver/`](pkg/wshrpc/wshserver/) - Server-side RPC implementation
- [`wshremote/`](pkg/wshrpc/wshremote/) - Remote connection handling
- [`wshclient/`](pkg/wshrpc/wshclient/) - Go client for making RPC calls

**Routing:** Callers address RPC calls using _routes_ (e.g. a block ID, connection name, or `"wavesrv"`) rather than caring about the underlying transport. The RPC layer resolves the route to the correct transport (WebSocket, Unix socket, SSH tunnel, stdio) automatically. This means the same RPC interface works whether the target is local or a remote SSH connection.

## Development Notes

- **Build commands** - Plain `go build` / `go test`; see README.md for the build tags and the release workflow
- **Code generation** - None any more: `pkg/wshrpc/wshclient/wshclient.go`, `pkg/wconfig/metaconsts.go`, `pkg/waveobj/metaconsts.go` and `schema/` are maintained by hand despite their "Generated Code" headers
- **Testing** - Standard `go test -race` for Go packages
- **Database migrations** - SQL migration files in `db/migrations-wstore/` and `db/migrations-filestore/`
