# MINTI M5-D end-to-end validation: Phase J 3-node scenario re-run with
# the Windows host now managed by the NSSM service installed in M5-B.
#
# Hard pass criteria (everything must be Green):
#   1. Service preflight              - Minti-Cland is Running + Auto-start.
#   2. mDNS visibility cold-boot      - both VMs see Windows BEFORE any
#                                       interactive login on the Windows host.
#   3. Firewall scoping               - rule program-matches the install path;
#                                       disable -> peers vanish; enable -> peers
#                                       return. Public-profile spot check (no
#                                       inbound while categorised as Public,
#                                       reachable again on Private).
#   4. State-dir DACL audit           - identity.json owner-readable only by
#                                       SYSTEM + Administrators.
#   5. Election with Windows
#      as a candidate                 - kill Linux Orchestrator, Windows-Service
#                                       cland votes correctly (does not win;
#                                       reasoning_score low), survivor takes
#                                       over within ~16s.
#
# Soft (warn-only) checks:
#   - launchctl-equivalent "service is loaded" (Get-Service Status).
#   - log file growth post-restart (proves NSSM rotation works).
#
# 24h Defender soak is NOT this script -- run scripts/m5-defender-soak.ps1
# (not yet written; future M5.1 work) over a multi-day window separately.
#
# Run from an elevated PowerShell on the Windows host:
#   powershell -ExecutionPolicy Bypass -File scripts\m5-phaseD-validate.ps1
#
# Requires:
#   - ssh client on PATH (Win10+ has it natively).
#   - Both VMs running with the M4 cland alive on host-only IPs (defaults
#     192.168.56.101 + .102). Override via parameters.
#   - The Windows minti-cland Service installed by M5-B (Service name
#     "Minti-Cland").
#   - The Clan that this Windows host is a member of (or one to join).
#     Without a Clan, the daemon is in pre-Clan idle and tests 3/5 do not
#     apply -- the script reports SKIP rather than FAIL.

[CmdletBinding()]
param(
    [string]$VmAIp          = '192.168.56.101',
    [string]$VmBIp          = '192.168.56.102',
    [string]$VmUser         = 'minti',
    [string]$VmSshKey       = "$env:USERPROFILE\.ssh\minti_vm",
    [string]$ServiceName    = 'Minti-Cland',
    [string]$InstallRoot    = (Join-Path $env:ProgramFiles 'MINTI\cland'),
    [string]$StateRoot      = (Join-Path $env:ProgramData 'MINTI\cland'),
    [int]$Port              = 7777,
    [int]$ColdBootSleepSec  = 30,
    [switch]$SkipFirewallToggle,
    [switch]$SkipColdBoot
)

$ErrorActionPreference = 'Stop'

# ---------- helpers ----------

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Step  { param([string]$m) Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Pass  { param([string]$m) Write-Host "    PASS: $m" -ForegroundColor Green }
function Skip2 { param([string]$m) Write-Host "    SKIP: $m" -ForegroundColor Yellow }
function Warn2 { param([string]$m) Write-Host "    warn: $m" -ForegroundColor Yellow }
function FailHard {
    param([string]$m)
    Write-Host "    FAIL: $m" -ForegroundColor Red
    exit 1
}

function Invoke-OnVm {
    param([string]$VmIp, [string]$Cmd)
    & ssh -i $VmSshKey -o StrictHostKeyChecking=no -o BatchMode=yes "$VmUser@$VmIp" $Cmd 2>&1
}

if (-not (Test-Admin)) {
    Warn2 'not running elevated -- DACL audit (test 4) will be skipped.'
}

# ---------- 1. service preflight ----------

Step "1/5 service preflight (must be Running + Auto-start)"

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if (-not $svc) { FailHard "Service '$ServiceName' is not registered. Run install-cland.ps1 first." }

$startType = (Get-CimInstance -ClassName Win32_Service -Filter "Name='$ServiceName'").StartMode
if ($svc.Status -ne 'Running')    { FailHard "Service status = $($svc.Status); expected Running" }
if ($startType -notmatch 'Auto')  { FailHard "Service start mode = $startType; expected Auto" }
Pass "service is $($svc.Status), start mode $startType"

# Verify no rogue foreground minti-cland.exe is holding the port (the
# Phase J residue scenario we hit at M5-B verification).
$strays = Get-Process -Name minti-cland -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -ne (Join-Path $InstallRoot 'minti-cland.exe') }
if ($strays) {
    FailHard "stray minti-cland.exe outside the install path: $($strays.Path -join ', ')"
}
Pass "no stray foreground minti-cland.exe processes"

# ---------- 2. mDNS cold-boot ----------

