<#
.SYNOPSIS
  Install the full MINTI stack (cland + runtime + workspace) as Windows
  Services via NSSM — the door-B "Add MINTI to this machine" installer.

.DESCRIPTION
  Dist D1 installer. Extends the proven, peer-reviewed M5-B pattern
  (cland\windows\nssm\install-cland.ps1) from one service to three:

    Minti-Cland      LAN :7777 (TLS+HMAC)   LocalSystem (M5-B precedent)
    Minti-Runtime    127.0.0.1:7780         NT SERVICE\Minti-Runtime
    Minti-Workspace  127.0.0.1:8088         NT SERVICE\Minti-Workspace

  Per the D0 3-LLM review (docs/plans/distribution-roadmap.md, triage at
  scripts/m4-reviews/dist-d0-triage.md):
    F1  runtime + workspace run as NT SERVICE virtual accounts, NOT
        LocalSystem; cland keeps the live M5-B account. icacls grants:
        runtime = Modify on its own state dir; workspace = Modify on its
        own state dir + Read on the cland state dir (its shelled
        minti-cland CLI reads identity/clan_key/cland.yaml, writes
        nothing — write attempts fail closed into demo mode).
    F3  the post-install browser open is de-elevated via explorer.exe.
    F5  Ollama detection parses /api/version JSON, not just an open port.
    F7  the workspace health poll runs up to 30 s.
    F10 crash-window honesty: between binary swap and NSSM refresh the
        services are stopped; AppParameters are version-stable; re-running
        this installer is the designed repair.

  The workspace service environment sets LOCALAPPDATA=%PROGRAMDATA% —
  load-bearing, not cosmetic: the shelled minti-cland CLI resolves its
  default config/state from %LOCALAPPDATA% when NOT LocalSystem
  (cland/internal/config/paths_windows.go), and the virtual account's own
  profile would be the wrong place. With the override, every CLI default
  lands on C:\ProgramData\MINTI\... — the system state this installer
  manages. minti-workspace itself reads no per-user state (embedded SPA,
  no config file); revisit if that ever changes.

  Idempotent: re-running upgrades binaries + refreshes service config but
  preserves the state directories (clan_key, identity.json, audit.jsonl,
  all *.yaml configs). UPGRADES IN PLACE over an existing M5-B
  "Minti-Cland" service — same service name, same paths, no remove/re-add.

