#Requires -Version 5.1

$ErrorActionPreference = 'Stop'

switch -CaseSensitive ($env:SSH_ORIGINAL_COMMAND) {
  'status' {
    Write-Output 'ready'
    exit 0
  }
  'shutdown-safe' {
    & "$env:SystemRoot\System32\shutdown.exe" /s /t 0
    exit $LASTEXITCODE
  }
  'shutdown-force' {
    & "$env:SystemRoot\System32\shutdown.exe" /s /t 0 /f
    exit $LASTEXITCODE
  }
  default {
    Write-Error 'Command not allowed.'
    exit 64
  }
}
