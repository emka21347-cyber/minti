<#
.SYNOPSIS
  Uninstall the MINTI stack services installed by install-minti.ps1.

.DESCRIPTION
  Dist D1 uninstaller. Stops + removes the three services (each skipped
  gracefully when absent — also works on a box that only ever had the
  M5-B cland-only install), removes the firewall rule, deletes the
  binaries under %PROGRAMFILES%\MINTI.

  **State is preserved by default** (%PROGRAMDATA%\MINTI holds the
  clan_key + identity.json — deleting it silently would drop this host
  out of any Clan it has joined). Pass -Purge to delete it after the
  printed warning; the member identity is gone and the host re-joins
  from scratch.

  Third-party software is never touched: Ollama uninstalls from Windows
  "Add or remove programs" if you want it gone.

.PARAMETER InstallRoot
  Binaries root to remove. Default %PROGRAMFILES%\MINTI.

.PARAMETER StateRoot
  State root. Preserved by default; removed with -Purge.
  Default %PROGRAMDATA%\MINTI.

.PARAMETER Purge
  Also delete state + logs. Member identity is lost; re-install
  generates a fresh member_id and the host must re-join its Clan.
#>
[CmdletBinding()]
param(
    [string]$InstallRoot = (Join-Path $env:ProgramFiles 'MINTI'),
    [string]$StateRoot   = (Join-Path $env:ProgramData 'MINTI'),
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Step  { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok    { param([string]$m) Write-Host "  ok: $m" -ForegroundColor Green }
function Write-Warn2 { param([string]$m) Write-Host "  warn: $m" -ForegroundColor Yellow }
function Write-Err   { param([string]$m) Write-Host "  ERROR: $m" -ForegroundColor Red }

if (-not (Test-Admin)) {
    Write-Err 'uninstall-minti.ps1 requires Administrator.'
    exit 2
}

Write-Step "MINTI stack uninstaller (purge=$($Purge.IsPresent))"

# ---------- 1. stop + remove services (reverse dependency order) ----------

$services = @(
    @{ Name = 'Minti-Workspace'; Dir = (Join-Path $InstallRoot 'workspace') },
    @{ Name = 'Minti-Runtime';   Dir = (Join-Path $InstallRoot 'runtime') },
    @{ Name = 'Minti-Cland';     Dir = (Join-Path $InstallRoot 'cland') }
)

foreach ($entry in $services) {
    $name = $entry.Name
    $svc = Get-Service -Name $name -ErrorAction SilentlyContinue
    if (-not $svc) {
        Write-Warn2 "service '$name' is not registered; skipping"
        continue
    }
    if ($svc.Status -ne 'Stopped') {
        Write-Step "Stopping service '$name'"
        Stop-Service -Name $name -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 1
        Write-Ok 'stopped'
    }
    $nssm = Join-Path $entry.Dir 'nssm.exe'
    if (Test-Path $nssm) {
        Write-Step "Removing '$name' via NSSM"
        & $nssm remove $name confirm | Out-Null
        Write-Ok 'service removed via nssm'
    } else {
        # NSSM binary gone (manually wiped); fall back to sc.exe. Also the
        # path for an M5-B-era cland whose nssm lives in the same dir —
        # Test-Path above finds it; this branch is for true wipes only.
        Write-Step "Removing '$name' via sc.exe (NSSM binary missing)"
        & sc.exe delete $name | Out-Null
        Write-Ok 'service removed via sc'
    }
}

# ---------- 2. firewall rules ----------
# By DisplayName + a sweep of inbound rules bound to our exes (D0 review
# F8: catches a rule an admin renamed).

Write-Step 'Removing inbound firewall rules'
$rule = Get-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)' -ErrorAction SilentlyContinue
if ($rule) {
    Remove-NetFirewallRule -DisplayName 'MINTI cland (Clan TLS)' -ErrorAction SilentlyContinue
    Write-Ok "removed rule 'MINTI cland (Clan TLS)'"
}
$ourExes = @()
foreach ($sub in @('cland\minti-cland.exe', 'runtime\minti-runtime.exe', 'workspace\minti-workspace.exe')) {
    $ourExes += (Join-Path $InstallRoot $sub).ToLower()
}
$swept = 0
Get-NetFirewallApplicationFilter -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.Program -and ($ourExes -contains $_.Program.ToLower())) {
        $parent = $_ | Get-NetFirewallRule -ErrorAction SilentlyContinue
        if ($parent) {
            $parent | Remove-NetFirewallRule -ErrorAction SilentlyContinue
            $swept++
        }
    }
}
if ($swept -gt 0) { Write-Ok "swept $swept additional rule(s) bound to MINTI binaries" }
if (-not $rule -and $swept -eq 0) { Write-Warn2 'no MINTI firewall rules present; skipping' }

# ---------- 3. binaries ----------

if (Test-Path $InstallRoot) {
    Write-Step "Removing install dir $InstallRoot"
    Remove-Item -Recurse -Force $InstallRoot -ErrorAction Stop
    Write-Ok "$InstallRoot removed"
} else {
    Write-Warn2 "$InstallRoot already absent"
}

# ---------- 4. state — preserved by default ----------

if ($Purge) {
    if (Test-Path $StateRoot) {
        Write-Step "PURGE: removing state root $StateRoot"
        Write-Warn2 'this deletes the member identity + clan_key — the host'
        Write-Warn2 'leaves its Clan and a re-install must re-join from scratch.'
        Remove-Item -Recurse -Force $StateRoot -ErrorAction Stop
        Write-Ok "$StateRoot removed (member identity + clan_key gone)"
    } else {
        Write-Warn2 "$StateRoot already absent"
    }
} else {
    if (Test-Path $StateRoot) {
        Write-Host ''
        Write-Host "State preserved at: $StateRoot"
        Write-Host '  identity.json + clan.json + configs kept. Re-installing reuses'
        Write-Host '  the same member_id and Clan membership.'
        Write-Host '  Pass -Purge to remove (member identity + clan_key destroyed).'
    }
}

Write-Host ''
Write-Host 'Not touched (third-party): Ollama — remove via Settings > Apps if desired.'
Write-Step 'Uninstall complete'
