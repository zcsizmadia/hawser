# Spike B cleanup: remove the service, task, account, distros, and logs.
$ErrorActionPreference = 'Continue'
$here = $PSScriptRoot

Write-Host "== service ==" -ForegroundColor Cyan
sc.exe stop HawserSpikeB 2>$null | Out-Null
Start-Sleep -Seconds 2
sc.exe delete HawserSpikeB 2>$null | Out-Null

Write-Host "== scheduled task ==" -ForegroundColor Cyan
schtasks /Delete /TN HawserSpikeBImport /F 2>$null | Out-Null

Write-Host "== distros ==" -ForegroundColor Cyan
foreach ($d in 'hawser-spike-b', 'hawser-spike-b-svc') {
    wsl --terminate $d 2>$null | Out-Null
    wsl --unregister $d 2>$null | Out-Null
}
# The service account owns its own registration, which the interactive user
# cannot unregister; removing the account and its profile takes it with them.

Write-Host "== local account ==" -ForegroundColor Cyan
if (Get-LocalUser hawser-svc -ErrorAction SilentlyContinue) {
    Remove-LocalUser -Name hawser-svc
    Write-Host "  removed hawser-svc" -ForegroundColor Green
}
# Its profile directory, if a logon ever created one.
Get-CimInstance Win32_UserProfile -ErrorAction SilentlyContinue |
    Where-Object { $_.LocalPath -like '*hawser-svc*' } |
    ForEach-Object { Remove-CimInstance $_ -ErrorAction SilentlyContinue }

Write-Host "== files ==" -ForegroundColor Cyan
Remove-Item -Recurse -Force 'C:\ProgramData\hawser-spike-b' -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force (Join-Path $env:LOCALAPPDATA 'hawser-spike-b') -ErrorAction SilentlyContinue
Remove-Item -Force (Join-Path $here 'agent\probe.exe') -ErrorAction SilentlyContinue
Get-ChildItem $env:TEMP -Filter 'hawser-spike-import-*.ps1' -ErrorAction SilentlyContinue | Remove-Item -Force

Write-Host "`nSpike B cleanup done. Verify nothing is left:" -ForegroundColor Green
Write-Host "  distros:" -ForegroundColor DarkGray
(wsl --list --quiet) -replace "`0","" | Where-Object { $_ -match '\S' } | ForEach-Object { "    $_" }
Write-Host "  SeServiceLogonRight may still list a stale SID; harmless once the account is gone." -ForegroundColor DarkGray