.PARAMETER InstallRoot
  Binaries root. Default %PROGRAMFILES%\MINTI (cland\, runtime\,
  workspace\ subdirs — cland's matches the M5-B location exactly).

.PARAMETER StateRoot
  State root. Default %PROGRAMDATA%\MINTI (cland\, runtime\, workspace\
  subdirs). Each gets a restrictive NTFS DACL.

.PARAMETER ClandPort
  Inbound TCP port for the cland HMAC-on-TLS listener. Default 7777.
  The ONLY port this installer opens in the firewall.

.PARAMETER WorkspacePort
  Loopback port for the workspace UI. Default 8088. No firewall rule —
  loopback traffic is not filtered.

.PARAMETER FirewallProfile
  Profiles for the one inbound rule. Default 'Private,Domain' (M5-B
  peer-review item 1: pass 'Private,Domain,Public' explicitly if you
  really want cland reachable on Public networks).

.PARAMETER ZipRoot
  Where this script's siblings live (bin\, configs\). Defaults to the
  script's own directory.

.PARAMETER Unattended
  Skip interactive prompts (the Ollama download-page offer). The browser
  auto-open still happens; pass -NoAutoOpen too for fully headless runs.

.PARAMETER NoAutoOpen
  Don't open the workspace URL in a browser at the end.

.PARAMETER PauseOnExit
  Wait for Enter before the window closes (set by Install-MINTI.cmd so
  the elevated console doesn't vanish with the summary).

.NOTES
  Requires Administrator. Refuses + prints remediation if not elevated
  (M5-B stance: no self-elevation from inside a running script; the
  Install-MINTI.cmd shim is the blessed double-click entry).
#>
[CmdletBinding()]
param(
    [string]$InstallRoot     = (Join-Path $env:ProgramFiles 'MINTI'),
    [string]$StateRoot       = (Join-Path $env:ProgramData 'MINTI'),
    [int]$ClandPort          = 7777,
    [int]$WorkspacePort      = 8088,
    [string]$FirewallProfile = 'Private,Domain',
    [string]$ZipRoot         = (Split-Path -Parent $MyInvocation.MyCommand.Path),
    [switch]$Unattended,
    [switch]$NoAutoOpen,
    [switch]$PauseOnExit
)

$ErrorActionPreference = 'Stop'

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

function Exit-Install {
    param([int]$Code)
    if ($PauseOnExit) {
        Write-Host ''
        Read-Host 'Press Enter to close this window' | Out-Null
    }
    exit $Code
}

# Service catalogue. Order matters: stop reverse (workspace first),
# start forward (cland first).
$svcCland     = 'Minti-Cland'
$svcRuntime   = 'Minti-Runtime'
$svcWorkspace = 'Minti-Workspace'

$dirCland     = Join-Path $InstallRoot 'cland'
$dirRuntime   = Join-Path $InstallRoot 'runtime'
$dirWorkspace = Join-Path $InstallRoot 'workspace'

$stCland      = Join-Path $StateRoot 'cland'
$stRuntime    = Join-Path $StateRoot 'runtime'
$stWorkspace  = Join-Path $StateRoot 'workspace'

# ---------- preflight ----------

if (-not (Test-Admin)) {
    Write-Err 'install-minti.ps1 requires Administrator.'
    Write-Host ''
    Write-Host 'Either double-click Install-MINTI.cmd (UAC prompt), or re-launch'
    Write-Host 'from an elevated PowerShell prompt:'
    Write-Host '  Start-Process powershell -Verb RunAs -ArgumentList "-ExecutionPolicy Bypass -File install-minti.ps1"'
    Exit-Install 2
}

Write-Step "MINTI stack installer (Dist D1)"
Write-Host "  InstallRoot      = $InstallRoot"
Write-Host "  StateRoot        = $StateRoot"
Write-Host "  ClandPort        = $ClandPort"
Write-Host "  WorkspacePort    = $WorkspacePort"
Write-Host "  FirewallProfile  = $FirewallProfile"

$bundledCland     = Join-Path $ZipRoot 'bin\minti-cland.exe'
$bundledRuntime   = Join-Path $ZipRoot 'bin\minti-runtime.exe'
$bundledWorkspace = Join-Path $ZipRoot 'bin\minti-workspace.exe'
$bundledNssm      = Join-Path $ZipRoot 'bin\nssm.exe'
$nssmSha          = Join-Path $ZipRoot 'bin\nssm.sha256'
$bundledClandYaml = Join-Path $ZipRoot 'configs\cland.yaml.windows.example'
$bundledRtYaml    = Join-Path $ZipRoot 'configs\runtime.yaml.windows.example'
$bundledRubric    = Join-Path $ZipRoot 'configs\reasoning-scores.yaml.example'

foreach ($p in @($bundledCland, $bundledRuntime, $bundledWorkspace, $bundledNssm, $nssmSha, $bundledClandYaml, $bundledRtYaml)) {
    if (-not (Test-Path $p)) {
        Write-Err "missing required bundled file: $p"
        Write-Host '  Re-extract the zip; the bundle is incomplete.'
        Exit-Install 3
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
    Exit-Install 4
}
Write-Ok "NSSM hash = $actualSha"

# ---------- 2. stray-process + port-conflict preflight ----------
# Refuse + remediate, never kill (D0 safety bar). The M5-D finding: a
# Phase-J-style FOREGROUND minti-cland.exe silently blocks the service.

Write-Step 'Checking for stray processes and port conflicts'

function Get-ServicePid {
    param([string]$Name)
    $wmi = Get-CimInstance Win32_Service -Filter "Name='$Name'" -ErrorAction SilentlyContinue
    if ($wmi -and $wmi.ProcessId -gt 0) { return [int]$wmi.ProcessId }
    return 0
}

$svcPids = @{}
foreach ($s in @($svcCland, $svcRuntime, $svcWorkspace)) { $svcPids[$s] = Get-ServicePid $s }

# NSSM is the service process; the app exes are its children. A stray is
# any minti-* process that is neither a service's nssm nor its child.
$childPids = @{}
foreach ($s in $svcPids.Keys) {
    if ($svcPids[$s] -gt 0) {
        Get-CimInstance Win32_Process -Filter "ParentProcessId=$($svcPids[$s])" -ErrorAction SilentlyContinue |
            ForEach-Object { $childPids[[int]$_.ProcessId] = $s }
    }
}

$strays = @()
foreach ($pname in @('minti-cland', 'minti-runtime', 'minti-workspace')) {
    Get-Process -Name $pname -ErrorAction SilentlyContinue | ForEach-Object {
        $procId = [int]$_.Id
        $isService = $false
        foreach ($s in $svcPids.Keys) { if ($svcPids[$s] -eq $procId) { $isService = $true } }
        if ($childPids.ContainsKey($procId)) { $isService = $true }
        if (-not $isService) { $strays += "$pname (PID $procId)" }
    }
}
if ($strays.Count -gt 0) {
    Write-Err "stray foreground MINTI process(es) found: $($strays -join ', ')"
    Write-Host '  These are not service-managed and will block ports/binaries.'
    Write-Host '  Stop them first (Stop-Process -Id <PID>) and re-run. This'
    Write-Host '  installer never kills processes it did not start.'
    Exit-Install 8
}
Write-Ok 'no stray foreground MINTI processes'

# Foreign listeners on our loopback ports (our own services are fine —
# they get stopped for the upgrade).
foreach ($check in @(@{Port = 7780; Svc = $svcRuntime }, @{Port = $WorkspacePort; Svc = $svcWorkspace })) {
    $conns = Get-NetTCPConnection -LocalPort $check.Port -State Listen -ErrorAction SilentlyContinue
    foreach ($c in $conns) {
        $owner = [int]$c.OwningProcess
        $ours = $false
        foreach ($s in $svcPids.Keys) { if ($svcPids[$s] -eq $owner) { $ours = $true } }
        if ($childPids.ContainsKey($owner)) { $ours = $true }
        if (-not $ours) {
            $pname = (Get-Process -Id $owner -ErrorAction SilentlyContinue).ProcessName
            Write-Err "port $($check.Port) is held by foreign process '$pname' (PID $owner)"
            Write-Host "  $($check.Svc) needs this port. Stop that process or change its"
            Write-Host '  port, then re-run. This installer never kills foreign processes.'
            Exit-Install 8
        }
    }
}
Write-Ok "ports 7780 + $WorkspacePort are free or held by our own services"

# ---------- 3. stop existing services (upgrade path) ----------
# Reverse dependency order: workspace -> runtime -> cland. Each skipped
# gracefully when absent (fresh install / M5-B-only box).

Write-Step 'Stopping existing services for upgrade (if present)'
foreach ($s in @($svcWorkspace, $svcRuntime, $svcCland)) {
    $svc = Get-Service -Name $s -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -ne 'Stopped') {
        Write-Host "  stopping $s"
        Stop-Service -Name $s -Force -ErrorAction SilentlyContinue
        # Give NSSM a moment to release file handles before binary swap.
        Start-Sleep -Seconds 1
    } elseif ($svc) {
        Write-Host "  $s already stopped"
    } else {
        Write-Host "  $s not installed yet"
    }
}
Write-Ok 'services quiesced'

# ---------- 4. create dirs ----------

Write-Step 'Creating directories'
$dirMcp = Join-Path $InstallRoot 'mcp'   # matches cland.yaml mcp.binaries_dir
foreach ($d in @($dirCland, $dirRuntime, $dirWorkspace, $dirMcp, $stCland, $stRuntime, $stWorkspace,
        (Join-Path $stCland 'logs'), (Join-Path $stRuntime 'logs'), (Join-Path $stWorkspace 'logs'))) {
    New-Item -ItemType Directory -Path $d -Force | Out-Null
}
Write-Ok "$InstallRoot\{cland,runtime,workspace,mcp}"
Write-Ok "$StateRoot\{cland,runtime,workspace} (+logs)"

# ---------- 5. stage binaries + per-service NSSM copies ----------

Write-Step 'Staging binaries'
$exeCland     = Join-Path $dirCland 'minti-cland.exe'
$exeRuntime   = Join-Path $dirRuntime 'minti-runtime.exe'
$exeWorkspace = Join-Path $dirWorkspace 'minti-workspace.exe'

Copy-Item -Path $bundledCland     -Destination $exeCland     -Force
Copy-Item -Path $bundledRuntime   -Destination $exeRuntime   -Force
Copy-Item -Path $bundledWorkspace -Destination $exeWorkspace -Force
# Per-service NSSM copies (D0 review Q1: endorsed — total isolation, no
# shared-file coupling; the cland copy lands exactly where M5-B put it).
foreach ($d in @($dirCland, $dirRuntime, $dirWorkspace)) {
    Copy-Item -Path $bundledNssm -Destination (Join-Path $d 'nssm.exe') -Force
}
Write-Ok "minti-cland.exe     -> $exeCland"
Write-Ok "minti-runtime.exe   -> $exeRuntime"
Write-Ok "minti-workspace.exe -> $exeWorkspace"
Write-Ok 'nssm.exe            -> each service dir'

# MCP tool servers — the daemon's agent loop spawns these from mcp.binaries_dir.
$bundledMcpDir = Join-Path $ZipRoot 'bin\mcp'
if (Test-Path $bundledMcpDir) {
    $mcpCopied = 0
    Get-ChildItem -Path $bundledMcpDir -Filter 'minti-mcp-*.exe' | ForEach-Object {
        Copy-Item -Path $_.FullName -Destination (Join-Path $dirMcp $_.Name) -Force
        $mcpCopied++
    }
    Write-Ok "$mcpCopied MCP tool servers -> $dirMcp"
} else {
    Write-Warn "no bundled MCP servers found at $bundledMcpDir — the agent's tools will be unavailable"
}

# ---------- 6. stage configs (preserve existing on re-install) ----------

Write-Step 'Staging configs'
$clandYaml = Join-Path $stCland 'cland.yaml'
if (Test-Path $clandYaml) {
    Write-Ok "cland.yaml         preserved ($clandYaml)"
} else {
    Copy-Item -Path $bundledClandYaml -Destination $clandYaml -Force
    Write-Ok "cland.yaml         -> $clandYaml"
}

$rtYaml = Join-Path $stRuntime 'runtime.yaml'
if (Test-Path $rtYaml) {
    Write-Ok "runtime.yaml       preserved ($rtYaml)"
} else {
    Copy-Item -Path $bundledRtYaml -Destination $rtYaml -Force
    Write-Ok "runtime.yaml       -> $rtYaml"
}

$rubric = Join-Path $stCland 'reasoning-scores.yaml'
if (Test-Path $bundledRubric) {
    if (Test-Path $rubric) {
        Write-Ok 'reasoning-scores.yaml preserved'
    } else {
        Copy-Item -Path $bundledRubric -Destination $rubric -Force
        Write-Ok "reasoning-scores.yaml -> $rubric"
    }
    # System-level mirror for cland's LocalSystem DefaultRubricPath (M5-B).
    $sysRubric = Join-Path $StateRoot 'reasoning-scores.yaml'
    if (-not (Test-Path $sysRubric)) {
        Copy-Item -Path $rubric -Destination $sysRubric -Force
        Write-Ok "system rubric mirror -> $sysRubric"
    }
}

# ---------- 7. register / refresh the three services ----------
# Registration happens BEFORE the DACL grants so the NT SERVICE virtual
# account SIDs are unambiguously resolvable, and while everything is
# stopped so `sc config obj=` is legal.

Write-Step 'Registering Windows Services via NSSM'

function Register-MintiService {
    param(
        [string]$Name,
        [string]$Nssm,
        [string]$Exe,
        [string]$AppParams,
        [string]$WorkDir,
        [string]$LogDir,
        [string]$Description
    )
    $svc = Get-Service -Name $Name -ErrorAction SilentlyContinue
    if ($svc) {
        Write-Host "  $Name exists; refreshing config"
    } else {
        & $Nssm install $Name "$Exe" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Err "nssm install $Name failed (exit $LASTEXITCODE)"
            Exit-Install 6
        }
        Write-Host "  $Name installed"
    }
    & $Nssm set $Name Application      "$Exe"      | Out-Null
    & $Nssm set $Name AppParameters    $AppParams  | Out-Null
    & $Nssm set $Name AppDirectory     $WorkDir    | Out-Null
    & $Nssm set $Name AppStdout        (Join-Path $LogDir 'out.log') | Out-Null
    & $Nssm set $Name AppStderr        (Join-Path $LogDir 'err.log') | Out-Null
    & $Nssm set $Name AppRotateFiles   1           | Out-Null
    & $Nssm set $Name AppRotateBytes   10485760    | Out-Null
    & $Nssm set $Name AppRotateOnline  1           | Out-Null
    & $Nssm set $Name Start            SERVICE_AUTO_START | Out-Null
    # Stop-method timings — M5 peer-review item 7 (Go shutdown 3 s; NSSM
    # waits 5.5 s; comfortably under the SCM ~10 s budget).
    & $Nssm set $Name AppStopMethodConsole 5500 | Out-Null
    & $Nssm set $Name AppStopMethodWindow  5500 | Out-Null
    & $Nssm set $Name Description      $Description | Out-Null
}

$nssmCland     = Join-Path $dirCland 'nssm.exe'
$nssmRuntime   = Join-Path $dirRuntime 'nssm.exe'
$nssmWorkspace = Join-Path $dirWorkspace 'nssm.exe'

Register-MintiService -Name $svcCland -Nssm $nssmCland -Exe $exeCland `
    -AppParams "--config `"$clandYaml`"" -WorkDir $stCland -LogDir (Join-Path $stCland 'logs') `
    -Description 'MINTI Clan daemon (Orchestrator election + peer routing + MCP tool exec)'
# cland account: LocalSystem, the proven M5-B identity (D0 review F1).
& $nssmCland set $svcCland ObjectName LocalSystem | Out-Null

Register-MintiService -Name $svcRuntime -Nssm $nssmRuntime -Exe $exeRuntime `
    -AppParams "--config `"$rtYaml`"" -WorkDir $stRuntime -LogDir (Join-Path $stRuntime 'logs') `
    -Description 'MINTI runtime adapter (OpenAI/Ollama/Anthropic surfaces over local models, loopback :7780)'

Register-MintiService -Name $svcWorkspace -Nssm $nssmWorkspace -Exe $exeWorkspace `
    -AppParams "-listen 127.0.0.1:$WorkspacePort" -WorkDir $stWorkspace -LogDir (Join-Path $stWorkspace 'logs') `
    -Description 'MINTI Clan Workspace (web UI, loopback only until PIN/bearer auth lands)'

# Virtual accounts for the two loopback daemons (D0 review F1). sc.exe
# takes no password for NT SERVICE\ accounts; NSSM's ObjectName setter
# demands one, so sc is the right tool. Services are stopped here.
Write-Step 'Setting service accounts (least privilege — D0 review F1)'
& sc.exe config $svcRuntime obj= "NT SERVICE\$svcRuntime" | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Err "sc config $svcRuntime obj failed (exit $LASTEXITCODE)"; Exit-Install 6 }
& sc.exe config $svcWorkspace obj= "NT SERVICE\$svcWorkspace" | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Err "sc config $svcWorkspace obj failed (exit $LASTEXITCODE)"; Exit-Install 6 }
Write-Ok "$svcRuntime   -> NT SERVICE\$svcRuntime"
Write-Ok "$svcWorkspace -> NT SERVICE\$svcWorkspace"
Write-Ok "$svcCland     -> LocalSystem (M5-B precedent, upgraded in place)"

