<script>
  import { onMount, untrack } from 'svelte';
  import 'maplibre-gl/dist/maplibre-gl.css';
  import mapWorkerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?url';
  import Icon from './Icon.svelte';
  import PhotoTimeline from './PhotoTimeline.svelte';
  import { filterPlaces, placesGeoJSON, photoKey } from './media-utils.js';
  import { formats } from './i18n.js';
  let { onopen, onback, locale, t } = $props();
  const format = $derived(formats(locale));
  let container = $state(), map = $state.raw(), places = $state([]), selected = $state([]);
  let loading = $state(true), error = $state(''), tileError = $state(''), from = $state(''), to = $state(''), source = $state('');
  let retry = $state(0), ready = $state(false);
  let filtersOpen = $state(false);
  const activeFilters = $derived([from, to, source].filter(Boolean).length);
  const sources = $derived([...new Set(places.map(p => p.source))].sort());
  const filtered = $derived(filterPlaces(places, from, to, source));
  let dead = false, controller, resize;
  onMount(() => () => {dead = true; controller?.abort(); resize?.disconnect(); map?.remove();});
  $effect(() => {
    const attempt = retry, node = container;
    if (!node) return;
    untrack(() => void initialise(node, attempt));
  });
  async function json(url, signal) {
    const r = await fetch(url, {signal});
    if (!r.ok) throw new Error(r.status === 401 ? t('mapLoginRequired') : t('mapRequestFailed'));
    return r.json();
  }
  async function initialise(node, attempt) {
    controller?.abort(); controller = new AbortController(); const signal = controller.signal;
    ready = false; loading = true; error = ''; tileError = ''; selected = []; map?.remove(); map = undefined;
    try {
      const [lib, data, config] = await Promise.all([import('maplibre-gl'), json('/api/photos/places', signal), json('/api/maps/config', signal)]);
      if (dead || signal.aborted || attempt !== retry) return;
      places = data.places || [];
      lib.setWorkerUrl(mapWorkerUrl);
      const instance = new lib.Map({container:node, style:config.style, center:[20, 45], zoom:2,
        maxZoom:config.maxZoom || 19, attributionControl:true, renderWorldCopies:false});
      map = instance; instance.addControl(new lib.NavigationControl({showCompass:false}), 'top-right');
      resize?.disconnect(); resize = new ResizeObserver(() => instance.resize()); resize.observe(node);
      instance.on('error', event => {if (!dead && map === instance) tileError = t('mapTilesUnavailable');});
      instance.on('style.load', () => {
        if (dead || map !== instance) return;
        instance.addSource('photos', {type:'geojson', data:placesGeoJSON(filtered), cluster:true, clusterRadius:52, clusterMaxZoom:18,maxzoom:19});
        instance.addLayer({id:'clusters', type:'circle', source:'photos', filter:['has','point_count'], paint:{'circle-color':'#006DE2', 'circle-radius':['step',['get','point_count'],20,100,25,1000,30], 'circle-stroke-width':3,'circle-stroke-color':'#ffffff'}});
        instance.addLayer({id:'counts', type:'symbol', source:'photos', filter:['has','point_count'], layout:{'text-field':['get','point_count_abbreviated'],'text-font':['Open Sans Regular'],'text-size':13}, paint:{'text-color':'#ffffff'}});
        instance.addLayer({id:'photos', type:'circle', source:'photos', filter:['!', ['has','point_count']], paint:{'circle-color':'#006DE2','circle-radius':9,'circle-stroke-width':3,'circle-stroke-color':'#ffffff'}});
        instance.on('click', 'clusters', async e => {
          const feature = e.features?.[0]; if (!feature) return;
          const dataSource = instance.getSource('photos'); const id = feature.properties.cluster_id;
          const snapshot = filtered; const byKey = new Map(snapshot.map(p => [photoKey(p),p]));
          try {
            const leaves = await dataSource.getClusterLeaves(id, Math.min(feature.properties.point_count, 50000), 0);
            if (dead || map !== instance || snapshot !== filtered) return;
            selected = leaves.map(f => byKey.get(f.properties.key)).filter(Boolean).sort((a,b) => b.modTime - a.modTime);
            const zoom = await dataSource.getClusterExpansionZoom(id);
            if (map === instance && snapshot === filtered) instance.easeTo({center:feature.geometry.coordinates, zoom:Math.min(zoom,19), duration:300});
          } catch { /* A filter change invalidates old cluster IDs. */ }
        });
        instance.on('click', 'photos', e => {const key = e.features?.[0]?.properties.key; const p = filtered.find(p => photoKey(p) === key); if (p) selected = [p];});
        for (const layer of ['clusters','photos']) {
          instance.on('mouseenter',layer,() => instance.getCanvas().style.cursor = 'pointer');
          instance.on('mouseleave',layer,() => instance.getCanvas().style.cursor = '');
        }
        ready = true; loading = false; fit(filtered);
      });
      // The photo list is still useful when the tile/style host is unreachable.
      instance.once('idle', () => {if (!dead) loading = false;});
    } catch (e) {if (!signal.aborted && !dead) {error = e.message || t('mapOpenFailed'); loading = false;}}
  }
  function fit(items) {
    if (!map || !items.length) return;
    if (items.length === 1) {map.easeTo({center:[items[0].lon,items[0].lat],zoom:14,duration:0});return;}
    const lng = items.map(p => p.lon), lat = items.map(p => Math.max(-85,Math.min(85,p.lat)));
    map.fitBounds([[Math.min(...lng),Math.min(...lat)],[Math.max(...lng),Math.max(...lat)]],{padding:48,maxZoom:15,duration:0});
  }
  $effect(() => {const items = filtered, loaded = ready; if (loaded) untrack(() => {selected=[];map?.getSource('photos')?.setData(placesGeoJSON(items));fit(items);});});
  function visiblePhotos() {
    const bounds = map?.getBounds(); if (!bounds) return;
    selected = filtered.filter(p => bounds.contains([p.lon,Math.max(-85,Math.min(85,p.lat))])).sort((a,b) => b.modTime-a.modTime);
  }
