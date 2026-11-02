# Joins this machine to the mesh using the saved pre-auth key.
# Run (as Administrator) after start-node.ps1 is running in another window.
param(
    [string]$LoginServer = "http://192.168.5.207:8080",
    [switch]$Gateway   # advertise this node as a gateway (exit node)
)
$root = Split-Path $PSScriptRoot -Parent
$key = (Get-Content "$root\server\data\preauthkey.json" -Raw | ConvertFrom-Json).key

$upArgs = @("up", "--login-server=$LoginServer", "--auth-key=$key", "--unattended")
if ($Gateway) { $upArgs += "--advertise-exit-node" }
& "$root\bin\tailscale.exe" @upArgs
& "$root\bin\tailscale.exe" status
