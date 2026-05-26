# Claude Code preset for MINTI

This document is for users who already have Anthropic's **Claude Code** CLI
installed and want to route it at their local MINTI stack. It is **not
bundled** — MINTI never ships proprietary software (see [PRD §2a / P1, open-source-first](../README.md)). Open-weight clients like [`opencode`](https://opencode.ai) and OpenHands are the default. This preset is here so that
users who already pay for Claude Code can point it at MINTI's `minti-runtime` for
*local* model inference and at MINTI's MCP servers for tool execution, without
losing the agent UX they're used to.

## What this preset gives you

| Component | What gets routed | Notes |
|---|---|---|
| Chat completions | Claude Code → `http://127.0.0.1:7780/v1/messages` → minti-runtime → Ollama → a local open-weight model | Text-only in M3 — tool-use content blocks are M3.5+ |
| MCP tools | Claude Code spawns `/opt/minti/mcp/minti-mcp-*` directly over stdio | Same binaries opencode and `mcptest` use; same `/etc/minti/policy.yaml` gates them; same `~/.minti/audit.jsonl` records them |

The point is **uniformity of policy + audit across agent clients**. Whether you
drive MINTI via opencode, mcptest, or Claude Code, the tools route through the
same MCP servers under the same policy file.

## Prerequisites

- MINTI installed (`install.sh` ran clean; `minti-runtime.service` is `active`).
- A local model pulled in Ollama: `ollama pull llama3.2:3b` (or one of the
  open-weight options listed in [`/etc/minti/opencode.config.example.json`](../install/opencode.config.example.json)).
- Claude Code installed: follow Anthropic's installer at
  <https://docs.claude.com/en/docs/claude-code/setup>.

## Step 1 — Route chat at the local runtime

Claude Code reads two environment variables for its API target:

```sh
export ANTHROPIC_BASE_URL="http://127.0.0.1:7780"
export ANTHROPIC_API_KEY="minti-local-no-auth"   # any non-empty string works
```

`minti-runtime` accepts and ignores the `x-api-key` and `anthropic-version`
headers — there is no remote service to authenticate against; the request never
leaves your machine. The `ANTHROPIC_API_KEY` value is therefore arbitrary, but
it must be set or Claude Code refuses to start.

To make this persistent, add the exports to `~/.bashrc` / `~/.zshrc`, or write
them into `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:7780",
    "ANTHROPIC_API_KEY":  "minti-local-no-auth"
  },
  "model": "llama3.2:3b"
}
```

(`model` is whatever you've pulled in Ollama.)

## Step 2 — Wire the MCP servers

Claude Code discovers MCP servers from its own config, not from
`/etc/opencode/`. Register each MINTI server by binary path:

```sh
claude mcp add minti-fs    /opt/minti/mcp/minti-mcp-fs
claude mcp add minti-shell /opt/minti/mcp/minti-mcp-shell
claude mcp add minti-recon /opt/minti/mcp/minti-mcp-recon
claude mcp add minti-pkg   /opt/minti/mcp/minti-mcp-pkg
claude mcp add minti-http  /opt/minti/mcp/minti-mcp-http
```

These commands edit `~/.claude.json` for you. To verify:

```sh
claude mcp list
```

You should see five `minti-*` entries marked `stdio` with the right paths.

## Step 3 — Verify

```sh
claude
# In the TUI:
#  > what user am I and where is my home dir?
# Claude will pick up minti-fs, render a consent prompt for the read_text /
# list_dir call, run it, and report the result.
```

If the consent prompt doesn't appear and Claude refuses to call the tool, check
`~/.claude/settings.json` for a `permissions` block that may have pre-approved
or pre-denied the tool. The MINTI side records the call in `~/.minti/audit.jsonl`
either way:

```sh
tail -3 ~/.minti/audit.jsonl
```

You should see a structured event with `server: "minti-mcp-fs"` and
`decision: "allow"`.

## Limitations in M3

- **No tool-use over `/v1/messages` yet.** Claude Code sends and receives tool
  calls as JSON content blocks within messages. minti-runtime currently
  translates text-only content blocks and rejects `tool_use` / `tool_result`
  blocks with a 400 and a clear error. Tool execution still works because
  Claude Code's MCP layer talks to the MINTI MCP servers directly over stdio —
  not through `/v1/messages`. The combined picture: chat reasoning runs on
  the local model via the API, tools run via stdio. Mixed tool-driven
  conversations land in **M3.5** alongside the runtime-adapter's tool-call
  translation.
- **Single-Clan routing.** The runtime is `localhost`-only in M3. Routing
  inference across Clan members is M4 (cland). Until then, the chat reasoning
  pinned to whatever model you've pulled in your local Ollama.
- **No Claude Code feature parity.** This preset gives you Claude Code's TUI
  pointed at *your local model* — not Opus/Sonnet capabilities. If you want
  Claude's frontier reasoning, leave `ANTHROPIC_BASE_URL` unset and use the
  real API. If you want fully local reasoning, the preset above lets the same
  UX drive open-weight models on your hardware.

## Why this exists despite P1

PRD §2a P1 says MINTI never *depends* on closed-source software. This preset
respects that: nothing in the MINTI install path requires Claude Code, mentions
Claude Code in the default config, or fails to function without it. Users who
already have Claude Code on their box can opt-in by exporting two env vars.
opencode (bundled, MIT) and OpenHands (optional, MIT) remain the supported
defaults.
