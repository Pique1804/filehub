#!/usr/bin/env python3
"""First-run UI for the published Filehub 1.40 binary. No build tools needed.

Linux, Python 3.11/3.12 (libcrypt bcrypt), nginx. Runs as the Filehub user.
Only explicitly registered storage roots can be browsed or selected.
"""
import hashlib
import hmac
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socketserver
import sqlite3
import stat
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlsplit, quote
from passwords import crypt
import mounting
import setup_draft

from storage_model import Invalid, PhotoConfirmationRequired, Storage, legacy_config

ASSETS = Path(__file__).parent / 'web'


class DatabaseRecoveryRequired(Invalid):
    pass

PHOTO_EXT = {'.jpg', '.jpeg', '.png', '.webp', '.heic', '.heif', '.gif', '.mp4', '.mov', '.mkv'}


def atomic_create(path, value):
    """Publish complete private JSON once; never overwrite existing setup."""
    temporary = path.with_name('.pending-' + secrets.token_hex(8))
    with open(temporary, 'x', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
        json.dump(value, stream, ensure_ascii=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.link(temporary, path)
    temporary.unlink()  # Only our new temporary file, after successful publication.
    fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def bootstrap_code(state):
    """Operator-only enrollment secret; never returned by a public endpoint."""
    path = Path(state) / 'setup-code.json'
    if not path.exists():
        try:
            atomic_create(path, {'code': secrets.token_hex(16)})
        except FileExistsError:
            pass
    if path.is_symlink() or not stat.S_ISREG(path.stat().st_mode) or path.stat().st_mode & 0o077:
        raise RuntimeError('Setup code must be a private regular file')
    value = json.loads(path.read_text())['code']
    if not isinstance(value, str) or not re.fullmatch(r'[0-9a-f]{32}', value):
        raise RuntimeError('Invalid setup code; refusing to reopen setup')
    return value


class Runtime:
    def __init__(self, catalog):
        self.catalog = catalog
        self.state = Path(catalog['state_dir'])
        self.config_path = self.state / 'installation.json'
        self.initial_database = self.state / '.initial-index.db'
        self.owner_path = self.state / 'setup-owner.json'
        self.owner = json.loads(self.owner_path.read_text()) if self.owner_path.exists() else None
        self.config = None
        self.child = None
        self.ready = False
        self.error = ''
        self.lock = threading.RLock()
        self.csrf_key = secrets.token_bytes(32)
        self.failures = {}
        self.dummy_hash = crypt.crypt('filehub-invalid-login', crypt.mksalt(crypt.METHOD_BLOWFISH, rounds=1024))
        self.storage_model = Storage(catalog, self.state)
        self.source_devices = {}
        self.offline = False
        self.port = int(catalog['backend_port'])
        self.host = urlsplit(catalog['public_url']).netloc.lower()
        self.allowed_hosts = {self.host, *catalog.get('extra_hosts', [])}
        if self.config_path.exists():
            self.config = legacy_config(json.loads(self.config_path.read_text()))
            try:
                self.start()
            except DatabaseRecoveryRequired as error:
                self.error = str(error)
                self.offline = True
            except (Invalid, OSError):
                self.error = 'Хранилище недоступно. Откройте настройки и подключите прежний диск.'
                self.offline = True
        elif (self.state / 'index.db').exists():
            raise RuntimeError('Existing database without installation.json: refusing new setup')
        if not self.owner and not self.config:
            bootstrap_code(self.state)

    def app_version(self):
        manifest = Path(self.catalog["app_dir"]) / "version.json"
        return json.loads(manifest.read_text()).get("versionName", "") if manifest.is_file() else ""

    def storage(self, identifier):
        return self.storage_model.entry(identifier)

    def folder(self, identifier, relative, create=False, allow_root=False):
        return self.storage_model.folder(identifier, relative, create=create)

    def listing(self, identifier, relative):
        path = self.folder(identifier, relative, allow_root=True)
        if not path.is_dir():
            raise Invalid('Папка ещё не существует. Она будет создана при завершении настройки.')
        folders, media, count, truncated, samples = [], 0, 0, False, []
        with os.scandir(path) as entries:
            for entry in entries:
                count += 1
                if count > 2000:
                    truncated = True
                    break
                if entry.name.startswith('.') or entry.is_symlink():
                    continue
                if entry.is_dir(follow_symlinks=False):
                    folders.append(entry.name)
                    if len(samples)<8:samples.append({'name':entry.name,'kind':'folder'})
                elif Path(entry.name).suffix.lower() in PHOTO_EXT:
                    media += 1
                    if len(samples)<8:samples.append({'name':entry.name,'kind':'photo'})
                elif entry.is_file(follow_symlinks=False) and len(samples)<8:
                    samples.append({'name':entry.name,'kind':'file'})
        return {'folders': sorted(folders, key=str.casefold), 'mediaHere': media,
                'hasEntries': count > 0, 'truncated': truncated, 'path': relative,
                'samples':samples,'writable':os.access(path,os.W_OK|os.X_OK)}

    def choices(self):
        return self.storage_model.choices()

    def claim(self, data, address='local'):
        """Claim setup before any privileged disk operation; survives restarts."""
        with self.lock:
            if self.config:
                raise Invalid('Первоначальная настройка уже завершена. Войдите в Filehub.')
            user, password = data.get('username'), data.get('password')
            if not isinstance(user, str) or not re.fullmatch(r'[a-zA-Z0-9_.-]{1,32}', user):
                raise Invalid('Логин: от 1 до 32 латинских букв, цифр, точек, дефисов или подчёркиваний.')
            if not isinstance(password, str) or not 12 <= len(password.encode()) <= 72:
                raise Invalid('Пароль должен содержать от 12 до 72 байт.')
            if not self.owner:
                supplied = data.get('setupCode', '')
                if not isinstance(supplied, str) or not secrets.compare_digest(supplied.encode(), bootstrap_code(self.state).encode()):
                    raise Invalid('Введите код настройки из терминала установки. Это не пароль аккаунта.')
                hashed = crypt.crypt(password, crypt.mksalt(crypt.METHOD_BLOWFISH, rounds=1024))
                if not hashed or not hashed.startswith(('$2b$', '$2a$', '$2y$')):
                    raise Invalid('На сервере недоступно безопасное сохранение пароля.')
                owner = {'username': user, 'pwhash': hashed, 'session_version': secrets.token_hex(16)}
                if not (self.state / 'secret').exists():
                    with open(self.state / 'secret', 'xb', opener=lambda p, f: os.open(p, f, 0o600)) as stream:
                        stream.write(secrets.token_bytes(32))
                atomic_create(self.owner_path, owner)
                self.owner = owner
            return self.login(data, address)

    def revision(self):
        return hashlib.sha256(self.config_path.read_bytes()).hexdigest() if self.config else ''

    def storage_info(self):
        items = []
        for source in self.config['sources']:
            item = dict(source)
            try:
                path = self.folder(source['storage'], source['path'])
                item['online'] = path.is_dir()
            except (Invalid, OSError):
                item['online'] = False
            items.append(item)
        return {'sources': items, 'photoDirs': self.config['photoDirs'],
                'storages': self.choices(), 'revision': self.revision(),
                'ready': self.ready, 'error': self.error,
                'mountingAvailable': self.storage_model.host is not None}

    def install(self, data, authenticated=None):
        with self.lock:
            if self.config is not None or self.config_path.exists():
                raise Invalid('Первоначальная настройка уже завершена.')
            user = data.get('username', '')
            if not isinstance(user, str) or not re.fullmatch(r'[a-zA-Z0-9_.-]{1,32}', user):
                raise Invalid('Логин: от 1 до 32 латинских букв, цифр, точек, дефисов или подчёркиваний.')
            resumed = self.owner and authenticated and authenticated.get('role')=='admin' and authenticated.get('username')==self.owner['username'] and user==self.owner['username']
            if not resumed:
                raise Invalid('Сначала создайте аккаунт с кодом установки и войдите в него.')
            sources, scopes = self.storage_model.validate(data.get('sources'), data.get('photoDirs'))
            self.storage_model.validate(sources, data.get('photoDirs'), create=True)
            hashed = self.owner['pwhash']
            if not hashed or not hashed.startswith(('$2b$', '$2a$', '$2y$')):
                raise Invalid('Сервер не поддерживает сохранение пароля. Настройки не изменены.')
            config = {'version': 2, 'username': user, 'adminHash': hashed,
                      'sources': sources, 'photoDirs': scopes}
            if (self.state / 'index.db').exists():
                raise Invalid('Обнаружена существующая база. Восстановите прежнюю установку.')
            fd, pending = tempfile.mkstemp(prefix='.setup-db-', dir=self.state)
            os.close(fd)
            try:
                with sqlite3.connect(pending) as db:
                    db.execute("CREATE TABLE users(username TEXT PRIMARY KEY,pwhash TEXT,role TEXT,created INTEGER,session_version TEXT NOT NULL DEFAULT '')")
                    db.execute('INSERT INTO users VALUES(?,?,?,?,?)',
                               (user, hashed, 'admin', int(time.time()), self.owner['session_version']))
                os.replace(pending, self.initial_database)
            finally:
                Path(pending).unlink(missing_ok=True)
            atomic_create(self.config_path, config)
            self.config = config
            self.start()

    def start(self):
        database = self.state / 'index.db'
        if not database.exists():
            if not self.initial_database.is_file():
                raise DatabaseRecoveryRequired('База FileHub отсутствует. Восстановите index.db из резервной копии и перезапустите службу.')
            os.replace(self.initial_database, database)
        elif self.initial_database.exists():
            # An interrupted first setup may leave a staging file beside a complete DB.
            self.initial_database.unlink()
        with sqlite3.connect(f'file:{database}?mode=ro', uri=True) as db:
            if not db.execute("SELECT 1 FROM users WHERE role='admin'").fetchone():
                raise DatabaseRecoveryRequired('В базе FileHub нет администратора. Восстановите рабочую резервную копию.')
        # On restart never create missing selected folders: fail closed on lost disks.
        paths = self.storage_model.paths(self.config)
        self.storage_model.remember_volumes(self.config)
        self.source_devices = {name: path.stat().st_dev for name, path in paths.items()}
        self.offline = False
        self.ready = False
        self.error = ''
        optional_settings = {'FILEHUB_MAP_TILES', 'FILEHUB_MAP_ATTRIBUTION',
                             'FILEHUB_CLIP_SIDECAR', 'FILEHUB_CLIP_DEFAULT_ENABLED'}
        env = {k: v for k, v in os.environ.items() if not k.startswith('FILEHUB_') or k in optional_settings}
        env.setdefault('FILEHUB_CLIP_DEFAULT_ENABLED', '0')
        photo_dirs = ','.join(item['source'] + ':' + item['path'] for item in self.config['photoDirs'])
        # The shipped server treats an empty setting as all sources. An empty
        # source name matches none of the configured sources.
        env.update(PORT=str(self.port), GOMAXPROCS='1', GOMEMLIMIT='256MiB',
                   VIPS_CONCURRENCY='1', OMP_THREAD_LIMIT='1', HOME=str(self.state),
                   FILEHUB_SOURCES=','.join(name + ':' + str(path) for name, path in paths.items()),
                   FILEHUB_PHOTO_DIRS=photo_dirs or ':', FILEHUB_DB=str(database),
                   FILEHUB_APP_DIR=self.catalog['app_dir'], FILEHUB_BIND_HOST='127.0.0.1',
                   FILEHUB_TRUSTED_PROXIES='127.0.0.1/32,::1/128')
        for key, leaf in [('SECRET_FILE', 'secret'), ('TUS_DIR', 'tus'), ('THUMB_DIR', 'thumbs'),
                          ('PREVIEW_DIR', 'previews'), ('BACKUP_DIR', 'db-backups'),
                          ('ORIGINAL_BACKUP_STATUS', 'originals-backup-status.json')]:
            env['FILEHUB_' + key] = str(self.state / leaf)
        if not (self.state / 'secret').exists():
            with open(self.state / 'secret', 'xb') as stream:
                stream.write(secrets.token_bytes(32))
            (self.state / 'secret').chmod(0o600)
        self.child = subprocess.Popen([self.catalog['binary']], cwd=self.state, env=env)
        for _ in range(100):
            if self.child.poll() is not None:
                self.error = 'Сервер не запустился. Сохранённые настройки и файлы оставлены на месте.'
                return
            try:
                conn = http.client.HTTPConnection('127.0.0.1', self.port, timeout=1)
                conn.request('GET', '/api/auth')
                result = conn.getresponse()
                result.read()
                conn.close()
                if result.status == 200:
                    self.ready = True
                    return
            except OSError:
                pass
            time.sleep(.1)
        self.error = 'Запуск занимает больше времени. Обновите страницу через несколько секунд.'

    def auth(self, cookie):
        # Same HMAC, expiry and live user/session_version checks as Go 1.40.
        # This keeps storage settings accessible while an offline disk stops Go.
        from http.cookies import SimpleCookie, CookieError
        try:
            values = SimpleCookie(cookie or '')
            if 'fh_session' not in values:
                return None
            token = values['fh_session'].value
            parts = token.split('|')
            if len(parts) not in (3, 4, 5) or int(parts[1]) < int(time.time()):
                return None
            secret = (self.state / 'secret').read_bytes()
            if len(secret) < 32:
                return None
            signature = hmac.new(secret, '|'.join(parts[:-1]).encode(), hashlib.sha256).hexdigest()
            if not hmac.compare_digest(signature, parts[-1]):
                return None
            if not self.config and self.owner:
                row = ('admin', self.owner['session_version'], None) if parts[0] == self.owner['username'] else None
            else:
                with sqlite3.connect(f'file:{self.state / "index.db"}?mode=ro', uri=True) as db:
                    has_folders = db.execute("SELECT 1 FROM pragma_table_info('users') WHERE name='folder_access'").fetchone()
                    policy_column = 'folder_access' if has_folders else 'NULL'
                    row = db.execute(f'SELECT role,session_version,{policy_column} FROM users WHERE username=?', (parts[0],)).fetchone()
                    if db.execute("SELECT 1 FROM sqlite_master WHERE name='revoked_sessions' AND type='table'").fetchone():
                        if db.execute('SELECT 1 FROM revoked_sessions WHERE token_hash=?', (hashlib.sha256(token.encode()).hexdigest(),)).fetchone():
                            return None
            if not row or row[1] != (parts[2] if len(parts) >= 4 else '') or row[0] not in ('admin', 'user', 'viewer'):
                return None
            folders = None
            if row[2] is not None:
                folders = json.loads(row[2])
                if not isinstance(folders, dict) or not isinstance(folders.get('folders'), list) or len(folders['folders']) > 64:
                    return None
                personal = folders.get('personal')
                grants = folders['folders'] + ([personal] if personal is not None else [])
                if any(not isinstance(grant, dict) or not isinstance(grant.get('source'), str) or not isinstance(grant.get('path'), str) for grant in grants):
                    return None
            role = 'viewer' if folders is not None and folders.get('personal') is None else row[0]
            return {'username': parts[0], 'role': role, 'authed': True, 'folders': folders}
        except (ValueError, OSError, sqlite3.Error, CookieError):
            return None

    def photo_destination(self, auth):
        if auth['folders'] is not None:
            personal = auth['folders'].get('personal')
            if personal is None or auth['role'] == 'viewer':
                return '', '', ''
            return personal['source'], personal['path'], personal['path']
        if not self.config['photoDirs']:
            return '', '', ''
        scope = self.config['photoDirs'][0]
        return scope['source'], scope['path'], '/'.join(filter(None, (scope['path'], 'PhoneBackup')))

    def login(self, data, address):
        if not self.config and not self.owner:
            raise Invalid('Сначала завершите первоначальную настройку.')
        with self.lock:
            now = time.monotonic()
            self.failures = {key: value for key, value in self.failures.items() if now - value[1] < 900}
            user, password = data.get('username'), data.get('password')
            if not isinstance(user, str) or not isinstance(password, str) or not 1 <= len(password.encode()) <= 72:
                raise Invalid('Неверный логин или пароль.')
            budgets = [(('source', address), 5)]
            if re.fullmatch(r'[a-zA-Z0-9_.-]{1,32}', user):
                budgets.append((('account', user), 20))
            if len(self.failures) >= 128 or any(self.failures.get(key, (0, now))[0] >= limit for key, limit in budgets):
                raise Invalid('Слишком много попыток. Повторите вход через 15 минут.')
            if not self.config:
                row = (self.owner['pwhash'], self.owner['session_version']) if user == self.owner['username'] else None
            else:
                with sqlite3.connect(f'file:{self.state / "index.db"}?mode=ro', uri=True) as db:
                    row = db.execute('SELECT pwhash,session_version FROM users WHERE username=?', (user,)).fetchone()
            candidate_hash = row[0] if row else self.dummy_hash
            valid = secrets.compare_digest(crypt.crypt(password, candidate_hash) or '', candidate_hash)
            if not row or not valid:
                for key, _ in budgets:
                    count, first = self.failures.get(key, (0, now))
                    self.failures[key] = (count + 1, first)
                raise Invalid('Неверный логин или пароль.')
            message = user + '|' + str(int(time.time()) + 30*86400) + '|' + row[1] + '|' + secrets.token_hex(16)
            signature = hmac.new((self.state / 'secret').read_bytes(), message.encode(), hashlib.sha256).hexdigest()
            return message + '|' + signature

    def reconfigure(self, data):
        with self.lock:
            if data.get('revision') != self.revision():
                raise Invalid('Настройки уже изменились. Обновите страницу перед сохранением.')
            sources, scopes = self.storage_model.validate(data.get('sources'), data.get('photoDirs'), previous=self.config)
            self.storage_model.validate(sources, data.get('photoDirs'), create=True, previous=self.config)
            updated = dict(self.config, sources=sources, photoDirs=scopes)
            self.ready = False
            self.stop()
            backup = self.config_path.with_name('installation.previous-' + secrets.token_hex(8) + '.json')
            atomic_create(backup, json.loads(self.config_path.read_text()))
            temporary = self.config_path.with_name('.updated-' + secrets.token_hex(8))
            atomic_create(temporary, updated)
            os.replace(temporary, self.config_path)
            descriptor = os.open(self.state, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
            self.config = updated
            try:
                self.start()
            except (Invalid, OSError):
                self.offline = True
                self.error = 'Настройки сохранены, но хранилище недоступно. Подключите прежний диск.'

    def check_storage(self):
        if not self.config:
            return
        with self.lock:
            try:
                paths = self.storage_model.paths(self.config)
                if self.child and self.child.poll() is not None:
                    self.ready = False
                    self.error = 'Сервер остановлен. Проверьте журнал; настройки сохранены.'
                if self.ready and any(path.stat().st_dev != self.source_devices.get(name) for name, path in paths.items()):
                    raise Invalid('Изменилось подключение диска.')
            except (Invalid, OSError):
                self.ready = False
                self.offline = True
                self.error = 'Диск отключён. Filehub остановлен; файлы и настройки сохранены.'
                self.stop()
                return
            if self.offline:
                self.start()

    def stop(self):
        if self.child and self.child.poll() is None:
            self.child.terminate()
            try:
                self.child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.child.kill()
                self.child.wait()


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = 'Filehub'

    def log_message(self, *_):
        pass  # Do not log URLs, cookies or credentials.

    @property
    def runtime(self):
        return self.server.runtime

    def send(self, code, data=None, content_type='application/json; charset=utf-8', headers=None):
        payload = json.dumps(data, ensure_ascii=False).encode() if content_type.startswith('application/json') and data is not None else data
        if payload is None or code in (204, 304):
            payload = b''
        self.send_response(code)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')
        self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
        self.end_headers()
        if payload:
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass

    def host_ok(self):
        return self.headers.get('Host', '').lower() in self.runtime.allowed_hosts

    def browser_cookie(self, name, value):
        secure = '; Secure' if urlsplit(self.runtime.catalog['public_url']).scheme == 'https' else ''
        return f'{name}={value}; Path=/; HttpOnly; SameSite=Strict{secure}'

    def session(self, create=False):
        from http.cookies import SimpleCookie, CookieError
        try:
            cookies = SimpleCookie(self.headers.get('Cookie', ''))
        except CookieError:
            cookies = SimpleCookie()
        cookie = cookies.get('fh_install')
        sid = cookie.value if cookie else ''
        now = int(time.time())
        key = self.runtime.csrf_key

        def digest(label, value):
            return hmac.new(key, (label + value).encode(), hashlib.sha256).hexdigest()

        if re.fullmatch(r'[0-9a-f]{48}\.[0-9]{1,12}\.[0-9a-f]{64}', sid):
            payload, mac = sid.rsplit('.', 1)
            if int(payload.rsplit('.', 1)[1]) > now and secrets.compare_digest(mac, digest('session:', payload)):
                return sid, digest('csrf:', sid)
        if not create:
            return '', ''
        payload = secrets.token_hex(24) + '.' + str(now + 3600)
        sid = payload + '.' + digest('session:', payload)
        return sid, digest('csrf:', sid)

    def do_GET(self):
        if not self.host_ok():
            return self.send(403, {'error': 'Недопустимый адрес сервера.'})
        path = urlsplit(self.path).path
        try:
            if path == '/api/onboarding/offline':
                return self.send(503, {'error': 'Хранилище недоступно. Подключите прежний диск.', 'storageSettings': '/storage'})
            if path == '/api/onboarding/gate':
                self.runtime.check_storage()
                return self.send(204 if self.runtime.ready else (403 if self.runtime.config else 401))
            if path == '/api/onboarding/status':
                sid, token = self.session(create=True)
                if self.runtime.child and self.runtime.child.poll() is not None:
                    self.runtime.ready = False
                    self.runtime.error = 'Сервер остановлен. Настройки сохранены; повторная установка не запущена.'
                data = {'configured': self.runtime.config is not None, 'ready': self.runtime.ready,
                        'csrf': token, 'error': self.runtime.error, 'version': 2, 'claimed': self.runtime.owner is not None}
                data['setupCodeRequired'] = not self.runtime.owner and not self.runtime.config
                auth=self.runtime.auth(self.headers.get('Cookie'))
                data['authenticated']=bool(auth and auth.get('role')=='admin')
                if data['authenticated']:data['username']=auth['username']
                if not self.runtime.config:
                    data['storages'] = self.runtime.choices()
                return self.send(200, data, headers={'Set-Cookie': self.browser_cookie('fh_install', sid)})
            if path == '/api/onboarding/draft':
                if not self.admin():return
                if self.runtime.config:return self.send(409, {'error':'Настройка уже завершена.'})
                with self.runtime.lock:
                    return self.send(200, {'draft':setup_draft.read(self.runtime.state,self.runtime.owner)})
            if path == '/api/onboarding/folders':
                if not self.admin():
                    return
                if not self.session()[0]:
                    return self.send(403, {'error': 'Сначала откройте мастер настройки.'})
                query = parse_qs(urlsplit(self.path).query, keep_blank_values=True)
                return self.send(200, self.runtime.listing(query.get('storage', [''])[0], query.get('path', [''])[0]))
            if path == '/api/onboarding/info':
                auth = self.runtime.auth(self.headers.get('Cookie'))
                if not auth:
                    return self.send(401, {'error': 'Войдите в Filehub, чтобы открыть настройки.'})
                config = self.runtime.config
                if not config:
                    return self.send(409, {'error': 'Сначала выберите источники и завершите настройку.'})
                source, photo_path, phone_folder = self.runtime.photo_destination(auth)
                values = {'serverUrl': self.runtime.catalog['public_url'], 'username': auth.get('username'),
                          'photoSource': source, 'phoneFolder': phone_folder,
                          'photoPath': photo_path, 'version': self.runtime.app_version()}
                if auth.get('role') == 'admin':
                    values.update(sources=config['sources'], photoDirs=config['photoDirs'])
                return self.send(200, values)
            if path == '/api/onboarding/connection':
                auth = self.runtime.auth(self.headers.get('Cookie'))
                if not auth:
                    return self.send(401, {'error': 'Войдите в Filehub.'})
                if not self.runtime.config:
                    return self.send(409, {'error': 'Завершите настройку хранилища.'})
                source, _, phone_folder = self.runtime.photo_destination(auth)
                base = self.runtime.catalog['public_url']
                app = Path(self.runtime.catalog['app_dir'])
                version = json.loads((app / 'version.json').read_text()) if (app / 'version.json').is_file() else None
                return self.send(200, {'serverUrl': base, 'connectUrl': 'filehub://connect?server=' + quote(base, safe=''),
                                       'backupSource': source, 'backupFolder': phone_folder,
                                       'canUpload': auth['role'] != 'viewer', 'appAvailable': version is not None and (app / 'app.apk').is_file(),
                                       'appVersion': version})
            if path == '/api/onboarding/sources':
                auth = self.runtime.auth(self.headers.get('Cookie'))
                if not auth:
                    return self.send(401, {'error': 'Войдите в Filehub.'})
                if not self.runtime.config:
                    return self.send(409, {'error': 'Завершите настройку хранилища.'})
                if auth['folders'] is not None:
                    grants = auth['folders']['folders'] + ([auth['folders']['personal']] if auth['folders'].get('personal') else [])
                    visible = {grant['source'] for grant in grants}
                    return self.send(200, {'sources': [{'name': source['name']} for source in self.runtime.config['sources'] if source['name'] in visible]})
                values = []
                for source in self.runtime.config['sources']:
                    try:
                        path = self.runtime.folder(source['storage'], source['path'])
                        usage = os.statvfs(path)
                        values.append({'name': source['name'], 'total': usage.f_blocks * usage.f_frsize,
                                       'free': usage.f_bavail * usage.f_frsize, 'online': True})
                    except (Invalid, OSError):
                        values.append({'name': source['name'], 'total': 0, 'free': 0, 'online': False})
                return self.send(200, {'sources': values})
            if path == '/api/onboarding/storage':
                if not self.admin():
                    return
                if not self.runtime.config:
                    return self.send(409, {'error': 'Завершите настройку хранилища.'})
                return self.send(200, self.runtime.storage_info())
            if path == '/api/onboarding/devices':
                if not self.admin():
                    return
                diagnostics = mounting.info(self.runtime.catalog)
                if not self.runtime.storage_model.host:
                    return self.send(200, {'available': False, 'volumes': [], 'connection': diagnostics,
                                          'reason': 'Диски хоста ещё не подключены к Filehub.'})
                try:
                    return self.send(200, dict(self.runtime.storage_model.host.request('/v1/volumes'), available=True, connection=diagnostics))
                except Invalid:
                    return self.send(200, {'available': False, 'volumes': [], 'connection': diagnostics,
                                          'reason': 'Связь с сервисом дисков потеряна. Проверьте подключение на хосте.'})
            assets = {'/setup': 'setup.html', '/connect': 'connect.html',
                      '/onboarding/style.css': 'style.css', '/onboarding/setup.js': 'setup.js',
                      '/onboarding/connect.js': 'connect.js', '/storage': 'storage.html',
                      '/onboarding/storage.js': 'storage.js', '/onboarding/selection.js': 'selection.js'}
            assets['/onboarding/devices.js'] = 'devices.js'
            assets['/onboarding/theme.js'] = 'theme.js'
            assets['/onboarding/language.js'] = 'language.js'
            if path == '/onboarding/golos-license.txt':
                return self.send(200, (ASSETS / 'golos-license.txt').read_bytes(), 'text/plain; charset=utf-8')
            if path == '/onboarding/golos.woff2':
                return self.send(200, (ASSETS / 'golos.woff2').read_bytes(), 'font/woff2')
            if path in assets:
                name = assets[path]
                content_type = 'text/html; charset=utf-8' if name.endswith('.html') else ('text/css; charset=utf-8' if name.endswith('.css') else 'text/javascript; charset=utf-8')
                return self.send(200, (ASSETS / name).read_bytes(), content_type)
            self.send(404, {'error': 'Страница не найдена.'})
        except (Invalid, OSError, ValueError, sqlite3.Error, RecursionError) as error:
            # File names are returned only by the bounded browser, never exception traces.
            self.send(400, {'error': str(error) if isinstance(error, Invalid) else 'Не удалось открыть хранилище. Проверьте подключение и права.'})

    def admin(self):
        auth = self.runtime.auth(self.headers.get('Cookie'))
        if not auth:
            self.send(401, {'error': 'Войдите в Filehub.'})
            return False
        if auth.get('role') != 'admin':
            self.send(403, {'error': 'Настройки хранилищ доступны администратору.'})
            return False
        return True

    def do_POST(self):
        if not self.host_ok():
            return self.send(403, {'error': 'Недопустимый адрес сервера.'})
        origin = self.headers.get('Origin', '')
        expected = urlsplit(self.runtime.catalog['public_url']).scheme + '://' + self.headers.get('Host', '')
        token = self.session()[1]
        if origin != expected or not token or not secrets.compare_digest(self.headers.get('X-CSRF-Token', '').encode(), token.encode()):
            return self.send(403, {'error': 'Обновите страницу настройки и повторите.'})
        if self.path not in ('/api/onboarding/setup', '/api/onboarding/claim', '/api/onboarding/storage', '/api/onboarding/attach', '/api/onboarding/login', '/api/onboarding/validate', '/api/onboarding/draft'):
            return self.send(404, {'error': 'Страница не найдена.'})
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if length < 2 or length > 65536 or self.headers.get('Content-Type') != 'application/json':
                raise Invalid('Неверный запрос настройки.')
            data = json.loads(self.rfile.read(length))
            if not isinstance(data, dict):
                raise Invalid('Неверный запрос настройки.')
            if self.path in ('/api/onboarding/login', '/api/onboarding/claim'):
                action = self.runtime.claim if self.path.endswith('/claim') else self.runtime.login
                cookie = action(data, self.headers.get('X-Real-IP', 'local'))
                return self.send(200, {'ok': True}, headers={'Set-Cookie': self.browser_cookie('fh_session', cookie)})
            if self.path == '/api/onboarding/setup':
                if not self.admin():return
                self.runtime.install(data,self.runtime.auth(self.headers.get('Cookie')))
            else:
                if not self.admin():
                    return
                if self.path == '/api/onboarding/draft':
                    if self.runtime.config:raise Invalid('Настройка уже завершена.')
                    with self.runtime.lock:setup_draft.write(self.runtime.state,self.runtime.owner,data)
                    return self.send(200, {'saved':True})
                if self.path == '/api/onboarding/validate':
                    return self.send(200, self.runtime.storage_model.review(data.get('sources'), data.get('photoDirs'), previous=self.runtime.config))
                if self.path == '/api/onboarding/attach':
                    if set(data) != {'uuid'} or not self.runtime.storage_model.host:
                        raise Invalid('Сервис подключения дисков не настроен.')
                    result = self.runtime.storage_model.host.request('/v1/attach', data)
                    self.runtime.storage_model.host_cache = None
                    return self.send(200, result)
                self.runtime.reconfigure(data)
            self.send(200, {'configured': True, 'ready': self.runtime.ready, 'error': self.runtime.error})
        except PhotoConfirmationRequired as error:
            self.send(400, {'error': str(error), 'step': 2, 'code': 'photo-confirmation'})
        except (Invalid, OSError, ValueError, sqlite3.Error, RecursionError) as error:
            self.send(400, {'error': str(error) if isinstance(error, Invalid) else 'Не удалось сохранить настройки. Проверьте свободное место и права хранилища.'})


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
    allow_reuse_address = True
    request_queue_size = 8

    def __init__(self, path, runtime):
        self.runtime = runtime
        self.slots = threading.BoundedSemaphore(8)
        super().__init__(path, Handler)
        os.chmod(path, 0o600)

    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        request.settimeout(15)
        try:
            super().process_request(request, address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()


def main():
    os.umask(0o077)
    catalog = json.loads(Path(sys.argv[1]).read_text())
    runtime = Runtime(catalog)
    path = Path(catalog['socket'])
    if path.exists():
        if not stat.S_ISSOCK(path.lstat().st_mode):
            raise RuntimeError('Refusing to replace non-socket path')
        path.unlink()  # Only this service's stale runtime socket.
    server = Server(str(path), runtime)
    def stop(*_):
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    stopped = threading.Event()
    def monitor():
        while not stopped.wait(1):
            try:
                runtime.check_storage()
            except (OSError, Invalid):
                runtime.ready = False
    threading.Thread(target=monitor, daemon=True).start()
    try:
        server.serve_forever(poll_interval=.2)
    finally:
        stopped.set()
        runtime.stop()
        server.server_close()


if __name__ == '__main__':
    main()
