#Requires -RunAsAdministrator
#Requires -Version 5.1

[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [ValidatePattern('^ssh-(ed25519|rsa)\s+[A-Za-z0-9+/=]+(?:\s+.*)?$')]
  [string]$PublicKey,

  [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$')]
  [string]$AccountName = 'jona-homelab-remote'
)

$ErrorActionPreference = 'Stop'
$programDataSsh = Join-Path $env:ProgramData 'ssh'
$sshdConfig = Join-Path $programDataSsh 'sshd_config'
$commandSource = Join-Path $PSScriptRoot 'jona-homelab-command.ps1'
$commandTarget = Join-Path $programDataSsh 'jona-homelab-command.ps1'
$authorizedKeys = Join-Path $programDataSsh 'jona-homelab-authorized-keys'
$blockStart = '# BEGIN JONA-HOMELAB RESTRICTED SHUTDOWN'
$blockEnd = '# END JONA-HOMELAB RESTRICTED SHUTDOWN'

if (-not (Test-Path -LiteralPath $commandSource)) {
  throw "Missing wrapper next to setup script: $commandSource"
}

$capability = Get-WindowsCapability -Online -Name 'OpenSSH.Server~~~~0.0.1.0'
if ($capability.State -ne 'Installed') {
  Add-WindowsCapability -Online -Name 'OpenSSH.Server~~~~0.0.1.0' | Out-Null
}

Set-Service -Name sshd -StartupType Automatic
Start-Service -Name sshd

$account = Get-LocalUser -Name $AccountName -ErrorAction SilentlyContinue
if (-not $account) {
  $bytes = New-Object byte[] 32
  $random = [Security.Cryptography.RandomNumberGenerator]::Create()
  $random.GetBytes($bytes)
  $random.Dispose()
  $passwordText = [Convert]::ToBase64String($bytes) + '!aA1'
  $password = ConvertTo-SecureString $passwordText -AsPlainText -Force
  New-LocalUser -Name $AccountName -Password $password -AccountNeverExpires -PasswordNeverExpires -UserMayNotChangePassword | Out-Null
}

$administrators = Get-LocalGroup -SID 'S-1-5-32-544'
if (-not (Get-LocalGroupMember -Group $administrators.Name -Member $AccountName -ErrorAction SilentlyContinue)) {
  Add-LocalGroupMember -Group $administrators.Name -Member $AccountName
}

Copy-Item -LiteralPath $commandSource -Destination $commandTarget -Force
Set-Content -LiteralPath $authorizedKeys -Value $PublicKey.Trim() -Encoding ascii
& icacls.exe $commandTarget /inheritance:r /grant '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
& icacls.exe $authorizedKeys /inheritance:r /grant '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null

$managedBlock = @"
$blockStart
Match User $AccountName
    AuthorizedKeysFile __PROGRAMDATA__/ssh/jona-homelab-authorized-keys
    AuthenticationMethods publickey
    PasswordAuthentication no
    PermitTTY no
    AllowTcpForwarding no
    ForceCommand powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:/ProgramData/ssh/jona-homelab-command.ps1
$blockEnd
"@

$config = Get-Content -Raw -LiteralPath $sshdConfig
$escapedStart = [regex]::Escape($blockStart)
$escapedEnd = [regex]::Escape($blockEnd)
$config = [regex]::Replace($config, "(?ms)^$escapedStart.*?^$escapedEnd\s*", '')
$administratorMatch = [regex]::Match($config, '(?im)^Match\s+Group\s+administrators\s*$')
if ($administratorMatch.Success) {
  $config = $config.Insert($administratorMatch.Index, "$managedBlock`r`n")
}
else {
  $config = $config.TrimEnd() + "`r`n`r`n$managedBlock`r`n"
}

$backup = "$sshdConfig.jona-homelab-backup-$(Get-Date -Format 'yyyyMMddHHmmss')"
Copy-Item -LiteralPath $sshdConfig -Destination $backup
Set-Content -LiteralPath $sshdConfig -Value $config -Encoding ascii

$sshd = Join-Path $env:SystemRoot 'System32\OpenSSH\sshd.exe'
& $sshd -t
if ($LASTEXITCODE -ne 0) {
  Copy-Item -LiteralPath $backup -Destination $sshdConfig -Force
  throw 'Invalid sshd_config; original configuration restored.'
}

Restart-Service -Name sshd
Write-Output "Restricted SSH shutdown account configured: $AccountName"
Write-Output 'Verify this computer SSH host-key fingerprint before adding it to the homelab known_hosts file.'
