# MINTI M8 Phase 1 knock-flow end-to-end smoke (Windows, 127.0.0.1).
#
# Validates §3.4 knock-join path:
#   Founder A creates Clan -> Daemon A starts (receiver)
#   Joiner B knocks (no shared secret) -> derives SAS -> confirms y
#   Operator uses A's CLI: knocks (sees SAS) -> knock-accept
#   Receiver seals + delivers welcome -> Joiner B persists Clan state
#   Verify B shows correct Clan ID and A's roster has B
#
# Run (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts\m8-phase1-smoke.ps1

$ErrorActionPreference = "Stop"

$Repo   = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
$Bin    = "$Repo\cland\minti-cland-smoke.exe"
$StateA = "$env:TEMP\cland-knock-A"
$StateB = "$env:TEMP\cland-knock-B"
$ConfA  = "$StateA\config.yaml"
$ConfB  = "$StateB\config.yaml"
$LogA   = "$StateA\daemon.log"
$KnockOut = "$env:TEMP\knock-out.txt"
$KnockErr = "$env:TEMP\knock-err.txt"
$StdinY   = "$env:TEMP\knock-stdin.txt"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "    FAIL: $msg" -ForegroundColor Red
    if ($script:KnockProc -and -not $script:KnockProc.HasExited) {
        $script:KnockProc.Kill()
    }
    Get-Process -Name "minti-cland-smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
    Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
    if (Test-Path $KnockOut) { Write-Host "--- knock stdout ---"; Get-Content $KnockOut }
    if (Test-Path $KnockErr) { Write-Host "--- knock stderr ---"; Get-Content $KnockErr }
    if (Test-Path $LogA)     { Write-Host "--- daemon A log (tail 20) ---"; Get-Content $LogA -Tail 20 }
    exit 1
}

# ─── 1. Build ─────────────────────────────────────────────────────────────────
Step "1/7 build minti-cland"
Push-Location "$Repo\cland"
& "C:\Program Files\Go\bin\go.exe" build -o $Bin ".\cmd\minti-cland"
Pop-Location
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
Pass "built $Bin"

# ─── 2. Clean slate ────────────────────────────────────────────────────────────
Step "2/7 clean state dirs + write configs"
Get-Process -Name "minti-cland-smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
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
($confTemplate -creplace '__PORT__','17990' -creplace '__STATEDIR__',($StateA -replace '\\','/')) | Out-File -Encoding utf8 $ConfA
($confTemplate -creplace '__PORT__','17991' -creplace '__STATEDIR__',($StateB -replace '\\','/')) | Out-File -Encoding utf8 $ConfB
Pass "configs written"

$env:MINTI_CLAND_FORCE_HEALTHY = "1"

# ─── 3. Founder A creates Clan ────────────────────────────────────────────────
Step "3/7 founder A creates Clan"
$createOut = & $Bin create --config $ConfA --state $StateA --address "127.0.0.1:17990" --json
if ($LASTEXITCODE -ne 0) { Fail "create failed" }
$create = $createOut | ConvertFrom-Json
$ClanID   = $create.clan_id
$Pin      = $create.pin
Write-Host "    clan_id = $ClanID"
Write-Host "    pin     = $Pin"
Pass "Clan created"

# ─── 4. Start daemon A (receiver) ────────────────────────────────────────────
Step "4/7 start daemon A on :17990"
$daemA = Start-Process -FilePath $Bin `
    -ArgumentList @("--config", $ConfA, "--state", $StateA) `
    -WindowStyle Hidden -PassThru `
    -RedirectStandardError $LogA -RedirectStandardOutput "$StateA\out.log"
Start-Sleep -Seconds 2
if (-not (Get-Process -Id $daemA.Id -EA SilentlyContinue)) { Fail "daemon A died; see $LogA" }
Pass "daemon A PID=$($daemA.Id)"

# ─── 5. Joiner B knocks ───────────────────────────────────────────────────────
# Write "y" to a file so the interactive SAS-confirmation prompt is answered
# automatically. The knock CLI goroutine reads stdin; it'll get "y" immediately
# after the prompt appears (F3: sasConfirmed is only set after y is consumed).
Step "5/7 B knocks A (auto-answer SAS confirmation with 'y')"
"y" | Out-File -Encoding ascii -NoNewline $StdinY

$script:KnockProc = Start-Process -FilePath $Bin `
    -ArgumentList @(
        "knock",
        "--config", $ConfB, "--state", $StateB,
        "--clan-id", $ClanID,
        "--address", "127.0.0.1:17990",
        "--pin", $Pin,
        "--listen", "127.0.0.1:0"
    ) `
    -WindowStyle Hidden -PassThru `
    -RedirectStandardInput  $StdinY `
    -RedirectStandardOutput $KnockOut `
    -RedirectStandardError  $KnockErr

