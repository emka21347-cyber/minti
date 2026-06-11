# MINTI on macOS — the full stack ("Add MINTI to this machine")

macOS 10.13+. Pick the tarball for your chip: **arm64** (Apple Silicon,
M1 and newer) or **amd64** (Intel).

| Service | What it is | Listens | Account (system mode) |
|---|---|---|---|
| `com.minti.cland` | Clan daemon | `:7777` TLS+HMAC — **the only LAN port** | `_minti` |
| `com.minti.runtime` | runtime adapter (local-model API) | `127.0.0.1:7780` | `_minti` |
| `com.minti.workspace` | the web UI | `127.0.0.1:8088` | `_minti` |

> **You will see security warnings — expected, not a malfunction.** The
> binaries are not yet code-signed or notarized. The installer strips the
> Gatekeeper quarantine attribute from the binaries it installs; macOS
> may still prompt on first inbound connection for `minti-cland`
> (Application Firewall — click Allow). Verify the tarball's SHA-256
> against the download page *before* installing.

> **Honesty:** these recipes are peer-reviewed and lint-verified but have
> **not yet run on physical Apple hardware** (same posture as the M5-C
> cland-only build they extend). If something misbehaves, the uninstaller
> removes everything cleanly — and a bug report is gold.

## Install

```bash
tar xzf minti-macos-<arch>-v<version>.tar.gz
cd minti-macos-<arch>-v<version>
sudo bash install-minti.sh        # system install (recommended)
# or, without sudo — per-user LaunchAgents under your account:
bash install-minti.sh
```

When it finishes it opens `http://127.0.0.1:8088` (the workspace) as the
console user. Flags: `--unattended` (skip prompts), `--no-auto-open`.

**System mode writes:** binaries → `/usr/local/bin/minti-{cland,runtime,workspace}`;
state → `/Library/Application Support/MINTI/{cland,runtime,workspace}`
(mode 0700, owner `_minti` — a hidden service account the installer
creates at the first free UID in 300–499); logs →
`/usr/local/var/log/minti/`; plists → `/Library/LaunchDaemons/com.minti.*`.
Nothing else — no kexts, no profiles, no telemetry.

## Ollama (the models)

The installer **detects** Ollama (PATH, `/Applications/Ollama.app`, a
`127.0.0.1:11434/api/version` probe) and **guides** you to
<https://ollama.com/download/mac> when missing — it never downloads or
runs third-party installers. Ollama.app is a per-user login item; that's
fine — the runtime talks to `127.0.0.1:11434` regardless of who started
it and picks it up the moment it appears.

## Upgrade

Re-run `install-minti.sh` from a newer tarball — idempotent: binaries
swapped, configs + state + Clan identity preserved, services re-bootstrapped.

## Uninstall

```bash
sudo bash uninstall-minti.sh           # state preserved (re-install reuses identity)
sudo bash uninstall-minti.sh --purge   # full wipe: identity + clan_key + _minti account
```

All three plists are booted out **and deleted** (an orphan plist would
respawn-fail at every boot). Ollama is never touched.

## Troubleshooting

| Symptom | Fix |
|---|---|
| a service won't stay loaded | `cat /usr/local/var/log/minti/<name>.err.log` |
| workspace shows demo data | is cland configured? `minti-cland show` (CLI defaults resolve via `/Library/Application Support/MINTI/cland.yaml`) |
| peers can't reach this Mac | allow `minti-cland` in System Settings → Network → Firewall |
| "cannot be opened" dialogs | quarantine xattr on a manually-copied binary: `xattr -d com.apple.quarantine <path>` |
