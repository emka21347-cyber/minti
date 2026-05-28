# minti-cland on Windows (NSSM-managed service)

This is the Windows installer for `minti-cland` — the MINTI Clan daemon —
packaged as a `.zip` distribution. Installation registers `minti-cland.exe`
as a Windows Service via [NSSM 2.24](https://nssm.cc) and configures
inbound firewall + logging.

> **No code-signing yet.** The bundled binary is unsigned; Windows Defender
> + SmartScreen will warn on first run. M6 ships signed installers; until
> then, see the "Defender guidance" section below.

---

## Install (operator workflow)

From an elevated PowerShell:

```powershell
Expand-Archive minti-cland-windows-amd64-v0.2.0-M5.zip -DestinationPath C:\Temp\cland-install
cd C:\Temp\cland-install
powershell -ExecutionPolicy Bypass -File install-cland.ps1
```

Defaults:

| Knob | Default | Why |
|---|---|---|
| `-InstallRoot` | `%PROGRAMFILES%\MINTI\cland` | binaries (cland.exe + nssm.exe) |
| `-StateRoot` | `%PROGRAMDATA%\MINTI\cland` | clan.json, identity.json, audit.jsonl |
| `-LogRoot` | `$StateRoot\logs` | NSSM-rotated stdout/stderr |
| `-Port` | `7777` | inbound Clan TLS (HMAC-on-self-signed-cert) |
| `-ServiceName` | `Minti-Cland` | shown in `Get-Service` |
| `-FirewallProfile` | `Private,Domain` | LAN-local only by default — see "Network profile" |

Override any of them at install:

```powershell
powershell -ExecutionPolicy Bypass -File install-cland.ps1 -Port 17777 -FirewallProfile Private,Domain,Public
```

---

## Verify

```powershell
Get-Service Minti-Cland                         # → Status: Running, StartType: Automatic
Test-NetConnection -ComputerName 127.0.0.1 -Port 7777
Get-Content $env:ProgramData\MINTI\cland\logs\cland.out.log -Tail 30
& "$env:ProgramFiles\MINTI\cland\minti-cland.exe" show       # → member_id + (unaffiliated) initially
```

---

## Joining a Clan from this Windows host

After install, the host has a fresh `member_id` but no Clan. To join one
that's already running:

```powershell
& "$env:ProgramFiles\MINTI\cland\minti-cland.exe" join `
    --mnemonic "<12 BIP39 words>" `
    --address <orchestrator-ip>:7777 `
    --pin sha256:<hex>
```

Restart the service for it to pick up the new clan state:

```powershell
Restart-Service Minti-Cland
```

You should see this host in the Orchestrator's `peers` output within ~30 s
(advertise tick) and in the election history within ~10 s thereafter.

---

## Upgrading

```powershell
# Stop the running service so cland.exe is unlocked.
Stop-Service Minti-Cland

# Drop the new binary in (or re-run install-cland.ps1, which is idempotent —
# preserves state + refreshes binaries + NSSM config).
Copy-Item .\new-minti-cland.exe "$env:ProgramFiles\MINTI\cland\minti-cland.exe" -Force

Start-Service Minti-Cland
```

The recommended pattern is **always re-running `install-cland.ps1`** from a
fresh build — it handles binary swap + NSSM config refresh + firewall rule
re-application in one shot, with the same idempotency guarantees as a fresh
install.

---

## Uninstall

```powershell
# State preserved by default — re-installing reuses the same member_id.
powershell -ExecutionPolicy Bypass -File uninstall-cland.ps1

# Full wipe (member_id + clan_key destroyed; host has to re-join from scratch):
powershell -ExecutionPolicy Bypass -File uninstall-cland.ps1 -Purge
```

---

## Security model

| Layer | Mechanism | Notes |
|---|---|---|
| Binary integrity | NSSM SHA-256 pinned via `bin\nssm.sha256` | install script fails closed on mismatch. cland.exe itself is unsigned in v1 (M6 work). |
| State directory access | NTFS DACL: SYSTEM + Administrators full control, no inheritance | Set by `install-cland.ps1` via `icacls /inheritance:r`. Holds `identity.json` (Ed25519 private key) + `clan.json` (32-byte `clan_key`). |
| Inbound network | Windows Firewall rule scoped to `Program $InstallRoot\minti-cland.exe` | Only the cland binary at the canonical install path can accept :7777 inbound. Default profiles `Private,Domain`. |
| Service identity | `LocalSystem` | High-privilege, but matches Linux's root-systemd model. M6 candidate: dedicated virtual service account. |

The **state directory's DACL is load-bearing**: cland's identity.json is
written with mode `0o600` which is a no-op on NTFS. Manually creating
`%PROGRAMDATA%\MINTI\cland` (e.g. dropping a config file in there before the
installer runs) can result in the dir inheriting a permissive DACL from
`%PROGRAMDATA%`. The installer always reruns `icacls /inheritance:r` to
override; document this for any procedure that touches the dir outside the
installer.

---

## Network profile gotcha

Windows 11 categorises new / unrecognised networks as **Public** by default
("Make this network public" prompt). The install rule defaults to
`Private,Domain`, so a Public-categorised network will block inbound :7777
and cland will be invisible to peers — election will still work because
this host can reach out *to* peers, but mDNS-based discovery will fail.

**Fixes (any one):**
- Change network category: Settings > Network & Internet > Ethernet (or Wi-Fi) > Network profile → Private.
- Re-run installer with `-FirewallProfile Private,Domain,Public`.
- Manually widen the rule:
  ```powershell
  Set-NetFirewallRule -DisplayName "MINTI cland (Clan TLS)" -Profile Private,Domain,Public
  ```

---

## Defender guidance (until code-signing arrives in M6)

Unsigned LocalSystem services that bind UDP 5353 (mDNS) and TCP 7777 are
a common Defender behavioural-detection trigger — the daemon can run fine
for a few minutes and then be silently terminated + quarantined.

**Pre-emptive Folder Exclusion** (run from elevated PowerShell):

```powershell
Add-MpPreference -ExclusionPath "$env:ProgramFiles\MINTI"
Add-MpPreference -ExclusionPath "$env:ProgramData\MINTI"
```

The installer prints this guidance at the end; it does not auto-apply
because (a) the Defender PS module isn't present on every SKU and (b)
managed corporate machines may have Group Policy that overrides
user-applied exclusions. On corporate hardware, ask IT to allow-list
`minti-cland.exe` instead of adding exclusions yourself.

---

## NSSM-side tuning

The installer sets these NSSM service properties. Override post-install with
`nssm set Minti-Cland <Key> <value>`:

| Property | Value | Why |
|---|---|---|
| `Start` | `SERVICE_AUTO_START` | come up on boot, before any user logs in |
| `ObjectName` | `LocalSystem` | privileged enough for multicast + DACL'd state |
| `AppRotateFiles` | `1` | rotate log files |
| `AppRotateBytes` | `10485760` | rotate at 10 MiB |
| `AppRotateOnline` | `1` | rotate without restart |
| `AppStopMethodConsole` | `5500` ms | grace window for cland's graceful HTTP shutdown (3 s on the Go side) |
| `AppStopMethodWindow` | `5500` ms | matches Console; relevant if cland ever grows a window |

Tuned per M5 peer-review: the (Go shutdown 3 s → NSSM grace 5.5 s) stack
stays well under the Windows SCM "Not Responding" threshold (~10 s),
preventing `Restart-Service` from spuriously marking the service unhealthy.

If you want a Windows host *without* a local minti-runtime install to still
be Orchestrator-eligible (the host's reasoning_score would otherwise be
penalised by the missing runtime), set the escape-hatch env var:

```powershell
& "$env:ProgramFiles\MINTI\cland\nssm.exe" set Minti-Cland AppEnvironmentExtra "MINTI_CLAND_FORCE_HEALTHY=1"
Restart-Service Minti-Cland
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Get-Service` shows Status=Stopped after install | NSSM couldn't launch cland — usually a state-dir permission or config-parse error | `Get-Content $env:ProgramData\MINTI\cland\logs\cland.err.log -Tail 50` |
| `Test-NetConnection :7777` fails from a peer | firewall profile mismatch | see "Network profile gotcha" |
| Service mysteriously stops minutes after start | Defender behavioural block | apply Folder Exclusion (see "Defender guidance") |
| `peers` from a Linux VM doesn't show the Windows host | mDNS not reaching across the LAN segment | manual `peer-add <ip:port>` from the Linux side as fallback |
| Re-install fails copying `minti-cland.exe` | service didn't fully stop | `Stop-Service Minti-Cland; Start-Sleep 2; & install-cland.ps1` |

For Clan-side debugging (election history, who's Orchestrator, why a peer
isn't joining), use `minti-cland orchestrator` + `minti-cland peers` +
`minti-cland election-history` on any node in the Clan.
