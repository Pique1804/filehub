"""Transaction failure tests; no service changes, compilation or real accounts."""
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('upgrade_ready', Path(__file__).with_name('upgrade-ready.py'))
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class TransactionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.program = self.root / 'filehub'
        self.program.write_bytes(b'old')
        self.program.chmod(0o755)
        self.new = self.root / 'new'
        self.new.write_bytes(b'new')
        self.originals = self.root / 'originals'
        self.originals.mkdir()
        (self.originals / 'photo.jpg').write_bytes(b'original photo bytes')
        self.database = self.root / 'index.db'
        with sqlite3.connect(self.database) as connection:
            connection.executescript("CREATE TABLE users(username,pwhash,role,session_version);"
                                     "INSERT INTO users VALUES('fixture','hash','admin','unchanged');"
                                     "CREATE TABLE files(name,path,source);"
                                     "INSERT INTO files VALUES('photo.jpg','photo.jpg','HDD2');")
        self.target = {'database': self.database, 'oldSha256': upgrade.sha(self.program),
                       'configurationHashes': {}, 'configuration': [], 'healthUrl': 'unused',
                       'sources': [str(self.originals)]}
        self.backup = self.root / 'backup'
        self.files = [(self.new, self.program)]
        self.service = self.Service()
        self.health = patch.object(upgrade, 'health').start()
        self.addCleanup(patch.stopall)

    class Service:
        name = 'filehub-fixture.service'
        def __init__(self):
            self.stops, self.starts = 0, 0
        def stop(self):
            self.stops += 1
        def start(self):
            self.starts += 1

    def apply(self):
        return upgrade.apply_upgrade(self.target, self.files, self.backup, self.service)

    def test_update_and_rollback_retain_new_database_writes(self):
        before = upgrade.logical_state(self.database)
        self.apply()
        self.assertEqual(self.program.read_bytes(), b'new')
        self.assertEqual(upgrade.logical_state(self.database), before)
        self.assertEqual(os.stat(self.program).st_mode & 0o777, 0o755)
        with sqlite3.connect(self.database) as connection:
            connection.execute("INSERT INTO files VALUES('later.jpg','later.jpg','HDD2')")
        upgrade.restore_files(self.backup, self.service, 'unused')
        self.assertEqual(self.program.read_bytes(), b'old')
        self.assertEqual(upgrade.logical_state(self.database)['files']['rows'], 2)
        self.assertEqual((self.originals / 'photo.jpg').read_bytes(), b'original photo bytes')
        self.assertEqual(self.backup.stat().st_mode & 0o777, 0o700)
        self.assertTrue(all(p.stat().st_mode & 0o077 == 0 for p in self.backup.iterdir()))

    def test_failed_health_restores_program_and_preserves_database(self):
        self.health.side_effect = [upgrade.Refuse('fixture: new server fails'), None]
        before = upgrade.logical_state(self.database)
        with self.assertRaisesRegex(upgrade.Refuse, 'new server fails'):
            self.apply()
        self.assertEqual(self.program.read_bytes(), b'old')
        self.assertEqual(upgrade.logical_state(self.database), before)
        self.assertEqual(json.loads((self.backup / 'automatic-rollback.json').read_text())['status'], 'PASS')

    def test_background_index_changes_do_not_abort_upgrade(self):
        with sqlite3.connect(self.database) as connection:
            connection.executescript("CREATE TABLE photo_meta(path, taken);"
                                     "CREATE TABLE content_meta(path, attempts);"
                                     "INSERT INTO content_meta VALUES('photo.jpg', 1);")

        def finish_indexing(_url):
            with sqlite3.connect(self.database) as connection:
                connection.executescript("INSERT INTO photo_meta VALUES('photo.jpg', 123);"
                                         "UPDATE content_meta SET attempts=2;")

        self.health.side_effect = finish_indexing
        self.apply()
        self.assertEqual(self.program.read_bytes(), b'new')
        saved = json.loads((self.backup / 'database-state.json').read_text())
        current = upgrade.logical_state(self.database)
        self.assertEqual(saved['photo_meta']['rows'], 0)
        self.assertEqual(current['photo_meta']['rows'], 1)
        self.assertNotEqual(saved['content_meta'], current['content_meta'])
        result = json.loads((self.backup / 'result.json').read_text())
        self.assertEqual(result['preservedTables'], ['users', 'files'])

    def test_changed_album_triggers_rollback_without_replacing_current_database(self):
        with sqlite3.connect(self.database) as connection:
            connection.executescript("CREATE TABLE albums(id, name);"
                                     "INSERT INTO albums VALUES(1, 'before');")

        def change_album(*_):
            with sqlite3.connect(self.database) as connection:
                connection.execute("UPDATE albums SET name='after' WHERE id=1")

        with self.assertRaisesRegex(upgrade.Refuse, 'changed during startup: albums'):
            upgrade.apply_upgrade(self.target, self.files, self.backup, self.service,
                                  change_album)
        self.assertEqual(self.program.read_bytes(), b'old')
        self.assertEqual(json.loads((self.backup / 'automatic-rollback.json').read_text())['status'], 'PASS')
        self.assertNotEqual(upgrade.logical_state(self.database)['albums'],
                            upgrade.logical_state(self.backup / 'index.db')['albums'])
        with sqlite3.connect(self.database) as connection:
            self.assertEqual(connection.execute('SELECT name FROM albums').fetchone(), ('after',))

    def test_partial_publication_restores_every_file(self):
        app = self.root / 'app.apk'
        app.write_bytes(b'old apk')
        new_app = self.root / 'new.apk'
        new_app.write_bytes(b'new apk')
        self.files.append((new_app, app))
        actual_copy = upgrade.atomic_copy
        def fail_on_new_apk(source, destination):
            if source == new_app:
                raise OSError('fixture: interrupted APK publication')
            return actual_copy(source, destination)
        with patch.object(upgrade, 'atomic_copy', side_effect=fail_on_new_apk):
            with self.assertRaisesRegex(OSError, 'interrupted APK'):
                self.apply()
        self.assertEqual(self.program.read_bytes(), b'old')
        self.assertEqual(app.read_bytes(), b'old apk')

    def test_private_backup_inside_a_storage_source_is_rejected(self):
        self.backup = self.originals / 'unsafe-backup'
        with self.assertRaisesRegex(upgrade.Refuse, 'outside every'):
            self.apply()
        self.assertEqual(self.service.stops, 0)
        self.assertFalse(self.backup.exists())

    def test_tampered_recovery_copy_is_rejected_before_stopping(self):
        self.apply()
        (self.backup / 'artifact-0').write_bytes(b'tampered')
        stops = self.service.stops
        with self.assertRaisesRegex(upgrade.Refuse, 'checksum mismatch'):
            upgrade.restore_files(self.backup, self.service, 'unused')
        self.assertEqual(self.service.stops, stops)
        self.assertEqual(self.program.read_bytes(), b'new')

    def test_sqlite_failure_restarts_old_service(self):
        with patch.object(upgrade, 'snapshot', side_effect=sqlite3.OperationalError('fixture: disk full')):
            with self.assertRaisesRegex(sqlite3.OperationalError, 'disk full'):
                self.apply()
        self.assertEqual(self.program.read_bytes(), b'old')
        self.assertEqual(self.service.starts, 1)

    def test_symlinked_binary_is_rejected_without_stopping(self):
        alias = self.root / 'alias'
        alias.symlink_to(self.program)
        self.files = [(self.new, alias)]
        with self.assertRaisesRegex(upgrade.Refuse, 'Symlinked'):
            self.apply()
        self.assertEqual(self.service.stops, 0)

    def test_insufficient_backup_space_is_rejected_without_stopping(self):
        with patch.object(upgrade.shutil, 'disk_usage', return_value=type('Usage', (), {'free': 0})()):
            with self.assertRaisesRegex(upgrade.Refuse, 'Not enough free'):
                self.apply()
        self.assertEqual(self.service.stops, 0)

    def test_space_forecast_includes_wal_pages_and_both_program_copies(self):
        with sqlite3.connect(self.database) as writer:
            writer.execute('PRAGMA journal_mode=WAL')
            writer.execute('CREATE TABLE extra(data)')
            writer.execute('INSERT INTO extra VALUES(zeroblob(65536))')
            writer.commit()
            estimate = upgrade.forecast_space(self.target, self.files, self.root)
            self.assertGreater(estimate['sqliteSnapshotBytes'], self.database.stat().st_size)
            self.assertEqual(estimate['backupBytes'],
                             estimate['sqliteSnapshotBytes'] + self.program.stat().st_size)
            volume, = estimate['filesystems']
            self.assertEqual(volume['additionalBytes'], estimate['backupBytes'] + self.new.stat().st_size)
            self.assertEqual(volume['estimatedFreeBytes'], volume['freeBytes'] - volume['additionalBytes'])


if __name__ == '__main__':
    unittest.main()
