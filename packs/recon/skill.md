# Recon Pack — Agent Skill

This file is read by MINTI agents to learn what tools the recon pack provides
and the safe pattern for invoking each one. The actual command execution
happens through `minti-mcp-recon` — never run these binaries directly from the
shell server unless the user explicitly asks.

## Tools (via `minti-mcp-recon`)

### `nmap_scan`
- **Purpose:** TCP service discovery + version detection on a target.
- **Safe default:** `nmap -sV -T3 --top-ports 1000 <target>` (connect scan, no
  root needed, top-1000 ports).
- **Privileged option:** Set `raw_socket: true` to use `-sS` (SYN/stealth).
  Requires `mcp.recon.allow_raw_socket: true` in `~/.minti/policy.yaml`. Off
  by default.
- **Target accepted forms:** IPv4, IPv6, hostname, CIDR (e.g. `10.0.0.0/24`).
- **When to use:** When the user asks "what's running on host X" or "is port Y
  open." Always confirm the target is owned/authorized by the user before
  running.

### `whois`
- **Purpose:** Domain registration and contact lookup.
- **Underlying:** Debian `whois` package.
- **When to use:** For domain ownership questions, expiration dates,
  registrant info. Read-only; no rate-limiting concerns for ad-hoc lookups.

### `dig_lookup`
- **Purpose:** DNS record resolution.
- **Underlying:** `dig` from `dnsutils` package.
- **Default record type:** `A`. Override via the `type` argument (`AAAA`,
  `MX`, `TXT`, `NS`, `SOA`, `CAA`, etc.).
- **Output:** Short form (`+short`).

### `http_probe`
- **Purpose:** Quick HEAD request to identify a web server's status, Server
  header, and other response headers. No body fetched — use `minti-mcp-http`
  for that.
- **Use case:** "What server is at https://example.com?" / "Is this URL
  reachable?"

## Tools NOT installed by default

These are commonly useful for recon but are not in stock Debian 12:

- **rustscan** (faster nmap front-end). Install:
  `cargo install rustscan` — requires Rust toolchain.
- **dnsx** (DNS toolkit from ProjectDiscovery). Install:
  `go install -v github.com/projectdiscovery/dnsx/cmd/dnsx@latest`.
- **naabu** (port scanner). Install:
  `go install -v github.com/projectdiscovery/naabu/v2/cmd/naabu@latest`.
- **amass** (subdomain enumeration). Available in some Debian releases via
  contrib; if missing: `go install -v github.com/owasp-amass/amass/v4/...@master`.

Once installed, these can be invoked via `minti-mcp-shell` (subject to its
policy), and a future iteration of `minti-pack-recon` will wrap them as
first-class `minti-mcp-recon` tools.

## Safety rules — REQUIRED reading

1. **Authorization gate.** Before scanning any target the user does not
   explicitly own, ask for written authorization. Half of the use-cases for
   these tools are legal grey areas.
2. **Default to safe flags.** Never escalate to SYN scans, OS detection,
   script scans, or aggressive timing without the user explicitly approving.
3. **Rate limits.** `theHarvester` and `amass` hit external APIs. For
   anything iterative, throttle to avoid getting the user's IP rate-limited
   or banned from search engines.
4. **Stay local first.** When debugging the agent itself, default to
   `127.0.0.1` or `localhost` for test scans, not external hosts.
5. **Audit log.** Every tool call lands in `~/.minti/audit.jsonl`. If the user
   asks "what did you scan today" — read that file.
