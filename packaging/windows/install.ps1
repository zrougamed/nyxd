<#
.SYNOPSIS
  Installs nyx on Windows: nyx.exe, the nyx WSL2 distro that runs nyxd, and
  a logon task that starts nyxd, so containers work after every sign-in.

.DESCRIPTION
  No administrator rights are needed once WSL is enabled. Everything is per
  user: nyx.exe goes to %LOCALAPPDATA%\Programs\nyx (added to your PATH), the
  WSL distro is imported by `nyx machine init`, and a scheduled task named
  "nyx machine" runs `nyx machine start` at logon.

    irm https://github.com/zrougamed/nyxd/releases/latest/download/install.ps1 | iex

  With options, download it first:
    .\install.ps1 -Version v0.10.0 -NoAutostart
    .\install.ps1 -Uninstall [-Purge]

.PARAMETER Version
  Release tag to install (default: the latest).
.PARAMETER InstallDir
  Where nyx.exe goes (default: %LOCALAPPDATA%\Programs\nyx).
.PARAMETER NoMachine
  Only install nyx.exe; do not import the WSL distro or start nyxd.
.PARAMETER NoAutostart
  Do not register the logon task.
.PARAMETER Uninstall
  Remove the logon task, nyx.exe and its PATH entry. The WSL distro (your
  images and containers) is kept unless -Purge is given.
.PARAMETER Purge
  With -Uninstall: also unregister the nyx WSL distro, deleting its data.
#>
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\nyx"),
    [switch]$NoMachine,
    [switch]$NoAutostart,
    [switch]$Uninstall,
    [switch]$Purge
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue" # Invoke-WebRequest is far faster without it
$Repo = "zrougamed/nyxd"
$TaskName = "nyx machine"
$Nyx = Join-Path $InstallDir "nyx.exe"

function Say([string]$msg) { Write-Host "nyx-install: $msg" }

function Get-Arch {
    $a = $env:PROCESSOR_ARCHITEW6432
    if (-not $a) { $a = $env:PROCESSOR_ARCHITECTURE }
    switch ($a) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "unsupported CPU architecture $a (release builds: x64, ARM64)" }
    }
}

function Get-BaseUrl([string]$tag) {
    if ($env:NYXD_RELEASE_URL) { return $env:NYXD_RELEASE_URL.TrimEnd("/") }
    return "https://github.com/$Repo/releases/download/$tag"
}

function Resolve-Version {
    if ($Version) { return $Version }
    $rel = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "nyx-install" }
    return $rel.tag_name
}

function Get-Verified([string]$base, [string]$name, [string]$dest) {
    $sums = Join-Path $env:TEMP "nyx-checksums.txt"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
    Say "downloading $name"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile $dest
    $line = Get-Content $sums | Where-Object { ($_ -split "\s+")[1] -eq $name } | Select-Object -First 1
    if (-not $line) { throw "$name is not listed in checksums.txt" }
    $want = ($line -split "\s+")[0].ToLowerInvariant()
    $got = (Get-FileHash -Algorithm SHA256 -Path $dest).Hash.ToLowerInvariant()
    if ($got -ne $want) { Remove-Item $dest -Force; throw "checksum mismatch for $name" }
    Remove-Item $sums -Force
}

function Add-UserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @(); if ($p) { $parts = $p -split ";" | Where-Object { $_ } }
    if ($parts -notcontains $dir) {
        [Environment]::SetEnvironmentVariable("Path", (($parts + $dir) -join ";"), "User")
        Say "added $dir to your PATH (open a new terminal to use it)"
    }
    if (($env:Path -split ";") -notcontains $dir) { $env:Path = "$env:Path;$dir" }
}

function Remove-UserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $p) { return }
    $kept = $p -split ";" | Where-Object { $_ -and $_ -ne $dir }
    [Environment]::SetEnvironmentVariable("Path", ($kept -join ";"), "User")
}

