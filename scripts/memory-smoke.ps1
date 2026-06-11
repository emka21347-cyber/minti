# MINTI Memory M2 two-daemon gossip smoke (Windows, 127.0.0.1).
#
# Validates spec §13.5 end-to-end across two real daemons:
#   member_joined system event (§13.7.1) written on the founder at join,
#     gossiped to the joiner          (request leg: Orchestrator -> follower)
#   research session + finding added on the ORCHESTRATOR appear on the
#     follower within a heartbeat round (request leg)
#   fact added + finding archived on the FOLLOWER appear on the
#     Orchestrator (RESPONSE leg -- the §13.5 ack digest; without it,
#     follower research contributions would never propagate)
#   failover -> survivor writes the §13.7.1 election_failover node;
#     restarted daemon converges to the full union
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\memory-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo   = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin    = "$Repo\cland\minti-cland-memsmoke.exe"
$StateA = "$env:TEMP\cland-memory-A"
$StateB = "$env:TEMP\cland-memory-B"
$ConfA  = "$StateA\config.yaml"
$ConfB  = "$StateB\config.yaml"
$LogA   = "$StateA\daemon.log"
$LogB   = "$StateB\daemon.log"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process -Name "minti-cland-memsmoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
    if (Test-Path $LogA) { Write-Host "--- daemon A log (tail 15) ---"; Get-Content $LogA -Tail 15 }
    if (Test-Path $LogB) { Write-Host "--- daemon B log (tail 15) ---"; Get-Content $LogB -Tail 15 }
    exit 1
}
function MemList($conf, $state) {
    $raw = & $Bin memory list --config $conf --state $state --json
    if ($LASTEXITCODE -ne 0) { Fail "memory list failed against $state" }
    return $raw | ConvertFrom-Json
}
function MemDigest($conf, $state) {
    $d = (& $Bin memory digest --config $conf --state $state).Trim()
    if ($LASTEXITCODE -ne 0) { Fail "memory digest failed against $state" }
    return $d
}
function StartDaemon($conf, $state, $log) {
    $p = Start-Process -FilePath $Bin -ArgumentList @("--config", $conf, "--state", $state) `
        -WindowStyle Hidden -PassThru -RedirectStandardError $log -RedirectStandardOutput "$state\out.log"
    Start-Sleep -Seconds 2
    if (-not (Get-Process -Id $p.Id -EA SilentlyContinue)) { Fail "daemon died on start; see $log" }
    return $p
}

# --- 1. build -------------------------------------------------------------------
Step "1/9 build"
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Pass "built $Bin"

# --- 2. clean slate ---------------------------------------------------------------
Step "2/9 clean state + configs"
Get-Process -Name "minti-cland-memsmoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item -Recurse -Force -EA SilentlyContinue $StateA, $StateB
New-Item -ItemType Directory -Force $StateA, $StateB | Out-Null
$confTemplate = @'
listen:
  address: "127.0.0.1"
  port: __PORT__
state:
  dir: "__STATEDIR__"
discovery:
  mdns_enabled: false
runtime:
  base_url: "http://127.0.0.1:7780"
election:
  heartbeat_interval: 2s
  lease_duration: 8s
  failover_grace: 6s
  election_timeout: 1s
  history_size: 32
'@
($confTemplate -creplace '__PORT__','17984' -creplace '__STATEDIR__',($StateA -replace '\\','/')) | Out-File -Encoding utf8 $ConfA
($confTemplate -creplace '__PORT__','17985' -creplace '__STATEDIR__',($StateB -replace '\\','/')) | Out-File -Encoding utf8 $ConfB
$env:MINTI_CLAND_FORCE_HEALTHY = "1"
Pass "configs written"

# --- 3. A creates + daemon up --------------------------------------------------------
Step "3/9 founder A creates Clan + daemon"
$create = (& $Bin create --config $ConfA --state $StateA --address "127.0.0.1:17984" --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "create failed" }
$daemA = StartDaemon $ConfA $StateA $LogA
$idA = (Get-Content "$StateA\identity.json" -Raw | ConvertFrom-Json).member_id
Pass "A up (member $idA)"

# --- 4. B joins + daemon up -----------------------------------------------------------
Step "4/9 B joins via paste-key + daemon"
& $Bin join --config $ConfB --state $StateB --mnemonic $create.mnemonic --address "127.0.0.1:17984" --pin $create.pin | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "join failed" }
$daemB = StartDaemon $ConfB $StateB $LogB
$idB = (Get-Content "$StateB\identity.json" -Raw | ConvertFrom-Json).member_id
Pass "B up (member $idB)"

# --- 5. pair + election ----------------------------------------------------------------
Step "5/9 peer-add + election settles"
& $Bin peer-add --config $ConfA --state $StateA "127.0.0.1:17985" | Out-Null
& $Bin peer-add --config $ConfB --state $StateB "127.0.0.1:17984" | Out-Null
Start-Sleep -Seconds 12
$orchA = (& $Bin orchestrator --config $ConfA --state $StateA --json) | ConvertFrom-Json
$orchB = (& $Bin orchestrator --config $ConfB --state $StateB --json) | ConvertFrom-Json
if (-not $orchA.current_orchestrator -or ($orchA.current_orchestrator -ne $orchB.current_orchestrator)) {
    Fail "election did not settle (A=$($orchA.current_orchestrator) B=$($orchB.current_orchestrator))"
}
# Map orchestrator/follower onto A/B (founder usually wins the tiebreak, but don't assume).
if ($orchA.current_orchestrator -eq $idA) {
    $OConf=$ConfA; $OState=$StateA; $FConf=$ConfB; $FState=$StateB; $orchSide="A"; $follSide="B"
    $orchDaemon=$daemA; $orchPort="17984"; $follPort="17985"; $FLog=$LogB; $OLog=$LogA
} else {
    $OConf=$ConfB; $OState=$StateB; $FConf=$ConfA; $FState=$StateA; $orchSide="B"; $follSide="A"
    $orchDaemon=$daemB; $orchPort="17985"; $follPort="17984"; $FLog=$LogA; $OLog=$LogB
}
Pass "Orchestrator=$orchSide follower=$follSide (term $($orchA.current_term))"

# --- 6. member_joined event gossips ------------------------------------------------------
Step "6/9 member_joined system event converges"
Start-Sleep -Seconds 4
$gB = MemList $FConf $FState
$joined = $gB.nodes | Where-Object { $_.type -eq "event" -and $_.provenance.source -eq "system" -and $_.title -like "member_joined:*" }
if (-not $joined) { Fail "follower never received the member_joined system node" }
$dA = MemDigest $OConf $OState; $dB = MemDigest $FConf $FState
if ($dA -ne $dB) { Fail "digests diverge after join event: $dA vs $dB" }
Pass "member_joined gossiped; digests equal"

# --- 7. orchestrator-side research propagates (request leg) -------------------------------
Step "7/9 research on the Orchestrator -> follower converges (request leg)"
$session = (& $Bin memory research start --config $OConf --state $OState --json "Memory smoke research") | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "research start failed" }
$finding = (& $Bin memory add --config $OConf --state $OState --json `
    --type finding --session $session.id --title "finding from the orchestrator" --tags smoke) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "memory add on orchestrator failed" }