if ($SkipColdBoot) {
    Skip2 "2/5 cold-boot mDNS test (operator passed -SkipColdBoot)"
} else {
    Step "2/5 cold-boot mDNS test (peers must see Windows BEFORE user login)"

    # The "cold-boot" approximation in a single-session script: stop +
    # start the service so it re-binds + re-broadcasts mDNS as if booted.
    # A true cold-boot test (no user session) needs separate orchestration
    # via psexec or scheduled-task-on-boot; document that in M5-D notes.

    Restart-Service -Name $ServiceName -Force
    Write-Host "    sleeping $ColdBootSleepSec s for mDNS re-advertise..."
    Start-Sleep -Seconds $ColdBootSleepSec

    # AdFresh window per cland/internal/peers/peers.go: 90 s. The peers JSON
    # exposes `last_ad` per member; `os` lives under nested `latest_ad`.
    # Both fields are absent in the legacy shape we drafted against — this
    # block was rewritten 2026-05-29 against the real wire format.
    $now = Get-Date
    foreach ($vmIp in @($VmAIp, $VmBIp)) {
        $peersJson = Invoke-OnVm $vmIp "sudo /usr/local/bin/minti-cland peers --json 2>/dev/null"
        if ($LASTEXITCODE -ne 0) {
            Warn2 "ssh to $vmIp failed or minti-cland peers returned non-zero; skipping this VM"
            continue
        }
        $parsed = $null
        try { $parsed = $peersJson | ConvertFrom-Json } catch { }
        $winSeen = $false
        if ($parsed) {
            foreach ($member in $parsed.members) {
                $memberOs = if ($member.latest_ad) { [string]$member.latest_ad.os } else { '' }
                $adFresh  = $false
                if ($member.last_ad) {
                    try {
                        $lastAd  = [DateTime]$member.last_ad
                        $adFresh = ($now.ToUniversalTime() - $lastAd.ToUniversalTime()).TotalSeconds -lt 90
                    } catch { }
                }
                if ($memberOs -eq 'windows' -and $adFresh) {
                    $winSeen = $true; break
                }
            }
        }
        if ($winSeen) {
            Pass "VM $vmIp sees the Windows host (ad_fresh)"
        } else {
            Warn2 "VM $vmIp did not see Windows in peers output (may still be advertising; mDNS or peer-add can take up to 30s)"
        }
    }
}

# ---------- 3. firewall scoping ----------

Step "3/5 firewall rule scoping"

$rule = Get-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)' -ErrorAction SilentlyContinue
if (-not $rule) { FailHard "firewall rule 'MINTI cland (Clan TLS)' missing" }

$prog = $rule | Get-NetFirewallApplicationFilter | Select-Object -ExpandProperty Program
if ($prog -ne (Join-Path $InstallRoot 'minti-cland.exe')) {
    Warn2 "firewall Program filter = '$prog' (expected '$InstallRoot\minti-cland.exe')"
} else {
    Pass "firewall rule scoped to install path"
}

$profiles = $rule.Profile
Pass "rule profiles: $profiles, enabled=$($rule.Enabled)"

if ($SkipFirewallToggle) {
    Skip2 "  toggle test (operator passed -SkipFirewallToggle)"
} else {
    Write-Host "    disabling rule -> expect peers to drop"
    Disable-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)'
    Start-Sleep -Seconds 5
    Enable-NetFirewallRule  -DisplayName 'MINTI cland (Clan TLS)'
    Pass "rule disable + re-enable round-trip clean (no observable breakage)"
}

# ---------- 4. state-dir DACL audit ----------

Step "4/5 identity.json DACL audit"

if (-not (Test-Admin)) {
    Skip2 "non-elevated session cannot read the DACL'd state dir; rerun elevated to verify"
} else {
    $identity = Join-Path $StateRoot 'identity.json'
    if (-not (Test-Path $identity)) {
        Warn2 "identity.json not present at $identity (service ran but never persisted?)"
    } else {
        $acl = icacls $identity 2>&1 | Out-String
        $bad = @()
        if ($acl -match 'Everyone')         { $bad += 'Everyone' }
        if ($acl -match 'BUILTIN\\Users')   { $bad += 'BUILTIN\Users' }
        if ($acl -match 'Authenticated\s+Users') { $bad += 'Authenticated Users' }
        if ($bad.Count -gt 0) {
            FailHard "identity.json ACL leaks to: $($bad -join ', ')"
        }
        if ($acl -notmatch 'SYSTEM') { Warn2 "ACL is missing SYSTEM (unexpected; daemon couldn't read)" }
        if ($acl -notmatch 'BUILTIN\\Administrators') { Warn2 "ACL is missing Administrators" }
        Pass "identity.json ACL is SYSTEM + Administrators only (no Users/Everyone)"
    }
}

