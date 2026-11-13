# Installs TapperVPN on this Windows PC as a service that starts with Windows,
# plus a daily scheduled task that installs signed updates from GitHub.
# Run from an Administrator PowerShell:
#
#   scripts\install-windows.ps1                 # from this repo's bin\ (after building)
#   scripts\install-windows.ps1 -From <folder>  # from an unzipped release
#   scripts\install-windows.ps1 -Uninstall
#
# Then sign in once: & "C:\Program Files\TapperVPN\tappervpn.exe" up --login-server=<url> --auth-key=<key>
param(
    [string]$From,
    [string]$Dest = "$env:ProgramFiles\TapperVPN",
    [switch]$Uninstall
)
$ErrorActionPreference = "Stop"
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { throw "Run this from an Administrator PowerShell." }

$task = "TapperVPN Update"
$helperTask = "TapperVPN Remote Helper"
$appTask = "TapperVPN App"
$files = "tappervpn.exe", "tappervpnd.exe", "tappervpn-updater.exe", "tappervpn-remote.exe", "tappervpn-gui.exe", "wintun.dll"

if ($Uninstall) {
    Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $helperTask -Confirm:$false -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $appTask -Confirm:$false -ErrorAction SilentlyContinue
    Stop-Process -Name tappervpn-remote -Force -ErrorAction SilentlyContinue
    Stop-Process -Name tappervpn-gui -Force -ErrorAction SilentlyContinue
    if (Test-Path "$Dest\tappervpnd.exe") { & "$Dest\tappervpnd.exe" uninstall-system-daemon }
    Write-Host "Removed the service, update task, and remote helper. Files are still in $Dest."
    return
}

if (-not $From) { $From = Join-Path (Split-Path $PSScriptRoot -Parent) "bin" }
foreach ($f in $files) {
    if (-not (Test-Path (Join-Path $From $f))) { throw "$f not found in $From (build it, or pass -From)." }
}

# Stop a running copy before replacing its files.
$svc = Get-Service TapperVPN -ErrorAction SilentlyContinue
if ($svc) { Stop-Service TapperVPN -Force }
Stop-Process -Name tappervpn-remote -Force -ErrorAction SilentlyContinue
Stop-Process -Name tappervpn-gui -Force -ErrorAction SilentlyContinue

New-Item -ItemType Directory -Force $Dest | Out-Null
foreach ($f in $files) { Copy-Item (Join-Path $From $f) $Dest -Force }

if ($svc) {
    Start-Service TapperVPN
} else {
    & "$Dest\tappervpnd.exe" install-system-daemon
    if ($LASTEXITCODE) { throw "installing the TapperVPN service failed" }
}

# Daily update check as SYSTEM, at a random minute after 3 AM; also an hour
# after startup for PCs that are off at night.
$action = New-ScheduledTaskAction -Execute "$Dest\tappervpn-updater.exe"
$daily = New-ScheduledTaskTrigger -Daily -At ("03:{0:D2}" -f (Get-Random -Maximum 60))
$boot = New-ScheduledTaskTrigger -AtStartup
$boot.Delay = "PT1H"
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -RunOnlyIfNetworkAvailable `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 30)
Register-ScheduledTask -TaskName $task -Action $action -Trigger $daily, $boot -Settings $settings `
    -User "SYSTEM" -RunLevel Highest -Force | Out-Null

# A failed update *check* must not abort the install (a fresh local build
# reports 0.0.0-dev and the check can exit non-zero).
try { & "$Dest\tappervpn-updater.exe" -check } catch { Write-Host "update check skipped: $_" }

# Remote-control helper: runs in the interactive user session (the session-0
# service can't capture the screen or inject input), launched at each logon for
# any user. It attaches to the daemon and only acts after on-screen consent.
$helperAction = New-ScheduledTaskAction -Execute "$Dest\tappervpn-remote.exe"
$helperTrigger = New-ScheduledTaskTrigger -AtLogOn
$helperPrincipal = New-ScheduledTaskPrincipal -GroupId "S-1-5-32-545" -RunLevel Limited # Users
$helperSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Seconds 0)
Register-ScheduledTask -TaskName $helperTask -Action $helperAction -Trigger $helperTrigger `
    -Principal $helperPrincipal -Settings $helperSettings -Force | Out-Null

# The TapperVPN app (window + taskbar icon). Like the helper, it runs in the
# interactive user session, launched at each logon for any user.
$appAction = New-ScheduledTaskAction -Execute "$Dest\tappervpn-gui.exe"
$appTrigger = New-ScheduledTaskTrigger -AtLogOn
$appPrincipal = New-ScheduledTaskPrincipal -GroupId "S-1-5-32-545" -RunLevel Limited # Users
$appSettings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Seconds 0)
Register-ScheduledTask -TaskName $appTask -Action $appAction -Trigger $appTrigger `
    -Principal $appPrincipal -Settings $appSettings -Force | Out-Null

Write-Host "TapperVPN is installed in $Dest and updates itself daily (task '$task')."
Write-Host "The remote-control helper (task '$helperTask') starts at logon; it asks for consent before any session."
Write-Host "The TapperVPN app (task '$appTask') starts at logon."
