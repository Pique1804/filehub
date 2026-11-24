#!/usr/bin/env python3
"""Install one bounded host mount service. No dependency downloads or builds."""
import argparse
import http.client
import os
from pathlib import Path
import pwd
import re
import shutil
import subprocess
import socket as sockets
import time


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--name',default='filehub-storage')
    parser.add_argument('--user',default='filehub')
    parser.add_argument('--uid',type=int)
    parser.add_argument('--gid',type=int)
    parser.add_argument('--base',default='/srv/filehub-disks')
    parser.add_argument('--socket',default='/run/filehub-storage/control.sock')
    parser.add_argument('--exposed-base')
    parser.add_argument('--allow-uuid',action='append',default=[])
    parser.add_argument('--allow-loop',action='store_true')
    parser.add_argument('--update',action='store_true')
    args=parser.parse_args()
    if os.geteuid()!=0:parser.error('Run on the Linux host as root')
    if subprocess.run(['systemd-detect-virt','--container','--quiet']).returncode==0:
        parser.error('The mount service must be installed on the Linux host, outside its container')
    if not re.fullmatch(r'[a-z][a-z0-9-]{1,48}',args.name):parser.error('Invalid service name')
    for value in (args.base,args.socket,args.exposed_base or args.base):
        if not re.fullmatch(r'/[A-Za-z0-9_./-]+',value) or '..' in Path(value).parts:
            parser.error('Use plain absolute paths')
    if (args.uid is None)!=(args.gid is None):parser.error('Pass both mapped UID and GID')
    if args.uid is None:
        account=pwd.getpwnam(args.user);args.uid,args.gid=account.pw_uid,account.pw_gid
    if min(args.uid,args.gid)<1:parser.error('Application UID/GID must not be root')
    if any(not re.fullmatch(r'[A-Za-z0-9-]{4,80}',value) for value in args.allow_uuid):parser.error('Invalid UUID')
    if args.allow_loop and not args.allow_uuid:parser.error('Loop disks require an explicit UUID allowlist')
    for binary in ('lsblk','mount','findmnt','python3'):
        if not shutil.which(binary):parser.error('Missing system dependency: '+binary)
    code=Path('/opt')/args.name
    unit=Path('/etc/systemd/system')/(args.name+'.service')
    if (code.exists() or unit.exists()) and not args.update:parser.error('Existing service: inspect first or explicitly use --update')
    if unit.exists():
        backup=Path('/var/backups')/(args.name+'-'+__import__('time').strftime('%Y%m%d-%H%M%S'))
        backup.mkdir(mode=0o700);shutil.copy2(unit,backup/unit.name)
        if code.exists():shutil.copytree(code,backup/'code')
    base=Path(args.base)
    if base.is_symlink() or base.resolve()!=base:parser.error('Symlinked mount roots are refused')
    base.mkdir(parents=True,mode=0o755,exist_ok=True)
    for parent in (base,*base.parents):
        info=parent.stat()
        if info.st_uid!=0 or info.st_mode&0o022:parser.error('Mount root and ancestors must be controlled by root')
    code.mkdir(mode=0o755,exist_ok=True)
    shutil.copy2(Path(__file__).with_name('service.py'),code/'service.py')
    state=Path('/var/lib')/args.name;state.mkdir(mode=0o700,exist_ok=True)
    socket=Path(args.socket)
    socket.parent.mkdir(parents=True,mode=0o750,exist_ok=True)
    os.chown(socket.parent,0,args.gid);socket.parent.chmod(0o750)
    command=['/usr/bin/python3',str(code/'service.py'),'--uid',str(args.uid),'--gid',str(args.gid),
             '--base',args.base,'--state',str(state/'volumes.json'),'--socket',args.socket]
    if args.exposed_base:command+=['--exposed-base',args.exposed_base]
    for value in args.allow_uuid:command+=['--allow-uuid',value]
    if args.allow_loop:command+=['--allow-loop']
    runtime=f'RuntimeDirectory={socket.parent.name}\nRuntimeDirectoryMode=0755\n' if str(socket.parent).startswith('/run/') else ''
    unit.write_text(f'''[Unit]
Description=Filehub host storage service ({args.name})
After=local-fs.target
[Service]
Type=simple
User=root
Group=root
UMask=0077
{runtime}ExecStart={' '.join(command)}
NoNewPrivileges=true
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE CAP_CHOWN CAP_FOWNER
RestrictAddressFamilies=AF_UNIX
RestrictSUIDSGID=true
MemoryMax=96M
MemorySwapMax=0
CPUQuota=25%
TasksMax=8
Restart=on-failure
RestartSec=5s
[Install]
WantedBy=multi-user.target
''')
    subprocess.run(['systemctl','daemon-reload'],check=True)
    subprocess.run(['systemctl','enable',args.name+'.service'],check=True)
    subprocess.run(['systemctl','restart',args.name+'.service'],check=True)
    for attempt in range(100):
        connection=http.client.HTTPConnection('localhost',timeout=2)
        connection.sock=sockets.socket(sockets.AF_UNIX,sockets.SOCK_STREAM)
        connection.sock.settimeout(2)
        try:
            connection.sock.connect(args.socket)
            connection.request('GET','/v1/volumes')
            response=connection.getresponse();response.read()
            if response.status==200:break
        except (OSError,http.client.HTTPException):pass
        finally:connection.close()
        if attempt==99:raise RuntimeError('Storage service failed to become ready; inspect journalctl -u '+args.name)
        time.sleep(.1)
    print('Storage service socket: '+args.socket)


if __name__=='__main__':main()
