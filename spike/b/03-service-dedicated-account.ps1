# Spike B step 3: dedicated service account, with the distro imported AS that
# account.
#
# This is the pattern PLAN §09 proposes, and the one most likely to work: if WSL
# registration is per-user, then the account that runs the service must be the
# account that owns the distro. Importing as another user is done through a
# scheduled task, which is the only built-in way to run a command as a local
# account non-interactively.
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$svc = 'HawserSpikeB'
$user = 'hawser-svc'
$distro = 'hawser-spike-b'
$exe = Join-Path $here 'agent\probe.exe'

if (-not (Test-Path $exe)) { throw "run 01-preflight.ps1 first (probe.exe missing)" }

# Generated locally, used only for these API calls, never printed or written to
# disk. A real installer would use a virtual service account or a managed one.
function New-RandomPassword {
    param([int]$Length = 24)
    # Not System.Web.Security.Membership: that is .NET Framework only, so it is
    # absent under PowerShell 7 (.NET Core). RandomNumberGenerator::Create()
    # exists on both, so this works in 5.1 and 7 alike.
    #
    # Symbols are restricted to ones that survive sc.exe and schtasks argument
    # parsing unscathed - no quotes, ampersands, carets or percent signs.
    $sets = @(
        'ABCDEFGHIJKLMNPQRSTUVWXYZ',
        'abcdefghijkmnopqrstuvwxyz',
        '23456789',
        '!#$*+-=?@_'
    )
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $bytes = [byte[]]::new(4)
        $pick = {
            param($chars)
            $rng.GetBytes($bytes)
            $n = [BitConverter]::ToUInt32($bytes, 0)
            $chars[[int]($n % [uint32]$chars.Length)]
        }
        # One character from each set first, so Windows password-complexity
        # policy is satisfied regardless of what the rest happens to draw.
        $chars = foreach ($s in $sets) { & $pick $s }
        $all = -join $sets
        $chars += 1..($Length - $sets.Count) | ForEach-Object { & $pick $all }
        # Shuffle, so the guaranteed characters are not always in front.
        -join ($chars | Sort-Object { & $pick '0123456789' })
    } finally {
        $rng.Dispose()
    }
}

$pw = New-RandomPassword -Length 24
$sec = ConvertTo-SecureString $pw -AsPlainText -Force

if (-not (Get-LocalUser $user -ErrorAction SilentlyContinue)) {
    Write-Host "== creating local account $user ==" -ForegroundColor Cyan
    New-LocalUser -Name $user -Password $sec -PasswordNeverExpires `
        -AccountNeverExpires -Description "Hawser Spike B service account" | Out-Null
    # Not added to any group beyond Users: least privilege is part of the test.
} else {
    Write-Host "== resetting password for existing $user ==" -ForegroundColor Yellow
    Set-LocalUser -Name $user -Password $sec
}

function Grant-ServiceLogonRight([string]$account) {
    # No PowerShell cmdlet exposes user rights, so secedit is the built-in path.
    $tmp = Join-Path $env:TEMP "secpol-$PID"
    New-Item -ItemType Directory -Force $tmp | Out-Null
    $inf = Join-Path $tmp 'export.inf'
    $db = Join-Path $tmp 'secedit.sdb'
    secedit /export /cfg $inf /quiet

    $sid = (Get-LocalUser $account).SID.Value
    $content = Get-Content $inf
    $line = $content | Where-Object { $_ -match '^SeServiceLogonRight' }
    if ($line) {
        if ($line -match [regex]::Escape($sid)) {
            Write-Host "  already granted" -ForegroundColor DarkGray
            Remove-Item $tmp -Recurse -Force
            return
        }
        $new = "$line,*$sid"
        $content = $content -replace [regex]::Escape($line), $new
    } else {
        $content = $content -replace '(\[Privilege Rights\])', "`$1`r`nSeServiceLogonRight = *$sid"
    }
    $applied = Join-Path $tmp 'apply.inf'
    $content | Set-Content $applied -Encoding Unicode
    secedit /configure /db $db /cfg $applied /areas USER_RIGHTS /quiet
    Remove-Item $tmp -Recurse -Force
    Write-Host "  granted SeServiceLogonRight" -ForegroundColor Green
}

Write-Host "== granting 'log on as a service' ==" -ForegroundColor Cyan
Grant-ServiceLogonRight $user

