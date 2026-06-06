# build-iso.ps1 — Windows wrapper: rsync repo to VM-A, build ISO there, copy back.
# Usage: .\scripts\build-iso.ps1 [-VmAIp 192.168.56.101] [-SshKey ~\.ssh\minti_vm] [-SkipPng] [-SkipFont]
param(
    [string]$VmAIp   = '192.168.56.101',
    [string]$SshKey  = "$env:USERPROFILE\.ssh\minti_vm",
    [string]$SshUser = 'minti',
    [switch]$SkipPng,
    [switch]$SkipFont
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Log  { Write-Host "[build-iso] $args" -ForegroundColor Green }
function Warn { Write-Host "[build-iso WARN] $args" -ForegroundColor Yellow }
function Die  { Write-Host "[build-iso ERROR] $args" -ForegroundColor Red; exit 1 }

$RepoRoot = git rev-parse --show-toplevel
if ($LASTEXITCODE -ne 0) { Die "Not inside a git repository." }
$RepoRoot = $RepoRoot.Trim()

$SshTarget = "${SshUser}@${VmAIp}"
$SshArgs   = @('-o', 'StrictHostKeyChecking=accept-new', '-i', $SshKey)
$RemoteDir = "/home/${SshUser}/minti-build"

# ── pre-flight ────────────────────────────────────────────────────────────────
Log "Checking SSH connectivity to $SshTarget..."
$ping = ssh @SshArgs "$SshTarget" 'echo ok' 2>&1
if ($ping -ne 'ok') { Die "Cannot reach $SshTarget. Check VPN/host-only network and SSH key at $SshKey." }

# Check live-build installed on VM-A
$lb = ssh @SshArgs "$SshTarget" 'command -v lb 2>/dev/null || echo missing'
if ($lb -match 'missing') {
    Log "Installing live-build on $SshTarget (requires sudo)..."
    ssh @SshArgs "$SshTarget" 'sudo apt-get install -y live-build' | Out-Host
}

# ── rsync repo to VM-A ────────────────────────────────────────────────────────
Log "Rsyncing repo to ${SshTarget}:${RemoteDir}..."
$rsyncArgs = @(
    '-avz', '--delete',
    '--exclude=.git',
    '--exclude=build/iso/minti-bookworm-amd64.hybrid.iso',
    '--exclude=*.exe',
    '-e', "ssh $($SshArgs -join ' ')",
    "${RepoRoot}/",
    "${SshTarget}:${RemoteDir}/"
)
& rsync @rsyncArgs
if ($LASTEXITCODE -ne 0) { Die "rsync failed." }

# ── run build on VM-A ─────────────────────────────────────────────────────────
$buildFlags = ""
if ($SkipPng)  { $buildFlags += " --skip-png" }
if ($SkipFont) { $buildFlags += " --skip-font" }

Log "Running build-iso.sh on $SshTarget (sudo required)..."
ssh @SshArgs "$SshTarget" "cd $RemoteDir && sudo bash scripts/build-iso.sh$buildFlags"
if ($LASTEXITCODE -ne 0) { Die "build-iso.sh failed on VM-A. SSH in and check $RemoteDir/build/iso/build.log" }

# ── copy ISO back ─────────────────────────────────────────────────────────────
$LocalIsoDir = Join-Path $RepoRoot 'build\iso'
New-Item -ItemType Directory -Force $LocalIsoDir | Out-Null

Log "Copying ISO back from VM-A..."
$remoteIso = "${SshTarget}:${RemoteDir}/build/iso/minti-bookworm-amd64.hybrid.iso"
& scp @SshArgs "$remoteIso" "$LocalIsoDir\"
if ($LASTEXITCODE -ne 0) { Die "scp failed — ISO may not have been created." }

$isoPath = Join-Path $LocalIsoDir 'minti-bookworm-amd64.hybrid.iso'
$size = (Get-Item $isoPath).Length / 1MB
Log ("Done. ISO: $isoPath ({0:N0} MB)" -f $size)
Log "Flash to USB on Linux: dd if=$isoPath of=/dev/sdX bs=4M status=progress oflag=sync"
Log "Flash to USB on Windows: dd.exe (via WSL) or Rufus/balenaEtcher"
