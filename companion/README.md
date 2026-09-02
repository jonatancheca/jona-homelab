# Jona Homelab Companion

Windows 11 x64 companion for authenticated LAN status and shutdown commands. Package is self-contained and does not require .NET. It is not Authenticode-signed; Windows SmartScreen may show a warning. Verify the SHA256 file from the same GitHub release before extracting.

## Install

Run PowerShell as administrator from the extracted release directory:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

The installer creates the automatic `JonaHomelabCompanion` service, a tray task, a Private-profile firewall rule for TCP 47654 and protected state under `C:\ProgramData\JonaHomelabCompanion`. The service runs without an interactive login and the tray shows its status.

The tray starts at user logon. Restarting the service does not reopen a tray process that was closed; start it again with `Start-ScheduledTask -TaskName JonaHomelabCompanionTray` or sign in again.

Open the tray, copy the `jhcp1_...` pairing code, then edit the device in Jona Homelab and select `Companion`. Paste the code and save. The code is never returned by the homelab API.

## Updates

The service checks the latest GitHub release when it starts and every 24 hours. It downloads the Windows ZIP over HTTPS, validates its SHA256, stages it under the installation releases directory and restarts the service. A failed local health check restores the previous release. Configuration and pairing code remain in `ProgramData`.

## Uninstall

```powershell
.\uninstall.ps1
```

Configuration stays in `ProgramData` for a later reinstall. Use `-PurgeData` only when that state and pairing code must be removed.