# The distro must be registered under the service account's own hive, so the
# import runs as that account via a one-shot scheduled task.
Write-Host "`n== importing $distro as $user (via scheduled task) ==" -ForegroundColor Cyan
$dl = Join-Path (Split-Path $here -Parent) 'a\dl'
$alpine = Join-Path $dl 'alpine-minirootfs-3.24.1-x86_64.tar.gz'
$docker = Join-Path $dl 'docker-29.7.2.tgz'
$setupSh = Join-Path (Split-Path $here -Parent) 'a\03-setup.sh'
$svcDistro = "$distro-svc"
$vhd = "C:\ProgramData\hawser-spike-b\$svcDistro"

# The account needs to reach these files, so stage them somewhere readable and
# grant the account access to its own VHDX directory.
New-Item -ItemType Directory -Force $vhd | Out-Null
icacls "C:\ProgramData\hawser-spike-b" /grant "${user}:(OI)(CI)F" /T | Out-Null

$importScript = Join-Path $env:TEMP "hawser-spike-import-$PID.ps1"
@"
`$ErrorActionPreference = 'Continue'
`$log = 'C:\ProgramData\hawser-spike-b\import.log'
"whoami: `$(whoami)" | Out-File `$log -Append
"session: `$((Get-Process -Id `$PID).SessionId)" | Out-File `$log -Append
"distros before:" | Out-File `$log -Append
(wsl --list --verbose) -replace "``0","" | Out-File `$log -Append
wsl --import $svcDistro '$vhd' '$alpine' --version 2 2>&1 | Out-File `$log -Append
"import exit: `$LASTEXITCODE" | Out-File `$log -Append
`$p = (wsl -d $svcDistro --exec wslpath -a '$setupSh')
`$t = (wsl -d $svcDistro --exec wslpath -a '$docker')
wsl -d $svcDistro -u root --exec sh `$p.Trim() `$t.Trim() 2>&1 | Out-File `$log -Append
"setup exit: `$LASTEXITCODE" | Out-File `$log -Append
"distros after:" | Out-File `$log -Append
(wsl --list --verbose) -replace "``0","" | Out-File `$log -Append
"@ | Set-Content $importScript -Encoding UTF8

$taskName = 'HawserSpikeBImport'
schtasks /Delete /TN $taskName /F 2>$null | Out-Null
schtasks /Create /TN $taskName /TR "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$importScript`"" `
    /SC ONCE /ST 23:59 /RU $user /RP $pw /RL LIMITED /F | Out-Null
if ($LASTEXITCODE -ne 0) { throw "schtasks create failed (account may lack batch logon right)" }
schtasks /Run /TN $taskName | Out-Null

Write-Host "waiting for import (up to 3 min)..." -ForegroundColor DarkGray
$deadline = (Get-Date).AddMinutes(3)
do {
    Start-Sleep -Seconds 5
    $state = (schtasks /Query /TN $taskName /FO LIST | Select-String 'Status:').ToString()
} while ($state -match 'Running' -and (Get-Date) -lt $deadline)

Write-Host "`n== import log (as $user) ==" -ForegroundColor Cyan
Get-Content 'C:\ProgramData\hawser-spike-b\import.log' -ErrorAction SilentlyContinue | Select-Object -Last 30

Write-Host "`n== creating $svc under $user ==" -ForegroundColor Cyan
if (Get-Service $svc -ErrorAction SilentlyContinue) {
    sc.exe stop $svc | Out-Null; Start-Sleep -Seconds 2; sc.exe delete $svc | Out-Null; Start-Sleep -Seconds 1
}
# HAWSER_SPIKE_DISTRO defaults to hawser-spike-b, but the service account owns
# hawser-spike-b-svc, so point the service at that one via the registry.
sc.exe create $svc binPath= "`"$exe`"" start= demand obj= ".\$user" password= "$pw" DisplayName= "Hawser Spike B (dedicated account)" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "sc create failed" }
$key = "HKLM:\SYSTEM\CurrentControlSet\Services\$svc"
Set-ItemProperty $key -Name Environment -Value @("HAWSER_SPIKE_DISTRO=$svcDistro") -Type MultiString

$pw = $null  # drop it from this session

Write-Host "`n== starting ==" -ForegroundColor Cyan
sc.exe start $svc
Start-Sleep -Seconds 30
sc.exe query $svc | Select-String "STATE|WIN32_EXIT_CODE"

Write-Host "`n== probe log ==" -ForegroundColor Cyan
& (Join-Path $here 'show-log.ps1') -Mode service

Write-Host @"

If 'distro-visible' and 'socket-up' are both ok=true here, the dedicated-account
pattern works and the CI claim holds. Then run 04 to prove it survives with
nobody logged in - that is the part that actually matters.

Next: .\04-nologin-window.ps1
"@ -ForegroundColor Green
