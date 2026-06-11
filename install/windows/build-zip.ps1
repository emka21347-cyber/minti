<#
.SYNOPSIS
  Build the full-stack MINTI Windows distribution zip (door B).

.DESCRIPTION
  Dist D1 release packager. Produces
  `dist/minti-windows-amd64-v<VERSION>.zip` containing cland + runtime +
  workspace, NSSM (SHA-256 sidecar), configs, installer/uninstaller, the
  Install-MINTI.cmd shim, and README.

  Sibling of the proven cland\windows\nssm\build-zip.ps1 (M5-B, which
  remains for the cland-only artifact). Reuses that script's NSSM download
  cache at cland\windows\nssm\.cache so the binary is fetched (and
  release-engineer-verified) exactly once per checkout.

  Steps:
    1. Cross-compile minti-cland, minti-runtime, minti-workspace for
       windows-amd64 (-trimpath -s -w; -X main.version where the binary
       has the symbol).
    2. Locate (or download) nssm-2.24.zip, extract win64/nssm.exe,
       compute its SHA-256 -> bin\nssm.sha256 (verified at install time).
    3. Assemble the staging tree + Compress-Archive to dist/.

.PARAMETER Version
  Version string for the zip name + -X main.version. Default 0.4.0-D1
  (keep in sync with the Makefile VERSION).

.PARAMETER NssmUrl
  Override the NSSM download URL (used only when the M5-B cache is
  absent). Default https://nssm.cc/release/nssm-2.24.zip.

.PARAMETER GoExe
  Path to go.exe. Defaults to "go" (PATH lookup).
#>
[CmdletBinding()]
param(
    [string]$Version = '0.4.0-D1',
    [string]$NssmUrl = 'https://nssm.cc/release/nssm-2.24.zip',
    [string]$GoExe   = 'go'
)

$ErrorActionPreference = 'Stop'

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot  = (Resolve-Path (Join-Path $scriptDir '..\..')).Path
$distDir   = Join-Path $repoRoot 'dist'
$cacheDir  = Join-Path $repoRoot 'cland\windows\nssm\.cache'   # shared with M5-B builder
$stageRoot = Join-Path $distDir "minti-windows-amd64-v$Version"

function Write-Step { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok   { param([string]$m) Write-Host "  ok: $m" -ForegroundColor Green }
function Write-Err  { param([string]$m) Write-Host "  ERROR: $m" -ForegroundColor Red }

# ---------- 1. cross-compile the three binaries ----------

$builds = @(
    @{ Name = 'minti-cland.exe';     Dir = (Join-Path $repoRoot 'cland');           Pkg = './cmd/minti-cland';     Versioned = $true },
    @{ Name = 'minti-runtime.exe';   Dir = (Join-Path $repoRoot 'runtime-adapter'); Pkg = './cmd/minti-runtime';   Versioned = $false },
    @{ Name = 'minti-workspace.exe'; Dir = (Join-Path $repoRoot 'workspace');       Pkg = './cmd/minti-workspace'; Versioned = $true }
)

$outDir = Join-Path $distDir 'd1-build'
New-Item -ItemType Directory -Path $outDir -Force | Out-Null

$env:GOOS   = 'windows'
$env:GOARCH = 'amd64'
try {
    foreach ($b in $builds) {
        Write-Step "Cross-compiling $($b.Name) (windows-amd64, version=$Version)"
        $ldflags = '-s -w'
        if ($b.Versioned) { $ldflags = "-X main.version=$Version -s -w" }
        $out = Join-Path $outDir $b.Name
        Push-Location $b.Dir
        try {
            & $GoExe build -trimpath -ldflags $ldflags -o $out $b.Pkg
            if ($LASTEXITCODE -ne 0) {
                Write-Err "go build $($b.Name) failed (exit $LASTEXITCODE)"
                exit 1
            }
        } finally {
            Pop-Location
        }
        Write-Ok "$out"
    }
} finally {
    Remove-Item env:GOOS, env:GOARCH -ErrorAction SilentlyContinue
}

# ---------- 2. NSSM (shared cache with the M5-B builder) ----------

New-Item -ItemType Directory -Path $cacheDir -Force | Out-Null
$nssmZip = Join-Path $cacheDir 'nssm-2.24.zip'

if (-not (Test-Path $nssmZip)) {
    Write-Step 'Downloading NSSM 2.24 (cache miss)'
    Write-Host "  $NssmUrl"
    # nssm.cc is famously flaky; curl.exe handles retries better than
    # PS5.1's Invoke-WebRequest (M5-B finding).
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & curl.exe -sS -L -o $nssmZip --max-time 60 --retry 3 --retry-delay 5 $NssmUrl
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $nssmZip) -or (Get-Item $nssmZip).Length -lt 100000) {
            Write-Err "curl.exe download of $NssmUrl failed (exit=$LASTEXITCODE)."
            Write-Host '  Try again later or pass -NssmUrl <mirror>.'
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

$nssmExtractDir = Join-Path $cacheDir 'nssm-2.24'
if (-not (Test-Path $nssmExtractDir)) {
    Expand-Archive -Path $nssmZip -DestinationPath $cacheDir -Force
}
$nssmExe = Join-Path $nssmExtractDir 'win64\nssm.exe'
if (-not (Test-Path $nssmExe)) {
    Write-Err 'expected win64\nssm.exe not found in extracted nssm zip'
    exit 2
}
$nssmExeHash = (Get-FileHash -Algorithm SHA256 -Path $nssmExe).Hash.ToLower()
Write-Host "  win64\nssm.exe sha256 = $nssmExeHash"

# ---------- 3. assemble staging tree ----------

Write-Step "Assembling staging tree at $stageRoot"
if (Test-Path $stageRoot) {
    Remove-Item -Recurse -Force $stageRoot
}
New-Item -ItemType Directory -Path (Join-Path $stageRoot 'bin') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $stageRoot 'configs') -Force | Out-Null

