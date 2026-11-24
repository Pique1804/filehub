#!/usr/bin/env python3
"""Install a first-run UI around an already published binary. Never builds."""
import argparse
import json
import os
from pathlib import Path
import pwd
import shutil
import socket
import platform
import subprocess
import sys
from urllib.parse import urlsplit

from launcher import bootstrap_code
from storage_model import mount_table, overlaps_state, volume_identity
from apk_signature import verified_apk

HERE = Path(__file__).resolve().parent


def show_setup_summary(public_url, code, state, *, started):
    setup_url = urlsplit(public_url).geturl().rstrip('/') + '/setup#code=' + code
    heading = 'FileHub установлен' if started else 'Конфигурация FileHub готова'
    first_step = 'Откройте мастер в браузере.' if started else 'Откройте мастер после запуска служб.'
    link = setup_url
    if sys.stdout.isatty() and os.environ.get('TERM') != 'dumb':
        heading = f'\x1b[1m{heading}\x1b[0m'
        link = f'\x1b]8;;{setup_url}\x1b\\\x1b[1;36m{setup_url}\x1b[0m\x1b]8;;\x1b\\'

    print(
        f'\n{heading}\n\n'
        f'1. {first_step}\n'
        f'   {link}\n'
        '2. Создайте аккаунт и выберите папку для файлов.\n\n'
        'Ссылка содержит код настройки. Сохраните её у себя.\n'
        f'Код также хранится в {state / "setup-code.json"}.\n'
    )


