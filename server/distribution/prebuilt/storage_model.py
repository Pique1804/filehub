"""Named file sources and independent photo scopes for the no-build launcher."""
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import socket
import stat
import tempfile
import time


class Invalid(ValueError):
    pass


class PhotoConfirmationRequired(Invalid):
    """A photo folder must be confirmed on the photo step, including late changes."""
    pass


def mount_table():
    mounts = {}
    for line in Path('/proc/self/mountinfo').read_text().splitlines():
        before, after = line.split(' - ', 1)
        fields = before.split()
        filesystem, source, *_ = after.split()
        path, root, source = (re.sub(r'\\([0-7]{3})', lambda m: chr(int(m[1], 8)), value)
                              for value in (fields[4], fields[3], source))
        mounts[path] = {'device': fields[2], 'type': filesystem, 'source': source, 'root': root}
    return mounts


def overlaps_state(root, state, mounts):
    def physical(path):
        for parent in (path, *path.parents):
            record = mounts.get(str(parent))
            if record and 'root' in record:
                return record['device'], Path(record['root']) / path.relative_to(parent)
        return None, path

    def overlaps(a, b):
        return a == b or a in b.parents or b in a.parents

    if overlaps(root, state):
        return True
    state_device, state_path = physical(state)
    if state_device is None:
        return False
    candidates = [root, *(Path(path) for path in mounts if root in Path(path).parents)]
    return any(device == state_device and overlaps(path, state_path)
               for device, path in map(physical, candidates))


def volume_identity(record):
    if record.get('type') in {'nfs', 'nfs4', 'cifs', 'zfs'}:
        return [record['type'], record['source'], record['root']]
    return None


class HostClient:
    def __init__(self, path):
        self.path = path

    def request(self, route, data=None):
        connection = http.client.HTTPConnection('localhost', timeout=35)
        connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.sock.settimeout(35)
        try:
            connection.sock.connect(self.path)
            connection.request('POST' if data is not None else 'GET', route,
                               json.dumps(data) if data is not None else None,
                               {'Content-Type': 'application/json'})
            response = connection.getresponse()
            raw = response.read(262145)
            if len(raw) > 262144:
                raise Invalid('Слишком большой ответ сервиса дисков.')
            result = json.loads(raw)
            if response.status != 200:
                raise Invalid(result.get('error', 'Не удалось подключить устройство.'))
            return result
        except (OSError, ValueError, http.client.HTTPException) as error:
            if isinstance(error, Invalid):
                raise
            raise Invalid('Сервис подключения дисков недоступен на хосте.') from None
        finally:
            connection.close()


