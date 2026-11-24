import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {checkExistingUpload} from '../src/upload-preflight.js';

test('browser sends only a small manifest and hashes the complete file in chunks', async t => {
  const content = Buffer.alloc(2 * 1024 * 1024 + 11, 65);
  const file = new File([content], 'video.mp4');
  const hash = createHash('sha256').update(content).digest('hex');
  const read = t.mock.method(file, 'slice');
  const endpoint = t.mock.method(globalThis, 'fetch', async (url, request) => {
    assert.equal(url, '/api/upload/preflight');
    assert.equal(request.method, 'POST');
    assert.ok(request.body.length < 250);
    const sent = JSON.parse(request.body);
    assert.equal(sent.source, 'Photos');
    assert.equal(sent.path, 'Videos/2026');
    assert.equal(sent.filename, 'video.mp4');
    assert.equal(sent.size, file.size);
    if (!sent.sha256) return Response.json({candidate: true});
    assert.equal(sent.sha256, hash);
    return Response.json({source: sent.source, path: 'Videos/2026/video (1).mp4', size: sent.size, sha256: hash});
  });
  const receipt = await checkExistingUpload(file, 'Photos', 'Videos/2026', new AbortController().signal);
  assert.equal(receipt?.path, 'Videos/2026/video (1).mp4');
  assert.equal(endpoint.mock.callCount(), 2);
  assert.equal(read.mock.callCount(), 3);
});

test('browser leaves uploads to TUS when an old server has no preflight route', async t => {
  const file = new File(['new bytes'], 'video.mp4');
  const read = t.mock.method(file, 'slice');
  t.mock.method(globalThis, 'fetch', async () => new Response(null, {status: 404}));
  const receipt = await checkExistingUpload(file, 'Photos', '', new AbortController().signal);
  assert.equal(receipt, null);
  assert.equal(read.mock.callCount(), 0);
});
