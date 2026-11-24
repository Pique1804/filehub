"""Authenticate the APK bytes before a release starts serving them."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile


PUBLISHED_SIGNER = '15fc6661c4196a4732d7a055140b9d01b697c8fc15ef169617070ba953dff5b2'
MAX_APK_BYTES = 256 * 1024 * 1024


def verified_apk(directory):
    return verified_apk_files(directory / 'app.apk', directory / 'version.json')


def verified_apk_files(apk_path, manifest_path):
    metadata = manifest_path.read_bytes()
    manifest = json.loads(metadata)
    with apk_path.open('rb') as stream:
        apk = stream.read(MAX_APK_BYTES + 1)
    if not apk or len(apk) > MAX_APK_BYTES:
        raise ValueError('APK size is outside the supported range')
    if manifest.get('sha256') != hashlib.sha256(apk).hexdigest():
        raise ValueError('APK does not match its published manifest')
    if 'size' in manifest and manifest['size'] != len(apk):
        raise ValueError('APK size does not match its published manifest')
    with tempfile.TemporaryDirectory(prefix='filehub-apk-') as work:
        snapshot = Path(work) / 'app.apk'
        snapshot.write_bytes(apk)
        certs = subprocess.check_output(['apksigner', 'verify', '--print-certs', str(snapshot)], text=True, timeout=45)
        signers = re.findall(r'^Signer #\d+ certificate SHA-256 digest: ([0-9a-fA-F]{64})$', certs, re.M)
        if [entry.lower() for entry in signers] != [PUBLISHED_SIGNER]:
            raise ValueError('APK was not signed by the established FileHub certificate')
        badging = subprocess.check_output(['aapt', 'dump', 'badging', str(snapshot)], text=True, timeout=30)
        package = re.search(r"^package: name='([^']+)' versionCode='([^']+)' versionName='([^']+)'", badging, re.M)
        if not package or package.groups() != ('com.filehub.backup', str(manifest.get('versionCode')), manifest.get('versionName')):
            raise ValueError('APK package or version does not match the published manifest')
    return apk, metadata, manifest


if __name__ == '__main__':
    try:
        verified_apk(Path(sys.argv[1]))
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        sys.exit(f'APK verification failed: {error}')