function Test-Wsl {
    $wsl = Join-Path $env:SystemRoot "System32\wsl.exe"
    if (-not (Test-Path $wsl)) { return $false }
    & $wsl --status *> $null
    return ($LASTEXITCODE -eq 0)
}

function Update-DistroNyxd([string]$base, [string]$arch) {
    $tmp = Join-Path $env:TEMP "nyxd-linux-$arch"
    Get-Verified $base "nyxd-linux-$arch" $tmp
    & $Nyx machine stop *> $null
    $src = (& wsl.exe -d nyx -u root --exec wslpath -a $tmp | Out-String).Trim()
    & wsl.exe -d nyx -u root --exec install -m 0755 $src /usr/local/bin/nyxd
    if ($LASTEXITCODE -ne 0) { throw "could not update nyxd in the nyx distro" }
    Remove-Item $tmp -Force
    Say "updated nyxd in the nyx WSL distro"
}

function Register-Autostart {
    # Hidden PowerShell wrapper: a console program started at logon would
    # flash a window.
    $arg = "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"& '$Nyx' machine start`""
    $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $arg
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Minutes 5)
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
    try {
        Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Description "Starts nyxd in the nyx WSL2 distro at logon" -Force | Out-Null
        Say "nyxd starts at every logon (scheduled task `"$TaskName`")"
    } catch {
        # Some policies forbid user tasks: fall back to the Startup folder.
        $startup = [Environment]::GetFolderPath("Startup")
        $cmd = Join-Path $startup "nyx machine start.cmd"
        Set-Content -Path $cmd -Encoding ASCII -Value "@start `"`" /min powershell.exe $arg"
        Say "nyxd starts at every logon ($cmd)"
    }
}

function Unregister-Autostart {
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    }
    $cmd = Join-Path ([Environment]::GetFolderPath("Startup")) "nyx machine start.cmd"
    if (Test-Path $cmd) { Remove-Item $cmd -Force }
}

if ($Uninstall) {
    Unregister-Autostart
    if (Test-Path $Nyx) {
        if ($Purge) { & $Nyx machine rm } else { & $Nyx machine stop *> $null }
    }
    if (Test-Path $InstallDir) { Remove-Item $InstallDir -Recurse -Force }
    Remove-UserPath $InstallDir
    if ($Purge) { Say "removed nyx and the nyx WSL distro" }
    else { Say "removed nyx; the WSL distro `"nyx`" (images, containers) is kept: -Purge deletes it" }
    return
}

$arch = Get-Arch
$tag = Resolve-Version
$base = Get-BaseUrl $tag
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$tmp = Join-Path $env:TEMP "nyx-$arch.exe"
Get-Verified $base "nyx-windows-$arch.exe" $tmp
if (Test-Path $Nyx) { & $Nyx machine stop *> $null } # the running nyx.exe may be replaced now
Move-Item -Force $tmp $Nyx
Add-UserPath $InstallDir
Say "installed $Nyx ($tag)"

if ($NoMachine) { return }

if (-not (Test-Wsl)) {
    Say "WSL is not enabled yet. In an administrator terminal run:"
    Say "    wsl --install --no-distribution"
    Say "then restart Windows and run this installer again."
    return
}

$distros = (& wsl.exe --list --quiet) -replace "`0", "" | ForEach-Object { $_.Trim() }
if ($distros -notcontains "nyx") {
    Say "importing the nyx WSL distro"
    & $Nyx machine init
    if ($LASTEXITCODE -ne 0) { throw "nyx machine init failed" }
} else {
    # Upgrade: keep the distro (images, containers) and replace nyxd in it.
    Update-DistroNyxd $base $arch
}
& $Nyx machine start
if ($LASTEXITCODE -ne 0) { throw "nyx machine start failed; see: nyx machine logs" }
if (-not $NoAutostart) { Register-Autostart }
Say "done: try `"nyx run alpine echo hello`" in a new terminal"
