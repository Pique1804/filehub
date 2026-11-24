#!/usr/bin/env python3
"""Publish Android release assets on an installed Linux FileHub."""
import argparse
import ctypes
import fcntl
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'distribution/prebuilt'))
from apk_signature import verified_apk, verified_apk_files

DEFAULT_APP_DIR = Path('/opt/filehub-installed/filehub-onboarding/app')
APP_FILES = ('app.apk', 'version.json')


def exchange_directories(first, second):
    """Switch both files together on Linux, including for legacy FileHub servers."""
    at_fdcwd, rename_exchange = -100, 2
    renameat2 = ctypes.CDLL(None, use_errno=True).renameat2
    renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    if renameat2(at_fdcwd, os.fsencode(first), at_fdcwd, os.fsencode(second), rename_exchange):
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error), str(second))


def publish(app_dir, apk_path, manifest_path):
    if app_dir != app_dir.resolve(strict=True) or not app_dir.is_dir():
        raise ValueError('Use the absolute APK directory from the FileHub installation')
    for path in (app_dir.parent, app_dir):
        info = path.stat()
        if info.st_uid != os.geteuid() or info.st_mode & 0o022:
            raise ValueError(f'APK publication requires a directory controlled by the operator: {path}')
    apk, metadata, release = verified_apk_files(apk_path, manifest_path)
    with os.fdopen(os.open(app_dir.parent / '.filehub-upgrade.lock',
                           os.O_CREAT | os.O_RDONLY | os.O_NOFOLLOW, 0o600), 'rb') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if set(path.name for path in app_dir.iterdir()) != set(APP_FILES):
            raise ValueError('The APK directory must contain only app.apk and version.json')
        existing = [(app_dir / name).lstat() for name in APP_FILES]
        if any(not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or
               info.st_mode & 0o022 for info in existing):
            raise ValueError('Use a private installation directory with ordinary APK files')
        _, _, installed = verified_apk(app_dir)
        current_code, release_code = installed.get('versionCode'), release.get('versionCode')
        if type(current_code) is not int or type(release_code) is not int or release_code <= current_code:
            raise ValueError('The published Android version must be newer than the installed version')

        directory = app_dir.stat()
        with tempfile.TemporaryDirectory(prefix='.filehub-android-', dir=app_dir.parent) as temporary:
            staged = Path(temporary) / 'app'
            staged.mkdir()
            os.chown(staged, directory.st_uid, directory.st_gid)
            staged.chmod(stat.S_IMODE(directory.st_mode))
            for name, data, info in zip(APP_FILES, (apk, metadata), existing):
                with (staged / name).open('xb') as stream:
                    stream.write(data)
                    os.fchown(stream.fileno(), info.st_uid, info.st_gid)
                    os.fchmod(stream.fileno(), stat.S_IMODE(info.st_mode))
                    stream.flush()
                    os.fsync(stream.fileno())
            exchange_directories(staged, app_dir)
    return release['versionName'], release_code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apk', required=True, type=Path, help='Signed APK from a FileHub release')
    parser.add_argument('--manifest', required=True, type=Path, help='Matching app.json from that release')
    parser.add_argument('--app-dir', type=Path, default=DEFAULT_APP_DIR,
                        help=f'Installed directory with app.apk and version.json (default: {DEFAULT_APP_DIR})')
    args = parser.parse_args()
    try:
        version, code = publish(args.app_dir, args.apk, args.manifest)
    except (AttributeError, OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f'Android publication refused: {error}\n')
    print(f'Published Android {version} (versionCode {code})')


if __name__ == '__main__':
    main()
