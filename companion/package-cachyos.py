"""Build the CachyOS x86_64 package with Go and an isolated Arch makepkg."""
import argparse
import hashlib
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

COMPANION = Path(__file__).resolve().parent
ARCHIVE = "jona-homelab-companion-cachyos-x86_64.pkg.tar.zst"


def build(release, output):
    if release != "local-cachyos" and not re.fullmatch(r"main-[0-9a-f]{12}", release):
        raise ValueError("Expected local-cachyos or main-<12 hex digits>")
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="jona-cachyos-") as temp:
        stage = Path(temp)
        shutil.copytree(COMPANION / "cachyos", stage, dirs_exist_ok=True)
        shutil.copyfile(COMPANION / "VERSION", stage / "VERSION")
        (stage / "RELEASE_VERSION").write_text(release + "\n", encoding="ascii")
        # A working tree checked out by Windows Git may use CRLF.
        for file in stage.iterdir():
            if file.is_file():
                file.write_bytes(file.read_bytes().replace(b"\r\n", b"\n"))
        env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
        subprocess.run(["go", "build", "-trimpath", "-ldflags", f"-s -w -X main.buildRelease={release}",
                        "-o", str(stage / "companion-daemon"), "."], cwd=COMPANION, env=env, check=True)
        subprocess.run(["go", "test", "-c", "-o", str(stage / "companion.test"), "."],
                       cwd=COMPANION, env=env, check=True)
        # Match the repository layout used by the shared protocol fixtures.
        fixtures = COMPANION.parent / "tests" / "fixtures"
        shutil.copytree(fixtures, stage / "fixtures")
        subprocess.run(["docker", "run", "--rm", "--network=none", "-v", f"{stage}:/source:ro",
                        "-v", f"{output}:/output", "archlinux:base-devel", "bash", "-euc", """
mkdir -p /tmp/work/companion /tmp/work/tests
cp -r /source/. /tmp/work/companion
cd /tmp/work/companion
cp -r fixtures /tmp/work/tests/
chmod +x companion.test companion-daemon
./companion.test -test.v
useradd -m builder
chown -R builder:builder /tmp/work
runuser -u builder -- makepkg --nodeps --noconfirm
pkg=$(find . -maxdepth 1 -name '*.pkg.tar.zst' -print -quit)
pacman -Qip "$pkg"
pacman -Qlp "$pkg"
install -m644 "$pkg" /output/""" + ARCHIVE], check=True)
    artifact = output / ARCHIVE
    digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
    (output / (ARCHIVE + ".sha256")).write_bytes(f"{digest}  {ARCHIVE}\n".encode())
    print(artifact)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release", default="local-cachyos")
    parser.add_argument("--output", type=Path, default=COMPANION.parent / "artifacts")
    args = parser.parse_args()
    build(args.release, args.output.resolve())