# Workspace service environment — all three entries are load-bearing:
#   PATH          lets exec.LookPath("minti-cland") find the CLI.
#   MINTI_CONFIG  points the shelled CLI straight at the system cland.yaml
#                 (cland's DefaultConfigPath honors this env FIRST) — the
#                 explicit, account-independent fix; matches the Linux unit.
#   LOCALAPPDATA  kept as belt-and-braces for any CLI default path that is
#                 not the config file (paths_windows.go falls back to
#                 %LOCALAPPDATA% for non-SYSTEM processes).
# Scoped to this service's registry key; removed with the service. cland
# and runtime envs are deliberately untouched (an operator may have set
# e.g. MINTI_CLAND_FORCE_HEALTHY on the M5-B service — preserved).
$wsPath = "$dirCland;$env:SystemRoot\System32;$env:SystemRoot"
$wsConfig = Join-Path $stCland 'cland.yaml'
& $nssmWorkspace set $svcWorkspace AppEnvironmentExtra "PATH=$wsPath" "MINTI_CONFIG=$wsConfig" "LOCALAPPDATA=$env:ProgramData" | Out-Null
Write-Ok 'workspace env: PATH includes cland dir; MINTI_CONFIG + LOCALAPPDATA -> ProgramData'

# ---------- 8. DACLs (strict base + least-privilege grants) ----------
# One icacls call per state dir listing ALL its grants — idempotent on
# re-run (each run resets inheritance and rewrites the full grant set).