def render_nginx(catalog, prefix, listen):
    public = urlsplit(catalog['public_url'])
    host = public.hostname
    forwarded_scheme = 'https' if public.scheme == 'https' else '$scheme'
    upstream = 'http://unix:' + catalog['socket'] + ':'
    install = f'proxy_pass {upstream}; proxy_set_header Host $http_host; proxy_set_header X-Real-IP $remote_addr;'
    return f'''worker_processes 1;
pid {prefix}/nginx.pid;
error_log stderr warn;
events {{ worker_connections 128; }}
http {{
  access_log off;
  client_max_body_size 0;
  client_body_temp_path {prefix}/client;
  proxy_temp_path {prefix}/proxy;
  server {{
    listen {listen} default_server;
    server_name _;
    return 444;
  }}
  server {{
    listen {listen};
    server_name {host};
    location = /_fh_gate {{
      internal;
      proxy_pass {upstream}/api/onboarding/gate;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_pass_request_body off;
      proxy_set_header Content-Length "";
    }}
    location = /setup {{ {install} }}
    location = /connect {{ {install} }}
    location = /storage {{ {install} }}
    location = /api/connection {{
      proxy_pass {upstream}/api/onboarding/connection;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
    }}
    location = /api/sources {{
      proxy_pass {upstream}/api/onboarding/sources;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
    }}
    location = / {{
      auth_request /_fh_gate;
      error_page 401 = /setup;
      error_page 403 = /storage;
      proxy_pass http://127.0.0.1:{catalog['backend_port']};
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto {forwarded_scheme};
      sub_filter_once on;
      sub_filter '</body>' '<a href="/storage" style="position:fixed;right:16px;bottom:70px;z-index:100;background:#006DE2;color:white;border-radius:10px;padding:10px 16px;font:600 12px Golos Text,system-ui;text-decoration:none">Хранилища и телефон</a></body>';
    }}
    location /onboarding/ {{ {install} }}
    location /api/onboarding/ {{ {install} client_max_body_size 64k; }}
    location / {{
      auth_request /_fh_gate;
      error_page 401 = /setup;
      error_page 403 =503 /api/onboarding/offline;
      proxy_pass http://127.0.0.1:{catalog['backend_port']};
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto {forwarded_scheme};
      proxy_http_version 1.1;
      proxy_set_header Connection "";
      proxy_buffering off;
      proxy_request_buffering off;
      proxy_read_timeout 3600s;
      sub_filter_once on;
      sub_filter '</body>' '<a href="/connect" style="position:fixed;right:16px;bottom:70px;z-index:100;background:#006DE2;color:white;border-radius:10px;padding:10px 16px;font:600 12px Golos Text,system-ui;text-decoration:none;box-shadow:0 2px 8px #0002">Телефон и хранилища</a></body>';
    }}
  }}
}}
'''


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--app-dir', required=True)
    parser.add_argument('--url', help='Public LAN URL; detected from the default route if omitted')
    parser.add_argument('--state')
    parser.add_argument('--storage')
    parser.add_argument('--storage-name', default='Папка сервера')
    parser.add_argument('--storage-description', default='Файлы хранятся на сервере Filehub')
    parser.add_argument('--name', default='filehub-onboarding')
    parser.add_argument('--port', type=int, default=8080)
    parser.add_argument('--backend-port', type=int, default=18081)
    parser.add_argument('--listen-address', default='0.0.0.0')
    parser.add_argument('--storage-socket', help='Optional host Filehub Storage Unix socket')
    parser.add_argument('--with-host-storage', action='store_true', help='Install the optional mount service on a standalone Linux host')
    parser.add_argument('--folders-only', action='store_true', help='Explicitly install without physical disk management')
    parser.add_argument('--generate-only', action='store_true')
    args = parser.parse_args()
    args.state=args.state or '/var/lib/'+args.name
    args.storage=args.storage or ('/srv/filehub-storage' if args.name=='filehub-onboarding' else '/srv/'+args.name)
    if os.geteuid() != 0:
        parser.error('Run installer as root; services themselves use the filehub account.')
    for dependency in ('nginx','nft','lsblk','mount','findmnt','systemctl'):
        if not shutil.which(dependency):parser.error('Missing system package: '+dependency+'. Use install-ready.sh --install-dependencies ...')
    from passwords import crypt
    if not crypt.crypt('preflight',crypt.mksalt()).startswith('$2b$'):
        parser.error('Install libcrypt1 with bcrypt support')
    container = subprocess.run(['systemd-detect-virt','--container','--quiet']).returncode == 0
    if args.folders_only and (args.with_host_storage or args.storage_socket):
        parser.error('--folders-only cannot be combined with disk management')
    if not args.folders_only and not args.storage_socket and not container:
        args.with_host_storage = True
    if not args.folders_only and not args.storage_socket and container:
        if Path('/srv/filehub-storage-control/control.sock').is_socket():
            args.storage_socket='/srv/filehub-storage-control/control.sock'
        elif Path('/run/filehub-storage/control.sock').is_socket():
            args.storage_socket='/run/filehub-storage/control.sock'
        else:
            print('Host disks are not connected yet. Web setup will guide the host connection; the server folder is already usable.')
    if not args.url:
        connection=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
        try:
            connection.connect(('1.1.1.1',53))
            address=connection.getsockname()[0]
        except OSError:
            parser.error('Cannot detect LAN address: pass --url http://YOUR-SERVER:8080')
        finally:connection.close()
        args.url=f'http://{address}:{args.port}'
    if args.with_host_storage:
        if args.storage_socket:
            parser.error('Choose --with-host-storage or an existing --storage-socket, not both')
        detection = subprocess.run(['systemd-detect-virt', '--container', '--quiet'])
        if detection.returncode == 0:
            parser.error('Install host storage on the Linux host, not inside Docker/LXC')
        args.storage_socket = '/run/filehub-storage/control.sock'
    import re
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', args.name):
        parser.error('Invalid service name')
    parsed = urlsplit(args.url)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment:
        parser.error('Use http://host:port for LAN access or https://host for a local reverse proxy.')
    if not re.fullmatch(r'[a-zA-Z0-9.-]+', parsed.hostname):
        parser.error('Invalid server hostname')
    if not 1024 <= args.port <= 65535 or not 1024 <= args.backend_port <= 65535 or args.port == args.backend_port:
        parser.error('Invalid ports')
    if parsed.scheme == 'http' and parsed.port != args.port:
        parser.error('Public URL port must match --port')
    if parsed.scheme == 'https' and args.listen_address != '127.0.0.1':
        parser.error('Use --listen-address 127.0.0.1 with an HTTPS reverse proxy on this host.')
    for value in (args.binary, args.app_dir, args.state, args.storage, *([args.storage_socket] if args.storage_socket else [])):
        if not os.path.isabs(value) or any(c in value for c in ' \t\r\n:;,{}'):
            parser.error('Use absolute simple paths without spaces or configuration delimiters')
    binary, app = Path(args.binary), Path(args.app_dir)
    if not binary.is_file() or not os.access(binary, os.X_OK) or not (app / 'app.apk').is_file() or not (app / 'version.json').is_file():
        parser.error('Published Filehub binary, app.apk and version.json required')
    try:
        apk_bytes, apk_metadata, version = verified_apk(app)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.error(f'APK verification failed: {error}')
    if not re.fullmatch(r'1\.40(?:\.[0-9]+)?', version.get('versionName', '')):
        parser.error('Unsupported Android API compatibility version')
    header=binary.read_bytes()[:20]
    architecture={'x86_64':62,'aarch64':183}.get(platform.machine())
    if header[:4]!=b'\x7fELF' or len(header)<20 or int.from_bytes(header[18:20],'little')!=architecture:
        parser.error('Published server binary does not match this CPU architecture')
    for address,port in ((args.listen_address,args.port),('127.0.0.1',args.backend_port)):
        probe=socket.socket()
        try:probe.bind((address,port))
        except OSError:parser.error('Port unavailable: '+str(port))
        finally:probe.close()
    # Validate all installation destinations before creating accounts/directories.
    reserved=[Path('/etc/'+args.name+'.json'),Path('/etc/'+args.name+'-nginx.conf'),
              Path('/etc/systemd/system/'+args.name+'.service'),Path('/etc/systemd/system/'+args.name+'-front.service'),
              Path('/etc/systemd/system/'+args.name+'-backend-private.service'),Path('/opt/filehub-installed')/args.name]
    if any(path.exists() for path in reserved):parser.error('Existing installation found; it was not changed')
    private_name = args.name + '-backend-private'
    rules = Path('/etc/' + private_name + '.nft')
    table = 'fh_' + args.name.replace('-', '_') + '_' + str(args.backend_port)
    rules_text = f'''add table inet {table}
flush table inet {table}
table inet {table} {{
 chain input {{
  type filter hook input priority -10; policy accept;
  iifname != "lo" tcp dport {args.backend_port} counter reject with tcp reset
 }}
}}
'''
    try:
        subprocess.run(['nft', '--check', '--file', '-'], input=rules_text, text=True, check=True)
    except (OSError, subprocess.CalledProcessError):
        parser.error('Firewall check failed. Fix the nftables error and run the installer again.')
    try:
        account = pwd.getpwnam('filehub')
    except KeyError:
        subprocess.run(['useradd', '--system', '--home-dir', args.state, '--no-create-home', '--shell', '/usr/sbin/nologin', 'filehub'], check=True)
        account = pwd.getpwnam('filehub')
    state, storage = Path(args.state), Path(args.storage)
    mounts = mount_table()
    if overlaps_state(storage.resolve(), state.resolve(), mounts):
        parser.error('State and storage must be separate directories')
    if state.exists() and any(state.iterdir()):
        parser.error('State directory is not empty. Refusing to replace an existing installation.')
    for target in (state, storage):
        if target.is_symlink():
            parser.error('Symlinked directories are not supported')
        if not target.exists():
            target.mkdir(parents=True, mode=0o750)
            os.chown(target, account.pw_uid, account.pw_gid)
        elif target == state:
            os.chown(target, account.pw_uid, account.pw_gid)
    code = bootstrap_code(state)
    os.chown(state / "setup-code.json", account.pw_uid, account.pw_gid)
    mount = storage
    while str(mount) not in mounts:
        mount = mount.parent
    storages = [{'id': 'server', 'name': args.storage_name, 'root': str(storage),
                 'mount': str(mount), 'description': args.storage_description}]
    # Registered mount points are choices, never imported or recursively chowned.
    # The test CT has no such mounts; on a standalone machine /mnt and /media
    # data disks appear automatically. Ignore system mounts and bind internals.
    for line in Path('/proc/self/mountinfo').read_text().splitlines():
        before, after = line.split(' - ', 1)
        fields, device = before.split(), after.split()
        target = fields[4]
        if target == '/srv/filehub-storage-control' or target.startswith('/srv/filehub-storage-control/'):
            continue  # Never expose the privileged service's control directory.
        if args.storage_socket and Path(target) == Path(args.storage_socket).parent:
            continue
        if args.storage_socket and (target == '/srv/filehub-disks' or target.startswith('/srv/filehub-disks/')):
            continue  # Dynamic host-managed mounts must retain their UUID identity.
        if not target.startswith(('/mnt/', '/media/', '/srv/')) or '\\' in target:
            continue
        if device[0] not in ('ext4', 'xfs', 'btrfs', 'zfs', 'exfat', 'ntfs3', 'cifs', 'nfs', 'nfs4'):
            continue
        root = Path(target)
        if root == storage or root.is_symlink() or not root.is_dir():
            continue
        if overlaps_state(root.resolve(), state.resolve(), mounts):
            continue
        if any(c in target for c in ':,\r\n'):
            continue
        storages.append({'id': f'disk{len(storages)}', 'name': root.name,
                         'root': target, 'mount': target, 'description': 'Подключённый диск'})
    # UUIDs identify local mounted volumes across device-name changes and reboots.
    for entry in storages:
        if entry['mount'] == '/':
            continue
        result = subprocess.run(['findmnt', '--noheadings', '--output', 'UUID', '--target', entry['root']], capture_output=True, text=True)
        uuid = result.stdout.strip() if result.returncode == 0 else ''
        if re.fullmatch(r'[A-Za-z0-9-]{4,80}', uuid):
            entry['uuid'] = uuid
        else:
            identity = volume_identity(mounts.get(entry['mount'], {}))
            if identity is not None:
                entry['mountIdentity'] = identity
    runtime_dir = '/run/' + args.name
    catalog = {'version': 1, 'service_name': args.name, 'public_url': args.url.rstrip('/'), 'binary': str(binary),
               'app_dir': str(app), 'state_dir': str(state), 'backend_port': args.backend_port,
               'socket': runtime_dir + '/setup.sock', 'storages': storages}
    if args.storage_socket:
        catalog['storage_socket'] = args.storage_socket
    # Named installations keep their launcher code separate from the default service.
    destination = (Path('/opt/filehub-installer') if args.name == 'filehub-onboarding'
                   else Path('/opt/filehub-installers') / args.name)
    if not destination.exists():
        shutil.copytree(HERE, destination, ignore=shutil.ignore_patterns('__pycache__', 'test_*'))
    elif (destination / 'launcher.py').read_bytes() != (HERE / 'launcher.py').read_bytes():
        parser.error('Existing installer code differs. Inspect and back it up before an update.')
    config = Path('/etc/' + args.name + '.json')
    nginx = Path('/etc/' + args.name + '-nginx.conf')
    launcher_unit = Path('/etc/systemd/system/' + args.name + '.service')
    front_unit = Path('/etc/systemd/system/' + args.name + '-front.service')
    if any(p.exists() for p in (config, nginx, launcher_unit, front_unit)):
        parser.error('Existing configuration found; refusing to overwrite it.')
    artifacts = Path('/opt/filehub-installed') / args.name
    if artifacts.exists():
        parser.error('Published artifact destination already exists; refusing to overwrite it.')
    (artifacts / 'app').mkdir(parents=True, mode=0o755)
    shutil.copyfile(binary, artifacts / 'filehub')
    (artifacts / 'filehub').chmod(0o755)
    for leaf, data in [('app.apk', apk_bytes), ('version.json', apk_metadata)]:
        (artifacts / 'app' / leaf).write_bytes(data)
        (artifacts / 'app' / leaf).chmod(0o644)
    catalog['binary'] = str(artifacts / 'filehub')
    catalog['app_dir'] = str(artifacts / 'app')
    config.write_text(json.dumps(catalog, ensure_ascii=False, indent=2) + '\n')
    config.chmod(0o644)
    nginx.write_text(render_nginx(catalog, runtime_dir, args.listen_address + ':' + str(args.port)))
    rules.write_text(rules_text)
    private_unit=Path('/etc/systemd/system/'+private_name+'.service')
    private_unit.write_text(f'''[Unit]
Description=Keep Filehub backend private
Before={args.name}.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft --file {rules}
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_NET_ADMIN
MemoryMax=32M
MemorySwapMax=0
[Install]
WantedBy=multi-user.target
''')
    common = '''Restart=no
MemoryAccounting=true
MemorySwapMax=0
CPUAccounting=true
IOWeight=10
Nice=10
OOMPolicy=stop
NoNewPrivileges=true
CapabilityBoundingSet=
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectControlGroups=true
ProtectKernelTunables=true
ProtectKernelModules=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
'''
    launcher_unit.write_text(f'''[Unit]
Description=Filehub first-run wizard and published server (no builds)
{'Requires=filehub-storage.service' if args.with_host_storage else ''}
{'After=filehub-storage.service' if args.with_host_storage else ''}
Requires={private_name}.service
After={private_name}.service
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=filehub
Group=filehub
UMask=0077
WorkingDirectory={state}
RuntimeDirectory={args.name}
RuntimeDirectoryMode=0750
ExecStart=/usr/bin/python3 {destination}/launcher.py {config}
MemoryHigh=384M
MemoryMax=768M
CPUQuota=100%
TasksMax=96
ReadWritePaths={state} {' '.join(s['root'] for s in storages)}{' -/srv/filehub-disks' if args.storage_socket else ''}
{common}
[Install]
WantedBy=multi-user.target
''')
    front_unit.write_text(f'''[Unit]
Description=Filehub web entry point
Requires={args.name}.service
After={args.name}.service
BindsTo={args.name}.service
[Service]
Type=simple
User=filehub
Group=filehub
ExecStartPre=/usr/bin/mkdir -p {runtime_dir}/client {runtime_dir}/proxy
ExecStart=/usr/sbin/nginx -c {nginx} -g "daemon off;"
MemoryHigh=32M
MemoryMax=96M
CPUQuota=25%
TasksMax=8
ReadWritePaths={runtime_dir}
{common}
[Install]
WantedBy=multi-user.target
''')
    if not args.generate_only:
        if args.with_host_storage:
            subprocess.run(['bash', str(HERE.parents[1] / 'host-storage' / 'install.sh')], check=True)
        subprocess.run(['systemctl', 'daemon-reload'], check=True)
        subprocess.run(['systemctl', 'enable', '--now', args.name + '-front.service'], check=True)
    show_setup_summary(catalog['public_url'], code, state, started=not args.generate_only)


if __name__ == '__main__':
    main()
