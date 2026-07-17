# Tiny launcher for minti-status interactive on Windows. Sets PATH so
# the Clan probe can find minti-cland.exe (installed by the NSSM
# service to %PROGRAMFILES%\MINTI\cland), then runs the dashboard.
#
# Used by `wt.exe` to dodge wt's command-line parsing gotchas. wt
# treats `;` as subcommand-delimiters and splits unquoted spaces in
# args even when PowerShell would quote them — both bite us if we try
# to pass an inline `-Command` string. This launcher takes that string
# OUT of the wt invocation, so the wt call stays simple:
#
#   wt.exe new-tab --title minti-status powershell.exe -NoProfile `
#     -NoExit -ExecutionPolicy Bypass -File <this-script>
#
# Keep the `--title` value a single word (no spaces, no parens), or
# wt will reparse it as a positional command argument.

$ErrorActionPreference = 'Stop'

# Resolve the minti-status binary relative to this script (so the
# launcher works whether the repo is at C:\Users\... or moved
# elsewhere).
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe  = Join-Path $root 'minti-status.exe'

# Add minti-cland's install dir to PATH so the Clan probe finds it.
$clandDir = 'C:\Program Files\MINTI\cland'
if (Test-Path $clandDir) {
    $env:Path = "$clandDir;$env:Path"
}

if (-not (Test-Path $exe)) {
    Write-Host "minti-status.exe not found at $exe"
    Write-Host "Build it with: cd $root; go build -o minti-status.exe ./cmd/minti-status"
    Write-Host ''
    Write-Host 'Press Enter to close...'
    Read-Host
    exit 1
}

& $exe
