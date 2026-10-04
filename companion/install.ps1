#Requires -RunAsAdministrator
#Requires -Version 5.1

[CmdletBinding()]
param(
  [string]$InstallRoot = (Join-Path ${env:ProgramFiles} 'JonaHomelabCompanion'),
  [switch]$SkipTray
)

$ErrorActionPreference = 'Stop'
$packageRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$binary = Join-Path $packageRoot 'JonaHomelab.Companion.exe'
$versionFile = Join-Path $packageRoot 'RELEASE_VERSION'
if (-not (Test-Path -LiteralPath $binary) -or -not (Test-Path -LiteralPath $versionFile)) { throw 'Run install.ps1 from an extracted Companion release.' }
$version = (Get-Content -Raw -LiteralPath $versionFile).Trim()
if ($version -notmatch '^(main-[0-9a-f]{12}|local-[a-z0-9-]+)$') { throw 'Invalid RELEASE_VERSION.' }

function Invoke-Checked([string]$File, [string[]]$Arguments) {
  & $File @Arguments | Write-Output
  if ($LASTEXITCODE -ne 0) { throw "$File failed with exit code $LASTEXITCODE" }
}

$InstallRoot = [IO.Path]::GetFullPath($InstallRoot)
$data = Join-Path ${env:ProgramData} 'JonaHomelabCompanion'
New-Item -ItemType Directory -Force -Path $data | Out-Null
Invoke-Checked 'icacls.exe' @($data, '/inheritance:r', '/grant:r', '*S-1-5-18:(OI)(CI)(F)', '*S-1-5-32-544:(OI)(CI)(F)')
$installLog = Join-Path $data 'install.log'
if (Test-Path -LiteralPath $installLog) { Move-Item -LiteralPath $installLog -Destination "$installLog.1" -Force }
Start-Transcript -LiteralPath $installLog -Force | Out-Null
try {

$releases = Join-Path $InstallRoot 'releases'
$target = Join-Path $releases $version
$current = Join-Path $InstallRoot 'current'
Stop-ScheduledTask -TaskName 'JonaHomelabCompanionTray' -ErrorAction SilentlyContinue
if ($packageRoot.StartsWith($InstallRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Extract the package outside the installation directory.' }
if (-not $target.StartsWith($releases + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe release directory.' }
if (Test-Path -LiteralPath $current) {
  $link = Get-Item -LiteralPath $current -Force
  if ($link.LinkType -ne 'Junction') { throw 'current must be a junction; refusing to replace a directory.' }
}
$existing = Get-Service -Name 'JonaHomelabCompanion' -ErrorAction SilentlyContinue
if ($existing) {
  Stop-Service -Name $existing.Name -Force
  $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
# Only stop Companion tray processes belonging to this installation.
Get-Process -Name 'JonaHomelab.Companion' -ErrorAction SilentlyContinue |
  Where-Object { $_.Path -and $_.Path.StartsWith($InstallRoot + '\', [StringComparison]::OrdinalIgnoreCase) } |
  Stop-Process -Force
New-Item -ItemType Directory -Force -Path $releases | Out-Null
New-Item -ItemType Directory -Force -Path $target | Out-Null
Copy-Item -Path (Join-Path $packageRoot '*') -Destination $target -Recurse -Force
if (Test-Path -LiteralPath $current) { Remove-Item -LiteralPath $current -Force }
New-Item -ItemType Junction -Path $current -Target $target | Out-Null

$servicePath = Join-Path $current 'JonaHomelab.Companion.exe'
$service = Get-Service -Name 'JonaHomelabCompanion' -ErrorAction SilentlyContinue
if ($service) {
  if ($service.Status -ne 'Stopped') { Stop-Service -Name 'JonaHomelabCompanion' -Force -ErrorAction SilentlyContinue }
  # CIM preserves the quoted executable path on both Windows PowerShell 5.1 and PowerShell 7.
  $installedService = Get-CimInstance Win32_Service -Filter "Name='JonaHomelabCompanion'"
  $changed = Invoke-CimMethod -InputObject $installedService -MethodName Change -Arguments @{ PathName = "`"$servicePath`" --service" }
  if ($changed.ReturnValue -ne 0) { throw "Service path update failed: $($changed.ReturnValue)" }
  Invoke-Checked 'sc.exe' @('config', 'JonaHomelabCompanion', 'start=', 'delayed-auto')
}
else {
  New-Service -Name 'JonaHomelabCompanion' -BinaryPathName "`"$servicePath`" --service" -DisplayName 'Jona Homelab Companion' -Description 'Authenticated LAN shutdown companion for Jona Homelab.' -StartupType Automatic | Out-Null
  Invoke-Checked 'sc.exe' @('config', 'JonaHomelabCompanion', 'start=', 'delayed-auto')
}
Invoke-Checked 'sc.exe' @('config', 'JonaHomelabCompanion', 'obj=', 'LocalSystem')
Invoke-Checked 'sc.exe' @('failure', 'JonaHomelabCompanion', 'reset=', '86400', 'actions=', 'restart/5000/restart/15000/none/0')
Invoke-Checked 'sc.exe' @('failureflag', 'JonaHomelabCompanion', '1')

Get-NetFirewallRule -DisplayName 'Jona Homelab Companion' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
New-NetFirewallRule -DisplayName 'Jona Homelab Companion' -Direction Inbound -Protocol TCP -LocalPort 47654 -Profile Private -RemoteAddress LocalSubnet -Action Allow | Out-Null
Start-Service -Name 'JonaHomelabCompanion'
$healthy = $false
$deadline = (Get-Date).AddSeconds(30)
while ((Get-Date) -lt $deadline) {
  try {
    $health = Invoke-RestMethod 'http://127.0.0.1:47654/health' -TimeoutSec 2
    if ($health.status -eq 'ok' -and $health.version -eq $version -and -not $health.simulated) { $healthy = $true; break }
  } catch {}
  Start-Sleep -Milliseconds 500
}
if (-not $healthy) { throw 'El servicio no supera la comprobacion HTTP. Consulta service.log y exporta el diagnostico.' }
if (-not $SkipTray) {
try {
$trayTaskName = 'JonaHomelabCompanionTray'
& (Join-Path $current 'tray-task.ps1') -ExecutablePath $servicePath
Start-ScheduledTask -TaskName $trayTaskName
} catch { Write-Warning "Servicio listo, pero la bandeja no arranco: $($_.Exception.Message)" }
}
Write-Output "Companion $version instalado. Servicio y API comprobados."
Write-Output 'Copia el codigo de la bandeja en el dispositivo de Jona Homelab.'
Write-Output 'Si hay errores: ejecuta diagnostics.ps1 y comparte el ZIP generado.'
} catch {
  Write-Warning $_.Exception.Message
  Write-Warning "Trazas: $data. Ejecuta diagnostics.ps1 incluso si el servicio no arranca."
  throw
} finally { Stop-Transcript | Out-Null }