Write-Step 'Applying restrictive DACLs to state dirs'
$daclSpecs = @(
    @{ Dir = $stCland;     Grants = @('SYSTEM:(OI)(CI)F', 'BUILTIN\Administrators:(OI)(CI)F', "NT SERVICE\${svcWorkspace}:(OI)(CI)RX") },
    @{ Dir = $stRuntime;   Grants = @('SYSTEM:(OI)(CI)F', 'BUILTIN\Administrators:(OI)(CI)F', "NT SERVICE\${svcRuntime}:(OI)(CI)M") },
    @{ Dir = $stWorkspace; Grants = @('SYSTEM:(OI)(CI)F', 'BUILTIN\Administrators:(OI)(CI)F', "NT SERVICE\${svcWorkspace}:(OI)(CI)M") }
)
foreach ($spec in $daclSpecs) {
    $grantArgs = @()
    foreach ($g in $spec.Grants) { $grantArgs += @('/grant:r', $g) }
    & icacls $spec.Dir /inheritance:r @grantArgs | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Err "icacls failed (exit $LASTEXITCODE) on $($spec.Dir)"
        Exit-Install 5
    }
    Write-Ok "$($spec.Dir)  [$($spec.Grants -join ' | ')]"
}

# ---------- 9. firewall rule (the ONE inbound change) ----------

