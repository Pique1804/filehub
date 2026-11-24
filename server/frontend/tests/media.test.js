import test from 'node:test';
import assert from 'node:assert/strict';
import {filterPlaces,placesGeoJSON,photoGridLayout,photoKey,thumbUrl,timelineRows,uploadFingerprint} from '../src/media-utils.js';
const p={source:'disk',path:'a.jpg',name:'a.jpg',lat:60,lon:30,modTime:new Date('2026-06-01T12:00:00').getTime()/1000};
test('map filters include the entire local end date and reject invalid GPS',()=>{
 assert.deepEqual(filterPlaces([p,{...p,lat:NaN},{...p,lon:181},{...p,source:'other'}],'2026-06-01','2026-06-01','disk'),[p]);
 assert.deepEqual(filterPlaces([p],'2026-06-02'),[]);
});
test('feature identities survive reorder and separator-containing names',()=>{
 const other={...p,source:'di|sk',path:'a/b.jpg'};
 assert.equal(placesGeoJSON([p,other]).features[1].properties.key,placesGeoJSON([other,p]).features[0].properties.key);
 assert.notEqual(photoKey({source:'a|b',path:'c'}),photoKey({source:'a',path:'b|c'}));
});
test('timeline packs rows without losing photos across date boundaries',()=>{
 const list=Array.from({length:7},(_,i)=>({...p,path:i+'.jpg',modTime:p.modTime-(i>=5?86400:0)}));
 const rows=timelineRows(list,3);assert.equal(rows.filter(r=>r.type==='date').length,2);
 assert.deepEqual(rows.filter(r=>r.photos).flatMap(r=>r.photos),list);
 assert.deepEqual(rows.filter(r=>r.photos).map(r=>r.photos.length),[3,2,2]);
});
test('desktop grid goes from compact cells at one third of the original width to large cells', () => {
  for (const width of [620, 1050, 1750, 2200]) {
    const layouts = ['compact', 'small', 'medium', 'large'].map(size => photoGridLayout(width, size, true));
    const compact = layouts[0];
    const large = layouts.at(-1);
    assert.equal(compact.columns, large.columns * 3);
    assert.ok(Math.abs(compact.cell - large.cell / 3) < 2);
    for (let i = 1; i < layouts.length; i++) assert.ok(layouts[i].cell > layouts[i - 1].cell);
  }
});
test('photo grid preference does not change the number or size of tiles on narrow screens', () => {
  assert.deepEqual(photoGridLayout(358, 'compact', false), {columns:2, gap:10, cell:174});
  assert.deepEqual(photoGridLayout(358, 'large', false), photoGridLayout(358, 'compact', false));
});
test('resuming cannot cross account or destination',()=>{
 const f={name:'a.jpg',size:123,lastModified:456};const base=uploadFingerprint(f,'alice','disk','Photos');
 for(const args of [['bob','disk','Photos'],['alice','other','Photos'],['alice','disk','Files']])assert.notEqual(base,uploadFingerprint(f,...args));
});

test('gallery thumbnails use the same width as the server prewarm', () => {
  const url = new URL(thumbUrl({...p, revision:'current'}), 'http://localhost');
  assert.equal(url.searchParams.get('w'), '192');
  assert.equal(url.searchParams.get('v'), 'current');
  assert.equal(new URL(thumbUrl(p, 2048), 'http://localhost').searchParams.get('w'), '2048');
});
