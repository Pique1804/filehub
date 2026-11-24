"""Storage/auth contracts on synthetic files. No Go, devices or builds."""
import hashlib
import hmac
import http.client
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import socket
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch, Mock

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
from launcher import Runtime, Server, crypt, atomic_create
from storage_model import Storage, Invalid, legacy_config, overlaps_state


class StorageTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='filehub-storage-')
        self.root = Path(self.directory.name)
        self.state, self.data = self.root / 'state', self.root / 'data'
        self.state.mkdir(); self.data.mkdir()
        self.catalog = {'state_dir': str(self.state), 'storages': [
            {'id': 'server', 'name': 'Папка сервера', 'root': str(self.data)}],
            'backend_port': 18081, 'public_url': 'http://filehub.test:8080',
            'app_dir': str(self.root / 'app'), 'binary': '/not-a-real-binary'}
        self.storage = Storage(self.catalog, self.state)
        self.sources = [{'name': 'Main', 'storage': 'server', 'path': ''}]
        self.scopes = [{'source': 'Main', 'path': 'Photos'}]

    def tearDown(self):
        self.directory.cleanup()  # Only this test's newly created synthetic fixture.

    def runtime(self):
        return Runtime(self.catalog)

    def test_configured_installation_with_missing_database_requires_recovery(self):
        runtime = self.install()
        database = self.state / 'index.db'
        with self.assertRaises(FileNotFoundError):
            runtime.start()  # The fixture uses a binary path that does not exist.
        self.assertTrue(database.is_file())
        self.assertFalse(runtime.initial_database.exists())
        database.rename(self.state / 'index.saved.db')
        resumed = self.runtime()
        self.assertTrue(resumed.offline)
        self.assertIn('Восстановите index.db', resumed.error)
        self.assertFalse(database.exists())

    def test_overlaps_state_compares_physical_bind_roots(self):
        mounts = {
            '/': {'device': '8:1', 'root': '/'},
            '/srv/view': {'device': '8:1', 'root': '/var/lib/filehub'},
            '/srv/archive': {'device': '8:2', 'root': '/'},
        }
        state = Path('/var/lib/filehub/state')
        self.assertTrue(overlaps_state(Path('/srv/view'), state, mounts))
        self.assertFalse(overlaps_state(Path('/srv/archive'), state, mounts))

    def install(self):
        runtime = self.runtime()
        cookie=runtime.claim({'username':'owner','password':'synthetic-password-only','setupCode':__import__('launcher').bootstrap_code(self.state)})
        with patch.object(runtime, 'start'):
            runtime.install({'username': 'owner', 'password': 'synthetic-password-only',
                             'sources': self.sources, 'photoDirs': self.scopes}, authenticated=runtime.auth('fh_session='+cookie))
        return runtime

    def account(self, runtime, role='admin'):
        secret = b'synthetic-test-session-key-32bytes'
        (self.state / 'secret').write_bytes(secret)
        with sqlite3.connect(self.state / 'index.db') as db:
            db.execute('CREATE TABLE users(username TEXT,pwhash TEXT,role TEXT,session_version TEXT)')
            db.execute('INSERT INTO users VALUES(?,?,?,?)', ('owner', runtime.config['adminHash'], role, 'v1'))
        message = 'owner|' + str(int(time.time())+3600) + '|v1'
        return message + '|' + hmac.new(secret, message.encode(), hashlib.sha256).hexdigest()

    def test_default_folder_without_hdd_and_nested_photos_are_one_source(self):
        (self.data / 'document.txt').write_text('preserve existing original')
        runtime = self.install()
        self.assertEqual(runtime.config['sources'], self.sources)
        self.assertEqual(runtime.config['photoDirs'], self.scopes)
        self.assertTrue((self.data / 'Photos').is_dir())
        self.assertEqual((self.data / 'document.txt').read_text(), 'preserve existing original')
        self.assertNotIn('synthetic-password-only', runtime.config_path.read_text())
        self.assertEqual(runtime.config_path.stat().st_mode & 0o777, 0o600)

    def test_setup_can_skip_photos_and_choose_them_later(self):
        original = self.data / 'original.jpg'
        original.write_bytes(b'unchanged fixture')
        self.scopes = []
        runtime = self.install()
        self.assertEqual(runtime.config['sources'], self.sources)
        self.assertEqual(runtime.config['photoDirs'], [])
        self.assertEqual(runtime.photo_destination({'folders': None}), ('', '', ''))
        self.assertFalse((self.data / 'Photos').exists())
        self.assertEqual(original.read_bytes(), b'unchanged fixture')
        sources, scopes = runtime.storage_model.validate(
            self.sources, [{'source': 'Main', 'path': 'Photos'}],
            create=True, previous=runtime.config,
        )
        self.assertEqual(sources, self.sources)
        self.assertEqual(scopes, [{'source': 'Main', 'path': 'Photos'}])
        self.assertTrue((self.data / 'Photos').is_dir())

    def test_claimed_admin_survives_restart_before_storage_selection(self):
        runtime=self.runtime()
        account={'username':'owner','password':'synthetic-password-only','setupCode':__import__('launcher').bootstrap_code(self.state)}
        cookie=runtime.claim(account)
        self.assertFalse((self.state/'index.db').exists())
        self.assertFalse(runtime.config_path.exists())
        self.assertEqual(runtime.auth('fh_session='+cookie)['role'],'admin')
        resumed=self.runtime()
        self.assertEqual(resumed.auth('fh_session='+cookie)['username'],'owner')
        with self.assertRaises(Invalid):resumed.claim({'username':'intruder','password':'different-password'})
        with self.assertRaises(Invalid):resumed.install({'username':'intruder','password':'different-password',
                                                        'sources':self.sources,'photoDirs':self.scopes})
        self.assertNotIn(account['password'],runtime.owner_path.read_text())
        self.assertEqual(runtime.owner_path.stat().st_mode&0o777,0o600)

    def test_unprepared_container_has_authenticated_diagnostics_and_recovers_missing_helper(self):
        runtime=self.runtime()
        cookie=runtime.claim({'username':'owner','password':'synthetic-password-only','setupCode':__import__('launcher').bootstrap_code(self.state)})
        server=Server(str(self.root/'connection.sock'),runtime)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        def get(auth):
            import socket
            connection=http.client.HTTPConnection('localhost')
            connection.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
            connection.sock.connect(str(self.root/'connection.sock'))
            connection.request('GET','/api/onboarding/devices',headers={'Host':'filehub.test:8080', 'Cookie':auth})
            response=connection.getresponse();body=json.loads(response.read());connection.close()
            return response.status,body
        try:
            self.assertEqual(get('')[0],401)
            code,data=get('fh_session='+cookie)
            self.assertEqual(code,200)
            self.assertFalse(data['available'])
            self.assertEqual(data['volumes'],[])
            self.assertEqual(data['connection']['serverUrl'],self.catalog['public_url'])
            from storage_model import HostClient
            runtime.storage_model.host=HostClient(str(self.root/'absent-host.sock'))
            code,data=get('fh_session='+cookie)
            self.assertEqual(code,200)
            self.assertFalse(data['available'])
            self.assertIn('Связь',data['reason'])
            self.assertFalse(runtime.config_path.exists())
        finally:server.shutdown();server.server_close();thread.join()

    def test_multiple_sources_and_photo_scopes_preserve_identity(self):
        extra = self.root / 'second'; extra.mkdir()
        self.catalog['storages'].append({'id': 'second', 'root': str(extra), 'name': 'Second'})
        sources = self.sources + [{'name': 'HDD2', 'storage': 'second', 'path': ''}]
        scopes = self.scopes + [{'source': 'HDD2', 'path': 'Camera'}, {'source': 'HDD2', 'path': 'Archive'}]
        actual, photos = self.storage.validate(sources, scopes, create=True)
        self.assertEqual([s['name'] for s in actual], ['Main','HDD2'])
        self.assertEqual(photos, scopes)
        for folder in ('Camera','Archive'): self.assertTrue((extra / folder).is_dir())

    def test_existing_subfolder_on_read_only_filesystem_does_not_probe_write(self):
        photos = self.data / 'Family' / 'Photos'
        photos.mkdir(parents=True)
        sources = [{**self.sources[0], 'path': 'Family'}]
        with patch('storage_model.os.statvfs', return_value=Mock(f_flag=os.ST_RDONLY)), \
                patch.object(self.storage, 'folder', wraps=self.storage.folder) as folder:
            self.storage.validate(sources, self.scopes, create=True)
        write_checks = [call.kwargs['writable'] for call in folder.call_args_list if call.kwargs.get('create')]
        self.assertEqual(write_checks, [False, False])
        self.assertEqual(list(photos.iterdir()), [])

    def test_existing_photo_import_requires_consent_without_changing_original(self):
        (self.data / 'Photos').mkdir()
        original = self.data / 'Photos' / 'original.jpg'; original.write_bytes(b'unchanged fixture')
        with self.assertRaises(Invalid): self.storage.validate(self.sources, self.scopes, create=True)
        self.scopes[0]['includeExisting'] = True
        self.storage.validate(self.sources, self.scopes, create=True)
        self.assertEqual(original.read_bytes(), b'unchanged fixture')

    def test_photo_review_is_read_only_and_reports_each_unconfirmed_folder(self):
        (self.data / 'Camera').mkdir()
        original = self.data / 'Camera' / 'photo.png'
        original.write_bytes(b'preserved original')
        scopes = [{'source':'Main','path':'Camera'}, {'source':'Main','path':'NewPhotos'}]
        review = self.storage.review(self.sources, scopes)
        self.assertFalse(review['valid'])
        self.assertTrue(review['photoDirs'][0]['needsConfirmation'])
        self.assertFalse(review['photoDirs'][1]['needsConfirmation'])
        self.assertFalse((self.data / 'NewPhotos').exists())
        self.assertFalse((self.state / 'installation.json').exists())
        self.assertEqual(original.read_bytes(), b'preserved original')
        scopes[0]['includeExisting'] = True
        self.assertTrue(self.storage.review(self.sources, scopes)['valid'])
        previous = {'sources':self.sources, 'photoDirs':[{'source':'Main','path':'Camera'}]}
        scopes[0]['includeExisting'] = False
        self.assertTrue(self.storage.review(self.sources, scopes, previous=previous)['valid'])

    def test_new_files_between_review_and_setup_still_require_confirmation(self):
        from storage_model import PhotoConfirmationRequired
        self.assertTrue(self.storage.review(self.sources, self.scopes)['valid'])
        (self.data / 'Photos').mkdir()
        (self.data / 'Photos' / 'appeared.png').write_bytes(b'new original')
        with self.assertRaises(PhotoConfirmationRequired):
            self.storage.validate(self.sources, self.scopes, create=True)
        self.assertFalse((self.state / 'installation.json').exists())
        for path in ('../outside','/etc','Photo//folder'):
            with self.subTest(path=path), self.assertRaises(Invalid):
                self.storage.review(self.sources, [{'source':'Main','path':path}])

    def test_parent_scope_and_multiple_photo_folders_do_not_require_separate_roots(self):
        sources, scopes = self.storage.validate(self.sources, [{'source':'Main','path':''}], create=True)
        self.assertEqual(scopes, [{'source':'Main','path':''}])
        self.assertEqual(self.storage.paths({'sources':sources})['Main'], self.data)

    def test_traversal_links_duplicate_names_and_overlap_are_rejected(self):
        (self.data / 'escape').symlink_to(self.state, target_is_directory=True)
        for relative in ('../state','/etc','escape','.hidden','one//two','one\\two','one:two','Photos ',' Photos'):
            with self.subTest(relative=relative), self.assertRaises(Invalid):
                self.storage.validate(self.sources, [{'source':'Main','path':relative}], create=True)
        for name in ('Main','main'):
            with self.assertRaises(Invalid):
                self.storage.validate(self.sources+[{'name':name,'storage':'server','path':'Other'}], self.scopes)
        with self.assertRaises(Invalid):
            self.storage.validate(self.sources+[{'name':'Other','storage':'server','path':'Other'}], self.scopes)

    def test_existing_source_cannot_be_remapped_renamed_or_removed(self):
        previous = {'sources':self.sources,'photoDirs':self.scopes}
        for candidate in ([{'name':'New','storage':'server','path':''}], [{'name':'Main','storage':'server','path':'Other'}]):
            with self.assertRaises(Invalid): self.storage.validate(candidate,[{'source':candidate[0]['name'],'path':'Photos'}],previous=previous)

    def test_legacy_migration_keeps_old_receipt_and_album_source_names(self):
        old={'version':1,'files':{'storage':'server','path':'Files'},'photos':{'storage':'server','path':'Pictures'},'username':'owner'}
        updated=legacy_config(old)
        self.assertEqual(updated['sources'],[{'name':'Files','storage':'server','path':'Files'},{'name':'Photos','storage':'server','path':'Pictures'}])
        self.assertEqual(updated['photoDirs'],[{'source':'Photos','path':''}])
        self.assertEqual(old['version'],1)

    def test_missing_mount_blocks_fallback_folder_creation(self):
        self.catalog['storages'][0]['mount'] = str(self.data)
        with patch('storage_model.mount_table',return_value={}):
            with self.assertRaises(Invalid): self.storage.validate(self.sources,self.scopes,create=True)
        self.assertFalse((self.data/'Photos').exists())

    def test_live_device_change_stops_server_and_keeps_offline_source(self):
        runtime=self.install();runtime.ready=True;runtime.source_devices={'Main':-1};runtime.child=Mock();runtime.child.poll.return_value=None
        runtime.check_storage()
        self.assertFalse(runtime.ready);runtime.child.terminate.assert_called_once()
        self.assertEqual(runtime.config['sources'],self.sources)

    def test_cookie_auth_works_offline_and_checks_current_role_and_revocation(self):
        runtime=self.install();cookie=self.account(runtime)
        self.assertFalse(runtime.ready)
        self.assertEqual(runtime.auth('fh_session='+cookie)['role'],'admin')
        with sqlite3.connect(self.state/'index.db') as db: db.execute("UPDATE users SET role='viewer'")
        self.assertEqual(runtime.auth('fh_session='+cookie)['role'],'viewer')
        with sqlite3.connect(self.state/'index.db') as db: db.execute("UPDATE users SET session_version='v2'")
        self.assertIsNone(runtime.auth('fh_session='+cookie))
        self.assertIsNone(runtime.auth('fh_session='+cookie[:-1]+'x'))

    def test_offline_login_is_throttled_and_uses_current_password(self):
        runtime=self.install();self.account(runtime)
        token=runtime.login({'username':'owner','password':'synthetic-password-only','setupCode':__import__('launcher').bootstrap_code(self.state)},'client1')
        self.assertEqual(runtime.auth('fh_session='+token)['username'],'owner')
        for _ in range(5):
            with self.assertRaises(Invalid): runtime.login({'username':'owner','password':'wrong-synthetic-password'},'client2')
        with self.assertRaisesRegex(Invalid,'15 минут'): runtime.login({'username':'owner','password':'synthetic-password-only','setupCode':__import__('launcher').bootstrap_code(self.state)},'client2')

    def test_start_passes_arbitrary_sources_and_scopes_to_published_binary(self):
        runtime=self.install();self.account(runtime)
        connection=Mock();connection.getresponse.return_value.status=200
        child=Mock();child.poll.return_value=None
        with patch('launcher.subprocess.Popen',return_value=child) as start, patch('launcher.http.client.HTTPConnection',return_value=connection):
            runtime.start()
        environment=start.call_args.kwargs['env']
        self.assertEqual(environment['FILEHUB_SOURCES'],'Main:'+str(self.data))
        self.assertEqual(environment['FILEHUB_PHOTO_DIRS'],'Main:Photos')

    def test_stale_settings_cannot_restart_or_replace_configuration(self):
        runtime=self.install();before=runtime.config_path.read_bytes()
        with patch.object(runtime,'stop') as stop:
            with self.assertRaises(Invalid):runtime.reconfigure({'revision':'stale','sources':self.sources,'photoDirs':self.scopes})
            stop.assert_not_called()
        self.assertEqual(runtime.config_path.read_bytes(),before)

    def test_settings_save_preserves_account_and_backs_up_previous_config(self):
        runtime=self.install();cookie=self.account(runtime)
        before=runtime.config_path.read_bytes()
        with patch.object(runtime,'start'):
            runtime.reconfigure({'revision':runtime.revision(),'sources':self.sources,'photoDirs':self.scopes})
        backups=list(self.state.glob('installation.previous-*.json'))
        self.assertEqual(len(backups),1)
        self.assertEqual(json.loads(backups[0].read_text()),json.loads(before))
        self.assertEqual(backups[0].stat().st_mode & 0o777,0o600)
        self.assertEqual(runtime.auth('fh_session='+cookie)['username'],'owner')

    def test_http_settings_require_admin_and_csrf_even_when_disk_offline(self):
        runtime=self.install();cookie=self.account(runtime,role='viewer')
        socket=self.root/'http.sock';server=Server(str(socket),runtime)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        from storage_model import HostClient
        client=HostClient(str(socket))
        def request(path,headers=None,data=None):
            import socket as network
            connection=http.client.HTTPConnection('localhost');connection.sock=network.socket(network.AF_UNIX,network.SOCK_STREAM);connection.sock.connect(str(socket))
            connection.request('POST' if data is not None else 'GET',path,json.dumps(data) if data is not None else None,{'Host':'filehub.test:8080',**(headers or {})})
            response=connection.getresponse();body=response.read();response_headers=dict(response.getheaders());code=response.status;connection.close();return code,body,response_headers
        try:
            self.assertEqual(request('/api/onboarding/storage')[0],401)
            self.assertEqual(request('/api/onboarding/storage',{'Cookie':'fh_session='+cookie})[0],403)
            _, state_body, state_headers = request('/api/onboarding/status')
            install_cookie = state_headers['Set-Cookie'].split(';',1)[0]
            headers = {'Origin':'http://filehub.test:8080', 'X-CSRF-Token':json.loads(state_body)['csrf'],
                       'Content-Type':'application/json', 'Cookie':install_cookie}
            candidate = {'sources':self.sources, 'photoDirs':self.scopes}
            self.assertEqual(request('/api/onboarding/validate',headers,candidate)[0],401)
            headers['Cookie'] += '; fh_session='+cookie
            self.assertEqual(request('/api/onboarding/validate',headers,candidate)[0],403)
            code, body, _ = request('/api/onboarding/connection',{'Cookie':'fh_session='+cookie})
            self.assertEqual(code,200)
            self.assertEqual(json.loads(body)['backupFolder'],'Photos/PhoneBackup')
            self.assertFalse(json.loads(body)['canUpload'])
            with sqlite3.connect(self.state/'index.db') as db:db.execute("UPDATE users SET role='admin'")
            before = runtime.config_path.read_bytes()
            code, review, _ = request('/api/onboarding/validate',headers,candidate)
            self.assertEqual(code,200)
            self.assertTrue(json.loads(review)['valid'])
            self.assertEqual(runtime.config_path.read_bytes(),before)
            headers['Origin'] = 'http://untrusted.invalid'
            self.assertEqual(request('/api/onboarding/validate',headers,candidate)[0],403)
            self.assertEqual(request('/api/onboarding/storage',{'Cookie':'fh_session='+cookie})[0],200)
            self.assertEqual(request('/api/onboarding/attach',{'Cookie':'fh_session='+cookie,'Content-Type':'application/json'}, {'uuid':'1234-ABCD'})[0],403)
        finally:
            server.shutdown();server.server_close();thread.join()


    def test_offline_metadata_uses_the_current_profile_grants(self):
        private_root = self.root / 'private'
        private_root.mkdir()
        self.catalog['storages'].append({'id': 'private', 'root': str(private_root), 'name': 'Private'})
        self.sources.append({'name': 'Private', 'storage': 'private', 'path': ''})
        self.scopes = [{'source': 'Private', 'path': 'Camera'}]
        runtime = self.install()
        cookie = self.account(runtime)
        self.assertFalse(runtime.ready)
        with sqlite3.connect(self.state / 'index.db') as db:
            db.execute('ALTER TABLE users ADD COLUMN folder_access TEXT')
        socket_path = str(self.root / 'metadata.sock')
        server = Server(socket_path, runtime)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()

        def read_metadata(route):
            connection = http.client.HTTPConnection('localhost', timeout=3)
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                client.connect(socket_path)
                connection.sock = client
                connection.request('GET', '/api/onboarding/' + route, headers={
                    'Host': 'filehub.test:8080', 'Cookie': 'fh_session=' + cookie,
                })
                response = connection.getresponse()
                self.assertEqual(response.status, 200)
                return json.loads(response.read())

        def set_grants(folders, personal=None):
            policy = json.dumps({'folders': folders, 'personal': personal})
            with sqlite3.connect(self.state / 'index.db') as db:
                db.execute("UPDATE users SET role='user', folder_access=?", (policy,))

        shared = {'source': 'Main', 'path': 'Shared'}
        personal = {'source': 'Main', 'path': 'MyFiles'}
        try:
            unrestricted = read_metadata('sources')['sources']
            self.assertEqual([source['name'] for source in unrestricted], ['Main', 'Private'])
            self.assertGreater(unrestricted[1]['total'], 0)
            self.assertEqual(read_metadata('connection')['backupFolder'], 'Camera/PhoneBackup')
            self.assertIn('photoDirs', read_metadata('info'))
            set_grants([shared])
            self.assertEqual(read_metadata('sources')['sources'], [{'name': 'Main'}])
            info = read_metadata('info')
            self.assertNotIn('photoDirs', info)
            self.assertNotIn('sources', info)
            self.assertEqual((info['photoSource'], info['photoPath'], info['phoneFolder']), ('', '', ''))
            connection = read_metadata('connection')
            self.assertFalse(connection['canUpload'])
            self.assertEqual((connection['backupSource'], connection['backupFolder']), ('', ''))
            set_grants([], personal)
            self.assertEqual(read_metadata('sources')['sources'], [{'name': 'Main'}])
            connection = read_metadata('connection')
            self.assertTrue(connection['canUpload'])
            self.assertEqual((connection['backupSource'], connection['backupFolder']), ('Main', 'MyFiles'))
            set_grants([])
            self.assertEqual(read_metadata('sources')['sources'], [])
            self.assertFalse(read_metadata('connection')['canUpload'])
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__=='__main__':unittest.main()
