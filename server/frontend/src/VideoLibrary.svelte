<script>
  import { onMount, tick } from 'svelte';
  import BackupFolderPicker from './BackupFolderPicker.svelte';
  import CompatibleVideo from './CompatibleVideo.svelte';
  import Icon from './Icon.svelte';

  let { t, sources } = $props();
  let catalog = $state(null);
  let query = $state('');
  let kind = $state('all');
  let busy = $state(false);
  let error = $state('');
  let configuring = $state(false);
  let folderKind = $state('movies');
  let selected = $state(null);
  let selectedSeason = $state(null);
  let showEpisodes = $state(false);
  let watchSeason = $state(0);
  let playing = $state(null);
  let dialog = $state(null);
  let matchDialog = $state(null);
  let matchTitle = $state(null);
  let matchQuery = $state('');
  let matchYear = $state();
  let matches = $state([]);
  let searchingMatches = $state(false);
  let savingMatch = $state(false);
  let matchError = $state('');
  let matchRequest;
  let refreshTimer;
  let request;
  let progress = Promise.resolve();
  const metadataRefreshDelays = [1500, 3000, 6000];

  const visible = $derived((catalog?.titles ?? []).filter(item =>
    (kind === 'all' || item.kind === kind) &&
    [item.name, item.description || '', ...(item.genres || []), ...item.videos.map(video => video.name)].some(name => name.toLowerCase().includes(query.trim().toLowerCase()))));
  const seasons = $derived([...new Set(selected?.videos.map(video => video.season) ?? [])]);
  const activeSeason = $derived(seasons.includes(selectedSeason) ? selectedSeason : seasons[0]);
  const playingIndex = $derived(playing ? playing.item.videos.findIndex(video => video.path === playing.video.path && video.source === playing.video.source) : -1);
  const nextEpisode = $derived(playing?.item.kind === 'series' && playingIndex >= 0 ? playing.item.videos[playingIndex + 1] : null);
  const continueWatching = $derived((catalog?.titles ?? []).flatMap(item =>
    item.videos.filter(video => video.position > 0).map(video => ({item, video})))
    .sort((a,b) => b.video.watched - a.video.watched).slice(0, 8));

  async function load(attempt = 0) {
    clearTimeout(refreshTimer);
    request?.abort();
    const controller = new AbortController();
    request = controller;
    busy = true;
    error = '';
    try {
      const response = await fetch('/api/video/library', {signal:controller.signal});
      if (!response.ok) throw new Error();
      const result = await response.json();
      if (controller.signal.aborted) return;
      catalog = result;
      if (selected) selected = catalog.titles.find(item => item.id === selected.id) ?? null;
      if (catalog.tmdbPending && attempt < metadataRefreshDelays.length) {
        refreshTimer = setTimeout(() => {
          if (!playing && !configuring && !matchTitle) void load(attempt + 1);
        }, metadataRefreshDelays[attempt]);
      }
    } catch (failure) {
      if (failure.name !== 'AbortError') error = 'videoLibraryLoadFailed';
    } finally {
      if (request === controller) busy = false;
    }
  }

  async function changeFolder(source, path, choice) {
    busy = true;
    error = '';
    try {
      const response = await fetch('/api/admin/video/folder', {method:'POST', body:new URLSearchParams({source, path, kind:choice})});
      if (!response.ok) throw new Error();
      await load();
    } catch {
      error = 'videoFolderSaveFailed';
      busy = false;
    }
  }

  function openTitle(item) {
    selected = item;
    selectedSeason = null;
  }

  async function openMatchPicker(item) {
    matchTitle = item;
    matchQuery = item.tmdbQuery || item.name;
    matchYear = item.tmdbQueryYear || item.year || undefined;
    matches = [];
    matchError = '';
    await tick();
    matchDialog?.showModal();
    await searchMatches();
  }

  async function searchMatches() {
    const q = matchQuery.trim();
    if (!q || !matchTitle) return;
    matchRequest?.abort();
    const controller = new AbortController();
    matchRequest = controller;
    searchingMatches = true;
    matchError = '';
    matches = [];
    try {
      const params = new URLSearchParams({title:matchTitle.id, q, year:matchYear ?? ''});
      const response = await fetch('/api/admin/video/tmdb/search?' + params, {signal:controller.signal});
      if (!response.ok) throw new Error();
      const result = await response.json();
      if (!controller.signal.aborted) matches = result;
    } catch (failure) {
      if (failure.name !== 'AbortError') matchError = 'videoTMDBSearchError';
    } finally {
      if (matchRequest === controller) searchingMatches = false;
    }
  }

  function closeMatchPicker() {
    matchRequest?.abort();
    matchTitle = null;
  }

  async function saveMatch(match) {
    if (!matchTitle || savingMatch) return;
    savingMatch = true;
    matchError = '';
    try {
      const response = await fetch('/api/admin/video/tmdb/match', {
        method:'POST', body:new URLSearchParams({title:matchTitle.id, match:match.id}),
      });
      if (!response.ok) throw new Error();
      matchDialog?.close();
      await load();
    } catch {
      matchError = 'videoTMDBSaveError';
    } finally {
      savingMatch = false;
    }
  }

  async function openVideo(item, video) {
    showEpisodes = false;
    watchSeason = video.season;
    playing = {item, video, remember: (position) => rememberPosition(video, position)};
    await tick();
    if (!dialog?.open) dialog?.showModal();
  }

  function rememberPosition(video, position) {
    video.position = position;
    if (position > 0) video.watched = Math.floor(Date.now() / 1000);
    // Serialize writes so a pause update cannot arrive behind a newer position.
    progress = progress.then(() => fetch('/api/video/progress', {
      method:'POST', body:new URLSearchParams({source:video.source, path:video.path, position}),
    })).catch(() => undefined);
  }

  async function closeVideo() {
    playing = null;
    await tick();
    await progress;
    await load();
  }

  function posterSources(item, frame = true) {
    const first = item.videos[0];
    return [item.cover && thumbnail(first.source, item.cover, 360), item.poster,
      frame && thumbnail(first.source, first.path, 360, 'video-frame')].filter(Boolean);
  }

  function thumbnail(source, path, w = 192, kind = '') {
    return '/api/thumb?' + new URLSearchParams({source, path, w, ...(kind ? {kind} : {})});
  }

  function episodeSources(item, video) {
    return [thumbnail(video.source, video.path, 192, 'video-frame'), ...posterSources(item, false)];
  }

  function label(video) {
    return video.episode ? t('videoEpisode', {n:video.episode}) + ' · ' + video.name : video.name;
  }

  function nextArtwork(event, urls) {
    const img = event.currentTarget;
    const next = Number(img.dataset.artIndex ?? 0) + 1;
    if (next === urls.length) {
      img.hidden = true;
      return;
    }
    img.dataset.artIndex = String(next);
    img.src = urls[next];
  }

  onMount(() => {
    void load();
    return () => {
      request?.abort();
      matchRequest?.abort();
      clearTimeout(refreshTimer);
    };
  });