class Storage:
    def __init__(self, catalog, state):
        self.catalog = catalog
        self.state = Path(state).resolve()
        self.host = HostClient(catalog['storage_socket']) if catalog.get('storage_socket') else None
        self.host_cache = None
        self.host_cached_at = 0

    def entries(self):
        values = [dict(item) for item in self.catalog['storages']]
        if self.host:
            try:
                if self.host_cache is None or time.monotonic() - self.host_cached_at > 2:
                    self.host_cache = self.host.request('/v1/volumes')['volumes']
                    self.host_cached_at = time.monotonic()
                volumes = self.host_cache
                for volume in volumes:
                    if volume.get('managed') and volume.get('mounted'):
                        values.append({'id': 'uuid-' + volume['uuid'], 'name': volume['label'] or 'Подключённый диск',
                                       'root': volume['mount'], 'mount': volume['mount'], 'uuid': volume['uuid'],
                                       'hostManaged': True, 'filesystemDevice': volume.get('filesystemDevice', ''),
                                       'defaultPath': 'Filehub', 'description': volume['filesystem']})
            except Invalid:
                pass  # Persisted sources stay in settings as offline, not silently replaced.
        identifiers = [v['id'] for v in values]
        if len(identifiers) != len(set(identifiers)):
            raise Invalid('Повторяющиеся идентификаторы хранилищ.')
        return values

    def entry(self, identifier):
        item = next((v for v in self.entries() if v['id'] == identifier), None)
        if item is None:
            raise Invalid('Хранилище недоступно. Подключите прежний диск.')
        root = Path(item['root'])
        if str(root) != str(root).strip() or not root.is_absolute() or root == Path('/') or root.is_symlink() or not root.is_dir():
            raise Invalid('Хранилище недоступно. Проверьте подключение диска.')
        mounts = mount_table()
        if root.resolve() != root or overlaps_state(root, self.state, mounts):
            raise Invalid('Служебные данные должны находиться вне доступного хранилища.')
        mount = item.get('mount')
        if mount:
            record = mounts.get(mount)
            if record is None or root.stat().st_dev != Path(mount).stat().st_dev:
                raise Invalid('Диск отключён. Запись в папку на другом диске запрещена.')
            if not item.get('uuid'):
                pinned = item.get('mountIdentity')
                if pinned is None:
                    identities = self.state / 'mount-identities.json'
                    if identities.exists():
                        pinned = json.loads(identities.read_text()).get(identifier)
                current = volume_identity(record)
                if pinned is not None and pinned != current:
                    raise Invalid('Сетевое хранилище изменилось. Подключите прежний ресурс или добавьте новый источник.')
                item['mountIdentity'] = current
            if item.get('uuid'):
                if not re.fullmatch(r'[A-Za-z0-9-]{4,80}', item['uuid']):
                    raise Invalid('Неверный UUID в настройках хранилища.')
                if item.get('hostManaged') and item.get('filesystemDevice'):
                    # The trusted host verifies UUID; no raw block devices need
                    # to be exposed to an unprivileged container.
                    if record['device'] != item['filesystemDevice']:
                        raise Invalid('Подключение диска ещё не появилось в Filehub или изменилось.')
                else:
                    device = Path('/dev/disk/by-uuid') / item['uuid']
                    source = Path(record['source'])
                    if not device.exists() or not source.is_absolute() or not source.exists() or device.stat().st_rdev != source.stat().st_rdev:
                        raise Invalid('На месте выбранного диска подключено другое устройство.')
        return item

    def folder(self, identifier, relative='', create=False, writable=False):
        item = self.entry(identifier)
        if not isinstance(relative, str) or len(relative) > 512:
            raise Invalid('Неверная папка.')
        parts = relative.split('/') if relative else []
        if any(not p or p != p.strip() or p.startswith('.') or any(c in p for c in ':,\\\x00\r\n') for p in parts):
            raise Invalid('Выберите обычную папку внутри хранилища.')
        current = Path(item['root'])
        descriptor = os.open(current, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            for part in parts:
                current /= part
                if descriptor is None:
                    continue
                try:
                    next_descriptor = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
                except FileNotFoundError:
                    if not create:
                        os.close(descriptor)
                        descriptor = None
                        continue
                    os.mkdir(part, 0o750, dir_fd=descriptor)
                    next_descriptor = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
                except OSError:
                    raise Invalid('Выбранный путь содержит ссылку, файл или недоступную папку.') from None
                os.close(descriptor)
                descriptor = next_descriptor
            if writable:
                if descriptor is None:
                    raise Invalid('Папка недоступна для записи.')
                probe = '.filehub-write-' + secrets.token_hex(12)
                fd = os.open(probe, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=descriptor)
                os.close(fd)
                os.unlink(probe, dir_fd=descriptor)  # Only our own empty probe.
        finally:
            if descriptor is not None:
                os.close(descriptor)
        return current

    def choices(self):
        values = []
        for item in self.entries():
            data = {key: item.get(key, '') for key in ('id', 'name', 'description', 'defaultPath')}
            data['wholeDisk']=bool(item.get('hostManaged') or (item.get('mount')==item.get('root') and item.get('mount') not in (None,'/')))
            try:
                self.entry(item['id'])
                usage = os.statvfs(item['root'])
                data.update(available=True, total=usage.f_blocks * usage.f_frsize,
                            free=usage.f_bavail * usage.f_frsize,rootWritable=os.access(item['root'],os.W_OK|os.X_OK))
            except (OSError, Invalid):
                data.update(available=False, total=0, free=0)
            values.append(data)
        return values

    def whole_disk_source(self, source):
        item=self.entry(source['storage'])
        return source['path']=='' and bool(item.get('hostManaged') or (item.get('mount')==item.get('root') and item.get('mount') not in (None,'/')))

    def readable_disk_folder(self, source, path):
        if not path.is_dir() or not os.access(path, os.R_OK | os.X_OK):
            return False
        return self.whole_disk_source(source) or bool(os.statvfs(path).f_flag & os.ST_RDONLY)

    def validate(self, sources, photo_dirs, create=False, previous=None, require_confirmation=True):
        if not isinstance(sources, list) or not 1 <= len(sources) <= 16:
            raise Invalid('Добавьте от 1 до 16 источников файлов.')
        if not isinstance(photo_dirs, list) or len(photo_dirs) > 32:
            raise Invalid('Можно выбрать до 32 папок для фотобиблиотеки.')
        names, paths, normalized = set(), [], []
        for value in sources:
            if not isinstance(value, dict):
                raise Invalid('Неверный источник.')
            name, identifier, relative = value.get('name'), value.get('storage'), value.get('path')
            if not isinstance(name, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,32}', name) or name.casefold() in names:
                raise Invalid('Имя источника: 1–32 латинских символа, цифры, - или _. Имена не должны повторяться.')
            path = self.folder(identifier, relative)
            if any(path == p or path in p.parents or p in path.parents for p in paths):
                raise Invalid('Источники пересекаются. Выберите папки без повторного включения одних файлов.')
            names.add(name.casefold())
            paths.append(path)
            normalized.append({'name': name, 'storage': identifier, 'path': relative})
        # Existing (source,path) identities must survive updates: no rename/remap/remove.
        for old in (previous or {}).get('sources', []):
            if not any(all(item.get(k) == old.get(k) for k in ('name', 'storage', 'path')) for item in normalized):
                raise Invalid('Существующий источник нельзя переименовать, удалить или переназначить: на него ссылаются файлы телефона и альбомы.')
        scopes = []
        for value in photo_dirs:
            if not isinstance(value, dict):
                raise Invalid('Неверная папка фотобиблиотеки.')
            source = next((s for s in normalized if s['name'] == value.get('source')), None)
            if source is None:
                raise Invalid('Выберите источник для каждой папки фотографий.')
            relative = value.get('path')
            prefix = '/'.join(filter(None, (source['path'], relative))) if isinstance(relative, str) else None
            path = self.folder(source['storage'], prefix)
            # Validating the joined path alone would accept an absolute child or empty components.
            if not isinstance(relative, str) or (relative and any(not p or p != p.strip() or p.startswith('.') or any(c in p for c in ':,\\\x00\r\n') for p in relative.split('/'))):
                raise Invalid('Неверная фото-папка внутри источника.')
            has_entries = False
            if path.is_dir():
                with os.scandir(path) as entries:
                    has_entries = next(entries, None) is not None
            if require_confirmation and has_entries and value.get('includeExisting') is not True:
                old_scopes = (previous or {}).get('photoDirs', [])
                if not any(s['source'] == source['name'] and s['path'] == relative for s in old_scopes):
                    raise PhotoConfirmationRequired('В папке ' + source['name'] + '/' + (relative or '') + ' уже есть файлы. Подтвердите добавление существующих фотографий на шаге «Фотографии».')
            scope = {'source': source['name'], 'path': relative}
            if scope not in scopes:
                scopes.append(scope)
        if create:
            for source in normalized:
                root=self.folder(source['storage'],source['path'])
                self.folder(source['storage'], source['path'], create=True, writable=not self.readable_disk_folder(source,root))
            for scope in scopes:
                source = next(s for s in normalized if s['name'] == scope['source'])
                relative='/'.join(filter(None,(source['path'],scope['path'])))
                path=self.folder(source['storage'],relative)
                self.folder(source['storage'], relative, create=True, writable=not self.readable_disk_folder(source,path))
        return normalized, scopes

    def review(self, sources, photo_dirs, previous=None):
        """Read-only preflight. Use the same path/identity rules as final setup."""
        normalized, scopes = self.validate(sources, photo_dirs, previous=previous, require_confirmation=False)
        reviews = []
        for scope in scopes:
            source = next(s for s in normalized if s['name'] == scope['source'])
            path = self.folder(source['storage'], '/'.join(filter(None, (source['path'], scope['path']))))
            has_entries = False
            if path.is_dir():
                with os.scandir(path) as entries:
                    has_entries = next(entries, None) is not None
            known = any(old['source'] == scope['source'] and old['path'] == scope['path']
                        for old in (previous or {}).get('photoDirs', []))
            confirmed = all(p.get('includeExisting') is True for p in photo_dirs
                            if p.get('source') == scope['source'] and p.get('path') == scope['path'])
            if not path.exists():
                parent=path.parent
                while not parent.exists():parent=parent.parent
                if not os.access(parent,os.W_OK|os.X_OK):
                    raise Invalid('Фото-папку нельзя создать в этом месте: нет доступа для записи. Выберите доступную папку внутри диска, например Filehub/Photos.')
            reviews.append({**scope, 'exists': path.is_dir(), 'hasEntries': has_entries,'writable':os.access(path,os.W_OK|os.X_OK),
                            'needsConfirmation': has_entries and not known, 'confirmed': confirmed})
        return {'valid': all(not p['needsConfirmation'] or p['confirmed'] for p in reviews), 'photoDirs': reviews}

    def paths(self, config):
        paths = {}
        for value in config['sources']:
            path = self.folder(value['storage'], value['path'])
            if not path.is_dir():
                raise Invalid('Источник ' + value['name'] + ' недоступен. Восстановите подключение прежнего диска.')
            guard = value.get('device')
            if guard is not None and path.stat().st_dev != guard:
                raise Invalid('Изменилась файловая система источника ' + value['name'] + '.')
            paths[value['name']] = path
        return paths

    def remember_volumes(self, config):
        path = self.state / 'mount-identities.json'
        current = json.loads(path.read_text()) if path.exists() else {}
        saved = dict(current)
        for source in config['sources']:
            item = self.entry(source['storage'])
            if item.get('mountIdentity') is not None:
                current.setdefault(item['id'], item['mountIdentity'])
        if current == saved:
            return
        with tempfile.NamedTemporaryFile(mode='w', dir=self.state, prefix='.mounts-', delete=False) as stream:
            temp = Path(stream.name)
            try:
                json.dump(current, stream)
                stream.flush()
                os.fsync(stream.fileno())
                os.replace(temp, path)
            finally:
                temp.unlink(missing_ok=True)


def legacy_config(config):
    if config.get('version') == 2:
        return config
    if config.get('version') != 1:
        raise Invalid('Неизвестная версия настроек. Восстановите конфигурацию.')
    # Keep old names and physical locations so all old receipts/albums remain valid.
    result = dict(config, version=2)
    result['sources'] = [{'name': name, **config[key]} for name, key in [('Files', 'files'), ('Photos', 'photos')]]
    result['photoDirs'] = [{'source': 'Photos', 'path': ''}]
    return result
