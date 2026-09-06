<#
.SYNOPSIS
  Uninstall the Minti-Cland Windows Service installed by install-cland.ps1.

.DESCRIPTION
  Stops the service, removes the NSSM registration, deletes the install
  directory + firewall rule. **State directory is preserved by default**
  (it holds the clan_key + identity.json — deleting it silently would
  drop this host out of any Clan it has joined).

  Pass -Purge to also remove the state directory + any log files. The
  member_id is gone after that and the host will have to re-join from
  scratch.

.PARAMETER ServiceName
  The service name set at install time. Default "Minti-Cland".

.PARAMETER InstallRoot
  Binary install root to remove. Default %PROGRAMFILES%\MINTI\cland.

.PARAMETER StateRoot
  State directory. Preserved by default; removed with -Purge.
  Default %PROGRAMDATA%\MINTI\cland.

.PARAMETER LogRoot
  Log directory (defaults to $StateRoot\logs); removed with -Purge.

.PARAMETER Purge
  Also delete state + logs. Member identity is lost; re-install will
  generate a fresh member_id and the host will have to re-join its Clan.
#>
[CmdletBinding()]
param(
    [string]$ServiceName = 'Minti-Cland',
    [string]$InstallRoot = (Join-Path $env:ProgramFiles 'MINTI\cland'),
    [string]$StateRoot   = (Join-Path $env:ProgramData 'MINTI\cland'),
    [string]$LogRoot     = '',
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'
if (-not $LogRoot) { $LogRoot = Join-Path $StateRoot 'logs' }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Step { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok   { param([string]$m) Write-Host "  ok: $m" -ForegroundColor Green }
function Write-Warn2{ param([string]$m) Write-Host "  warn: $m" -ForegroundColor Yellow }
function Write-Err  { param([string]$m) Write-Host "  ERROR: $m" -ForegroundColor Red }

if (-not (Test-Admin)) {
    Write-Err 'uninstall-cland.ps1 requires Administrator.'
    exit 2
}

Write-Step "MINTI cland uninstaller (purge=$($Purge.IsPresent))"

# ---------- 1. stop + remove service via NSSM ----------

$nssm = Join-Path $InstallRoot 'nssm.exe'
$svc  = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue

if ($svc) {
    if ($svc.Status -ne 'Stopped') {
        Write-Step "Stopping service '$ServiceName'"
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        # Give NSSM time to release file handles before we delete the exe.
        Start-Sleep -Seconds 1
        Write-Ok "service stopped"
    }
    if (Test-Path $nssm) {
        Write-Step 'Removing NSSM service registration'
        & $nssm remove $ServiceName confirm | Out-Null
        Write-Ok "service removed via nssm"
    } else {
        # NSSM binary gone (manually wiped); fall back to sc.exe.
        Write-Step 'Removing service via sc.exe (NSSM binary missing)'
        & sc.exe delete $ServiceName | Out-Null
        Write-Ok "service removed via sc"
    }
} else {
    Write-Warn2 "service '$ServiceName' is not registered; skipping"
}

# ---------- 2. firewall rule ----------

Write-Step 'Removing inbound firewall rule'
$rule = Get-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)' -ErrorAction SilentlyContinue
if ($rule) {
    Remove-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)' -ErrorAction SilentlyContinue
    Write-Ok "firewall rule removed"
} else {
    Write-Warn2 "firewall rule absent; skipping"
}

# ---------- 3. binary install dir ----------

if (Test-Path $InstallRoot) {
    Write-Step "Removing install dir $InstallRoot"
    Remove-Item -Recurse -Force $InstallRoot -ErrorAction Stop
    Write-Ok "$InstallRoot removed"
} else {
    Write-Warn2 "$InstallRoot already absent"
}

# ---------- 4. state dir — preserved by default ----------

if ($Purge) {
    if (Test-Path $StateRoot) {
        Write-Step "PURGE: removing state dir $StateRoot"
        Remove-Item -Recurse -Force $StateRoot -ErrorAction Stop
        Write-Ok "$StateRoot removed (member_id + clan_key gone)"
    }
    $sysRubric = Join-Path $env:ProgramData 'MINTI\reasoning-scores.yaml'
    if (Test-Path $sysRubric) {
        Remove-Item -Force $sysRubric
        Write-Ok "system rubric mirror removed"
    }
} else {
    if (Test-Path $StateRoot) {
        Write-Host ''
        Write-Host "State preserved at: $StateRoot"
        Write-Host '  identity.json + clan.json kept. Re-installing will reuse the same member_id.'
        Write-Host '  Pass -Purge to remove (member_id + clan_key destroyed).'
    }
}

Write-Step 'Uninstall complete'