Start-Sleep -Seconds 6
$gF = MemList $FConf $FState
if (-not ($gF.nodes | Where-Object { $_.id -eq $session.id })) { Fail "session node did not reach the follower" }
if (-not ($gF.nodes | Where-Object { $_.id -eq $finding.id })) { Fail "finding did not reach the follower" }
if (-not ($gF.edges | Where-Object { $_.from -eq $finding.id -and $_.relation -eq "contributes_to" })) {
    Fail "contributes_to edge did not reach the follower"
}
if ((MemDigest $OConf $OState) -ne (MemDigest $FConf $FState)) { Fail "digests diverge after orchestrator add" }
Pass "orchestrator research reached the follower"

# --- 8. FOLLOWER-side edits propagate (response leg) ---------------------------------------
Step "8/9 follower adds + archives -> Orchestrator converges (RESPONSE leg)"
$fact = (& $Bin memory add --config $FConf --state $FState --json `
    --type fact --session $session.id --title "fact contributed by the follower") | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "memory add on follower failed" }
& $Bin memory archive --config $FConf --state $FState $finding.id --json | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "archive on follower failed" }
Start-Sleep -Seconds 8
$gO = MemList $OConf $OState
if (-not ($gO.nodes | Where-Object { $_.id -eq $fact.id })) {
    Fail "follower's fact never reached the Orchestrator -- §13.5 response leg broken"
}
$archivedOnO = $gO.nodes | Where-Object { $_.id -eq $finding.id }
if ($archivedOnO.status -ne "archived") {
    Fail "follower's archive (tombstone) never reached the Orchestrator (status=$($archivedOnO.status))"
}
if ((MemDigest $OConf $OState) -ne (MemDigest $FConf $FState)) { Fail "digests diverge after follower edits" }
Pass "follower edits reached the Orchestrator; graphs converged"

# --- 9. failover event + restarted convergence ----------------------------------------------
Step "9/9 failover -> election_failover node -> restarted daemon converges"
Stop-Process -Id $orchDaemon.Id -Force
Start-Sleep -Seconds 14
$surv = (& $Bin orchestrator --config $FConf --state $FState --json) | ConvertFrom-Json
if ($surv.current_orchestrator -ne $surv.self) { Fail "survivor did not take over" }
$gS = MemList $FConf $FState
$failNode = $gS.nodes | Where-Object { $_.type -eq "event" -and $_.title -like "election failover:*" }
if (-not $failNode) { Fail "survivor did not write the election_failover system node" }

# Restart the killed side, re-seed its peer registry, expect full convergence.
# Heal path needs: restart (2s) + R6 startup grace (6s) + lease-silent election
# + survivor accepts same-term preferred candidate + first ack-pull -- poll up
# to 36s rather than guessing a fixed sleep.
$restarted = StartDaemon $OConf $OState $OLog
& $Bin peer-add --config $OConf --state $OState "127.0.0.1:$follPort" | Out-Null
$converged = $false
for ($i = 0; $i -lt 18; $i++) {
    Start-Sleep -Seconds 2
    if ((MemDigest $OConf $OState) -eq (MemDigest $FConf $FState)) { $converged = $true; break }
}
if (-not $converged) { Fail "digests still diverged after 36s post-restart" }
$gR = MemList $OConf $OState
if (-not ($gR.nodes | Where-Object { $_.id -eq $fact.id })) { Fail "restarted daemon missing follower fact" }
if (-not ($gR.nodes | Where-Object { $_.type -eq "event" -and $_.title -like "election failover:*" })) {
    Fail "restarted daemon missing the failover node"
}
Pass "failover recorded; restarted daemon converged to the union ($((2*($i+1)))s)"

# --- cleanup ---------------------------------------------------------------------------------
Get-Process -Name "minti-cland-memsmoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
Remove-Item -Force -EA SilentlyContinue $Bin
Write-Host ""
Write-Host "ALL MEMORY M2 SMOKE TESTS PASSED" -ForegroundColor Green
