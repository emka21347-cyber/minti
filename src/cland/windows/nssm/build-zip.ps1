<#
.SYNOPSIS
  Build the minti-cland Windows distribution zip.

.DESCRIPTION
  Release packager for the NSSM-wrapped Windows service. Produces
  `dist/minti-cland-windows-amd64-v<VERSION>.zip`.

  Steps:
    1. Cross-compile cland for windows-amd64 via the existing
       cland-windows Makefile target shape (but invoked directly here
       because make is not consistently on the Windows PATH).
    2. Download nssm-2.24.zip from https://nssm.cc (cached in
       cland\windows\nssm\.cache\). Extract win64/nssm.exe.
    3. Compute SHA-256 of nssm.exe, write to bin\nssm.sha256 in the
       staging dir. install-cland.ps1 verifies this at install time.
       The release engineer verifies the upstream zip independently
       against a known-good source (see -PrintHashes below) — that is
       the trust boundary; tamper-evidence at install is downstream of
       the build step.
    4. Lay out the staging tree.
    5. Compress-Archive to dist/.

.PARAMETER Version
  String embedded in the zip name + cland's -X main.version. Default
  taken from the Makefile's VERSION (currently 0.2.0-M5).

.PARAMETER NssmUrl
  Override the NSSM download URL. Default https://nssm.cc/release/nssm-2.24.zip.

.PARAMETER PrintHashes
  Print SHA-256 of the downloaded NSSM zip + the extracted nssm.exe and exit.
  Use this to verify against publicly-known NSSM hashes before the first
  release build. (No standardised pinning; the upstream is a single
  download URL last touched in 2014, so the hash is stable.)

.PARAMETER GoExe
  Path to go.exe. Defaults to "go" (PATH lookup).
#>
[CmdletBinding()]
param(
    [string]$Version     = '0.2.0-M5',
    [string]$NssmUrl     = 'https://nssm.cc/release/nssm-2.24.zip',
    [switch]$PrintHashes,
    [string]$GoExe       = 'go'
)

$ErrorActionPreference = 'Stop'

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$clandDir  = (Resolve-Path (Join-Path $scriptDir '..\..')).Path
$repoRoot  = (Resolve-Path (Join-Path $clandDir '..')).Path
$distDir   = Join-Path $repoRoot 'dist'
$cacheDir  = Join-Path $scriptDir '.cache'
$stageRoot = Join-Path $distDir "minti-cland-windows-amd64-v$Version"

function Write-Step { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok   { param([string]$m) Write-Host "  ok: $m" -ForegroundColor Green }
function Write-Warn2{ param([string]$m) Write-Host "  warn: $m" -ForegroundColor Yellow }
function Write-Err  { param([string]$m) Write-Host "  ERROR: $m" -ForegroundColor Red }

# ---------- 1. cross-compile cland for windows-amd64 ----------

if (-not $PrintHashes) {
    Write-Step "Cross-compiling minti-cland for windows-amd64 (version=$Version)"
    $env:GOOS   = 'windows'
    $env:GOARCH = 'amd64'
    $ldflags    = "-X main.version=$Version -s -w"
    $outDir     = Join-Path $clandDir 'dist'
    New-Item -ItemType Directory -Path $outDir -Force | Out-Null
    $outExe     = Join-Path $outDir 'minti-cland-windows-amd64.exe'
    Push-Location $clandDir
    try {
        & $GoExe build -trimpath -ldflags $ldflags -o $outExe ./cmd/minti-cland
        if ($LASTEXITCODE -ne 0) {
            Write-Err "go build failed (exit $LASTEXITCODE)"
            exit 1
        }
    } finally {
        Pop-Location
        Remove-Item env:GOOS, env:GOARCH -ErrorAction SilentlyContinue
    }
    Write-Ok "$outExe"
}

# ---------- 2. fetch + extract NSSM ----------

New-Item -ItemType Directory -Path $cacheDir -Force | Out-Null
$nssmZip = Join-Path $cacheDir 'nssm-2.24.zip'

if (-not (Test-Path $nssmZip)) {
    Write-Step "Downloading NSSM 2.24"
    Write-Host "  $NssmUrl"
    # nssm.cc is famously flaky (random 503s; PS5.1's Invoke-WebRequest
    # sometimes can't reach it cleanly). Prefer curl.exe (native on Win10+)
    # which handles redirects + retries more reliably, fall back to
    # Invoke-WebRequest if curl isn't on PATH.
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & curl.exe -sS -L -o $nssmZip --max-time 60 --retry 3 --retry-delay 5 $NssmUrl
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $nssmZip) -or (Get-Item $nssmZip).Length -lt 100000) {
            Write-Err "curl.exe download of $NssmUrl failed (exit=$LASTEXITCODE)."
            Write-Host '  Try a few minutes later or pass -NssmUrl <mirror>.'
            exit 2
        }
    } else {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -UseBasicParsing -Uri $NssmUrl -OutFile $nssmZip
    }
    Write-Ok "$nssmZip"
} else {
    Write-Ok "NSSM zip cached at $nssmZip"
}

