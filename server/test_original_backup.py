import importlib.util
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('original_backup', Path(__file__).with_name('original-backup.py'))
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.source = self.root / 'source'
        self.versions = self.root / 'versions'
        self.destination = self.root / 'backup'
        self.source.mkdir()
        self.versions.mkdir()
        (self.source / 'photo.bin').write_bytes(b'original')
        (self.versions / 'version.bin').write_bytes(b'previous')
        self.database = self.root / 'data.db'
        with sqlite3.connect(self.database) as db:
            db.execute('create table proof(value text)')
            db.execute("insert into proof values('recoverable')")

    def tearDown(self):
        self.temp.cleanup()

    def run_copy(self, **kwargs):
        return backup.run_backup(self.source, self.versions, self.destination,
                                 self.database, require_distinct=False, **kwargs)

    def test_restorable_database_and_both_trees(self):
        state = self.run_copy()
        self.assertTrue(state['checksumVerified'])
        self.assertTrue(state['versionsChecksumVerified'])
        self.assertEqual((self.destination / 'Photos/photo.bin').read_bytes(), b'original')
        with sqlite3.connect(self.destination / state['databaseSnapshot']) as db:
            self.assertEqual(db.execute('select value from proof').fetchone()[0], 'recoverable')

    def test_same_metadata_corruption_repaired_and_preserved(self):
        self.run_copy()
        for relative in ['Photos/photo.bin', 'edit-versions/version.bin']:
            target = self.destination / relative
            before = target.stat()
            target.write_bytes(b'corrupt!')
            os.utime(target, ns=(before.st_atime_ns, before.st_mtime_ns))
        state = self.run_copy()
        self.assertEqual(state['state'], 'ok')
        self.assertEqual((self.destination / 'Photos/photo.bin').read_bytes(), b'original')
        self.assertEqual((self.destination / 'edit-versions/version.bin').read_bytes(), b'previous')
        self.assertEqual(len(list((self.destination / 'changed').rglob('*.bin'))), 2)

    def test_missing_source_file_does_not_remove_backup(self):
        self.run_copy()
        (self.source / 'photo.bin').rename(self.root / 'removed-by-fixture.bin')
        self.run_copy()
        self.assertEqual((self.destination / 'Photos/photo.bin').read_bytes(), b'original')

    def test_failure_keeps_previous_success_but_not_success_flags(self):
        first = self.run_copy()
        with mock.patch.object(backup.subprocess, 'run', side_effect=RuntimeError('simulated transfer failure')):
            with self.assertRaises(RuntimeError):
                self.run_copy()
        state = json.loads((self.destination / 'status.json').read_text())
        self.assertEqual(state['state'], 'error')
        self.assertFalse(state['checksumVerified'])
        self.assertEqual(state['lastSuccessfulCompleted'], first['completed'])
        self.assertTrue(state['lastSuccess']['checksumVerified'])

    def test_missing_mount_fails_before_creating_destination(self):
        with self.assertRaises(RuntimeError):
            self.run_copy(required_mounts=(str(self.root),))
        self.assertFalse(self.destination.exists())

    def configured_plan(self):
        def identity(folder):
            stat = folder.stat()
            return {'fullPath':str(folder), 'device':stat.st_dev, 'inode':stat.st_ino}

        self.destination.mkdir()
        extra = self.root / 'documents'
        extra.mkdir()
        (extra / 'letter.txt').write_text('document')
        (self.source / '.filehub-hidden').mkdir()
        (self.source / '.filehub-hidden' / 'secret').write_text('private')
        version_folder = self.source / '.filehub-versions'
        version_folder.mkdir()
        (version_folder / 'version.bin').write_bytes(b'previous')
        with sqlite3.connect(self.database) as db:
            db.execute('CREATE TABLE photo_versions(source,path,stored_source,stored,sha256)')
            db.execute('INSERT INTO photo_versions VALUES(?,?,?,?,?)',
                       ('Photos','photo.bin','Photos','version.bin',hashlib.sha256(b'previous').hexdigest()))
        return {'instance':'a'*32, 'database':str(self.database),
                'destination':identity(self.destination),
                'folders':[dict(source='Photos',path='',**identity(self.source)),
                           dict(source='Docs',path='',**identity(extra))],
                'roots':{'Photos':identity(self.source), 'Docs':identity(extra)}}

    def test_configured_snapshot_copies_selected_roots_and_preserves_previous_snapshot(self):
        plan = self.configured_plan()
        base = self.destination / backup.BACKUP_DIRECTORY / plan['instance']
        first = base / backup.run_configured_backup(plan)['snapshot']
        manifest = json.loads((first / 'manifest.json').read_text())
        photo = Path(manifest['folders'][0]['copy']) / 'photo.bin'
        document = Path(manifest['folders'][1]['copy']) / 'letter.txt'
        self.assertEqual((first / photo).read_bytes(), b'original')
        self.assertEqual((first / document).read_text(), 'document')
        self.assertFalse((first / photo.parent / '.filehub-hidden').exists())
        self.assertEqual(manifest['versions'], 1)
        self.assertEqual((first / 'versions' / backup.path_key('Photos') / 'version.bin').read_bytes(), b'previous')
        with sqlite3.connect(first / manifest['database']) as db:
            self.assertEqual(db.execute('select value from proof').fetchone()[0], 'recoverable')

        (self.source / 'photo.bin').write_bytes(b'updated!')
        second = base / backup.run_configured_backup(plan)['snapshot']
        self.assertEqual((first / photo).read_bytes(), b'original')
        self.assertEqual((second / photo).read_bytes(), b'updated!')
        self.assertEqual((first / document).stat().st_ino, (second / document).stat().st_ino)

        (self.source / '.filehub-versions' / 'version.bin').write_bytes(b'corrupt!')
        with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
            backup.run_configured_backup(plan)
        self.assertEqual(sorted(base.glob('snapshot-*')), [first, second])
        self.assertEqual(list(base.glob('.pending-*')), [])

    def test_configured_backup_refuses_replaced_destination(self):
        plan = self.configured_plan()
        self.destination.rename(self.root / 'unmounted')
        self.destination.mkdir()
        with self.assertRaisesRegex(RuntimeError, 'directory has changed'):
            backup.run_configured_backup(plan)
        self.assertEqual(list(self.destination.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