</script>

<section class="places">
  <header>
    <button class="secondary back" type="button" onclick={onback} aria-label={t('back')}><Icon name="back" size={20}/></button>
    <div class="place-heading">
      <h1>{t('places')}</h1><p>{t('mapGeotaggedCount', {n:format.number(filtered.length)})}</p>
    </div>
    <button class="secondary" type="button" aria-expanded={filtersOpen}
      aria-label={activeFilters ? t('mapFiltersActive',{n:format.number(activeFilters)}) : t('mapFilters')}
      onclick={() => filtersOpen=!filtersOpen}>
      {t('mapFilters')}{#if activeFilters}<span class="active-filters">{format.number(activeFilters)}</span>{/if}
    </button>
    {#if filtersOpen}
      <div class="filters">
        <label>{t('dateFrom')}<input type="date" bind:value={from}/></label>
        <label>{t('dateTo')}<input type="date" bind:value={to} min={from}/></label>
        <label class="wide">{t('storage')}<select bind:value={source}><option value="">{t('allStorages')}</option>{#each sources as name (name)}<option value={name}>{name}</option>{/each}</select></label>
        {#if activeFilters}<button class="secondary wide" type="button" onclick={() => {from='';to='';source='';}}>{t('reset')}</button>{/if}
      </div>
    {/if}
  </header>
  {#if error}<div class="notice" role="alert">{error}<button onclick={() => retry++}>{t('retry')}</button></div>{/if}
  {#if tileError}<div class="notice" role="status">{tileError}<button onclick={() => retry++}>{t('retry')}</button></div>{/if}
  {#if !loading && !error && !filtered.length}<p class="empty">{places.length ? t('noMapDateMatches') : t('mapEmpty')}</p>{/if}
  <div class="workspace" class:with-selection={selected.length > 0}>
    <div class="map" bind:this={container} aria-label={t('photoMap')}></div>
    <button class="secondary map-area" type="button" onclick={visiblePhotos} disabled={!ready}>{t('mapAreaPhotos')}</button>
    {#if loading}<div class="loading" role="status">{t('mapLoading')}</div>{/if}
    {#if selected.length}<aside><div class="selection-head"><strong>{format.plural('photoCount', selected.length)}</strong><button aria-label={t('closePhotoList')} onclick={() => selected=[]}>✕</button></div><PhotoTimeline photos={selected} {locale} {t} onopen={p => onopen(p,selected)}/></aside>{/if}
  </div>
</section>

<style>
  .places{display:flex;flex-direction:column;flex:1;min-height:0;padding:24px;gap:10px}
  header{position:relative;z-index:5;display:flex;align-items:center;gap:10px}
  .place-heading{flex:1;min-width:0}
  h1{font-size:26px;letter-spacing:-.5px;margin:0;font-weight:650}
  p{margin:3px 0;color:var(--muted);font-size:12px}
  .filters{position:absolute;top:calc(100% + 8px);right:0;width:min(320px,100%);max-height:65dvh;overflow:auto;display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;padding:12px;border:1px solid var(--border);border-radius:12px;background:var(--panel);box-shadow:0 12px 30px #10142620}
  .filters label{display:grid;gap:4px;min-width:0;font-size:12px;color:var(--muted)}
  .filters .wide{grid-column:1/-1}
  input,select{width:100%;min-width:0;min-height:42px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--text);padding:8px 10px}
  .secondary,.notice button{min-height:42px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--text);padding:8px 12px;cursor:pointer}
  .secondary.back{display:grid;place-items:center;flex:none;width:44px;padding:0}
  .active-filters{margin-left:5px;padding:1px 5px;border-radius:6px;background:var(--accent-soft);color:var(--accent-deep)}
  .workspace{position:relative;flex:1;min-height:180px;display:grid;grid-template-columns:minmax(0,1fr);overflow:hidden;border:1px solid var(--border);border-radius:14px;background:var(--row)}
  .workspace.with-selection{grid-template-columns:minmax(0,1fr) 340px}
  .map{height:100%;min-height:0}
  .map-area,.loading{position:absolute;z-index:2;left:10px;box-shadow:var(--shadow)}
  .map-area{top:10px;max-width:calc(100% - 64px)}
  .loading{top:62px;border-radius:8px;padding:9px 14px;background:var(--panel);color:var(--text)}
  aside{display:flex;flex-direction:column;min-height:0;background:var(--panel);padding:0 12px 12px;border-left:1px solid var(--border)}
  .selection-head{display:flex;justify-content:space-between;align-items:center;padding:10px 0;font-size:13px}
  .selection-head button{border:0;background:transparent;color:var(--text);cursor:pointer;padding:8px}
  .notice{padding:8px 10px;border:1px solid var(--border);border-radius:9px;color:var(--text2);font-size:12px;display:flex;gap:10px;align-items:center}
  @media(max-width:900px){
    .places{padding:12px;gap:8px}
    .workspace.with-selection{grid-template-columns:minmax(0,1fr);grid-template-rows:minmax(0,1fr) minmax(100px,40%)}
    aside{border-left:0;border-top:1px solid var(--border)}
    h1{font-size:23px}
  }
</style>