</script>

{#snippet artwork(urls, size)}
  <Icon name="play" {size}/>
  {#if urls.length}
    {#key urls.join('|')}
      <img src={urls[0]} alt="" loading="lazy" referrerpolicy="no-referrer" onerror={event => nextArtwork(event, urls)}>
    {/key}
  {/if}
{/snippet}

<section class="library">
  <header class="heading">
    <h1>{t('videoLibrary')}</h1>
    <div class="tools">
      {#if catalog?.canManage}
        <button type="button" class="ghost" aria-expanded={configuring} onclick={() => configuring = !configuring}>{t('videoLibraryFolders')}</button>
      {/if}
      <button type="button" class="ghost" onclick={() => load()} disabled={busy}>{t('videoLibraryRefresh')}</button>
    </div>
  </header>

  {#if configuring && catalog?.canManage}
    <section class="panel" aria-label={t('videoLibraryFolders')}>
      <ul class="folders">
        {#each catalog.folders as folder (JSON.stringify([folder.source,folder.path]))}
          <li>
            <span><b>{t(folder.kind === 'movies' ? 'videoMovies' : 'videoSeries')}</b> {folder.source}:/{folder.path}</span>
            <button type="button" class="ghost" disabled={busy} onclick={() => changeFolder(folder.source, folder.path, 'remove')}>{t('remove')}</button>
          </li>
        {/each}
      </ul>
      <label>{t('videoFolderType')}
        <select bind:value={folderKind}>
          <option value="movies">{t('videoMovies')}</option>
          <option value="series">{t('videoSeries')}</option>
        </select>
      </label>
      <BackupFolderPicker label={t('backupAddFolder')} {sources} {t} choose={({source, path}) => changeFolder(source, path, folderKind)}/>
      {#if !catalog.tmdbEnabled}
        <p class="detail-meta">{t('videoTMDBSetup')} <a href="https://developer.themoviedb.org/docs/faq" target="_blank" rel="noreferrer">TMDB</a></p>
      {/if}
    </section>
  {/if}

  {#if error}<p role="alert">{t(error)}</p>{/if}
  {#if busy}<p role="status">{t('loading')}</p>{/if}
  {#if catalog?.partial}<p role="status">{t('videoLibraryPartial')}</p>{/if}

  {#if selected}
    <button type="button" class="ghost back" onclick={() => selected = null}>{t('videoCatalogBack')}</button>
    {@const resume = [...selected.videos].filter(video => video.position > 0).sort((a,b) => b.watched - a.watched)[0]}
    <div class="detail">
      <span class="poster detail-poster">{@render artwork(posterSources(selected), 32)}</span>
      <div>
        <h2>{selected.name}</h2>
        {#if selected.year || selected.genres?.length}<p class="detail-meta">{[selected.year, ...(selected.genres || [])].filter(Boolean).join(' · ')}</p>{/if}
        {#if selected.description}<p class="summary">{selected.description}</p>{/if}
        {#if selected.metadataLink}<a class="detail-meta" href={selected.metadataLink} target="_blank" rel="noreferrer">{selected.metadataSource === 'tmdb' ? 'TMDB' : 'TVmaze · CC BY-SA'}</a>{/if}
        <div class="detail-actions">
          <button type="button" class="primary" onclick={() => openVideo(selected, resume || selected.videos[0])}>
            <Icon name="play" size={19}/>{t(resume ? 'videoContinue' : 'videoPlay')}
          </button>
          {#if catalog?.canManage && catalog.tmdbEnabled}
            <button type="button" class="ghost" class:needs-match={selected.tmdbNeedsMatch} onclick={() => openMatchPicker(selected)}>{t('videoTMDBIdentify')}</button>
          {/if}
        </div>
      </div>
    </div>
    {#if selected.kind === 'series'}
      <div class="season-bar">
        <h3>{activeSeason > 0 ? t('videoSeason', {n:activeSeason}) : t('videoEpisodes')}</h3>
        {#if seasons.length > 1}
          <select aria-label={t('videoSeasonPicker')} value={activeSeason} onchange={event => selectedSeason = Number(event.currentTarget.value)}>
            {#each seasons as season}<option value={season}>{season > 0 ? t('videoSeason', {n:season}) : t('videoEpisodes')}</option>{/each}
          </select>
        {/if}
      </div>
      <ul class="episodes">
        {#each selected.videos.filter(video => video.season === activeSeason) as video (JSON.stringify([video.source,video.path]))}
          <li><button type="button" onclick={() => openVideo(selected, video)}>
            <span class="episode-picture">{@render artwork(episodeSources(selected, video), 20)}</span>
            <span><b>{label(video)}</b>{#if video.position > 0}<small>{t('videoContinue')}</small>{/if}</span>
          </button></li>
        {/each}
      </ul>
    {/if}
  {:else if catalog}
    {#if continueWatching.length}
      <h2>{t('videoContinue')}</h2>
      <ul class="continue">
        {#each continueWatching as {item, video} (JSON.stringify([video.source,video.path]))}
          <li><button type="button" onclick={() => openVideo(item, video)}>
            <span class="episode-picture">{@render artwork(episodeSources(item, video), 20)}</span><span><b>{item.name}</b>{#if item.kind === 'series'}<small>{video.name}</small>{/if}</span>
          </button></li>
        {/each}
      </ul>
    {/if}
    {#if catalog.folders.length}
      <div class="filter">
        <label>{t('videoFolderType')}
          <select bind:value={kind}>
            <option value="all">{t('videoAll')}</option>
            <option value="movies">{t('videoMovies')}</option>
            <option value="series">{t('videoSeries')}</option>
          </select>
        </label>
        <input type="search" aria-label={t('videoSearch')} placeholder={t('videoSearch')} bind:value={query}>
      </div>
      {#if visible.length}
        <div class="grid">
          {#each visible as item (item.id)}
            <button type="button" class="title" onclick={() => openTitle(item)}>
              <span class="poster">{@render artwork(posterSources(item), 32)}</span>
              <strong>{item.name}</strong>
              <small>{item.kind === 'series' ? t('videoEpisodeCount', {n:item.videos.length}) : (item.year || t('videoMovie'))}</small>
            </button>
          {/each}
        </div>
      {:else if !busy}<p>{t('videoLibraryEmpty')}</p>{/if}
    {:else if !busy}
      <p>{t('videoLibraryNoFolders')}</p>
    {/if}
  {/if}
  {#if catalog?.tmdbEnabled}
    <footer class="metadata-credits" aria-label={t('videoMetadataCredits')}>
      <a href="https://www.themoviedb.org/" target="_blank" rel="noreferrer"><img src="/tmdb-logo.svg" alt="TMDB" width="56" referrerpolicy="no-referrer"></a>
      <span lang="en">This product uses the TMDB API but is not endorsed or certified by TMDB.</span>
    </footer>
  {/if}
</section>

{#if matchTitle}
  <dialog class="match-dialog" bind:this={matchDialog} onclose={closeMatchPicker}
    oncancel={event => { if (savingMatch) event.preventDefault(); }} aria-labelledby="tmdb-match-title">
    <header class="match-heading">
      <div><h2 id="tmdb-match-title">{t('videoTMDBIdentify')}</h2><p>{matchTitle.name}</p></div>
      <button type="button" class="ghost" disabled={savingMatch} aria-label={t('close')} onclick={() => matchDialog?.close()}><Icon name="close" size={20}/></button>
    </header>
    <form class="match-form" onsubmit={event => {event.preventDefault(); void searchMatches();}}>
      <label>{t('name')}<input type="search" bind:value={matchQuery} required maxlength="160" disabled={savingMatch}></label>
      <label>{t('videoTMDBYear')}<input type="number" bind:value={matchYear} min="1000" max="9999" disabled={savingMatch}></label>
      <button class="ghost" type="submit" disabled={searchingMatches || savingMatch}>{t('find')}</button>
    </form>
    {#if matchError}<p role="alert">{t(matchError)}</p>{/if}
    {#if searchingMatches}<p role="status">{t('loading')}</p>
    {:else if matches.length}
      <ul class="matches">
        {#each matches as match (match.id)}
          <li><button type="button" class="match-option" disabled={savingMatch} onclick={() => saveMatch(match)}>
            <span class="poster">{@render artwork(match.poster ? [match.poster] : [], 18)}</span>
            <span><b>{match.name || match.original}</b>
              {#if match.year || (match.original && match.original !== match.name)}
                <small>{[match.year, match.original !== match.name && match.original].filter(Boolean).join(' · ')}</small>
              {/if}
            </span>
            {#if match.id === matchTitle.tmdbMatchId}<Icon name="check" size={19}/>{/if}
          </button></li>
        {/each}
      </ul>
    {:else if !matchError}<p>{t('videoTMDBNoResults')}</p>{/if}
  </dialog>
{/if}

{#if playing}
  <dialog class="player-dialog" bind:this={dialog} onclose={closeVideo} aria-label={t('videoPlayer')}
    oncancel={event => { if (showEpisodes) { event.preventDefault(); showEpisodes = false; } }}>
    {#snippet episodePanel()}
      {@const seasonOptions = [...new Set(playing.item.videos.map(video => video.season))]}
      <aside class="playlist" aria-label={t('videoEpisodes')}>
        <header>
          <strong>{t('videoEpisodes')}</strong>
          <button class="panel-close" type="button" aria-label={t('close')} onclick={() => showEpisodes = false}><Icon name="close"/></button>
        </header>
        {#if seasonOptions.length > 1}
          <select aria-label={t('videoSeasonPicker')} bind:value={watchSeason}>
            {#each seasonOptions as season}
              <option value={season}>{season > 0 ? t('videoSeason', {n:season}) : t('videoEpisodes')}</option>
            {/each}
          </select>
        {/if}
        <div class="playlist-items">
          {#each playing.item.videos.filter(video => video.season === watchSeason) as video (JSON.stringify([video.source,video.path]))}
            <button type="button" aria-current={video === playing.video ? 'true' : undefined} onclick={() => {
              if (video !== playing.video) openVideo(playing.item, video);
              else showEpisodes = false;
            }}>
              <span class="episode-picture"><Icon name="play" size={16}/><img src={thumbnail(video.source, video.path)}
                alt="" loading="lazy" onerror={event => event.currentTarget.hidden = true}></span>
              <span>{label(video)}</span>
            </button>
          {/each}
        </div>
      </aside>
    {/snippet}
    <div class="watch-shell">
    {#key JSON.stringify([playing.video.source, playing.video.path])}
      <CompatibleVideo source={playing.video.source} path={playing.video.path} position={playing.video.position}
        title={playing.item.name} subtitle={playing.item.kind === 'series' ? label(playing.video) : ''}
        {t} onposition={playing.remember} onnext={nextEpisode ? () => openVideo(playing.item, nextEpisode) : null}
        onclose={() => dialog.close()} episodesOpen={showEpisodes} {episodePanel}
        onepisodes={playing.item.kind === 'series' ? () => { showEpisodes = !showEpisodes; watchSeason = playing.video.season; } : null}/>
    {/key}
    </div>
  </dialog>
{/if}

<style>
  .library{overflow:auto;min-height:0;padding:26px clamp(16px,4vw,38px) 32px;color:var(--text)}
  .heading, .tools, .filter, header, li{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
  .heading, header{justify-content:space-between}
  h1{font-size:clamp(25px,4vw,34px);letter-spacing:-.03em;margin:0 0 8px}
  h2{font-size:20px;margin:22px 0 12px}
  h3{font-size:16px;margin:16px 0 8px}
  .ghost, .filter input, select{min-height:44px;border:1px solid var(--border);border-radius:10px;padding:7px 12px;background:var(--panel);color:var(--text);font:inherit}
  button{cursor:pointer}
  button:disabled{opacity:.6;cursor:default}
  .panel{padding:14px;margin:14px 0;border:1px solid var(--border);border-radius:14px;background:var(--panel)}
  .panel>label{display:block;margin:8px 0}
  .folders,.episodes,.continue{list-style:none;padding:0;margin:0 0 16px}
  .folders li{justify-content:space-between;overflow-wrap:anywhere;margin:4px 0}
  .filter{margin:16px 0}
  .filter input{flex:1;min-width:min(160px,100%)}
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(140px,45%),1fr));gap:16px}
  .title,.episodes button,.continue button{text-align:start;border:1px solid var(--border);border-radius:12px;background:var(--panel);color:var(--text);font:inherit;padding:10px}
  .title{min-width:0;display:grid;gap:6px;align-content:start;overflow-wrap:anywhere;padding:0 0 10px;overflow:hidden}.title strong,.title small{padding-inline:10px}.title:hover,.episodes button:hover,.continue button:hover{border-color:var(--accent)}
  .poster,.episode-picture{position:relative;aspect-ratio:2/3;display:grid;place-items:center;overflow:hidden;color:#a9c4ed;background:linear-gradient(145deg,#253959,#141822)}
  .poster img,.episode-picture img{position:absolute;inset:0;width:100%;height:100%;object-fit:cover}
  .poster img[hidden],.episode-picture img[hidden]{display:none}
  .episode-picture{aspect-ratio:16/9;width:clamp(64px,14vw,124px);flex:none;border-radius:7px}
  .detail{display:grid;grid-template-columns:clamp(86px,18vw,180px) minmax(0,1fr);align-items:center;gap:clamp(14px,4vw,34px);margin:18px 0;padding:16px clamp(12px,3vw,24px);border-radius:16px;background:var(--panel2)}
  .detail h2{font-size:clamp(21px,4vw,36px);margin:0 0 5px}
  .detail-poster{border-radius:10px}
  .detail-meta,.summary{font-size:14px;margin:8px 0;line-height:1.5}
  .detail-meta{color:var(--muted)}
  .primary{display:inline-flex;align-items:center;gap:6px;min-height:42px;margin-top:8px;padding:7px 12px;border:0;border-radius:9px;background:#a4ceff;color:#122237;font:inherit;font-weight:650}
  .season-bar{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-block:16px}
  small{display:block;color:var(--muted)}
  .episodes button,.continue button{display:flex;align-items:center;gap:9px;min-height:44px;width:100%;overflow-wrap:anywhere}
  .episodes li{margin-bottom:6px}
  .continue{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(210px,100%),1fr));gap:8px}
  .player-dialog{box-sizing:border-box;position:fixed;inset:0;width:100vw;height:100dvh;max-width:none;max-height:none;margin:0;padding:0;border:0;background:#000;color:#f7f8ff;overflow:hidden}
  .player-dialog::backdrop{background:#000}
  .detail-actions{display:flex;flex-wrap:wrap;gap:8px}
  .needs-match{color:var(--accent-deep)}
  .metadata-credits{display:flex;align-items:center;gap:10px;margin-top:24px;color:var(--muted);font-size:11px}
  .metadata-credits img{display:block;height:auto}
  .match-dialog{width:min(540px,calc(100% - 28px));max-height:calc(100dvh - 32px);padding:16px;border:1px solid var(--border);border-radius:14px;background:var(--panel);color:var(--text)}
  .match-dialog::backdrop{background:#101426aa}
  .match-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:8px;margin-bottom:12px}
  .match-heading h2{margin:0;font-size:19px}
  .match-heading p{margin:4px 0 0;color:var(--muted);overflow-wrap:anywhere}
  .match-form{display:flex;align-items:flex-end;flex-wrap:wrap;gap:8px}
  .match-form label{display:grid;gap:4px;color:var(--text2);font-size:13px}
  .match-form label:first-child{flex:1;min-width:130px}
  .match-form label:nth-child(2){width:85px}
  .match-form input{min-width:0;width:100%;min-height:44px;border:1px solid var(--border);border-radius:9px;padding:7px 9px;background:var(--panel2);color:var(--text);font:inherit}
  .matches{display:grid;gap:6px;margin:12px 0 0;padding:0;list-style:none}
  .match-option{display:flex;align-items:center;gap:11px;width:100%;min-height:66px;padding:6px 9px;border:1px solid var(--border);border-radius:9px;background:var(--panel);text-align:left;color:var(--text);cursor:pointer}
  .match-option:hover{background:var(--row)}
  .match-option .poster{width:36px;flex:none;border-radius:4px}
  .match-option span:nth-child(2){flex:1;min-width:0;overflow-wrap:anywhere}
  .match-option small{display:block;color:var(--muted);margin-top:3px}
  .watch-shell{width:100%;height:100%;background:#000}
  .playlist{position:absolute;right:max(10px,env(safe-area-inset-right));top:max(10px,env(safe-area-inset-top));bottom:max(10px,env(safe-area-inset-bottom));width:min(360px,calc(100% - 20px));display:flex;flex-direction:column;gap:8px;min-height:0;border:1px solid #32415b;border-radius:16px;background:#131b2a;padding:10px;color:#f7faff}
  .playlist header{justify-content:space-between;padding-inline:4px}
  .playlist select{min-height:40px;background:#222d40;border-color:#41516e;color:#f7faff;font-size:13px}
  .playlist-items{overflow:auto;min-height:0;overscroll-behavior:contain}
  .playlist-items button,.panel-close{border:0;border-radius:8px;color:inherit;background:transparent;font:inherit}
  .playlist-items button{display:flex;gap:10px;align-items:center;text-align:start;width:100%;padding:7px;min-height:48px;margin-bottom:3px;font-size:13px}
  .playlist-items .episode-picture{width:88px}
  .playlist-items button:hover,.playlist-items button[aria-current]{background:#273b58}
  .panel-close{display:grid;place-items:center;min-width:44px;min-height:44px}
  @media(orientation:portrait){.playlist{width:auto;left:8px;right:8px;top:auto;height:min(70dvh,500px)}}
</style>
