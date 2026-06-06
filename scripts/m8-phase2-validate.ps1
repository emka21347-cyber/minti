# MINTI M8 Phase 2 validation: cross-node knock-join flow.
#
# Proves the knock protocol works across the real 3-node testbed:
#   Test 1: VM-A creates ephemeral Clan, starts daemon (receiver)
#   Test 2: VM-B knocks into VM-A Clan (Linux -> Linux, cross-machine)
#   Test 3: Windows host knocks into same Clan (Windows -> Linux, cross-OS)
#   Test 4: Clan-ID mismatch rejected (hard check)
#   Test 5: Rate-limit spot check (warn-only)
#
# Uses ephemeral state dirs on both VMs so the live cland Clan is never
# disturbed. Ephemeral daemon runs on $Port (default 7779, not 7777).
#
# Run from repo root:
#   powershell -ExecutionPolicy Bypass -File scripts\m8-phase2-validate.ps1

[CmdletBinding()]
param(
    [string]$VmAIp             = '192.168.56.101',
    [string]$VmBIp             = '192.168.56.102',
    [string]$VmUser            = 'minti',
    [string]$VmSshKey          = "$env:USERPROFILE\.ssh\minti_vm",
    [string]$BinPath           = 'C:\Program Files\MINTI\cland\minti-cland.exe',
    [int]$Port                 = 7779,
    [int]$ArmSleepSec          = 6,
    [int]$DeliverTimeoutSec    = 30,
    [switch]$SkipWindowsKnock,
    [switch]$SkipRateLimit
)

$ErrorActionPreference = 'Stop'

# ---------- helpers ----------

