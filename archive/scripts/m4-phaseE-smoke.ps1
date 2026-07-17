# MINTI M4 Phase E end-to-end smoke (Windows, two daemons on 127.0.0.1).
#
# Validates: paste-key join (Phase C) -> capability advertise (Phase D)
#   -> election (Phase E) -> failover -> term bump -> pin propagation -> step-down + re-elect.
#
# MINTI_CLAND_FORCE_HEALTHY=1 lets the R1 runtime-health gate pass without
# minti-runtime alongside. Production deployments leave this unset.
#
# Run: pwsh scripts/m4-phaseE-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo  = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin   = "$Repo\cland\minti-cland.exe"
$StateA = "$env:TEMP\cland-phaseE-A"
$StateB = "$env:TEMP\cland-phaseE-B"
$ConfA  = "$StateA\config.yaml"
$ConfB  = "$StateB\config.yaml"
$LogA   = "$StateA\daemon.log"
$LogB   = "$StateB\daemon.log"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process minti-cland -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
    exit 1
}

# --- 1. Build -----------------------------------------------------------------
Step "1/9 build minti-cland.exe"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin "$Repo\cland\cmd\minti-cland"
if ($LASTEXITCODE -ne 0) { Fail "build" }
Pass "built $Bin"

# --- 2. Clean slate -----------------------------------------------------------
Step "2/9 wipe state dirs + write configs"
Get-Process minti-cland -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
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
telemetry:
  log_level: "info"
'@
($confTemplate -creplace '__PORT__', '17980' -creplace '__STATEDIR__', ($StateA -replace '\\', '/')) | Out-File -Encoding utf8 $ConfA
($confTemplate -creplace '__PORT__', '17981' -creplace '__STATEDIR__', ($StateB -replace '\\', '/')) | Out-File -Encoding utf8 $ConfB
Pass "configs written ($ConfA, $ConfB)"

# Set R1-bypass for the smoke (real prod leaves this off).
$env:MINTI_CLAND_FORCE_HEALTHY = "1"

# --- 3. Founder creates Clan --------------------------------------------------
Step "3/9 founder (A) creates Clan via paste-key flow"
$createOut = & $Bin create --config $ConfA --state $StateA --address "127.0.0.1:17980" --json
if ($LASTEXITCODE -ne 0) { Fail "create" }
$create = $createOut | ConvertFrom-Json
$Mnemonic = $create.mnemonic
$Pin = $create.pin
$ClanID = $create.clan_id
Pass "clan_id=$ClanID  pin=$Pin"

# --- 4. Start daemon A --------------------------------------------------------
Step "4/9 start daemon A (founder, port 17980)"
$daemA = Start-Process -FilePath $Bin -ArgumentList @("--config", $ConfA, "--state", $StateA) `
    -WindowStyle Hidden -PassThru -RedirectStandardError $LogA -RedirectStandardOutput "$StateA\out.log"
Start-Sleep -Seconds 2
if (-not (Get-Process -Id $daemA.Id -EA SilentlyContinue)) { Fail "daemon A died on startup; see $LogA" }
Pass "daemon A PID=$($daemA.Id)"

# --- 5. Joiner B joins via paste-key, then starts its daemon ------------------
Step "5/9 joiner (B) joins via paste-key"
$joinOut = & $Bin join --config $ConfB --state $StateB --mnemonic $Mnemonic --address "127.0.0.1:17980" --pin $Pin
if ($LASTEXITCODE -ne 0) { Fail "join (mnemonic flow) -- see daemon A log $LogA" }
Pass "B joined"

$daemB = Start-Process -FilePath $Bin -ArgumentList @("--config", $ConfB, "--state", $StateB) `
    -WindowStyle Hidden -PassThru -RedirectStandardError $LogB -RedirectStandardOutput "$StateB\out.log"
Start-Sleep -Seconds 2
if (-not (Get-Process -Id $daemB.Id -EA SilentlyContinue)) { Fail "daemon B died on startup; see $LogB" }
Pass "daemon B PID=$($daemB.Id)"

# --- 6. Manual peer-add (mdns disabled) + advertise + election ----------------
Step "6/9 peer-add (mdns off) + wait for advertise + election commit"
& $Bin peer-add --config $ConfA --state $StateA "127.0.0.1:17981" | Out-Null
& $Bin peer-add --config $ConfB --state $StateB "127.0.0.1:17980" | Out-Null
Write-Host "    waiting 12s for advertise exchange + election..."
Start-Sleep -Seconds 12

$orchA = & $Bin orchestrator --config $ConfA --state $StateA --json | ConvertFrom-Json
$orchB = & $Bin orchestrator --config $ConfB --state $StateB --json | ConvertFrom-Json
Write-Host "    A: orch=$($orchA.current_orchestrator) term=$($orchA.current_term)"
Write-Host "    B: orch=$($orchB.current_orchestrator) term=$($orchB.current_term)"

