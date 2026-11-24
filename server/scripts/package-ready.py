#!/usr/bin/env python3
"""Archive already-published artifacts and the installer. Never compiles code."""
import argparse
import hashlib
import io
import json
import re
import subprocess
from pathlib import Path
import tarfile
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'distribution/prebuilt'))
from mounting import build_bundle, sign_host_tools
from apk_signature import PUBLISHED_SIGNER, verified_apk
from source_identity import collect_sources


def installer_revision_sources(repo, base_commit, commit, build_sources):
    subprocess.run(['git', 'merge-base', '--is-ancestor', base_commit, commit],
                   cwd=repo, check=True)
    changes = subprocess.check_output(
        ['git', 'diff', '--no-renames', '--name-only', '-z', base_commit, commit],
        cwd=repo, text=True,
    ).split('\0')
    installer_folders = ('docs/', 'server/distribution/prebuilt/',
                         'server/host-storage/', 'server/clip-sidecar/')
    installer_files = {'AGENTS.md', 'CHANGELOG.md', 'README.md', 'README.ru.md',
                       'server/scripts/package-ready.py'}
    for path in filter(None, changes):
        if path not in installer_files and not path.startswith(installer_folders):
            raise ValueError(f'Installer revision requires a new build because {path} changed')

    def compiled_inputs(sources):
        return {path: digest for path, digest in sources.items()
                if path != 'scripts/package-ready.py' and not path.startswith('clip-sidecar/')}

    if compiled_inputs(build_sources) != compiled_inputs(collect_sources(repo/'server')):
        raise ValueError('Previous build inputs differ from the current release source')


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--binary',required=True)
    parser.add_argument('--app-dir',required=True)
    parser.add_argument('--server-manifest',help='filehub-build.json from the same verified build')
    parser.add_argument('--reuse-release-artifacts', action='store_true',
                        help='Reuse verified artifacts from an ancestor with unchanged compiled inputs')
    parser.add_argument('--host-tools-signing-key', type=Path,
                        help='Optional release signing key in PEM format for a standalone host kit')
    parser.add_argument('--output',required=True)
    args=parser.parse_args()
    repo=Path(__file__).resolve().parents[2]
    binary=Path(args.binary);app=Path(args.app_dir);destination=Path(args.output)
    if destination.exists():parser.error('Output already exists; refusing to overwrite')
    try:
        apk_bytes, apk_metadata, version = verified_apk(app)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.error(f'APK verification failed: {error}')
    expected=json.loads((repo/'server/release.json').read_text())['version']
    if version.get('versionName')!=expected:parser.error('APK version differs from the source release')
    manifest_path=Path(args.server_manifest) if args.server_manifest else binary.parent/'filehub-build.json'
    if not manifest_path.is_file():parser.error('Verified server build manifest required; old prebuilt binary cannot carry source fixes')
    manifest=json.loads(manifest_path.read_text())
    if manifest.get('binarySha256')!=hashlib.sha256(binary.read_bytes()).hexdigest():parser.error('Server binary does not match build manifest')
    if manifest.get('version')!=expected:parser.error('Server version differs from the source release')
    changed=subprocess.check_output(['git','diff','--name-only','HEAD','--'],cwd=repo,text=True)
    if changed:parser.error('Commit the tested installer changes before packaging')
    untracked = subprocess.check_output(
        ['git', 'ls-files', '--others', '--exclude-standard', '--',
         'server/distribution/prebuilt', 'server/host-storage', 'server/scripts', 'docs'],
        cwd=repo, text=True,
    )
    if untracked:parser.error('Remove untracked installer sources from the packaging checkout')
    commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
    build_commit = manifest.get('sourceCommit', '')
    if args.reuse_release_artifacts:
        if len(build_commit) != 40 or any(char not in '0123456789abcdef' for char in build_commit):
            parser.error('Installer revision requires a full build commit hash')
        try:
            installer_revision_sources(repo, build_commit, commit, manifest['sourceFiles'])
        except (KeyError, ValueError, subprocess.CalledProcessError) as error:
            parser.error(str(error))
    elif build_commit != commit or manifest.get('sourceFiles') != collect_sources(repo/'server'):
        parser.error('Server build must match the release commit and source files')
    android_commit = version.get('sourceCommit')
    if not isinstance(android_commit, str) or not re.fullmatch(r'[0-9a-f]{40}', android_commit):
        parser.error('Android manifest requires a full source commit hash')
    if android_commit != commit:
        reusable = args.reuse_release_artifacts and android_commit == build_commit
        if not reusable:
            parser.error('Android build must match the release commit or the verified base release')
    files={}
    for folder in ('server/distribution/prebuilt','server/host-storage'):
        for path in (repo/folder).rglob('*'):
            if path.is_file() and '__pycache__' not in path.parts and path.suffix in ('.py','.js','.css','.html','.sh','.service','.md','.woff2','.txt'):
                files[str(path.relative_to(repo))]=path.read_bytes()
    for path in (repo/'docs').rglob('*'):
        if path.is_file() and path.suffix in ('.png','.jpg','.svg','.txt','.mp4') and not path.is_symlink():
            files[str(path.relative_to(repo))]=path.read_bytes()
    for script in ('install-ready.sh', 'upgrade-installed.py', 'upgrade-ready.py', 'rollback-retention.py', 'publish-android.py'):
        leaf = 'server/scripts/' + script
        files[leaf] = (repo/leaf).read_bytes()
    if args.host_tools_signing_key:
        kit = build_bundle(repo)
        try:
            signature = sign_host_tools(kit, args.host_tools_signing_key.resolve(strict=True))
        except (OSError, subprocess.CalledProcessError) as error:
            parser.error(f'Could not sign the host tools: {error}')
        files['artifacts/host-tools.tar.gz'] = kit
        files['artifacts/host-tools.tar.gz.sig'] = signature
        files['server/scripts/connect-host.sh'] = (repo/'server/scripts/connect-host.sh').read_bytes()
    for leaf in ('README.md', 'README.ru.md', 'CHANGELOG.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'SECURITY.md', 'CONTRIBUTING.md', 'docs/resources.md', 'docs/installation.md', 'docs/faq.md', 'docs/faq.ru.md', 'docs/android-screenshots.md', 'docs/storage.md', 'docs/development.md', 'docs/release-verification-1.40.1.md', 'docs/screenshot-credits.md', 'docs/maps-and-media.md', 'docs/release-verification-1.40.2.md', 'docs/upgrading.md', 'docs/compose.md', 'server/distribution/filehub-rollback-retention.service', 'server/distribution/filehub-rollback-retention.timer'):
        path=repo/leaf
        if path.is_file():files[leaf]=path.read_bytes()
    for leaf in ('docs/installation.en.md', 'docs/faq.md', 'docs/faq.ru.md',
                 'docs/android-screenshots.md', 'docs/semantic-search-evaluation.md',
                 'docs/release-verification-android-1.40.3.md', 'docs/android-performance.md',
                 'android/README.md', 'server/README.md', 'server/BUILD.md',
                 'server/clip-sidecar/README.md', 'server/clip-sidecar/onnx_server.py',
                 'server/clip-sidecar/requirements.txt', 'server/clip-sidecar/sidecar.py',
                 'server/clip-sidecar/text_only.py',
                 'server/frontend/public/licenses/Material-Symbols-LICENSE.txt'):
        files[leaf] = (repo / leaf).read_bytes()
    files['artifacts/filehub']=binary.read_bytes()
    files['artifacts/filehub-build.json']=manifest_path.read_bytes()
    files['artifacts/app/app.apk'] = apk_bytes
    files['artifacts/app/version.json'] = apk_metadata
    release = {'sourceCommit': commit, 'serverVersion': expected, 'androidVersion': version['versionName'],
               'serverBuildCommit': build_commit, 'serverSha256': manifest['binarySha256'],
               'androidBuildCommit': android_commit, 'androidSha256': version['sha256'],
               'androidSigningCertSha256': PUBLISHED_SIGNER,
               'architecture': 'x86_64', 'builds': False}
    if args.reuse_release_artifacts:
        release['baseReleaseCommit'] = build_commit
    files['release.json'] = json.dumps(release, indent=2).encode() + b'\n'
    files['install.sh']=b'''#!/usr/bin/env bash
set -euo pipefail
bundle_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$bundle_dir"
sha256sum --check --quiet SHA256SUMS
if [[ ${1-} == --help ]]; then
  echo 'Linux: sudo ./install.sh [--url http://HOST:8080]'
  echo 'Proxmox LXC: sudo ./install.sh --proxmox CTID --restart'
  echo 'Upgrade: sudo ./install.sh --upgrade plan --backup /path/to/new-backup'
  echo 'Uses checked Filehub server/APK artifacts and installs host disk management. No builds.'
  exit 0
fi
if [[ ${1-} == --upgrade ]]; then
  shift
  exec python3 server/scripts/upgrade-installed.py "$@"
fi
if [[ ${1-} == --proxmox ]]; then
  ct_id=${2:?Specify the existing container ID}; shift 2
  exec python3 server/host-storage/proxmox.py --ctid "$ct_id" --artifacts "$bundle_dir/artifacts" --install-dependencies "$@"
fi
exec bash server/scripts/install-ready.sh --install-dependencies --binary "$bundle_dir/artifacts/filehub" --app-dir "$bundle_dir/artifacts/app" "$@"
'''
    files['SHA256SUMS']=''.join(hashlib.sha256(data).hexdigest()+'  '+name+'\n' for name,data in sorted(files.items())).encode()
    with destination.open('xb') as output:
        with tarfile.open(fileobj=output,mode='w:gz') as archive:
            for name,data in sorted(files.items()):
                info=tarfile.TarInfo('filehub/'+name);info.size=len(data)
                info.mode=0o755 if name in ('install.sh','artifacts/filehub') else 0o644
                archive.addfile(info,io.BytesIO(data))
    print(json.dumps({'archive':str(destination),'bytes':destination.stat().st_size,
                      'sha256':hashlib.sha256(destination.read_bytes()).hexdigest(),
                      'serverVersion':expected,'androidVersion':version['versionName'],'builds':False},indent=2))


if __name__=='__main__':main()
