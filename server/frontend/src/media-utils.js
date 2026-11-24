export const photoKey = p => JSON.stringify([p.source, p.path]);
// Match gridThumbW on the server so the gallery can use prewarmed thumbnails.
export const thumbUrl = (p, width = 192) => '/api/thumb?' + new URLSearchParams({ source:p.source, path:p.path, w:width, v:p.revision || p.modTime || '' });
export const originalUrl = p => '/api/download?' + new URLSearchParams({ source:p.source, path:p.path, inline:'1' });

export function dateBounds(from, to) {
  const start = from ? new Date(from + 'T00:00:00').getTime() / 1000 : -Infinity;
  const endDate = to ? new Date(to + 'T00:00:00') : null;
  if (endDate) endDate.setDate(endDate.getDate() + 1);
  return [start, endDate ? endDate.getTime() / 1000 : Infinity];
}

export function filterPlaces(places, from = '', to = '', source = '') {
  const [start, end] = dateBounds(from, to);
  return places.filter(p => Number.isFinite(p.lat) && Number.isFinite(p.lon) && Math.abs(p.lat) <= 90 && Math.abs(p.lon) <= 180 &&
    (!source || p.source === source) && p.modTime >= start && p.modTime < end);
}

export function placesGeoJSON(places) {
  return { type:'FeatureCollection', features:places.map((p, index) => ({ type:'Feature', id:index,
    properties:{ key:photoKey(p) }, geometry:{ type:'Point', coordinates:[p.lon, Math.max(-85.051129, Math.min(85.051129, p.lat))] } })) };
}

export const photoGridScales = { compact:3, small:2, medium:1.5, large:1 };

export function photoGridLayout(width, size, desktop) {
  const originalColumns = Math.max(2, Math.floor((width + 14) / (width > 1100 ? 240 : 194)));
  const scale = desktop ? photoGridScales[size] : 1;
  const columns = Math.round(originalColumns * scale);
  const gap = desktop ? Math.round(14 / scale) : 10;
  const cell = Math.max(0, (width - (columns - 1) * gap) / columns);
  return {columns, gap, cell};
}

export function timelineRows(photos, columns, locale = 'ru', groupByDate = true) {
  const dayFormatter = new Intl.DateTimeFormat(locale, {year:'numeric', month:'long', day:'numeric'});
  const rows = []; let day = '', row;
  for (const photo of photos) {
    const timestamp = new Date(photo.modTime * 1000);
    const dayKey = [timestamp.getFullYear(), timestamp.getMonth(), timestamp.getDate()].join('-');
    if (groupByDate && dayKey !== day) {
      day = dayKey;
      rows.push({type:'date', key:'date:' + dayKey, date:dayFormatter.format(timestamp)});
      row = null;
    }
    if (!row || row.photos.length >= columns) { row = {type:'photos', key:photoKey(photo), photos:[]}; rows.push(row); }
    row.photos.push(photo);
  }
  return rows;
}

// Resume fingerprints must include the chosen destination and account, not just
// the local filename. Switching folders/accounts must never resume into another.
export function uploadFingerprint(file, account, source, path) {
  return JSON.stringify(['filehub-v2', account, source, path, file.name, file.size, file.lastModified]);
}
