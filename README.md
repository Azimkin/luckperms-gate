# LuckPerms (Gate port)

A read-only port of the [LuckPerms](https://luckperms.net/) Velocity module for the
[Gate](https://gate.minekube.com/) proxy.

This plugin lets a Gate proxy act as a node in an existing LuckPerms network. It
reads permission data from the **same PostgreSQL database** used by the rest of the
network and stays in sync via Postgres `LISTEN`/`NOTIFY`, so a player's permissions
on the proxy match their permissions on the backend servers.

## Features

- **Permissions** — resolves a player's effective permissions exactly like the
  LuckPerms Velocity module: group inheritance (weighted, depth-first), `server`/
  `world` contexts, node expiry, and the `Direct`, `Regex`, `Wildcard`, and
  `Sponge` wildcard processors.
- **Messaging** — consumes LuckPerms `update` / `userupdate` messages over the
  `luckperms:update` Postgres channel and refreshes its caches live. Fully wire
  compatible with Java LuckPerms instances.
- **Storage** — PostgreSQL, **read-only**. The proxy never writes to the database.
- **Configuration** — a LuckPerms-compatible `config.yml`. An existing LuckPerms
  config can be reused as-is (unknown keys are ignored).
- **Operator permission** — an optional Gate-specific shortcut (see below).

> **Note:** This is a *reimplementation* of the relevant behaviour, not a Java
> transpilation. There is no editing/command support — manage permissions with a
> real LuckPerms instance on a backend server.

## Installation

This is a self-contained Go package. To add it to your own Gate plugin project
(a normal Go program that imports `go.minekube.com/gate` and calls `gate.Execute()`):

### 1. Copy the package

Drop this `luckperms` folder into your module, e.g. `plugins/luckperms/`:

```
your-gate-project/
├── go.mod                 # module example.com/my-gate
├── main.go                # or gate.go
└── plugins/
    └── luckperms/         # ← copy this folder here
        ├── plugin.go
        ├── config.go
        └── ...
```

The Go package name stays `luckperms`; its import path follows your module path —
for the layout above it is `example.com/my-gate/plugins/luckperms`.

### 2. Add dependencies

The only dependency you likely don't already have is the PostgreSQL driver:

```sh
go get github.com/jackc/pgx/v5
```

The package also imports these, which a Gate plugin project normally already pulls
in transitively via `go.minekube.com/gate` (running `go mod tidy` in step 4 will add
any that are missing):

| Import                          | Purpose                              |
|---------------------------------|--------------------------------------|
| `go.minekube.com/gate`          | proxy, events, permission, uuid APIs |
| `github.com/jackc/pgx/v5`       | PostgreSQL pool + LISTEN/NOTIFY      |
| `gopkg.in/yaml.v3`              | config parsing                       |
| `github.com/go-logr/logr`       | logging                              |
| `github.com/robinbraemer/event` | event subscription                   |

### 3. Register the plugin

Add `luckperms.Plugin` to the proxy's plugin list (adjust the import path to your
module):

```go
package main

import (
    "example.com/my-gate/plugins/luckperms"
    "go.minekube.com/gate/cmd/gate"
    "go.minekube.com/gate/pkg/edition/java/proxy"
)

func main() {
    proxy.Plugins = append(proxy.Plugins,
        luckperms.Plugin,
    )
    gate.Execute()
}
```

(In this repository it is wired up in [`gate.go`](../../gate.go).)

### 4. Tidy and build

```sh
go mod tidy
go build ./...
```

### 5. Configure

On first run the plugin writes a default `luckperms/config.yml` next to the working
directory and logs that you must edit it with your database credentials — see
[Configuration](#configuration) below. Override the path with the `LUCKPERMS_CONFIG`
environment variable.

## Configuration

On first run the plugin writes a default config to `luckperms/config.yml`
(relative to the proxy's working directory) and logs that you should edit it. Set
the location with the `LUCKPERMS_CONFIG` environment variable.

```yaml
# The name of this server, used as the "server" context for permission lookups.
# Must match the value LuckPerms uses for this part of the network.
server: global

# Only PostgreSQL is supported by this port.
storage-method: postgresql

data:
  address: localhost:5432      # "host" or "host:port"
  database: minecraft
  username: root
  password: ''
  pool-settings:
    maximum-pool-size: 10
  table-prefix: 'luckperms_'

# "postgresql" (or "auto") uses Postgres LISTEN/NOTIFY for live updates.
# Anything else disables cross-network sync.
messaging-service: postgresql

# Permission resolution flags (same meaning as LuckPerms).
include-global: true
include-global-world: true
apply-global-groups: true
apply-global-world-groups: true
apply-wildcards: true
apply-sponge-implicit-wildcards: false
apply-regex: true

# at-least-one-value-per-key (default) or all-values-per-key
context-satisfy-mode: at-least-one-value-per-key

# Players holding this permission are treated as operators: any permission check
# that would otherwise be undefined resolves to true. Explicit grants/denials
# still take precedence. Leave empty ('') to disable.
op-permission: gate.operator
```

### Operator permission

`op-permission` is a convenience for the proxy that does not exist in upstream
LuckPerms. When a player is granted the configured node (default `gate.operator`),
any permission check that would otherwise be **undefined** resolves to **true** —
effectively granting all proxy permissions. Explicit grants and denials are always
respected, so you can still deny specific nodes to an operator. Set it to an empty
string to disable the behaviour entirely.

## How it works

1. On `PermissionsSetupEvent` (fired during login, before the player connects to a
   backend), the plugin loads the player and their permission nodes from the
   database, then installs a permission function via `SetFunc`.
2. The permission function resolves checks against the cached user + group data
   using the player's `server` context.
3. A background goroutine `LISTEN`s on the `luckperms:update` channel. Incoming
   `update` messages reload all data; `userupdate` messages reload a single user.
4. On `DisconnectEvent` the player's cached data is dropped.

### Database tables read

| Table                          | Columns used                                                  |
|--------------------------------|---------------------------------------------------------------|
| `<prefix>players`              | `uuid`, `username`, `primary_group`                           |
| `<prefix>user_permissions`     | `permission`, `value`, `server`, `world`, `expiry`, `contexts`|
| `<prefix>group_permissions`    | `name`, `permission`, `value`, `server`, `world`, `expiry`, `contexts` |
| `<prefix>groups`               | `name`                                                        |

## Testing

Unit tests cover the resolution engine (direct allow/deny, inheritance, user
overrides, weighted groups, expiry, wildcard/root-wildcard, regex, server context,
`include-global`, and the operator fallback):

```sh
go test ./plugins/luckperms/
```

Manual / integration checks:

- Point the config at a PostgreSQL database populated by a real LuckPerms install,
  start the proxy, and verify a player's `HasPermission` results.
- Trigger a sync manually:

  ```sql
  SELECT pg_notify(
    'luckperms:update',
    '{"id":"<uuid>","type":"userupdate","content":{"userUuid":"<player-uuid>"}}'
  );
  ```

  The plugin should log the update and reload that user.

## Extending: custom storage / messaging providers

Storage and messaging are pluggable. Backends are chosen at runtime from the
config (`storage-method` / `messaging-service`) by looking them up in a registry,
so adding one means implementing an interface and registering a factory — no
changes to the resolution engine or the plugin wiring. The built-in PostgreSQL
backend is registered exactly this way (see the `init()` functions in
[`storage.go`](storage.go) and [`messaging.go`](messaging.go)).

The interfaces live in [`provider.go`](provider.go):

```go
type StorageProvider interface {
    LoadUser(ctx context.Context, id uuid.UUID, username string) (*User, error)
    LoadGroup(ctx context.Context, name string) (*Group, error)
    ListGroups(ctx context.Context) ([]string, error)
    Close()
}

type Messenger interface {
    Run(ctx context.Context) // blocks until ctx is cancelled; reconnects on failure
}
```

### Adding a storage backend

1. Implement `StorageProvider`. It only needs to **read** data and build `*User`
   / `*Group` values whose `Nodes` use the same `Node` shape the engine expects
   (key, value, server/world contexts defaulting to `"global"`, expiry as unix
   seconds, extra contexts). `LoadUser` must return a non-nil `*User` even for an
   unknown player.
2. Register it from an `init()`:

   ```go
   func init() {
       RegisterStorage(func(ctx context.Context, cfg *Config) (StorageProvider, error) {
           return NewMyStorage(ctx, cfg)
       }, "mysql") // matches storage-method: mysql
   }
   ```

3. Set `storage-method: mysql` in the config.

### Adding a messaging backend

1. Implement `Messenger`. `Run` should block until `ctx` is cancelled and, on
   receiving a change notification, call `manager.InvalidateUser(ctx, id)` for a
   single user or `manager.InvalidateAll(ctx)` for everything. De-dup by message
   id if your transport can deliver duplicates.
2. Register it from an `init()`:

   ```go
   func init() {
       RegisterMessenger(func(ctx context.Context, cfg *Config, store StorageProvider, manager *Manager, log logr.Logger) (Messenger, error) {
           return NewMyMessenger(cfg, manager, log), nil
       }, "redis") // matches messaging-service: redis
   }
   ```

   The factory receives the live `StorageProvider`; if your messenger needs to
   reuse the storage connection (as the Postgres one reuses the DSN), type-assert
   for the capability you need.
3. Set `messaging-service: redis` (or leave it `auto` to follow `storage-method`,
   or `none` to disable sync).

To keep message-format compatibility with a real LuckPerms network, encode/decode
the same envelope the network uses — for the SQL/Postgres transports that is the
JSON `{"id","type","content"}` shape handled in [`messaging.go`](messaging.go).

## Contributing

Contributions are welcome — bug fixes, new storage/messaging backends, closing the
gaps listed under [Limitations](#limitations).

### Development setup

This package is consumed by copying it into a Gate project, so develop it inside
one:

1. Place the package in a Gate plugin project (see [Installation](#installation)).
2. `go build ./...` and `go test ./plugins/luckperms/` from the project root.

The resolution engine is covered by unit tests in
[`resolve_test.go`](resolve_test.go) that construct `User`/`Group` fixtures
directly and need no database — run them with:

```sh
go test ./plugins/luckperms/
```

### Guidelines

- **Match upstream behaviour.** This is a reimplementation of LuckPerms' Velocity
  module; when in doubt, mirror what LuckPerms does rather than inventing new
  semantics. Reference the relevant LuckPerms class in a comment.
- **Keep storage read-only.** The proxy must never write to the shared database.
- **Add tests** for any change to resolution, context matching, or message
  decoding. Prefer fixture-based unit tests over requiring a live database.
- **Run `go build ./...`, `go vet ./...`, and `go test ./...`** before opening a PR,
  and keep `gofmt` clean.
- **Update this README** when you add a config key or a provider.

## Limitations

- Read-only: no commands and no writes. Edit permissions via a backend LuckPerms
  instance.
- `apply-shorthand` (expansion of `a.{b,c}` nodes) is not implemented, matching the
  set of processors the Velocity calculator factory installs.
- Only the static `server` context (from config) is applied; the current backend
  server name is not mixed into the context.
- Only PostgreSQL storage and Postgres/SQL-style messaging are supported.
