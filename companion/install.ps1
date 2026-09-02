#Requires -RunAsAdministrator
#Requires -Version 5.1

[CmdletBinding()]
param(
  [string]$InstallRoot = (Join-Path ${env:ProgramFiles} 'JonaHomelabCompanion')
)

$ErrorActionPreference = 'Stop'
$packageRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$binary = Join-Path $packageRoot 'JonaHomelab.Companion.exe'
$versionFile = Join-Path $packageRoot 'RELEASE_VERSION'
if (-not (Test-Path -LiteralPath $binary) -or -not (Test-Path -LiteralPath $versionFile)) { throw 'Run install.ps1 from an extracted Companion release.' }
$version = (Get-Content -Raw -LiteralPath $versionFile).Trim()
if ($version -notmatch '^main-[0-9a-f]{12}$') { throw 'Invalid RELEASE_VERSION.' }

$releases = Join-Path $InstallRoot 'releases'
$target = Join-Path $releases $version
$current = Join-Path $InstallRoot 'current'
New-Item -ItemType Directory -Force -Path $releases | Out-Null
if (Test-Path -LiteralPath $target) { Remove-Item -LiteralPath $target -Recurse -Force }
New-Item -ItemType Directory -Force -Path $target | Out-Null
Copy-Item -Path (Join-Path $packageRoot '*') -Destination $target -Recurse -Force
if (Test-Path -LiteralPath $current) { Remove-Item -LiteralPath $current -Force }
New-Item -ItemType Junction -Path $current -Target $target | Out-Null

$servicePath = Join-Path $current 'JonaHomelab.Companion.exe'
$service = Get-Service -Name 'JonaHomelabCompanion' -ErrorAction SilentlyContinue
if ($service) {
  if ($service.Status -ne 'Stopped') { Stop-Service -Name 'JonaHomelabCompanion' -Force -ErrorAction SilentlyContinue }
  & sc.exe config JonaHomelabCompanion binPath= "`"$servicePath`" --service" start= delayed-auto | Out-Null
}
else {
  New-Service -Name 'JonaHomelabCompanion' -BinaryPathName "`"$servicePath`" --service" -DisplayName 'Jona Homelab Companion' -Description 'Authenticated LAN shutdown companion for Jona Homelab.' -StartupType Automatic | Out-Null
  & sc.exe config JonaHomelabCompanion start= delayed-auto | Out-Null
}
& sc.exe config JonaHomelabCompanion obj= LocalSystem password= "" | Out-Null

$data = Join-Path ${env:ProgramData} 'JonaHomelabCompanion'
New-Item -ItemType Directory -Force -Path $data | Out-Null
& icacls.exe $data /inheritance:r /grant '*S-1-5-18:(OI)(CI)(F)' '*S-1-5-32-544:(OI)(CI)(F)' | Out-Null
Get-NetFirewallRule -DisplayName 'Jona Homelab Companion' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
New-NetFirewallRule -DisplayName 'Jona Homelab Companion' -Direction Inbound -Protocol TCP -LocalPort 47654 -Profile Private -RemoteAddress LocalSubnet -Action Allow | Out-Null
$trayTaskName = 'JonaHomelabCompanionTray'
$trayAction = New-ScheduledTaskAction -Execute $servicePath -Argument '--tray'
$trayTrigger = New-ScheduledTaskTrigger -AtLogOn
$trayPrincipal = New-ScheduledTaskPrincipal -GroupId 'S-1-5-4' -RunLevel Limited
Register-ScheduledTask -TaskName $trayTaskName -Action $trayAction -Trigger $trayTrigger -Principal $trayPrincipal -Force | Out-Null
Start-Service -Name 'JonaHomelabCompanion'
Start-ScheduledTask -TaskName $trayTaskName
Write-Output "Installed Jona Homelab Companion $version"
Write-Output 'Open the tray app and copy its pairing code into the homelab device editor.'
