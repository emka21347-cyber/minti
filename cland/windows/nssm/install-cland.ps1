<#
.SYNOPSIS
  Install minti-cland as a Windows Service via NSSM.

.DESCRIPTION
  M5-B installer. Lays out binaries + state under %PROGRAMFILES%\MINTI\cland
  and %PROGRAMDATA%\MINTI\cland with a restrictive NTFS DACL (the
  load-bearing security step that replaces in-process ACL hardening — see
  the M5 plan, deviation #3, and cland/internal/identity/identity.go).
  Registers the cland binary as the "Minti-Cland" service via NSSM 2.24
  (64-bit; the 32-bit variant has documented env-var truncation bugs when
  managing 64-bit child processes — M5 peer-review item 2).

  Idempotent: re-running upgrades binaries + refreshes the NSSM service
  config but preserves the state directory (clan_key, identity.json,
  audit.jsonl).

.PARAMETER InstallRoot
  Where to lay out minti-cland.exe + nssm.exe. Default %PROGRAMFILES%\MINTI\cland.

.PARAMETER StateRoot
  Where cland stores clan.json + identity.json + audit.jsonl. Default
  %PROGRAMDATA%\MINTI\cland. Receives the restrictive DACL.

.PARAMETER LogRoot
  Where NSSM redirects stdout + stderr. Default $StateRoot\logs.

.PARAMETER Port
  Inbound TCP port for the cland HMAC-on-TLS listener. Default 7777.

.PARAMETER ServiceName
  Windows service name. Default "Minti-Cland".

.PARAMETER FirewallProfile
  Comma-separated list of network profiles the inbound rule applies to.
  Default 'Private,Domain'. Windows 11 categorises unrecognised networks
  as Public by default, so a Private-only rule leaves cland invisible on
  first connect-to-new-network — peer-review item 1 (qwen + gemma).
  Operators who actually want cland reachable on a coffee-shop / public
  network must pass `-FirewallProfile Private,Domain,Public`.

.PARAMETER ZipRoot
  Where this script's own siblings live (bin\, configs\, nssm.sha256). Defaults
  to the script's parent directory — works for `Expand-Archive` + cd-in install.

.NOTES
  Requires Administrator. Re-launches itself if not elevated? No — refuses
  + prints remediation. The reason is that auto-relaunching a self-signed
  script through `Start-Process -Verb RunAs` confuses Windows SmartScreen
  and ends up in a worse UX than the operator fixing the elevation
  themselves.
#>
[CmdletBinding()]
param(
    [string]$InstallRoot      = (Join-Path $env:ProgramFiles 'MINTI\cland'),
    [string]$StateRoot        = (Join-Path $env:ProgramData 'MINTI\cland'),
    [string]$LogRoot          = '',
    [int]$Port                = 7777,
    [string]$ServiceName      = 'Minti-Cland',
    [string]$FirewallProfile  = 'Private,Domain',
    [string]$ZipRoot          = (Split-Path -Parent $MyInvocation.MyCommand.Path)
)

$ErrorActionPreference = 'Stop'

if (-not $LogRoot) { $LogRoot = Join-Path $StateRoot 'logs' }

# ---------- helpers ----------

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Step  { param([string]$msg) Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok    { param([string]$msg) Write-Host "  ok: $msg" -ForegroundColor Green }
function Write-Warn2 { param([string]$msg) Write-Host "  warn: $msg" -ForegroundColor Yellow }
function Write-Err   { param([string]$msg) Write-Host "  ERROR: $msg" -ForegroundColor Red }

# ---------- preflight ----------

if (-not (Test-Admin)) {
    Write-Err 'install-cland.ps1 requires Administrator.'
    Write-Host ''
    Write-Host 'Re-launch from an elevated PowerShell prompt:'
    Write-Host '  Start-Process powershell -Verb RunAs -ArgumentList "-ExecutionPolicy Bypass -File install-cland.ps1"'
    exit 2
}

Write-Step "MINTI cland installer (M5-B)"
Write-Host "  InstallRoot      = $InstallRoot"
Write-Host "  StateRoot        = $StateRoot"
Write-Host "  LogRoot          = $LogRoot"
Write-Host "  Port             = $Port"
Write-Host "  ServiceName      = $ServiceName"
Write-Host "  FirewallProfile  = $FirewallProfile"

$bundledExe   = Join-Path $ZipRoot 'bin\minti-cland.exe'
$bundledNssm  = Join-Path $ZipRoot 'bin\nssm.exe'
$nssmSha      = Join-Path $ZipRoot 'bin\nssm.sha256'
$bundledYaml  = Join-Path $ZipRoot 'configs\cland.yaml.windows.example'
$bundledRubric= Join-Path $ZipRoot 'configs\reasoning-scores.yaml.example'

foreach ($p in @($bundledExe, $bundledNssm, $nssmSha, $bundledYaml)) {
    if (-not (Test-Path $p)) {
        Write-Err "missing required bundled file: $p"
        Write-Host '  Re-extract the zip; the bundle is incomplete.'
        exit 3
    }
}

# ---------- 1. verify bundled NSSM against pinned hash ----------

Write-Step 'Verifying bundled NSSM SHA-256'
$expectedSha = (Get-Content -Raw -Path $nssmSha).Trim().ToLower()
$actualSha   = (Get-FileHash -Algorithm SHA256 -Path $bundledNssm).Hash.ToLower()
if ($expectedSha -ne $actualSha) {
    Write-Err "NSSM SHA-256 mismatch:"
    Write-Host "  expected: $expectedSha"
    Write-Host "  actual:   $actualSha"
    Write-Host '  The zip you extracted may be tampered with. Aborting.'
    exit 4
}
Write-Ok "NSSM hash = $actualSha"

# ---------- 2. create install + state dirs with restrictive DACL ----------

Write-Step 'Creating directories'
New-Item -ItemType Directory -Path $InstallRoot -Force | Out-Null
New-Item -ItemType Directory -Path $StateRoot   -Force | Out-Null
New-Item -ItemType Directory -Path $LogRoot     -Force | Out-Null
Write-Ok "$InstallRoot"
Write-Ok "$StateRoot"
Write-Ok "$LogRoot"

# Strict DACL on StateRoot. Inheritance disabled. Only SYSTEM + Administrators.
# Identity.json + clan_key live here; this is the load-bearing security
# step per the M5 plan deviation #3.
Write-Step 'Applying restrictive DACL to state dir'
$icaclsCmd = "icacls `"$StateRoot`" /inheritance:r /grant:r `"SYSTEM:(OI)(CI)F`" `"BUILTIN\Administrators:(OI)(CI)F`""
& cmd /c $icaclsCmd | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Err "icacls failed with exit code $LASTEXITCODE on $StateRoot"
    exit 5
}
Write-Ok "DACL set (SYSTEM + Administrators only; inheritance disabled)"

# ---------- 3. stage binaries ----------

Write-Step 'Staging binaries'
$installedExe  = Join-Path $InstallRoot 'minti-cland.exe'
$installedNssm = Join-Path $InstallRoot 'nssm.exe'

# Stop the service first if it exists — we can't overwrite a running .exe.
$existingSvc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existingSvc -and $existingSvc.Status -ne 'Stopped') {
    Write-Host "  stopping existing service to allow binary swap"
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    # Give NSSM a moment to release the file handles before we copy.
    Start-Sleep -Seconds 1
}

