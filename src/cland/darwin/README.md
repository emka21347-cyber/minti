# minti-cland on macOS (launchd-managed daemon)

This is the macOS installer for `minti-cland` — the MINTI Clan daemon —
packaged as a `.tar.gz` distribution. Installation registers `minti-cland`
as a launchd LaunchDaemon (system) or LaunchAgent (per-user).

> **Not notarised.** The bundled binary is unsigned + un-notarised; on
> macOS 11+ Gatekeeper will refuse to run it from a user-mounted
> downloads location. M6 ships notarised `.pkg` installers; until then,
> install from a tarball extracted to `/tmp` (Gatekeeper relaxes its
> rules for code already on the local filesystem) and accept the security
> dialog the first time it's invoked. See "Gatekeeper / quarantine"
> below.

---

## Install (system, recommended)

From the unpacked tarball, run as root:

```bash
tar -xzf minti-cland-darwin-amd64-v0.2.0-M5.tar.gz -C /tmp
cd /tmp/minti-cland-darwin-amd64-v0.2.0-M5
sudo ./install-cland.sh
```

What it does:

| Step | Effect |
|---|---|
| Service account | Creates `_minti` user + group at the lowest free UID in 270–499 (idempotent; skipped if `_minti` already exists). `IsHidden=1` keeps it off the login window. |
| Binary | `install -m 0755` to `/usr/local/bin/minti-cland`. |
| State dir | `/Library/Application Support/MINTI/cland`, `chmod 0700 chown _minti:_minti`. Holds `clan.json`, `identity.json`, `audit.jsonl`. |
| Logs | `/usr/local/var/log/minti/cland.{out,err}.log`, `chmod 0750 chown _minti:_minti`. |
| Config | Default `cland.yaml` dropped at `/Library/Application Support/MINTI/cland/cland.yaml`. Preserved on re-install. |
| LaunchDaemon | `/Library/LaunchDaemons/com.minti.cland.plist`, `chmod 0644 chown root:wheel`. |
| Bootstrap | `launchctl bootstrap system <plist>` (modern API; falls back to `launchctl load -w` on the rare 10.10 release where bootstrap is buggy). |

Verify:

```bash
sudo launchctl print system/com.minti.cland | head -40       # state = running
/usr/local/bin/minti-cland show --config '/Library/Application Support/MINTI/cland/cland.yaml'
sudo tail -40 /usr/local/var/log/minti/cland.err.log         # startup trace
```

---

## Install (per-user, no sudo)

If you don't have admin on this Mac, the per-user install puts everything
under `$HOME`. Same install command, no `sudo`:

```bash
./install-cland.sh
```

| Knob | System | Per-user |
|---|---|---|
| Binary | `/usr/local/bin/minti-cland` | `$HOME/.local/bin/minti-cland` |
| State dir | `/Library/Application Support/MINTI/cland` | `~/Library/Application Support/MINTI/cland` |
| Logs | `/usr/local/var/log/minti/` | `~/Library/Logs/minti-cland/` |
| Plist | `/Library/LaunchDaemons/com.minti.cland.plist` | `~/Library/LaunchAgents/com.minti.cland.plist` |
| Bootstrap domain | `system` | `gui/<uid>` |
| Runs as | `_minti` | invoking user |

The user agent only runs when you're logged in (no automatic start at
boot the way LaunchDaemons get). Fine for a Mac you actually use; not
fine for a Mac you want to leave headless in a corner.

---

## Joining a Clan

After install, this Mac has a fresh `member_id` but no Clan affiliation:

```bash
/usr/local/bin/minti-cland join \
    --mnemonic "<12 BIP39 words>" \
    --address <orchestrator-ip>:7777 \
    --pin sha256:<hex>
```

The daemon picks up new Clan state at the next launchd restart:

```bash
sudo launchctl kickstart -k system/com.minti.cland   # SIGTERM + relaunch
```

Within ~30 s the Orchestrator's `peers` output should list this Mac.

---

## Performance expectations on 10-year-old Macs

The headline use case is "the old Mac in the closet becomes useful again"
(PRD §P2). Realistic numbers on a 2010-era Core 2 Duo:

| Metric | Range | Implication |
|---|---|---|
| CPU SHA-256 bench | 150–300 (scale 0–2000) | low end of the spectrum |
| RAM | 1–2 GB usually | Ollama unlikely to host useful models |
| VRAM | 0 GB (integrated graphics) | not Orchestrator-eligible by reasoning score |
| reasoning_score | ~5–15 (vs 50+ for a modern desktop) | almost never the Orchestrator |
| system_score | ~30–60 | useful as a worker for cheap tools (mDNS responder, log relay, watch jobs) |

This is **by design** — old Macs are most valuable as Clan workers, not
elected leaders. If `minti-cland orchestrator` keeps showing the modern
member as leader and yours as a peer, that's correct.

