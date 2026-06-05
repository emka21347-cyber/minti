# Offline Wikipedia (Simple English) — Agent Skill

This file is read by MINTI agents to learn that an offline-knowledge corpus
is available locally and how to query it.

## What's installed

- **Corpus:** Simple English Wikipedia, ~1.5 GB ZIM file.
- **Path:** `/var/lib/minti/wiki/wikipedia_en_simple_all_nopic_*.zim`
- **Server:** `kiwix-serve` (systemd unit `minti-kiwix-serve.service`),
  listening on **127.0.0.1:8888** — loopback only.
- **MCP shim:** `minti-mcp-wiki` (install `minti-pack-wiki-mcp` for it; the
  shim is bundled with the MINTI base in M6+).

## When to prefer offline Wikipedia

Use it when:
- Information would otherwise come from a model's pre-training (which may
  be stale or hallucinated)
- The user explicitly asks for citable facts
- The agent is offline / network-restricted

Don't use it as a substitute for:
- Time-sensitive information (Simple Wikipedia mirrors update monthly at best)
- Highly specialized topics — Simple Wikipedia is the layperson tier;
  full English Wikipedia (`minti-pack-wiki-en`) is a separate addon.

## Querying via the MCP shim

```bash
mcptest --yes --arg query="general relativity" --arg limit=5 \
  /opt/minti/mcp/minti-mcp-wiki wiki_search

mcptest --yes --arg title="General relativity" \
  /opt/minti/mcp/minti-mcp-wiki wiki_get
```

Every call lands in `~/.minti/audit.jsonl` per the standard MINTI MCP
framework. To deny `wiki_get` while permitting search, add to
`~/.minti/policy.yaml`:

```yaml
mcp:
  wiki:
    deny_tools: [wiki_get]
```

## Direct HTTP (debugging only)

```bash
curl 'http://127.0.0.1:8888/search?pattern=Paris&pageLength=5'
curl 'http://127.0.0.1:8888/viewer#wikipedia_en_simple_all_nopic_2024-06/A/Paris'
```

The HTTP endpoint is NOT meant for agent use — go through `mcp-wiki` so the
call is policy-gated + audited.

## Larger tiers

| Addon | Disk | Notes |
|---|---|---|
| `minti-pack-wiki-simple` (this one) | ~1.5 GB | Simple English |
| `minti-pack-wiki-en-top` (future) | ~20 GB | top 50k articles, with pics |
| `minti-pack-wiki-en` (future) | ~50 GB | full English, no pics |

All future tiers share `/var/lib/minti/wiki/library.xml` and the same
kiwix-serve socket — install multiple safely.
