#Requires -Version 5.1
[CmdletBinding()]
param(
  [string]$OutputDirectory = [Environment]::GetFolderPath('Desktop'),
  [string]$DataDirectory = (Join-Path $env:ProgramData 'JonaHomelabCompanion')
)
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$report = Join-Path ([IO.Path]::GetFullPath($OutputDirectory)) "companion-diagnostico-$stamp-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Path $report | Out-Null
function Save-Section([string]$Name, [scriptblock]$Read) {
  try { & $Read | Out-String -Width 240 | Set-Content -LiteralPath (Join-Path $report "$Name.txt") -Encoding UTF8 }
  catch { $_.Exception.Message | Set-Content -LiteralPath (Join-Path $report "$Name.txt") -Encoding UTF8 }
}
Save-Section 'service' {
  Get-CimInstance Win32_Service -Filter "Name='JonaHomelabCompanion'" | Select-Object Name, State, StartMode, StartName, PathName, ExitCode, ProcessId
  & sc.exe queryex JonaHomelabCompanion
}
Save-Section 'health' { Invoke-RestMethod 'http://127.0.0.1:47654/health' -TimeoutSec 3 | ConvertTo-Json }
Save-Section 'power-states' { & powercfg.exe /a }
Save-Section 'network' {
  Get-NetConnectionProfile | Select-Object InterfaceAlias, NetworkCategory, IPv4Connectivity
  Get-NetIPAddress -AddressFamily IPv4 | Select-Object InterfaceAlias, IPAddress, PrefixLength
  Get-NetTCPConnection -LocalPort 47654 -State Listen -ErrorAction SilentlyContinue | Select-Object LocalAddress, LocalPort, OwningProcess
}
Save-Section 'firewall' {
  $rules = Get-NetFirewallRule -DisplayName 'Jona Homelab Companion' -ErrorAction Stop
  $rules | Select-Object DisplayName, Enabled, Profile, Direction, Action
  $rules | Get-NetFirewallAddressFilter | Select-Object RemoteAddress
  $rules | Get-NetFirewallPortFilter | Select-Object Protocol, LocalPort
}
Save-Section 'windows-events' {
  Get-WinEvent -FilterHashtable @{ LogName = 'System'; StartTime = (Get-Date).AddDays(-3) } -MaxEvents 3000 -ErrorAction Stop |
    Where-Object { $_.ProviderName -eq 'Service Control Manager' -and $_.Message -match 'Jona.?Homelab.?Companion|Jona Homelab Companion' } |
    Select-Object -First 50 TimeCreated, Id, LevelDisplayName, Message
}
Save-Section 'configuration-summary' {
  $file = Join-Path $DataDirectory 'config.json'
  if (Test-Path -LiteralPath $file) {
    $config = Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
    [pscustomobject]@{ Port = $config.port; DPAPIScope = $config.dpapiScope; LastServerCall = $config.lastServerCall; HasSecret = [bool]$config.encryptedSecret }
  } else { 'No configuration file.' }
}
# Strict allowlist: never copy config.json, pairing-code.txt, headers or request bodies.
foreach ($name in @('service.log', 'service.log.1', 'install.log', 'install.log.1', 'crash.log', 'crash.log.1', 'update-status.json')) {
  $source = Join-Path $DataDirectory $name
  Save-Section $name {
    if (Test-Path -LiteralPath $source) { Get-Content -LiteralPath $source -Tail 5000 }
    else { 'Log not present.' }
  }
}
@"
Diagnostico Jona Homelab Companion, UTC $([DateTime]::UtcNow.ToString('o')).
Incluye IP locales, rutas, estado del servicio, firewall, eventos y trazas.
No incluye el codigo de emparejado ni config.json. No se ha enviado a nadie.
Si alguna seccion indica acceso denegado, repite como administrador.
"@ | Set-Content -LiteralPath (Join-Path $report 'LEEME.txt') -Encoding UTF8
$zip = "$report.zip"
Compress-Archive -Path (Join-Path $report '*') -DestinationPath $zip
Write-Output "Diagnostico listo: $zip"