If you actually want the old Mac to be Orchestrator-eligible (e.g. for a
demo), set the escape hatch in the plist:

```xml
<key>EnvironmentVariables</key>
<dict>
    <key>HOME</key>
    <string>/Library/Application Support/MINTI/cland</string>
    <key>MINTI_CLAND_FORCE_HEALTHY</key>
    <string>1</string>
</dict>
```

Then `sudo launchctl kickstart -k system/com.minti.cland`.

---

## Apple Silicon vs Intel

The tarball is per-arch:

| Tarball | Run on |
|---|---|
| `minti-cland-darwin-amd64-vX.tar.gz` | Intel Macs (2006–2020) |
| `minti-cland-darwin-arm64-vX.tar.gz` | Apple Silicon (M1 onwards) |

Running the wrong arch via Rosetta 2 *works* on Apple Silicon but burns
energy unnecessarily. Stick to the native build.

---

## Upgrading

Always re-run `install-cland.sh` from a fresh tarball — it's idempotent
and handles the binary swap + plist refresh + launchd bootout/bootstrap:

```bash
sudo ./install-cland.sh   # or ./install-cland.sh for the user variant
```

If you want to force the bootout step even when nothing seems stuck:

```bash
sudo ./install-cland.sh --force-bootstrap
```

---

## Uninstall

```bash
sudo ./uninstall-cland.sh           # preserve state -- re-install will reuse member_id
sudo ./uninstall-cland.sh --purge   # also remove state + _minti user + group
```

The default (`--purge` absent) keeps `identity.json` + `clan.json` +
`audit.jsonl` so a future reinstall is invisible to peers. The `--purge`
form is destructive: this Mac will have to be re-`join`-ed from scratch
and will appear to peers as a brand-new member with a new `member_id`.

---

## Gatekeeper / quarantine

Unsigned binaries downloaded from the web get the `com.apple.quarantine`
xattr applied. Gatekeeper then refuses to run them ("cannot be opened
because the developer cannot be verified"). Workarounds in order of
preference:

1. **Extract the tarball under `/tmp`** (or any path you wrote to via
   `tar`). `tar` doesn't apply the quarantine xattr; binaries inside
   are clean.
2. **Strip the quarantine xattr after install**:
   ```bash
   sudo xattr -d com.apple.quarantine /usr/local/bin/minti-cland 2>/dev/null || true
   ```
3. **Override per-binary in System Settings > Privacy & Security**:
   on first run, macOS pops up the "cannot be opened" dialog; open
   System Settings → Privacy & Security → scroll down → "Open Anyway"
   button next to minti-cland.

M6 lands a notarised `.pkg` so steps 2 and 3 go away.

---

## Application Firewall

The macOS Application Firewall (System Settings → Network → Firewall)
prompts on the first inbound connection: "Allow incoming network
connections for `minti-cland`?". Click **Allow**. There's no CLI
pre-approval that works without a GUI dialog.

If the firewall is off (the default on most fresh installs), no prompt
appears and cland accepts inbound connections immediately.

---

## Security model

| Layer | Mechanism |
|---|---|
| Service identity | Dedicated `_minti` user, lowest free system UID, `UserShell=/usr/bin/false`, `NFSHomeDirectory=/var/empty`, `IsHidden=1`. |
| State directory access | `chmod 0700 chown _minti:_minti` on `/Library/Application Support/MINTI/cland`. `identity.json` is owner-only readable. |
| Code integrity | None at install time in v1 (unsigned). v2 lands code-signing + notarisation. |
| Inbound network | Application Firewall (manual approval on first connect). Process-bound, not port-bound. |

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `launchctl print system/com.minti.cland` shows `state = not running` | binary crashed at startup | `sudo tail -200 /usr/local/var/log/minti/cland.err.log` |
| First-run Gatekeeper dialog blocks the binary | quarantine xattr | `sudo xattr -d com.apple.quarantine /usr/local/bin/minti-cland` |
| `_minti` user creation fails on `dscl` | another `_minti` exists with the same name but different UID | `sudo dscl . -read /Users/_minti` to inspect; if hostile, `sudo dscl . -delete /Users/_minti` then re-run installer |
| `peers` from a Linux VM doesn't show the Mac | mDNS scoped to the wrong NIC | manual `minti-cland peer-add <ip:port>` from the other side |
| `bootstrap` fails with "Bootstrap failed: 5" | service already loaded from a prior run | run installer with `--force-bootstrap`, or `sudo launchctl bootout system com.minti.cland` then re-run |

For Clan-side debugging (election history, who's Orchestrator),
`minti-cland orchestrator` + `minti-cland peers` + `minti-cland
election-history` work the same on macOS as everywhere else.
