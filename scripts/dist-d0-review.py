"""Peer-review the Distribution milestone D0 plan with local LLMs.

Mirrors memory-peer-review.py exactly in shape, narrowed to INSTALLER
SAFETY — the D milestone ships one-click installers that write to other
people's machines (Windows/Linux/macOS services) plus a public two-door
download site. Sends each model:

  - The locked distribution decisions + browser reality check (the
    contract the plan must honor).
  - The NEW D-milestone detail section being reviewed (per-OS
    install/uninstall/upgrade flows, Ollama strategy, auto-open, site IA).
  - Ground-truth behavioral summaries of the EXISTING peer-reviewed
    install surfaces the plan extends (M5-B NSSM scripts, install.sh,
    M5-C launchd) + the three daemons' real flags/ports/defaults.

Per memory/reference_local_llms.md: qwen3.6 gets think:false to avoid the
trace-overflow failure mode observed in earlier rounds.
"""
import json
import os
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    ("qwen3.6:latest",  {"think": False}),
    ("deepseek-r1:32b", {}),
    ("gemma4:31b",      {}),
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
ROADMAP_PATH = os.path.join(REPO, "docs", "plans", "distribution-roadmap.md")
OUT_DIR = os.path.join(REPO, "scripts", "m4-reviews")
os.makedirs(OUT_DIR, exist_ok=True)


def strip_thinking(s: str) -> str:
    return re.sub(r"<think>.*?</think>\s*", "", s, flags=re.DOTALL)


def read(p: str) -> str:
    with open(p, encoding="utf-8") as f:
        return f.read()


def extract_section(text: str, start_marker: str, end_marker: str) -> str:
    a = text.find(start_marker)
    b = text.find(end_marker, a + 1) if a >= 0 else -1
    if a < 0 or b < 0:
        raise SystemExit(f"could not locate section: start={start_marker!r} end={end_marker!r}")
    return text[a:b]


