# MINTI on Windows — the full stack ("Add MINTI to this machine")

This is the door-B Windows installer: the complete MINTI stack packaged as
one `.zip`. Installation registers **three** Windows Services via
[NSSM 2.24](https://nssm.cc) and opens the workspace in your browser when
it finishes.

| Service | What it is | Listens | Account |
|---|---|---|---|
| `Minti-Cland` | Clan daemon (election, peers, tool exec) | `:7777` TLS+HMAC — **the only open port** | `LocalSystem` |
| `Minti-Runtime` | runtime adapter (OpenAI/Ollama/Anthropic API shapes over local models) | `127.0.0.1:7780` loopback | `NT SERVICE\Minti-Runtime` |
| `Minti-Workspace` | the web UI | `127.0.0.1:8088` loopback | `NT SERVICE\Minti-Workspace` |

> **You will see security warnings — that's expected, not a malfunction.**
> The binaries are not yet code-signed. SmartScreen will interpose on the
> downloaded zip/scripts ("Windows protected your PC" → **More info → Run
> anyway**), and Defender may need a Folder Exclusion (below). Verify the
> zip's SHA-256 against the one published on the download page *before*
> clicking through any warning. Signed installers are planned; until then
> the warning is part of the product.

---

## Install

**The one-click path:** extract the zip, double-click **`Install-MINTI.cmd`**,
accept the UAC prompt. The shim only elevates and runs `install-minti.ps1`
from the same folder — its header says exactly that.

**The by-hand path** (same result), from an elevated PowerShell:

```powershell
Expand-Archive minti-windows-amd64-v0.4.0-D1.zip -DestinationPath C:\Temp\minti-install
cd C:\Temp\minti-install
powershell -ExecutionPolicy Bypass -File install-minti.ps1
```

When it finishes it opens `http://127.0.0.1:8088` (the workspace) in your
default browser — de-elevated, via `explorer.exe`.

Defaults (override as parameters):

| Knob | Default | Why |
|---|---|---|
| `-InstallRoot` | `%PROGRAMFILES%\MINTI` | binaries: `cland\`, `runtime\`, `workspace\` |
| `-StateRoot` | `%PROGRAMDATA%\MINTI` | per-service state + logs; survives uninstall |
| `-ClandPort` | `7777` | inbound Clan TLS; the one firewall rule |
| `-WorkspacePort` | `8088` | loopback UI port |
| `-FirewallProfile` | `Private,Domain` | LAN-local by default — see "Network profile gotcha" |
| `-Unattended` | off | skips the Ollama download-page prompt |
| `-NoAutoOpen` | off | suppresses the browser open |

The runtime's port lives in `%PROGRAMDATA%\MINTI\runtime\runtime.yaml`
(default `7780`), not in an installer parameter.

### What exactly gets written

- `%PROGRAMFILES%\MINTI\{cland,runtime,workspace}\` — the three binaries +
  a per-service `nssm.exe` copy.
- `%PROGRAMDATA%\MINTI\{cland,runtime,workspace}\` — configs, state, `logs\`.
- Three service registrations (`HKLM\SYSTEM\...\Services\Minti-*`).
- One inbound firewall rule (`MINTI cland (Clan TLS)`, TCP 7777, bound to
  the cland binary path).

Nothing else. No system PATH edits, no scheduled tasks, no telemetry.

---

## Already running the M5-B cland-only service?

Re-running this installer **upgrades it in place**: same `Minti-Cland`
service name, same paths, state + configs preserved, no remove/re-add. The
runtime + workspace services are added alongside. Your `member_id` and any
Clan membership survive.

---

## Verify

```powershell
Get-Service Minti-Cland, Minti-Runtime, Minti-Workspace   # all Running
Test-NetConnection -ComputerName 127.0.0.1 -Port 7777
Invoke-WebRequest http://127.0.0.1:7780/minti/health -UseBasicParsing
Invoke-WebRequest http://127.0.0.1:8088/ -UseBasicParsing  # the workspace
& "$env:ProgramFiles\MINTI\cland\minti-cland.exe" show
```

---

## Ollama (the models)

The installer **detects** Ollama (PATH, default install location, and a
`127.0.0.1:11434/api/version` probe) and **guides** you to
<https://ollama.com/download/windows> when it's missing — it never
downloads or runs third-party installers itself.

MINTI works without Ollama (the runtime reports its backend honestly at
`/minti/health` and picks it up the moment it appears). With Ollama
running, pull a starter model:

```powershell
ollama pull llama3.2:3b      # ~2 GB, CPU-friendly
ollama pull hermes3:8b       # ~5 GB, agent-tuned (recommended with >=12 GB RAM)
```

**Hardware honesty:** a 3B model wants ~4 GB free RAM; an 8B model ~8-10 GB.
On a 4 GB machine stick to the smallest models — heavy swapping looks like
"MINTI broke my machine" and it isn't MINTI.

---

## Joining a Clan from this host

```powershell
& "$env:ProgramFiles\MINTI\cland\minti-cland.exe" join `
    --mnemonic "<12 BIP39 words>" `
    --address <founder-ip>:7777 `
    --pin sha256:<hex>
Restart-Service Minti-Cland
```

Or use the knock flow from the workspace UI of an existing member.

---

## Upgrading

Re-run the installer from a newer zip. It is idempotent: stops the
services (workspace → runtime → cland), swaps binaries, preserves every
config + the state dirs + DACLs, refreshes service settings, restarts
(cland → runtime → workspace). If anything fails midway, fix the cause and
re-run — that *is* the repair path.

---

## Uninstall

```powershell
# State preserved by default — re-installing reuses the same member_id.
powershell -ExecutionPolicy Bypass -File uninstall-minti.ps1

# Full wipe (member identity + clan_key destroyed; re-join from scratch):
powershell -ExecutionPolicy Bypass -File uninstall-minti.ps1 -Purge
```

Works on any subset — a box with only the M5-B cland service uninstalls
cleanly too. Ollama is never touched (remove it via Settings → Apps).

---

## Security model

| Layer | Mechanism | Notes |
|---|---|---|
| Binary integrity | NSSM SHA-256 pinned via `bin\nssm.sha256` | installer fails closed on mismatch. MINTI binaries themselves are unsigned for now — verify the zip hash from the download page. |
| State dir access | NTFS DACL per dir: SYSTEM + Administrators full control, inheritance off | cland's holds `identity.json` (Ed25519 key) + `clan.json` (`clan_key`). |
| Least privilege | runtime + workspace run as `NT SERVICE\` virtual accounts | runtime: Modify on its own state only. workspace: Modify on its own state + **Read** on cland's (its shelled `minti-cland` CLI reads identity/config; a write attempt fails closed into demo data). A workspace compromise yields Clan-scoped key read — not SYSTEM. |
| Inbound network | one firewall rule, scoped to the cland binary path, profiles `Private,Domain` | runtime + workspace are loopback-bound: unreachable from the LAN by construction. |
| Workspace exposure | loopback-only until PIN/bearer auth lands | do not reverse-proxy it onto the LAN. |

The workspace service environment intentionally sets
`LOCALAPPDATA=%PROGRAMDATA%`: the cland CLI it shells resolves its default
config/state from `%LOCALAPPDATA%` when not running as LocalSystem, and
the service account's own profile would be the wrong place. It's scoped to
that service's registry key and removed with the service.

---

## Network profile gotcha

Windows 11 categorises new networks as **Public** by default. The firewall
rule defaults to `Private,Domain`, so on a Public-categorised network
inbound :7777 is blocked and peers can't reach this host (outbound still
works). Fixes (any one):

- Settings > Network & Internet > your network > Network profile → **Private**.
- Re-run the installer with `-FirewallProfile Private,Domain,Public`.
- `Set-NetFirewallRule -DisplayName "MINTI cland (Clan TLS)" -Profile Private,Domain,Public`

---

## Defender guidance (until code-signing)

Unsigned LocalSystem services binding mDNS (UDP 5353) + TCP 7777 are a
known Defender behavioural-detection trigger — cland can run fine for
minutes and then be silently quarantined. Pre-emptive exclusions (elevated
PowerShell):

```powershell
Add-MpPreference -ExclusionPath "$env:ProgramFiles\MINTI"
Add-MpPreference -ExclusionPath "$env:ProgramData\MINTI"
```

The installer prints this but doesn't auto-apply it (the Defender module
isn't on every SKU; corporate Group Policy may override). On managed
machines ask IT to allow-list the binaries instead.

---

## NSSM-side tuning

Each service is NSSM-wrapped with: `SERVICE_AUTO_START`, log rotation
(10 MiB, online), `AppStopMethodConsole/Window 5500` ms (the Go daemons
shut down in 3 s; 5.5 s NSSM grace stays under the SCM ~10 s threshold).
Override post-install with the service's own copy, e.g.:

```powershell
& "$env:ProgramFiles\MINTI\runtime\nssm.exe" set Minti-Runtime <Key> <value>
```

A host without Ollama that should still be Orchestrator-eligible can set
cland's escape hatch (reasoning score is otherwise penalised by the
missing backend):

```powershell
& "$env:ProgramFiles\MINTI\cland\nssm.exe" set Minti-Cland AppEnvironmentExtra "MINTI_CLAND_FORCE_HEALTHY=1"
Restart-Service Minti-Cland
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| A service shows Stopped after install | launch failure — permissions or config parse | `Get-Content $env:ProgramData\MINTI\<svc>\logs\err.log -Tail 50` |
| Workspace shows demo/unconfigured data | cland not running, or pre-Clan | `Get-Service Minti-Cland`; create/join a Clan; the UI self-heals per request |
| Workspace page never loads | foreign process on 8088 (installer would have refused), or service down | `Get-NetTCPConnection -LocalPort 8088 -State Listen` |
| `Test-NetConnection :7777` fails from a peer | firewall profile mismatch | see "Network profile gotcha" |
| Service stops minutes after start | Defender behavioural block | Folder Exclusions (above) |
| Re-install fails copying an exe | service didn't fully stop | `Stop-Service <name>; Start-Sleep 2;` re-run installer |
| SmartScreen blocks the scripts | unsigned download (expected) | verify the zip SHA-256, then More info → Run anyway |
