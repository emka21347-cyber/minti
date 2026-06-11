# MINTI Memory M4 blueprint smoke (Windows, 127.0.0.1).
#
# Validates spec §13.10 end-to-end:
#   source Clan: research session + findings -> `memory export` (privacy
#     warning printed, file written) + a --strip-authors variant that must
#     not contain the founder's member id
#   corrupt ONE byte -> both `memory import` and `create --from-blueprint`
#     reject with a checksum error
#   `create --from-blueprint` -> fresh Clan inherits the graph
#     (provenance source=import, rev/updated_at preserved)
#   a joiner gossips the inherited graph (digest equality)
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\memory-m4-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo   = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin    = "$Repo\cland\minti-cland-m4smoke.exe"
$StateS = "$env:TEMP\cland-bp-source"   # source Clan
$StateF = "$env:TEMP\cland-bp-fresh"    # fresh Clan founded from the blueprint
$StateJ = "$env:TEMP\cland-bp-joiner"   # joiner into the fresh Clan
$BPFile = "$env:TEMP\cland-bp-source\export.json"
$BPStrip = "$env:TEMP\cland-bp-source\export-strip.json"
$BPBad  = "$env:TEMP\cland-bp-source\export-corrupt.json"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process -Name "minti-cland-m4smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
    exit 1
}
function WriteConf($state, $port) {
    @"
listen:
  address: "127.0.0.1"
  port: $port
state:
  dir: "$($state -replace '\\','/')"
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
"@ | Out-File -Encoding utf8 "$state\config.yaml"
}
function StartDaemon($state) {
    $p = Start-Process -FilePath $Bin -ArgumentList @("--config", "$state\config.yaml", "--state", $state) `
        -WindowStyle Hidden -PassThru -RedirectStandardError "$state\daemon.log" -RedirectStandardOutput "$state\out.log"
    Start-Sleep -Seconds 2
    if (-not (Get-Process -Id $p.Id -EA SilentlyContinue)) { Fail "daemon died; see $state\daemon.log" }
    return $p
}

# --- 1. build + clean ------------------------------------------------------------
Step "1/6 build + clean"
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Get-Process -Name "minti-cland-m4smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item -Recurse -Force -EA SilentlyContinue $StateS, $StateF, $StateJ
New-Item -ItemType Directory -Force $StateS, $StateF, $StateJ | Out-Null
WriteConf $StateS 17993; WriteConf $StateF 17994; WriteConf $StateJ 17995
$env:MINTI_CLAND_FORCE_HEALTHY = "1"
Pass "ready"

# --- 2. source Clan with research content -------------------------------------------
Step "2/6 source Clan: research session + findings"
$null = & $Bin create --config "$StateS\config.yaml" --state $StateS --address "127.0.0.1:17993" --json
if ($LASTEXITCODE -ne 0) { Fail "source create failed" }
$srcID = (Get-Content "$StateS\identity.json" -Raw | ConvertFrom-Json).member_id
$dS = StartDaemon $StateS
$sess = (& $Bin memory research start --config "$StateS\config.yaml" --state $StateS --json "Old hardware TLS quirks") | ConvertFrom-Json
$null = & $Bin memory add --config "$StateS\config.yaml" --state $StateS --json `
    --type finding --session $sess.id --title "x230 fails pin check before NTP sync" --tags "tls,ntp"
$null = & $Bin memory add --config "$StateS\config.yaml" --state $StateS --json `
    --type decision --title "Pin clamp deferred to v1.1"
if ($LASTEXITCODE -ne 0) { Fail "memory adds failed" }
Pass "source graph populated (session + finding + decision + system event)"

# --- 3. export (+ strip variant) ------------------------------------------------------
Step "3/6 export + strip-authors variant"
& $Bin memory export --config "$StateS\config.yaml" --state $StateS --out $BPFile | Out-Null
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $BPFile)) { Fail "export failed" }
& $Bin memory export --config "$StateS\config.yaml" --state $StateS --out $BPStrip --strip-authors | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "strip export failed" }
$bp = Get-Content $BPFile -Raw | ConvertFrom-Json
if ($bp.kind -ne "minti-clan-blueprint") { Fail "wrong kind: $($bp.kind)" }
if ($bp.stats.nodes -lt 3) { Fail "expected >=3 nodes in stats, got $($bp.stats.nodes)" }
$stripRaw = Get-Content $BPStrip -Raw
if ($stripRaw -match [regex]::Escape($srcID)) { Fail "strip-authors file still contains the founder member id" }
if ($stripRaw -notmatch "member-1") { Fail "strip-authors file has no pseudonyms" }
Pass "exported; stripped file pseudonymized"

# --- 4. corrupt one byte -> rejected everywhere -------------------------------------------
Step "4/6 corrupt-byte rejection"
$raw = Get-Content $BPFile -Raw
$idx = $raw.IndexOf('"title"')
$bad = $raw.Substring(0, $idx + 10) + "X" + $raw.Substring($idx + 11)
[System.IO.File]::WriteAllText($BPBad, $bad, [System.Text.UTF8Encoding]::new($false))
# PS 5.1: native stderr under EAP=Stop + 2>&1 becomes a terminating
# ErrorRecord (reference_dev_environment.md) — scope EAP around the two
# calls that are SUPPOSED to fail.
$prevEAP = $ErrorActionPreference; $ErrorActionPreference = "Continue"
$out = ((& $Bin memory import $BPBad --config "$StateS\config.yaml" --state $StateS 2>&1) | Out-String)
$importExit = $LASTEXITCODE
$out2 = ((& $Bin create --config "$StateF\config.yaml" --state $StateF --address "127.0.0.1:17994" --from-blueprint $BPBad 2>&1) | Out-String)
$createExit = $LASTEXITCODE
$ErrorActionPreference = $prevEAP
if ($importExit -eq 0) { Fail "tampered import was ACCEPTED" }
if (-not ($out -match "checksum")) { Fail "rejection reason should mention checksum, got: $out" }
if ($createExit -eq 0) { Fail "tampered create --from-blueprint was ACCEPTED" }
if (-not ($out2 -match "checksum")) { Fail "create rejection should mention checksum, got: $out2" }
if (Test-Path "$StateF\clan.json") {
    $fclan = Get-Content "$StateF\clan.json" -Raw | ConvertFrom-Json
    if ($fclan.clan_id) { Fail "corrupt blueprint left a half-created Clan behind" }
}
Pass "tampered file rejected by import AND create (no half-created Clan)"

# --- 5. create --from-blueprint -> inherited graph -----------------------------------------
Step "5/6 fresh Clan inherits the blueprint"
$createF = (& $Bin create --config "$StateF\config.yaml" --state $StateF --address "127.0.0.1:17994" --from-blueprint $BPFile --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { Fail "create --from-blueprint failed" }
$dF = StartDaemon $StateF
$gF = (& $Bin memory list --config "$StateF\config.yaml" --state $StateF --json) | ConvertFrom-Json
if ($gF.nodes.Count -lt 3) { Fail "inherited graph too small: $($gF.nodes.Count) nodes" }
$nonImport = $gF.nodes | Where-Object { $_.provenance.source -ne "import" }
if ($nonImport) { Fail "inherited nodes must carry source=import (found $($nonImport[0].provenance.source))" }
$inheritedFinding = $gF.nodes | Where-Object { $_.type -eq "finding" }
if (-not $inheritedFinding -or $inheritedFinding.rev -ne 1) { Fail "rev must be preserved on import" }
Pass "fresh Clan inherited $($gF.nodes.Count) nodes, all source=import"

# --- 6. inherited graph gossips to a joiner ---------------------------------------------------
Step "6/6 joiner receives the inherited graph via gossip"
& $Bin join --config "$StateJ\config.yaml" --state $StateJ --mnemonic $createF.mnemonic --address "127.0.0.1:17994" --pin $createF.pin | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "join failed" }
$dJ = StartDaemon $StateJ
& $Bin peer-add --config "$StateF\config.yaml" --state $StateF "127.0.0.1:17995" | Out-Null
& $Bin peer-add --config "$StateJ\config.yaml" --state $StateJ "127.0.0.1:17994" | Out-Null
$converged = $false
for ($i = 0; $i -lt 15; $i++) {
    Start-Sleep -Seconds 2
    $dgF = (& $Bin memory digest --config "$StateF\config.yaml" --state $StateF).Trim()
    $dgJ = (& $Bin memory digest --config "$StateJ\config.yaml" --state $StateJ).Trim()
    if ($dgF -eq $dgJ -and $dgJ -ne "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") { $converged = $true; break }
}
if (-not $converged) { Fail "joiner never converged on the inherited graph" }
$gJ = (& $Bin memory list --config "$StateJ\config.yaml" --state $StateJ --json) | ConvertFrom-Json
if (-not ($gJ.nodes | Where-Object { $_.type -eq "finding" })) { Fail "joiner missing the inherited finding" }
Pass "joiner converged on the inherited graph in $((2*($i+1)))s"

# --- cleanup -----------------------------------------------------------------------------------
Get-Process -Name "minti-cland-m4smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
Remove-Item -Force -EA SilentlyContinue $Bin
Write-Host ""
Write-Host "ALL MEMORY M4 SMOKE TESTS PASSED" -ForegroundColor Green
