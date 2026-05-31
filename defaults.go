package luckperms

// defaultConfigYAML is written on first run if no config exists. It mirrors the
// relevant subset of the LuckPerms config.yml. An existing LuckPerms config.yml
// can also be used as-is (unknown keys are ignored).
const defaultConfigYAML = `# LuckPerms (Gate port) configuration.
# This proxy node reads permission data from PostgreSQL and stays in sync with
# the rest of the LuckPerms network. Storage is READ-ONLY.

# The name of this server, used as the "server" context for permission lookups.
# Must match the value used by LuckPerms on this part of the network.
server: global

# Only PostgreSQL is supported by this port.
storage-method: postgresql

data:
  # Database connection address, "host" or "host:port".
  address: localhost:5432
  database: minecraft
  username: root
  password: ''
  pool-settings:
    maximum-pool-size: 10
  table-prefix: 'luckperms_'

# Messaging service. "postgresql" (or "auto") uses Postgres LISTEN/NOTIFY to
# receive live updates from the rest of the network. Anything else disables sync.
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
`
