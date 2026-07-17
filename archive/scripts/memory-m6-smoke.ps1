# MINTI Memory M6 scribe-distillation smoke (Windows, 127.0.0.1, REAL LLM).
#
# Validates spec §13.9 end-to-end with an actual local model: a 1-node Clan
# (scribe == orchestrator == self, the legal §13.8 edge case) watches a chat
# file + an open research session, distills through Ollama's OpenAI-compatible
# /v1/chat/completions (runtime.base_url points at 11434), and lands
# status:"proposed" source:"scribe" nodes. Asserts the human gate: NOTHING
# the scribe wrote is active until an explicit promote.
#
# Requires: Ollama running on 11434 with the model below pulled.
# (gemma4 — no <think> traces; qwen3.6 thinks via the OpenAI surface.)
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\memory-m6-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo    = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin     = "$Repo\cland\minti-cland-m6smoke.exe"
$State   = "$env:TEMP\cland-scribe-smoke"
$ChatDir = "$env:TEMP\cland-scribe-chat"
$Conf    = "$State\config.yaml"
$Model   = "gemma4:31b"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    Get-Process -Name "minti-cland-m6smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    foreach ($v in "MINTI_CLAND_FORCE_SCORE","MINTI_CLAND_SCRIBE_MODEL","MINTI_CLAND_SCRIBE_INTERVAL","MINTI_CLAND_SCRIBE_CHATDIR") { Remove-Item "env:$v" -EA SilentlyContinue }
    if (Test-Path "$State\daemon.log") { Write-Host "--- daemon log (tail 25) ---"; Get-Content "$State\daemon.log" -Tail 25 }
    exit 1
}

# --- 1. preconditions + build -----------------------------------------------------
Step "1/6 preconditions + build"
try { $tags = (Invoke-RestMethod -Uri "http://127.0.0.1:11434/api/tags" -TimeoutSec 5).models.name } catch { Fail "Ollama not reachable on 11434" }
if ($tags -notcontains $Model) { Fail "model $Model not pulled in Ollama" }
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Pass "Ollama up with $Model; binary built"

# --- 2. clean rig -------------------------------------------------------------------
Step "2/6 clean state + config (runtime -> Ollama's OpenAI surface)"
Get-Process -Name "minti-cland-m6smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item -Recurse -Force -EA SilentlyContinue $State, $ChatDir
New-Item -ItemType Directory -Force $State, $ChatDir | Out-Null
# Chat file must EXIST at the scribe's seed tick; appends after it distill.
New-Item -ItemType File -Path "$ChatDir\workspace-chat.jsonl" | Out-Null
@"
listen:
  address: "127.0.0.1"
  port: 17996
state:
  dir: "$($State -replace '\\','/')"
discovery:
  mdns_enabled: false
runtime:
  base_url: "http://127.0.0.1:11434"
election:
  heartbeat_interval: 2s
  lease_duration: 8s
  failover_grace: 6s
  election_timeout: 1s
  history_size: 32
"@ | Out-File -Encoding utf8 $Conf
$env:MINTI_CLAND_FORCE_SCORE    = "30"
$env:MINTI_CLAND_SCRIBE_MODEL   = $Model
$env:MINTI_CLAND_SCRIBE_INTERVAL = "8s"
$env:MINTI_CLAND_SCRIBE_CHATDIR = $ChatDir
Pass "rig ready (scribe interval 8s, chat dir $ChatDir)"

# --- 3. 1-node Clan: scribe == orchestrator == self ----------------------------------
Step "3/6 create + daemon; self becomes scribe"
$null = & $Bin create --config $Conf --state $State --address "127.0.0.1:17996" --json
if ($LASTEXITCODE -ne 0) { Fail "create failed" }
$d = Start-Process -FilePath $Bin -ArgumentList @("--config",$Conf,"--state",$State) `
    -WindowStyle Hidden -PassThru -RedirectStandardError "$State\daemon.log" -RedirectStandardOutput "$State\out.log"
Start-Sleep -Seconds 3
if (-not (Get-Process -Id $d.Id -EA SilentlyContinue)) { Fail "daemon died; see $State\daemon.log" }
# Scribe selection rides the first election, which waits out the R6 startup
# grace (6s) — poll up to 30s rather than guessing.
$selfScribe = $false
for ($i = 0; $i -lt 15; $i++) {
    Start-Sleep -Seconds 2
    $sr = (& $Bin scribe --config $Conf --state $State --json) | ConvertFrom-Json
    if ($sr.is_self) { $selfScribe = $true; break }
}
if (-not $selfScribe) { Fail "1-node Clan never self-scribed (last: '$($sr.current_scribe)')" }
Pass "scribe == self (§13.8 one-node edge case)"

# --- 4. feed it real activity ----------------------------------------------------------
Step "4/6 research session + finding + chat (post-seed)"
Start-Sleep -Seconds 6   # let the scribe's first (seeding) tick pass
$sess = (& $Bin memory research start --config $Conf --state $State --json "ZIM integrity research") | ConvertFrom-Json
$null = & $Bin memory add --config $Conf --state $State --json `
    --type finding --session $sess.id `
    --title "kiwix 302 redirect strips the Digest header" `
    --body "Observed on the load balancer: the sha256 sidecar is authoritative, the redirect header is not."
