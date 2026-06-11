@echo off
REM ============================================================
REM  MINTI one-click installer shim (Dist D1).
REM
REM  What this does — nothing hidden:
REM    1. Asks Windows for Administrator rights (the UAC prompt
REM       you are about to see).
REM    2. Runs install-minti.ps1 from this folder, which installs
REM       the three MINTI services and opens the workspace at
REM       http://127.0.0.1:8088 when done.
REM
REM  Prefer doing it by hand? Open an elevated PowerShell here:
REM    powershell -ExecutionPolicy Bypass -File install-minti.ps1
REM
REM  D0 review Q2: this double-click -> UAC consent flow is the
REM  standard Windows pattern (distinct from a script silently
REM  re-launching itself, which M5-B rejected).
REM ============================================================
setlocal
set "PSFILE=%~dp0install-minti.ps1"
if not exist "%PSFILE%" (
    echo ERROR: install-minti.ps1 not found next to this shim.
    echo Re-extract the zip and keep the folder contents together.
    pause
    exit /b 3
)
echo Requesting Administrator rights (UAC prompt)...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$q=[char]34; Start-Process powershell.exe -Verb RunAs -ArgumentList ('-NoProfile -ExecutionPolicy Bypass -File ' + $q + $env:PSFILE + $q + ' -PauseOnExit')"
if errorlevel 1 (
    echo.
    echo The UAC prompt was declined or elevation failed. Nothing was installed.
    pause
    exit /b 2
)
echo The installer is running in the elevated window.
endlocal
