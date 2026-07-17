# mcp-servers

MINTI's MCP tool servers and shared framework. Each `cmd/mcp-*` is a small Go binary that an agent client (`opencode`, `mcptest`, Claude Code preset) spawns over stdio JSON-RPC to perform work on the local system.

## Servers (M2)

| Binary | Purpose |
|---|---|
| `minti-mcp-fs` | Filesystem read/write/list, home-jailed |
| `minti-mcp-shell` | Shell execution with policy modes (prompt / allowlist / deny) |
| `minti-mcp-recon` | nmap / whois / dig / http-probe with safe-flag defaults |
| `minti-mcp-http` | HTTP fetch (size-capped, no JS) |
| `minti-mcp-pkg` | apt-get search / install (sudo-gated) |
| `mcptest` | stdio MCP test client — renders consent prompts on TTY |

## Shared framework

- `internal/policy/` — loads `/etc/minti/policy.yaml` + `~/.minti/policy.yaml` (user overrides system) — schema per `docs/clan-protocol.md` §7.2.
- `internal/audit/` — appends one JSON event per line to `~/.minti/audit.jsonl`.
- `internal/permission/` — `Check(server, tool, args) → Allow|Deny` consulted before every tool execution.
- `internal/mcpserve/` — wraps `github.com/modelcontextprotocol/go-sdk/mcp` so every registered tool runs through policy + audit transparently.

## Build

```sh
make mcp           # native
make mcp-linux     # cross-compile linux/amd64
```

Binaries are installed by `install/install.sh` to `/opt/minti/mcp/`.