def build_prompt() -> str:
    roadmap = read(ROADMAP_PATH)
    # The contract: locked decisions (Calamares, firmware, A+B), the
    # browser reality check, the resequencing + phase table.
    contract = extract_section(
        roadmap, "## Decisions (user, 2026-06-07)", "## D-milestone detail"
    )
    # The NEW section under review.
    plan_detail = extract_section(
        roadmap, "## D-milestone detail", "## Kickstart prompt"
    )

    code_summary = """Ground truth — the EXISTING, already-peer-reviewed install surfaces the
plan extends (behavioral summaries, faithful to the code on disk):

cland/windows/nssm/install-cland.ps1 (M5-B, live on the author's daily
driver right now as service "Minti-Cland", pre-Clan):
  - Requires admin; REFUSES to auto-elevate (documented reason: a script
    re-launching itself via Start-Process -Verb RunAs confuses SmartScreen;
    operator elevates by hand).
  - Verifies bundled nssm.exe against a SHA-256 sidecar BEFORE use; aborts
    on mismatch ("zip may be tampered with").
  - Dirs: %PROGRAMFILES%\\MINTI\\cland (binaries), %PROGRAMDATA%\\MINTI\\cland
    (state: clan_key, identity.json, audit log, cland.yaml, logs\\).
  - icacls on the state dir: /inheritance:r, grant only SYSTEM:(OI)(CI)F +
    BUILTIN\\Administrators:(OI)(CI)F. This DACL is the load-bearing
    security step (replaces in-process ACL hardening).
  - If the service exists and is running: Stop-Service, 1 s sleep for
    handle release, THEN copy binaries over. Configs preserved if present
    (copy only when missing). NSSM config refreshed via `nssm set` (the
    install path never does `nssm remove`).
  - Service: LocalSystem, SERVICE_AUTO_START, AppParameters `--config
    <state>\\cland.yaml`, stdout/err logs with 10 MiB online rotation,
    AppStopMethodConsole/Window 5500 ms (Go srv.Shutdown is 3 s; stays
    under the SCM ~10 s "Not Responding" budget).
  - Firewall: ONE inbound rule "MINTI cland (Clan TLS)" TCP 7777, default
    profiles Private,Domain (NOT Public; warns + documents the override),
    bound to the program path. Existing rule is removed + recreated.
  - Exits non-zero with remediation text at every failure (missing bundle
    file, hash mismatch, icacls fail, nssm fail, service not Running).
  - Ends with Defender guidance: unsigned LocalSystem service doing mDNS
    can get behaviorally quarantined within minutes; until code-signing,
    operators add Folder Exclusions for install + state dirs.

cland/windows/nssm/uninstall-cland.ps1 (M5-B):
  - Stops service, `nssm remove confirm` (sc.exe delete fallback), removes
    firewall rule, removes %PROGRAMFILES%\\MINTI\\cland.
  - STATE PRESERVED BY DEFAULT with printed explanation (clan_key +
    identity = Clan membership; deleting silently would drop the host out
    of its Clan). -Purge wipes state + logs + the system rubric mirror.

install/install.sh (Linux, M0-M6, validated end-to-end in the minti-dev VM):
  - Root-only, Debian-family gate, amd64 gate, warn-only network check.
  - Installs Ollama via the OFFICIAL `curl | sh` script when missing
    (validated M0 behavior; the ISO chroot hook reuses install.sh with
    MINTI_CHROOT=1 which skips systemctl calls but still installs).
  - Creates system user `minti` (nologin, no home); /var/lib/minti owned
    minti:minti; /var/lib/minti/cland mode 0700 (clan_key inside).
  - Per-daemon blocks (runtime, cland): stage binary to /usr/local/bin,
    install hardened systemd unit, preserve existing /etc/minti/*.yaml,
    daemon-reload, then the B13 idempotency pattern: compare new binary
    hash vs sha256 of /proc/<MainPID>/exe — restart ONLY when the running
    binary is stale; else enable --now.
  - Also stages 5 MCP server binaries + mcptest + minti-pack-fetch +
    opencode (official installer as $SUDO_USER) + minti-fetch branding.
  - There is NO uninstall.sh today. install.sh does NOT deploy the
    workspace today (D2 adds both).

cland/systemd/minti-cland.service (the hardening template):
  ProtectSystem=strict, ProtectHome=true, PrivateTmp, NoNewPrivileges,
  LockPersonality, MemoryDenyWriteExecute, RestrictRealtime, User=minti,
  ReadWritePaths=/var/lib/minti/cland /var/lib/minti, Restart=on-failure,
  Environment=HOME=/var/lib/minti/cland (audit-log path redirect;
  ProtectHome would block the default). IPAddressDeny deliberately ABSENT
  on cland (it needs LAN peer traffic on :7777).

cland/darwin/* (M5-C, peer-reviewed recipes; NO live Mac validation yet):
  - install-cland.sh: root => system LaunchDaemon under dedicated _minti
    account (dscl create at lowest free UID 300-499, IsHidden +
    HiddenSystemUser, NFSHomeDirectory=/var/empty); non-root => per-user
    LaunchAgent (plist with UserName stripped via plutil -remove).
  - Binary /usr/local/bin/minti-cland; state "/Library/Application
    Support/MINTI/cland" chmod 0700 chown _minti; plist root:wheel 0644 +
    plutil -lint; `launchctl bootstrap system` with `load -w` fallback;
    xattr -d com.apple.quarantine after install.
  - com.minti.cland.plist: RunAtLoad=true, KeepAlive={SuccessfulExit:false},
    ThrottleInterval=5, EnvironmentVariables.HOME=<state_dir>,
    ProcessType=Background.
  - uninstall mirrors; --purge deletes state + the _minti account.

The three daemons (ports/flags ground truth):
  - minti-cland: Clan daemon, listens :7777 (TLS + HMAC, LAN-facing).
    State dir holds clan_key + Ed25519 identity. CLI subcommands (show,
    members, orchestrator, join, ...) talk to the LOCAL daemon over a
    loopback HMAC client that reads the state dir for the key.
  - minti-runtime: flag `--config` (compiled-in default is the Unix path
    /etc/minti/runtime.yaml; a MISSING config file is NOT an error —
    defaults apply: listen 127.0.0.1:7780, backend ollama at its default
    127.0.0.1:11434). Startup probes the backend but continues if down;
    /minti/health reports honestly.
  - minti-workspace: single static binary, embeds its SPA; flag `-listen`,
    default 127.0.0.1:8088; NO config file. Doc comment: "Default bind is
    loopback; LAN exposure waits until PIN/bearer auth lands so the
    mutating API is never opened without a gate." It NEVER speaks HMAC
    itself — it shells `minti-cland <sub> --json` (exec.LookPath +
    CommandContext) and degrades to a demo snapshot when cland is missing.

Constraints in force: P1 = no new external Go deps. P2 = lean enough for
1-2 GB resurrected boxes (and a lean site). P3 = spec/plan wins over
clever implementation ideas. Safety bar from M5-B: explicit uninstall
preserving state by default, no silent destructive steps, every bundled
third-party binary checksum-verified, services hardened like the existing
units. The author's daily driver ALREADY runs the M5-B "Minti-Cland"
service (pre-Clan) — D1's installer must upgrade it in place, never
double-install, and destructive paths are never exercised on that machine.
"""

    instructions = """You are reviewing the implementation plan for MINTI's Distribution
milestone "D" — putting two products on the internet: door B = one-click
installers that set up the MINTI stack (cland Clan daemon + runtime
adapter + workspace web UI) as services on Windows/Linux/macOS, and door
A = a lean-OS ISO (its Calamares installer phase D4 is only a pointer
here, not under review). D3 is a static two-door download site.

INSTALLERS TOUCH OTHER PEOPLE'S MACHINES. Your job is to find concrete
safety problems in the plan BEFORE any code is written: what is written,
started, opened, upgraded, and uninstalled per OS; how Ollama is handled;
failure + rollback paths; the auto-open flow; the upgrade-over-existing-
service path; and whether the download site's information architecture is
honest and earns trust. Find real problems, not style notes. Be willing
to say "looks good" rather than padding criticism. Be willing to disagree
with the plan author. Cite the plan's own headings/question numbers
(e.g. "D1 upgrade flow", "review Q3") so the author can find each item.

Priorities, in order:

1. **Windows upgrade-over-live-service (D1).** The author's own machine
   runs the M5-B "Minti-Cland" service today. The plan: stop workspace ->
   runtime -> cland, swap binaries, preserve state+configs+DACL, refresh
   NSSM config via `nssm set` only, start cland -> runtime -> workspace.
   Any way this bricks the existing service, loses clan state, leaves a
   half-upgraded box on failure midway, or races NSSM handle release?
   Is per-service nssm.exe copies (3x ~330KB) vs one shared copy the
   right call (review Q1)? Is LocalSystem for all three services
   acceptable given the workspace MUST read the strict-DACL cland state
   via its shelled CLI (review Q1) — or is there a least-privilege split
   worth doing NOW (e.g. dedicated virtual service accounts + DACL grants)?

2. **What gets written/started/opened per OS — completeness + honesty.**
   The plan's "exhaustive" write lists: anything missing that the
   scripts will inevitably touch (registry via NSSM? event log? PATH?)?
   The firewall posture (ONE inbound rule :7777 Private,Domain; nothing
   for loopback 7780/8088) — correct on all three OSes? macOS
   Application Firewall / Gatekeeper implications of unsigned binaries?

3. **Uninstall semantics.** Preserve-state default + purge opt-in per OS;
   never touches Ollama/opencode; must work on a box that only has M5-B
   cland (services absent -> skip gracefully). Anything destructive
   hiding in the default path? Anything the default SHOULD remove but
   keeps (e.g. the firewall rule? PATH edits? launchd bootout order)?

4. **Ollama strategy (review Q3).** Win/mac: detect (PATH, default
   location, port probe) -> guide (offer to open ollama.com download
   page), NEVER auto-download/run third-party installers. Linux keeps
   the validated existing behavior: auto-run the OFFICIAL ollama.com
   install script when missing + new MINTI_NO_OLLAMA=1 opt-out (changing
   it would regress the validated M0 path + the ISO chroot hook that
   reuses install.sh). Is the asymmetry defensible if stated honestly on
   the site, or a real safety/consistency problem? Is probing
   127.0.0.1:11434 and trusting whatever answers a risk (Q8)?

5. **Auto-open flow (review Q4).** Poll http://127.0.0.1:8088/ for 200
   up to 15 s, then open the default browser (Start-Process URL from the
   elevated session / xdg-open as $SUDO_USER / open as console user);
   on timeout print URL + status command + log paths and exit non-zero.
   Failure modes where the browser opens but the stack is actually
   broken, or where opening from an elevated/root context does something
   surprising? Is exit-nonzero-on-timeout right when services ARE
   running but slow?

6. **Linux/macOS service wiring (review Q7).** Workspace unit copies the
   cland hardening + IPAddressAllow=localhost + IPAddressDeny=any; the
   workspace shells the minti-cland CLI (child process of the unit)
   which makes a loopback HMAC call to :7777 and reads
   /var/lib/minti/cland (0700 minti:minti; workspace runs as the same
   `minti` user). Does the child survive those sandbox knobs
   (IPAddressDeny applies to the whole cgroup — localhost IS allowed;
   ProtectSystem/ProtectHome/MemoryDenyWriteExecute effects on the
   child exec + on Go runtime?). NoNewPrivileges + exec of
   /usr/local/bin/minti-cland — any gotcha? macOS: three KeepAlive
   daemons under one _minti account — any launchd ordering/contention
   issue (workspace up before cland -> demo data on first open)?

7. **Failure + rollback.** Each phase's failure handling is
   refuse+remediate+rerun (idempotent), uninstaller as the rollback.
   Mid-install crash windows (binaries swapped but service config not
   refreshed; cland upgraded but runtime registration fails)? Is
   re-run-the-installer genuinely a sufficient recovery story, or does
   any window need explicit transactional care?

8. **Site IA + trust (review Q9).** Two doors, B primary; per-OS "what
   this installs" disclosure (services, the one port, paths, uninstall
   one-liner); SHA-256 inline + checksums.txt; honest "live preview —
   disk installer coming" badge on door A until D4; no telemetry
   statement; artifacts on GitHub Releases, never Vercel. Does this
   earn "this installer won't hurt my machine" trust? What's missing
   (signing reality? Defender/SmartScreen/Gatekeeper warnings shown
   up-front? minimum requirements?)? Is the two-door split
   self-explanatory to someone who has never heard of MINTI?

9. **The elevation shim (review Q2).** Zip ships Install-MINTI.cmd that
   does Start-Process -Verb RunAs on the .ps1. M5-B explicitly refused
   self-elevation for SmartScreen UX reasons; the shim is a
   double-clicked different entry path but still MOTW-flagged. Ship the
   shim, or README-only ("open elevated PowerShell, run one command")?

10. **What the plan got right and should NOT change.** Say so briefly;
    padding critique with non-issues wastes the author's time.

The plan's own "Questions put to the D0 reviewers" (Q1-Q10) overlap the
above — answer them explicitly where you have a position.

End your review with one short verdict line:
  VERDICT: build D1 as planned | build after addressing items N..M | block on item K

Below are THREE documents.
"""

    parts = [
        instructions,
        "==================================================================\n"
        "DOCUMENT 1: The locked contract — decisions, browser reality\n"
        "check, resequencing + phase table (NOT under review; the plan\n"
        "must honor it)\n"
        "==================================================================\n\n",
        contract,
        "\n\n==================================================================\n"
        "DOCUMENT 2: The NEW D-milestone detail (the plan being reviewed)\n"
        "==================================================================\n\n",
        plan_detail,
        "\n\n==================================================================\n"
        "DOCUMENT 3: Ground truth — existing install surfaces + daemon\n"
        "flags the plan extends\n"
        "==================================================================\n\n",
        code_summary,
    ]
    return "".join(parts)


