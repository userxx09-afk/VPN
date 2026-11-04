# Runs the TapperVPN node daemon in the foreground. Must be run as Administrator,
# because it creates the Wintun tunnel adapter.
#
#   -Policy <file>  per-app routing policy (JSON) to use when the control server
#                   sends none, e.g. examples\approuting-chrome-only.json
#   -LogFlows       log the routing decision for every new connection
param(
    [string]$Policy,
    [switch]$LogFlows
)
$root = Split-Path $PSScriptRoot -Parent
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { Write-Error "Run this from an Administrator PowerShell."; exit 1 }

if ($Policy) {
    $env:TS_DEBUG_APPROUTING_FILE = (Resolve-Path $Policy).Path
    Write-Host "App routing policy: $env:TS_DEBUG_APPROUTING_FILE"
}
if ($LogFlows) { $env:TS_DEBUG_APPROUTING = "true" }

New-Item -ItemType Directory -Force "$root\node\state" | Out-Null
& "$root\bin\tappervpnd.exe" --statedir="$root\node\state"