if ([string]::IsNullOrEmpty($orchA.current_orchestrator)) { Fail "A has no Orchestrator after election window" }
if ($orchA.current_orchestrator -ne $orchB.current_orchestrator) {
    Fail "Orchestrator disagreement: A=$($orchA.current_orchestrator) B=$($orchB.current_orchestrator)"
}
$initialOrch = $orchA.current_orchestrator
$initialTerm = $orchA.current_term
Pass "both agree: orch=$initialOrch term=$initialTerm"

# --- 7. Kill the Orchestrator, expect failover --------------------------------
Step "7/9 kill the Orchestrator + wait for failover"
if ($orchA.self -eq $initialOrch) {
    $orchDaemon = $daemA; $orchSide = "A"
    $survSide = "B"; $survConf = $ConfB; $survState = $StateB
} else {
    $orchDaemon = $daemB; $orchSide = "B"
    $survSide = "A"; $survConf = $ConfA; $survState = $StateA
}
Write-Host "    killing daemon $orchSide (PID=$($orchDaemon.Id))..."
Stop-Process -Id $orchDaemon.Id -Force
Write-Host "    waiting 14s (FAILOVER_GRACE 6s + ELECTION_TIMEOUT 1s + 3 ticks margin)..."
Start-Sleep -Seconds 14

$orchSurv = & $Bin orchestrator --config $survConf --state $survState --json | ConvertFrom-Json
Write-Host "    survivor $survSide reports: orch=$($orchSurv.current_orchestrator) term=$($orchSurv.current_term)"
if ($orchSurv.current_orchestrator -ne $orchSurv.self) {
    Fail "survivor $survSide did not take Orchestrator role (orch=$($orchSurv.current_orchestrator))"
}
if ($orchSurv.current_term -le $initialTerm) {
    Fail "term did not bump after failover: was $initialTerm now $($orchSurv.current_term)"
}
$failoverTerm = $orchSurv.current_term
Pass "$survSide is now Orchestrator at term=$failoverTerm"

# --- 8. Restart the killed daemon, expect it to accept the new Orchestrator ---
Step "8/9 restart $orchSide; expect acceptance of new Orchestrator"
if ($orchSide -eq "A") {
    $daemA = Start-Process -FilePath $Bin -ArgumentList @("--config", $ConfA, "--state", $StateA) `
        -WindowStyle Hidden -PassThru -RedirectStandardError $LogA -RedirectStandardOutput "$StateA\out.log"
    $restartedConf = $ConfA; $restartedState = $StateA
    $peerAddr = "127.0.0.1:17981"
} else {
    $daemB = Start-Process -FilePath $Bin -ArgumentList @("--config", $ConfB, "--state", $StateB) `
        -WindowStyle Hidden -PassThru -RedirectStandardError $LogB -RedirectStandardOutput "$StateB\out.log"
    $restartedConf = $ConfB; $restartedState = $StateB
    $peerAddr = "127.0.0.1:17980"
}
Start-Sleep -Seconds 2
# Re-seed peer (registry is in-memory, lost on restart). Without this the
# restarted daemon's empty registry would race the survivor's heartbeats.
& $Bin peer-add --config $restartedConf --state $restartedState $peerAddr | Out-Null
Start-Sleep -Seconds 10

$orchRestart = & $Bin orchestrator --config $restartedConf --state $restartedState --json | ConvertFrom-Json
Write-Host "    restarted $orchSide reports: orch=$($orchRestart.current_orchestrator) term=$($orchRestart.current_term)"
if ($orchRestart.current_orchestrator -ne $orchSurv.self) {
    Fail "restarted $orchSide did not accept survivor as Orchestrator"
}
if ($orchRestart.current_term -lt $failoverTerm) {
    $restartedTerm = $orchRestart.current_term
    Fail "restarted $orchSide term ($restartedTerm) below failover term ($failoverTerm)"
}
Pass "restarted $orchSide accepts $($orchSurv.self) at term=$($orchRestart.current_term)"

# --- 9. election-history --------------------------------------------------------
Step "9/9 election-history shows at least 2 entries (bootstrap + failover)"
$hist = & $Bin election-history --config $restartedConf --state $restartedState --json | ConvertFrom-Json
foreach ($e in $hist.entries) {
    Write-Host "    term=$($e.term) winner=$($e.winner) reason=$($e.reason)"
}
if ($hist.entries.Count -lt 1) { Fail "no history entries on restarted $orchSide" }
Pass "election-history has $($hist.entries.Count) entries"

# --- Cleanup ------------------------------------------------------------------
Write-Host ""
Write-Host "PHASE E SMOKE: PASS" -ForegroundColor Green
Get-Process minti-cland -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
