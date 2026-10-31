# Starts the Headscale control server (no admin needed).
# Windows may ask to allow it through the firewall the first time: allow on Private networks.
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
& "$root\bin\headscale.exe" serve -c "$root\server\config.yaml"