Write-Host "    knock PID=$($script:KnockProc.Id)"

# Give the knock CLI time to: send POST /clan/knock, get KnockResponse,
# read "y" from stdin (sets sasConfirmed=true), arm the deliver handler.
Write-Host "    waiting 4s for knock to arm deliver handler..."
Start-Sleep -Seconds 4

if ($script:KnockProc.HasExited) {
    Fail "knock process exited prematurely (exit=$($script:KnockProc.ExitCode))"
}
Pass "knock process still running (SAS confirmed, deliver handler armed)"

# ─── 6. Operator: list pending knocks, then accept ───────────────────────────
Step "6/7 operator: knocks --json -> knock-accept"
$knocksRaw = & $Bin knocks --config $ConfA --state $StateA --json
if ($LASTEXITCODE -ne 0) { Fail "knocks subcommand failed" }
$knocksJson = $knocksRaw | ConvertFrom-Json
$pending = $knocksJson.knocks
Write-Host "    pending knocks: $($pending.Count)"
if ($pending.Count -eq 0) { Fail "no pending knocks found on daemon A" }

$knock    = $pending[0]
$KnockID  = $knock.knock_id
$SAS      = $knock.sas
$JoinerID = $knock.joiner_member_id
Write-Host "    knock_id = $KnockID"
Write-Host "    sas      = $SAS"
Write-Host "    joiner   = $JoinerID"
Pass "pending knock found"

# Operator accepts (simulates: operator reads SAS, verifies with joiner, presses accept)
& $Bin knock-accept --config $ConfA --state $StateA $KnockID
if ($LASTEXITCODE -ne 0) { Fail "knock-accept failed" }
Pass "knock-accept sent"

# ─── 7. Wait for joiner B to receive delivery + verify ───────────────────────
Step "7/7 wait for joiner B to complete + verify state"
$deadline = (Get-Date).AddSeconds(20)
while (-not $script:KnockProc.HasExited -and (Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
}
if (-not $script:KnockProc.HasExited) {
    Fail "knock process did not exit within 20s after accept"
}
# WaitForExit() populates ExitCode on Start-Process -PassThru objects.
$null = $script:KnockProc.WaitForExit(1000)
$ec = $script:KnockProc.ExitCode
if ($null -ne $ec -and $ec -ne 0) {
    Fail "knock process exited with code $ec"
}

Write-Host "    knock stdout:"
Get-Content $KnockOut | ForEach-Object { Write-Host "      $_" }

# Verify B's on-disk state has the Clan
$membersOut = & $Bin members --config $ConfB --state $StateB
if ($LASTEXITCODE -ne 0) { Fail "members read from B's state failed" }
Write-Host "    B's roster:"
$membersOut | ForEach-Object { Write-Host "      $_" }
if (-not ($membersOut -match $ClanID)) { Fail "B's state doesn't show expected clan_id $ClanID" }
Pass "B shows clan_id $ClanID"

# Verify A's roster includes B
$membersA = & $Bin members --config $ConfA --state $StateA
if (-not ($membersA -match $JoinerID)) { Fail "A's roster doesn't include joiner $JoinerID" }
Pass "A's roster includes joiner $JoinerID"

# ─── Cleanup ─────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "M8 PHASE 1 KNOCK SMOKE: PASS" -ForegroundColor Green
Get-Process -Name "minti-cland-smoke" -EA SilentlyContinue | Stop-Process -Force -EA SilentlyContinue
Remove-Item env:MINTI_CLAND_FORCE_HEALTHY -EA SilentlyContinue
