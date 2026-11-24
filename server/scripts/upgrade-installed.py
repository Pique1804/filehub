#!/usr/bin/env python3
"""Update an existing installation of the official FileHub Linux package."""

import argparse
import fcntl
from importlib import import_module
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import stat
import subprocess
import sys
import tempfile


upgrade = import_module("upgrade-ready")
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "distribution/prebuilt"))
from apk_signature import verified_apk
from storage_model import Invalid, Storage, legacy_config

SUPPORTED_INSTALLATIONS = {
    ("1.40.20", 70): "d01c9c1dc34b96044244a13b15c10335a590c16d8a4fcb572153cdf54e330c22",
    ("1.40.21", 71): "c483b4e23eae8a8b986be73ec74a8482ed58b5838dee0ca0024a275f490c9035",
}
NEW_VERSION = "1.40.22"
NEW_APP_CODE = 72


def call(*args):
    return subprocess.check_output(["systemctl", *args], text=True, timeout=45).strip()


def private_backup(path, sources, state):
    parent = upgrade.plain(path.parent, directory=True)
    info = parent.stat()
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) & 0o077:
        raise upgrade.Refuse("Create a root-owned private backup directory (0700)")
    if any(parent.is_relative_to(root) or root.is_relative_to(path) for root in (*sources, state)):
        raise upgrade.Refuse("The backup must be outside FileHub storage and state")
    if os.path.lexists(path):
        raise upgrade.Refuse("Choose a new backup directory")


def saved_artifacts(files, backup):
    records = []
    for index, (_, destination) in enumerate(files):
        info = destination.stat()
        saved = backup / f"artifact-{index}"
        shutil.copyfile(destination, saved)
        saved.chmod(0o600)
        records.append({"destination": str(destination), "saved": saved.name,
                        "sha256": upgrade.sha(saved), "mode": stat.S_IMODE(info.st_mode),
                        "uid": info.st_uid, "gid": info.st_gid})
    return records


def restore_database(database, snapshot, metadata):
    # The public proxy has not restarted. Move failed startup state out of the
    # way before restoring the verified snapshot, including any WAL/SHM files.
    quarantine = Path(tempfile.mkdtemp(prefix=".failed-upgrade-", dir=database.parent))
    for suffix in ("", "-wal", "-shm"):
        existing = Path(str(database) + suffix)
        if existing.exists():
            upgrade.plain(existing)
            existing.rename(quarantine / existing.name)
    upgrade.atomic_copy(snapshot, database, metadata)


