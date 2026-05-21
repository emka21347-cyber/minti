# MINTI

> **MINTI is a minimal Linux software stack plus a cross-OS Clan protocol.** Local AI agents are first-class citizens; any machine — old laptop, dusty Mac, current Windows box — can join a trusted Clan; whichever member is the strongest *reasoner* becomes the Orchestrator that drives agent workload for the whole group.

**Status: M0 — pre-release. Not yet installable.**

## What this is

A two-part project:

1. **MINTI Linux** — a thin software layer on top of any Debian / Mint / Ubuntu host: Ollama + agent clients + MCP tool servers + Kali-style opt-in tool packs + the Clan daemon.
2. **The Clan protocol** — a cross-OS trust group (Linux / Windows / macOS) where members share AI inference, agent work, and tool execution. The strongest reasoner is elected Orchestrator via leader-lease + heartbeat. Cardputers with paid API keys can outrank workstations with small local models.

## Why

Old hardware that would otherwise be e-waste can join a Clan and contribute compute, tools, or storage. A LAN full of trusted machines becomes a private AI workgroup — no data leaves, weights stay local, agents do real work.

## Status

| Milestone | Status |
|---|---|
| M0 — Repo + install.sh v0 + Clan Protocol Spec v0.1 | in progress |
| M1 — Per-node AI Runtime adapter | pending |
| M2 — MCP servers + first tool pack | pending |
| M3 — Agent client integration | pending |
| M4 — Clan Layer v1 (Linux) | pending |
| M5 — Cross-platform Clan Agent (Win/Mac) | pending |
| M6 — Security hardening + remaining packs | pending |
| M7 — Old-hardware smoke test | pending |
| M8 — v1.0 release | pending |

## Layout

```
cland/            — Go daemon: discovery, membership, leader-lease, routing
runtime-adapter/  — `minti-runtime`: uniform interface over Ollama / llama.cpp / remote APIs
mcp-servers/      — fs, shell, recon, http, pkg
console/          — GTK tray app (Clan-aware status, Orchestrator, audit log viewer)
pack-manager/     — CLI in v1, GTK in v1.5+
packs/            — debian metapackages: recon, webapp, wireless, forensics
branding/         — wallpapers, themes (light v1; full ISO branding in v1.5)
docs/             — Clan Protocol Spec, threat model, quickstart
install/          — install.sh + apt-repo config + post-install hooks
ansible/          — convert-existing-host playbooks (alternative to install.sh)
scripts/          — dev/build/release scripts
tests/            — integration + protocol conformance tests
```

## Specification

The Clan Protocol Spec is the source of truth for wire format and semantics: [docs/clan-protocol.md](docs/clan-protocol.md).

## License

MIT. See [LICENSE](LICENSE).
