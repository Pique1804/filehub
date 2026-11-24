import test from 'node:test';
import assert from 'node:assert/strict';
import { formats, languageStorageKey, readLanguage, resolveLocale, saveLanguage, translator } from '../src/i18n.js';
import { timelineRows } from '../src/media-utils.js';

test('manual language overrides ordered browser preferences; unsupported languages fall back to English', () => {
  assert.equal(resolveLocale('system', ['fr-CA','ru-RU','en-US']), 'ru');
  assert.equal(resolveLocale('system', ['en-GB','ru-RU']), 'en');
  assert.equal(resolveLocale('system', ['fr-CA']), 'en');
  assert.equal(resolveLocale('ru', ['en-US']), 'ru');
  assert.equal(resolveLocale('en', ['ru-RU']), 'en');
});

test('language preference persists without writing other browser state', t => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const data = new Map([['fh_theme','dark']]);
  Object.defineProperty(globalThis, 'localStorage', { configurable:true, value:{
    getItem: key => data.get(key) ?? null,
    setItem: (key, value) => data.set(key,value),
  } });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, 'localStorage', descriptor);
    else delete globalThis.localStorage;
  });
  assert.equal(readLanguage(), 'system');
  saveLanguage('en');
  assert.equal(readLanguage(), 'en');
  assert.deepEqual([...data], [['fh_theme','dark'],[languageStorageKey,'en']]);
  data.set(languageStorageKey,'unsupported');
  assert.equal(readLanguage(), 'system');
});

test('localized counts and binary sizes handle Russian plurals and each language’s punctuation', () => {
  const ru = formats('ru'), en = formats('en');
  assert.deepEqual([1, 2, 5, 11, 21].map(n => ru.plural('fileCount',n)),
    ['1 файл','2 файла','5 файлов','11 файлов','21 файл']);
  assert.deepEqual([1, 2].map(n => en.plural('fileCount',n)), ['1 file','2 files']);
  assert.equal(ru.size(1536), '1,5\u00a0КиБ');
  assert.equal(en.size(1536), '1.5\u00a0KiB');
  assert.equal(translator('en')('openNamed', {name:'снимок {name}.jpg'}), 'Open снимок {name}.jpg');
});

test('timeline language changes labels while preserving virtual row identities', () => {
  const photos = ['a.jpg','b.jpg'].map((path, index) => ({
    source:'disk', path, modTime:new Date(2026, 5, 1 + index, 12).getTime()/1000,
  }));
  const ru = timelineRows(photos, 2, 'ru'), en = timelineRows(photos, 2, 'en');
  assert.deepEqual(ru.map(row => row.key), en.map(row => row.key));
  assert.notEqual(ru[0].date, en[0].date);
});
