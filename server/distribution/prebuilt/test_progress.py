"""Private draft, session resume and whole-disk selection contracts; no builds."""
import copy
import http.client
import json
import os
from pathlib import Path
import socket
import sqlite3
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).parent))
import setup_draft
from launcher import Runtime, Server
from storage_model import Invalid


class ProgressTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='filehub-progress-')
        self.root=Path(self.temp.name)
        self.state=self.root/'state';self.state.mkdir()
        self.data=self.root/'data';self.data.mkdir()
        self.catalog={'state_dir':str(self.state),'storages':[{'id':'server','name':'Server','root':str(self.data)}],
                      'backend_port':18081,'public_url':'http://filehub.test:8080','binary':'/not-real','app_dir':str(self.root/'app')}
        self.draft={'step':2,'mode':'server','diskChosen':False,'sources':[{'name':'Main','storage':'server','path':''}],
                    'photoDirs':[{'source':'Main','path':'Photos','includeExisting':False}]}
        self.runtime=Runtime(self.catalog)
        self.cookie=self.runtime.claim({'username':'owner','password':'synthetic-progress-only','setupCode':__import__('launcher').bootstrap_code(self.state)})

    def tearDown(self):self.temp.cleanup()  # Only our synthetic fixture.

    def test_private_draft_survives_restart_and_matches_owner_version(self):
        setup_draft.write(self.state,self.runtime.owner,self.draft)
        self.assertEqual((self.state/'setup-draft.json').stat().st_mode&0o777,0o600)
        restarted=Runtime(self.catalog)
        self.assertEqual(setup_draft.read(self.state,restarted.owner),self.draft)
        self.assertEqual(restarted.auth('fh_session='+self.cookie)['username'],'owner')
        self.assertIsNone(setup_draft.read(self.state,dict(restarted.owner,session_version='another-owner')))
        self.assertNotIn('synthetic-progress-only',(self.state/'setup-draft.json').read_text())
        self.assertFalse((self.data/'Photos').exists())

    def test_credentials_and_unknown_fields_cannot_be_saved(self):
        cases=[]
        for place in ('root','source','photo'):
            value=copy.deepcopy(self.draft)
            (value if place=='root' else value['sources' if place=='source' else 'photoDirs'][0])['password']='never-save'
            cases.append(value)
        for value in cases:
            with self.assertRaises(Invalid):setup_draft.write(self.state,self.runtime.owner,value)
        self.assertFalse((self.state/'setup-draft.json').exists())

    def test_partial_paths_are_preserved_but_not_executed(self):
        self.draft['sources'][0]['path']='unfinished/'
        self.draft['photoDirs'][0]['path']='../not-executed'
        setup_draft.write(self.state,self.runtime.owner,self.draft)
        self.assertEqual(setup_draft.read(self.state,self.runtime.owner),self.draft)
        with self.assertRaises(Invalid):self.runtime.storage_model.validate(self.draft['sources'],self.draft['photoDirs'])
        self.assertEqual(list(self.data.iterdir()),[])

    def test_invalid_or_unclaimed_draft_is_ignored(self):
        for value in ('[]','{','{}'):
            (self.state/'setup-draft.json').write_text(value)
            self.assertIsNone(setup_draft.read(self.state,self.runtime.owner))
        self.assertIsNone(setup_draft.read(self.state,None))
        with self.assertRaises(Invalid):setup_draft.write(self.state,None,self.draft)

    def test_cookie_authenticated_completion_needs_no_raw_password(self):
        auth=self.runtime.auth('fh_session='+self.cookie)
        with patch.object(self.runtime,'start'):
            self.runtime.install({'username':'owner',**self.draft},authenticated=auth)
        self.assertEqual(self.runtime.config['sources'],self.draft['sources'])
        self.assertTrue((self.data/'Photos').is_dir())
        self.assertNotIn('synthetic-progress-only',self.runtime.config_path.read_text())

    def test_completion_cannot_use_another_user_or_viewer(self):
        for auth in (None,{'username':'intruder','role':'admin'},{'username':'owner','role':'viewer'}):
            with self.assertRaises(Invalid):self.runtime.install({'username':'owner',**self.draft},authenticated=auth)
        with self.assertRaises(Invalid):self.runtime.install({'username':'intruder',**self.draft},authenticated={'username':'owner','role':'admin'})
        self.assertFalse(self.runtime.config_path.exists())

    def test_draft_http_requires_admin_and_csrf_and_closes_after_setup(self):
        socket_path=self.root/'http.sock';server=Server(str(socket_path),self.runtime)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        def request(path,headers=None,data=None):
            connection=http.client.HTTPConnection('localhost');connection.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);connection.sock.connect(str(socket_path))
            connection.request('POST' if data is not None else 'GET',path,json.dumps(data) if data is not None else None,{'Host':'filehub.test:8080','Content-Type':'application/json',**(headers or {})})
            response=connection.getresponse();body=json.loads(response.read());response_headers=dict(response.getheaders());code=response.status;connection.close();return code,body,response_headers
        try:
            self.assertEqual(request('/api/onboarding/draft')[0],401)
            code,status,headers=request('/api/onboarding/status',{'Cookie':'fh_session='+self.cookie})
            self.assertTrue(status['authenticated']);self.assertEqual(status['username'],'owner')
            install_cookie=headers['Set-Cookie'].split(';')[0]
            auth={'Cookie':'fh_session='+self.cookie+'; '+install_cookie,'Origin':'http://filehub.test:8080','X-CSRF-Token':status['csrf']}
            self.assertEqual(request('/api/onboarding/draft',{'Cookie':auth['Cookie']},self.draft)[0],403)
            self.assertEqual(request('/api/onboarding/draft',auth,self.draft)[0],200)
            self.assertEqual(request('/api/onboarding/draft',auth)[1]['draft'],self.draft)
            self.assertEqual(request('/api/onboarding/draft',auth,{**self.draft,'password':'never-save'})[0],400)
            no_login={**auth,'Cookie':install_cookie}
            self.assertEqual(request('/api/onboarding/setup',no_login,{'username':'owner',**self.draft})[0],401)
            with patch.object(self.runtime,'start'):
                self.assertEqual(request('/api/onboarding/setup',auth,{'username':'owner',**self.draft})[0],200)
            with sqlite3.connect(self.state/'index.db') as db:
                db.execute('CREATE TABLE users(username TEXT,pwhash TEXT,role TEXT,session_version TEXT)')
                db.execute('INSERT INTO users VALUES(?,?,?,?)',('owner',self.runtime.owner['pwhash'],'admin',self.runtime.owner['session_version']))
            self.assertEqual(request('/api/onboarding/draft',auth)[0],409)
            before=(self.state/'setup-draft.json').read_bytes()
            self.assertNotEqual(request('/api/onboarding/draft',auth,self.draft)[0],200)
            self.assertEqual((self.state/'setup-draft.json').read_bytes(),before)
        finally:server.shutdown();server.server_close();thread.join()

    def test_whole_disk_reads_root_without_write_probe_or_permission_changes(self):
        storage=self.runtime.storage_model
        self.catalog['storages'][0]['mount']=str(self.data)
        (self.data/'Filehub').mkdir();(self.data/'Filehub'/'Photos').mkdir()
        sentinel=self.data/'existing-original.txt';sentinel.write_bytes(b'preserve')
        before=self.data.stat()
        actual_folder=storage.folder
        def folder(identifier,relative='',**kwargs):
            if relative=='':self.assertFalse(kwargs.get('writable',False))
            return actual_folder(identifier,relative,**kwargs)
        with patch('storage_model.mount_table',return_value={str(self.data):{'device':'test'}}),patch.object(storage,'folder',side_effect=folder):
            self.assertTrue(storage.choices()[0]['wholeDisk'])
            storage.validate(self.draft['sources'],[{'source':'Main','path':'Filehub/Photos'}],create=True)
        self.assertEqual(sentinel.read_bytes(),b'preserve')
        self.assertEqual((before.st_uid,before.st_gid,before.st_mode),(self.data.stat().st_uid,self.data.stat().st_gid,self.data.stat().st_mode))

    def test_preflight_rejects_new_photo_folder_under_readonly_parent(self):
        actual_access=os.access
        with patch('storage_model.os.access',side_effect=lambda path,mode:False if Path(path)==self.data and mode & os.W_OK else actual_access(path,mode)):
            with self.assertRaisesRegex(Invalid,'нет доступа для записи'):
                self.runtime.storage_model.review(self.draft['sources'],self.draft['photoDirs'])
        self.assertFalse((self.data/'Photos').exists())

    def test_listing_previews_only_real_visible_entries_without_scanning_children(self):
        (self.data/'Folder').mkdir();(self.data/'Folder'/'nested.jpg').write_bytes(b'x')
        (self.data/'photo.png').write_bytes(b'x');(self.data/'file.txt').write_bytes(b'x')
        (self.data/'.hidden').write_bytes(b'x');(self.data/'alias').symlink_to(self.state,target_is_directory=True)
        listing=self.runtime.listing('server','')
        self.assertEqual(listing['folders'],['Folder']);self.assertEqual(listing['mediaHere'],1)
        self.assertEqual({x['name'] for x in listing['samples']},{'Folder','photo.png','file.txt'})
        for index in range(2002):(self.data/('entry-'+str(index))).touch()
        listing=self.runtime.listing('server','')
        self.assertTrue(listing['truncated']);self.assertLessEqual(len(listing['samples']),8)


if __name__=='__main__':unittest.main()
