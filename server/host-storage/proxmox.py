#!/usr/bin/env python3
"""Connect one Proxmox LXC to its own host storage helper. No builds."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time

HERE=Path(__file__).resolve().parent


def run(args,**kwargs):
    return subprocess.run(args,check=True,**kwargs)


def output(args):
    return subprocess.check_output(args,text=True).strip()


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--ctid',required=True,type=int)
    parser.add_argument('--name',default='filehub-onboarding')
    parser.add_argument('--artifacts',help='Host folder containing filehub and app/{app.apk,version.json}, for a new installation')
    parser.add_argument('--url',help='Optional public URL; otherwise detected inside the container')
    parser.add_argument('--port',default=8080,type=int)
    parser.add_argument('--backend-port',default=18081,type=int)
    parser.add_argument('--restart',action='store_true',help='Allow restarting this container to apply new mount points')
    parser.add_argument('--install-dependencies',action='store_true')
    parser.add_argument('--allow-uuid',action='append',default=[])
    parser.add_argument('--allow-loop',action='store_true')
    args=parser.parse_args()
    if os.geteuid()!=0 or not Path('/etc/pve/lxc').is_dir():parser.error('Run on the Proxmox host as root')
    if args.ctid<100 or not re.fullmatch(r'[a-z][a-z0-9-]{1,40}',args.name):parser.error('Invalid CTID/service name')
    if any(not re.fullmatch(r'[A-Za-z0-9-]{4,80}',u) for u in args.allow_uuid):parser.error('Invalid UUID')
    if args.allow_loop and not args.allow_uuid:parser.error('--allow-loop requires --allow-uuid')
    if args.url and not re.fullmatch(r'http://[A-Za-z0-9.-]+:[0-9]+/?',args.url):parser.error('Use a plain LAN http://host:port URL')
    ct=str(args.ctid)
    def guest(command):return ['pct','exec',ct,'--',*command]
    config_path=Path('/etc/pve/lxc')/(ct+'.conf')
    if not config_path.is_file():parser.error('Container not found')
    config=config_path.read_text()
    if 'status: running' not in output(['pct','status',ct]):parser.error('Start the target container before installation')
    helper='filehub-storage-ct'+ct
    base=Path('/srv/filehub-disks')/('ct'+ct)
    control=Path('/srv/filehub-storage-control')/('ct'+ct)
    marker=Path('/etc')/(helper+'.json')
    previous=json.loads(marker.read_text()) if marker.exists() else None
    if previous and not args.allow_uuid:
        args.allow_uuid=previous.get('allowed',[])
        args.allow_loop=args.allow_loop or previous.get('allow_loop',False)
    mounts={key:value for key,value in (line.split(': ',1) for line in config.splitlines() if ': ' in line and re.match(r'mp[0-9]+:',line))}
    selected={}
    recursive_entry=f'lxc.mount.entry: {base} srv/filehub-disks none rbind,create=dir 0 0'
    legacy=[key for key,value in mounts.items() if value.split(',')[0]==str(base) and 'mp=/srv/filehub-disks' in value.split(',')]
    if any('mp=/srv/filehub-disks' in value.split(',') and key not in legacy for key,value in mounts.items()):
        parser.error('The disk mount root belongs to a different host path')
    if any(line.startswith('lxc.mount.entry:') and ' srv/filehub-disks ' in line and line!=recursive_entry for line in config.splitlines()):
        parser.error('A different recursive bind already owns the disk path')
    for source,target in ((control,'/srv/filehub-storage-control'),):
        matches=[key for key,value in mounts.items() if value.split(',')[0]==str(source) and 'mp='+target in value.split(',')]
        if matches:selected[target]=matches[0];continue
        if any('mp='+target in value.split(',') for value in mounts.values()):parser.error('Target mount point already belongs to another source: '+target)
        for index in range(256):
            key='mp'+str(index)
            if key not in mounts:
                mounts[key]=str(source)+',mp='+target+',backup=0'+(',ro=1' if target=='/srv/filehub-storage-control' else '')
                selected[target]=key;break
    new_mounts=recursive_entry not in config or bool(legacy) or any((key+': '+mounts[key]) not in config for key in selected.values())
    if new_mounts and not args.restart:parser.error('New bind mounts need a CT restart: rerun with --restart to permit it')
    guest_config='/etc/'+args.name+'.json'
    existing=subprocess.run(guest(['test','-f',guest_config])).returncode==0
    if not existing and not args.artifacts:parser.error('For a fresh installation pass --artifacts with the published server and APK')
    if args.artifacts:
        release=Path(args.artifacts).resolve()
        if not all((release/leaf).is_file() for leaf in ('filehub','app/app.apk','app/version.json')):parser.error('Incomplete published artifacts folder')
    backup=Path('/var/backups')/('filehub-ct'+ct+'-'+time.strftime('%Y%m%d-%H%M%S'))
    backup.mkdir(mode=0o700)
    shutil.copy2(config_path,backup/(ct+'.conf'))
    if previous:shutil.copy2(marker,backup/marker.name)
    package_verifiers=' && command -v apksigner && command -v aapt' if not existing else ''
    dependencies_ready=subprocess.run(guest(['sh','-c','command -v python3 && command -v nginx && command -v nft && command -v ffmpeg && command -v tesseract && command -v vipsthumbnail && command -v rsync'+package_verifiers]),stdout=subprocess.DEVNULL).returncode==0
    if args.install_dependencies and not dependencies_ready:
        nginx_existed=subprocess.run(guest(['sh','-c','command -v nginx']),stdout=subprocess.DEVNULL).returncode==0
        extra_packages=['apksigner','aapt'] if package_verifiers else []
        for command in (['apt-get','update'],['apt-get','install','-y','--no-install-recommends','-o','Dpkg::Options::=--force-confold','python3','libcrypt1','nginx','nftables','util-linux','ffmpeg','tesseract-ocr','tesseract-ocr-eng','tesseract-ocr-rus','libvips-tools','rsync',*extra_packages]):
            run(guest(['systemd-run','--wait','--pipe','--collect','--property=MemoryMax=384M','--property=MemorySwapMax=0','--property=CPUQuota=50%','--setenv=DEBIAN_FRONTEND=noninteractive',*command]))
        if not nginx_existed:run(guest(['systemctl','disable','--now','nginx.service']))
    if subprocess.run(guest(['id','filehub']),stdout=subprocess.DEVNULL).returncode:
        run(guest(['useradd','--system','--home-dir','/var/lib/'+args.name,'--no-create-home','--shell','/usr/sbin/nologin','filehub']))
    user=json.loads(output(guest(['python3','-c',"import json,pwd; p=pwd.getpwnam('filehub'); print(json.dumps({'uid':p.pw_uid,'gid':p.pw_gid,'uidmap':open('/proc/self/uid_map').read(),'gidmap':open('/proc/self/gid_map').read()}))"])))
    def mapped(identifier,rows):
        for row in rows.splitlines():
            inner,outer,count=map(int,row.split())
            if inner<=identifier<inner+count:return outer+identifier-inner
        raise RuntimeError('Application UID/GID is not mapped')
    uid,gid=mapped(user['uid'],user['uidmap']),mapped(user['gid'],user['gidmap'])
    if previous and (previous['uid'],previous['gid'])!=(uid,gid):parser.error('Application ID mapping changed; inspect ownership before updating')
    code=Path('/opt/filehub-storage-bridge')/('ct'+ct)
    code.mkdir(parents=True,mode=0o755,exist_ok=True)
    shutil.copy2(HERE/'propagation.py',code/'propagation.py')
    propagation=Path('/etc/systemd/system')/(helper+'-propagation.service')
    propagation.write_text(f'''[Unit]
Description=Filehub CT{ct} mount propagation
After=local-fs.target
Before={helper}.service pve-container@{ct}.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/python3 {code}/propagation.py {base}
NoNewPrivileges=true
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE
MemoryMax=32M
MemorySwapMax=0
[Install]
WantedBy=multi-user.target
''')
    run(['systemctl','daemon-reload'])
    run(['systemctl','enable','--now',propagation.name])
    command=['python3',str(HERE/'install.py'),'--name',helper,'--uid',str(uid),'--gid',str(gid),
             '--base',str(base),'--socket',str(control/'control.sock'),'--exposed-base','/srv/filehub-disks']
    helper_unit=Path('/etc/systemd/system')/(helper+'.service')
    if helper_unit.exists():
        original=helper_unit.read_text()
        if '--base '+str(base) not in original or '--uid '+str(uid) not in original:
            parser.error('Existing host service differs; inspect before replacing it')
        command+=['--update']
    for value in args.allow_uuid:command+=['--allow-uuid',value]
    if args.allow_loop:command+=['--allow-loop']
    run(command)
    # Root in an unprivileged CT is a different host UID. It must be able to
    # stat the socket during setup; connection permission remains 0660 and the
    # daemon also checks the mapped application's SO_PEERCRED UID.
    control.chmod(0o755)
    dropin=Path('/etc/systemd/system')/(helper+'.service.d')
    dropin.mkdir(mode=0o755,exist_ok=True)
    (dropin/'propagation.conf').write_text(f'[Unit]\nRequires={propagation.name}\nAfter={propagation.name}\nBefore=pve-container@{ct}.service\n')
    ct_dropin=Path('/etc/systemd/system')/('pve-container@'+ct+'.service.d')
    ct_dropin.mkdir(mode=0o755,exist_ok=True)
    (ct_dropin/'filehub-storage.conf').write_text(f'[Unit]\nWants={helper}.service\nAfter={helper}.service\n')
    run(['systemctl','daemon-reload'])
    was_stopped=False
    if legacy:
        # Proxmox's normal mpN bind omits existing nested mounts on reboot.
        # Convert only our own bind to LXC's recursive bind, retaining all data.
        run(['pct','shutdown',ct,'--timeout','60'])
        was_stopped=True
        for key in legacy:run(['pct','set',ct,'-delete',key])
    if recursive_entry not in config_path.read_text():
        with config_path.open('a') as stream:stream.write('\n'+recursive_entry+'\n')
    for target in selected:
        if not was_stopped:run(guest(['mkdir','-p',target]))
    for key in selected.values():
        if key+': '+mounts[key] not in config:run(['pct','set',ct,'-'+key,mounts[key]])
    if new_mounts:
        run(['pct','start' if was_stopped else 'reboot',ct])
        for _ in range(120):
            if subprocess.run(guest(['true']),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0:break
            time.sleep(.5)
        else:raise RuntimeError('Container did not return after restart; original data/configuration retained')
    # The socket bind is a persistent directory, so helper restarts replace the
    # socket inode without leaving the container bound to a stale /run directory.
    run(guest(['test','-S','/srv/filehub-storage-control/control.sock']))
    source=HERE.parent/'distribution/prebuilt'
    if existing:
        run(guest(['systemctl','stop',args.name+'-front.service',args.name+'.service']))
        guest_backup='/root/filehub-before-universal-'+time.strftime('%Y%m%d-%H%M%S')+'.tar.gz'
        run(guest(['tar','-czf',guest_backup,'/opt/filehub-installer',guest_config,'/etc/'+args.name+'-nginx.conf','/etc/systemd/system/'+args.name+'.service']))
    run(guest(['mkdir','-p','/opt/filehub-installer/web']))
    for path in source.rglob('*'):
        if path.is_file() and (path.parent==source or path.parent==source/'web') and not path.name.startswith('test_') and path.suffix in ('.py','.js','.html','.css','.woff2','.gz'):
            target='/opt/filehub-installer/'+str(path.relative_to(source))
            run(['pct','push',ct,str(path),target,'--perms','0644'])
    if existing:
        script="""import json,importlib.util,pathlib,sys
