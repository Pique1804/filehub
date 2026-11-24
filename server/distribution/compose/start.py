"""Run the folder-only installer and reverse proxy in one container."""
import json
import os
from pathlib import Path, PurePosixPath
import re
import signal
import subprocess
import sys
from threading import Event
from time import monotonic
from urllib.parse import urlsplit

APP = Path("/opt/filehub")
RUN = Path("/run/filehub")
STATE = Path("/var/lib/filehub")
DATA = Path("/data")
sys.path.insert(0, str(APP / "installer"))

from configure import render_nginx


def public_url():
    value = os.environ["FILEHUB_PUBLIC_URL"]
    url = urlsplit(value)
    if (
        url.scheme not in {"http", "https"}
        or not re.fullmatch(r"[a-z0-9.-]+", url.hostname or "")
        or url.username is not None
        or url.password is not None
        or url.path not in {"", "/"}
        or url.query
        or url.fragment
        or url.port == 0
    ):
        raise ValueError("FILEHUB_PUBLIC_URL must be an HTTP(S) origin with a DNS name or IPv4 address")
    host = url.hostname if url.port is None else f"{url.hostname}:{url.port}"
    return f"{url.scheme}://{host}"


def check_mounts():
    if os.geteuid() == 0 or os.getegid() == 0:
        raise ValueError("Set FILEHUB_UID and FILEHUB_GID to non-root numeric IDs")
    mounts = {}
    for line in Path("/proc/self/mountinfo").read_text().splitlines():
        _, _, device, root, target, *_ = line.split()
        mounts[target] = device, PurePosixPath(re.sub(r"\\([0-7]{3})", lambda m: chr(int(m[1], 8)), root))
    for folder, access in ((STATE, os.R_OK | os.W_OK | os.X_OK), (DATA, os.R_OK | os.X_OK)):
        if str(folder) not in mounts or not folder.is_dir() or not os.access(folder, access):
            raise ValueError(f"{folder} must be a folder mount accessible to the configured UID/GID")
    state_device, state_root = mounts[str(STATE)]
    data_device, data_root = mounts[str(DATA)]
    if STATE.samefile(DATA) or (
        state_device == data_device
        and (state_root in data_root.parents or data_root in state_root.parents or state_root == data_root)
    ):
        raise ValueError("The state and data mounts must point to separate, non-nested folders")


def configuration():
    return {
        "version": 1,
        "service_name": "filehub-compose",
        "public_url": public_url(),
        "binary": str(APP / "artifacts/filehub"),
        "app_dir": str(APP / "artifacts/app"),
        "state_dir": str(STATE),
        "backend_port": 18081,
        "socket": str(RUN / "setup.sock"),
        "storages": [{
            "id": "server",
            "name": "Папка сервера",
            "root": str(DATA),
            "description": "Подключённая папка",
        }],
    }


def supervise(launcher, proxy):
    stopped = Event()

    def stop(*_):
        stopped.set()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    services = [subprocess.Popen(launcher)]
    try:
        services.append(subprocess.Popen(proxy))
        while not stopped.wait(0.2):
            if any(service.poll() is not None for service in services):
                return 1
        return 0
    finally:
        for service in services:
            if service.poll() is None:
                service.terminate()
        deadline = monotonic() + 18
        for service in services:
            try:
                service.wait(timeout=max(0, deadline - monotonic()))
            except subprocess.TimeoutExpired:
                service.kill()
                service.wait()


def main():
    os.umask(0o077)
    check_mounts()
    catalog = configuration()
    for folder in ("client", "proxy", "fastcgi", "uwsgi", "scgi"):
        (RUN / folder).mkdir(exist_ok=True)
    catalog_path = RUN / "catalog.json"
    catalog_path.write_text(json.dumps(catalog))
    nginx_path = RUN / "nginx.conf"
    # Debian's nginx defaults for unused HTTP modules point outside the runtime tmpfs.
    extra_paths = "\n".join(f"  {module}_temp_path {RUN}/{module};" for module in ("fastcgi", "uwsgi", "scgi"))
    nginx = render_nginx(catalog, str(RUN), "8080").replace("http {", "http {\n" + extra_paths, 1)
    nginx_path.write_text(nginx)
    return supervise(
        [sys.executable, str(APP / "installer/launcher.py"), str(catalog_path)],
        ["/usr/sbin/nginx", "-c", str(nginx_path), "-g", "daemon off;"],
    )


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (KeyError, OSError, ValueError) as error:
        sys.exit(f"FileHub Compose: {error}")
