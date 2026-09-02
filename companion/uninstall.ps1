#Requires -RunAsAdministrator
#Requires -Version 5.1

[CmdletBinding()]
param(
  [string]$InstallRoot = (Join-Path ${env:ProgramFiles} 'Jona Homelab Companion'),
  [switch]$PurgeData
)

$ErrorActionPreference = 'Stop'
Stop-Service -Name 'JonaHomelabCompanion' -Force -ErrorAction SilentlyContinue
& sc.exe delete JonaHomelabCompanion | Out-Null
& schtasks.exe /Delete /TN 'JonaHomelabCompanionTray' /F | Out-Null
Get-NetFirewallRule -DisplayName 'Jona Homelab Companion' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath $InstallRoot) { Remove-Item -LiteralPath $InstallRoot -Recurse -Force }
if ($PurgeData) {
  $data = Join-Path ${env:ProgramData} 'JonaHomelabCompanion'
  if (Test-Path -LiteralPath $data) { Remove-Item -LiteralPath $data -Recurse -Force }
}
Write-Output 'Jona Homelab Companion uninstalled. Configuration was preserved unless -PurgeData was supplied.'