foreach ($b in $builds) {
    Copy-Item -Path (Join-Path $outDir $b.Name) -Destination (Join-Path $stageRoot "bin\$($b.Name)") -Force
}
Copy-Item -Path $nssmExe -Destination (Join-Path $stageRoot 'bin\nssm.exe') -Force
Set-Content -Encoding ASCII -Path (Join-Path $stageRoot 'bin\nssm.sha256') -Value $nssmExeHash

Copy-Item -Path (Join-Path $repoRoot 'cland\windows\nssm\cland.yaml.windows.example') `
          -Destination (Join-Path $stageRoot 'configs\cland.yaml.windows.example') -Force
Copy-Item -Path (Join-Path $scriptDir 'configs\runtime.yaml.windows.example') `
          -Destination (Join-Path $stageRoot 'configs\runtime.yaml.windows.example') -Force
$repoRubric = Join-Path $repoRoot 'cland\configs\reasoning-scores.yaml.example'
if (Test-Path $repoRubric) {
    Copy-Item -Path $repoRubric -Destination (Join-Path $stageRoot 'configs\reasoning-scores.yaml.example') -Force
}

Copy-Item -Path (Join-Path $scriptDir 'install-minti.ps1')   -Destination $stageRoot -Force
Copy-Item -Path (Join-Path $scriptDir 'uninstall-minti.ps1') -Destination $stageRoot -Force
Copy-Item -Path (Join-Path $scriptDir 'Install-MINTI.cmd')   -Destination $stageRoot -Force
Copy-Item -Path (Join-Path $scriptDir 'README.md')           -Destination $stageRoot -Force

Write-Ok 'tree assembled'

# ---------- 4. compress ----------

$outZip = "$stageRoot.zip"
if (Test-Path $outZip) { Remove-Item -Force $outZip }
Write-Step "Compressing $outZip"
Compress-Archive -Path (Join-Path $stageRoot '*') -DestinationPath $outZip
Write-Ok "$outZip"

$zipHash = (Get-FileHash -Algorithm SHA256 -Path $outZip).Hash.ToLower()
Write-Host ''
Write-Host 'Build artifact:'
Write-Host "  $outZip"
Write-Host "  sha256: $zipHash"
Write-Host "  bundled nssm.exe sha256: $nssmExeHash"
Write-Host ''
Write-Host 'checksums.txt line (site D3):'
Write-Host "  $zipHash  minti-windows-amd64-v$Version.zip"
Write-Host ''
Write-Host 'Operator workflow:'
Write-Host "  Expand-Archive $outZip C:\Temp\minti-install"
Write-Host '  cd C:\Temp\minti-install'
Write-Host '  double-click Install-MINTI.cmd   (or: powershell -ExecutionPolicy Bypass -File install-minti.ps1)'