Copy-Item -Path $bundledExe  -Destination $installedExe  -Force
Copy-Item -Path $bundledNssm -Destination $installedNssm -Force
Write-Ok "minti-cland.exe -> $installedExe"
Write-Ok "nssm.exe        -> $installedNssm"

# ---------- 4. stage configs (preserve existing on re-install) ----------

Write-Step 'Staging configs'
$installedYaml = Join-Path $StateRoot 'cland.yaml'
if (Test-Path $installedYaml) {
    Write-Ok "cland.yaml      preserved ($installedYaml)"
} else {
    Copy-Item -Path $bundledYaml -Destination $installedYaml -Force
    Write-Ok "cland.yaml      -> $installedYaml"
}

$installedRubric = Join-Path $StateRoot 'reasoning-scores.yaml'
if (Test-Path $bundledRubric) {
    if (Test-Path $installedRubric) {
        Write-Ok "reasoning-scores.yaml preserved"
    } else {
        Copy-Item -Path $bundledRubric -Destination $installedRubric -Force
        Write-Ok "reasoning-scores.yaml -> $installedRubric"
    }
}

# Rubric also has a system-level mirror that cland's
# DefaultRubricPath() looks at under LocalSystem. Mirror the per-state
# copy so either path works.
$systemRubric = Join-Path $env:ProgramData 'MINTI\reasoning-scores.yaml'
$systemRubricDir = Split-Path -Parent $systemRubric
if (-not (Test-Path $systemRubricDir)) {
    New-Item -ItemType Directory -Path $systemRubricDir -Force | Out-Null
}
if ((Test-Path $installedRubric) -and (-not (Test-Path $systemRubric))) {
    Copy-Item -Path $installedRubric -Destination $systemRubric -Force
    Write-Ok "system rubric mirror -> $systemRubric"
}

# ---------- 5. register / update NSSM service ----------

Write-Step "Registering Windows Service '$ServiceName' via NSSM"

if ($existingSvc) {
    Write-Host "  service exists; refreshing NSSM config"
    & $installedNssm set $ServiceName Application "$installedExe" | Out-Null
} else {
    & $installedNssm install $ServiceName "$installedExe" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Err "nssm install failed (exit $LASTEXITCODE)"
        exit 6
    }
}

