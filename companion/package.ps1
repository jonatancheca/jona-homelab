#Requires -Version 5.1
[CmdletBinding()]
param([string]$Version = ('local-' + (Get-Date -Format 'yyyyMMddHHmmss')))
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^(main-[0-9a-f]{12}|local-[a-z0-9-]+)$') { throw 'Invalid package version.' }
$root = Join-Path (Split-Path $PSScriptRoot -Parent) 'artifacts'
$package = Join-Path $root "companion-$Version"
if (Test-Path -LiteralPath $package) { throw 'Package already exists. Use a new version.' }
New-Item -ItemType Directory -Path $package -Force | Out-Null
Push-Location $PSScriptRoot
$previousCGO = $env:CGO_ENABLED
try {
  $env:CGO_ENABLED = '0'
  go build -trimpath -ldflags '-s -w -H=windowsgui' -o (Join-Path $package 'JonaHomelab.Companion.exe') .
  if ($LASTEXITCODE -ne 0) { throw 'Companion compilation failed.' }
  Copy-Item -LiteralPath 'install.ps1', 'uninstall.ps1', 'diagnostics.ps1', 'README.md' -Destination $package
  Set-Content -LiteralPath (Join-Path $package 'RELEASE_VERSION') -Value $Version -NoNewline -Encoding Ascii
  $zip = Join-Path $root "jona-homelab-companion-$Version-win-x64.zip"
  Compress-Archive -Path (Join-Path $package '*') -DestinationPath $zip
  ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLower() + '  ' + (Split-Path $zip -Leaf)) | Set-Content -LiteralPath "$zip.sha256" -NoNewline -Encoding Ascii
  Write-Output $zip
} finally {
  $env:CGO_ENABLED = $previousCGO
  Pop-Location
}
