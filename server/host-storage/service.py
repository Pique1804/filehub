#!/usr/bin/env python3
"""Small host-only mount service. Unix socket, fixed commands, no formatting."""
import argparse
import http.server
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import socket
import socketserver
import stat
import struct
import subprocess
import threading

UUID = re.compile(r'[A-Za-z0-9-]{4,80}\Z')
FILESYSTEMS = {'ext4', 'xfs', 'btrfs', 'vfat', 'exfat', 'ntfs', 'ntfs3'}


class Rejected(ValueError):
    pass


def command(arguments):
    try:
        result = subprocess.run(arguments, check=True, capture_output=True, timeout=30,
                                env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C'})
        if len(result.stdout) > 262144:
            raise Rejected('Слишком много устройств в ответе системы.')
        return result.stdout
    except (OSError, subprocess.SubprocessError):
        raise Rejected('Системная операция не выполнена. Проверьте состояние диска и журнал сервиса.') from None


class Volumes:
    def __init__(self, state, base, uid, gid, run=command, exposed_base=None, allowed=None, allow_loop=False):
        self.state, self.base = Path(state), Path(base)
        self.uid, self.gid, self.run = uid, gid, run
        self.exposed_base = Path(exposed_base or base)
        self.allowed = set(allowed) if allowed is not None else None
        self.allow_loop = allow_loop
        self.lock = threading.RLock()
        self.desired = json.loads(self.state.read_text()) if self.state.exists() else []
        if not isinstance(self.desired, list) or any(not isinstance(u, str) or not UUID.fullmatch(u) for u in self.desired):
            raise Rejected('Повреждена конфигурация подключений.')

    def inventory(self):
        data = json.loads(self.run(['lsblk', '--json', '--bytes', '--paths', '--output',
                                   'NAME,TYPE,FSTYPE,UUID,LABEL,SIZE,MOUNTPOINTS,RO']))
        result = []
        def walk(nodes, system=False):
            for node in nodes:
                children = node.get('children', [])
                def system_tree(n):
                    points = n.get('mountpoints') or []
                    return any(p in ('/', '/boot', '/boot/efi', '/usr', '/var', '[SWAP]') for p in points) or any(system_tree(c) for c in n.get('children', []))
                protected = system or system_tree(node)
                if children:
                    walk(children, protected)
                    continue
                uuid = node.get('uuid') or ''
                if self.allowed is not None and uuid not in self.allowed:
                    continue
                # Shared subtrees can report an identical mount path repeatedly.
                # Treat it as one owner mount; never stack a second mount on it.
                points = list(dict.fromkeys(p for p in node.get('mountpoints', []) if p))
                target = str(self.base / uuid) if UUID.fullmatch(uuid) else ''
                managed = uuid in self.desired
                mounted = managed and points == [target]
                reason = ''
                if protected:
                    reason = 'Системное устройство: подключение через Filehub запрещено.'
                elif node.get('type') not in ('disk', 'part') and not (node.get('type') == 'loop' and self.allow_loop and self.allowed is not None and uuid in self.allowed):
                    reason = 'Этот тип устройства пока не поддерживается.'
                elif node.get('fstype') not in FILESYSTEMS or not UUID.fullmatch(uuid):
                    reason = 'Нужна готовая поддерживаемая файловая система. Filehub не форматирует устройства.'
                elif node.get('ro'):
                    reason = 'Устройство защищено от записи.'
                elif points and not mounted:
                    reason = 'Диск уже смонтирован системой. Добавьте предоставленную папку, повторное монтирование запрещено.'
                filesystem_device = ''
                if mounted:
                    info = Path(target).stat()
                    filesystem_device = f'{os.major(info.st_dev)}:{os.minor(info.st_dev)}'
                result.append({'uuid': uuid, 'label': str(node.get('label') or '')[:120],
                               'filesystem': node.get('fstype') or '', 'size': node.get('size') or 0,
                               'device': node['name'], 'testDevice': node.get('type') == 'loop', 'managed': managed, 'mounted': mounted,
                               'mount': str(self.exposed_base / uuid) if mounted else '',
                               'filesystemDevice': filesystem_device, 'canMount': not reason, 'reason': reason})
        walk(data.get('blockdevices', []))
        counts = {}
        for item in result:
            counts[item['uuid']] = counts.get(item['uuid'], 0) + 1
        for item in result:
            if item['uuid'] and counts[item['uuid']] != 1:
                item.update(canMount=False, reason='Обнаружены одинаковые UUID: подключение заблокировано.')
        return result

    def save(self):
        temporary = self.state.with_name('.pending-' + secrets.token_hex(8))
        with open(temporary, 'x', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
            json.dump(self.desired, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, self.state)
        descriptor = os.open(self.state.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def check_base(self):
        if self.base.is_symlink() or self.base.resolve() != self.base or not self.base.is_dir():
            raise Rejected('Каталог монтирования недоступен.')
        for parent in (self.base, *self.base.parents):
            info = parent.stat()
            if info.st_uid != 0 or info.st_mode & 0o022:
                raise Rejected('Каталог монтирования должен принадлежать root и быть закрыт для изменения другими пользователями.')

    def prepare_target(self, target):
        if os.path.ismount(target):
            raise Rejected('В этой точке уже подключено другое устройство. Его данные не изменены.')
        if target.exists():
            if not target.is_dir():
                raise Rejected('Каталог монтирования недоступен.')
            with os.scandir(target) as entries:
                if next(entries, None) is not None:
                    raise Rejected('Каталог монтирования не пуст. Его содержимое сохранено.')
        target.mkdir(mode=0o555, exist_ok=True)
        if target.stat().st_uid != 0 or target.stat().st_mode & 0o222:
            raise Rejected('Небезопасные права пустого каталога монтирования.')

    def attach(self, uuid):
        if not isinstance(uuid, str) or not UUID.fullmatch(uuid):
            raise Rejected('Неверный UUID устройства.')
        with self.lock:
            matches = [v for v in self.inventory() if v['uuid'] == uuid]
            if len(matches) != 1 or not matches[0]['canMount']:
                raise Rejected(matches[0]['reason'] if len(matches) == 1 else 'Устройство не найдено однозначно.')
            item = matches[0]
            target = self.base / uuid
            self.check_base()
            if target.is_symlink():
                raise Rejected('Каталог монтирования содержит ссылку.')
            if uuid not in self.desired:
                # Store the explicit request first, so a crash cannot leave an untracked mount.
                self.desired.append(uuid)
                self.save()
            if not item['mounted']:
                self.prepare_target(target)
                options = 'nodev,nosuid,noexec'
                filesystem = 'ntfs3' if item['filesystem'] == 'ntfs' else item['filesystem']
                if filesystem in ('vfat', 'exfat', 'ntfs3'):
                    options += f',uid={self.uid},gid={self.gid},umask=0027'
                self.run(['mount', '--types', filesystem, '--options', options,
                          '--source', 'UUID=' + uuid, '--target', str(target)])
            current = next((v for v in self.inventory() if v['uuid'] == uuid), None)
            if not current or not current['mounted']:
                raise Rejected('Не удалось подтвердить подключение нужного диска.')
            folder = target / 'Filehub'
            if folder.is_symlink():
                raise Rejected('Папка Filehub на диске является ссылкой.')
            try:
                folder.mkdir(mode=0o750)
            except FileExistsError:
                if not folder.is_dir():
                    raise Rejected('На диске уже существует файл с именем Filehub.') from None
            else:
                # Only the brand-new directory; never chown/chmod existing contents.
                os.chown(folder, self.uid, self.gid, follow_symlinks=False)
            return current

    def reconnect(self):
        for uuid in list(self.desired):
            try:
                self.attach(uuid)
            except (Rejected, OSError, ValueError):
                pass  # Missing devices remain registered and offline.


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = 'FilehubStorage'
    def log_message(self, *_):
        pass

    def reply(self, code, value):
        data = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def handle_request(self):
        _, uid, _ = struct.unpack('3i', self.connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid not in (0, self.server.volumes.uid):
            return self.reply(403, {'error': 'Нет доступа к сервису дисков.'})
        try:
            if self.command == 'GET' and self.path == '/v1/volumes':
                return self.reply(200, {'volumes': self.server.volumes.inventory()})
            if self.command == 'POST' and self.path == '/v1/attach':
                length = int(self.headers.get('Content-Length', '0'))
                if not 2 <= length <= 1024 or self.headers.get('Content-Type') != 'application/json':
                    raise Rejected('Неверный запрос.')
                data = json.loads(self.rfile.read(length))
                if not isinstance(data, dict) or set(data) != {'uuid'}:
                    raise Rejected('Неверный запрос.')
                return self.reply(200, self.server.volumes.attach(data['uuid']))
            self.reply(404, {'error': 'Операция не поддерживается.'})
        except Rejected as error:
            self.reply(400, {'error': str(error)})
        except (OSError, ValueError):
            self.reply(400, {'error': 'Не удалось подключить устройство: проверьте UUID, готовую файловую систему, права и подключение.'})

    do_GET = handle_request
    do_POST = handle_request


class Server(socketserver.UnixStreamServer):
    request_queue_size = 8
    def get_request(self):
        connection, address = super().get_request()
        connection.settimeout(40)
        return connection, address


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--user', default='filehub')
    parser.add_argument('--uid', type=int, help='Explicit mapped application UID for LXC')
    parser.add_argument('--gid', type=int, help='Explicit mapped application GID for LXC')
    parser.add_argument('--exposed-base', help='Mount path as seen by the application')
    parser.add_argument('--allow-uuid', action='append', help='Optionally restrict this instance to selected UUIDs')
    parser.add_argument('--allow-loop', action='store_true', help='Permit only explicitly allowlisted loop-backed test disks')
    parser.add_argument('--base', default='/srv/filehub-disks')
    parser.add_argument('--state', default='/var/lib/filehub-storage/volumes.json')
    parser.add_argument('--socket', default='/run/filehub-storage/control.sock')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('This service must run on the Linux host as root.')
    if (args.uid is None) != (args.gid is None):
        parser.error('Pass both --uid and --gid')
    if args.allow_loop and not args.allow_uuid:
        parser.error('--allow-loop requires explicit --allow-uuid')
    if any(not UUID.fullmatch(value) for value in args.allow_uuid or []):
        parser.error('Invalid allowed UUID')
    account = pwd.getpwnam(args.user) if args.uid is None else None
    uid, gid = (account.pw_uid, account.pw_gid) if account else (args.uid, args.gid)
    os.umask(0o077)
    # Directories are created by the service unit / installation instructions.
    volumes = Volumes(args.state, args.base, uid, gid, exposed_base=args.exposed_base,
                      allowed=args.allow_uuid, allow_loop=args.allow_loop)
    if Path(args.socket).exists():
        if not stat.S_ISSOCK(Path(args.socket).lstat().st_mode):
            raise Rejected('Refusing to replace a non-socket file')
        Path(args.socket).unlink()  # Only our stale runtime socket.
    server = Server(args.socket, Handler)
    server.volumes = volumes
    os.chown(args.socket, 0, gid)
    os.chmod(args.socket, 0o660)
    stopped = threading.Event()
    def reconnect():
        while not stopped.is_set():
            volumes.reconnect()
            stopped.wait(15)
    worker = threading.Thread(target=reconnect, daemon=True)
    worker.start()
    try:
        server.serve_forever()
    finally:
        stopped.set()
        server.server_close()


if __name__ == '__main__':
    main()
