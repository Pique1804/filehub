#!/usr/bin/env python3
"""Upgrade a direct systemd Filehub 1.40 installation without compiling anything.

The first-run installer is deliberately separate. No unit, environment, source
identity, password or original is changed here. Rollback keeps the current DB;
the verified pre-upgrade SQLite snapshot is an additional recovery copy.
"""
import argparse
import contextlib
import fcntl
import hashlib
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
import time
import urllib.error
import urllib.request

VERSION = '1.40.1'
BINARY_SHA = '46e682c4231bda59a6081920f6d59f83743983801453b278f0f7b1426c0d4096'
APK_SHA = 'cb8134aabc538eca4c7961e2ad34f0538475267e3fe1ca083f921931c706d2c3'
DATA_TABLES = ('users', 'files', 'favorites', 'albums', 'album_photos',
               'album_shares', 'shares', 'photo_meta', 'photo_versions',
               'path_aliases', 'path_alias_history', 'content', 'content_meta',
               'photo_embed', 'file_hashes')
# Startup workers may rebuild these from originals while the new server is healthy.
REBUILDABLE_TABLES = frozenset(('photo_meta', 'content', 'content_meta',
                                'photo_embed', 'file_hashes'))


class Refuse(RuntimeError):
    pass


def sha(path):
    with open(path, 'rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def plain(path, directory=False):
    path = Path(os.path.abspath(path))
    for component in (path, *path.parents):
        if component.is_symlink():
            raise Refuse('Symlinked installation paths are not supported: ' + str(component))
    mode = path.stat().st_mode
    if not (stat.S_ISDIR(mode) if directory else stat.S_ISREG(mode)):
        raise Refuse('Expected a regular file/directory: ' + str(path))
    return path


def write_json(path, value):
    with open(path, 'x', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
        json.dump(value, stream, indent=2, ensure_ascii=False)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())


def atomic_copy(source, destination, metadata=None):
    """Prepare on the destination filesystem, then publish a complete file."""
    destination = Path(destination)
    plain(destination.parent, directory=True)
    if destination.exists():
        plain(destination)
        metadata = destination.stat()
    descriptor, name = tempfile.mkstemp(prefix='.filehub-upgrade-', dir=destination.parent)
    try:
        with os.fdopen(descriptor, 'wb') as stream, open(source, 'rb') as incoming:
            shutil.copyfileobj(incoming, stream, 1 << 20)
            stream.flush()
            if metadata:
                os.fchmod(stream.fileno(), stat.S_IMODE(metadata.st_mode))
                if os.geteuid() == 0:
                    os.fchown(stream.fileno(), metadata.st_uid, metadata.st_gid)
            else:
                os.fchmod(stream.fileno(), 0o644)
            os.fsync(stream.fileno())
        os.replace(name, destination)
        descriptor = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        if os.path.exists(name):
            os.unlink(name)  # Only an unpublished temporary file created above.


def connect(path):
    connection = sqlite3.connect(Path(path).as_uri() + '?mode=ro', uri=True, timeout=5)
    connection.execute('PRAGMA cache_size=-8192')
    deadline = time.monotonic() + 180
    connection.set_progress_handler(lambda: int(time.monotonic() > deadline), 10000)
    return connection


def snapshot(source, destination):
    deadline = time.monotonic() + 180
    def progress(*unused):
        if time.monotonic() > deadline:
            raise Refuse('SQLite backup exceeded its time limit')
    with contextlib.closing(connect(source)) as incoming:
        with contextlib.closing(sqlite3.connect(destination)) as outgoing:
            incoming.backup(outgoing, pages=256, progress=progress, sleep=0.05)
    Path(destination).chmod(0o600)
    with contextlib.closing(connect(destination)) as connection:
        if connection.execute('PRAGMA quick_check').fetchall() != [('ok',)]:
            raise Refuse('SQLite snapshot failed quick_check')


def logical_state(database, tables=DATA_TABLES):
    """Hashes rather than account names, passwords, tokens or personal paths."""
    result = {}
    with contextlib.closing(connect(database)) as connection:
        connection.execute('BEGIN')
        existing = {row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        for table in tables:
            if table not in existing:
                continue
            digest, count = hashlib.sha256(), 0
            for row in connection.execute('SELECT * FROM "' + table + '" ORDER BY rowid'):
                encoded = json.dumps(row, ensure_ascii=True, separators=(',', ':'),
                                     default=lambda value: {'blob': value.hex()}).encode()
                digest.update(len(encoded).to_bytes(8, 'big'))
                digest.update(encoded)
                count += 1
            result[table] = {'rows': count, 'sha256': digest.hexdigest()}
    if result.get('users', {}).get('rows', 0) < 1:
        raise Refuse('Existing Filehub accounts required; use the installer for a new server')
    return result


def release_files(package, root, app):
    package = plain(package, directory=True)
    artifact = package / 'artifacts'
    if sha(plain(artifact / 'filehub')) != BINARY_SHA or sha(plain(artifact / 'app/app.apk')) != APK_SHA:
        raise Refuse('Published server/APK checksum mismatch')
    manifest = json.loads(plain(artifact / 'app/version.json').read_text())
    build = json.loads(plain(artifact / 'filehub-build.json').read_text())
    if (manifest.get('versionName'), manifest.get('versionCode'), manifest.get('sha256')) != (VERSION, 51, APK_SHA):
        raise Refuse('Unexpected Android release manifest')
    if build.get('version') != VERSION or build.get('binarySha256') != BINARY_SHA:
        raise Refuse('Unexpected server build manifest')
    return [(artifact / 'filehub', root / 'filehub'),
            (artifact / 'filehub-build.json', root / 'filehub-build.json'),
            (artifact / 'app/app.apk', app / 'app.apk'),
            (artifact / 'app/version.json', app / 'version.json')]


class SystemService:
    def __init__(self, name):
        if not re.fullmatch(r'[a-zA-Z0-9_-]+\.service', name):
            raise Refuse('Invalid direct systemd service name')
        self.name = name

    def command(self, *arguments):
        result = subprocess.run(['systemctl', *arguments, self.name], capture_output=True, text=True, timeout=45)
        if result.returncode:
            raise Refuse('systemctl ' + arguments[0] + ' failed; inspect the local journal')
        return result.stdout.strip()

    def properties(self):
        output = self.command('show', '-p', 'MainPID', '-p', 'FragmentPath', '-p', 'DropInPaths', '-p', 'ActiveState')
        return dict(line.split('=', 1) for line in output.splitlines() if '=' in line)

    def stop(self):
        self.command('stop')

    def start(self):
        self.command('start')


def inspect(service, expected=None):
    properties = service.properties()
    if properties['ActiveState'] != 'active' or int(properties['MainPID']) <= 0:
        raise Refuse('The existing direct Filehub service must be running')
    process = Path('/proc') / properties['MainPID']
    executable = plain(os.readlink(process / 'exe'))
    if executable.name != 'filehub':
        raise Refuse('This updater supports direct Go services, not the onboarding launcher')
    root = plain(executable.parent, directory=True)
    environment = dict(part.split(b'=', 1) for part in (process / 'environ').read_bytes().split(b'\0') if b'=' in part)
    environment = {k.decode(): v.decode() for k, v in environment.items()}
    database = plain(environment.get('FILEHUB_DB', '/opt/filehub/index.db'))
    app = plain(environment.get('FILEHUB_APP_DIR', '/opt/filehub/app'), directory=True)
    sources = [part.split(':', 1)[1].strip() for part in environment.get('FILEHUB_SOURCES', 'HDD2:/mnt/hdd2').split(',') if ':' in part]
    if not sources or any(not Path(path).is_dir() for path in sources):
        raise Refuse('All configured sources must be mounted and available')
    if Path(str(database) + '.restore').exists():
        raise Refuse('A database restore is pending; resolve it before upgrading')
    current = sha(executable)
    if expected and current != expected:
        raise Refuse('Running server differs from the explicitly selected pre-upgrade checksum')
    configuration = [plain(properties['FragmentPath'])]
    configuration += [plain(path) for path in properties.get('DropInPaths', '').split()]
    # Copy the standard private environment file without parsing or printing it.
    unit_text = '\n'.join(path.read_text() for path in configuration)
    env_paths = re.findall(r'^EnvironmentFile=-?([^\s"\']+)$', unit_text, re.M)
    configuration += [plain(path) for path in env_paths]
    if len(env_paths) != len(re.findall(r'^EnvironmentFile=', unit_text, re.M)):
        raise Refuse('Complex EnvironmentFile syntax needs operator review')
    secret_file = Path(environment.get('FILEHUB_SECRET_FILE', '/opt/filehub/secret'))
    if not environment.get('FILEHUB_SECRET') and secret_file.exists():
        configuration.append(plain(secret_file))
    port = environment.get('PORT', '8080')
    if not port.isdecimal() or not 1 <= int(port) <= 65535:
        raise Refuse('Invalid service port')
    config_hashes = {str(path): sha(path) for path in configuration}
    for path in sources:
        mounted = plain(path, directory=True).stat()
        config_hashes['storage:' + path] = [mounted.st_dev, mounted.st_ino]
    config_hashes['process_environment'] = hashlib.sha256(json.dumps(
        {k: v for k, v in environment.items() if k.startswith('FILEHUB_') or k == 'PORT'},
        sort_keys=True).encode()).hexdigest()
    return {'root': root, 'app': app, 'database': database, 'sources': sources,
            'configuration': configuration, 'configurationHashes': config_hashes,
            'oldSha256': current, 'healthUrl': 'http://127.0.0.1:' + port,
            'service': service.name}


def health(url):
    # Never use system HTTP proxies for loopback health checks.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        try:
            with opener.open(url + '/', timeout=2) as response:
                if response.status != 200 or b'<html' not in response.read(65536).lower():
                    raise Refuse('Filehub web page failed its health check')
            try:
                opener.open(url + '/api/sources', timeout=2).close()
            except urllib.error.HTTPError as error:
                if error.code == 401:
                    return
            raise Refuse('Anonymous private API access must return 401')
        except (OSError, urllib.error.URLError):
            time.sleep(0.25)
    raise Refuse('Filehub did not become healthy within 25 seconds')


def verify_configuration(target, service, expected_binary):
    current = inspect(service, expected_binary)
    if current['configurationHashes'] != target['configurationHashes']:
        raise Refuse('Service configuration changed; automatic operation stopped')


def forecast_space(target, files, backup_parent):
    """Estimate disk space at the point where both old and new files exist."""
    database = target['database']
    with contextlib.closing(connect(database)) as connection:
        pages = connection.execute('PRAGMA page_count').fetchone()[0]
        page_size = connection.execute('PRAGMA page_size').fetchone()[0]
    snapshot_bytes = max(database.stat().st_size, pages * page_size)
    backup_bytes = snapshot_bytes + sum(path.stat().st_size for path in target['configuration'])
    backup_bytes += sum(destination.stat().st_size for _, destination in files)
    planned = {}

    def add(directory, size):
        device = directory.stat().st_dev
        if device not in planned:
            planned[device] = {'path': str(directory), 'freeBytes': shutil.disk_usage(directory).free,
                               'additionalBytes': 0}
        planned[device]['additionalBytes'] += size

    add(backup_parent, backup_bytes)
    for source, destination in files:
        add(destination.parent, source.stat().st_size)
    for volume in planned.values():
        volume['estimatedFreeBytes'] = volume['freeBytes'] - volume['additionalBytes']
    return {'sqliteSnapshotBytes': snapshot_bytes, 'backupBytes': backup_bytes,
            'filesystems': list(planned.values())}


def apply_upgrade(target, files, backup, service, verify=None):
    backup = Path(os.path.abspath(backup))
    if backup.exists():
        raise Refuse('Choose a new, empty backup path')
    plain(backup.parent, directory=True)
    for source in target['sources']:
        if backup == Path(source) or Path(source) in backup.parents:
            raise Refuse('Private backups must be outside every Filehub storage source')
    forecast = forecast_space(target, files, backup.parent)
    if any(volume['estimatedFreeBytes'] < 256 << 20 for volume in forecast['filesystems']):
        raise Refuse('Not enough free space for the SQLite and program backups (256 MiB reserve)')
    backup.mkdir(mode=0o700)
    records = []
    for index, (_, destination) in enumerate(files):
        plain(destination)
        saved = backup / ('artifact-' + str(index))
        shutil.copy2(destination, saved)
        saved.chmod(0o600)
        records.append({'destination': str(destination), 'saved': saved.name, 'sha256': sha(saved),
                        'mode': stat.S_IMODE(destination.stat().st_mode),
                        'uid': destination.stat().st_uid, 'gid': destination.stat().st_gid})
    for index, path in enumerate(target['configuration']):
        saved = backup / ('configuration-' + str(index))
        shutil.copy2(path, saved)
        saved.chmod(0o600)
    record = {'service': service.name, 'oldSha256': target['oldSha256'], 'newSha256': BINARY_SHA,
              'database': str(target['database']), 'configurationHashes': target['configurationHashes'],
              'artifacts': records, 'databaseRestoredByRollback': False}
    write_json(backup / 'transaction.json', record)
    stopped = False
    try:
        service.stop()
        stopped = True
        snapshot(target['database'], backup / 'index.db')
        before = logical_state(backup / 'index.db')
        write_json(backup / 'database-state.json', before)
        for source, destination in files:
            atomic_copy(source, destination)
        service.start()
        health(target['healthUrl'])
        if verify:
            verify(target, service, BINARY_SHA)
        protected = {table: value for table, value in before.items() if table not in REBUILDABLE_TABLES}
        after = logical_state(target['database'], protected)
        changed = [table for table, value in protected.items() if after.get(table) != value]
        if changed:
            raise Refuse('Existing database records changed during startup: ' + ', '.join(changed))
        write_json(backup / 'result.json', {'status': 'PASS', 'version': VERSION,
                   'backupDatabaseSha256': sha(backup / 'index.db'), 'preservedTables': list(protected)})
    except BaseException:
        if stopped:
            try:
                restore_files(backup, service, target['healthUrl'])
                write_json(backup / 'automatic-rollback.json', {'status': 'PASS'})
            except BaseException:
                # Retain all backups and both journals. Never overwrite the DB.
                raise Refuse('Upgrade failed and rollback needs operator attention: ' + str(backup)) from None
        raise
    return backup


def restore_files(backup, service, url):
    backup = plain(backup, directory=True)
    if backup.stat().st_mode & 0o077 or backup.stat().st_uid != os.geteuid():
        raise Refuse('Rollback journal must be private and owned by this operator')
    record = json.loads(plain(backup / 'transaction.json').read_text())
    if record['service'] != service.name:
        raise Refuse('Rollback belongs to another service')
    for item in record['artifacts']:
        if not re.fullmatch(r'artifact-[0-9]+', item['saved']) or sha(plain(backup / item['saved'])) != item['sha256']:
            raise Refuse('Rollback artifact checksum mismatch')
        plain(item['destination'])
    service.stop()
    for item in record['artifacts']:
        atomic_copy(backup / item['saved'], Path(item['destination']))
        os.chmod(item['destination'], item['mode'])
        if os.geteuid() == 0:
            os.chown(item['destination'], item['uid'], item['gid'])
    service.start()
    health(url)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('plan', 'apply', 'rollback'))
    parser.add_argument('--service', default='filehub.service')
    parser.add_argument('--release-dir', type=Path)
    parser.add_argument('--backup', type=Path, help='New private directory, outside all storage sources')
    parser.add_argument('--expect-current-sha256')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run with sudo; the plan never changes the existing installation')
    service = SystemService(args.service)
    target = inspect(service, args.expect_current_sha256)
    # The same lock covers both directions. Never follow a planted lock symlink.
    with contextlib.ExitStack() as stack:
        if args.action != 'plan':
            descriptor = os.open(target['root'] / '.filehub-upgrade.lock',
                                 os.O_CREAT | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
            lock = stack.enter_context(os.fdopen(descriptor, 'w'))
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            target = inspect(service, args.expect_current_sha256)
        execute(args, parser, service, target)


def execute(args, parser, service, target):
    if args.action == 'rollback':
        if not args.backup:
            parser.error('--backup required')
        record = json.loads((args.backup / 'transaction.json').read_text())
        if target['oldSha256'] != record['newSha256'] or target['configurationHashes'] != record['configurationHashes']:
            raise Refuse('Installed version/configuration differs from the rollback journal')
        restore_files(args.backup, service, target['healthUrl'])
        verify_configuration(target, service, record['oldSha256'])
        print('Rollback PASS. Current accounts, database and originals were retained.')
        return
    if not args.release_dir:
        parser.error('--release-dir required')
    files = release_files(args.release_dir, target['root'], target['app'])
    for _, destination in files:
        plain(destination)
    installed = json.loads((target['app'] / 'version.json').read_text())
    if installed.get('versionName') != '1.40' or installed.get('versionCode') != 50:
        raise Refuse('Only the verified direct 1.40 → 1.40.1 upgrade is supported')
    health(target['healthUrl'])
    default = Path('/var/lib/filehub-upgrades')
    backup_parent = args.backup.absolute().parent if args.backup else (default if default.is_dir() else default.parent)
    space = forecast_space(target, files, plain(backup_parent, directory=True))
    print(json.dumps({'service': service.name, 'from': '1.40', 'to': VERSION,
                      'currentServerSha256': target['oldSha256'], 'database': str(target['database']),
                      'replace': [str(destination) for _, destination in files],
                      'spaceForecast': space,
                      'preserves': ['accounts and sessions', 'database', 'source identities',
                                    'photo folders', 'originals', 'units', 'environment', 'backup timer']}, indent=2))
    if args.action == 'plan':
        return
    if not args.backup or not args.expect_current_sha256:
        parser.error('apply requires --backup and the checksum shown by plan')
    verify_configuration(target, service, args.expect_current_sha256)
    apply_upgrade(target, files, args.backup, service, verify_configuration)
    print('Upgrade PASS. Private recovery copy: ' + str(args.backup))


if __name__ == '__main__':
    try:
        main()
    except (Refuse, OSError, sqlite3.Error, ValueError) as error:
        print('REFUSED: ' + str(error), file=sys.stderr)
        sys.exit(1)