Write-Step "Configuring inbound firewall rule (Profile=$FirewallProfile)"
$ruleName = 'MINTI cland (Clan TLS)'
$existingRule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
$profiles = $FirewallProfile -split ',' | ForEach-Object { $_.Trim() }
if ($existingRule) {
    # Program path can't be cleanly changed post-creation; remove+recreate
    # (M5-B pattern — also heals a renamed InstallRoot between installs).
    Remove-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
}
New-NetFirewallRule -DisplayName $ruleName `
    -Direction Inbound -Action Allow -Protocol TCP -LocalPort $ClandPort `
    -Profile $profiles -Program $exeCland | Out-Null
Write-Ok "rule '$ruleName' :$ClandPort (profiles: $($profiles -join ', '))"
Write-Ok 'runtime + workspace are loopback-only: no firewall rules needed or made'

if ($FirewallProfile -notmatch 'Public') {
    Write-Warn2 'rule excludes Public -- if your network reads as Public, either'
    Write-Warn2 'set it to Private in Settings > Network, or re-run with'
    Write-Warn2 '  -FirewallProfile Private,Domain,Public'
}

# ---------- 10. Ollama detect-or-guide (never silent-install) ----------

Write-Step 'Detecting Ollama (detect-or-guide — this installer never downloads third-party software)'
$ollamaStatus = 'not found'
$ollamaCmd = Get-Command ollama.exe -ErrorAction SilentlyContinue
if ($ollamaCmd) {
    $ollamaStatus = "found on PATH ($($ollamaCmd.Source))"
} else {
    $defaultOllama = Join-Path $env:LOCALAPPDATA 'Programs\Ollama\ollama.exe'
    if (Test-Path $defaultOllama) {
        $ollamaStatus = "found at $defaultOllama (not on system PATH)"
    }
}
# Liveness probe — D0 review F5: parse /api/version JSON for a version
# field rather than trusting any open port. Advisory only.
$ollamaServing = $false
try {
    $resp = Invoke-RestMethod -Uri 'http://127.0.0.1:11434/api/version' -TimeoutSec 3 -ErrorAction Stop
    if ($resp -and $resp.version) {
        $ollamaServing = $true
        $ollamaStatus = "serving on 127.0.0.1:11434 (version $($resp.version))"
    }
} catch { }

