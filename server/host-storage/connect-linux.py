#!/usr/bin/env python3
"""Add disk management to an existing native installation. No disk is mounted here."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--name', default='filehub-onboarding')
    parser.add_argument('--restart', action='store_true')
    args = parser.parse_args()
    if os.geteuid() != 0 or not args.restart:
        parser.error('Run as root; pass --restart to permit restarting Filehub')
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', args.name):
        parser.error('Invalid service name')
    if subprocess.run(['systemd-detect-virt', '--container', '--quiet']).returncode == 0:
        parser.error('Run on a native Linux host; use the Proxmox connection for LXC')
    catalog = Path('/etc') / (args.name + '.json')
    unit = Path('/etc/systemd/system') / (args.name + '.service')
    if not catalog.is_file() or not unit.is_file():
        parser.error('Existing Filehub installation not found')
    data = json.loads(catalog.read_text())
    if data.get('storage_socket') not in (None, '/run/filehub-storage/control.sock'):
        parser.error('A different disk connection is configured; inspect it before changing')
    helper = Path('/etc/systemd/system/filehub-storage.service')
    if helper.exists() and ('--allow-' in helper.read_text() or '--base /srv/filehub-disks ' not in helper.read_text()):
        parser.error('Custom host disk policy found; preserve it and repair the existing service')
    backup = Path('/var/backups') / ('filehub-connect-' + time.strftime('%Y%m%d-%H%M%S'))
    backup.mkdir(mode=0o700)
    shutil.copy2(catalog, backup / catalog.name)
    shutil.copy2(unit, backup / unit.name)
    subprocess.run(['python3', str(Path(__file__).with_name('install.py')), '--update'], check=True)
    data['storage_socket'] = '/run/filehub-storage/control.sock'
    temporary = catalog.with_suffix('.connecting-' + str(os.getpid()))
    with temporary.open('x') as stream:
        json.dump(data, stream, ensure_ascii=False, indent=2)
    temporary.chmod(0o644)
    os.replace(temporary, catalog)
    dropin = unit.with_name(unit.name + '.d')
    dropin.mkdir(exist_ok=True)
    target = dropin / 'host-storage.conf'
    if target.exists():shutil.copy2(target, backup / target.name)
    target.write_text('[Unit]\nWants=filehub-storage.service\nAfter=filehub-storage.service\n[Service]\nReadWritePaths=/srv/filehub-disks\n')
    subprocess.run(['systemctl', 'daemon-reload'], check=True)
    subprocess.run(['systemctl', 'restart', args.name + '.service'], check=True)
    subprocess.run(['systemctl', 'start', args.name + '-front.service'], check=True)
    print('Host connection ready. Return to Filehub and check the disks. Backup: ' + str(backup))


if __name__ == '__main__':main()
