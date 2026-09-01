# Spike A cleanup: remove every trace of the spike.
$ErrorActionPreference = 'Continue'

wsl --terminate hawser-spike 2>$null
wsl --unregister hawser-spike 2>$null
Remove-Item -Recurse -Force (Join-Path $env:LOCALAPPDATA 'hawser-spike') -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force (Join-Path $PSScriptRoot 'dl') -ErrorAction SilentlyContinue
Remove-Item -Force (Join-Path $PSScriptRoot 'relay\relay.exe'), (Join-Path $PSScriptRoot 'relay\go.sum') -ErrorAction SilentlyContinue

Write-Host "hawser-spike distro unregistered, downloads and binaries removed."
