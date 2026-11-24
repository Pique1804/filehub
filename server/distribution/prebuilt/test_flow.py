#!/usr/bin/env python3
"""Exercise a separate unconfigured instance; preserves all generated fixtures."""
import base64
import hashlib
import http.cookiejar
import json
from pathlib import Path
import secrets
import struct
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib


def main():
    url = sys.argv[1].rstrip('/')
    root = Path(sys.argv[2])
    state = Path(sys.argv[3])
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    checks = []

    def request(path, data=None, headers=None, method=None, code=200):
        req = urllib.request.Request(url + path, data=data, headers=headers or {}, method=method)
        try:
            result = client.open(req, timeout=20)
        except urllib.error.HTTPError as error:
            result = error
        body = result.read()
        assert result.code == code, (path, result.code, body[:180])
        return body, result.headers

    def get(path):
        return json.loads(request(path)[0])

    def post(data, token, origin=url, code=200):
        return request('/api/onboarding/setup', json.dumps(data).encode(),
                       {'Content-Type': 'application/json', 'X-CSRF-Token': token, 'Origin': origin}, code=code)

    assert 'Создайте аккаунт'.encode() in request('/')[0]
    status = get('/api/onboarding/status')
    assert not status['configured'] and not status['ready']
    assert len(status['storages']) >= 1 and status['storages'][0]['total'] > 0 and status['storages'][0]['available']
    token = status['csrf']
    checks.append('First visit opens setup, disk capacity/free space are real')
    password = 'test-only-' + secrets.token_urlsafe(18)
    account = {'username': 'installer-check', 'password': password,
               'sources': [{'name':'Main','storage':'server','path':'Documents'}],
               'photoDirs': [{'source':'Main','path':'Pictures'}]}
    request('/api/onboarding/devices',code=401)
    request('/api/onboarding/claim',json.dumps({'username':account['username'],'password':password,'setupCode':json.loads((state/'setup-code.json').read_text())['code']}).encode(),
            {'Content-Type':'application/json','X-CSRF-Token':token,'Origin':url})
    assert get('/api/onboarding/status')['claimed']
    request('/api/onboarding/devices')
    checks.append('Admin claims setup before device access; claimed session can inspect disks before final configuration')
    post(account, '', code=403)
    post(account, token, origin='http://other.invalid', code=403)
    assert not (state / 'installation.json').exists()
    request('/api/onboarding/folders?storage=server&path=../etc', code=400)
    root.joinpath('escape').symlink_to('/etc', target_is_directory=True)
    request('/api/onboarding/folders?storage=server&path=escape', code=400)
    assert 'escape' not in get('/api/onboarding/folders?storage=server&path=')['folders']
    post({**account, 'sources': account['sources']+[{'name':'Other','storage':'server','path':'Documents/Nested'}]}, token, code=400)
    assert not (root / 'Documents').exists()
    checks.append('CSRF/cross-origin/traversal/symlink/overlapping roots rejected before setup')
    old = root / 'Documents' / 'ExistingPictures'
    old.mkdir(parents=True)
    (old / 'preserve.txt').write_text('Existing user fixture: must remain untouched')
    post({**account, 'photoDirs':[{'source':'Main','path':'ExistingPictures'}]}, token, code=400)
    checks.append('Existing photo directory requires explicit approval; file preserved')
    result = json.loads(post(account, token)[0])
    assert result['configured'] and result['ready'], result
    config = json.loads((state / 'installation.json').read_text())
    assert password not in json.dumps(config) and config['adminHash'].startswith('$2')
    assert (state / 'installation.json').stat().st_mode & 0o777 == 0o600
    for private in ('/api/onboarding/info','/api/sources'):
        try:urllib.request.urlopen(url+private,timeout=10);raise AssertionError('Anonymous request accepted')
        except urllib.error.HTTPError as failure:assert failure.code==401
    assert get('/api/auth')['authed']  # The same claimed-owner cookie now verifies in Go.
    request('/api/login', urllib.parse.urlencode({'username': account['username'], 'password': password}).encode())
    assert get('/api/auth')['authed']
    sources = get('/api/sources')['sources']
    assert {s['name'] for s in sources} == {'Main'}
    assert get('/api/list?source=Main&path=Pictures')['entries'] == []
    photos = get('/api/photos')
    assert not photos.get('items') and not photos.get('photos'), photos
    info = get('/api/onboarding/info')
    assert info['sources'][0]['path'] == 'Documents' and info['photoPath'] == 'Pictures'
    connection = get('/api/connection')
    assert connection['backupSource'] == 'Main' and connection['backupFolder'] == 'Pictures/PhoneBackup'
    checks.append('Custom account verifies with Go bcrypt; only chosen folders exposed; empty library')
    assert 'Хранилища и телефон'.encode() in request('/')[0]
    assert b'data-photo-source' in request('/connect')[0]
    assert request('/api/qr?' + urllib.parse.urlencode({'data': url + '/connect'}))[1]['Content-Type'].startswith('image/')
    apk = request('/api/app/apk')[0]
    assert hashlib.sha256(apk).hexdigest() == '12873ee86c5cb8f5e6de5b231b777e35f1af915af56f6a1dc7fa03ddd3f3f9bb'
    checks.append('Storage/phone page, QR, actual published APK hash and in-app link')
    payload = b'Filehub installer protocol fixture: upload and preserve on restart.\n'
    metadata = ','.join(k+' '+base64.b64encode(v.encode()).decode() for k,v in
                        [('filename','installer-check.txt'),('source','Main'),('path','')])
    _, headers = request('/files/', b'', {'Tus-Resumable':'1.0.0','Upload-Length':str(len(payload)),
                                         'Upload-Metadata':metadata}, code=201)
    location = headers['Location']
    path = urllib.parse.urlsplit(location).path
    request(path, payload, {'Tus-Resumable':'1.0.0','Upload-Offset':'0',
                          'Content-Type':'application/offset+octet-stream'}, method='PATCH', code=204)
    downloaded = request('/api/download?source=Main&path=installer-check.txt')[0]
    assert downloaded == payload and (root / 'Documents' / 'installer-check.txt').read_bytes() == payload
    checks.append('TUS PATCH and download through gateway preserve exact bytes in selected storage')
    # The launcher-issued session must be accepted by the real published Go server.
    request('/api/onboarding/login', json.dumps({'username': account['username'], 'password': password}).encode(),
            {'Content-Type':'application/json', 'X-CSRF-Token':token, 'Origin':url})
    assert get('/api/auth')['authed']
    settings = get('/api/onboarding/storage')
    updated = {'sources': settings['sources'] + [{'name':'Archive','storage':'server','path':'Archive'}],
               'photoDirs': settings['photoDirs'] + [{'source':'Archive','path':'Camera'}],
               'revision': settings['revision']}
    request('/api/onboarding/storage', json.dumps(updated).encode(),
            {'Content-Type':'application/json', 'X-CSRF-Token':token, 'Origin':url})
    assert get('/api/onboarding/storage')['ready']
    assert {s['name'] for s in get('/api/sources')['sources']} == {'Main','Archive'}
    assert request('/api/download?source=Main&path=installer-check.txt')[0] == payload
    assert (root/'Archive/Camera').is_dir()
    request('/api/onboarding/storage', json.dumps(updated).encode(),
            {'Content-Type':'application/json', 'X-CSRF-Token':token, 'Origin':url}, code=400)
    checks.append('Real launcher login interoperates with Go; adding a source and photo scope restarts Go and preserves bytes; stale save rejected')
    # A small valid image, created without an image library or a build.
    def png_chunk(kind, content):
        return struct.pack('!I', len(content)) + kind + content + struct.pack('!I', zlib.crc32(kind + content))
    image = (b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', struct.pack('!2I5B', 64, 64, 8, 2, 0, 0, 0))
             + png_chunk(b'IDAT', zlib.compress((b'\x00' + b'\x30\x90\x60' * 64) * 64)) + png_chunk(b'IEND', b''))
    metadata = ','.join(k+' '+base64.b64encode(v.encode()).decode() for k,v in
                        [('filename','installer-photo.png'),('source','Main'),('path','Pictures')])
    _, headers = request('/files/', b'', {'Tus-Resumable':'1.0.0','Upload-Length':str(len(image)),
                                         'Upload-Metadata':metadata}, code=201)
    target = urllib.parse.urlsplit(headers['Location']).path
    request(target, image, {'Tus-Resumable':'1.0.0','Upload-Offset':'0',
                           'Content-Type':'application/offset+octet-stream'}, method='PATCH', code=204)
    assert request('/api/download?source=Main&path=Pictures/installer-photo.png')[0] == image
    assert request('/api/thumb?source=Main&path=Pictures/installer-photo.png&w=64')[1]['Content-Type'].startswith('image/')
    deadline = time.monotonic() + 20
    while True:
        gallery = get('/api/photos')['photos']
        if any(p.get('path') == 'Pictures/installer-photo.png' for p in gallery):
            break
        assert time.monotonic() < deadline, gallery
        time.sleep(1)
    checks.append('Uploaded real PNG appears in photo timeline; original bytes and generated thumbnail verified')
    post(account, token, code=400)
    request('/api/onboarding/folders?storage=server&path=')
    assert (old / 'preserve.txt').read_text() == 'Existing user fixture: must remain untouched'
    assert [p.name for p in (root / 'Documents' / 'Pictures').iterdir()] == ['installer-photo.png']
    session_path = state / 'test-session.json'
    session_path.write_text(json.dumps({'cookie': '; '.join(c.name+'='+c.value for c in jar),
                                      'payload_sha256': hashlib.sha256(payload).hexdigest()}))
    session_path.chmod(0o600)
    print(json.dumps({'result':'PASS','checks': checks}, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