if ($ollamaStatus -eq 'not found') {
    Write-Warn2 'Ollama not detected. MINTI installs fine without it, but the'
    Write-Warn2 'runtime has no local models until Ollama (or another backend)'
    Write-Warn2 'is present. Get it from: https://ollama.com/download/windows'
    if (-not $Unattended) {
        $answer = Read-Host '  Open the Ollama download page in your browser? [y/N]'
        if ($answer -match '^[Yy]') {
            Start-Process explorer.exe 'https://ollama.com/download/windows'
        }
    }
} elseif (-not $ollamaServing) {
    Write-Ok "$ollamaStatus"
    Write-Warn2 'Ollama is installed but not serving. Launch the Ollama app once;'
    Write-Warn2 'it auto-starts at login thereafter. MINTI works either way and'
    Write-Warn2 'picks it up automatically when it appears.'
} else {
    Write-Ok "$ollamaStatus"
}

# ---------- 11. start services (dependency order) ----------

Write-Step 'Starting services (cland -> runtime -> workspace)'
foreach ($s in @($svcCland, $svcRuntime, $svcWorkspace)) {
    Start-Service -Name $s -ErrorAction SilentlyContinue
    $svc = Get-Service -Name $s -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -eq 'Running') {
        Write-Ok "$s is Running"
    } else {
        Write-Err "$s did not enter Running state."
        $errLog = switch ($s) {
            $svcCland     { Join-Path $stCland 'logs\err.log' }
            $svcRuntime   { Join-Path $stRuntime 'logs\err.log' }
            $svcWorkspace { Join-Path $stWorkspace 'logs\err.log' }
        }
        Write-Host "  Check: $errLog"
        Write-Host '  Then re-run this installer (idempotent) after fixing the cause.'
        Exit-Install 7
    }
}