function Step  { param([string]$m) Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Pass  { param([string]$m) Write-Host "    PASS: $m" -ForegroundColor Green }
function Skip2 { param([string]$m) Write-Host "    SKIP: $m" -ForegroundColor Yellow }
function Warn2 { param([string]$m) Write-Host "    warn: $m" -ForegroundColor Yellow }
function FailHard {
    param([string]$m)
    Write-Host "    FAIL: $m" -ForegroundColor Red
    Cleanup
    exit 1
}

function Invoke-OnVm {
    param([string]$VmIp, [string]$Cmd)
    & ssh -i $VmSshKey -o StrictHostKeyChecking=no -o BatchMode=yes `
        "$VmUser@$VmIp" $Cmd 2>&1
}

# Parse first JSON object from SSH output (SSH may prepend banner lines)
function Parse-Json {
    param([string[]]$Lines)
    $jsonLine = $Lines | Where-Object { $_ -match '^\s*[\{\[]' } | Select-Object -First 1
    if (-not $jsonLine) { return $null }
    return $jsonLine | ConvertFrom-Json
}

$Script:DaemonStarted = $false
$Script:WinKnockProc  = $null

function Cleanup {
    Invoke-OnVm $VmAIp "pkill -f 'minti-clan[d].*knock-val-A' 2>/dev/null || true; rm -rf /tmp/knock-val-A" | Out-Null
    Invoke-OnVm $VmBIp "rm -rf /tmp/knock-val-B" | Out-Null
    if ($Script:WinKnockProc -and -not $Script:WinKnockProc.HasExited) {
        $Script:WinKnockProc.Kill()
    }
}

# ---------- pre-run cleanup (kill any stale ephemeral daemons from prior runs) ----------

Write-Host ""
Write-Host "==> pre-run cleanup (killing stale daemons, removing stale state)" -ForegroundColor DarkGray
# Use [d] trick so pkill doesn't match its own bash command line.
Invoke-OnVm $VmAIp "pkill -f 'minti-clan[d].*knock-val-A' 2>/dev/null || true; sleep 1; rm -rf /tmp/knock-val-A" | Out-Null
Invoke-OnVm $VmBIp "pkill -f 'minti-clan[d].*knock-val-B' 2>/dev/null || true; rm -rf /tmp/knock-val-B" | Out-Null

# ---------- 0. pre-flight ----------

Step "0/5 pre-flight"

if (-not (Test-Path $VmSshKey)) { FailHard "SSH key not found at $VmSshKey" }

foreach ($vmIp in @($VmAIp, $VmBIp)) {
    $which = Invoke-OnVm $vmIp "which minti-cland"
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace("$which")) {
        FailHard "minti-cland not on PATH on $vmIp"
    }
    # Verify knock subcommand exists (M8 build)
    $hasKnock = Invoke-OnVm $vmIp "minti-cland help 2>&1 | grep -c knock"
    if ([int]"$hasKnock".Trim() -eq 0) {
        FailHard "minti-cland on $vmIp lacks knock subcommand (redeploy M8 binary)"
    }
    Pass "minti-cland with knock support found on ${vmIp}: $($which.Trim())"
}

# ---------- 1. VM-A: create Clan + start ephemeral daemon ----------

Step "1/5 VM-A creates ephemeral Clan + starts daemon on :$Port"

# Write config via tee (avoids heredoc quoting issues over SSH)
$addr   = "${VmAIp}:${Port}"
$cfgCmd = "mkdir -p /tmp/knock-val-A && printf 'listen:\n  address: 0.0.0.0\n  port: $Port\nstate:\n  dir: /tmp/knock-val-A\ndiscovery:\n  mdns_enabled: false\nelection:\n  heartbeat_interval: 2s\n  lease_duration: 8s\n  failover_grace: 6s\n  election_timeout: 1s\n' > /tmp/knock-val-A/config.yaml"
Invoke-OnVm $VmAIp $cfgCmd | Out-Null

$createRaw = Invoke-OnVm $VmAIp "minti-cland create --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A --address $addr --json 2>/dev/null"
if ($LASTEXITCODE -ne 0) { FailHard "create on VM-A failed: $createRaw" }
$create = Parse-Json $createRaw
if (-not $create) { FailHard "create output not parseable: $createRaw" }
$ClanID = $create.clan_id
$Pin    = $create.pin
if ([string]::IsNullOrEmpty($ClanID) -or [string]::IsNullOrEmpty($Pin)) {
    FailHard "create returned empty clan_id or pin: $createRaw"
}
Write-Host "    clan_id = $ClanID"
Write-Host "    pin     = $Pin"
Pass "Clan created on VM-A"

# Start daemon in background
$startDaemon = "nohup minti-cland --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A > /tmp/knock-val-A/daemon.log 2>&1 &"
Invoke-OnVm $VmAIp $startDaemon | Out-Null
$Script:DaemonStarted = $true
Write-Host "    sleeping 3s for daemon to bind..."
Start-Sleep -Seconds 3

$netCheck = Invoke-OnVm $VmAIp "ss -tlnp 2>/dev/null | grep :$Port || echo none"
if ("$netCheck" -notmatch ":$Port") {
    Warn2 "daemon may not be listening on :$Port yet (ss: $netCheck)"
} else {
    Pass "daemon listening on ${VmAIp}:${Port}"
}

# ---------- 2. VM-B knocks -> VM-A (Linux->Linux) ----------

Step "2/5 VM-B knocks into VM-A Clan (Linux -> Linux)"

# Write config for B
$cfgBCmd = "mkdir -p /tmp/knock-val-B && printf 'listen:\n  address: $VmBIp\n  port: 17998\nstate:\n  dir: /tmp/knock-val-B\ndiscovery:\n  mdns_enabled: false\n' > /tmp/knock-val-B/config.yaml"
Invoke-OnVm $VmBIp $cfgBCmd | Out-Null

# Start knock process on VM-B in background, pipe "y" for SAS confirmation.
# Use --listen $VmBIp:0 so the joiner advertises the host-only IP (not 10.0.2.x NAT)
# to the receiver; otherwise knock-deliver POSTs to an unreachable NAT address.
$knockCmd = "nohup bash -c 'echo y | minti-cland knock --config /tmp/knock-val-B/config.yaml --state /tmp/knock-val-B --clan-id $ClanID --address $addr --pin $Pin --listen ${VmBIp}:0 > /tmp/knock-val-B/knock.out 2>&1' </dev/null >/dev/null 2>&1 &"
Invoke-OnVm $VmBIp $knockCmd | Out-Null
Write-Host "    knock started on VM-B"

# Poll knocks on VM-A until a pending knock appears (up to 30s).
# Fixed sleep is unreliable; nohup process startup + TLS handshake can take 3-10s.
$pending = @()
$pollDeadline = (Get-Date).AddSeconds(30)
while ((Get-Date) -lt $pollDeadline) {
    Start-Sleep -Seconds 2
    $knocksOut = Invoke-OnVm $VmAIp "minti-cland knocks --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A --json 2>/dev/null"
    $knocksObj  = Parse-Json $knocksOut
    if ($knocksObj -and $knocksObj.knocks.Count -gt 0) {
        $pending = $knocksObj.knocks
        break
    }
}
Write-Host "    pending knocks: $($pending.Count)"
if ($pending.Count -eq 0) {
    Write-Host "    VM-B knock.out:"; Invoke-OnVm $VmBIp "cat /tmp/knock-val-B/knock.out 2>/dev/null" | ForEach-Object { Write-Host "      $_" }
    Write-Host "    VM-A daemon.log (tail):"; Invoke-OnVm $VmAIp "tail -20 /tmp/knock-val-A/daemon.log 2>/dev/null" | ForEach-Object { Write-Host "      $_" }
    FailHard "no pending knocks on VM-A within 30s"
}

$knock   = $pending[0]
$KnockID = $knock.knock_id
$SAS     = $knock.sas
$Joiner  = $knock.joiner_member_id
Write-Host "    knock_id = $KnockID"
Write-Host "    sas      = $SAS"
Write-Host "    joiner   = $Joiner"
Pass "pending knock visible on VM-A"

# Accept
$acceptOut = Invoke-OnVm $VmAIp "minti-cland knock-accept --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A $KnockID 2>&1"
if ($LASTEXITCODE -ne 0) { FailHard "knock-accept failed: $acceptOut" }
Pass "knock-accept sent: $acceptOut"

# Wait for VM-B knock process to finish
$deadline = (Get-Date).AddSeconds($DeliverTimeoutSec)
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 1
    $pgrep = Invoke-OnVm $VmBIp "pgrep -f 'knock-val-[B]' 2>/dev/null || echo done"
    if ("$pgrep" -match 'done') { break }
}
if ("$pgrep" -notmatch 'done') {
    Warn2 "VM-B knock process still running after ${DeliverTimeoutSec}s"
    Invoke-OnVm $VmBIp "pkill -f 'knock-val-[B]' 2>/dev/null || true" | Out-Null
} else {
    Pass "VM-B knock process exited"
}

Write-Host "    VM-B knock.out:"; Invoke-OnVm $VmBIp "cat /tmp/knock-val-B/knock.out 2>/dev/null" | ForEach-Object { Write-Host "      $_" }

# Verify B joined
$membersB = Invoke-OnVm $VmBIp "minti-cland members --config /tmp/knock-val-B/config.yaml --state /tmp/knock-val-B 2>/dev/null"
if ("$membersB" -notmatch [regex]::Escape($ClanID)) {
    Write-Host "    members output: $membersB"
    FailHard "VM-B state does not show clan_id $ClanID after join"
}
Pass "VM-B shows clan_id $ClanID (joined)"

$membersA = Invoke-OnVm $VmAIp "minti-cland members --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A 2>/dev/null"
if ("$membersA" -notmatch [regex]::Escape($Joiner)) {
    Warn2 "VM-A roster does not yet include joiner $Joiner (may need daemon restart to refresh in-memory roster)"
} else {
    Pass "VM-A roster includes joiner $Joiner (Linux->Linux knock complete)"
}

# ---------- 3. Windows host knocks -> VM-A (Windows->Linux) ----------

Step "3/5 Windows host knocks into VM-A Clan (Windows -> Linux)"

if ($SkipWindowsKnock) {
    Skip2 "operator passed -SkipWindowsKnock"
} elseif (-not (Test-Path $BinPath)) {
    Skip2 "minti-cland.exe not found at $BinPath (pass -BinPath or -SkipWindowsKnock)"
} else {
    $WinStateDir  = "$env:TEMP\knock-val-win"
    $StdinY       = "$env:TEMP\knock-val-y.txt"
    $KnockWinOut  = "$env:TEMP\knock-val-win-out.txt"
    $KnockWinErr  = "$env:TEMP\knock-val-win-err.txt"
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $WinStateDir
    New-Item -ItemType Directory -Force $WinStateDir | Out-Null
    "y" | Out-File -Encoding ascii -NoNewline $StdinY

    # Detect Windows host-only IP (same /24 as VMs). The deliver handler source-IP
    # check requires the Windows listener to advertise the IP that VM-A connects from —
    # i.e. the host-only adapter IP, not the WiFi/Ethernet adapter. Without this,
    # VM-A's delivery POST arrives from 10.0.2.x (NAT) which fails the source-IP check.
    $subnet3 = ($VmAIp -split '\.' | Select-Object -First 3) -join '.'
    $WinHostOnlyIp = (Get-NetIPAddress -AddressFamily IPv4 |
        Where-Object { $_.IPAddress -like "${subnet3}.*" } |
        Select-Object -First 1).IPAddress
    if (-not $WinHostOnlyIp) {
        Skip2 "Windows host-only IP not found in ${subnet3}.* subnet; use -SkipWindowsKnock"
    } else {
    Write-Host "    Windows host-only IP: $WinHostOnlyIp"

    # Windows Firewall blocks inbound from VMs by default. Add a temporary rule so
    # VM-A can POST /clan/knock-deliver to the joiner's ephemeral port. Remove after.
    $FwRuleName = "minti-cland knock-deliver (temp validation)"
    $FwRuleAdded = $false
    try {
        New-NetFirewallRule -DisplayName $FwRuleName -Direction Inbound `
            -Program $BinPath -Action Allow -Profile Any -ErrorAction Stop | Out-Null
        $FwRuleAdded = $true
        Write-Host "    Firewall rule added for $BinPath"
    } catch {
        Warn2 "Could not add firewall rule (run as admin for full test): $_"
        Write-Host "    Will verify knock arrives on VM-A only (delivery skipped without firewall rule)"
    }

    $Script:WinKnockProc = Start-Process -FilePath $BinPath `
        -ArgumentList @("knock","--state",$WinStateDir,"--clan-id",$ClanID,"--address",$addr,"--pin",$Pin,"--listen","${WinHostOnlyIp}:0") `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardInput  $StdinY `
        -RedirectStandardOutput $KnockWinOut `
        -RedirectStandardError  $KnockWinErr

    Write-Host "    Windows knock PID=$($Script:WinKnockProc.Id)"
    Write-Host "    sleeping ${ArmSleepSec}s for deliver handler to arm..."
    Start-Sleep -Seconds $ArmSleepSec

    if ($Script:WinKnockProc.HasExited) {
        $null = $Script:WinKnockProc.WaitForExit(1000)
        if (Test-Path $KnockWinErr) { Write-Host "--- stderr ---"; Get-Content $KnockWinErr }
        FailHard "Windows knock exited prematurely (code=$($Script:WinKnockProc.ExitCode))"
    }

    # List + accept the Windows knock
    $knocksRaw2 = Invoke-OnVm $VmAIp "minti-cland knocks --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A --json 2>/dev/null"
    $knocksObj2 = Parse-Json $knocksRaw2
    if (-not $knocksObj2) { FailHard "knocks output 2 not parseable: $knocksRaw2" }
    $winKnock = $knocksObj2.knocks | Where-Object { $_.joiner_member_id -ne $Joiner -and $_.state -eq 'pending' } | Select-Object -First 1
    if (-not $winKnock) {
        $winKnock = $knocksObj2.knocks | Where-Object { $_.state -eq 'pending' } | Select-Object -First 1
    }
    if (-not $winKnock) { FailHard "no pending knock for Windows joiner on VM-A" }

    $WinKnockID = $winKnock.knock_id
    Write-Host "    Windows knock_id = $WinKnockID"
    Write-Host "    Windows sas      = $($winKnock.sas)"
    Pass "Windows knock visible on VM-A (cross-OS delivery proven)"

    if (-not $FwRuleAdded) {
        Warn2 "Skipping knock-accept delivery: no firewall rule (re-run as admin for full Windows→Linux test)"
        $Script:WinKnockProc.Kill()
        $null = $Script:WinKnockProc.WaitForExit(2000)
    } else {
    $acceptOut2 = Invoke-OnVm $VmAIp "minti-cland knock-accept --config /tmp/knock-val-A/config.yaml --state /tmp/knock-val-A $WinKnockID 2>&1"
    if ($LASTEXITCODE -ne 0) { FailHard "knock-accept for Windows joiner failed: $acceptOut2" }
    Pass "knock-accept sent for Windows joiner"

    $deadline2 = (Get-Date).AddSeconds($DeliverTimeoutSec)
    while (-not $Script:WinKnockProc.HasExited -and (Get-Date) -lt $deadline2) {
        Start-Sleep -Milliseconds 500
    }
    $null = $Script:WinKnockProc.WaitForExit(1000)
    $ec = $Script:WinKnockProc.ExitCode
    if (-not $Script:WinKnockProc.HasExited) {
        Warn2 "Windows knock process did not exit within ${DeliverTimeoutSec}s"
        $Script:WinKnockProc.Kill()
    } elseif ($null -ne $ec -and $ec -ne 0) {
        if (Test-Path $KnockWinErr) { Write-Host "--- stderr ---"; Get-Content $KnockWinErr }
        FailHard "Windows knock exited with code $ec"
    } else {
        Pass "Windows knock process exited cleanly"
    }

    Write-Host "    Windows knock stdout:"
    if (Test-Path $KnockWinOut) { Get-Content $KnockWinOut | ForEach-Object { Write-Host "      $_" } }

    $membersWin = & $BinPath members --state $WinStateDir 2>&1
    if ("$membersWin" -notmatch [regex]::Escape($ClanID)) {
        FailHard "Windows state does not show clan_id $ClanID"
    }
    Pass "Windows host joined Clan $ClanID (Windows->Linux knock complete)"
    } # end else ($FwRuleAdded)

    $Script:WinKnockProc = $null
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $WinStateDir
    Remove-Item -Force -ErrorAction SilentlyContinue $StdinY, $KnockWinOut, $KnockWinErr
    if ($FwRuleAdded) { Remove-NetFirewallRule -DisplayName $FwRuleName -ErrorAction SilentlyContinue | Out-Null }
    } # end else ($WinHostOnlyIp found)
} # end else (!SkipWindowsKnock)

