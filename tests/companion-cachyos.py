"""Run inside the disposable Arch image used by the CachyOS release job."""
import base64
import hashlib
import hmac
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

PACKAGE = "jona-homelab-companion"
ASSET = PACKAGE + "-cachyos-x86_64.pkg.tar.zst"


def run(*args):
    return subprocess.check_output(args, text=True, timeout=30).strip()


def signed_status(code):
    key = base64.urlsafe_b64decode(code.removeprefix("jhcp1_") + "=")
    stamp = str(int(time.time()))
    nonce = secrets.token_urlsafe(16)
    body_hash = hashlib.sha256(b"").hexdigest()
    signature = hmac.digest(key, f"GET\n/v1/status\n{stamp}\n{nonce}\n{body_hash}".encode(), "sha256").hex()
    req = urllib.request.Request("http://127.0.0.1:47654/v1/status", headers={
        "X-Jona-Timestamp": stamp, "X-Jona-Nonce": nonce, "X-Jona-Signature": signature})
    with urllib.request.urlopen(req, timeout=3) as response:
        body = response.read()
        expected = hmac.digest(key, f"200\n{nonce}\n{hashlib.sha256(body).hexdigest()}".encode(), "sha256").hex()
        assert hmac.compare_digest(response.headers["X-Jona-Response-Signature"], expected)
        return json.loads(body)


def smoke():
    artifact = Path("/packages") / ASSET
    assert hashlib.sha256(artifact.read_bytes()).hexdigest() == Path(str(artifact) + ".sha256").read_text().split()[0]
    cached = Path("/var/cache/pacman/pkg") / ASSET
    run("install", "-m", "0644", str(artifact), str(cached))
    run("pacman", "-U", "--noconfirm", str(cached))
    assert run("systemctl", "is-enabled", PACKAGE + ".service") == "enabled"
    assert Path("/var/cache/pacman/pkg", ASSET).is_file(), "rollback archive not cached"
    run("systemd-analyze", "verify", PACKAGE + ".service", PACKAGE + "-update@main-123456789abc.service")
    run("systemctl", "--no-ask-password", "--no-block", "--check-inhibitors=yes", "--version")
    service = subprocess.Popen([PACKAGE, "serve"])
    try:
        for _ in range(60):
            try:
                with urllib.request.urlopen("http://127.0.0.1:47654/health", timeout=1) as response:
                    health = json.load(response)
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("Companion did not start")
        original = run(PACKAGE, "pair")
        status = signed_status(original)
        assert status["platform"] == "cachyos-x64" and status["remoteUpdate"] and status["ready"]
        assert health["version"] == status["version"] and not health["simulated"]
        rotated = run(PACKAGE, "rotate")
        assert original != rotated and signed_status(rotated)["ready"]
        try:
            signed_status(original)
        except urllib.error.HTTPError as err:
            assert err.code == 401
        else:
            raise AssertionError("old pairing key accepted")
        diagnostics = run(PACKAGE, "diagnostics")
        assert rotated not in diagnostics and original not in diagnostics
        assert json.loads(diagnostics)["version"] == status["version"]
        # The second launch must activate the first window and exit successfully.
        run("dbus-run-session", "--", "bash", "-euc", """
            export QT_QPA_PLATFORM=offscreen
            jona-homelab-companion-tray --autostart &
            tray=$!
            trap 'kill "$tray" 2>/dev/null || true' EXIT
            for i in {1..50}; do
                if busctl --user --list | grep -q '^org.jonahomelab.CompanionTray '; then break; fi
                sleep 0.1
            done
            busctl --user introspect org.jonahomelab.CompanionTray /Tray | grep showWindow
            timeout 5 jona-homelab-companion-tray
            kill -0 "$tray"
        """)
    finally:
        service.terminate()
        service.wait(10)
    assert run(PACKAGE, "pair") == rotated
    run("pacman", "-R", "--noconfirm", PACKAGE)
    assert Path("/var/lib/jona-homelab-companion/pairing.key").stat().st_mode & 0o777 == 0o600
    print("PASS: native package, rollback cache, systemd units, signed status, pairing, diagnostics, Qt and uninstall")


if __name__ == "__main__":
    if not Path("/.dockerenv").is_file() or Path("/run/systemd/system").exists() or os.getuid() != 0:
        sys.exit("Use a disposable Arch Docker container without host systemd.")
    smoke()
