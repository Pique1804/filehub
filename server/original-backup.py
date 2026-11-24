#!/usr/bin/env python3
"""Independent checked originals backup; never propagates source deletions."""
import datetime
from contextlib import ExitStack, closing
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


def atomic_json(path, value):
    temporary = path.with_name(path.name + '.new')
    with temporary.open('w') as stream:
        os.chmod(temporary, 0o600)
        json.dump(value, stream, ensure_ascii=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    descriptor = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def backup_database(database, target, remaining):
    with closing(sqlite3.connect(Path(database).absolute().as_uri() + '?mode=ro',
                                 uri=True, timeout=5)) as source, closing(sqlite3.connect(target)) as copy:
        source.backup(copy, pages=256, progress=lambda *_: remaining(), sleep=0.05)
        copy.set_progress_handler(lambda: int(remaining() <= 0), 10000)
        if copy.execute('PRAGMA quick_check').fetchall() != [('ok',)]:
            raise RuntimeError('SQLite backup integrity check failed')
    os.chmod(target, 0o600)


def transfer(arguments, remaining, descriptors=()):
    result = subprocess.run(['rsync', *arguments], pass_fds=descriptors,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=remaining())
    if result.returncode:
        raise RuntimeError('Backup transfer/check failed, rsync exit ' + str(result.returncode))
    return bool(result.stdout.strip())


def run_backup(source, versions, base, database, *, require_distinct=True,
               required_mounts=(), timeout=7200):
    source, versions, base, database = map(Path, (source, versions, base, database))
    for mount in required_mounts:
        if not os.path.ismount(mount):
            raise RuntimeError('Required data mount is unavailable')
    if not source.is_dir() or not database.is_file():
        raise RuntimeError('Backup source or database is unavailable')
    base.mkdir(mode=0o700, parents=True, exist_ok=True)
    if require_distinct and base.stat().st_dev == source.stat().st_dev:
        raise RuntimeError('Backup must use a different disk')
    lock = (base / 'lock').open('a')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return _backup_locked(source, versions, base, database, timeout)
    finally:
        lock.close()


def _backup_locked(source, versions, base, database, timeout):
    started = int(time.time())
    deadline = time.monotonic() + timeout
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    status_path = base / 'status.json'
    try:
        previous = json.loads(status_path.read_text())
    except (OSError, ValueError):
        previous = {}
    last_success = previous if previous.get('state') == 'ok' else previous.get('lastSuccess', {})
    last_success = {key: value for key, value in last_success.items() if key != 'lastSuccess'}
    last_completed = last_success.get('completed', previous.get('lastSuccessfulCompleted', 0))

    def remaining():
        seconds = deadline - time.monotonic()
        if seconds <= 0:
            raise TimeoutError('Backup exceeded its two-hour time limit')
        return seconds

    def save(**values):
        result = dict(started=started, updated=int(time.time()), destination=str(base),
                      completed=last_completed, lastSuccessfulCompleted=last_completed,
                      lastSuccess=last_success, checksumVerified=False,
                      versionsChecksumVerified=False)
        result.update(values)
        atomic_json(status_path, result)
        return result

    def rsync(src, dst, checksum=False, dry=False):
        dst.mkdir(mode=0o700, parents=True, exist_ok=True)
        options = ['-rcn', '--out-format=%i'] if dry else [
            '-rtc' if checksum else '-rt', '--backup',
            '--backup-dir=' + str(base / 'changed' / stamp / dst.name)]
        different = transfer([*options, '--safe-links', '--', str(src) + '/', str(dst) + '/'], remaining)
        return dry and different

    def checked_copy(src, dst):
        rsync(src, dst)
        if rsync(src, dst, dry=True):
            # Size/mtime can match even when bytes are corrupt. Repair by checksum.
            rsync(src, dst, checksum=True)
            if rsync(src, dst, dry=True):
                raise RuntimeError('Source changed during verification or checksum mismatch remains')

    try:
        save(state='copying', message='Копирование и проверка оригиналов на отдельном диске')
        checked_copy(source, base / 'Photos')
        versions_present = versions.is_dir()
        if versions_present:
            checked_copy(versions, base / 'edit-versions')
        save(state='checking', message='Проверка согласованной копии базы')
        database_copy = base / ('index-' + stamp + '.db')
        backup_database(database, database_copy, remaining)
        count = size = 0
        for item in source.rglob('*'):
            remaining()
            if item.is_file() and not item.is_symlink():
                count += 1
                size += item.stat().st_size
        completed = int(time.time())
        return save(state='ok', message='Оригиналы, версии и база проверены',
                    completed=completed, lastSuccessfulCompleted=completed,
                    files=count, bytes=size, checksumVerified=True,
                    versionsChecksumVerified=True, versionsPresent=versions_present,
                    databaseIntegrity='ok', databaseSnapshot=database_copy.name)
    except Exception as error:
        message = ('Backup exceeded its two-hour time limit' if isinstance(error, subprocess.TimeoutExpired)
                   else str(error) if isinstance(error, (RuntimeError, TimeoutError))
                   else 'Backup failed: ' + type(error).__name__)
        save(state='error', message=message)
        raise


DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
BACKUP_DIRECTORY = '.filehub-server-backups'
SNAPSHOT = re.compile(r'snapshot-[0-9]{8}T[0-9]{12}Z')


def own_directory(stack, parent, name):
    try:
        os.mkdir(name, 0o700, dir_fd=parent)
    except FileExistsError:
        pass
    fd = os.open(name, DIRECTORY, dir_fd=parent)
    stack.callback(os.close, fd)
    info = os.fstat(fd)
    if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise RuntimeError('Backup directory is not private')
    return fd


def same_directory(info, directory):
    return (info.st_dev, info.st_ino) == (directory['device'], directory['inode'])


def pin_directory(stack, directory):
    fd = os.open(directory['fullPath'], DIRECTORY)
    stack.callback(os.close, fd)
    if not same_directory(os.fstat(fd), directory):
        raise RuntimeError('A selected storage directory has changed')
    return fd


def path_key(value):
    return hashlib.sha256(value.encode()).hexdigest()


def copy_versions(plan, database, stage, remaining, stack):
    scopes = {}
    for entry in plan['folders']:
        scopes.setdefault(entry['source'], []).append(entry['path'])
    directories = {}
    copied = set()
    with closing(sqlite3.connect(database)) as snapshot:
        for source, path, stored_source, stored, digest in snapshot.execute(
                'SELECT source,path,stored_source,stored,sha256 FROM photo_versions'):
            remaining()
            if not any(not folder or path.startswith(folder + '/') for folder in scopes.get(source, [])):
                continue
            if not stored or stored in ('.', '..') or any(char in stored for char in '/\\\x00'):
                raise RuntimeError('A saved version filename is invalid')
            key = (stored_source, stored, digest)
            if key in copied:
                continue
            if stored_source not in directories:
                root_fd = pin_directory(stack, plan['roots'][stored_source])
                version_fd = os.open('.filehub-versions', DIRECTORY, dir_fd=root_fd)
                stack.callback(os.close, version_fd)
                directories[stored_source] = version_fd
            out = stage / 'versions' / path_key(stored_source) / stored
            out.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            fd = os.open(stored, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                         dir_fd=directories[stored_source])
            with os.fdopen(fd, 'rb') as original:
                if not stat.S_ISREG(os.fstat(original.fileno()).st_mode):
                    raise RuntimeError('A saved version is unavailable')
                actual = hashlib.sha256()
                with out.open('xb') as copy:
                    for chunk in iter(lambda: original.read(1 << 20), b''):
                        remaining()
                        copy.write(chunk)
                        actual.update(chunk)
                    copy.flush()
                    os.fsync(copy.fileno())
                if actual.hexdigest() != digest.lower():
                    raise RuntimeError('A saved version failed SHA-256 verification')
            copied.add(key)
    return len(copied)


def run_configured_backup(plan, timeout=7200):
    deadline = time.monotonic() + timeout

    def remaining():
        seconds = deadline - time.monotonic()
        if seconds <= 0:
            raise TimeoutError('Backup exceeded its time limit')
        return seconds

    if not re.fullmatch(r'[a-f0-9]{32}', plan['instance']):
        raise RuntimeError('Invalid FileHub backup instance')
    with ExitStack() as stack:
        destination = pin_directory(stack, plan['destination'])
        private = own_directory(stack, destination, BACKUP_DIRECTORY)
        directory = own_directory(stack, private, plan['instance'])
        lock = os.open('lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600, dir_fd=directory)
        stack.callback(os.close, lock)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        base = Path(f'/proc/self/fd/{directory}')
        previous = max((p for p in base.iterdir() if SNAPSHOT.fullmatch(p.name)
                        and p.is_dir() and not p.is_symlink()), default=None)
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        name = 'snapshot-' + stamp
        with tempfile.TemporaryDirectory(prefix='.pending-', dir=base) as partial:
            stage = Path(partial)
            database = stage / 'index.db'
            backup_database(plan['database'], database, remaining)
            entries = []
            for item in plan['folders']:
                source = pin_directory(stack, item)
                relative = Path('sources') / path_key(item['source']) / item['path']
                copy = stage / relative
                copy.mkdir(parents=True, mode=0o700)
                args = ['-rtc', '--fsync', '--exclude=.filehub-*']
                if previous and (previous / relative).is_dir():
                    args.append('--link-dest=' + str(previous / relative))
                paths = ['--', f'/proc/self/fd/{source}/', str(copy) + '/']
                transfer([*args, *paths], remaining, (source, directory))
                if transfer(['-rnc', '--exclude=.filehub-*', '--out-format=%i', *paths],
                            remaining, (source, directory)):
                    raise RuntimeError('An original changed during verification')
                if not same_directory(os.stat(item['fullPath'], follow_symlinks=False), item):
                    raise RuntimeError('A source was disconnected during verification')
                entries.append({'source': item['source'], 'path': item['path'], 'copy': str(relative)})
            versions = copy_versions(plan, database, stage, remaining, stack)
            atomic_json(stage / 'manifest.json', {'folders': entries, 'database': 'index.db',
                         'versions': versions, 'versionSourceKeys': {
                             source: path_key(source) for source in plan['roots']}})
            if not same_directory(os.stat(plan['destination']['fullPath'], follow_symlinks=False),
                                  plan['destination']):
                raise RuntimeError('The backup storage was disconnected')
            stage.rename(base / name)
            os.fsync(directory)
    return {'snapshot': name, 'versions': versions}


if __name__ == '__main__':
    os.umask(0o077)
    if sys.argv[1:] == ['--configured']:
        print(json.dumps(run_configured_backup(json.load(sys.stdin))))
    else:
        run_backup('/mnt/hdd2/Photos', '/mnt/hdd2/.filehub-versions',
                   '/mnt/data/.filehub-originals-backup', '/opt/filehub/index.db',
                   required_mounts=('/mnt/data', '/mnt/hdd2'))
