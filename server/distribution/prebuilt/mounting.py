"""Portable host connection diagnostics. No browser-supplied commands."""
import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile


def environment():
    marker = Path('/run/systemd/container')
    value = marker.read_text().strip() if marker.is_file() else os.environ.get('container', '')
    if value in ('lxc', 'lxc-libvirt'):
        return 'lxc'
    if Path('/.dockerenv').exists() or value in ('docker', 'podman'):
        return 'docker'
    return 'linux'


def info(catalog):
    return {'environment': environment(), 'serverUrl': catalog['public_url'],
            'storageHostConfigured': bool(catalog.get('storage_socket'))}


def sign_host_tools(bundle, private_key):
    with tempfile.TemporaryDirectory(prefix='filehub-sign-') as work:
        archive = Path(work) / 'host-tools.tar.gz'
        signature = Path(work) / 'host-tools.tar.gz.sig'
        archive.write_bytes(bundle)
        subprocess.run(['openssl', 'dgst', '-sha256', '-sign', str(private_key),
                        '-out', str(signature), str(archive)], check=True)
        return signature.read_bytes()


def build_bundle(repo):
    """Explicit source directories only: no state, binaries or credentials."""
    files = {}
    repo = Path(repo)
    for notice in ('LICENSE', 'docs/licenses/Golos-Text-OFL.txt'):
        path = repo / notice
        if path.is_file():
            files[notice] = path.read_bytes()
    for folder in ('server/host-storage', 'server/distribution/prebuilt'):
        for path in sorted((repo / folder).rglob('*')):
            if not path.is_file() or path.is_symlink() or '__pycache__' in path.parts:
                continue
            if path.name.startswith('test_') or path.name == 'host-tools.tar.gz':
                continue
            if path.suffix in ('.py', '.sh', '.html', '.css', '.js', '.md', '.woff2', '.txt'):
                files[str(path.relative_to(repo))] = path.read_bytes()
    files['connect.sh'] = b'''#!/usr/bin/env bash
set -euo pipefail
bundle_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$bundle_dir"
sha256sum --check --quiet SHA256SUMS
if [[ ${1-} == --linux ]]; then
  shift
  exec python3 server/host-storage/connect-linux.py "$@"
fi
exec python3 server/host-storage/proxmox.py "$@"
'''
    files['SHA256SUMS'] = ''.join(hashlib.sha256(data).hexdigest()+'  '+name+'\n'
                                for name, data in sorted(files.items())).encode()
    output = io.BytesIO()
    # Freeze the gzip timestamp too, so the displayed checksum stays stable.
    import gzip
    with gzip.GzipFile(fileobj=output, mode='wb', mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode='w', format=tarfile.PAX_FORMAT) as archive:
            for name, data in sorted(files.items()):
                item = tarfile.TarInfo('filehub-host-tools/' + name)
                item.size = len(data)
                item.mode = 0o755 if name == 'connect.sh' else 0o644
                archive.addfile(item, io.BytesIO(data))
    return output.getvalue()
