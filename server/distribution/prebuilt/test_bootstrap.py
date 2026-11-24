"""Owner enrollment through the real HTTP handler; synthetic files, no builds."""
import http.client
import json
from pathlib import Path
import socket
import tempfile
import threading
import unittest
from unittest.mock import patch
from launcher import Runtime, Server, bootstrap_code
from storage_model import Invalid


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='filehub-enrollment-')
        root = Path(self.temp.name)
        self.state = root/'state'; self.state.mkdir()
        self.data = root/'data'; self.data.mkdir()
        self.catalog = {'state_dir':str(self.state),'storages':[{'id':'server','name':'Server','root':str(self.data)}],
                        'backend_port':18081,'public_url':'http://filehub.test:8080','binary':'/not-real','app_dir':str(root/'app')}
        self.runtime = Runtime(self.catalog)
        self.account = {'username':'owner','password':'synthetic-bootstrap-password'}
        self.code = bootstrap_code(self.state)

    def tearDown(self): self.temp.cleanup()  # Only this test's synthetic fixture.

    def test_missing_wrong_and_non_ascii_code_cannot_create_owner(self):
        for code in ('', '0'*32, 'чужой код'):
            with self.assertRaises(Invalid): self.runtime.claim({**self.account,'setupCode':code})
        self.assertIsNone(self.runtime.owner)
        self.assertFalse(self.runtime.owner_path.exists())
        self.assertFalse((self.state/'secret').exists())

    def test_code_private_durable_and_claim_is_bound_to_owner(self):
        self.assertEqual((self.state/'setup-code.json').stat().st_mode & 0o777, 0o600)
        self.assertEqual(bootstrap_code(self.state), self.code)
        cookie = self.runtime.claim({**self.account,'setupCode':self.code})
        resumed = Runtime(self.catalog)
        self.assertEqual(resumed.auth('fh_session='+cookie)['username'], 'owner')
        with self.assertRaises(Invalid): resumed.claim({'username':'intruder','password':'another-password','setupCode':self.code})
        self.assertEqual(resumed.owner['username'], 'owner')
        # A retained code cannot reset an existing account; normal resume needs its password.
        self.assertTrue(resumed.claim(self.account))

    def test_direct_setup_requires_claim_and_authenticated_owner(self):
        payload = {**self.account,'sources':[{'name':'Main','storage':'server','path':'Files'}],
                   'photoDirs':[{'source':'Main','path':'Photos'}]}
        with self.assertRaises(Invalid): self.runtime.install(payload)
        self.assertFalse(self.runtime.config_path.exists()); self.assertFalse((self.data/'Files').exists())
        cookie = self.runtime.claim({**self.account,'setupCode':self.code})
        with self.assertRaises(Invalid): self.runtime.install(payload)
        with patch.object(self.runtime,'start'):
            self.runtime.install(payload, authenticated=self.runtime.auth('fh_session='+cookie))
        self.assertTrue((self.data/'Files/Photos').is_dir())
        with self.assertRaises(Invalid): self.runtime.claim({**self.account,'setupCode':self.code})

    def test_http_status_never_discloses_code_and_ownerless_setup_is_denied(self):
        path = self.state/'http.sock'; server = Server(str(path), self.runtime)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        def request(route, body=None, headers=None):
            c = http.client.HTTPConnection('localhost'); c.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); c.sock.connect(str(path))
            c.request('GET' if body is None else 'POST',route,None if body is None else json.dumps(body),
                      {'Host':'filehub.test:8080','Content-Type':'application/json',**(headers or {})})
            r=c.getresponse(); data=r.read(); result=(r.status,json.loads(data),dict(r.getheaders())); c.close(); return result
        try:
            code,status,headers=request('/api/onboarding/status')
            self.assertEqual(code,200); self.assertTrue(status['setupCodeRequired'])
            self.assertNotIn(self.code,json.dumps(status))
            auth={'Cookie':headers['Set-Cookie'].split(';')[0],'Origin':'http://filehub.test:8080','X-CSRF-Token':status['csrf']}
            sessions = {auth['Cookie']}
            for _ in range(70):
                code, _, headers = request('/api/onboarding/status')
                self.assertEqual(code, 200)
                sessions.add(headers['Set-Cookie'].split(';', 1)[0])
            self.assertEqual(len(sessions), 71)
            self.assertEqual(request('/api/onboarding/status', headers={'Cookie': auth['Cookie']})[1]['csrf'], status['csrf'])
            self.assertEqual(request('/api/onboarding/setup',self.account,auth)[0],401)
            self.assertEqual(request('/api/onboarding/folders?storage=server&path=')[0],401)
            self.assertEqual(request('/api/onboarding/claim',self.account,auth)[0],400)
            self.assertFalse(self.runtime.owner_path.exists())
            code, _, headers = request('/api/onboarding/claim', {**self.account, 'setupCode': self.code}, auth)
            self.assertEqual(code, 200)
            owner_cookie = headers['Set-Cookie'].split(';', 1)[0]
            code, status, headers = request('/api/onboarding/status', headers={'Cookie': owner_cookie})
            self.assertEqual(code, 200)
            self.assertTrue(status['authenticated'])
            install_cookie = headers['Set-Cookie'].split(';', 1)[0]
            self.assertEqual(request('/api/onboarding/folders?storage=server&path=', headers={
                'Cookie': f'{owner_cookie}; {install_cookie}',
            })[0], 200)
        finally:
            server.shutdown(); server.server_close(); thread.join()
