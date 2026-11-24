#!/usr/bin/env python3
"""Plan or expire old, verified FileHub program rollback points."""

import argparse
from contextlib import contextmanager, nullcontext
from dataclasses import dataclass
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import uuid


KEEP = 3
SHA256 = re.compile(r"[0-9a-f]{64}")
POINT_NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9._-]*")


class Refuse(RuntimeError):
    pass


@dataclass(frozen=True)
class Point:
    path: Path
    old_sha: str
    new_sha: str
    verified_ns: int
    bytes: int


def digest(path):
    with path.open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def private_entry(path, device, directory=False):
    info = path.lstat()
    expected = stat.S_ISDIR if directory else stat.S_ISREG
    if (not expected(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077
            or info.st_dev != device or (not directory and info.st_nlink != 1)):
        raise Refuse("unsupported owner, permissions, link or filesystem")
    return info


def mounted_paths():
    return {
        Path(re.sub(r"\\([0-7]{3})", lambda match: chr(int(match[1], 8)), line.split()[4]))
        for line in Path("/proc/self/mountinfo").read_text().splitlines()
    }


def program_paths(install, app):
    return (install / "filehub", install / "filehub-build.json", app / "app.apk", app / "version.json")


def read_point(path, device, destinations, service, mounts):
    if not POINT_NAME.fullmatch(path.name) or any(path == mount or path in mount.parents for mount in mounts):
        raise Refuse("nonstandard name or mounted content")
    directory = private_entry(path, device, directory=True)
    files = {child.name: child for child in path.iterdir()}
    if set(files) not in (
        {"transaction.json", "result.json", "artifact-0", "artifact-1"},
        {"transaction.json", "result.json", "artifact-0", "artifact-1", "artifact-2", "artifact-3"},
    ):
        raise Refuse("contains a database or unsupported files")
    sizes = {name: private_entry(child, device) for name, child in files.items()}
    if any(sizes[name].st_size > 1 << 20 for name in
           ("transaction.json", "result.json", "artifact-1", "artifact-3") if name in sizes):
        raise Refuse("unexpected journal size")
    record = json.loads(files["transaction.json"].read_bytes())
    result = json.loads(files["result.json"].read_bytes())
    if not isinstance(record, dict) or not isinstance(result, dict):
        raise Refuse("invalid journal")
    if record.get("database") or result.get("backupDatabaseSha256"):
        raise Refuse("journal records a database backup")
    if record.get("service") != service or result.get("status") != "PASS":
        raise Refuse("release did not pass verification")
    old_sha, new_sha = record.get("oldSha256"), record.get("newSha256")
    if not all(isinstance(sha, str) and SHA256.fullmatch(sha) for sha in (old_sha, new_sha)):
        raise Refuse("invalid release checksums")
    records = record.get("artifacts")
    if not isinstance(records, list) or len(records) != len(files) - 2:
        raise Refuse("incomplete artifact list")
    for index, (item, destination) in enumerate(zip(records, destinations)):
        name = f"artifact-{index}"
        if (not isinstance(item, dict) or item.get("saved") != name
                or item.get("destination") != str(destination)
                or digest(files[name]) != item.get("sha256")
                or (index == 0 and item.get("sha256") != old_sha)):
            raise Refuse("artifact path or checksum differs from the journal")
    manifest = json.loads(files["artifact-1"].read_bytes())
    if not isinstance(manifest, dict) or manifest.get("binarySha256") != old_sha:
        raise Refuse("program manifest does not match the rollback binary")
    if "artifact-3" in files:
        apk = json.loads(files["artifact-3"].read_bytes())
        if not isinstance(apk, dict) or apk.get("sha256") != records[2]["sha256"]:
            raise Refuse("Android manifest does not match the rollback APK")
    used = directory.st_blocks * 512 + sum(info.st_blocks * 512 for info in sizes.values())
    return Point(path, old_sha, new_sha, sizes["result.json"].st_mtime_ns, used)


def inventory(root, destinations, service):
    device = root.stat().st_dev
    private_entry(root, device, directory=True)
    mounts = mounted_paths()
    points, protected = [], []
    for path in sorted(root.iterdir()):
        try:
            points.append(read_point(path, device, destinations, service, mounts))
        except (Refuse, OSError, ValueError):
            protected.append(path.name)
    points.sort(key=lambda point: (point.verified_ns, point.path.name), reverse=True)
    return points, protected


def retention(points, live_sha):
    # Follow the rollback chain from the running build. If the chain has a
    # gap, fill the reserve with the newest other verified program points.
    keep = []
    expected = live_sha
    for _ in range(KEEP):
        match = next((point for point in points if point.new_sha == expected and point not in keep), None)
        if match is None:
            break
        keep.append(match)
        expected = match.old_sha
    if not keep:
        return points, []
    keep.extend([point for point in points if point not in keep][:KEEP - len(keep)])
    return keep, [point for point in points if point not in keep]


def running_program(service, path):
    status = subprocess.check_output(
        ["systemctl", "show", service, "-p", "ActiveState", "-p", "MainPID"], text=True, timeout=5
    )
    props = dict(line.split("=", 1) for line in status.splitlines())
    pid = props.get("MainPID", "")
    if props.get("ActiveState") != "active" or not pid.isdecimal() or int(pid) == 0:
        raise Refuse("FileHub service is not running")
    executable = Path("/proc") / pid / "exe"
    if not executable.samefile(path):
        raise Refuse("running binary differs from the installation")
    return digest(executable)


@contextmanager
def release_lock(install):
    path = install / ".filehub-upgrade.lock"
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as lock:
        info = os.fstat(lock.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1:
            raise Refuse("invalid FileHub upgrade lock")
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise Refuse("FileHub is being updated or rolled back") from error
        yield


def expire(root, points):
    for point in points:
        # Remove the whole verified point from the active inventory first.
        # An interrupted removal leaves a hidden directory, never a partial
        # rollback journal that could be mistaken for a usable point.
        expired = root / (".expired-" + uuid.uuid4().hex)
        point.path.rename(expired)
        shutil.rmtree(expired)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "apply"))
    parser.add_argument("--root", type=Path, default=Path("/var/lib/filehub-upgrades"))
    parser.add_argument("--install-dir", type=Path, default=Path("/opt/filehub"))
    parser.add_argument("--app-dir", type=Path)
    parser.add_argument("--service", default="filehub.service")
    parser.add_argument("--incoming-bytes", type=int, help="planned additional bytes on the backup filesystem")
    parser.add_argument("--expect-plan", help="require the plan ID from a previous review")
    args = parser.parse_args()
    if args.incoming_bytes is not None and args.incoming_bytes < 0:
        parser.error("--incoming-bytes must be non-negative")
    if not re.fullmatch(r"[a-zA-Z0-9_-]+\.service", args.service):
        parser.error("invalid systemd service name")
    for path in (args.root, args.install_dir):
        if path.resolve(strict=True) != path:
            raise Refuse("use a canonical absolute directory")
    destinations = program_paths(args.install_dir, args.app_dir or args.install_dir / "app")
    apply = args.action == "apply"
    with release_lock(args.install_dir) if apply else nullcontext():
        live_sha = running_program(args.service, destinations[0])
        points, protected = inventory(args.root, destinations, args.service)
        keep, removable = retention(points, live_sha)
        anchored = any(point.new_sha == live_sha for point in keep)
        identity = [str(args.root), args.service, live_sha,
                    [point.path.name for point in keep],
                    [[point.path.name, point.old_sha, point.new_sha, point.verified_ns] for point in removable],
                    protected]
        plan_id = hashlib.sha256(json.dumps(identity, separators=(",", ":")).encode()).hexdigest()
        if args.expect_plan is not None and args.expect_plan != plan_id:
            raise Refuse("rollback inventory changed after the plan was reviewed")
        free = shutil.disk_usage(args.root).free
        incoming = args.incoming_bytes
        if incoming is None:
            incoming = sum(path.stat().st_size for path in destinations if path.is_file())
        report = {
            "mode": args.action,
            "planId": plan_id,
            "hasVerifiedRollbackForRunningBuild": anchored,
            "keep": [point.path.name for point in keep],
            "remove": [point.path.name for point in removable],
            "protected": protected,
            "freeBytes": free,
            "incomingBytes": incoming,
            "freeAfterBackupBytes": free - incoming,
            "reclaimableBytes": sum(point.bytes for point in removable),
        }
        print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
        if apply:
            if not anchored:
                raise Refuse("no verified rollback point for the running build")
            expire(args.root, removable)


if __name__ == "__main__":
    try:
        main()
    except (Refuse, OSError, ValueError, subprocess.SubprocessError) as error:
        print("REFUSED: " + str(error), file=sys.stderr)
        sys.exit(1)
