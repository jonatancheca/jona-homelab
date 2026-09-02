# Jona Homelab Companion (Go)

Windows 11 x64 companion for authenticated LAN status and shutdown commands. The package is self-contained, written in Go and does not require .NET. It is not Authenticode-signed; Windows SmartScreen may show a warning. Verify the SHA256 file from the same GitHub release before extracting.

## Install

Run PowerShell as administrator from the extracted release directory:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

The installer creates the automatic `JonaHomelabCompanion` service, a tray task, a Private-profile firewall rule for TCP 47654 and protected state under `C:\ProgramData\JonaHomelabCompanion`. The service runs as `LocalSystem`; the tray is a separate interactive process.

The tray starts at user logon. Restarting the service does not reopen a tray process that was closed; start it again with:

```powershell
Start-ScheduledTask -TaskName JonaHomelabCompanionTray
```

Do not launch the tray with `&` when you want your shell prompt back: the tray is intentionally a resident process. Use the scheduled task or:

```powershell
Start-Process -WindowStyle Hidden -FilePath 'C:\Program Files\JonaHomelabCompanion\current\JonaHomelab.Companion.exe' -ArgumentList '--tray'
```

The tray window shows service status, version, local IPv4 address, API port, pairing code and the last authenticated server call. Copy or rotate the code, refresh state and check updates from the window or tray menu.

If Windows shows the service as stopped, read the safe startup error log from an administrator PowerShell:

```powershell
Get-Content 'C:\ProgramData\JonaHomelabCompanion\service.log'
```

The log contains startup/bind errors only; it never contains the pairing secret or request signatures.

Open the tray, copy the `jhcp1_...` pairing code, then edit the device in Jona Homelab and select `Companion`. Paste the code and save. The code is never returned by the homelab API.

## Protocol

The service listens on IPv4 port `47654`. `GET /v1/status` and `POST /v1/shutdown` use HMAC-SHA256 signatures with the method, path, timestamp, nonce and SHA-256 body hash. Timestamps allow ±60 seconds; nonces are single-use. Responses are signed too. Only private IPv4 clients are accepted. Shutdown returns `202` and runs `shutdown.exe /s /t 0`; forced shutdown adds `/f`. A local ten-second cooldown prevents repeated requests.

The named pipe is local-only and exposes tray operations (`get-info`, `rotate`, `check-update`). The secret is generated once, protected with machine DPAPI and never logged.

## Updates

The service checks the latest GitHub release after startup and every 24 hours. It downloads the Windows ZIP over HTTPS, validates the `main-<sha>` version, checksum, archive paths and required files, stages it under the versioned installation directory and restarts the service. A failed local health check restores the previous release. Configuration and pairing code remain in `ProgramData`. The same Go executable runs service, tray and external updater modes.

## Uninstall

```powershell
.\uninstall.ps1
```

Configuration stays in `ProgramData` for a later reinstall. Use `-PurgeData` only when that state and pairing code must be removed.