# ---------- 12. workspace health poll + de-elevated auto-open ----------

Write-Step "Waiting for the workspace to answer on http://127.0.0.1:$WorkspacePort/ (up to 30 s)"
$workspaceUrl = "http://127.0.0.1:$WorkspacePort/"
$healthy = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $r = Invoke-WebRequest -Uri $workspaceUrl -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
        if ($r.StatusCode -eq 200) { $healthy = $true; break }
    } catch { Start-Sleep -Seconds 1 }
}

# Runtime reachability note (informational — Ollama may legitimately be
# absent; /minti/health reports the backend honestly).
try {
    $null = Invoke-WebRequest -Uri 'http://127.0.0.1:7780/minti/health' -UseBasicParsing -TimeoutSec 3 -ErrorAction Stop
    Write-Ok 'runtime answering on 127.0.0.1:7780'
} catch {
    Write-Warn2 'runtime not answering on 127.0.0.1:7780 yet (check its err.log if this persists)'
}

if ($healthy) {
    Write-Ok "workspace answering at $workspaceUrl"
    if (-not $NoAutoOpen) {
        # D0 review F3: explorer.exe marshals the URL to the desktop
        # user's NON-elevated context (this script runs elevated).
        Start-Process explorer.exe $workspaceUrl
        Write-Ok 'opened the workspace in your browser (de-elevated)'
    }
} else {
    Write-Err "workspace did not answer 200 at $workspaceUrl within 30 s."
    Write-Host "  Status:  Get-Service $svcWorkspace"
    Write-Host "  Logs:    $(Join-Path $stWorkspace 'logs')\{out,err}.log"
    Write-Host "  The URL to try manually once fixed: $workspaceUrl"
    Exit-Install 9
}

