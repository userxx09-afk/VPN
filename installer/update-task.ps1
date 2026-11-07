# Adds or removes the "TapperVPN Update" scheduled task. The Windows
# installer (installer/tappervpn.iss) runs this from the install folder:
#
#   update-task.ps1 -Install      # daily signed-update check as SYSTEM
#   update-task.ps1 -Uninstall
param([switch]$Install, [switch]$Uninstall)
$ErrorActionPreference = "Stop"
$task = "TapperVPN Update"

if ($Uninstall) {
    Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
    return
}

# Daily at a random minute after 3 AM, and an hour after startup for PCs
# that are off at night.
$updater = Join-Path $PSScriptRoot "tappervpn-updater.exe"
$action = New-ScheduledTaskAction -Execute $updater
$daily = New-ScheduledTaskTrigger -Daily -At ("03:{0:D2}" -f (Get-Random -Maximum 60))
$boot = New-ScheduledTaskTrigger -AtStartup
$boot.Delay = "PT1H"
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -RunOnlyIfNetworkAvailable `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 30)
Register-ScheduledTask -TaskName $task -Action $action -Trigger $daily, $boot -Settings $settings `
    -User "SYSTEM" -RunLevel Highest -Force | Out-Null