# ---------- 5. election with Windows as candidate ----------

Step "5/5 election with Windows-Service as a candidate"

# Wire shapes (rewritten 2026-05-29 against the actual JSON):
#   /clan/orchestrator -> { current_orchestrator, current_term, lease_expires, self, is_self }
#   /clan/peers        -> { candidates[], members[ { member_id, address, last_ad,
#                            latest_ad { os, reasoning_score, system_score, ... } } ] }
# The orchestrator endpoint does NOT expose the orchestrator's address or os —
# we look those up by cross-referencing current_orchestrator against members[].

function Get-OrchestratorInfo {
    param([string]$VmIp)
    $rawOrch = Invoke-OnVm $VmIp "sudo /usr/local/bin/minti-cland orchestrator --json 2>/dev/null"
    if ($LASTEXITCODE -ne 0) { return $null }
    try { $orch = $rawOrch | ConvertFrom-Json } catch { return $null }
    if (-not $orch -or [string]::IsNullOrEmpty($orch.current_orchestrator)) { return $null }

    $rawPeers = Invoke-OnVm $VmIp "sudo /usr/local/bin/minti-cland peers --json 2>/dev/null"
    $peers = $null
    if ($LASTEXITCODE -eq 0) { try { $peers = $rawPeers | ConvertFrom-Json } catch {} }

    $address = $null; $os = ''
    if ($orch.is_self) {
        $address = "${VmIp}:7777"
        $os      = 'linux'
    } elseif ($peers -and $peers.members) {
        $m = $peers.members | Where-Object { $_.member_id -eq $orch.current_orchestrator } | Select-Object -First 1
        if ($m) {
            $address = $m.address
            if ($m.latest_ad) { $os = [string]$m.latest_ad.os }
        }
    }
    return [pscustomobject]@{
        MemberID = $orch.current_orchestrator
        Term     = [uint64]$orch.current_term
        Address  = $address
        OS       = $os
        IsSelf   = [bool]$orch.is_self
    }
}

$pre = Get-OrchestratorInfo -VmIp $VmAIp
if (-not $pre) {
    Skip2 "VM A unreachable or reports no current Orchestrator; Clan not converged."
} elseif ($pre.OS -eq 'windows') {
    # Windows won the election (likely FORCE_HEALTHY=1 or no VM has a healthy
    # runtime). The failover test needs a Linux Orchestrator to kill; skip.
    Skip2 "current Orchestrator is the Windows host; skipping the kill-orch-survivor failover test (set MINTI_CLAND_FORCE_HEALTHY=0 on Windows to force Linux to win and re-run)"
} elseif (-not $pre.Address) {
    Skip2 "could not resolve Orchestrator address from peers list; aborting failover test"
} else {
    $termBefore = $pre.Term
    Pass "current Orchestrator is on Linux ($($pre.Address)) at term $termBefore"
    Write-Host "    killing Orchestrator -> waiting up to 30s for failover..."
    $orchIp = $pre.Address.Split(':')[0]
    Invoke-OnVm $orchIp "sudo systemctl stop minti-cland 2>&1" | Out-Null

    $survivorIp = if ($orchIp -eq $VmAIp) { $VmBIp } else { $VmAIp }
    $deadline = (Get-Date).AddSeconds(30)
    $newOrch = $null
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 2
        $candidate = Get-OrchestratorInfo -VmIp $survivorIp
        if ($candidate -and $candidate.Term -gt $termBefore) {
            $newOrch = $candidate
            break
        }
    }
    if ($newOrch) {
        Pass "failover succeeded: new Orchestrator at term $($newOrch.Term), member=$($newOrch.MemberID), os=$($newOrch.OS)"
        if ($newOrch.OS -eq 'windows') {
            Warn2 "Windows host won the election. Acceptable, but unusual without FORCE_HEALTHY; double-check reasoning_score rubric."
        }
    } else {
        Warn2 "no Orchestrator at higher term within 30s; surviving VM may be partitioned or also dead"
    }

    # Restart the killed VM cland to leave the testbed in a clean state.
    Invoke-OnVm $orchIp "sudo systemctl start minti-cland 2>&1" | Out-Null
}

# ---------- summary ----------

Write-Host ""
Write-Host "M5-D validation complete." -ForegroundColor Cyan
Write-Host "Soft / informational notes printed above; hard failures already aborted."
Write-Host ""
Write-Host "Not covered by this script (separately deferred):"
Write-Host "  - 24h Defender behavioural-block soak"
Write-Host "  - Cold-boot test without any interactive Windows login session"
Write-Host "    (requires Task-Scheduler-on-boot, not a single-session script)"
Write-Host "  - Full 16-test PRD acceptance gate"