def chat(model: str, prompt: str, extra_body: dict) -> dict:
    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_ctx": 24576,
            "num_predict": 16000,
        },
    }
    body.update(extra_body)
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        OLLAMA, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=2400) as r:
        return json.loads(r.read().decode("utf-8"))


def main() -> int:
    prompt = build_prompt()
    print(f"input chars={len(prompt)}, approx tokens={len(prompt)//4}", flush=True)

    with open(os.path.join(OUT_DIR, "dist-d0-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"dist-d0-{safe}.md")
        print(f"\n--- {model} ---", flush=True)
        t0 = time.time()
        try:
            resp = chat(model, prompt, extra)
        except Exception as exc:
            print(f"  ERROR: {exc}", flush=True)
            summary.append((model, "ERROR", 0, str(exc)))
            continue
        dur = time.time() - t0
        raw = resp.get("message", {}).get("content", "")
        clean = strip_thinking(raw).strip()
        eval_count = resp.get("eval_count", 0)
        prompt_eval = resp.get("prompt_eval_count", 0)

        with open(out_path, "w", encoding="utf-8") as o:
            o.write(f"# {model} - Distribution D0 installer-safety review\n\n")
            o.write(f"- wall_time_s: {dur:.1f}\n- prompt_tokens: {prompt_eval}\n")
            o.write(f"- eval_tokens: {eval_count}\n- raw_chars: {len(raw)}\n")
            o.write(f"- clean_chars: {len(clean)}\n- extra_body: {extra}\n\n---\n\n")
            o.write(clean)
            o.write("\n\n---\n\n## Raw (with thinking trace if any)\n\n")
            o.write(raw)

        print(
            f"  done in {dur:.1f}s; eval_tokens={eval_count}; "
            f"clean_chars={len(clean)}; saved {out_path}",
            flush=True,
        )
        summary.append((model, "OK", dur, out_path))

    print("\n--- summary ---", flush=True)
    for m, status, dur, info in summary:
        print(f"  {m:<25} {status:<6} {dur:>5.0f}s  {info}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
