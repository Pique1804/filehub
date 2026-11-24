import test from 'node:test';
import assert from 'node:assert/strict';
import { operationSkipReason, readClipboardFiles, readDroppedTree, uploadFileReader, uploadParentPath } from '../src/file-operations.js';
import { uploadFingerprint } from '../src/media-utils.js';

test('folder uploads preserve the selected root and distinguish equal names in different subfolders', () => {
  const file = {name:'кадр.jpg', size:123, lastModified:100, webkitRelativePath:'Архив/Камера/кадр.jpg'};
  const first = uploadParentPath('Imports/2026', file);
  const second = uploadParentPath('Imports/2026', {...file, webkitRelativePath:'Архив/Телефон/кадр.jpg'});
  assert.equal(first, 'Imports/2026/Архив/Камера');
  assert.equal(second, 'Imports/2026/Архив/Телефон');
  assert.notEqual(uploadFingerprint(file, 'alice', 'A', first), uploadFingerprint(file, 'alice', 'A', second));
  assert.equal(uploadParentPath('', file), 'Архив/Камера');
  assert.equal(uploadParentPath('Imports/2026', {...file, webkitRelativePath:''}), 'Imports/2026');
});

test('folder uploads reject inconsistent or non-canonical picker paths', () => {
  for (const webkitRelativePath of ['/root/x', 'root//x', 'root/../x', 'root/./x', 'root\\sub/x', 'root/other', 'x']) {
    assert.equal(uploadParentPath('Imports', {name:'x', webkitRelativePath}), null, webkitRelativePath);
  }
});

test('batch skips are constrained to the same storage and the actual subtree', () => {
  const file = {source:'A', path:'notes.txt', isDir:false};
  const folder = {source:'A', path:'Архив/raw', isDir:true};
  assert.equal(operationSkipReason(file, {source:'A', path:''}), 'operationSameFolder');
  assert.equal(operationSkipReason(folder, {source:'A', path:'Архив'}), 'operationSameFolder');
  assert.equal(operationSkipReason(folder, {source:'A', path:'Архив/raw'}), 'operationInsideFolder');
  assert.equal(operationSkipReason(folder, {source:'A', path:'Архив/raw/sub'}), 'operationInsideFolder');
  assert.equal(operationSkipReason(folder, {source:'A', path:'Архив/raw2'}), '');
  assert.equal(operationSkipReason(folder, {source:'B', path:'Архив/raw/sub'}), '');
});

test('drop entries preserve populated and empty directories across reader pages', async () => {
  function directory(name, pages) {
    return {name, isDirectory:true, createReader() {
      let page = 0;
      return {readEntries: resolve => queueMicrotask(() => resolve(pages[page++] ?? []))};
    }};
  }
  const kept = {name:'test.txt'};
  const readable = {name:kept.name, isFile:true, file:resolve => resolve(kept)};
  const unreadable = {name:'offline.txt', isFile:true, file:(_, reject) => reject(new Error('offline'))};
  const tree = directory('Import', [[directory('empty', [])], [directory('nested', [[readable, unreadable]])]]);
  const plain = {name:'other.txt'};
  const entries = [
    {kind:'file', webkitGetAsEntry:() => tree},
    {kind:'file', webkitGetAsEntry:() => null, getAsFile:() => plain},
  ];
  const result = readDroppedTree({items:entries, files:[]});
  entries.length = 0;
  assert.deepEqual(await result, {files:[
    {file:kept, relativePath:'Import/nested/test.txt'}, {file:plain, relativePath:''},
  ], emptyDirectories:['Import/empty'], rejected:1});
  assert.deepEqual(await readDroppedTree({items:[], files:[plain]}),
    {files:[{file:plain, relativePath:''}], emptyDirectories:[], rejected:0});
});

test('file paste prefers actual files and also accepts file items', () => {
  const file = {name:'Screenshot.png'};
  const ignoredText = {kind:'string'};
  const imageItem = {kind:'file', getAsFile:() => file};
  assert.deepEqual(readClipboardFiles({files:[file], items:[imageItem]}), [file]);
  assert.deepEqual(readClipboardFiles({files:[], items:[ignoredText, imageItem]}), [file]);
  assert.deepEqual(readClipboardFiles({items:[ignoredText]}), []);
});

test('a TUS chunk is read and checked before it can be sent', async () => {
  const source = await uploadFileReader.openFile(new Blob(['0123456789']));
  assert.equal(source.size, 10);
  const resumed = await source.slice(3, 7);
  assert.equal(await resumed.value.text(), '3456');
  assert.equal(resumed.done, false);
  assert.equal((await source.slice(7, 10)).done, true);
  const truncated = await uploadFileReader.openFile({size:10, slice:() => new Blob(['1'])});
  await assert.rejects(truncated.slice(0, 10), /ERR_FILEHUB_LOCAL_READ/);
});
