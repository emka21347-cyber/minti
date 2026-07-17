# MINTI M4 Phase J — clone minti-dev → minti-dev-2 + wire host-only NIC for mDNS.
#
# Run from the Windows host. Idempotent: safe to re-run if it fails partway.
#
# What it does (per plan + Phase J peer-review J1+J2):
#  1. Pre-flight: minti-dev must exist + be powered off
#  2. Enable DHCP on the existing host-only adapter (if disabled)
#  3. Add host-only NIC 2 to minti-dev (was nic2=none)
#  4. Clone minti-dev → minti-dev-2 (full clone, new MAC, registered)
#  5. Add host-only NIC 2 + port-forwards (ssh 2223, runtime 17780, cland 27777) to -dev-2
#  6. Re-add the shared folder on -dev-2 (clones don't inherit)
#  7. Power both VMs on headless
#  8. Wait for ssh on both (-dev on :2222, -dev-2 on :2223)
#  9. J1+J2 hygiene on -dev-2: wipe inherited MINTI state, regen ssh host keys
# 10. Print final summary
#
# This script does NOT install cland on -dev-2 — that's Pass 2B
# (Phase J plan's "ssh in and run install.sh").

# PS5.1 treats every native-command stderr write as a NativeCommandError under
# EAP=Stop, even with 2>$null. VBoxManage writes progress + benign "rule
# absent" messages to stderr that aren't actual failures. We use Continue +
# explicit $LASTEXITCODE checks instead.
$ErrorActionPreference = "Continue"
$vb = "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe"
$ssh = "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i $env:USERPROFILE/.ssh/minti_vm"
$repo = "C:\Users\aouad\Documents\CCode\MINT\MINT_wip"

function Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Pass($msg) { Write-Host "    PASS: $msg" -ForegroundColor Green }
function Fail($msg) { Write-Host "    FAIL: $msg" -ForegroundColor Red; exit 1 }

# --- 1. Pre-flight ---------------------------------------------------------
Step "1/10 pre-flight"
$vms = & $vb list vms
if (-not ($vms -match '"minti-dev"')) { Fail "minti-dev VM not found" }
$running = & $vb list runningvms
if ($running -match '"minti-dev"') {
    Write-Host "    minti-dev is running; sending acpipowerbutton..."
    & $vb controlvm minti-dev acpipowerbutton 2>&1 | Out-Null
    # Wait up to 60s for graceful shutdown
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Seconds 2
        $running = & $vb list runningvms
        if (-not ($running -match '"minti-dev"')) { break }
    }
    if ($running -match '"minti-dev"') {
        Write-Host "    didn't shut down gracefully; forcing power off..."
        & $vb controlvm minti-dev poweroff 2>&1 | Out-Null
        Start-Sleep -Seconds 3
    }
}
Pass "minti-dev present + powered off"

# --- 2. Enable DHCP on host-only ------------------------------------------
Step "2/10 ensure DHCP on host-only adapter"
$hoIfName = "VirtualBox Host-Only Ethernet Adapter"
# VirtualBox uses NetworkName="HostInterfaceNetworking-<ifname>"; match flexibly.
$dhcpList = (& $vb list dhcpservers 2>&1) -join "`n"
$enabledLine = ($dhcpList -split "`n" | Where-Object { $_ -match "^Enabled:\s+Yes" })
if ($dhcpList -match [regex]::Escape($hoIfName) -and $enabledLine) {
    Pass "DHCP server already present + enabled for host-only"
} else {
    # Try modify first (the dhcp server may exist but be disabled); fall back to add.
    & $vb dhcpserver modify --interface="$hoIfName" --enable 2>$null
    if ($LASTEXITCODE -ne 0) {
        & $vb dhcpserver add `
            --interface="$hoIfName" `
            --ip 192.168.56.100 `
            --netmask 255.255.255.0 `
            --lowerip 192.168.56.101 `
            --upperip 192.168.56.150 `
            --enable 2>$null
        if ($LASTEXITCODE -ne 0) { Fail "couldn't create or enable dhcpserver" }
        Pass "DHCP server created (192.168.56.101-150)"
    } else {
        Pass "DHCP server enabled"
    }
}

# --- 3. Add host-only NIC 2 to minti-dev ----------------------------------
Step "3/10 add host-only NIC 2 to minti-dev"
& $vb modifyvm minti-dev --nic2 hostonly --hostonlyadapter2 "$hoIfName" 2>&1 | Out-Null
Pass "minti-dev nic2 = hostonly ($hoIfName)"

# --- 4. Clone --------------------------------------------------------------
Step "4/10 clone minti-dev → minti-dev-2"
# Re-read vms list (initial pre-flight was before this script run iteration)
$vms = & $vb list vms 2>$null
if ($vms -match '"minti-dev-2"') {
    Write-Host "    minti-dev-2 already exists; skipping clone"
    Pass "minti-dev-2 reused from prior run"
} else {
    # VBoxManage clonevm emits progress on stderr; 2>$null avoids tripping
    # $ErrorActionPreference = "Stop"
    & $vb clonevm minti-dev --name minti-dev-2 --register 2>$null
    if ($LASTEXITCODE -ne 0) { Fail "clonevm failed (exit $LASTEXITCODE)" }
    Pass "minti-dev-2 cloned + registered"
}