$nssmZipHash = (Get-FileHash -Algorithm SHA256 -Path $nssmZip).Hash.ToLower()
Write-Host "  nssm-2.24.zip sha256 = $nssmZipHash"

# Extract win64/nssm.exe out of the downloaded zip.
$nssmExtractDir = Join-Path $cacheDir 'nssm-2.24'
if (-not (Test-Path $nssmExtractDir)) {
    Expand-Archive -Path $nssmZip -DestinationPath $cacheDir -Force
}
$nssmExe = Join-Path $nssmExtractDir 'win64\nssm.exe'
if (-not (Test-Path $nssmExe)) {
    Write-Err "expected win64\nssm.exe not found in extracted nssm zip"
    exit 2
}
$nssmExeHash = (Get-FileHash -Algorithm SHA256 -Path $nssmExe).Hash.ToLower()
Write-Host "  win64\nssm.exe sha256 = $nssmExeHash"

if ($PrintHashes) {
    Write-Host ''
    Write-Host 'Compare these hashes against the canonical NSSM 2.24 release'
    Write-Host '(https://nssm.cc/usage -- sourceforge mirror also publishes hashes).'
    Write-Host 'Once you have verified, run build-zip.ps1 without -PrintHashes.'
    exit 0
}

# ---------- 3. assemble staging tree ----------

Write-Step "Assembling staging tree at $stageRoot"
if (Test-Path $stageRoot) {
    Remove-Item -Recurse -Force $stageRoot
}
New-Item -ItemType Directory -Path $stageRoot -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $stageRoot 'bin') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $stageRoot 'configs') -Force | Out-Null

# Binaries.
Copy-Item -Path (Join-Path $clandDir 'dist\minti-cland-windows-amd64.exe') `
          -Destination (Join-Path $stageRoot 'bin\minti-cland.exe') -Force
Copy-Item -Path $nssmExe -Destination (Join-Path $stageRoot 'bin\nssm.exe') -Force
Set-Content -Encoding ASCII -Path (Join-Path $stageRoot 'bin\nssm.sha256') -Value $nssmExeHash

# Configs.
Copy-Item -Path (Join-Path $scriptDir 'cland.yaml.windows.example') `
          -Destination (Join-Path $stageRoot 'configs\cland.yaml.windows.example') -Force

# Include reasoning-scores rubric if it exists alongside the cland repo
# (currently lives at /etc/minti/reasoning-scores.yaml on Linux; the
# repository ships a sample under cland/configs/reasoning-scores.yaml).
$repoRubric = Join-Path $clandDir 'configs\reasoning-scores.yaml.example'
if (Test-Path $repoRubric) {
    Copy-Item -Path $repoRubric `
              -Destination (Join-Path $stageRoot 'configs\reasoning-scores.yaml.example') -Force
}

# Install + uninstall scripts.
Copy-Item -Path (Join-Path $scriptDir 'install-cland.ps1') -Destination $stageRoot -Force
Copy-Item -Path (Join-Path $scriptDir 'uninstall-cland.ps1') -Destination $stageRoot -Force
Copy-Item -Path (Join-Path $scriptDir 'README.md') -Destination $stageRoot -Force

Write-Ok "tree assembled"

# ---------- 4. compress ----------

$outZip = "$stageRoot.zip"
if (Test-Path $outZip) { Remove-Item -Force $outZip }
Write-Step "Compressing $outZip"
Compress-Archive -Path (Join-Path $stageRoot '*') -DestinationPath $outZip
Write-Ok "$outZip"

# Final summary.
$zipHash = (Get-FileHash -Algorithm SHA256 -Path $outZip).Hash.ToLower()
Write-Host ''
Write-Host "Build artifact:"
Write-Host "  $outZip"
Write-Host "  sha256: $zipHash"
Write-Host "  bundled nssm.exe sha256: $nssmExeHash"
Write-Host ''
Write-Host 'Operator workflow:'
Write-Host "  Expand-Archive $outZip C:\Temp\cland-test"
Write-Host '  cd C:\Temp\cland-test'
Write-Host '  powershell -ExecutionPolicy Bypass -File install-cland.ps1'