# Service config. Build the AppParameters string with single-quoted YAML
# path so an embedded space (none expected, but defensive) doesn't break
# argv splitting.
$cfgArg = "--config `"$installedYaml`""
& $installedNssm set $ServiceName AppParameters $cfgArg     | Out-Null
& $installedNssm set $ServiceName AppDirectory  $StateRoot  | Out-Null
& $installedNssm set $ServiceName AppStdout     (Join-Path $LogRoot 'cland.out.log') | Out-Null
& $installedNssm set $ServiceName AppStderr     (Join-Path $LogRoot 'cland.err.log') | Out-Null
& $installedNssm set $ServiceName AppRotateFiles  1        | Out-Null
& $installedNssm set $ServiceName AppRotateBytes  10485760 | Out-Null   # 10 MiB
& $installedNssm set $ServiceName AppRotateOnline 1        | Out-Null
& $installedNssm set $ServiceName Start           SERVICE_AUTO_START | Out-Null
& $installedNssm set $ServiceName ObjectName      LocalSystem        | Out-Null

# Stop-method timings — peer-review item 7. NSSM waits 5.5 s for graceful
# exit; cland's Go-side srv.Shutdown is 3 s; SCM "Not Responding" budget
# is ~10 s. This stack stays comfortably under SCM's threshold.
& $installedNssm set $ServiceName AppStopMethodConsole 5500 | Out-Null
& $installedNssm set $ServiceName AppStopMethodWindow  5500 | Out-Null

& $installedNssm set $ServiceName Description     "MINTI Clan daemon (Orchestrator election + peer routing + MCP tool exec)" | Out-Null

Write-Ok "service configured"

# ---------- 6. firewall rule ----------

Write-Step "Configuring inbound firewall rule (Profile=$FirewallProfile)"
$ruleName = "MINTI cland (Clan TLS)"
$existingRule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
$profiles = $FirewallProfile -split ',' | ForEach-Object { $_.Trim() }
if ($existingRule) {
    # Set-NetFirewallRule can't change Program after creation cleanly; the
    # safe pattern is remove + re-create. Defensive against rename of the
    # install dir between re-installs.
    Remove-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
}
New-NetFirewallRule -DisplayName $ruleName `
    -Direction Inbound -Action Allow -Protocol TCP -LocalPort $Port `
    -Profile $profiles -Program $installedExe | Out-Null
Write-Ok "firewall rule installed (profiles: $($profiles -join ', '))"

if ($FirewallProfile -notmatch 'Public') {
    Write-Warn2 "rule excludes Public -- if your network reads as Public,"
    Write-Warn2 "either set it to Private in Settings > Network, or re-run"
    Write-Warn2 "  install-cland.ps1 -FirewallProfile Private,Domain,Public"
}

# ---------- 7. start the service ----------

Write-Step "Starting service"
Start-Service -Name $ServiceName -ErrorAction SilentlyContinue
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq 'Running') {
    Write-Ok "service is Running"
} else {
    Write-Err "service did not enter Running state. Check $LogRoot\cland.err.log."
    exit 7
}

# ---------- 8. summary + Defender guidance ----------

Write-Step 'Install complete'
Write-Host ''
Write-Host "Service:        $ServiceName ($($svc.Status))"
Write-Host "Binary:         $installedExe"
Write-Host "State dir:      $StateRoot"
Write-Host "Logs:           $LogRoot\cland.{out,err}.log"
Write-Host "Config:         $installedYaml"
Write-Host ''
Write-Host "Verify:"
Write-Host "  Get-Service $ServiceName"
Write-Host "  Test-NetConnection -ComputerName 127.0.0.1 -Port $Port"
Write-Host "  & '$installedExe' show"
Write-Host ''
Write-Host "Join an existing Clan:"
Write-Host "  & '$installedExe' join --mnemonic '<12 words>' --address <ip>:7777 --pin sha256:<hex>"
Write-Host ''
Write-Warn2 'DEFENDER GUIDANCE (M5 peer-review item 8 -- gemma)'
Write-Host '  Unsigned LocalSystem services doing multicast (mDNS, UDP 5353) can'
Write-Host '  trigger Windows Defender behavioural blocks within ~5 minutes of'
Write-Host '  service start, silently quarantining minti-cland.exe mid-session.'
Write-Host '  Until cland ships code-signed (planned for M6), add Folder Exclusions:'
Write-Host ''
Write-Host '    Settings > Privacy & security > Windows Security > Virus & threat protection'
Write-Host '      Manage settings > Exclusions > Add or remove exclusions > Folder'
Write-Host "      Add: $InstallRoot"
Write-Host "      Add: $StateRoot"
Write-Host ''
Write-Host '  Skip this step on managed corporate machines where Group Policy may'
Write-Host '  override Defender; ask IT to allowlist instead.'