# --- 5. NIC2 + MAC regen + port-forwards on -dev-2 ------------------------
Step "5/10 wire minti-dev-2 NICs + port-forwards"
& $vb modifyvm minti-dev-2 --macaddress1 auto 2>$null
& $vb modifyvm minti-dev-2 --nic2 hostonly --hostonlyadapter2 "$hoIfName" --macaddress2 auto 2>$null

# Remove pre-existing port-forwards (cloned from parent OR re-run) — errors when
# rule absent are expected + harmless; 2>$null discards the noise.
$forwards = @("ssh", "minti-runtime", "minti-cland")
foreach ($name in $forwards) {
    & $vb modifyvm minti-dev-2 --natpf1 delete $name 2>$null
}
& $vb modifyvm minti-dev-2 --natpf1 "ssh,tcp,127.0.0.1,2223,,22" 2>$null
& $vb modifyvm minti-dev-2 --natpf1 "minti-runtime,tcp,127.0.0.1,17780,,7780" 2>$null
& $vb modifyvm minti-dev-2 --natpf1 "minti-cland,tcp,127.0.0.1,27777,,7777" 2>$null
Pass "-dev-2: nic1 NAT (new MAC), nic2 hostonly (new MAC), ssh=:2223, runtime=:17780, cland=:27777"

# --- 6. Shared folder on -dev-2 -------------------------------------------
Step "6/10 re-add minti-repo shared folder on -dev-2"
& $vb sharedfolder remove minti-dev-2 --name minti-repo 2>$null
& $vb sharedfolder add minti-dev-2 --name minti-repo --hostpath $repo --automount 2>$null
if ($LASTEXITCODE -ne 0) { Fail "couldn't add shared folder (exit $LASTEXITCODE)" }
Pass "minti-repo shared folder mapped on -dev-2"

# --- 7. Power on both VMs --------------------------------------------------
Step "7/10 power on both VMs headless"
& $vb startvm minti-dev --type headless 2>&1 | Select-Object -First 2
& $vb startvm minti-dev-2 --type headless 2>&1 | Select-Object -First 2
Pass "both VMs starting"

# --- 8. Wait for SSH on both ----------------------------------------------
Step "8/10 wait for SSH (up to 120s each)"
function Wait-Ssh($port, $name) {
    for ($i = 0; $i -lt 60; $i++) {
        $out = & ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null `
                    -o ConnectTimeout=3 -i $env:USERPROFILE/.ssh/minti_vm `
                    -p $port minti@127.0.0.1 "echo ok" 2>$null
        if ($out -eq "ok") { Pass "ssh ready on :$port ($name)"; return $true }
        Start-Sleep -Seconds 2
    }
    Fail "ssh on :$port ($name) timed out after 120s"
}
# Clear any cached host keys on the Windows side for clean reconnects
ssh-keygen -R "[127.0.0.1]:2222" 2>$null | Out-Null
ssh-keygen -R "[127.0.0.1]:2223" 2>$null | Out-Null
Wait-Ssh 2222 "minti-dev" | Out-Null
Wait-Ssh 2223 "minti-dev-2" | Out-Null

# --- 9. J1 + J2 post-clone hygiene on -dev-2 ------------------------------
Step "9/10 J1+J2 post-clone hygiene on minti-dev-2"
$hygiene = @"
set -e
echo '--- J1 wipe MINTI state ---'
sudo systemctl stop minti-cland 2>/dev/null || true
sudo rm -rf /var/lib/minti/cland/* /home/minti/.minti /home/aouad/.minti /etc/minti/clan-* 2>/dev/null || true
echo '--- J2 regen ssh host keys ---'
sudo rm -f /etc/ssh/ssh_host_*
sudo ssh-keygen -A
sudo systemctl restart ssh
echo '--- done ---'
"@
$hygiene | ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null `
              -i $env:USERPROFILE/.ssh/minti_vm `
              -p 2223 minti@127.0.0.1 'bash -s' 2>&1 | Select-Object -Last 10
# After ssh-key regen, drop cached known_hosts entry again
ssh-keygen -R "[127.0.0.1]:2223" 2>$null | Out-Null
Pass "post-clone hygiene applied on -dev-2"

# --- 10. Summary -----------------------------------------------------------
Step "10/10 summary"
Write-Host ""
Write-Host "minti-dev:    ssh -i ~\.ssh\minti_vm -p 2222 minti@127.0.0.1  (cland :7777 → host :7777 via host-only)" -ForegroundColor Yellow
Write-Host "minti-dev-2:  ssh -i ~\.ssh\minti_vm -p 2223 minti@127.0.0.1  (cland :7777 → host :7777 via host-only)" -ForegroundColor Yellow
Write-Host ""
Write-Host "Next: ssh into -dev-2 and run sudo bash /media/sf_minti-repo/install/install.sh" -ForegroundColor Cyan
Write-Host "Then: get each VM's 192.168.56.x address (ip -4 addr show enp0s8) for cross peer-add" -ForegroundColor Cyan