if ($LASTEXITCODE -ne 0) { Fail "research seeding failed" }
@'
{"role":"user","text":"why did the wiki pack fail checksum on the thinkpad?"}
{"role":"assistant","text":"the mirror served a truncated ZIM. we decided to ALWAYS verify the sha256 sidecar before serving wiki content, and to re-download on mismatch."}
{"role":"user","text":"ok make that a permanent rule for every content pack"}
{"role":"assistant","text":"agreed: every content pack download must verify its pinned sha256 before install; truncated files are deleted and re-fetched once."}
'@ | Add-Content -Encoding utf8 "$ChatDir\workspace-chat.jsonl"
Pass "activity written (chat + open-session finding)"

# --- 5. wait for a REAL distillation -----------------------------------------------------
Step "5/6 poll for proposed scribe nodes (model load + generation can take a while)"
$proposed = $null
for ($i = 0; $i -lt 60; $i++) {
    Start-Sleep -Seconds 4
    $g = (& $Bin memory list --config $Conf --state $State --json --status proposed) | ConvertFrom-Json
    $scribeNodes = @($g.nodes | Where-Object { $_.provenance.source -eq "scribe" })
    if ($scribeNodes.Count -ge 1) { $proposed = $scribeNodes; break }
}
if (-not $proposed) { Fail "no scribe proposals after 240s" }
Write-Host "    the scribe proposed:"
$proposed | ForEach-Object { Write-Host "      [$($_.type)] $($_.title)" -ForegroundColor DarkCyan }
# Human gate: nothing scribe-authored may be active.
$all = (& $Bin memory list --config $Conf --state $State --json) | ConvertFrom-Json
$autoActive = $all.nodes | Where-Object { $_.provenance.source -eq "scribe" -and $_.status -eq "active" }
if ($autoActive) { Fail "HUMAN GATE BROKEN: scribe node auto-actived: $($autoActive[0].title)" }
Pass "$($proposed.Count) proposal(s) landed, ALL gated at status=proposed"

# --- 6. explicit promote flips exactly one -----------------------------------------------
Step "6/6 explicit promote -> active (the only path)"
$p = $proposed[0]
$null = & $Bin memory add --config $Conf --state $State --json `
    --id $p.id --type $p.type --title $p.title --status active
if ($LASTEXITCODE -ne 0) { Fail "promote (node update) failed" }
$after = (& $Bin memory list --config $Conf --state $State --json) | ConvertFrom-Json
$promoted = $after.nodes | Where-Object { $_.id -eq $p.id }
if ($promoted.status -ne "active") { Fail "promote did not stick" }
if ($promoted.provenance.source -ne "scribe") { Fail "provenance must survive promotion" }
Pass "explicit promote worked; provenance intact (source=scribe)"

# --- cleanup -------------------------------------------------------------------------------
Stop-Process -Id $d.Id -Force -EA SilentlyContinue
Get-Process -Name "minti-cland-m6smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
foreach ($v in "MINTI_CLAND_FORCE_SCORE","MINTI_CLAND_SCRIBE_MODEL","MINTI_CLAND_SCRIBE_INTERVAL","MINTI_CLAND_SCRIBE_CHATDIR") { Remove-Item "env:$v" -EA SilentlyContinue }
Remove-Item -Force -EA SilentlyContinue $Bin
Write-Host ""
Write-Host "ALL MEMORY M6 SMOKE TESTS PASSED (real-LLM distillation)" -ForegroundColor Green
