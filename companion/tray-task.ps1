#Requires -Version 5.1
[CmdletBinding()]
param(
  [string]$ExecutablePath = (Join-Path $PSScriptRoot 'JonaHomelab.Companion.exe'),
  [switch]$DefinitionOnly,
  [switch]$OnlyIfPresent
)
$ErrorActionPreference = 'Stop'
if ($OnlyIfPresent -and -not $DefinitionOnly -and -not (Get-ScheduledTask -TaskName 'JonaHomelabCompanionTray' -ErrorAction SilentlyContinue)) { return }
$action = New-ScheduledTaskAction -Execute ([IO.Path]::GetFullPath($ExecutablePath)) -Argument '--tray'
$trigger = New-ScheduledTaskTrigger -AtLogOn
$trigger.Delay = 'PT10S'
$principal = New-ScheduledTaskPrincipal -GroupId 'S-1-5-4' -RunLevel Limited
# Tray processes live for the whole interactive session, including on battery.
# Parallel permits separate sessions; the executable prevents duplicates per session.
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances Parallel
$task = New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings
if ($DefinitionOnly) { return $task }
Register-ScheduledTask -TaskName 'JonaHomelabCompanionTray' -InputObject $task -Force | Out-Null
