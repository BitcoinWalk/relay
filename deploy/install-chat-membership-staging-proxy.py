#!/usr/bin/env python3
"""Expose only the staging membership endpoint; preserve all other site blocks."""
import fcntl
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def candidate(text):
    start = "chat-staging.bitcoinwalk.org {\n"
    if text.count(start) != 1:
        raise ValueError("Expected exactly one staging site block")
    offset = text.index(start)
    end = text.index("\n}", offset) + 2
    block = text[offset:end]
    health = "\t\thandle /healthz {\n\t\t\treverse_proxy 127.0.0.1:3335\n\t\t}"
    route = "\t\thandle /api/chat/upload-authorization {\n\t\t\treverse_proxy 127.0.0.1:3335\n\t\t}"
    if route in block:
        raise ValueError("Route already exists; verify instead of reinstalling")
    if block.count(health) != 1 or "/api/chat/upload-authorization" in block:
        raise ValueError("Staging proxy shape changed; review required")
    return text[:offset] + block.replace(health, health + "\n" + route) + text[end:]


def main():
    if sys.argv[1:] != ["--apply"] or os.geteuid() != 0:
        raise SystemExit("Run with sudo python3 SCRIPT --apply")
    with open("/run/lock/bitcoinwalk-chat-membership-staging-proxy.lock", "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        path = Path("/etc/caddy/Caddyfile")
        if path.is_symlink() or not path.is_file():
            raise SystemExit("Expected a regular Caddyfile")
        original = path.read_bytes()
        replacement = candidate(original.decode()).encode()
        subprocess.run(["systemctl", "is-active", "--quiet", "bitcoinwalk-chat-staging.service"], check=True)
        backup = Path(tempfile.mkdtemp(prefix="bitcoinwalk-chat-membership-proxy.", dir="/var/backups"))
        shutil.copy2(path, backup / "Caddyfile.before")
        # Validate beside the live configuration so relative imports keep their base.
        fd, staged = tempfile.mkstemp(prefix=".membership-staging-", dir=path.parent)
        os.close(fd)
        staged = Path(staged)
        try:
            staged.write_bytes(replacement)
            shutil.copystat(path, staged)
            info = path.stat()
            os.chown(staged, info.st_uid, info.st_gid)
            subprocess.run(["caddy", "validate", "--config", str(staged), "--adapter", "caddyfile"], check=True)
            if hashlib.sha256(path.read_bytes()).digest() != hashlib.sha256(original).digest():
                raise RuntimeError("Caddyfile changed during validation; no activation")
            os.replace(staged, path)
            try:
                subprocess.run(["systemctl", "reload", "caddy.service"], check=True)
            except subprocess.CalledProcessError:
                shutil.copy2(backup / "Caddyfile.before", path)
                subprocess.run(["systemctl", "reload", "caddy.service"], check=True)
                raise
        finally:
            staged.unlink(missing_ok=True)
        print(f"STAGING_MEMBERSHIP_PROXY_READY; backup: {backup}")
        print("Only staging route added. Web enforcement remains unchanged.")


if __name__ == "__main__":
    main()