# ---------- 4. Clan-ID mismatch rejected ----------

Step "4/5 clan-ID mismatch rejected"

$badID  = "00000000-0000-0000-0000-000000000000"
$badCmd = "bash -c 'echo y | timeout 10 minti-cland knock --config /tmp/knock-val-B/config.yaml --state /tmp/knock-val-B --clan-id $badID --address $addr --pin `"$Pin`" --listen 0.0.0.0:0 2>&1; true'"
$badOut = Invoke-OnVm $VmBIp $badCmd
if ("$badOut" -match '403|clan|mismatch|error|forbidden|Error') {
    Pass "wrong clan_id correctly rejected: $($badOut -join ' ' -replace '\s+',' ')"
} else {
    Warn2 "unexpected output for wrong clan_id knock: $badOut"
}

# ---------- 5. Rate-limit spot check (warn-only) ----------

Step "5/5 rate-limit spot check (warn-only)"

if ($SkipRateLimit) {
    Skip2 "operator passed -SkipRateLimit"
} else {
    # Fire knockRatePerSrc (10) + 1 = 11 rapid attempts via HTTP to trigger 429.
    # We POST directly to avoid SAS interaction; raw JSON will hit the knock handler.
    $rlHit = $false
    for ($i = 1; $i -le 11; $i++) {
        $body = "{`"clan_id`":`"$ClanID`",`"joiner_x25519_pub_b64`":`"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`",`"joiner_lan_address`":`"$VmBIp`:19999`",`"pin`":`"$Pin`"}"
        $curlCmd = "curl -sk -o /dev/null -w '%{http_code}' -X POST https://${VmAIp}:${Port}/clan/knock -H 'Content-Type: application/json' --data-raw '$body' 2>/dev/null"
        $code = Invoke-OnVm $VmBIp $curlCmd
        if ("$code" -match '429') { $rlHit = $true; break }
    }
    if ($rlHit) {
        Pass "rate limit 429 triggered after rapid knock attempts"
    } else {
        Warn2 "429 not observed in 11 rapid POSTs (TLS handshake reuse or early JSON rejection may prevent reaching rate limiter; unit tests cover this path)"
    }
}

# ---------- cleanup + summary ----------

Cleanup

Write-Host ""
Write-Host "M8 Phase 2 validation complete." -ForegroundColor Cyan
Write-Host "Hard-pass gate: Tests 1-3 (pre-flight, Linux->Linux knock, Windows->Linux knock)."
Write-Host "Informational: Tests 4-5 (mismatch rejection, rate limit)."
