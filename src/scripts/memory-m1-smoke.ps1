# MINTI Memory M1 single-daemon smoke (Windows, 127.0.0.1).
#
# Validates the spec §13 M1 surface end-to-end against a real daemon:
#   create Clan -> daemon up -> digest == empty-graph constant
#   research start -> add finding (--session) -> contributes_to edge exists
#   provenance.author_member_id == this member's id (daemon-set, §13.6)
#   archive -> tombstone visible          digest moves on every write
#   daemon RESTART -> graph + digest survive (memory.json, §13.2)
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\memory-m1-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo  = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin   = "$Repo\cland\minti-cland-m1smoke.exe"
$State = "$env:TEMP\cland-memory-m1"
$Conf  = "$State\config.yaml"
$Log   = "$State\daemon.log"
$Port  = 17992

# sha256 of empty input — the spec §13.5 empty-graph digest.
$EmptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process -Name "minti-cland-m1smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
    if (Test-Path $Log) { Write-Host "--- daemon log (tail 25) ---"; Get-Content $Log -Tail 25 }
    exit 1
}
function StartDaemon() {
    $p = Start-Process -FilePath $Bin `
        -ArgumentList @("--config", $Conf, "--state", $State) `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardError $Log -RedirectStandardOutput "$State\out.log"
    Start-Sleep -Seconds 2
    if (-not (Get-Process -Id $p.Id -EA SilentlyContinue)) { Fail "daemon died on start; see $Log" }
    return $p
}

# --- 1. build -----------------------------------------------------------------
Step "1/8 build minti-cland"
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Pass "built $Bin"

# --- 2. clean slate -----------------------------------------------------------
Step "2/8 clean state dir + config"
Get-Process -Name "minti-cland-m1smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item -Recurse -Force -EA SilentlyContinue $State
New-Item -ItemType Directory -Force $State | Out-Null
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
($confTemplate -creplace '__PORT__',"$Port" -creplace '__STATEDIR__',($State -replace '\\','/')) | Out-File -Encoding utf8 $Conf
$env:MINTI_CLAND_FORCE_HEALTHY = "1"
Pass "state at $State"

# --- 3. create Clan + start daemon ---------------------------------------------
Step "3/8 create Clan + start daemon"
$null = & $Bin create --config $Conf --state $State --address "127.0.0.1:$Port" --json
if ($LASTEXITCODE -ne 0) { Fail "create failed" }
$identity = Get-Content "$State\identity.json" -Raw | ConvertFrom-Json
$MemberID = $identity.member_id
if (-not $MemberID) { Fail "could not read member_id from identity.json" }
$daemon = StartDaemon
Pass "Clan created, daemon PID=$($daemon.Id), member=$MemberID"

# --- 4. empty digest ------------------------------------------------------------
Step "4/8 empty-graph digest"
$d0 = (& $Bin memory digest --config $Conf --state $State).Trim()
if ($LASTEXITCODE -ne 0) { Fail "memory digest failed" }
if ($d0 -ne $EmptyDigest) { Fail "empty digest = $d0, want $EmptyDigest" }
Pass "empty graph digests to the spec constant"

# --- 5. research session + finding ----------------------------------------------
Step "5/8 research start + add finding"
$sessionJson = & $Bin memory research start "M1 smoke research" --config $Conf --state $State --json
if ($LASTEXITCODE -ne 0) { Fail "research start failed" }
$session = $sessionJson | ConvertFrom-Json
if ($session.type -ne "research_session" -or $session.status -ne "active") { Fail "session node malformed: $sessionJson" }

$findingJson = & $Bin memory add --config $Conf --state $State --json `
    --type finding --session $session.id `
    --title "TLS pin mismatch reproduced on cold boot" `
    --body "Repro: power-cycle the thinkpad, knock before NTP sync." `
    --tags "tls,smoke"
if ($LASTEXITCODE -ne 0) { Fail "memory add failed" }
$finding = $findingJson | ConvertFrom-Json

if ($finding.provenance.author_member_id -ne $MemberID) {
    Fail "provenance author = $($finding.provenance.author_member_id), want daemon-set $MemberID"
}
if ($finding.session_id -ne $session.id) { Fail "finding not bound to session" }
if ($finding.rev -ne 1) { Fail "fresh node rev = $($finding.rev), want 1" }
Pass "finding added; provenance daemon-set to this member"

# --- 6. list + edge + archive ----------------------------------------------------
Step "6/8 list shows graph; contributes_to edge exists; archive works"
$graph = (& $Bin memory list --config $Conf --state $State --json) | ConvertFrom-Json
if ($graph.nodes.Count -ne 2) { Fail "node count = $($graph.nodes.Count), want 2" }
$edge = $graph.edges | Where-Object { $_.from -eq $finding.id -and $_.to -eq $session.id -and $_.relation -eq "contributes_to" }
if (-not $edge) { Fail "contributes_to edge missing" }
if ($edge.created_by -ne $MemberID) { Fail "edge created_by = $($edge.created_by), want $MemberID" }

$d1 = (& $Bin memory digest --config $Conf --state $State).Trim()
if ($d1 -eq $d0) { Fail "digest did not move after writes" }

$null = & $Bin memory archive $finding.id --config $Conf --state $State --json
if ($LASTEXITCODE -ne 0) { Fail "archive failed" }
$archived = ((& $Bin memory list --config $Conf --state $State --json --status archived) | ConvertFrom-Json).nodes
if ($archived.Count -ne 1 -or $archived[0].id -ne $finding.id) { Fail "archived tombstone not visible" }
if ($archived[0].rev -ne 2) { Fail "archive must bump rev (got $($archived[0].rev))" }
$d2 = (& $Bin memory digest --config $Conf --state $State).Trim()
if ($d2 -eq $d1) { Fail "digest did not move after archive" }
Pass "list + edge + archive + digest movement all correct"

# --- 7. restart persistence -------------------------------------------------------
Step "7/8 daemon restart -> memory persists"
Stop-Process -Id $daemon.Id -Force
Start-Sleep -Seconds 1
$daemon = StartDaemon
$graph2 = (& $Bin memory list --config $Conf --state $State --json) | ConvertFrom-Json
if ($graph2.nodes.Count -ne 2 -or $graph2.edges.Count -ne 1) {
    Fail "graph lost on restart: $($graph2.nodes.Count) nodes $($graph2.edges.Count) edges"
}
$d3 = (& $Bin memory digest --config $Conf --state $State).Trim()
if ($d3 -ne $d2) { Fail "digest changed across restart: $d3 vs $d2" }
$research = (& $Bin memory research list --config $Conf --state $State --json) | ConvertFrom-Json
if ($research.Count -ne 1 -or $research[0].id -ne $session.id) { Fail "research list lost the session" }
Pass "graph + digest + session survive restart"

# --- 8. cleanup --------------------------------------------------------------------
Step "8/8 cleanup"
Stop-Process -Id $daemon.Id -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
Remove-Item -Force -EA SilentlyContinue $Bin
Pass "daemon stopped, smoke binary removed (state left at $State for inspection)"

Write-Host ""
Write-Host "ALL MEMORY M1 SMOKE TESTS PASSED" -ForegroundColor Green