# ---------- 13. summary + Defender guidance ----------

Write-Step 'Install complete'
Write-Host ''
Write-Host "Services:"
Write-Host "  $svcCland      LAN :$ClandPort (TLS+HMAC)   LocalSystem"
Write-Host "  $svcRuntime    127.0.0.1:7780              NT SERVICE\$svcRuntime"
Write-Host "  $svcWorkspace  127.0.0.1:$WorkspacePort              NT SERVICE\$svcWorkspace"
Write-Host ''
Write-Host "Workspace:      $workspaceUrl"
Write-Host "Binaries:       $InstallRoot\{cland,runtime,workspace}"
Write-Host "State + logs:   $StateRoot\{cland,runtime,workspace}"
Write-Host "Ollama:         $ollamaStatus"
Write-Host ''
Write-Host 'Verify anytime:'
Write-Host "  Get-Service $svcCland, $svcRuntime, $svcWorkspace"
Write-Host "  & '$exeCland' show"
Write-Host ''
Write-Host 'Join an existing Clan:'
Write-Host "  & '$exeCland' join --mnemonic '<12 words>' --address <ip>:7777 --pin sha256:<hex>"
Write-Host ''
Write-Host 'Uninstall (preserves your Clan identity unless -Purge):'
Write-Host "  powershell -ExecutionPolicy Bypass -File uninstall-minti.ps1"
Write-Host ''
Write-Warn2 'DEFENDER GUIDANCE (M5 peer-review item 8)'
Write-Host '  Unsigned LocalSystem services doing multicast (mDNS, UDP 5353) can'
Write-Host '  trigger Windows Defender behavioural blocks within ~5 minutes of'
Write-Host '  service start, silently quarantining minti-cland.exe mid-session.'
Write-Host '  Until MINTI ships code-signed, add Folder Exclusions:'
Write-Host ''
Write-Host '    Settings > Privacy & security > Windows Security > Virus & threat protection'
Write-Host '      Manage settings > Exclusions > Add or remove exclusions > Folder'
Write-Host "      Add: $InstallRoot"
Write-Host "      Add: $StateRoot"
Write-Host ''
Write-Host '  Skip this step on managed corporate machines where Group Policy may'
Write-Host '  override Defender; ask IT to allowlist instead.'

Exit-Install 0
