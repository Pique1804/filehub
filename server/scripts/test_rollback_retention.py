"""Exercise retention and program rollback on disposable files and SQLite."""

import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest.mock import patch


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


policy = load("rollback_retention", "rollback-retention.py")
upgrade = load("retention_upgrade", "upgrade-ready.py")


class RetentionTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name) / "rollback"
        self.root.mkdir(mode=0o700)
        self.install = self.root.parent / "install"
        self.install.mkdir()
        self.paths = policy.program_paths(self.install, self.install / "app")
        self.service = "filehub-fixture.service"

    @staticmethod
    def save(path, data):
        path.write_bytes(data if isinstance(data, bytes) else json.dumps(data).encode())
        path.chmod(0o600)
        return path

    def point(self, n, *, new_sha=None, android=False):
        path = self.root / f"release-{n:02}"
        path.mkdir(mode=0o700)
        old = self.save(path / "artifact-0", f"binary-{n-1}".encode())
        old_sha = policy.digest(old)
        new_file = self.save(self.paths[0], f"binary-{n}".encode())
        files = [old, self.save(path / "artifact-1", {"binarySha256": old_sha})]
        if android:
            apk = self.save(path / "artifact-2", b"fixture apk")
            files.extend((apk, self.save(path / "artifact-3", {"sha256": policy.digest(apk)})))
        artifacts = [
            {"saved": file.name, "destination": str(destination), "sha256": policy.digest(file),
             "mode": 0o755 if index == 0 else 0o644, "uid": os.geteuid(), "gid": os.getegid()}
            for index, (file, destination) in enumerate(zip(files, self.paths))
        ]
        self.save(path / "transaction.json", {
            "service": self.service, "oldSha256": old_sha, "newSha256": new_sha or policy.digest(new_file),
            "artifacts": artifacts,
        })
        result = self.save(path / "result.json", {"status": "PASS"})
        os.utime(result, ns=(n, n))
        return path

    def test_older_program_points_expire_and_newest_rolls_back_without_data_loss(self):
        for n in range(1, 7):
            newest = self.point(n, android=n == 3)
        database = self.install / "index.db"
        with sqlite3.connect(database) as db:
            db.execute("CREATE TABLE files(name)")
            db.execute("INSERT INTO files VALUES ('created after the upgrade')")
        self.save(self.paths[1], {"binarySha256": policy.digest(self.paths[0])})
        points, protected = policy.inventory(self.root, self.paths, self.service)
        keep, remove = policy.retention(points, policy.digest(self.paths[0]))
        self.assertEqual(protected, [])
        self.assertEqual([p.path.name for p in keep], ["release-06", "release-05", "release-04"])
        policy.expire(self.root, remove)
        self.assertEqual(set(self.root.iterdir()), {p.path for p in keep})

        class FixtureService:
            name = self.service
            stop = start = lambda self: None

        with patch.object(upgrade, "health") as health:
            upgrade.restore_files(newest, FixtureService(), "unused")
            health.assert_called_once()
        self.assertEqual(self.paths[0].read_bytes(), b"binary-5")
        with sqlite3.connect(database) as db:
            self.assertEqual(db.execute("SELECT name FROM files").fetchall(), [("created after the upgrade",)])

    def test_database_unverified_corrupt_and_linked_points_are_protected(self):
        legacy = self.point(1)
        self.save(legacy / "index.db", b"fixture database")
        failed = self.point(2)
        self.save(failed / "result.json", {"status": "FAIL"})
        corrupt = self.point(3)
        self.save(corrupt / "artifact-0", b"modified after verification")
        linked = self.point(4)
        os.link(linked / "artifact-0", self.root.parent / "hardlink")
        (self.root / "symlink").symlink_to(legacy, target_is_directory=True)
        healthy = self.point(5)
        points, protected = policy.inventory(self.root, self.paths, self.service)
        self.assertEqual([point.path for point in points], [healthy])
        self.assertEqual(set(protected), {p.name for p in (legacy, failed, corrupt, linked)} | {"symlink"})

    def test_unmatched_running_build_blocks_expiration(self):
        for n in range(1, 6):
            self.point(n)
        points, _ = policy.inventory(self.root, self.paths, self.service)
        keep, remove = policy.retention(points, "0" * 64)
        self.assertEqual(keep, points)
        self.assertEqual(remove, [])

    def test_same_filesystem_bind_mount_is_protected(self):
        mounted = self.point(1)
        with patch.object(policy, "mounted_paths", return_value={mounted}):
            points, protected = policy.inventory(self.root, self.paths, self.service)
        self.assertEqual(points, [])
        self.assertEqual(protected, [mounted.name])

    def test_busy_release_lock_cannot_be_taken(self):
        self.save(self.install / ".filehub-upgrade.lock", b"")
        with policy.release_lock(self.install):
            with self.assertRaisesRegex(policy.Refuse, "being updated"):
                with policy.release_lock(self.install):
                    self.fail("concurrent retention acquired the upgrade lock")