def apply(name, files, target, backup, port):
    launcher, front = name + ".service", name + "-front.service"
    database = target["database"]
    snapshot = backup / "index.db"
    metadata = database.stat()
    backup.mkdir(mode=0o700)
    records = saved_artifacts(files, backup)
    journal = {"service": launcher, "oldSha256": records[0]["sha256"], "newSha256": upgrade.sha(files[0][0]),
               "database": str(database), "artifacts": records}
    upgrade.write_json(backup / "transaction.json", journal)
    backend_touched = False
    public = False
    endpoint = f"http://127.0.0.1:{port}"
    try:
        call("stop", front, launcher)
        upgrade.snapshot(database, snapshot)
        for artifact, target_file in files:
            upgrade.atomic_copy(artifact, target_file)
        backend_touched = True
        call("start", launcher)
        upgrade.health(endpoint)
        for source, destination in files:
            if upgrade.sha(source) != upgrade.sha(destination):
                raise upgrade.Refuse("Installed artifact checksum differs from the release")
        public = True
        call("start", front)
        call("is-active", front)
        upgrade.write_json(backup / "result.json", {"status": "PASS", "version": NEW_VERSION,
                                                   "backupDatabaseSha256": upgrade.sha(snapshot)})
    except BaseException:
        if public:
            raise upgrade.Refuse(f"The updated backend passed checks; inspect the web proxy. Backup: {backup}") from None
        try:
            call("stop", front, launcher)
            if backend_touched:
                restore_database(database, snapshot, metadata)
            for item in records:
                saved = backup / item["saved"]
                if upgrade.sha(saved) != item["sha256"]:
                    raise upgrade.Refuse("Rollback artifact checksum mismatch")
                upgrade.atomic_copy(saved, Path(item["destination"]))
            call("start", front)
            upgrade.health(endpoint)
        except BaseException:
            raise upgrade.Refuse(f"Update stopped and recovery needs attention. Backup: {backup}") from None
        upgrade.write_json(backup / "automatic-rollback.json", {"status": "PASS"})
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "apply"))
    parser.add_argument("--name", default="filehub-onboarding")
    parser.add_argument("--backup", type=Path, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0 or not re.fullmatch(r"[a-z][a-z0-9-]{1,40}", args.name):
        parser.error("Run with sudo and use a valid installation name")
    if not args.backup.is_absolute() or args.backup != args.backup.resolve():
        parser.error("Use a canonical absolute backup path")
    root = upgrade.plain(Path("/opt/filehub-installed") / args.name, directory=True)
    bundle = Path(__file__).resolve().parents[2]
    package = bundle / "artifacts"
    catalog = json.loads(upgrade.plain(Path("/etc") / f"{args.name}.json").read_bytes())
    if (catalog.get("service_name") != args.name or catalog.get("binary") != str(root / "filehub")
            or catalog.get("app_dir") != str(root / "app")):
        raise upgrade.Refuse("This installation does not match the official Linux package layout")
    state = upgrade.plain(Path(catalog["state_dir"]), directory=True)
    installation = json.loads(upgrade.plain(state / "installation.json").read_bytes())
    database = upgrade.plain(state / "index.db")
    new_build = json.loads(upgrade.plain(package / "filehub-build.json").read_bytes())
    _, _, new_app = verified_apk(package / "app")
    if (new_build.get("version") != NEW_VERSION or new_app.get("versionName") != NEW_VERSION
            or upgrade.sha(upgrade.plain(package / "filehub")) != new_build.get("binarySha256")
            or new_app.get("versionCode") != NEW_APP_CODE):
        raise upgrade.Refuse(f"Server and signed APK must belong to the {NEW_VERSION} release")
    files = [(upgrade.plain(package / name), upgrade.plain(root / name))
             for name in ("filehub", "app/app.apk", "app/version.json")]
    launcher_root = Path("/opt/filehub-installer") if args.name == "filehub-onboarding" else Path("/opt/filehub-installers") / args.name
    launcher_source = bundle / "server/distribution/prebuilt"
    files.extend((upgrade.plain(launcher_source / name), upgrade.plain(launcher_root / name))
                 for name in ("launcher.py", "storage_model.py"))

    def inspect():
        _, _, old_app = verified_apk(root / "app")
        old_version = old_app.get("versionName")
        expected = SUPPORTED_INSTALLATIONS.get((old_version, old_app.get("versionCode")))
        if expected is None or upgrade.sha(root / "filehub") != expected:
            raise upgrade.Refuse("This updater expects the official installed FileHub 1.40.20 or 1.40.21 release")
        roots = Storage(catalog, state).paths(legacy_config(installation))
        private_backup(args.backup, [path.resolve() for path in roots.values()], state)
        port = int(catalog["backend_port"])
        if not 1024 <= port <= 65535:
            raise upgrade.Refuse("Invalid configured backend port")
        call("is-active", args.name + ".service")
        call("is-active", args.name + "-front.service")
        upgrade.health(f"http://127.0.0.1:{port}")
        target = {"database": database, "configuration": []}
        forecast = upgrade.forecast_space(target, files, args.backup.parent)
        if any(volume["estimatedFreeBytes"] < (256 << 20) for volume in forecast["filesystems"]):
            raise upgrade.Refuse("Insufficient disk space for the update and recovery snapshot")
        print(json.dumps({"from": old_version, "to": NEW_VERSION, "spaceForecast": forecast}, indent=2), flush=True)
        return target, port

    if args.action == "plan":
        inspect()
        return
    descriptor = os.open(root / ".filehub-upgrade.lock", os.O_CREAT | os.O_RDONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "rb") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        target, port = inspect()
        apply(args.name, files, target, args.backup, port)
    print(f"Update verified. Private recovery snapshot: {args.backup}")


if __name__ == "__main__":
    try:
        main()
    except (upgrade.Refuse, Invalid, OSError, ValueError, KeyError, sqlite3.Error, subprocess.SubprocessError) as error:
        sys.exit(f"REFUSED: {error}")