name=sys.argv[1]; p=pathlib.Path('/etc/'+name+'.json'); c=json.loads(p.read_text()); c['storage_socket']='/srv/filehub-storage-control/control.sock';p.write_text(json.dumps(c,ensure_ascii=False,indent=2)+chr(10))
installer=pathlib.Path('/opt/filehub-installer')
sys.path.insert(0, str(installer))
spec=importlib.util.spec_from_file_location('cfg',installer/'configure.py'); cfg=importlib.util.module_from_spec(spec);spec.loader.exec_module(cfg)
pathlib.Path('/etc/'+name+'-nginx.conf').write_text(cfg.render_nginx(c,'/run/'+name,'0.0.0.0:'+str(__import__('urllib.parse',fromlist=['urlsplit']).urlsplit(c['public_url']).port)))
u=pathlib.Path('/etc/systemd/system/'+name+'.service');t=u.read_text()
if 'ReadWritePaths=/srv/filehub-disks' not in t:t=t.replace('[Service]','[Service]'+chr(10)+'ReadWritePaths=/srv/filehub-disks',1)
u.write_text(t)
"""
        run(guest(['python3','-c',script,args.name]))
        run(guest(['systemctl','daemon-reload']))
        run(guest(['systemctl','start',args.name+'-front.service']))
    else:
        run(guest(['mkdir','-p','/opt/filehub-release/app']))
        for leaf in ('filehub','app/app.apk','app/version.json'):
            run(['pct','push',ct,str(release/leaf),'/opt/filehub-release/'+leaf,'--perms','0755' if leaf=='filehub' else '0644'])
        command=['python3','/opt/filehub-installer/configure.py','--binary','/opt/filehub-release/filehub','--app-dir','/opt/filehub-release/app',
                 '--name',args.name,'--port',str(args.port),'--backend-port',str(args.backend_port),'--storage-socket','/srv/filehub-storage-control/control.sock']
        if args.url:command+=['--url',args.url]
        run(guest(command))
    result={'ctid':args.ctid,'name':args.name,'uid':uid,'gid':gid,'base':str(base),'control':str(control),
            'mounts':selected,'backup':str(backup),'allowed':args.allow_uuid,'allow_loop':args.allow_loop}
    marker.write_text(json.dumps(result,indent=2)+'\n')
    print('Filehub host storage connected to CT'+ct+'. Account and storage setup:')
    print(output(guest(['python3','-c',"import json;print(json.load(open('/etc/"+args.name+".json'))['public_url'])"])))


if __name__=='__main__':main()
