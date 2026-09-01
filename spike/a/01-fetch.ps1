# Spike A step 1: download + verify the Alpine rootfs, download the Docker static bundle.
$ErrorActionPreference = 'Stop'
$dl = Join-Path $PSScriptRoot 'dl'
New-Item -ItemType Directory -Force $dl | Out-Null

$alpine    = 'alpine-minirootfs-3.24.1-x86_64.tar.gz'
$alpineUrl = "https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/$alpine"
$docker    = 'docker-29.7.2.tgz'
$dockerUrl = "https://download.docker.com/linux/static/stable/x86_64/$docker"

Write-Host "Fetching $alpine ..."
Invoke-WebRequest $alpineUrl -OutFile (Join-Path $dl $alpine)
Invoke-WebRequest "$alpineUrl.sha256" -OutFile (Join-Path $dl "$alpine.sha256")
$expected = ((Get-Content (Join-Path $dl "$alpine.sha256")) -split '\s+')[0].ToLower()
$actual   = (Get-FileHash (Join-Path $dl $alpine) -Algorithm SHA256).Hash.ToLower()
if ($actual -ne $expected) { throw "Alpine checksum mismatch: expected $expected got $actual" }
Write-Host "  sha256 OK: $actual"

Write-Host "Fetching $docker ..."
Invoke-WebRequest $dockerUrl -OutFile (Join-Path $dl $docker)
# download.docker.com publishes no checksums for static bundles; record what we got.
$dockerHash = (Get-FileHash (Join-Path $dl $docker) -Algorithm SHA256).Hash.ToLower()
Write-Host "  sha256 (record in issue #2): $dockerHash"

Write-Host "Done. Next: .\02-import.ps1"
