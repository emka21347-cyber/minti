# MINTI Memory M3 three-daemon scribe smoke (Windows, 127.0.0.1).
#
# Validates spec §13.8 on a real rig:
#   3 nodes with forced reasoning scores A=70 B=50 C=30
#   -> A elected Orchestrator (highest), C elected Scribe (weakest capable)
#   -> `scribe --json` agrees on every member (heartbeat adoption)
#   -> kill C -> Orchestrator re-selects B within a few heartbeats (no lease)
#   -> pin-scribe --self on B is a no-op for selection (B already scribe);
#      clear works
#
# MINTI_CLAND_FORCE_SCORE differentiates node strength without a live
# runtime (implies FORCE_HEALTHY semantics). Smoke-only.
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\memory-m3-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin  = "$Repo\cland\minti-cland-m3smoke.exe"
$S = @{}
foreach ($n in "A","B","C") { $S[$n] = "$env:TEMP\cland-scribe-$n" }
$Port = @{ A = 17986; B = 17987; C = 17988 }
$Score = @{ A = "70"; B = "50"; C = "30" }

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process -Name "minti-cland-m3smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_SCORE -EA SilentlyContinue
    foreach ($n in "A","B","C") {
        $log = "$($S[$n])\daemon.log"
        if (Test-Path $log) { Write-Host "--- daemon $n log (tail 8) ---"; Get-Content $log -Tail 8 }
    }
    exit 1
}
function Conf($n) { return "$($S[$n])\config.yaml" }
function StartDaemon($n) {
    # FORCE_SCORE is read from the daemon's environment at advertise/select
    # time — set per-daemon before spawn.
    $env:MINTI_CLAND_FORCE_SCORE = $Score[$n]
    $p = Start-Process -FilePath $Bin -ArgumentList @("--config", (Conf $n), "--state", $S[$n]) `
        -WindowStyle Hidden -PassThru -RedirectStandardError "$($S[$n])\daemon.log" -RedirectStandardOutput "$($S[$n])\out.log"
    Start-Sleep -Seconds 2
    if (-not (Get-Process -Id $p.Id -EA SilentlyContinue)) { Fail "daemon $n died; see $($S[$n])\daemon.log" }
    return $p
}
function ScribeOf($n) {
    $env:MINTI_CLAND_FORCE_SCORE = $Score[$n]
    $r = (& $Bin scribe --config (Conf $n) --state $S[$n] --json) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { Fail "scribe --json failed on $n" }
    return $r
}

# --- 1. build ---------------------------------------------------------------
Step "1/6 build"
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Pass "built"

# --- 2. clean + configs --------------------------------------------------------
Step "2/6 clean state + configs"
Get-Process -Name "minti-cland-m3smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
foreach ($n in "A","B","C") {
    Remove-Item -Recurse -Force -EA SilentlyContinue $S[$n]
    New-Item -ItemType Directory -Force $S[$n] | Out-Null
    @"
listen:
  address: "127.0.0.1"
  port: $($Port[$n])
state:
  dir: "$($S[$n] -replace '\\','/')"
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
"@ | Out-File -Encoding utf8 (Conf $n)
}
Pass "3 state dirs ready"

# --- 3. create + join + daemons ---------------------------------------------------
Step "3/6 A creates; B + C join; all daemons up"
$env:MINTI_CLAND_FORCE_SCORE = $Score.A
$create = (& $Bin create --config (Conf "A") --state $S.A --address "127.0.0.1:$($Port.A)" --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "create failed" }
$daemons = @{}
$daemons["A"] = StartDaemon "A"
foreach ($n in "B","C") {
    $env:MINTI_CLAND_FORCE_SCORE = $Score[$n]
    & $Bin join --config (Conf $n) --state $S[$n] --mnemonic $create.mnemonic --address "127.0.0.1:$($Port.A)" --pin $create.pin | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "join $n failed" }
    $daemons[$n] = StartDaemon $n
}
$ids = @{}
foreach ($n in "A","B","C") { $ids[$n] = (Get-Content "$($S[$n])\identity.json" -Raw | ConvertFrom-Json).member_id }
Pass "A=$($ids.A.Substring(0,8)) B=$($ids.B.Substring(0,8)) C=$($ids.C.Substring(0,8))"

# --- 4. full mesh peer-add + settle -------------------------------------------------
Step "4/6 peer-add full mesh + election + scribe settle"
foreach ($n in "A","B","C") {
    $env:MINTI_CLAND_FORCE_SCORE = $Score[$n]
    foreach ($m in "A","B","C") {
        if ($n -ne $m) { & $Bin peer-add --config (Conf $n) --state $S[$n] "127.0.0.1:$($Port[$m])" | Out-Null }
    }
}
Start-Sleep -Seconds 14
$orch = (& $Bin orchestrator --config (Conf "A") --state $S.A --json) | ConvertFrom-Json
if ($orch.current_orchestrator -ne $ids.A) {
    Fail "Orchestrator should be A (score 70), got $($orch.current_orchestrator)"
}
$sA = ScribeOf "A"; $sB = ScribeOf "B"; $sC = ScribeOf "C"
Write-Host "    scribe view: A->$($sA.current_scribe.Substring(0,8)) B->$($sB.current_scribe.Substring(0,8)) C->$($sC.current_scribe.Substring(0,8))"
if ($sA.current_scribe -ne $ids.C) { Fail "scribe should be C (weakest, score 30), A sees $($sA.current_scribe)" }
if ($sB.current_scribe -ne $ids.C -or $sC.current_scribe -ne $ids.C) { Fail "scribe view diverges across members" }
if (-not $sC.is_self) { Fail "C must report is_self=true" }
Pass "A orchestrates (70), C scribes (30), all three agree"

# --- 5. kill the scribe -> re-selection ----------------------------------------------
Step "5/6 kill scribe C -> Orchestrator re-selects B"
Stop-Process -Id $daemons["C"].Id -Force
# No lease: C drops out when its registry entries go stale (Live window 4s,
# ad freshness 90s — but C was heartbeat-seen so the 4s Live gate applies).
# Poll up to 30s for the re-selection to propagate to A and B.
$reselected = $false
for ($i = 0; $i -lt 15; $i++) {
    Start-Sleep -Seconds 2
    $sA = ScribeOf "A"
    if ($sA.current_scribe -eq $ids.B) { $reselected = $true; break }
}
if (-not $reselected) { Fail "scribe never re-selected to B after C died (A sees $($sA.current_scribe))" }
$sB = ScribeOf "B"
if ($sB.current_scribe -ne $ids.B -or -not $sB.is_self) { Fail "B does not agree it is the new scribe" }
Pass "re-selected to B (next-weakest) in $((2*($i+1)))s"

# --- 6. pin override ---------------------------------------------------------------------
Step "6/6 pin-scribe on A overrides selection; clear restores"
$env:MINTI_CLAND_FORCE_SCORE = $Score.A
& $Bin pin-scribe --self --config (Conf "A") --state $S.A | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "pin-scribe --self failed" }
$pinned = $false
for ($i = 0; $i -lt 10; $i++) {
    Start-Sleep -Seconds 2
    $sB = ScribeOf "B"
    if ($sB.current_scribe -eq $ids.A) { $pinned = $true; break }
}
if (-not $pinned) { Fail "pin never moved the scribe to A (B sees $($sB.current_scribe))" }
$env:MINTI_CLAND_FORCE_SCORE = $Score.A
& $Bin pin-scribe --clear --config (Conf "A") --state $S.A | Out-Null
$cleared = $false
for ($i = 0; $i -lt 10; $i++) {
    Start-Sleep -Seconds 2
    $sA = ScribeOf "A"
    if ($sA.current_scribe -eq $ids.B) { $cleared = $true; break }
}
if (-not $cleared) { Fail "clearing the pin never restored B (A sees $($sA.current_scribe))" }
Pass "pin moved the scribe to A; clear restored B"

# --- cleanup -------------------------------------------------------------------------------
Get-Process -Name "minti-cland-m3smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_SCORE -EA SilentlyContinue
Remove-Item -Force -EA SilentlyContinue $Bin
Write-Host ""
Write-Host "ALL MEMORY M3 SMOKE TESTS PASSED" -ForegroundColor Green
