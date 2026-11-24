<script>
  import { onDestroy, tick } from 'svelte';
  import TextDocument from './TextDocument.svelte';
  import Icon from './Icon.svelte';
  import SettingsPage from './SettingsPage.svelte';
  import ProfilesDialog from './ProfilesDialog.svelte';
  import PhotoTimeline from './PhotoTimeline.svelte';
  import PlacesView from './PlacesView.svelte';
  import ServerBackups from './ServerBackups.svelte';
  import UploadShareDialog from './UploadShareDialog.svelte';
  import PartialUploads from './PartialUploads.svelte';
  import StorageInsights from './StorageInsights.svelte';
  import TrashPage from './TrashPage.svelte';
  import SearchFilters from './SearchFilters.svelte';
  import FileEntries from './FileEntries.svelte';
  import VideoLibrary from './VideoLibrary.svelte';
  import CompatibleVideo from './CompatibleVideo.svelte';
  import OfficePreview from './OfficePreview.svelte';
  import { openGallery } from './gallery.js';
  import { createUploadManager } from './upload-manager.js';
  import { operationSkipReason, readClipboardFiles, readDroppedTree } from './file-operations.js';
  import { photoGridScales, photoKey } from './media-utils.js';
  import { formats, languageStorageKey, readLanguage, resolveLocale, saveLanguage, translator } from './i18n.js';

  import { preferences } from './preferences.js';

  const enc = encodeURIComponent;
  const davUrl = location.protocol + '//' + location.host + '/dav/';
  const thumbExts = new Set(['jpg','jpeg','png','gif','webp','bmp']);

  // settings (persisted)
  let language = $state(readLanguage());
  let browserLanguages = $state([...navigator.languages]);
  const locale = $derived(resolveLocale(language, browserLanguages));
  const t = $derived(translator(locale));
  const format = $derived(formats(locale));
  $effect(() => {
    document.documentElement.lang = locale;
  });
  function changeLanguage(event) {
    language = event.currentTarget.value;
    saveLanguage(language);
  }
  let view = $state(preferences.getItem('fh_view') || 'grid');
  const savedPhotoGridSize = preferences.getItem('fh_photo_grid');
  let photoGridSize = $state(Object.hasOwn(photoGridScales, savedPhotoGridSize) ? savedPhotoGridSize : 'compact');
  let viewportWidth = $state(window.innerWidth);
  const desktopPhotoGrid = $derived(viewportWidth > 760);
  let selectionMode = $state(preferences.getItem('fh_sel') === '1');
  const settingsSections = {interface:'settingsInterface', files:'files', account:'settingsAccount', server:'settingsServer'};
  const settingsSectionFromHash = () => {
    const match = /^#\/settings\/([^/]+)$/.exec(location.hash);
    return match && Object.hasOwn(settingsSections, match[1]) ? match[1] : null;
  };
  let settingsSection = $state(settingsSectionFromHash());
  const settingsOpen = $derived(settingsSection !== null);
  let serverBackupsOpen = $state(false);
  let storageInsights = $state(null);
  function showStorageUsage(source, path = '') {
    storageInsights = {source, path};
    if (!diskList.length) void loadDisks();
  }
  let partialUploadsOpen = $state(false);
  $effect(() => { preferences.setItem('fh_view', view); });
  $effect(() => { preferences.setItem('fh_photo_grid', photoGridSize); });
  $effect(() => { preferences.setItem('fh_sel', selectionMode ? '1' : '0'); });
  let theme = $state(preferences.getItem('fh_theme') || 'system');
  $effect(() => {
    const preference = theme, media = matchMedia('(prefers-color-scheme: dark)');
    const apply = () => { document.documentElement.dataset.theme = preference === 'system' ? (media.matches ? 'dark' : 'light') : preference; };
    apply(); preferences.setItem('fh_theme', preference); media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  });
  const savedSortKey = preferences.getItem('fh_sortk');
  let folderSort = $state({
    key: ['size', 'mtime'].includes(savedSortKey) ? savedSortKey : 'name',
    order: preferences.getItem('fh_sorto') === 'desc' ? 'desc' : 'asc',
  });
  let recentSort = $state({key:'mtime', order:'desc'});
  let searchSort = $state({key:'', order:'asc'});
  $effect(() => { preferences.setItem('fh_sortk', folderSort.key); preferences.setItem('fh_sorto', folderSort.order); });

  function nextSort(current, key) {
    const order = current.key === key ? (current.order === 'asc' ? 'desc' : 'asc') : (key === 'mtime' ? 'desc' : 'asc');
    return {key, order};
  }

  function sortEntries(items, {key, order}, foldersFirst = false) {
    const rows = items.map(item => ({item, name:item.name.toLowerCase()}));
    rows.sort((a, b) => {
      if (foldersFirst && a.item.isDir !== b.item.isDir) return a.item.isDir ? -1 : 1;
      const byName = (a.name > b.name) - (a.name < b.name);
      const primary = key === 'size' ? a.item.size - b.item.size
        : key === 'mtime' ? a.item.modTime - b.item.modTime : byName;
      return primary * (order === 'desc' ? -1 : 1) || byName;
    });
    return rows.map(row => row.item);
  }
  let clickMode = $state(preferences.getItem('fh_click') || (matchMedia('(pointer: coarse)').matches ? 'single' : 'double')); // 'double' | 'single'
  $effect(() => { preferences.setItem('fh_click', clickMode); });

  let route = $state(parseHash());
  let trashOpen = $state(location.hash === '#/trash');
  let folderTrail = $state();
  const folderCrumbs = $derived(crumbParts(route.path));
  $effect(() => {
    // Keep the current folder in view when opening a folder or following history.
    if (folderCrumbs.length && folderTrail) folderTrail.scrollLeft = folderTrail.scrollWidth;
  });
  let sources = $state([]);
  let entries = $state([]);
  let loadedFolder = $state(null);
  let listingRequest = 0, recentRequest = 0;
  let loading = $state(false);
  let error = $state('');
  let selected = $state({});
  let uploads = $state([]);
  let folderUploads = $state([]);
  const uploadQueue = $derived([...uploads, ...folderUploads].filter(item => !item.done));
  let uploadSession = 0;
  let dragOver = $state(false);
  let diskList = $state([]);
  let recents = $state([]);
  let serverPing = $state(null); // ms, -1 = offline, null = checking
  let previewOpen = $state(false), previewItem = $state(null), previewText = $state(null), previewLoading = $state(false);
  let videosOpen = $state(false);
  let sharesOpen = $state(false), sharesList = $state([]);
  let uploadShareTarget = $state(null);
  let davOpen = $state(false);
  let textDoc = $state(null);
  let linkedTextOpened = false;
  $effect(() => {
    if (!currentUser || linkedTextOpened) return;
    linkedTextOpened = true;
    const q = new URLSearchParams(location.search);
    const source = q.get('textSource'), path = q.get('textPath');
    if (source && path) textDoc = {source, path, name:path.split('/').pop()};
  });

  let query = $state('');
  let results = $state([]);
  let semanticPhotos = $state([]);
  let visualState = $state(null), photoVisualResult = $state(false);
  const emptySearchFilters = {source:'', type:'', minMiB:undefined, maxMiB:undefined, from:'', through:''};
  const searchTypes = [
    ['file','files'], ['folder','folders'], ['image','searchImages'], ['video','searchVideos'],
    ['audio','searchAudio'], ['document','searchDocuments'], ['archive','searchArchives'],
  ];
  let searchFilters = $state({...emptySearchFilters});
  let searchFiltersActive = $derived(Object.values(searchFilters).some(value => value !== undefined && value !== ''));
  let searchLoading = $state(false), searchError = $state('');
  let searchLoadingMore = $state(false), searchMoreError = $state(false), nextSearchOffset = $state(0);
  let fileFiltersOpen = $state(false);
  let searchSession;
  let searchIntent = $state(null), literalFileSearch = $state(false);
  let manySearchTags = $derived(Boolean(searchIntent?.type && searchIntent?.year && searchIntent?.extension));
  let searchStatus = $state(null), searchRefresh = $state(0);
  let fileQueryActive = $derived([...query.trim()].length >= 3 || /^\.[a-z0-9]$/i.test(query.trim()));
  let searchMode = $derived(fileQueryActive || searchFiltersActive);
  let searchVisible = $derived(searchMode && !photosOpen && !placesOpen && !videosOpen && !settingsOpen && !trashOpen && !!currentUser);

  let needLogin = $state(false);
  let currentUser = $state(''), currentRole = $state('');
  const androidDownloadUrl = '/api/app/apk';
  let androidDownloadAvailable = $state(false);
  $effect(() => {
    androidDownloadAvailable = false;
    if (!currentUser || needLogin) return;
    const controller = new AbortController();
    fetch(androidDownloadUrl, {method: 'HEAD', cache: 'no-store', signal: controller.signal})
      .then(response => {
        if (!controller.signal.aborted) {
          androidDownloadAvailable = response.ok &&
            response.headers.get('Content-Type') === 'application/vnd.android.package-archive';
        }
      })
      .catch(() => {});
    return () => controller.abort();
  });
  let limitedProfile = $state(false);
  let personalFolder = $state(null);
  let personalQuota = $state(null);
  let quotaRequest;
  async function loadPersonalQuota() {
    quotaRequest?.abort();
    if (!personalFolder || !currentUser) { personalQuota = null; return; }
    const controller = new AbortController();
    quotaRequest = controller;
    try {
      const response = await fetch('/api/profile/quota', {signal: controller.signal, cache: 'no-store'});
      if (response.ok) {
        const {quota} = await response.json();
        if (!controller.signal.aborted) personalQuota = quota;
      }
    } catch {}
  }
  function canWritePath(source, path) {
    return currentRole !== 'viewer' && (!limitedProfile ||
      (personalFolder && source === personalFolder.source &&
        (path === personalFolder.path || path.startsWith(personalFolder.path + '/'))));
  }
  let canWrite = $derived(canWritePath(route.source, route.path));
  let canShare = $derived(!limitedProfile);
  const canUseTrash = $derived(!!currentUser && currentRole !== 'viewer' && (!limitedProfile || !!personalFolder));
  const trashSources = $derived(limitedProfile ? diskList.filter(disk => disk.name === personalFolder?.source) : diskList);
  function setProfileSession(session) {
    currentRole = session.role;
    limitedProfile = session.restricted === true;
    personalFolder = session.personal || null;
    personalQuota = null;
    void loadPersonalQuota();
  }
  let isAdmin = $derived(currentRole === 'admin');
  let showLoginPassword = $state(false), loginBusy = $state(false);
  let loginErr = $state('');
  let usersOpen = $state(false);

  const accountSettings = $derived([
    { label: 'changePassword', run: changePassword },
    ...(canShare ? [{ label: 'myLinks', run: openShares }] : []),
    { label: 'signOut', run: logout, danger: true },
  ]);
  const serverSettings = $derived([
    { label: 'settingsConnections', actions: [
      { label: 'webdav', run: () => { davOpen = true; } },
      ...(isAdmin ? [{ label: 'profilesAndAccess', run: () => { usersOpen = true; } }] : []),
    ] },
    { label: 'settingsStorage', actions: isAdmin ? [
      { label: 'storageUsage', run: () => showStorageUsage(route.source, route.path) },
      { label: 'backup', run: () => { serverBackupsOpen = true; void loadDisks(); } },
      { label: 'downloadDatabaseBackup', icon: 'download', run: () => { dl('/api/admin/backup'); showToast(t('preparingDatabaseBackup')); } },
    ] : [] },
    { label: 'settingsMaintenance', actions: [
      ...(isAdmin ? [{ label: 'partialUploads', run: () => { partialUploadsOpen = true; } }] : []),
      ...(canWrite && !limitedProfile ? [{ label: 'startReindex', run: reindexNow }] : []),
    ] },
  ].filter(group => group.actions.length));
  function selectSettingsSection(section) {
    if (section === settingsSection) return;
    const previous = history.state?.filehubSettings;
    const state = settingsOpen
      ? {origin:previous?.origin ?? '', depth:previous?.depth ? previous.depth+1 : 0}
      : {origin:location.hash, depth:1};
    history.pushState({filehubSettings:state}, '', '#/settings/'+section);
    settingsSection = section;
  }
  function openSettings() {
    if (!settingsOpen) selectSettingsSection('interface');
  }
  function closeSettings() {
    const depth = history.state?.filehubSettings?.depth;
    if (depth > 0) history.go(-depth);
    else leaveSettings();
  }
  function leaveSettings() {
    if (!settingsOpen) return;
    history.pushState(null, '', history.state?.filehubSettings?.origin || '#');
    settingsSection = null;
  }

  function openTrash() {
    leaveSettings();
    if (trashOpen) return;
    history.pushState({filehubTrash:{origin:location.hash}}, '', '#/trash');
    trashOpen = true;
    if (!diskList.length) void loadDisks();
  }

  function leaveTrash() {
    if (!trashOpen) return;
    history.pushState(null, '', history.state?.filehubTrash?.origin || '#');
    trashOpen = false;
  }
  let moveOpen = $state(false), moveItems = $state([]), moveTarget = $state(''), moveMode = $state('move');
  let moveTargetSource = $state(''), moveBusy = $state(false), moveStarted = $state(false), moveFromPhotos = $state(false);
  let moveFolderState = $state({}), moveResultsHeading = $state();
  let moveTreeController = null, moveSession = 0;
  let trashItems = $state([]), trashRunning = $state(false);
  let trashRevision = $state(0);
  let trashSession = 0;
  const trashFailures = $derived(trashItems.filter(item => item.status === 'failed'));
  const hiddenByTrash = $derived(trashPathMatcher(trashItems.filter(item => item.status !== 'failed')));
  let infoOpen = $state(false), infoItem = $state(null), infoData = $state(null), infoHashing = $state(false);
  let infoRequest = 0;
  let infoOwners = $state(null), infoOwner = $state(''), infoOwnerError = $state(''), infoOwnerBusy = $state(false);
  let qrToken = $state('');
  let photosOpen = $state(false), photos = $state([]), photosLoading = $state(false);
  let photoSelectionMode = $state(false), selectedPhotoKeys = $state(new Set());
  const visiblePhotos = $derived(photos.filter(photo => !hiddenByTrash(photo)));
  const selectedPhotos = $derived(selectedPhotoKeys.size ? visiblePhotos.filter(photo => selectedPhotoKeys.has(photoKey(photo))) : []);
  const canModifySelectedPhotos = $derived(selectedPhotos.length > 0 && selectedPhotos.every(p => canWritePath(p.source, p.path)));
  let placesOpen = $state(false), photoTotal = $state(0), photosMore = $state(false), photosLoadingMore = $state(false), photoPageError = $state('');
  let gallery = null, galleryController, galleryRequest = 0, uploadManager = null;
  let photoCursor = '';
  let albums = $state([]), activeAlbum = $state(null), albumPicker = $state(false), albumName = $state(''), albumError = $state(''), albumBusy = $state(false);
  let albumEntries = $state([]);
  let backupHealth = $state(null), photoQuery = $state('');
  let photoMemories = $state([]), photoPanel = $state(''), photoSearchActive = $state(false);
  const visibleMemories = $derived(photoMemories.filter(photo => !hiddenByTrash(photo)));
  let submittedPhotoQuery = '', photoSearchIntent = $state(null);
  let photoSearchField, mapTrigger;
  let photoRequest = 0;
  let photoPageController;
  function nextPhotoRequest() {
    const controller = photoPageController;
    photoPageController = null;
    controller?.abort();
    return ++photoRequest;
  }
  const backupVerified = $derived(backupHealth?.state === 'ok' && backupHealth?.stale === false);
  const backupDate = $derived(backupHealth?.lastSuccessfulCompleted || backupHealth?.completed || 0);
  const backupLabel = $derived(t(backupVerified ? 'verifiedOriginalBackup' : backupHealth?.state === 'running' ? 'checkingOriginalBackup' : backupHealth?.stale ? 'staleOriginalBackup' : 'unverifiedOriginalBackup'));
  const fileDragType = 'application/x-filehub-drag';
  let draggedItems = $state(null);
  let fileClipboard = null, filePasteTimer;
  let moveFixedTarget = $state(false);
  let toast = $state('');
  let toastTimer;
  function showToast(m){ clearTimeout(toastTimer); toast = m; toastTimer = setTimeout(()=>toast='', 2400); }

  let moveChildren = $state({}), moveExpanded = $state({});
  const moveSources = $derived(limitedProfile && personalFolder ? [personalFolder.source] :
    [...new Set([moveItems[0]?.source, ...diskList.map(disk => disk.name)].filter(Boolean))]);
  const moveTargetReady = $derived((moveFixedTarget || moveFolderState['/'] === 'ready') && canWritePath(moveTargetSource, moveTarget));
  const moveSummary = $derived(moveItems.reduce((counts, item) => {
    counts[item.status]++;
    return counts;
  }, {queued:0, running:0, done:0, skipped:0, failed:0}));
  const fileOperationLabels = {move:'moveItemsTitle', copy:'copyItemsTitle'};
  const operationStatusLabels = {queued:'operationQueued', running:'operationRunning', done:'done', skipped:'operationSkipped', failed:'operationFailed'};

  let folderEntries = $derived(loadedFolder?.source === route.source && loadedFolder?.path === route.path
    ? sortEntries(entries, folderSort, true).map(entry => ({...entry, source:route.source, path:fullPath(entry.name)}))
      .filter(entry => !hiddenByTrash(entry)) : []);
  let recentEntries = $derived(sortEntries(recents.filter(entry => !hiddenByTrash(entry)).slice(0, 8), recentSort));
  const visibleResults = $derived(results.filter(entry => !hiddenByTrash(entry)));
  const visibleSemanticPhotos = $derived(semanticPhotos.filter(entry => !hiddenByTrash(entry)));
  let selCount = $derived(folderEntries.filter(isSelected).length);

  function parseHash(){
    const hash = settingsSectionFromHash() ? history.state?.filehubSettings?.origin ?? '' : location.hash;
    const h = hash.slice(1);
    if(!h || h === '/trash') return { source:null, path:'' };
    const bar = h.indexOf('|');
    if(bar < 0) return { source:decodeURIComponent(h), path:'' };
    return { source:decodeURIComponent(h.slice(0,bar)), path:decodeURIComponent(h.slice(bar+1)) };
  }
  function go(source, path){ nextPhotoRequest(); photosLoading=false; photosOpen=false; placesOpen=false; videosOpen=false; location.hash = source ? enc(source)+'|'+enc(path||'') : ''; }
  const fmtSize = n => format.size(n);
  const fileCount = n => format.plural('fileCount', n);
  const fmtDate = seconds => format.date(seconds);
  function fullPath(name){ return route.path ? route.path+'/'+name : name; }
  function keyOf(e){ return (e.isDir?'d:':'f:')+e.name; }
  function crumbParts(path){ const out=[]; let acc=''; for(const p of (path||'').split('/').filter(Boolean)){ acc=acc?acc+'/'+p:p; out.push({name:p,path:acc}); } return out; }
  function ext(name){ return name.split('.').pop().toLowerCase(); }
  const vidThumbExts = new Set(['mp4','webm','mkv','mov','avi','m4v','ogv','wmv','flv','mpg','mpeg']);
  // ext → coloured badge (matches the prototype's hue map)
  const extHues = { JPG:255,JPEG:255,PNG:205,GIF:205,WEBP:205,HEIC:205,SVG:205, MP4:310,MOV:310,MKV:310,AVI:310,WEBM:310,M4V:310, ZIP:45,RAR:45,'7Z':45,GZ:45,TAR:45,XZ:45, PDF:25, TXT:175,MD:175,LOG:175,JSON:175,YML:175,YAML:175, DOC:255,DOCX:255,RTF:255, XLS:150,XLSX:150,CSV:150,APK:150, MP3:90,FLAC:90,WAV:90,M4A:90,OGG:90,AAC:90 };
  function extInfo(name){ const x = ext(name).toUpperCase(); const h = extHues[x] ?? 255; return { badge:x.slice(0,4), tint:`oklch(0.94 0.04 ${h})`, fg:`oklch(0.45 0.13 ${h})` }; }

  async function load(preserveSelection = false){
    const folder = {source:route.source, path:route.path};
    const request = ++listingRequest;
    error=''; loading=true;
    if (!preserveSelection) selected={};
    try{
      const url = folder.source ? '/api/list?' + new URLSearchParams(folder) : '/api/sources';
      const r = await fetch(url);
      if (request !== listingRequest) return;
      if(r.status === 401){ needLogin = true; loading = false; return; }
      if(!r.ok) throw new Error(await r.text());
      const data = await r.json();
      if (request !== listingRequest) return;
      if(folder.source){ entries = data.entries; loadedFolder = folder; sources = []; }
      else { sources = data.sources; entries = []; loadedFolder = null; void loadRecents(); }
    }catch(e){
      if (request !== listingRequest) return;
      error = e.message; entries = []; loadedFolder = null;
    }
    loading = false;
    loadDisks();
    void loadPersonalQuota();
  }
  $effect(() => {
    const onHash = () => {
      const nextSection = settingsSectionFromHash();
      const wasSettings = settingsOpen;
      const wasTrash = trashOpen;
      settingsSection = nextSection;
      if (nextSection) return;
      trashOpen = location.hash === '#/trash';
      if (trashOpen) return;
      const next = parseHash();
      // Returning from settings keeps the underlying section and its data.
      if ((wasSettings || wasTrash) && route.source === next.source && route.path === next.path) return;
      photosOpen=false; placesOpen=false; videosOpen=false; nextPhotoRequest();
      route=next;
    };
    addEventListener('hashchange', onHash);
    return () => removeEventListener('hashchange', onHash);
  });
  $effect(() => { route.source; route.path; load(); });

  function midnight(date, nextDay = false) {
    const time = new Date(date + 'T00:00:00');
    if (nextDay) time.setDate(time.getDate() + 1);
    return time.getTime() / 1000;
  }

  // Abort requests on changes so older queries cannot overwrite newer results.
  $effect(() => {
    searchRefresh;
    searchSession = null;
    results = []; searchLoading = false; searchError = ''; searchIntent = null;
    searchLoadingMore = false; searchMoreError = false; nextSearchOffset = 0;
    if (!searchVisible) return;

    const params = new URLSearchParams({
      q:fileQueryActive ? query.trim() : '',
      mode:literalFileSearch ? 'literal' : 'auto',
      zone:Intl.DateTimeFormat().resolvedOptions().timeZone ?? 'UTC',
    });
    if (searchSort.key) {
      params.set('sort', searchSort.key);
      params.set('order', searchSort.order);
    }
    const {source, type, minMiB, maxMiB, from, through} = searchFilters;
    const min = minMiB === undefined ? null : Math.ceil(minMiB * (1024 ** 2));
    const max = maxMiB === undefined ? null : Math.floor(maxMiB * (1024 ** 2));
    const firstDay = from ? midnight(from) : null;
    const afterLastDay = through ? midnight(through, true) : null;
    if ([min, max].some(value => value !== null && (!Number.isSafeInteger(value) || value < 0))
      || (min !== null && max !== null && min > max)) {
      searchError = 'searchInvalidSize';
      return;
    }
    if ([firstDay, afterLastDay].some(value => value !== null && !Number.isSafeInteger(value))
      || (firstDay !== null && afterLastDay !== null && firstDay >= afterLastDay)) {
      searchError = 'searchInvalidDate';
      return;
    }
    if (source) params.set('source', source);
    if (type) params.set('type', type);
    if (min !== null) params.set('minSize', min);
    if (max !== null) params.set('maxSize', max);
    if (firstDay !== null) params.set('modifiedFrom', firstDay);
    if (afterLastDay !== null) params.set('modifiedBefore', afterLastDay);

    const controller = new AbortController();
    const session = {params, controller};
    searchSession = session;
    searchLoading = true;
    const timer = setTimeout(() => fetchSearchPage(session, 0), 170);
    return () => {clearTimeout(timer); controller.abort();};
  });

  async function fetchSearchPage(session, offset) {
    const signal = session.controller.signal;
    const params = new URLSearchParams(session.params);
    params.set('offset', offset);
    try {
      const response = await fetch('/api/search?' + params, {signal});
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (signal.aborted) return;
      if (offset === 0) {
        results = data.results;
        searchIntent = data.intent ?? null;
      } else {
        const seen = new Set(results.map(searchResultKey));
        results = [...results, ...data.results.filter(result => !seen.has(searchResultKey(result)))];
      }
      nextSearchOffset = data.nextOffset ?? 0;
    } catch {
      if (!signal.aborted) {
        if (offset === 0) searchError = 'searchLoadError';
        else searchMoreError = true;
      }
    } finally {
      if (!signal.aborted) {
        searchLoading = false;
        searchLoadingMore = false;
      }
    }
  }
  function searchResultKey(result) { return JSON.stringify([result.source, result.path]); }
  function loadMoreSearchResults() {
    if (!searchSession || !nextSearchOffset || searchLoadingMore) return;
    searchLoadingMore = true;
    searchMoreError = false;
    void fetchSearchPage(searchSession, nextSearchOffset);
  }

  $effect(() => {
    if (!currentUser || needLogin || (!photosOpen && !searchVisible)) {
      visualState = null;
      return;
    }
    const controller = new AbortController();
    async function refresh() {
      try {
        const response = await fetch('/api/photos/visual-search', {signal:controller.signal});
        const next = response.ok ? await response.json() : null;
        if (controller.signal.aborted) return;
        const disabled = visualState?.enabled !== false && next?.enabled === false;
        if (visualState?.available !== next?.available || visualState?.enabled !== next?.enabled) visualState = next;
        if (disabled && photoVisualResult && photosOpen && photoSearchActive) {
          void searchPhotos();
        }
      } catch { /* Photo and OCR browsing remain available. */ }
    }
    void refresh();
    const timer = setInterval(refresh, 15000);
    return () => {controller.abort();clearInterval(timer);};
  });

  // Semantic results are shown separately on the unfiltered search.
  $effect(() => {
    semanticPhotos = [];
    if (!visualState?.available || !searchVisible || searchFiltersActive || literalFileSearch || searchLoading || searchIntent || searchError) return;
    const q = query.trim(), controller = new AbortController();
    if (q.startsWith('"') && q.endsWith('"')) return;
    void (async () => {
      try {
        const response = await fetch('/api/search/semantic?q=' + enc(q) + '&k=24', {signal:controller.signal});
        const data = response.ok ? await response.json() : {};
        if (!controller.signal.aborted) semanticPhotos = data.photos || [];
      } catch {
        // The semantic sidecar is optional.
      }
    })();
    return () => controller.abort();
  });

  $effect(() => {
    if (!searchVisible) {
      searchStatus = null;
      return;
    }
    const controller = new AbortController();
    let timer;
    async function pollIndex() {
      try {
        const response = await fetch('/api/search', {signal:controller.signal, cache:'no-store'});
        if (!response.ok) throw new Error();
        const {indexed, indexing} = await response.json();
        if (controller.signal.aborted) return;
        const previous = searchStatus;
        searchStatus = {indexed, indexing};
        if (!indexing && previous && (previous.indexing || previous.indexed !== indexed)) searchRefresh++;
      } catch {
        if (!controller.signal.aborted) searchStatus = null;
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(pollIndex, 3000);
      }
    }
    void pollIndex();
    return () => {clearTimeout(timer); controller.abort();};
  });
  function resetSearchFilters(){ searchFilters = {...emptySearchFilters}; }
  function clearFileSearch(){ query = ''; literalFileSearch=false; fileFiltersOpen=false; resetSearchFilters(); }
  function parentDir(p){ const i = p.lastIndexOf('/'); return i<0?'':p.slice(0,i); }
  function openResult(res) {
    if (res.isDir) {
      go(res.source,res.path);
      clearFileSearch();
      return;
    }
    void openPreview({...res, _src:res.source, _path:res.path});
  }
  function dlResult(res){ res.isDir ? dl('/api/zip?source='+enc(res.source)+'&path='+enc(res.path)) : dl('/api/download?source='+enc(res.source)+'&path='+enc(res.path)); }
  function openResultFolder(res) {
    clearFileSearch();
    go(res.source, parentDir(res.path));
  }
  function entryAction(icon, label, run, danger = false) { return {icon, label, run, danger}; }
  function resultActions(entry) {
    const actions = [];
    if (entry.isDir || previewType(entry) !== 'none') {
      actions.push(entryAction(entry.isDir ? 'folder' : 'preview', entry.isDir ? 'open' : 'searchPreview', () => openResult(entry)));
    }
    return [...actions,
      entryAction('download', entry.isDir ? 'downloadFolderZip' : 'download', () => dlResult(entry)),
      entryAction('open_folder', 'searchOpenFolder', () => openResultFolder(entry)),
      entryAction('info', 'properties', () => openInfo(entry)),
    ];
  }
  function folderActions(entry) {
    const actions = [
      entryAction(entry.isDir ? 'folder' : 'preview', entry.isDir || previewType(entry) === 'none' ? 'open' : 'searchPreview', () => openItem(entry)),
      entryAction('download', entry.isDir ? 'downloadFolderZip' : 'download', () => dlResult(entry)),
      entryAction('info', 'properties', () => openInfo(entry)),
    ];
    if (!canWrite) return actions;
    if (canShare) actions.push(entryAction('link', 'shareLink', () => shareItem(entry)));
    if (entry.isDir) {
      actions.push(entryAction('upload', 'uploadShareCreate', () => {
        uploadShareTarget = {source:entry.source, path:entry.path};
      }));
    }
    return [...actions,
      entryAction('rename', 'rename', () => renameItem(entry)),
      entryAction('move', 'moveTo', () => openMove([entry])),
      entryAction('copy', 'copyTo', () => openMove([entry], 'copy')),
      entryAction('delete', 'moveToTrash', () => deleteItems([entry]), true),
    ];
  }
  function resultThumbnailKind(res) {
    if (res.isDir) return '';
    const extension = ext(res.name);
    if (vidThumbExts.has(extension)) return 'video';
    if (thumbExts.has(extension)) return 'image';
    if (offE.has(extension) || extension === 'pdf' || extension === 'csv') return 'document';
    return '';
  }
  async function reindexNow(){ try{ await apiPost('/api/reindex'); if(searchStatus) searchStatus={...searchStatus,indexing:true}; showToast(t('reindexStarted')); }catch(e){ error=e.message; } }

  // open / select
  function openItem(e){ if(e.isDir) go(route.source, fullPath(e.name)); else openPreview(e); }
  function onItemClick(e, ev){
    if(selectionMode){ ev.preventDefault(); toggle(e); return; }
    if(ev.detail===0 || matchMedia('(pointer: coarse)').matches){ openItem(e); return; }
    if(clickMode==='single') openItem(e);
  }
  function onItemDbl(e){
    if(selectionMode || clickMode!=='double') return;
    openItem(e);
  }
  function toggle(e){ const k=keyOf(e); selected={...selected,[k]:!selected[k]}; }
  function isSelected(e){ return Boolean(selected[keyOf(e)]); }
  function selectedEntries(){ return folderEntries.filter(isSelected); }

  // downloads
  function dl(url){ const a=document.createElement('a'); a.href=url; a.download=''; document.body.appendChild(a); a.click(); a.remove(); }
  function dlItem(e){ if(e._path){ dl('/api/download?source='+enc(e._src)+'&path='+enc(e._path)); return; } e.isDir ? dl('/api/zip?source='+enc(route.source)+'&path='+enc(fullPath(e.name))) : dl('/api/download?source='+enc(route.source)+'&path='+enc(fullPath(e.name))); }
  function dlSelectedZip(){ const ps=selectedEntries().map(e=>fullPath(e.name)); if(!ps.length) return; dl('/api/zip?source='+enc(route.source)+ps.map(p=>'&p='+enc(p)).join('')); }

  // ops
  async function apiPost(url, form=null){
    const opts={method:'POST',headers:{}};
    if(form){ opts.headers['Content-Type']='application/x-www-form-urlencoded'; opts.body=new URLSearchParams(form).toString(); }
    const r=await fetch(url,opts); if(!r.ok) throw new Error((await r.text()).trim()); return r;
  }
  async function newFolder(){ const name=prompt(t('newFolderName')); if(!name) return; try{ await apiPost('/api/mkdir?source='+enc(route.source)+'&path='+enc(route.path)+'&name='+enc(name)); load(); }catch(e){ error=e.message; } }
  async function renameItem(e){ const name=prompt(t('newName'), e.name); if(!name||name===e.name) return; try{ await apiPost('/api/rename?source='+enc(route.source)+'&path='+enc(fullPath(e.name))+'&name='+enc(name)); load(); }catch(err){ error=err.message; } }
  function trashPathMatcher(items) {
    if (!items.length) return () => false;
    const paths = new Set(items.map(photoKey));
    const folders = new Set(items.filter(item => item.isDir).map(photoKey));
    return item => {
      if (paths.has(photoKey(item))) return true;
      if (!folders.size) return false;
      let parent = parentDir(item.path);
      while (parent) {
        if (folders.has(photoKey({source:item.source, path:parent}))) return true;
        parent = parentDir(parent);
      }
      return false;
    };
  }

  function deleteItems(items) {
    for (const entry of items) {
      const item = {name:entry.name, isDir:entry.isDir, source:entry._src || entry.source || route.source,
        path:entry._path || entry.path || fullPath(entry.name), status:'queued', error:''};
      if (!canWritePath(item.source, item.path)) continue;
      const existing = trashItems.find(queued => photoKey(queued) === photoKey(item));
      if (!existing) {
        trashItems.push(item);
      } else if (existing.status === 'failed') {
        existing.status = 'queued';
        existing.error = '';
      }
    }
    selectedPhotoKeys = new Set(selectedPhotos.map(photoKey));
    if (!selectedPhotoKeys.size) clearPhotoSelection();
    void processTrash();
  }

  function retryTrash() {
    for (const item of trashFailures) {
      item.status = 'queued';
      item.error = '';
    }
    void processTrash();
  }

  async function processTrash() {
    if (trashRunning) return;
    const session = trashSession;
    const completed = new Set();
    trashRunning = true;
    let item = trashItems.find(entry => entry.status === 'queued');
    while (item) {
      item.status = 'running';
      try {
        await apiPost('/api/delete?' + new URLSearchParams({source:item.source, path:item.path}));
        item.status = 'done';
        completed.add(item);
      } catch (err) {
        item.status = 'failed';
        item.error = err.message;
      }
      if (session !== trashSession) return;
      item = trashItems.find(entry => entry.status === 'queued');
    }
    trashRunning = false;
    if (!completed.size) return;
    trashRevision++;
    if (!trashFailures.length) showToast(t('movedToTrash'));
    const deleted = trashPathMatcher([...completed]);
    if (loadedFolder) {
      const {source, path} = loadedFolder;
      const remaining = [];
      const nextSelection = {...selected};
      for (const entry of entries) {
        if (deleted({source, path:path ? path + '/' + entry.name : entry.name})) {
          delete nextSelection[keyOf(entry)];
        } else {
          remaining.push(entry);
        }
      }
      entries = remaining;
      selected = nextSelection;
    }
    recentRequest++;
    recents = recents.filter(entry => !deleted(entry));
    results = results.filter(entry => !deleted(entry));
    semanticPhotos = semanticPhotos.filter(entry => !deleted(entry));
    photoMemories = photoMemories.filter(entry => !deleted(entry));
    photos = photos.filter(entry => !deleted(entry));
    if (searchVisible) {
      searchSession?.controller.abort();
      searchRefresh++;
    }
    await Promise.all([load(true), photosOpen && refreshPhotosAfterOperation([...completed], true)]);
    if (session === trashSession) trashItems = trashItems.filter(item => !completed.has(item));
  }

  function resetTrashQueue() {
    trashSession++;
    trashItems = [];
    trashRunning = false;
  }

  async function shareItem(it){
    const password = prompt(t('sharePassword'),''); if(password===null) return;
    const days = prompt(t('shareDays'),''); if(days===null) return;
    try{
      const r = await apiPost('/api/share/create',{source:it._src || route.source,path:it._path || fullPath(it.name),password,days});
      const url = location.origin + (await r.json()).url;
      try{ await navigator.clipboard.writeText(url); showToast(t('linkCopied')); }catch{ prompt(t('copyLinkPrompt'), url); }
    }catch(err){ error=err.message; }
  }

  async function loadChildren(path) {
    const key = '/' + path, signal = moveTreeController?.signal;
    if (!signal || ['ready', 'loading'].includes(moveFolderState[key])) return;
    moveFolderState[key] = 'loading';
    try {
      const response = await fetch('/api/list?' + new URLSearchParams({source:moveTargetSource, path}), {signal});
      if (!response.ok) throw new Error();
      const {entries:children} = await response.json();
      if (signal.aborted) return;
      moveChildren[key] = children.filter(entry => entry.isDir).map(entry => ({
        name:entry.name, path:path ? path + '/' + entry.name : entry.name,
      }));
      moveFolderState[key] = 'ready';
    } catch {
      if (!signal.aborted) moveFolderState[key] = 'failed';
    }
  }

  function toggleNode(path) {
    const key = '/' + path;
    moveExpanded[key] = !moveExpanded[key];
    if (moveExpanded[key]) void loadChildren(path);
  }

  function changeMoveSource(source) {
    moveTreeController?.abort();
    moveTreeController = new AbortController();
    moveTargetSource = source;
    moveTarget = limitedProfile && source === personalFolder?.source ? personalFolder.path : '';
    moveChildren = {};
    moveExpanded = {};
    moveFolderState = {};
    void loadChildren('');
  }

  function openMove(items, mode='move', destination = null) {
    if (!items.length || moveOpen || moveBusy) return;
    moveSession++;
    moveMode = mode;
    moveFromPhotos = photosOpen;
    moveItems = items.map(item => ({name:item.name, isDir:item.isDir,
      source:item._src || item.source || route.source, path:item._path || item.path || fullPath(item.name),
      status:'queued', reason:'', error:''}));
    moveStarted = false;
    moveOpen = true;
    moveFixedTarget = Boolean(destination);
    if (destination) {
      moveTargetSource = destination.source;
      moveTarget = destination.path;
      void confirmMove();
    } else changeMoveSource(moveItems[0].source);
  }

  function closeMove(force = false) {
    if (moveBusy && !force) return;
    moveSession++;
    moveTreeController?.abort();
    moveOpen = false;
    moveBusy = false;
    moveItems = [];
  }

  async function confirmMove(retry = false) {
    if (moveBusy || !moveTargetReady) return;
    const pending = moveItems.filter(item => item.status === (retry ? 'failed' : 'queued'));
    if (!pending.length) return;
    const session = moveSession;
    const photoView = photoRequest;
    const mode = moveMode, destination = {source:moveTargetSource, path:moveTarget};
    moveBusy = true;
    moveStarted = true;
    await tick();
    moveResultsHeading?.focus();
    for (const item of pending) {
      if (session !== moveSession) return;
      item.reason = operationSkipReason(item, destination);
      item.error = '';
      if (item.reason) {
        item.status = 'skipped';
        continue;
      }
      item.status = 'running';
      const query = new URLSearchParams({source:item.source, path:item.path});
      query.set('destSource', destination.source);
      query.set('dest', destination.path);
      try {
        await apiPost('/api/' + mode + '?' + query);
        item.status = 'done';
      } catch (err) {
        item.status = 'failed';
        item.error = err.message;
      }
    }
    if (session === moveSession) {
      moveBusy = false;
      const completed = pending.filter(item => item.status === 'done');
      if (photosOpen && moveFromPhotos) {
        if (completed.length && photoRequest === photoView) {
          void refreshPhotosAfterOperation(completed);
        }
      } else {
        if (searchVisible && completed.length) searchRefresh++;
        void load();
      }
    }
  }


  function filesActive() {
    return currentUser && !needLogin && !photosOpen && !placesOpen && !videosOpen && !settingsOpen && !trashOpen &&
      !previewOpen && !textDoc && !moveOpen;
  }

  function snapshotEntries(items) {
    return items.map(({source, path, name, isDir}) => ({source, path, name, isDir}));
  }

  function clipboardInField(event) {
    const target = event.target;
    return event.defaultPrevented || (target instanceof Element && (target.isContentEditable ||
      target.closest('input:not([type="checkbox"]), textarea, select, [role="dialog"]')));
  }

  function handleFileShortcut(event) {
    if (!(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey || event.repeat ||
      !filesActive() || clipboardInField(event)) return;
    const key = event.code || event.key.toUpperCase();
    if (key === 'KeyC' || key === 'C') {
      if (window.getSelection()?.isCollapsed === false) return;
      const listing = searchVisible ? visibleResults : route.source ? folderEntries : recentEntries;
      const chosen = !searchVisible && route.source && selectionMode ? listing.filter(isSelected) : [];
      const focusedKey = event.target instanceof Element
        ? event.target.closest('.file-name')?.dataset.fileKey : undefined;
      const focused = focusedKey ? listing.find(item => searchResultKey(item) === focusedKey) : undefined;
      const items = chosen.length ? chosen : focused ? [focused] : [];
      if (!items.length) return;
      event.preventDefault();
      fileClipboard = {user:currentUser, items:snapshotEntries(items)};
      showToast(t('fileClipboardCopied', {n:fileCount(items.length)}));
    } else if ((key === 'KeyV' || key === 'V') && fileClipboard?.user === currentUser) {
      // Let the browser deliver the system paste event before using the FileHub clipboard.
      clearTimeout(filePasteTimer);
      filePasteTimer = setTimeout(pasteFileHubSelection, 120);
    }
  }

  function clearFileClipboard() {
    clearTimeout(filePasteTimer);
    filePasteTimer = undefined;
    fileClipboard = null;
  }

  function pasteFileHubSelection() {
    filePasteTimer = undefined;
    if (!filesActive() || fileClipboard?.user !== currentUser) return;
    if (!route.source || searchVisible || !canWrite) {
      showToast(t('fileClipboardChooseFolder'));
      return;
    }
    openMove(fileClipboard.items, 'copy', {...route});
  }

  function pasteFiles(event) {
    const fileHubShortcut = filePasteTimer !== undefined;
    clearTimeout(filePasteTimer);
    filePasteTimer = undefined;
    if (!filesActive() || clipboardInField(event)) return;
    const files = readClipboardFiles(event.clipboardData);
    if (files.length) {
      event.preventDefault();
      clearFileClipboard();
      if (route.source && !searchVisible && canWrite) uploadFiles(files, {...route});
      else showToast(t('fileClipboardChooseFolder'));
    } else if (fileHubShortcut) {
      event.preventDefault();
      pasteFileHubSelection();
    }
  }

  function startFileDrag(event, entry, directory) {
    const items = directory && selectionMode && isSelected(entry) ? folderEntries.filter(isSelected) : [entry];
    if (!event.dataTransfer || !items.every(item => canWritePath(item.source, item.path))) {
      event.preventDefault();
      return;
    }
    event.dataTransfer.setData(fileDragType, '1');
    event.dataTransfer.effectAllowed = 'move';
    draggedItems = snapshotEntries(items);
  }

  function canDropFiles(event, target) {
    return draggedItems && event.dataTransfer?.types.includes(fileDragType) &&
      target.isDir && canWritePath(target.source, target.path) &&
      draggedItems.some(item => !operationSkipReason(item, target));
  }

  function dropFileSelection(target) {
    const items = draggedItems;
    draggedItems = null;
    if (items) openMove(items, 'move', {source:target.source, path:target.path});
  }

  // file info / properties
  async function openInfo(entry) {
    const item = {name:entry.name, source:entry.source || entry._src || route.source,
      path:entry.path ?? entry._path ?? fullPath(entry.name)};
    const request = ++infoRequest;
    infoItem = item;
    infoData = null;
    infoHashing = false;
    infoOwners = null;
    infoOwner = '';
    infoOwnerError = '';
    infoOwnerBusy = false;
    infoOpen = true;
    try {
      const response = await fetch('/api/info?' + new URLSearchParams({source:item.source, path:item.path}));
      if (!response.ok) return;
      const info = await response.json();
      if (request !== infoRequest || !infoOpen) return;
      infoData = info;
      infoOwner = info.owner || '';
      if (info.ownerEditToken && isAdmin) {
        const profiles = await fetch('/api/admin/users');
        if (!profiles.ok) throw new Error('profile list unavailable');
        const {users} = await profiles.json();
        if (request === infoRequest && infoOpen) infoOwners = users.map(row => row.username);
      }
    } catch {
      if (request === infoRequest && infoOpen && infoData?.ownerEditToken) infoOwnerError = t('profilesLoadError');
    }
  }
  async function computeHash() {
    const item = infoItem;
    if (!item) return;
    const request = infoRequest;
    infoHashing = true;
    try {
      const response = await fetch('/api/info?' + new URLSearchParams({source:item.source, path:item.path, hash:'1'}));
      if (response.ok) {
        const info = await response.json();
        if (request === infoRequest && infoOpen && info.sha256) infoData = {...infoData, sha256:info.sha256};
      }
    } catch {} finally { if (request === infoRequest) infoHashing = false; }
  }
  async function saveFileOwner() {
    const item = infoItem;
    const token = infoData?.ownerEditToken;
    const owner = infoOwner;
    if (!item || !token || infoOwnerBusy) return;
    const request = infoRequest;
    const user = currentUser;
    infoOwnerBusy = true;
    infoOwnerError = '';
    try {
      const response = await fetch('/api/admin/file-owner', {method:'POST', headers:{'Content-Type':'application/json'},
        body:JSON.stringify({source:item.source, path:item.path, owner, token})});
      if (!response.ok) throw new Error('owner update');
      if (!isAdmin || currentUser !== user) return;
      if (request === infoRequest && infoOpen) infoData = {...infoData, owner};
      const update = entry => entry.source === item.source && entry.path === item.path ? {...entry, owner} : entry;
      const currentFolder = loadedFolder;
      if (currentFolder?.source === item.source && currentFolder.path === parentDir(item.path)) {
        entries = entries.map(entry => entry.name === item.name ? {...entry, owner} : entry);
      }
      recents = recents.map(update);
      results = results.map(update);
      showToast(t('fileOwnerSaved'));
    } catch { if (request === infoRequest && infoOpen) infoOwnerError = t('fileOwnerSaveError'); }
    finally { if (request === infoRequest) infoOwnerBusy = false; }
  }

  // change own password
  async function changePassword(){ const cur=prompt(t('currentPassword')); if(!cur) return; const nw=prompt(t('newPassword')); if(!nw) return; try{ await apiPost('/api/password',{current:cur,new:nw}); showToast(t('passwordChanged')); }catch(e){ alert(e.message); } }

  // uploads (TUS)
  function uploadFiles(fileset, target = route, rejectedEntries = 0){
    if(!target.source || !canWrite || !currentUser) return;
    const account=location.origin+'|'+currentUser;
    if(uploadManager?.account !== account) {
      uploadManager?.destroy();
      uploadManager=createUploadManager(account,files=>{
        if (files.some(file => file.quotaError && !uploads.some(old => old.id === file.id && old.quotaError))) void loadPersonalQuota();
        uploads=files;
      },()=>{void load();},
        () => showToast(t('uploadResumeRestarted')));
    }
    const rejected = uploadManager.add([...fileset],target.source,target.path) + rejectedEntries;
    if (rejected) showToast(t('uploadSomeRejected', {n:format.number(rejected)}));
  }

  function queueEmptyDirectories(directories, target) {
    const added = new Set();
    const existing = new Set(folderUploads.map(item => item.id));
    for (const relative of directories) {
      const id = JSON.stringify(['folder', target.source, target.path, relative]);
      if (existing.has(id)) continue;
      existing.add(id);
      added.add(id);
      folderUploads.push({id, source:target.source, path:target.path, relative, name:relative,
        directory:true, error:'', pct:0, started:false, done:false});
    }
    void uploadDirectories(folderUploads.filter(item => added.has(item.id)));
  }

  async function uploadDirectories(items) {
    const session = uploadSession;
    let refresh = false;
    for (const item of items) {
      item.error = '';
      item.started = false;
    }
    for (const item of items) {
      if (session !== uploadSession) return;
      if (!folderUploads.includes(item)) continue;
      item.started = true;
      let path = item.path;
      try {
        for (const name of item.relative.split('/')) {
          if (session !== uploadSession) return;
          const nextPath = path ? path + '/' + name : name;
          try {
            await apiPost('/api/mkdir?' + new URLSearchParams({source:item.source, path, name}));
          } catch (err) {
            // A parent may have been created by a file from the same drop.
            if (session !== uploadSession) return;
            const check = await fetch('/api/list?' + new URLSearchParams({source:item.source, path:nextPath}));
            if (!check.ok) throw err;
          }
          path = nextPath;
        }
        item.done = true;
        item.pct = 100;
        refresh = true;
      } catch (err) {
        item.error = err.message || t('uploadFolderFailed');
      }
    }
    if (refresh && session === uploadSession) {
      folderUploads = folderUploads.filter(item => !item.done);
      void load();
    }
  }

  function retryUpload(item) {
    if (item.directory) void uploadDirectories([item]);
    else void uploadManager?.retry(item.id);
  }

  function retryFailedUploads() {
    if (uploads.some(item => item.error && !item.done)) void uploadManager?.retryFailed();
    void uploadDirectories(folderUploads.filter(item => item.error && !item.done));
  }

  function removeUpload(item) {
    if (item.directory) folderUploads = folderUploads.filter(upload => upload.id !== item.id);
    else uploadManager?.remove(item.id);
  }

  onDestroy(() => {resetTrashQueue();uploadSession++;moveTreeController?.abort();quotaRequest?.abort();uploadManager?.destroy();galleryController?.abort();gallery?.destroy();});
  let dragDepth=0;
  function onDragEnter(event) {
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    dragDepth++;
    if (route.source && canWrite && !trashOpen) dragOver = true;
  }
  function onDragLeave(e){ e.preventDefault(); if(--dragDepth<=0){ dragDepth=0; dragOver=false; } }
  async function onDrop(event) {
    event.preventDefault();
    dragDepth = 0;
    dragOver = false;
    if (!route.source || !canWrite || !currentUser || trashOpen || !event.dataTransfer?.types.includes('Files')) return;
    const target = {...route}, session = uploadSession;
    const {files, emptyDirectories, rejected} = await readDroppedTree(event.dataTransfer);
    if (session !== uploadSession) return;
    if (files.length || rejected) uploadFiles(files, target, rejected);
    queueEmptyDirectories(emptyDirectories, target);
  }

  // disks (sidebar) + server ping
  async function loadDisks(){ try{ const r=await fetch('/api/sources'); if(r.ok) diskList=(await r.json()).sources; }catch{} }
  async function loadRecents() {
    const request = ++recentRequest;
    try {
      const r = await fetch('/api/recents');
      if (!r.ok) return;
      const data = await r.json();
      if (request === recentRequest) recents = data.items || [];
    } catch {}
  }
  async function measurePing(){ const t=performance.now(); try{ const r=await fetch('/api/sources',{cache:'no-store'}); serverPing = r.ok ? Math.round(performance.now()-t) : -1; }catch{ serverPing=-1; } }

  function openText(e) {
    textDoc = { source:e._src || route.source, path:e._path || fullPath(e.name), name:e.name };
  }
  function closeTextDocument() {
    textDoc=null;
    const url=new URL(location.href);url.searchParams.delete('textSource');url.searchParams.delete('textPath');
    history.replaceState(null,'',url.href);load();
  }

  // preview
  const imgE=new Set(['jpg','jpeg','png','gif','webp','bmp','svg']);
  const vidE=vidThumbExts;
  const audE=new Set(['mp3','ogg','wav','flac','m4a','aac']);
  const txtE=new Set(['txt','md','log','json','js','ts','go','py','css','html','xml','csv','yml','yaml','sh','c','cpp','h','java','php','rb','ini','conf','sql','svelte','vue']);
  const offE=new Set(['doc','docx','odt','rtf','xls','xlsx','ods','ppt','pptx','odp']);
  function previewType(e){ const x=ext(e.name); if(imgE.has(x))return'image'; if(vidE.has(x))return'video'; if(audE.has(x))return'audio'; if(x==='pdf')return'pdf'; if(offE.has(x))return'office'; if(txtE.has(x))return'text'; return'none'; }
  function inlineUrl(e){ return e._path ? '/api/download?inline=1&source='+enc(e._src)+'&path='+enc(e._path) : '/api/download?inline=1&source='+enc(route.source)+'&path='+enc(fullPath(e.name)); }
  function previewUrl(e) {
    const params = new URLSearchParams({
      source: e._path ? e._src : route.source,
      path: e._path || fullPath(e.name),
      version: '3',
      modified: e.modTime || 0,
    });
    return '/api/preview?' + params;
  }
  async function openPreview(e){
    if (['txt','md'].includes(ext(e.name))) { previewOpen=false; openText(e); return; }
    previewItem=e; previewText=null; previewLoading=false; previewOpen=true;
    if(previewType(e)==='text'){
      if(e.size>2*1024*1024){ previewText=t('previewTooLarge'); return; }
      previewLoading=true;
      try{ const r=await fetch(inlineUrl(e)); previewText=await r.text(); }catch{ previewText=t('previewLoadError'); }
      previewLoading=false;
    }
  }
  function closePreview(){ previewOpen=false; }
  function previewNav(dir){
    if(!previewItem) return;
    if(previewItem._path){
      if (searchVisible || (!route.source && !photosOpen && !placesOpen && !videosOpen && !settingsOpen)) {
        const available = searchVisible ? visibleResults.filter(item => !item.isDir) : recentEntries;
        const index = available.findIndex(item => item.path === previewItem._path && item.source === previewItem._src);
        if (index >= 0 && available.length > 1) openResult(available[(index + dir + available.length) % available.length]);
        return;
      }
      const i = photos.findIndex(p => p.path===previewItem._path && p.source===previewItem._src);
      if(i<0) return;
      openPhoto(photos[(i + dir + photos.length) % photos.length]);
      return;
    }
    const list = entries.filter(e => !e.isDir && previewType(e)!=='none');
    if(!list.length) return;
    const i = list.findIndex(e => e.name===previewItem.name);
    if(i<0) return;
    openPreview(list[(i + dir + list.length) % list.length]);
  }
  let previewIsImage = $derived(previewOpen && previewItem && previewType(previewItem)==='image');

  // photo timeline
  function clearPhotoSelection() {
    photoSelectionMode = false;
    selectedPhotoKeys = new Set();
  }

  function togglePhotoSelection(photo) {
    const key = photoKey(photo);
    const next = new Set(selectedPhotoKeys);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    selectedPhotoKeys = next;
    photoSelectionMode = true;
  }

  function photoActions(photo) {
    const entry = asEntry(photo);
    const actions = [
      entryAction('preview', 'searchPreview', () => openPhoto(photo)),
      entryAction('check', selectedPhotoKeys.has(photoKey(photo)) ? 'clearSelection' : 'selectPhotos',
        () => togglePhotoSelection(photo)),
      entryAction('download', 'download', () => dlResult(photo)),
    ];
    if (canShare && currentRole !== 'viewer') actions.push(entryAction('link', 'shareLink', () => shareItem(entry)));
    if (canWrite && !limitedProfile) actions.push(entryAction('photos', 'toAlbum', () => showAlbumPicker([entry])));
    if (canWritePath(photo.source, photo.path)) {
      actions.push(
        entryAction('move', 'moveTo', () => openMove([entry])),
        entryAction('delete', 'moveToTrash', () => deleteItems([entry]), true),
      );
    }
    return actions;
  }

  $effect(() => {
    if (!photosOpen || needLogin) clearPhotoSelection();
  });

  async function openPhotos(){
    leaveSettings();
    leaveTrash();
    clearPhotoSelection();
    videosOpen=false;
    photoVisualResult=false;
    const request = nextPhotoRequest();
    photoCursor='';photosOpen=true;placesOpen=false;photos=[];photoTotal=0;photosMore=false;photosLoadingMore=false;photoPageError='';photoQuery='';error='';activeAlbum=null;
    photoMemories=[];photoPanel='';photoSearchActive=false;photoSearchIntent=null;submittedPhotoQuery='';
    await loadPhotoPage(true);
    if (request === photoRequest && photosOpen) {
      void loadAlbums(); void loadBackupHealth();
      if (!route.source) void loadPhotoMemories(request);
    }
  }
  async function loadPhotoMemories(request) {
    try {
      const response = await fetch('/api/photos/onthisday');
      if (!response.ok) return;
      const data = await response.json();
      if (request === photoRequest && photosOpen) photoMemories = (data.photos ?? []).slice(0, 12);
    } catch { /* The timeline is usable while memories are unavailable. */ }
  }
  async function togglePhotoPanel(panel) {
    photoPanel = photoPanel === panel ? '' : panel;
    if (photoPanel === 'search') {
      await tick();
      photoSearchField?.focus();
    }
  }
  async function loadPhotoPage(first=false, quietly=false){
    if(photosLoadingMore || (!first && !photosMore))return;
    const request=photoRequest;
    photoPageController?.abort();
    const controller=new AbortController();
    photoPageController=controller;
    const timeout=setTimeout(()=>controller.abort(), 12000);
    const search=photoSearchActive;
    let visual=search && !first && photoVisualResult;
    if(first){error='';if(!quietly)photosLoading=true;}else photosLoadingMore=true;
    photoPageError='';
    try {
      const q=new URLSearchParams({source:route.source||'',path:route.path||'',limit:'240'});
      if(!first && photoCursor)q.set('after',photoCursor);
      if (search) {
        q.set('q', submittedPhotoQuery);
        q.set('mode', 'auto');
        q.set('zone', Intl.DateTimeFormat().resolvedOptions().timeZone);
        if (activeAlbum) q.set('album', activeAlbum.id);
      }
      const quoted = submittedPhotoQuery.startsWith('"') && submittedPhotoQuery.endsWith('"');
      const autoVisual = first && search && !quoted && visualState?.enabled !== false;
      const fetchPage = (endpoint, params=q) => fetch(endpoint + params, {signal:controller.signal});
      let data;
      if (visual || autoVisual) {
        const params = new URLSearchParams(q);
        params.set('mode', autoVisual ? 'auto' : 'visual');
        params.set('k', '60');
        const response = await fetchPage('/api/search/semantic?', params);
        if (response.ok) {
          const matches = await response.json();
          if (visual || matches.photos?.length) {
            data = matches;
            visual = true;
          }
        } else if (visual) {
          throw new Error(t('visualSearchOffline'));
        }
      }
      if (!data) {
        const response = await fetchPage(search ? '/api/search/photos/text?' : '/api/photos?');
        if (!response.ok) throw new Error(t(search ? 'photoSearchError' : 'photosLoadError'));
        data = await response.json();
      }
      if(request===photoRequest && photosOpen && !controller.signal.aborted){
        const page=data.photos||[];
        if (first) photoVisualResult = visual;
        const known=new Set(photos.map(photoKey));photos=first ? page : [...photos,...page.filter(p=>!known.has(photoKey(p)))];
        photoTotal=visual ? photos.length : (search ? data.count : data.total) ?? photos.length;
        photoSearchIntent=data.intent ?? null;photoCursor=data.nextCursor||'';photosMore=!!photoCursor;
      }
    } catch(e){
      if(request===photoRequest && photosOpen && photoPageController===controller){
        const message=controller.signal.aborted ? t(visual ? 'visualSearchOffline' : search ? 'photoSearchError' : 'photosLoadError') : e.message;
        if(first)error=message;else photoPageError=message;
      }
    } finally {
      clearTimeout(timeout);
      if(photoPageController===controller){photoPageController=null;photosLoading=false;photosLoadingMore=false;}
    }
  }
  async function searchPhotos(){
    const text=photoQuery.trim();if(!text){if(activeAlbum)await openAlbum(activeAlbum);else await openPhotos();return;}
    if(text.length<2)return;
    clearPhotoSelection();
    photoVisualResult=false;
    nextPhotoRequest();photoSearchActive=true;submittedPhotoQuery=text;photoPanel='';
    photoSearchIntent=null;photoCursor='';photos=[];photoTotal=0;photosLoadingMore=false;photosMore=false;error='';
    await loadPhotoPage(true);
  }
  function focusModal(node){
    const previous=document.activeElement;
    const focusables=()=>[...node.querySelectorAll('button,input,select,[tabindex="0"]')].filter(e=>!e.disabled && e.getClientRects().length);
    queueMicrotask(()=>focusables()[0]?.focus());
    const onKey=e=>{if(e.key!=='Tab')return;const items=focusables(),first=items[0],last=items.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus();}};
    node.addEventListener('keydown',onKey);
    return {destroy(){node.removeEventListener('keydown',onKey);previous?.focus();}};
  }
  async function loadAlbums(){
    albumError='';
    try { const r=await fetch('/api/albums/list'); if(!r.ok) throw new Error(t('albumLoadFailed')); albums=(await r.json()).albums || []; }
    catch(e){ albumError=e.message; }
  }
  async function loadBackupHealth(){
    backupHealth=null;
    try { const r=await fetch('/api/backup/health'); backupHealth=r.ok ? await r.json() : null; } catch { backupHealth=null; }
  }
  async function openAlbum(album){
    clearPhotoSelection();
    const request=nextPhotoRequest();
    activeAlbum=album; photosLoading=true; photosLoadingMore=false;photosMore=false;photoPageError='';photoQuery='';error=''; photos=[];
    photoSearchActive=false;photoPanel='';photoSearchIntent=null;submittedPhotoQuery='';photoVisualResult=false;
    await loadAlbumPhotos(album, request);
  }
  async function loadAlbumPhotos(album, request) {
    try { const r=await fetch('/api/albums/photos?id='+enc(album.id)); if(!r.ok) throw new Error(t('albumOpenFailed')); const data=await r.json(); if(request===photoRequest && photosOpen){photos=data.photos || [];photoTotal=photos.length;} }
    catch(e){ if(request===photoRequest && photosOpen) error=e.message; } finally { if(request===photoRequest) photosLoading=false; }
  }
  async function refreshPhotosAfterOperation(completed, quietly=false) {
    const affected = new Set(completed.map(photoKey));
    selectedPhotoKeys = new Set([...selectedPhotoKeys].filter(key => !affected.has(key)));
    if (!selectedPhotoKeys.size) clearPhotoSelection();
    photoMemories = photoMemories.filter(photo => !affected.has(photoKey(photo)));
    photos = photos.filter(photo => !affected.has(photoKey(photo)));
    const request = nextPhotoRequest();
    photoCursor = '';
    if (!quietly) photosLoading = true;
    photosLoadingMore = false;
    photosMore = false;
    error = '';
    void loadAlbums();
    if (activeAlbum && !photoSearchActive) await loadAlbumPhotos(activeAlbum, request);
    else await loadPhotoPage(true, quietly);
  }

  function showAlbumPicker(items = []) {
    albumEntries = items.map(item => ({source:item._src ?? route.source, path:item._path ?? fullPath(item.name)}));
    albumError = '';
    albumName = '';
    albumPicker = true;
    void loadAlbums();
  }

  async function putInAlbum(album){
    if (!albumEntries.length || albumBusy) return;
    albumBusy = true;
    albumError = '';
    const failed = [];
    let lastError = '';
    try {
      for (const item of albumEntries) {
        try {
          await apiPost('/api/albums/add?' + new URLSearchParams({id:album.id, ...item}));
        } catch (e) {
          failed.push(item);
          lastError = e.message;
        }
      }
      albumEntries = failed;
      await loadAlbums();
      if (failed.length) {
        albumError = t('albumAddFailed', {n:format.number(failed.length), error:lastError});
        return;
      }
      albumPicker = false;
      showToast(t('albumAdded', {name:album.name}));
    } finally { albumBusy = false; }
  }
  async function createAlbum(){
    const name=albumName.trim(); if(!name || albumBusy)return; albumBusy=true; albumError='';
    try { const r=await apiPost('/api/albums/create?name='+enc(name)); const album=await r.json(); albumName=''; await loadAlbums(); albumBusy=false; if(albumEntries.length) await putInAlbum(album); else {albumPicker=false; await openAlbum(album);} }
    catch(e){ albumError=e.message; } finally { albumBusy=false; }
  }
  const asEntry=p=>({name:p.name,isDir:false,size:p.size,_src:p.source,_path:p.path,isVideo:p.isVideo});
  async function openPhoto(p,list=visiblePhotos){
    const request=++galleryRequest;galleryController?.abort();gallery?.destroy();galleryController=new AbortController();previewOpen=false;
    try {
      const instance=await openGallery({t, signal:galleryController.signal,photos:list,index:Math.max(0,list.findIndex(item=>photoKey(item)===photoKey(p))),
        onshare:canShare ? item=>shareItem(asEntry(item)) : null,onalbum:canWrite && !limitedProfile ? item=>showAlbumPicker([asEntry(item)]) : null,
        canTrash:item=>canWritePath(item.source, item.path), ontrash:item=>deleteItems([asEntry(item)]),
        onclose:()=>{if(request===galleryRequest)gallery=null;}});
      if(request!==galleryRequest || needLogin)instance.destroy();else gallery=instance;
    }catch(e){if(e.name!=='AbortError')showToast(t('galleryOpenError'));}
  }
  function openPlaces() {
    photoPanel='';
    placesOpen=true;
  }
  async function closePlaces() {
    placesOpen=false;
    await tick();
    mapTrigger?.focus({preventScroll:true});
  }
  function openVideos(){leaveSettings();leaveTrash();nextPhotoRequest();photosOpen=false;placesOpen=false;videosOpen=true;query='';if(!diskList.length)void loadDisks();}
  function photoThumb(p){ return '/api/thumb?source='+enc(p.source)+'&path='+enc(p.path)+'&v='+enc(p.revision||p.modTime); }

  // my share links
  async function openShares(){
    try{ const r=await fetch('/api/share/list'); if(!r.ok) throw new Error(await r.text()); sharesList=(await r.json()).shares; sharesOpen=true; }catch(e){ error=e.message; }
  }
  function sharedURL(share) {
    return location.origin + (share.kind === 'upload' ? '/u/' : '/s/') + share.token;
  }
  async function copySharedURL(url) {
    try { await navigator.clipboard.writeText(url); showToast(t('copied')); }
    catch { prompt(t('linkPrompt'), url); }
  }
  async function changeAlbumLink(revoke) {
    const album = activeAlbum;
    if (!album || albumBusy) return;
    albumBusy = true;
    try {
      const action = revoke ? 'revoke-link' : 'share';
      const response = await apiPost('/api/albums/' + action + '?id=' + enc(album.id));
      const data = await response.json();
      if (activeAlbum?.id === album.id) activeAlbum = {...activeAlbum, shareActive: !revoke};
      await loadAlbums();
      if (revoke) showToast(t('albumLinkRevoked'));
      else await copySharedURL(location.origin + data.url);
    } catch (e) {
      error = e.message;
    } finally {
      albumBusy = false;
    }
  }
  async function deleteShare(share) {
    const kind = share.kind === 'upload' ? '&kind=upload' : '';
    try {
      await apiPost('/api/share/delete?token=' + enc(share.token) + kind);
      if (qrToken === share.token) qrToken = '';
      await openShares();
    } catch(e) { error = e.message; }
  }

  // auth
  async function doLogin(event) {
    event.preventDefault();
    if (loginBusy) return;
    // Mobile password managers can fill DOM controls without emitting input.
    const form = event.currentTarget;
    const body = new URLSearchParams(new FormData(form));
    loginErr = '';
    loginBusy = true;
    try {
      const response = await fetch('/api/login', { method: 'POST', body });
      if (!response.ok) {
        loginErr = ({ 401: 'loginRejected', 403: 'loginRequestRejected', 429: 'loginRateLimited' })[response.status] || 'loginUnavailable';
        return;
      }
      const session = await response.json();
      currentUser = session.username; setProfileSession(session); needLogin = false;
      form.reset(); showLoginPassword = false; load(); measurePing();
    } catch {
      loginErr = 'loginUnavailable';
    } finally {
      loginBusy = false;
    }
  }
  async function logout(){try{const response=await fetch('/api/logout',{method:'POST'});if(!response.ok&&response.status!==401)throw new Error(await response.text());}catch(e){error=e.message;return;}clearFileClipboard();draggedItems=null;listingRequest++;recentRequest++;recents=[];loadedFolder=null;leaveSettings();infoOpen=false;infoRequest++;closeMove(true);resetTrashQueue();uploadSession++;uploadManager?.destroy();uploadManager=null;uploads=[];folderUploads=[];galleryRequest++;galleryController?.abort();gallery?.destroy();nextPhotoRequest();placesOpen=false;photosOpen=false;videosOpen=false;photosMore=false;photosLoading=false;photosLoadingMore=false;photoCursor='';photos=[];clearFileSearch();sharesOpen=false;sharesList=[];uploadShareTarget=null;qrToken='';currentUser=''; setProfileSession({role: ''}); needLogin=true; entries=[]; sources=[]; }

  async function closeProfiles(refreshSession = false) {
    usersOpen = false;
    if (!refreshSession) return;
    try {
      const response = await fetch('/api/auth');
      if (!response.ok) throw new Error();
      const session = await response.json();
      if (session.authed) setProfileSession(session);
      else await logout();
    } catch {
      error = t('loginUnavailable');
    }
  }

  let fileInput = $state(), folderInput = $state();
  fetch('/api/auth').then(r=>r.json()).then(d=>{ if(d.authed){ currentUser=d.username; setProfileSession(d); measurePing(); } else needLogin=true; }).catch(()=>{});
  setInterval(measurePing, 30000);
</script>

<svelte:window bind:innerWidth={viewportWidth} oncopy={clearFileClipboard} onblur={clearFileClipboard} onpaste={pasteFiles} ondragend={() => draggedItems = null} onlanguagechange={() => browserLanguages = [...navigator.languages]} onstorage={(event)=>{preferences.storageChanged(event.key);if(event.key === null || event.key === languageStorageKey) language = readLanguage();}} onkeydown={(e)=>{
  handleFileShortcut(e);
  if(e.key==='Escape'){
    if(usersOpen)return;
    if(albumPicker){if(!albumBusy)albumPicker=false;return;}
    if(moveOpen){closeMove();return;}
    if(photosOpen && !settingsOpen && photoPanel){photoPanel='';return;}
    if(photosOpen && !settingsOpen && !placesOpen && !gallery && !previewOpen && photoSelectionMode){clearPhotoSelection();return;}
    closePreview(); sharesOpen=false; davOpen=false; infoOpen=false;
  } else if(previewOpen && !albumPicker && e.key==='ArrowLeft'){ previewNav(-1); }
  else if(previewOpen && !albumPicker && e.key==='ArrowRight'){ previewNav(1); }
}} />
<svelte:body ondragenter={onDragEnter} ondragover={(e)=>e.preventDefault()} ondragleave={onDragLeave} ondrop={onDrop} />

{#snippet fileEntries(items, label, directory, sort, onSort)}
  <FileEntries results={items} {label} {directory} {view} {t} {fmtSize} {fmtDate} {extInfo} {parentDir}
    resultKey={searchResultKey} thumbnailKind={resultThumbnailKind} {sort} {onSort}
    dragging={Boolean(draggedItems)} canDrag={item => !moveOpen && canWritePath(item.source, item.path)}
    onDragStart={(event, item) => startFileDrag(event, item, directory)} canDrop={canDropFiles} onDrop={dropFileSelection}
    onopen={directory ? onItemClick : openResult} actionsFor={directory ? folderActions : resultActions}
    ondoubleclick={directory ? onItemDbl : undefined} onselect={directory && selectionMode ? toggle : undefined} {isSelected}/>
{/snippet}

{#snippet languageSelect()}
  <label class="drow language-setting">
    <span>{t('language')}</span>
    <select value={language} onchange={changeLanguage}>
      <option value="system">{t('system')}</option>
      <option value="ru" lang="ru">Русский</option>
      <option value="en" lang="en">English</option>
    </select>
  </label>
{/snippet}

{#if needLogin}
  <div class="login"><form onsubmit={doLogin}>
    <div class="wordmark big">File<span>hub</span></div><h1 class="login-title">{t('loginHeading')}</h1><p class="login-description">{t('loginDescription')}</p>
    {@render languageSelect()}
    {#if loginErr}<div class="lerr" role="alert">{t(loginErr)}</div>{/if}
    <input type="text" name="username" aria-label={t('username')} placeholder={t('username')} autocomplete="username"
      autocapitalize="none" autocorrect="off" spellcheck="false" required />
    <div class="login-password">
      <input type={showLoginPassword ? 'text' : 'password'} name="password" aria-label={t('password')}
        placeholder={t('password')} autocomplete="current-password" autocapitalize="none" spellcheck="false" required />
      <button type="button" onclick={() => showLoginPassword = !showLoginPassword}>
        {t(showLoginPassword ? 'hidePassword' : 'showPassword')}
      </button>
    </div>
    <button type="submit" disabled={loginBusy}>{t('signIn')}</button>
  </form></div>
{/if}

{#if albumPicker && !needLogin}
  <div class="modal-bg album-modal"><section class="modal" use:focusModal role="dialog" aria-modal="true" aria-label={t('albums')}>
    <div class="mhead"><h2>{t(albumEntries.length ? 'addToAlbum' : 'newAlbum')}</h2><span class="fl"></span><button class="xbtn" disabled={albumBusy} onclick={()=>albumPicker=false} aria-label={t('closeAlbums')}><Icon name="close"/></button></div>
    {#if albumError}<p class="lerr" role="alert">{albumError}</p>{/if}
    {#if albumEntries.length && albums.length}<div class="album-choices">{#each albums as album (album.id)}<button class="dact" disabled={albumBusy} onclick={()=>putInAlbum(album)}><Icon name="folder"/>{album.name}</button>{/each}</div>{/if}
    <form class="album-create" onsubmit={(e)=>{e.preventDefault();void createAlbum();}}><label>{t('albumName')}<input aria-label={t('albumName')} bind:value={albumName} placeholder={t('albumNameExample')} maxlength="120" required/></label><button class="primary" disabled={albumBusy || !albumName.trim()}>{t(albumBusy ? 'albumSaving' : albumEntries.length ? 'createAndAdd' : 'createAlbum')}</button></form>
  </section></div>
{/if}

{#if moveOpen && !needLogin}
  <div class="modal-bg"><section class="modal batch-modal" use:focusModal role="dialog" aria-modal="true" aria-labelledby="file-operation-title">
    <h3 class="mtitle" id="file-operation-title">{t(fileOperationLabels[moveMode], {n:format.number(moveItems.length)})}</h3>
    {#if moveStarted}
      <p class="batch-summary">{moveTargetSource} / {moveTarget || t('root')}</p>
      <p class="batch-summary" aria-live="polite" tabindex="-1" bind:this={moveResultsHeading}>{t('operationSummary', {
        done:format.number(moveSummary.done), skipped:format.number(moveSummary.skipped), failed:format.number(moveSummary.failed),
      })}</p>
      <ul class="batch-results" aria-label={t('operationResults')}>
        {#each moveItems as item (JSON.stringify([item.source, item.path]))}
          <li>
            <div class="batch-row"><strong title={item.path}>{item.name}</strong><span class:error-result={item.status === 'failed'}>{t(operationStatusLabels[item.status])}</span></div>
            {#if item.error || item.reason}<small>{item.error || t(item.reason)}</small>{/if}
          </li>
        {/each}
      </ul>
      <div class="modal-foot">
        <span class="fl"></span>
        <button class="ghost" disabled={moveBusy} onclick={()=>closeMove()}>{t('close')}</button>
        {#if moveSummary.failed}<button class="primary" disabled={moveBusy} onclick={()=>confirmMove(true)}>{t('retryFailed')}</button>{/if}
      </div>
    {:else}
      <label class="move-source"><span>{t('destinationStorage')}</span>
        <select value={moveTargetSource} onchange={event => changeMoveSource(event.currentTarget.value)}>
          {#each moveSources as source (source)}<option value={source}>{source}</option>{/each}
        </select>
      </label>
      <div class="tree">
        <div class="tnode" class:tsel={moveTarget === ''}>
          <button class="tname" disabled={!canWritePath(moveTargetSource, '')} aria-pressed={moveTarget === ''} onclick={()=>moveTarget=''}>{moveTargetSource} {t('rootLabel')}</button>
        </div>
        {@render tree('', 0)}
      </div>
      <div class="modal-foot">
        <span class="dest" title={moveTarget}>{moveTargetSource} / {moveTarget || t('root')}</span>
        <span class="fl"></span>
        <button class="ghost" onclick={()=>closeMove()}>{t('cancel')}</button>
        <button class="primary" disabled={!moveTargetReady} onclick={()=>confirmMove()}>
          {t(moveMode === 'copy' ? 'copyHere' : 'moveHere')}
        </button>
      </div>
    {/if}
  </section></div>
{/if}

{#if textDoc && !needLogin}
  {#key textDoc.source + '|' + textDoc.path}
    <TextDocument {...textDoc} {locale} {t} onclose={closeTextDocument} />
  {/key}
{/if}

{#if previewOpen}
  <div class="pv-bg">
    <div class="pv-head">
      <button class="pvbtn" onclick={closePreview} title={t('closeEscape')} aria-label={t('close')}><Icon name="close"/></button>
      <span class="pv-name">{previewItem.name}</span><span class="fl"></span>
      {#if canWrite && !limitedProfile && (previewType(previewItem)==='image' || previewType(previewItem)==='video')}<button class="pvpill" onclick={()=>showAlbumPicker([previewItem])}>{t('toAlbum')}</button>{/if}
      {#if canShare}<button class="pvpill" onclick={()=>shareItem(previewItem)}>{t('share')}</button>{/if}
      <button class="pvbtn" onclick={()=>dlItem(previewItem)} title={t('download')} aria-label={t('download')}><Icon name="download"/></button>
    </div>
    <div class="pv-body" onclick={closePreview}>
      <button class="pvarrow" onclick={(e)=>{e.stopPropagation();previewNav(-1);}} aria-label={t('back')}><Icon name="back"/></button>
      <div class="pv-stage" onclick={(e)=>e.stopPropagation()}>
        {#if previewType(previewItem)==='image'}
          <img class="pv-media" src={inlineUrl(previewItem)} alt={previewItem.name} />
        {:else if previewType(previewItem)==='video'}
          {#key inlineUrl(previewItem)}
            <CompatibleVideo source={previewItem._src || route.source} path={previewItem._path || fullPath(previewItem.name)} {t}/>
          {/key}
        {:else if previewType(previewItem)==='audio'}
          <div class="pv-audio"><audio src={inlineUrl(previewItem)} controls autoplay></audio></div>
        {:else if previewType(previewItem)==='pdf'}
          <iframe class="pv-frame" src={inlineUrl(previewItem)} title={previewItem.name}></iframe>
        {:else if previewType(previewItem)==='office'}
          {#key previewUrl(previewItem)}
            <OfficePreview src={previewUrl(previewItem)} name={previewItem.name} {t} download={() => dlItem(previewItem)}/>
          {/key}
        {:else if previewType(previewItem)==='text'}
          {#if previewLoading}<div class="pv-msg">{t('loading')}</div>{:else}<pre class="pv-text">{previewText}</pre>{/if}
        {:else}
          <div class="pv-msg">{t('previewUnavailable')}<br/><br/><button class="primary" onclick={()=>dlItem(previewItem)}>{t('download')}</button></div>
        {/if}
      </div>
      <button class="pvarrow" onclick={(e)=>{e.stopPropagation();previewNav(1);}} aria-label={t('forward')}><Icon name="next"/></button>
    </div>
  </div>
{/if}

{#if infoOpen}
  <div class="modal-bg" onclick={()=>infoOpen=false}><div class="modal" onclick={(e)=>e.stopPropagation()}>
    <div class="mhead"><span class="pv-name">{infoItem?.name}</span><span class="fl"></span><button class="xbtn" onclick={()=>infoOpen=false} aria-label={t('close')}><Icon name="close"/></button></div>
    {#if infoData}
      <table class="infotbl">
        <tbody>
        <tr><td>{t('type')}</td><td>{infoData.isDir?t('folder'):infoData.mime}</td></tr>
        <tr><td>{t('size')}</td><td>{infoData.isDir?'—':fmtSize(infoData.size)+' ('+t('bytes', {n:format.number(infoData.size)})+')'}</td></tr>
        <tr><td>{t('modified')}</td><td>{new Date(infoData.modTime*1000).toLocaleString(locale)}</td></tr>
        <tr><td>{t('permissions')}</td><td><code>{infoData.mode}</code></td></tr>
        <tr><td>{t('path')}</td><td><code>{infoData.source} / {infoData.path}</code></td></tr>
        {#if infoData.sha256}<tr><td>SHA-256</td><td><code class="hash">{infoData.sha256}</code></td></tr>{/if}
        {#if infoData.owner && (!infoData.ownerEditToken || !infoOwners)}
          <tr><td>{t('fileOwner')}</td><td>{infoData.owner}</td></tr>
        {/if}
        </tbody>
      </table>
      {#if infoData.ownerEditToken && infoOwners && isAdmin}
        <div class="file-owner-field">
          <label for="file-owner">{t('fileOwner')}</label>
          <select id="file-owner" bind:value={infoOwner} disabled={infoOwnerBusy}>
            <option value="">{t('fileOwnerUnassigned')}</option>
            {#each infoOwners as owner (owner)}<option value={owner}>{owner}</option>{/each}
          </select>
          <button class="secondary" type="button" onclick={saveFileOwner}
            disabled={infoOwnerBusy || infoOwner === (infoData.owner || '')}>{t('save')}</button>
        </div>
      {/if}
      {#if infoOwnerError}<p class="msg" role="alert">{infoOwnerError}</p>{/if}
      {#if !infoData.isDir && !infoData.sha256}<button class="secondary" style="margin-top:12px" onclick={computeHash} disabled={infoHashing}>{infoHashing?t('calculatingHash'):t('calculateHash')}</button>{/if}
    {:else}
      <div class="msg">{t('loading')}</div>
    {/if}
  </div></div>
{/if}

{#if uploadShareTarget}
  <UploadShareDialog initialFolder={uploadShareTarget} sources={diskList} {t}
    close={() => uploadShareTarget = null} created={() => { if (sharesOpen) void openShares(); }} copy={copySharedURL} />
{/if}

{#if sharesOpen}
  <div class="modal-bg" onclick={()=>sharesOpen=false}><div class="modal" onclick={(e)=>e.stopPropagation()}>
    <div class="mhead"><span class="pv-name">{t('myLinksCount', {n:format.number(sharesList.length)})}</span><span class="fl"></span><button class="xbtn" onclick={()=>sharesOpen=false} aria-label={t('close')}><Icon name="close"/></button></div>
    {#if canWrite}<button type="button" class="ghost upload-share-create" onclick={() => uploadShareTarget = {source: route.source, path: route.path}}>{t('uploadShareCreate')}</button>{/if}
    <div class="shlist">
      {#each sharesList as s ((s.kind || 'download') + s.token)}
        <div class="shrow">
          <div class="shinfo">
            <div class="shpath">{s.source} / {s.path || t('rootLabel')}</div>
            <div class="shmeta">
              {t(s.kind === 'upload' ? 'uploadShareTitle' : s.isDir ? 'folder' : 'file')}
              {#if s.hasPassword} · 🔒{/if}
              {#if s.expires} · {t('shareExpires', {date:fmtDate(s.expires)})}{/if}
              {#if s.kind === 'upload'} · {t('uploadShareUsage', {used:format.number(s.received || 0), count:format.number(s.maxFiles), size:fmtSize(s.maxBytes)})}{/if}
            </div>
          </div>
          <button class="ghost sm" class:on={qrToken===s.token} title={t('qrCode')} onclick={()=>qrToken = qrToken===s.token?'':s.token}>QR</button>
          <button class="ghost sm" title={t('copyLink')} onclick={() => copySharedURL(sharedURL(s))}>{t('copy')}</button>
          <button class="ghost sm danger" aria-label={t(s.kind === 'upload' ? 'uploadShareRevoke' : 'delete')} title={t(s.kind === 'upload' ? 'uploadShareRevoke' : 'delete')} onclick={() => deleteShare(s)}>✕</button>
        </div>
        {#if qrToken===s.token}<div class="qrbox"><img src="/api/qr?data={enc(sharedURL(s))}" alt="QR" width="200" height="200" /><div class="qrurl">{sharedURL(s)}</div></div>{/if}
      {/each}
      {#if !sharesList.length}<div class="msg">{t('noShareLinks')}</div>{/if}
    </div>
  </div></div>
{/if}

{#if davOpen}
  <div class="modal-bg" onclick={()=>davOpen=false}><div class="modal" onclick={(e)=>e.stopPropagation()}>
    <div class="mhead"><span class="pv-name">{t('webdav')}</span><span class="fl"></span><button class="xbtn" onclick={()=>davOpen=false} aria-label={t('close')}><Icon name="close"/></button></div>
    <div class="davbody">
      <div class="davurl"><code>{davUrl}</code><button class="ghost sm" onclick={()=>navigator.clipboard.writeText(davUrl).then(()=>showToast(t('copied'))).catch(()=>{})}>{t('copy')}</button></div>
      <p class="davnote">{t('davCredentials', {username:currentUser})}</p>
      <details open><summary>{t('davWindows')}</summary>
        <ol>
          <li>{t('davWindowsPath')} <code>{davUrl}</code></li>
          <li>{t('davFilehubCredentials')}</li>
          <li>{t('davWindowsHelp')}</li>
        </ol>
      </details>
      <details><summary>macOS (Finder)</summary>
        <ol><li>Finder → <b>{t('davFinderPath')}</b> (⌘K)</li><li>{t('davFinderCredentials', {address:davUrl})}</li></ol>
      </details>
      <details><summary>Linux / rclone</summary>
        <ol>
          <li>davfs2: <code>sudo mount -t davfs {davUrl} /mnt/filehub</code></li>
          <li>{t('davGnome')} <code>{davUrl.replace('http://','dav://').replace('https://','davs://')}</code></li>
          <li>rclone: <code>rclone config</code> → {t('davType')} <b>webdav</b>, url <code>{davUrl}</code>, vendor <b>other</b>.</li>
        </ol>
      </details>
      <p class="davwarn">{t('davHttp')}</p>
    </div>
  </div></div>
{/if}

{#if usersOpen && isAdmin}
  <ProfilesDialog {t} {currentUser} formatDate={fmtDate} post={apiPost} onclose={closeProfiles}/>
{/if}

{#snippet tree(parentPath, depth)}
  {#if moveFolderState['/' + parentPath] === 'loading'}
    <div class="tree-message" role="status">{t('loading')}</div>
  {:else if moveFolderState['/' + parentPath] === 'failed'}
    <div class="tree-message" role="status">{t('destinationFolderReadFailed')} <button class="ghost sm" onclick={()=>loadChildren(parentPath)}>{t('retry')}</button></div>
  {/if}
  {#each (moveChildren['/' + parentPath] || []) as ch (ch.path)}
    <div class="tnode" class:tsel={moveTarget===ch.path} style="padding-left:{depth*18+8}px">
      <button class="texp" aria-expanded={!!moveExpanded['/' + ch.path]} aria-label={t(moveExpanded['/' + ch.path] ? 'collapseFolder' : 'expandFolder', {name:ch.name})} onclick={()=>toggleNode(ch.path)}>{moveExpanded['/' + ch.path] ? '▾' : '▸'}</button>
      <button class="tname" disabled={!canWritePath(moveTargetSource, ch.path)} aria-pressed={moveTarget === ch.path} onclick={()=>moveTarget=ch.path}><Icon name="folder" size={16}/> {ch.name}</button>
    </div>
    {#if moveExpanded['/' + ch.path]}{@render tree(ch.path, depth+1)}{/if}
  {/each}
{/snippet}

{#if !needLogin}
<div class="app">
  <aside class="sidebar">
    <div class="wordmark side">File<span>hub</span><small>{t('personalSpace')}</small></div>
    <button class="snav" class:on={!settingsOpen && !trashOpen && !route.source && !photosOpen && !placesOpen && !videosOpen && !searchMode} onclick={()=>go(null)}>
      <Icon name="folder"/>
      <span>{t('files')}</span>
    </button>
    <button class="snav" class:on={photosOpen && !settingsOpen && !trashOpen} onclick={() => placesOpen && !settingsOpen && !trashOpen ? closePlaces() : openPhotos()}>
      <Icon name="photos"/>
      <span>{t('photos')}</span>
    </button>
    <button class="snav" class:on={videosOpen && !settingsOpen && !trashOpen} onclick={openVideos}><Icon name="play"/><span>{t('videoNav')}</span></button>
    {#if canUseTrash}
      <button class="snav trash-nav" class:on={trashOpen && !settingsOpen} type="button" onclick={openTrash}
        aria-label={t('trash')} aria-current={trashOpen && !settingsOpen ? 'page' : undefined}>
        <Icon name="delete"/><span>{t('trash')}</span>
      </button>
    {/if}
    <button class="snav" class:on={settingsOpen} type="button" onclick={openSettings} aria-current={settingsOpen ? 'page' : undefined}><Icon name="settings"/><span>{t('settings')}</span></button>
    {#if androidDownloadAvailable}
      <a class="snav android-download" href={androidDownloadUrl} download="filehub.apk"
        title={t('downloadAndroidApp')} aria-label={t('downloadAndroidApp')}>
        <Icon name="download"/>
        <span class="android-download-full">{t('downloadApp')}</span>
        <span class="android-download-short">Android</span>
      </a>
    {/if}

    <div class="slabel">{t('storageHeading')}</div>
    <div class="sdisks">
      {#each diskList as d (d.name)}
        <button class="sdisk" class:on={!settingsOpen && !trashOpen && route.source===d.name && !photosOpen && !videosOpen} onclick={()=>go(d.name,'')}>
          <span class="sdtop">
            <Icon name="storage"/>
          <span class="sdname">{d.name}</span></span>
          {#if d.total > 0}
            <span class="sbar"><span class="sfill" style="width:{Math.min(100,(1-d.free/d.total)*100)}%"></span></span>
            <span class="sinfo">{t('ofSize', {used:fmtSize(d.total-d.free),total:fmtSize(d.total)})}</span>
          {/if}
        </button>
      {/each}
    </div>

    <div class="fl"></div>
    <div class="sstatus">
      <span class="sdot" class:off={serverPing===-1} class:checking={serverPing===null}></span>
      <span class="sstext"><b>{t('homeServer')}</b><span>{location.host} · {serverPing===-1?t('serverOffline'):serverPing===null?t('serverChecking'):t('serverOnline')}</span></span>
      {#if serverPing!==null && serverPing>=0}<span class="sping">{t('milliseconds', {n:format.number(serverPing)})}</span>{/if}
    </div>
  </aside>

  <main class="main">
    {#if trashRunning || trashFailures.length}
      <div class="trash-feedback">
        {#if trashRunning}
          <span role="status">{t('trashingItems')}</span>
        {:else}
          <div class="trash-feedback-head">
            <strong role="alert">{t('trashFailed')}</strong>
            <div class="trash-feedback-actions">
              <button class="ghost sm" onclick={retryTrash}>{t('retryFailed')}</button>
              <button class="ghost sm" aria-label={t('close')} onclick={()=>trashItems=trashItems.filter(item => item.status !== 'failed')}><Icon name="close" size={16}/></button>
            </div>
          </div>
          <ul class="batch-results" aria-label={t('operationResults')}>
            {#each trashFailures as item (photoKey(item))}
              <li>
                <div class="batch-row"><strong title={item.path}>{item.name}</strong></div>
                <small>{item.source} / {item.path}</small>
                {#if item.error}<small>{item.error}</small>{/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
    {#if settingsOpen && currentUser}
      <SettingsPage {t} {currentUser} {currentRole} {language} bind:theme bind:view bind:clickMode bind:selectionMode
        section={settingsSection} sections={settingsSections} onsection={selectSettingsSection}
        onlanguagechange={changeLanguage} onclose={closeSettings} accountActions={accountSettings} serverActions={serverSettings} />
    {:else if trashOpen && canUseTrash}
      <TrashPage {t} {format} {locale} sources={trashSources} initialSource={route.source}
        revision={trashRevision} close={leaveTrash}
        onrestore={() => {void load(true); if (photosOpen) void refreshPhotosAfterOperation([], true);}} />
    {:else if videosOpen}
      <VideoLibrary sources={diskList} {t} />
    {:else if photosOpen}
      <div class="content photos-wrap" class:map-covered={placesOpen} inert={placesOpen}>
        <div class="phead">
          <div class="photo-heading">
            <h1 class="ptitle">{activeAlbum?.name ?? t('photoHeading')}</h1>
            <div class="pcount">
              <span>{photosLoading ? t('loading') : t(photoVisualResult ? 'visualPhotosShown' : photoSearchActive ? 'searchMatches' : 'photoAndVideoCount', {n:format.number(photoTotal)})}</span>
              {#if backupHealth}
                <button type="button" class="backup-chip" class:verified={backupVerified} aria-expanded={photoPanel==='backup'} aria-label={backupLabel} title={backupLabel} onclick={()=>togglePhotoPanel('backup')}>
                  <Icon name="storage" size={16}/><span>{backupLabel}</span>
                </button>
              {/if}
            </div>
          </div>
          <form class="photo-search searchbox" class:expanded={photoPanel==='search'} onsubmit={(e)=>{e.preventDefault();void searchPhotos();}}>
            <Icon name="search" size={20}/><input aria-label={t('photoSearchLabel')} bind:this={photoSearchField} bind:value={photoQuery} placeholder={t('photoSearchPlaceholder')} minlength="2"/><button type="submit" class="round" aria-label={t('find')}><Icon name="next" size={20}/></button>
            {#if photoQuery}<button type="button" class="round" aria-label={t('clearSearch')} onclick={()=>{photoQuery='';void searchPhotos();}}><Icon name="close" size={18}/></button>{/if}
          </form>
          <div class="photo-actions">
            {#if activeAlbum && canShare && currentRole !== 'viewer'}
              <button type="button" class="photo-tool" disabled={albumBusy} title={t('shareLink')} aria-label={t('shareLink')} onclick={()=>changeAlbumLink(false)}><Icon name="link" size={20}/></button>
              {#if activeAlbum.shareActive}
                <button type="button" class="ghost sm" disabled={albumBusy} onclick={()=>changeAlbumLink(true)}>{t('revokeAlbumLink')}</button>
              {/if}
            {/if}
            {#if desktopPhotoGrid}
              <label class="photo-grid-control">
                <Icon name="grid" size={18}/>
                <select bind:value={photoGridSize} aria-label={t('photoGridSize')} title={t('photoGridSize')}>
                  <option value="compact">{t('photoGridCompact')}</option>
                  <option value="small">{t('photoGridSmall')}</option>
                  <option value="medium">{t('photoGridMedium')}</option>
                  <option value="large">{t('photoGridLarge')}</option>
                </select>
              </label>
            {/if}
            <button type="button" class="photo-tool photo-search-trigger" aria-label={t('photoSearchLabel')} aria-expanded={photoPanel==='search'} onclick={()=>togglePhotoPanel('search')}><Icon name={photoPanel==='search' ? 'close' : 'search'} size={20}/></button>
            <button type="button" class="photo-tool" aria-label={t('albums')} aria-expanded={photoPanel==='albums'} onclick={()=>togglePhotoPanel('albums')}><Icon name="folder" size={20}/></button>
            <button type="button" class="photo-tool" bind:this={mapTrigger} aria-label={t('places')} onclick={openPlaces}><Icon name="map" size={20}/></button>
            {#if photos.length}
              <button type="button" class="photo-tool" title={t('selectPhotos')} aria-label={t('selectPhotos')} aria-pressed={photoSelectionMode}
                onclick={()=>photoSelectionMode ? clearPhotoSelection() : (photoPanel='', photoSelectionMode=true)}><Icon name="check_box" size={20}/></button>
            {/if}
          </div>
          {#if photoPanel==='albums'}
            <div class="album-strip photo-tool-panel" aria-label={t('albums')}>
              <button class="album-chip" class:active={!activeAlbum} onclick={openPhotos}><Icon name="photos"/><strong>{t('allPhotos')}</strong></button>
              {#each albums as album (album.id)}<button class="album-chip" class:active={activeAlbum?.id===album.id} onclick={()=>openAlbum(album)}>{#if album.coverPath}<img src={photoThumb({source:album.coverSource,path:album.coverPath,modTime:''})} alt=""/>{:else}<Icon name="folder"/>{/if}<span><strong>{album.name}</strong><small>{fileCount(album.count)}</small></span></button>{/each}
              {#if canWrite && !limitedProfile}<button class="album-chip new-album" onclick={()=>{photoPanel='';showAlbumPicker();}}><Icon name="add"/><span>{t('newAlbum')}</span></button>{/if}
              {#if albumError && !albumPicker}<div class="msg err" role="alert">{albumError}<button class="ghost" onclick={loadAlbums}>{t('retryAlbums')}</button></div>{/if}
            </div>
          {:else if photoPanel==='backup'}
            <div class="backup-note photo-tool-panel" class:verified={backupVerified} role="status">
              <strong>{backupLabel}</strong>
              {#if backupDate}<small>{t('lastSuccessfulBackup', {date:new Date(backupDate*1000).toLocaleString(locale,{day:'numeric',month:'short',hour:'2-digit',minute:'2-digit'})})}</small>{/if}
            </div>
          {/if}
        </div>
        {#if !activeAlbum && !photoSearchActive && !route.source && visibleMemories.length}
          <section class="photo-memories" aria-label={t('photoMemories')}>
            <h2>{t('photoMemories')}</h2>
            <div class="memory-strip">
              {#each visibleMemories as memory (photoKey(memory))}
                <button type="button" class="memory-card" aria-label={memory.name} onclick={()=>openPhoto(memory, visibleMemories)}>
                  <img src={photoThumb(memory)} alt="" loading="lazy"/>
                  <span>{new Date(memory.modTime*1000).toLocaleDateString(locale,{year:'numeric'})}</span>
                </button>
              {/each}
            </div>
          </section>
        {/if}
        {#if route.source && !activeAlbum}<div class="pscope">{t('folderScope', {path:route.source + (route.path ? ' / ' + route.path : '')})} · <a href="#" onclick={()=>go(null)}>{t('backToFiles')}</a></div>{/if}
        {#if error}<div class="msg err" role="alert">{error}<button class="ghost" onclick={()=>photoSearchActive ? loadPhotoPage(true) : activeAlbum ? openAlbum(activeAlbum) : openPhotos()}>{t('retry')}</button></div>{/if}
        {#if photosLoading}<div class="photo-loading" role="status" aria-label={t('photoLoading')}>{#each [1,2,3,4,5,6,7,8] as n}<span class="photo-skeleton"></span>{/each}</div>{/if}
        {#if photoSearchIntent}
          <div class="search-intent">
            {#if photoSearchIntent.type}
              {@const label = t(photoSearchIntent.type === 'video' ? 'searchVideos' : 'searchImages')}
              <button type="button" class="ghost sm" aria-label={t('removeInferredFilter',{label})} onclick={()=>{photoQuery=photoSearchIntent.withoutType;void searchPhotos();}}>{label} ×</button>
            {/if}
            {#if photoSearchIntent.year}
              {@const label = t('capturedInYear',{year:photoSearchIntent.year})}
              <button type="button" class="ghost sm" aria-label={t('removeInferredFilter',{label})} onclick={()=>{photoQuery=photoSearchIntent.withoutYear;void searchPhotos();}}>{label} ×</button>
            {/if}
          </div>
        {/if}
        {#if !photosLoading && !error && !visiblePhotos.length}<div class="msg">{t(photoSearchIntent ? 'photoFilterEmpty' : photoSearchActive ? 'photoSearchEmpty' : 'photosEmpty')}</div>{/if}
        {#if photoSelectionMode}
          <div class="photo-selection" role="group" aria-label={t('photoSelectionActions')}>
            <strong aria-live="polite">{t('photosSelected', {n:format.number(selectedPhotos.length)})}</strong>
            <div class="photo-selection-actions">
              {#if selectedPhotos.length && canWrite && !limitedProfile}
                <button type="button" class="ghost" onclick={()=>showAlbumPicker(selectedPhotos.map(asEntry))}><Icon name="photos" size={18}/>{t('toAlbum')}</button>
              {/if}
              {#if canModifySelectedPhotos}
                <button type="button" class="ghost" onclick={()=>openMove(selectedPhotos.map(asEntry))}><Icon name="move" size={18}/>{t('moveTo')}</button>
                <button type="button" class="ghost danger" onclick={()=>deleteItems(selectedPhotos.map(asEntry))}><Icon name="delete" size={18}/>{t('moveToTrash')}</button>
              {/if}
              <button type="button" class="photo-tool" aria-label={t('clearSelection')} title={t('clearSelection')} onclick={clearPhotoSelection}><Icon name="close" size={18}/></button>
            </div>
          </div>
        {/if}
        {#if !photosLoading && visiblePhotos.length}
          <PhotoTimeline photos={visiblePhotos} size={photoGridSize} desktop={desktopPhotoGrid}
            groupByDate={!photoSearchActive || !photoVisualResult}
            {locale} {t} onopen={openPhoto} actionsFor={photoActions} onselect={togglePhotoSelection}
            selectionMode={photoSelectionMode} selected={selectedPhotoKeys} hasMore={photosMore} loadingMore={photosLoadingMore}
            onloadmore={()=>loadPhotoPage()} pageError={photoPageError} onretry={()=>loadPhotoPage()}/>
        {/if}
      </div>
      {#if placesOpen}
        <div class="photo-map-view"><PlacesView {locale} {t} onopen={openPhoto} onback={closePlaces}/></div>
      {/if}
    {:else}
      {#snippet queryTags()}
        {#if searchIntent?.type}
          {@const label = t(searchTypes.find(([value])=>value===searchIntent.type)?.[1] ?? 'type')}
          <button type="button" class="query-tag inferred" aria-label={t('removeInferredFilter',{label})} onclick={()=>query=searchIntent.withoutType}>
            <span>{label}</span><Icon name="close" size={14}/>
          </button>
        {/if}
        {#if searchIntent?.year}
          {@const label = t('modifiedInYear',{year:searchIntent.year})}
          <button type="button" class="query-tag inferred" title={label} aria-label={t('removeInferredFilter',{label})} onclick={()=>query=searchIntent.withoutYear}>
            <span>{searchIntent.year}</span><Icon name="close" size={14}/>
          </button>
        {/if}
        {#if searchIntent?.extension}
          <button type="button" class="query-tag inferred" aria-label={t('removeInferredFilter',{label:searchIntent.extension})}
            onclick={()=>query=searchIntent.withoutExtension}>
            <span>{searchIntent.extension}</span><Icon name="close" size={14}/>
          </button>
        {/if}
        <button type="button" class="query-tag" aria-pressed={literalFileSearch} onclick={()=>literalFileSearch=!literalFileSearch}><span>{t('literalSearch')}</span></button>
      {/snippet}
      <header class="topbar">
        <a class="files-heading" href="#" onclick={(e)=>{e.preventDefault();go(null);}}>{t('files')}</a>
        {#if canUseTrash}
          <button type="button" class="obtn trash-mobile-trigger" aria-label={t('trash')} onclick={openTrash}><Icon name="delete" size={20}/></button>
        {/if}
        <div class="file-search-tools">
          <div class="searchbox">
            <div class="file-query">
              <Icon name="search" size={20}/>
              <input aria-label={t('searchFiles')} placeholder={t('searchPlaceholder')} bind:value={query} onfocus={()=>fileFiltersOpen=true} />
              {#if fileQueryActive && !manySearchTags}<div class="file-query-tags inline-tags">{@render queryTags()}</div>{/if}
              {#if query}<button type="button" class="search-clear round" aria-label={t('clearSearch')} onclick={clearFileSearch}><Icon name="close" size={18}/></button>{/if}
            </div>
          </div>
          {#if isAdmin && (route.source || diskList.length)}
            <button type="button" class="obtn storage-report" title={t('storageUsage')} aria-label={t('storageUsage')} onclick={()=>showStorageUsage(route.source, route.path)}>
              <Icon name="storage" size={20}/><span>{t('storageUsage')}</span>
            </button>
          {/if}
        </div>
        <span class="fl"></span>

        {#if selectionMode && selCount>0}
          <div class="selchip">
            <b>{selCount}</b>
            <button onclick={dlSelectedZip}>{t('download')}</button>
            {#if canWrite}<button onclick={()=>openMove(selectedEntries())}>{t('move')}</button>
            <button onclick={()=>openMove(selectedEntries(),'copy')}>{t('copy')}</button>
            <button class="danger" onclick={()=>deleteItems(selectedEntries())}>{t('moveToTrash')}</button>{/if}
            <button class="x" onclick={()=>selected={}}>✕</button>
          </div>
        {/if}

        {#if route.source && view === 'grid' && !searchVisible}
          <div class="sortbox">
            <select bind:value={folderSort.key} title={t('sort')}><option value="name">{t('name')}</option><option value="size">{t('size')}</option><option value="mtime">{t('date')}</option></select>
            <button onclick={()=>folderSort = nextSort(folderSort, folderSort.key)} title={folderSort.order==='asc'?t('ascending'):t('descending')}>{folderSort.order==='asc'?'↑':'↓'}</button>
          </div>
        {/if}
        {#if route.source && canWrite}
          <button type="button" class="obtn" onclick={newFolder}><Icon name="add" size={20}/>{t('createFolder')}</button>
          <input type="file" multiple bind:this={fileInput} style="display:none" onchange={(e)=>{ if(e.target.files.length) uploadFiles(e.target.files); e.target.value=''; }} />
          <input type="file" webkitdirectory multiple bind:this={folderInput} hidden onchange={(event)=>{
            const input = event.currentTarget;
            if (input.files.length) uploadFiles(input.files);
            input.value = '';
          }} />
          <button class="primary up" onclick={()=>fileInput.click()}>
            <Icon name="upload" size={20}/> {t('upload')}
          </button>
          <button class="obtn folder-upload" title={t('uploadFolderPickerHint')} onclick={()=>folderInput.click()}><Icon name="folder" size={20}/> {t('uploadFolder')}</button>
        {/if}
        <div class="seg">
          <button class:on={view==='grid'} aria-pressed={view==='grid'} onclick={()=>view='grid'} title={t('viewGrid')} aria-label={t('viewGrid')}><Icon name="grid" size={20}/></button>
          <button class:on={view==='list'} aria-pressed={view==='list'} onclick={()=>view='list'} title={t('viewList')} aria-label={t('viewList')}><Icon name="list" size={20}/></button>
        </div>
        {#if route.source}
          <nav class="crumbs" aria-label={t('path')} bind:this={folderTrail}>
            <a href={'#'+enc(route.source)+'|'} onclick={()=>go(route.source,'')}
              aria-current={!folderCrumbs.length ? 'location' : undefined} title={route.source}>{route.source}</a>
            {#each folderCrumbs as c (c.path)}
              <span class="sep" aria-hidden="true">›</span>
              <a href={'#'+enc(route.source)+'|'+enc(c.path)} onclick={()=>go(route.source,c.path)}
                aria-current={c.path === route.path ? 'location' : undefined} title={c.name}>{c.name}</a>
            {/each}
            {#if !canWrite}<span class="folder-read-only">{t('folderReadOnly')}</span>{/if}
          </nav>
        {/if}
      </header>

      {#if personalQuota}
        <section class="personal-quota" aria-label={t('profilePersonalFolder')}>
          <strong>{t('profilePersonalFolder')}</strong>
          <span>{t('ofSize',{used:fmtSize(personalQuota.usedBytes),total:fmtSize(personalQuota.limitBytes)})}</span>
          <span>{t('profileQuotaFree',{size:fmtSize(personalQuota.availableBytes)})}</span>
          {#if personalQuota.reservedBytes > 0}<span>{t('profileQuotaReserved',{size:fmtSize(personalQuota.reservedBytes)})}</span>{/if}
        </section>
      {/if}

      {#if uploadQueue.length}
        <section class="uploads" aria-label={t('uploadQueue')}>
          <div class="queue-head">
            <strong>{t('uploadQueueHeading')}</strong>
            <details class="queue-help">
              <summary>{t('uploadResumeHelp')}</summary>
              <p>{t('uploadResumeExplanation')}</p>
            </details>
            {#if uploadQueue.some(u => u.error)}
              <button class="ghost sm queue-retry" onclick={retryFailedUploads}>{t('retryFailed')}</button>
            {/if}
          </div>
          {#each uploadQueue as u (u.id)}
            <div class="upload-row">
              <span class="upn">
                <strong title={u.name}>{u.name}</strong>
                <small title={u.source + (u.path ? ' / ' + u.path : '')}>
                  {u.error ? t(u.quotaError ? 'profileQuotaFull' : u.directory ? 'uploadFolderFailed' : 'uploadFailed') : u.paused ? t('uploadPaused') : !u.started ? t('uploadQueued') : u.directory ? t('operationRunning') : t('ofSize', {used:fmtSize(u.uploaded),total:fmtSize(u.size)})}
                  · {u.source}{u.path ? ' / ' + u.path : ''}
                </small>
                {#if u.error}<small class="upload-error-detail">{
                  u.error.includes('ERR_FILEHUB_LOCAL_READ') ? t('uploadReadFailed')
                    : u.error.includes('ERR_UNEXPECTED_EOF') ? t('uploadInterrupted') : u.error}</small>{/if}
              </span>
              {#if u.started && !u.directory && !u.error}
                <div class="upload-progress">
                  <div class="bar" role="progressbar" aria-label={t('uploadProgress', {name:u.name})}
                    aria-valuemin="0" aria-valuemax="100" aria-valuenow={u.pct}>
                    <div class="fill" style:width={u.pct+'%'}></div>
                  </div>
                  <span class="upp" aria-hidden="true">{format.percent(u.pct/100)}</span>
                </div>
              {/if}
              <div class="upload-actions">
                {#if u.error}<button class="ghost sm" onclick={()=>retryUpload(u)}>{t('retry')}</button>
                {:else if !u.directory}<button class="ghost sm" onclick={()=>uploadManager?.toggle(u.id)}>{u.paused?t('continue'):t('pause')}</button>{/if}
                <button class="ghost sm" disabled={u.directory && u.started && !u.error} aria-label={t('removeNamed', {name:u.name})} onclick={()=>removeUpload(u)}>✕</button>
              </div>
            </div>
          {/each}
        </section>
      {/if}
      {#if error}<div class="msg err">{error}</div>{/if}

      <div class="content" class:drag={dragOver} class:search-content={searchMode}>
        {#if fileQueryActive}<div class="file-query-tags below-tags" class:many={manySearchTags}>{@render queryTags()}</div>{/if}
        {#if searchMode || fileFiltersOpen}
          <SearchFilters bind:filters={searchFilters} active={searchFiltersActive} {diskList} {searchTypes} {locale} {t} onreset={resetSearchFilters}/>
        {/if}
        {#if searchMode}
          {#if !searchFiltersActive && visibleSemanticPhotos.length}
            <div class="sec-label">{t('semanticPhotos')}</div>
            <div class="pgrid sem">
              {#each visibleSemanticPhotos as p (p.source+'|'+p.path)}
                <button class="pcell" onclick={()=>openPhoto(p,visibleSemanticPhotos)} title={p.name}>
                  <span class="photo-image"><img loading="lazy" src={photoThumb(p)} alt="" /></span><span class="photo-name">{p.name}</span>
                  {#if p.isVideo}<span class="pvid"><Icon name="play" size={18}/></span>{/if}
                </button>
              {/each}
            </div>
          {/if}
          <div class="rhead">
            <span aria-live="polite">
              {#if searchLoading}{t('loading')}
              {:else if !searchError}
                {t(nextSearchOffset ? 'searchLoaded' : 'searchMatches', {n:format.number(visibleResults.length)})}
              {/if}
            </span>
            {#if searchStatus?.indexing && !limitedProfile}<span aria-live="polite">{t('searchIndexing')}</span>{/if}
          </div>
          {#if searchError}<div class="msg err" role="alert">{t(searchError)}{#if searchError === 'searchLoadError'}<button class="ghost sm" onclick={()=>searchRefresh++}>{t('retry')}</button>{/if}</div>{/if}
          {#if visibleResults.length || (view === 'list' && searchLoading)}
            {@render fileEntries(visibleResults, t('searchResults'), false, searchSort, key => searchSort = nextSort(searchSort, key))}
          {:else if !searchLoading && !searchError}
            <div class="msg">{t('emptySearch')}</div>
          {/if}
          {#if nextSearchOffset}
            <div class="search-more">
              {#if searchMoreError}<span role="alert">{t('searchLoadError')}</span>{/if}
              <button type="button" class="ghost" aria-disabled={searchLoadingMore} onclick={loadMoreSearchResults}>
                {t(searchLoadingMore ? 'loading' : searchMoreError ? 'retry' : 'searchShowMore')}
              </button>
            </div>
          {/if}

        {:else if !route.source}
          {#if diskList.length}
            <section class="storage-links" aria-label={t('storages')}>
              {#each diskList as d (d.name)}
                <button type="button" class="storage-link" onclick={() => go(d.name, '')}>
                  <Icon name="storage" size={18}/>
                  <span class="storage-link-label">
                    <b title={d.name}>{d.name}</b>
                    {#if d.total > 0}
                      <span>{t('diskFree', {free:fmtSize(d.free)})}</span>
                    {/if}
                  </span>
                  <Icon name="chevron_right" size={18}/>
                </button>
              {/each}
            </section>
          {/if}

          {#if recentEntries.length}
            <h2 class="sec-title">{t('recentFiles')}</h2>
            {@render fileEntries(recentEntries, t('recentFiles'), false, recentSort, key => recentSort = nextSort(recentSort, key))}
          {/if}

        {:else if folderEntries.length}
          {@render fileEntries(folderEntries, t('files'), true, folderSort, key => folderSort = nextSort(folderSort, key))}
        {:else if !loading}
          <div class="msg">{t(canWrite ? 'emptyWritableFolder' : 'emptyFolder')}</div>
        {/if}
      </div>
    {/if}

    {#if serverBackupsOpen && isAdmin}
      <ServerBackups {locale} {t} sources={diskList} close={() => serverBackupsOpen=false}/>
    {/if}
    {#if partialUploadsOpen && isAdmin}
      <PartialUploads {locale} {format} {t} close={() => partialUploadsOpen=false}/>
    {/if}
    {#if storageInsights && isAdmin}
      <StorageInsights {format} {t} sources={diskList} initial={storageInsights} close={() => storageInsights=null}
        openFolder={(source,path)=>{ storageInsights=null; clearFileSearch(); go(source,path); }}/>
    {/if}
    {#if toast}<div class="toast" role="status">{toast}</div>{/if}
  </main>
</div>

{/if}

<style>
  .wordmark{font-weight:800;letter-spacing:-.025em;color:var(--text)}
  .wordmark span{color:var(--accent)}
  .wordmark.side{font-size:20px;padding:0 10px 16px}
  .wordmark.big{font-size:30px;text-align:center;margin-bottom:8px}
  .fl{flex:1}

  .app{display:flex;height:100%}

  /* ---- sidebar ---- */
  .sidebar{flex:0 0 236px;background:var(--panel);border-right:1px solid var(--divider);display:flex;flex-direction:column;padding:18px 12px 14px;overflow:auto}
  .snav{display:flex;align-items:center;gap:11px;padding:10px 12px;border:none;border-radius:12px;cursor:pointer;background:transparent;color:var(--text2);font:600 14px 'Golos Text',sans-serif;margin-top:4px;transition:background .2s}
  .snav:hover{background:var(--row)}
  .android-download{text-decoration:none}
  .android-download .android-download-short{display:none}
  .snav.on{background:var(--accent-pill);color:var(--accent-deep);font-weight:700}
  .slabel{margin:20px 10px 4px;font-size:11.5px;font-weight:600;letter-spacing:.02em;color:var(--muted)}
  .sdisks{display:flex;flex-direction:column;gap:6px}
  .personal-quota{display:flex;flex-wrap:wrap;align-items:center;gap:6px 16px;padding:10px 24px;border-bottom:1px solid var(--divider);color:var(--muted);font-size:.875rem}
  .personal-quota strong{color:var(--text)}
  .sdisk{display:flex;flex-direction:column;gap:7px;padding:11px 12px;border:1px solid var(--divider);border-radius:14px;cursor:pointer;background:var(--panel);text-align:left;transition:all .2s}
  .sdisk:hover{border-color:#bcd4f5}
  .sdisk.on{border-color:#8fbaf0;background:var(--accent-soft)}
  .sdtop{display:flex;align-items:center;gap:8px}
  .sdname{font-size:13.5px;font-weight:700;color:var(--text)}
  .sbar{display:block;height:5px;border-radius:99px;background:var(--row);overflow:hidden}
  .sfill{display:block;height:100%;border-radius:99px;background:var(--accent)}
  .sinfo{font-size:11.5px;color:var(--muted)}
  .sstatus{display:flex;align-items:center;gap:10px;padding:11px 12px;border-radius:14px;background:var(--panel2)}
  .sdot{width:8px;height:8px;border-radius:50%;background:var(--green);box-shadow:0 0 0 3px color-mix(in srgb,var(--green) 15%,transparent)}
  .sdot.off{background:var(--err);box-shadow:0 0 0 3px color-mix(in srgb,var(--err) 15%,transparent)}
  .sstext{flex:1;min-width:0;display:flex;flex-direction:column;gap:1px}
  .sstext b{font-size:12.5px}
  .sstext span{font-size:11px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .sping{font:600 10.5px ui-monospace,monospace;color:var(--green-deep)}

  /* ---- main ---- */
  .main{flex:1;min-width:0;position:relative;display:flex;flex-direction:column;height:100%}
  .topbar{flex:none;display:flex;align-items:center;gap:10px;padding:12px 20px;background:color-mix(in srgb,var(--panel) 75%,transparent);backdrop-filter:blur(14px);border-bottom:1px solid var(--divider);z-index:6}
  .files-heading,.crumbs a{padding:6px 8px;border-radius:8px;text-decoration:none;font-size:14px}
  .files-heading{color:var(--text);font-weight:700;padding-block:10px}
  .topbar .trash-mobile-trigger{display:none}
  .files-heading:hover,.crumbs a:hover{background:var(--row)}
  .crumbs{
    display:flex;align-items:center;gap:2px;flex:0 0 100%;min-width:0;max-width:100%;
    overflow-x:auto;scrollbar-width:thin
  }
  .crumbs a{flex:none;max-width:min(100%,40ch);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--accent-deep);font-weight:600}
  .crumbs a[aria-current]{color:var(--text);font-weight:700}
  .crumbs a:focus-visible{outline-offset:-2px}
  .crumbs .sep{flex:none;color:var(--chev);font-size:12px}
  .folder-read-only{flex:none;margin-inline-start:6px;padding:3px 8px;border:1px solid var(--divider);border-radius:6px;color:var(--muted);font-size:12px;white-space:nowrap}
  .searchbox{flex:0 0 260px;display:flex;align-items:center;gap:8px;background:var(--panel2);border:1px solid var(--divider);border-radius:12px;padding:8px 12px}
  .searchbox input{flex:1;min-width:0;border:none;outline:none;background:transparent;font:13px 'Golos Text',sans-serif;color:var(--text)}
  .sortbox{display:flex;border:1px solid var(--divider);border-radius:10px;overflow:hidden;background:var(--panel)}
  .sortbox select{background-color:var(--panel);color:var(--text);border:0;padding:7px 6px;font:13px 'Golos Text',sans-serif;cursor:pointer}
  .sortbox option{background-color:var(--panel);color:var(--text)}
  .sortbox button{background:transparent;color:var(--text2);border:0;border-left:1px solid var(--divider);padding:7px 10px;font-size:14px;cursor:pointer}
  .obtn{display:flex;align-items:center;gap:7px;border:1px solid var(--border);background:var(--panel);border-radius:12px;padding:8px 14px;cursor:pointer;font:600 13px 'Golos Text',sans-serif;color:var(--text2)}
  .obtn:hover{border-color:#bcd4f5}
  .primary{display:flex;align-items:center;gap:7px;border:none;background:var(--accent);border-radius:12px;padding:9px 16px;cursor:pointer;font:700 13px 'Golos Text',sans-serif;color:#fff;box-shadow:0 6px 16px color-mix(in srgb,var(--accent) 30%,transparent)}
  .primary:hover{background:var(--accent-deep)}
  .primary.sm{padding:8px 12px;box-shadow:none}
  .seg{display:flex;background:var(--panel2);border:1px solid var(--divider);border-radius:12px;padding:3px}
  .seg button{border:none;border-radius:9px;cursor:pointer;padding:6px 9px;background:transparent;color:var(--muted);display:flex}
  .seg button.on{background:var(--panel);color:var(--accent-deep);box-shadow:var(--shadow)}
  .round{width:36px;height:36px;border-radius:50%;border:1px solid var(--border);background:var(--panel);cursor:pointer;display:flex;align-items:center;justify-content:center;color:var(--text2)}
  .round:hover{border-color:#bcd4f5}
  .selchip{display:flex;align-items:center;gap:2px;background:var(--accent-pill);border-radius:12px;padding:5px 6px 5px 12px}
  .selchip b{font-size:12.5px;font-weight:700;color:var(--accent-deep);margin-right:4px}
  .selchip button{border:none;background:none;cursor:pointer;font:600 12.5px 'Golos Text',sans-serif;color:var(--accent-deep);padding:4px 7px;border-radius:7px}
  .selchip button:hover{background:color-mix(in srgb,var(--accent) 12%,transparent)}
  .selchip button.danger{color:var(--err)}
  .selchip button.x{color:var(--text2)}

  .drow{display:flex;align-items:center;gap:10px;padding:9px 12px;font-size:13px;font-weight:500;cursor:pointer}
  .drow span{flex:1}
  .language-setting select{max-width:65%;padding:5px 8px;color:var(--text);background:var(--panel2);border:1px solid var(--border);border-radius:8px;font:inherit}
  .dact{width:100%;display:flex;align-items:center;gap:11px;padding:9px 12px;border:none;border-radius:11px;background:none;cursor:pointer;text-align:left;color:var(--text2);font:600 13px 'Golos Text',sans-serif}
  .dact:hover{background:var(--row)}

  /* ---- content ---- */
  .content{flex:1;overflow-y:auto;padding:18px 24px 40px}
  .content.drag{outline:3px dashed var(--accent);outline-offset:-10px}
  .sec-title{font-size:15px;font-weight:700;margin:4px 0 12px}
  .sec-label{font-size:12.5px;font-weight:600;color:var(--muted);margin:4px 0 10px}

  .storage-links{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,232px),248px));gap:10px;margin-bottom:24px}
  .storage-link{
    min-height:44px;min-width:0;display:flex;align-items:center;gap:9px;padding:6px 12px;
    border:1px solid var(--border);border-radius:10px;background:var(--panel);
    color:var(--text);cursor:pointer;text-align:start
  }
  .storage-link:hover{border-color:var(--accent);background:var(--row)}
  .storage-link-label{flex:1;min-width:0;display:flex;align-items:baseline;gap:7px}
  .storage-link-label b{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px}
  .storage-link-label span{flex-shrink:0;font-size:12px;color:var(--muted);white-space:nowrap}

  .search-intent{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:8px}
  .search-intent button[aria-pressed="true"]{color:var(--accent-deep);background:var(--accent-soft)}

  .pgrid.sem{grid-template-columns:repeat(auto-fill,minmax(110px,1fr));gap:6px;margin:0 0 22px;max-width:900px}
  .search-clear{border:0;background:transparent;flex-shrink:0;width:28px;height:28px}
  .search-more{display:flex;flex-wrap:wrap;align-items:center;justify-content:center;gap:10px;margin-top:16px}
  .rhead{display:flex;justify-content:space-between;flex-wrap:wrap;gap:6px 16px;padding:2px 2px 12px;color:var(--muted);font-size:12.5px}
  .msg{padding:40px 10px;color:var(--muted);text-align:center}.msg.err{color:var(--err);padding:12px 24px;text-align:left}

  .uploads{flex-shrink:0;max-height:min(30vh,180px);overflow:auto;padding:8px clamp(14px,2vw,24px);border-bottom:1px solid var(--divider);background:var(--panel)}
  .queue-head{display:flex;flex-wrap:wrap;align-items:baseline;gap:2px 12px;font-size:12px;color:var(--muted)}
  .queue-help summary{cursor:pointer;color:var(--accent-deep)}
  .queue-help p{max-width:60ch;margin:4px 0;font-size:12px}
  .queue-retry{margin-inline-start:auto}
  .upload-row{display:grid;grid-template-columns:minmax(0,1fr) auto auto;align-items:center;gap:2px 12px;padding:3px 0;font-size:13px}
  .upn{grid-column:1;min-width:0}
  .upn strong,.upn small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .upn small{font-size:11px;color:var(--muted)}
  .upn .upload-error-detail{white-space:normal;overflow-wrap:anywhere}
  .upload-progress{grid-column:2;display:flex;align-items:center;gap:6px;width:clamp(84px,14vw,160px)}
  .upp{flex:none;font-size:11px;color:var(--muted);font-variant-numeric:tabular-nums}
  .bar{flex:1;height:4px;background:var(--row);border-radius:99px;overflow:hidden}
  .fill{height:100%;background:var(--accent);transition:width .2s}
  .upload-actions{grid-column:3;display:flex;gap:4px;justify-content:flex-end}
  @media (max-width:600px){
    .upload-row{grid-template-columns:minmax(0,1fr) auto}
    .upload-progress{grid-column:1;grid-row:2;width:100%;max-width:140px}
    .upload-actions{grid-column:2;grid-row:1}
    .upload-progress + .upload-actions{grid-row:1 / span 2}
  }

  /* ---- photos ---- */
  .photos-wrap{padding:20px 24px 40px}
  .ptitle{font-size:23px;font-weight:800;letter-spacing:-.02em}
  .pscope{margin-top:8px;font-size:12.5px;color:var(--muted)}
  .pscope a{cursor:pointer}
  .pgroup{margin-top:22px}
  .pdate{font-size:16px;font-weight:700;margin-bottom:10px;text-transform:capitalize}
  .pgrid{display:grid;grid-template-columns:repeat(auto-fill,minmax(148px,1fr));gap:4px}
  .pcell{position:relative;aspect-ratio:1;padding:0;border:0;border-radius:10px;overflow:hidden;cursor:pointer;background:var(--row);content-visibility:auto;contain-intrinsic-size:auto 148px}
  .pcell img{width:100%;height:100%;object-fit:cover;display:block;transition:transform .15s}
  .pcell:hover img{transform:scale(.97)}
  .pvid{position:absolute;right:7px;bottom:7px;background:rgba(10,14,24,.6);color:#fff;border-radius:7px;padding:2px 7px;font-size:10.5px;font-weight:600}

  /* ---- photo viewer ---- */
  .pv-bg{position:fixed;inset:0;background:var(--dark);z-index:95;display:flex;flex-direction:column;animation:fhFade .2s ease}
  @keyframes fhFade{from{opacity:0}to{opacity:1}}
  .pv-head{display:flex;align-items:center;gap:12px;padding:14px 18px 0}
  .pv-name{color:#fff;font-size:14.5px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:50vw}
  .pvbtn{width:38px;height:38px;border-radius:50%;background:rgba(255,255,255,.1);border:none;cursor:pointer;color:#fff;font-size:16px;display:flex;align-items:center;justify-content:center}
  .pvbtn:hover{background:rgba(255,255,255,.18)}
  .pvpill{border:none;border-radius:99px;background:rgba(255,255,255,.1);color:rgba(255,255,255,.9);cursor:pointer;padding:9px 15px;font:600 12.5px 'Golos Text',sans-serif}
  .pvpill:hover{background:rgba(255,255,255,.18)}
  .pv-body{flex:1;display:flex;align-items:center;justify-content:center;gap:18px;padding:18px 24px 26px;min-height:0}
  .pvarrow{flex:none;width:44px;height:44px;border-radius:50%;background:rgba(255,255,255,.08);border:none;cursor:pointer;color:#fff;font-size:22px;line-height:1;display:flex;align-items:center;justify-content:center}
  .pvarrow:hover{background:rgba(255,255,255,.16)}
  .pv-stage{flex:1;height:100%;display:flex;align-items:center;justify-content:center;min-width:0}
  .pv-media{max-width:100%;max-height:100%;border-radius:16px;box-shadow:0 24px 60px rgba(0,0,0,.45)}
  .pv-frame{width:100%;height:100%;border:0;border-radius:12px;background:#fff}
  .pv-text{margin:0;padding:20px;white-space:pre-wrap;word-break:break-word;font:13px/1.55 ui-monospace,Menlo,Consolas,monospace;color:#e6e9ef;background:rgba(255,255,255,.04);border-radius:12px;max-width:100%;max-height:100%;overflow:auto;align-self:stretch;width:100%}
  .pv-audio{background:rgba(255,255,255,.06);padding:24px;border-radius:16px}
  .pv-msg{color:rgba(255,255,255,.8);text-align:center;padding:40px}

  /* ---- modals / ctx / login / toast ---- */

  .login{position:fixed;inset:0;background:var(--bg);display:flex;flex-direction:column;align-items:center;overflow:auto;box-sizing:border-box;padding:max(16px,env(safe-area-inset-top)) max(16px,env(safe-area-inset-right)) max(16px,env(safe-area-inset-bottom)) max(16px,env(safe-area-inset-left));z-index:100}
  .login form{box-sizing:border-box;width:min(384px,100%);flex:none;margin:auto 0;background:var(--panel);border:1px solid var(--border);border-radius:20px;padding:32px;min-width:0;display:flex;flex-direction:column;gap:12px;box-shadow:0 10px 40px rgba(15,25,60,.1)}
  .login input{padding:12px 14px;border-radius:14px;border:1px solid var(--border);background:var(--panel);color:var(--text);font:14px 'Golos Text',sans-serif}
  .login-password{display:flex;align-items:stretch;gap:6px}
  .login-password input{flex:1;min-width:0}
  .login-password button[type="button"]{flex:none;min-height:48px;padding:0 8px;border:1px solid var(--border);background:var(--panel2);color:var(--accent-deep);font:600 12px 'Golos Text',sans-serif}
  .login input:focus{outline:none;border-color:var(--accent)}
  .login button{padding:13px;border:0;border-radius:14px;background:var(--accent);color:#fff;font:700 15px 'Golos Text',sans-serif;cursor:pointer}
  .lerr{color:var(--err);font-size:13px;text-align:center}

  .modal-bg{position:fixed;inset:0;background:rgba(10,16,36,.35);display:flex;align-items:center;justify-content:center;z-index:90;padding:20px}
  .modal{background:var(--panel);border:1px solid var(--border);border-radius:22px;padding:20px;width:480px;max-width:94vw;display:flex;flex-direction:column;box-shadow:0 24px 70px rgba(15,25,60,.2)}
  .mtitle{margin:0 0 10px;font-size:16px;font-weight:700}
  .mhead{display:flex;align-items:center;padding:0 0 12px}
  .xbtn{width:34px;height:34px;border-radius:50%;border:1px solid var(--border);background:var(--panel);cursor:pointer;color:var(--text2);font-size:14px}
  .tree{overflow:auto;border:1px solid var(--divider);border-radius:12px;background:var(--panel2);min-height:220px;max-height:50vh;padding:6px 0}
  .tnode{display:flex;align-items:center;gap:4px;padding:4px 6px}
  .tnode.tsel{background:var(--rowsel);border-radius:8px}
  .tnode button{border:0;background:transparent;text-align:left;min-height:32px;padding:2px 6px;font:inherit}
  .tnode .tname{margin:0;color:var(--text)}
  .tnode .tname :global(svg){vertical-align:middle;color:var(--accent-deep)}
  .texp{flex:0 0 28px;cursor:pointer;color:var(--muted)}
  .tname{cursor:pointer;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .tree-message{padding:6px 12px;color:var(--muted);font-size:13px}
  .move-source{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:8px;margin-bottom:12px;font-size:13px;font-weight:600}
  .move-source select{min-height:36px;max-width:100%;border:1px solid var(--border);border-radius:8px;padding:6px 10px;color:var(--text);background:var(--panel2);font:inherit}
  .batch-summary{margin:0 0 10px;font-size:13px;color:var(--muted);line-height:1.4}
  .batch-results{list-style:none;margin:0;padding:0;max-height:50vh;overflow:auto;display:grid;gap:6px}
  .batch-results li{padding:10px;background:var(--panel2);border-radius:8px;min-width:0;font-size:13px}
  .batch-results small{display:block;margin-top:4px;color:var(--muted);overflow-wrap:anywhere}
  .batch-row{display:flex;align-items:baseline;justify-content:space-between;gap:8px}
  .batch-row strong{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .batch-row span{flex-shrink:0;color:var(--muted)}
  .batch-row .error-result{color:var(--err)}
  .batch-modal button:disabled{opacity:.58;cursor:default}
  .trash-feedback{flex:none;padding:10px clamp(16px,2vw,28px);background:var(--panel);border-bottom:1px solid var(--divider);font-size:13px;color:var(--muted)}
  .trash-feedback-head{display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap;margin-bottom:8px;color:var(--err)}
  .trash-feedback-actions{display:flex;align-items:center;gap:6px}
  .trash-feedback .batch-results{max-height:min(28vh,160px)}
  .modal-foot{display:flex;align-items:center;gap:10px;margin-top:12px}
  .modal-foot .dest{color:var(--muted);font-size:13px;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .ghost{background:var(--panel);color:var(--text2);border:1px solid var(--border);border-radius:10px;padding:8px 12px;font:600 13px 'Golos Text',sans-serif;cursor:pointer}
  .ghost:hover{border-color:#bcd4f5}
  .ghost.sm{padding:6px 10px;font-size:12px}
  .ghost.danger{color:var(--err)}
  .secondary{background:var(--accent-soft);color:var(--accent-deep);border:none;border-radius:12px;padding:11px 14px;font:700 13px 'Golos Text',sans-serif;cursor:pointer}
  .shlist{display:flex;flex-direction:column;gap:8px;max-height:60vh;overflow:auto}
  .shrow{display:flex;align-items:center;gap:8px;padding:10px 12px;background:var(--panel2);border:1px solid var(--divider);border-radius:12px}
  .shinfo{flex:1;overflow:hidden}
  .shpath{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:600;font-size:13px}
  .shmeta{color:var(--muted);font-size:12px}
  .upload-share-create{align-self:flex-start;margin-bottom:8px}
  .shrow select{background:var(--panel2);border:1px solid var(--divider);color:var(--text);border-radius:10px;padding:7px;font:13px 'Golos Text',sans-serif;cursor:pointer}
  .infotbl{width:100%;border-collapse:collapse;font-size:13px}
  .infotbl td{padding:8px;border-bottom:1px solid var(--divider);vertical-align:top}
  .infotbl td:first-child{color:var(--muted);width:96px;white-space:nowrap}
  .infotbl code{font:12px ui-monospace,Menlo,Consolas,monospace;word-break:break-all}
  .infotbl code.hash{color:var(--accent-deep)}
  .file-owner-field{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-top:12px;font-size:13px}
  .file-owner-field select{flex:1;min-width:0}
  .qrbox{display:flex;flex-direction:column;align-items:center;gap:8px;padding:12px;background:#fff;border-radius:12px;margin:2px 0 6px}
  .qrurl{color:#333;font:11px ui-monospace,monospace;word-break:break-all;text-align:center}
  .davbody{display:flex;flex-direction:column;gap:10px;max-height:64vh;overflow:auto;font-size:13px;line-height:1.5}
  .davurl{display:flex;align-items:center;gap:8px;background:var(--panel2);border:1px solid var(--divider);border-radius:12px;padding:8px 10px}
  .davurl code{flex:1;color:var(--accent-deep);overflow:auto;white-space:nowrap}
  .davbody code{background:var(--panel2);border:1px solid var(--divider);border-radius:6px;padding:1px 5px;font:12px ui-monospace,monospace}
  .davbody details{background:var(--panel2);border:1px solid var(--divider);border-radius:12px;padding:8px 12px}
  .davbody summary{cursor:pointer;font-weight:600}
  .davbody ol{margin:8px 0 2px;padding-left:20px;display:flex;flex-direction:column;gap:5px}
  .davnote{color:var(--muted);margin:0}
  .davwarn{color:var(--err);margin:2px 0 0;font-size:12px}

  .toast{position:absolute;left:50%;bottom:22px;transform:translateX(-50%);z-index:70;background:var(--toast);color:#fff;font-size:13px;font-weight:500;padding:10px 18px;border-radius:99px;box-shadow:0 8px 26px rgba(10,16,36,.32);white-space:nowrap;animation:fhPop .25s ease}
  @keyframes fhPop{from{opacity:0;transform:translate(-50%,12px)}to{opacity:1;transform:translate(-50%,0)}}

  @media(max-width:720px){ .sidebar{flex-basis:64px}.wordmark.side span,.snav span,.slabel,.sdisk,.sstext,.sping{display:none}.searchbox{flex-basis:150px} }
  /* Studio adaptive product layout. */
  .sidebar{flex-basis:224px;padding:28px 16px 20px;gap:4px}
  .wordmark.side{font-size:25px;padding:0 12px 26px;letter-spacing:-1px}
  .wordmark.side small{display:block;font-size:11px;font-weight:400;letter-spacing:0;color:var(--muted);margin-top:3px}
  .snav{min-height:48px;font-weight:500;font-size:14px;border-radius:10px}
  .snav.on{font-weight:600}
  .slabel{font-size:10px;letter-spacing:1.2px;margin-top:28px;margin-bottom:8px}
  .sdisk{border:0;background:transparent;padding:12px}
  .sinfo{font-size:11px}
  .sstatus{border:1px solid var(--divider);border-radius:12px;padding:12px 10px}
  .sdot.checking{background:var(--muted);box-shadow:none}
  .sping{display:none}
  .topbar{padding:14px 28px;gap:12px;background:var(--bg);backdrop-filter:none;flex-wrap:wrap}
  .searchbox{flex:1 1 260px;max-width:420px;min-height:46px;padding:10px 14px;background:var(--panel);border:1px solid var(--border)}
  .file-search-tools{display:flex;align-items:center;gap:8px;flex:1 1 260px;min-width:0;max-width:420px}
  @media(min-width:761px){.file-search-tools:has(.storage-report){flex-basis:390px;max-width:580px}}
  .file-search-tools .searchbox{flex:1;min-width:0;max-width:none;height:46px;padding:0 10px 0 13px}
  .file-search-tools .searchbox:focus-within{border-color:var(--accent)}
  .file-query{display:flex;flex:1;align-items:center;gap:7px;min-width:0}
  .file-query-tags{display:flex;flex-wrap:wrap;gap:4px;max-width:100%}
  .inline-tags{flex:0 1 auto;flex-wrap:nowrap;min-width:0;max-width:62%}
  .below-tags{display:none;margin-bottom:8px}
  .below-tags.many{display:flex}
  .query-tag{display:inline-flex;align-items:center;gap:4px;min-width:0;min-height:30px;padding:3px 8px;border:1px solid var(--border);border-radius:7px;background:var(--panel2);color:var(--text2);font-weight:600;font-size:12px;cursor:pointer}
  .query-tag span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  @media(max-width:1060px){.inline-tags{display:none}.below-tags{display:flex}}
  .query-tag.inferred,.query-tag[aria-pressed="true"]{background:var(--accent-pill);color:var(--accent-deep);border-color:transparent}
  .query-tag:hover{border-color:var(--accent)}
  .file-search-tools .storage-report{flex:none;min-height:46px;white-space:nowrap}
  .searchbox input{font-size:14px}
  .topbar>.fl{display:none}
  .obtn,.primary,.ghost{min-height:44px;font-size:13px;border-radius:10px;box-shadow:none}
  .primary{font-weight:600}
  .round{width:44px;height:44px;border-radius:10px}
  .seg{padding:3px;border-radius:10px}
  .seg button{min-width:38px;min-height:38px;align-items:center;justify-content:center;border-radius:7px}
  .sortbox{min-height:44px}
  .selchip{flex-wrap:wrap;gap:4px}
  .selchip button{min-height:36px}
  .content{padding:28px 32px 48px;scrollbar-width:thin}
  .content.search-content{padding-top:20px}
  .ptitle{font-size:30px;line-height:1.2;font-weight:750;letter-spacing:-.9px;margin:0 0 10px}
  .sec-title{font-size:18px;font-weight:650;margin-bottom:16px;letter-spacing:-.3px}
  .sec-label{font-size:13px;margin-bottom:12px}
  .photos-wrap{padding:32px;display:flex;flex-direction:column;min-height:0;overflow:hidden}
  .photos-wrap.map-covered{visibility:hidden}
  .photo-map-view{position:absolute;inset:0;display:flex;flex-direction:column;background:var(--bg)}
  .folder-upload{white-space:nowrap}
  .ptitle{font-size:30px}
  .pdate{font-size:18px;font-weight:650;letter-spacing:-.3px;display:flex;justify-content:space-between;align-items:center;margin:26px 0 16px}
  .pdate>span{font-size:12px;font-weight:400;color:var(--muted);letter-spacing:0}
  .pgrid{grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:24px 14px}
  .pcell{aspect-ratio:auto;border-radius:0;overflow:visible;background:transparent;text-align:left;padding:0;position:relative;color:var(--text)}
  .photo-image{display:block;aspect-ratio:1;overflow:hidden;background:var(--row);border-radius:10px}
  .pcell img{width:100%;height:100%;object-fit:cover;display:block;transition:transform .2s}
  .photo-name{display:block;font-size:12px;font-weight:500;padding:8px 2px 0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .pvid{top:8px;right:8px;bottom:auto;display:flex;align-items:center;justify-content:center;width:28px;height:28px;padding:4px;border-radius:8px}
  .photo-loading{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:16px}
  .photo-skeleton{aspect-ratio:1;background:var(--row);border-radius:10px;animation:loading-pulse 1.6s ease-in-out infinite alternate}
  @keyframes loading-pulse{to{opacity:.45}}
  .msg{border-radius:14px;padding:28px;line-height:1.6;text-align:center;background:var(--panel);border:1px solid var(--border);font-size:14px;margin:16px 0}
  .msg.err{border-color:var(--err);color:var(--err);text-align:left}
  .msg.err button{margin-top:12px;color:var(--err)}
  .drow,.dact{min-height:44px;font-size:13px}
  .drow select{max-width:150px;min-height:40px;border:1px solid var(--border);border-radius:8px;padding:5px;color:var(--text);background:var(--panel2);font-size:12px}
  .login-title{font-size:26px;line-height:1.3;letter-spacing:-.7px;margin:20px 0 10px;text-align:center}
  .login-description{text-align:center;color:var(--muted);font-size:14px;line-height:1.6;margin:0 0 24px}
  .login form{border-radius:20px;padding:32px;box-shadow:0 12px 48px #10142608;background:var(--panel)}
  .login input,.login button{min-height:48px;font-size:16px;border-radius:10px}
  .pv-head{padding:16px 24px;gap:14px;min-height:76px}
  .pvbtn{width:44px;height:44px;border-radius:10px;display:flex;align-items:center;justify-content:center}
  .pvpill{min-height:44px;border-radius:10px;padding:10px 16px}
  .pv-name{font-size:14px;font-weight:500}
  .pv-media{border-radius:10px;box-shadow:none}
  .pvarrow{min-width:48px;min-height:48px;display:flex;align-items:center;justify-content:center;border-radius:12px}
  .modal{border-radius:18px;max-width:calc(100vw - 32px);max-height:calc(100dvh - 48px);overflow:auto}
  .modal-foot{flex-wrap:wrap}
  .xbtn{min-width:44px;min-height:44px;display:flex;align-items:center;justify-content:center}
  .toast{max-width:calc(100vw - 32px);white-space:normal;overflow-wrap:anywhere;font-size:14px;line-height:1.5;border-radius:12px}
  @media(hover:hover){.pcell:hover img{transform:scale(1.025)}}
  @media(min-width:1400px){.content{padding-left:40px;padding-right:40px}.pgrid{grid-template-columns:repeat(auto-fill,minmax(240px,1fr))}}
  @media(max-width:760px){
    .app{height:100dvh;padding-bottom:calc(76px + env(safe-area-inset-bottom,0px));padding-top:env(safe-area-inset-top,0px)}
    .sidebar{position:fixed;bottom:0;left:0;right:0;height:calc(76px + env(safe-area-inset-bottom,0px));padding:6px 12px calc(6px + env(safe-area-inset-bottom,0px));border-right:0;border-top:1px solid var(--border);z-index:30;flex-direction:row;gap:4px;overflow:visible;flex-basis:auto}
    .sidebar .wordmark,.sidebar .slabel,.sidebar .sdisks,.sidebar .sstatus,.sidebar>.fl{display:none}
    .sidebar .snav{display:flex;flex:1;flex-direction:column;justify-content:center;gap:4px;font-size:11px;padding:8px 4px;margin:0;border-radius:12px;min-width:0}
    .sidebar .snav span{display:block}
    .sidebar .android-download .android-download-full{display:none}
    .sidebar .android-download .android-download-short{display:block}
    .sidebar .trash-nav{display:none}
    .snav.on{background:var(--accent-pill)}
    .main{width:100%}
    .topbar{padding:12px 16px;gap:8px;flex-wrap:wrap}
    .files-heading{order:0}
    .crumbs{order:4}
    .searchbox{order:2;flex-basis:100%;max-width:none;min-height:48px}
    .file-search-tools{order:2;flex-basis:100%;max-width:none}
    .file-search-tools .searchbox{order:0}
    .file-search-tools .storage-report{justify-content:center;width:48px;min-height:48px;padding:0}
    .storage-report span{display:none}
    .searchbox input{font-size:16px}
    .topbar>.seg,.topbar>.sortbox,.topbar>.obtn,.topbar>.primary,.topbar>.selchip{order:3}
    .topbar>.seg{order:0;margin-left:auto}
    .topbar .trash-mobile-trigger{display:grid;place-items:center;order:0;width:42px;min-height:42px;padding:0}
    .obtn{padding:10px 12px;min-height:48px}
    .primary{min-height:48px;padding:10px 14px}
    .content{padding:24px 20px 32px}
    .ptitle{font-size:27px}
    .storage-links{grid-template-columns:repeat(auto-fill,minmax(min(100%,132px),1fr));gap:8px;margin-bottom:20px}
    .storage-link{padding:6px 8px;gap:5px}
    .storage-link-label{align-items:stretch;flex-direction:column;gap:1px}
    .storage-link-label span{font-size:11px;white-space:normal}
    .pdate{font-size:16px;margin-top:24px}
    .pgrid,.photo-loading{grid-template-columns:repeat(2,minmax(0,1fr));gap:18px 10px}
    .photo-name{font-size:11px}
    .photo-image{border-radius:8px}
    .drow,.dact{min-height:48px;font-size:14px}
    .pv-head{padding:12px;gap:8px;flex-wrap:wrap}
    .pv-name{flex:1;max-width:none;min-width:0;overflow-wrap:anywhere;font-size:13px}
    .pvpill{font-size:12px;padding:8px 10px}
    .pv-body{gap:0;padding:0}
    .pvarrow{min-width:36px;width:36px;border-radius:8px;padding:4px}
    .pv-stage{min-width:0}
    .pv-media{border-radius:0}
    .login form{padding:28px 24px}
    .toast{bottom:92px}
  }


  .phead{position:relative;display:flex;align-items:center;gap:14px;flex-wrap:wrap;margin-bottom:14px}
  .photo-heading{flex:1;min-width:0}
  .photo-heading h1{margin:0;overflow-wrap:anywhere}
  .pcount{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-top:3px;font-size:12px;color:var(--muted)}
  .photo-actions{display:flex;gap:6px;align-items:center}
  @media(min-width:761px){.photo-heading{flex-basis:170px}}
  .photo-grid-control{display:flex;align-items:center;gap:6px;min-height:44px;padding:0 8px 0 10px;border:1px solid var(--border);border-radius:10px;background:var(--panel);color:var(--text2)}
  .photo-grid-control select{min-height:40px;padding-inline:2px;border:0;background:transparent;color:var(--text);font:inherit;font-size:13px;cursor:pointer}
  .photo-tool,.backup-chip{display:inline-flex;align-items:center;justify-content:center;gap:5px;background:var(--panel);color:var(--text2);border:1px solid var(--border);border-radius:10px;cursor:pointer}
  .photo-tool{width:44px;height:44px}
  .photo-tool[aria-expanded="true"],.photo-tool[aria-pressed="true"]{background:var(--accent-soft);color:var(--accent-deep)}
  .photo-selection{display:flex;flex-shrink:0;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:6px 12px;padding:6px 10px;margin-bottom:8px;border:1px solid var(--border);border-radius:10px;background:var(--panel)}
  .photo-selection strong{font-size:13px}
  .photo-selection-actions{display:flex;align-items:center;flex-wrap:wrap;gap:4px}
  .photo-selection-actions button{display:inline-flex;align-items:center;justify-content:center;gap:5px;min-height:36px;padding:4px 8px;white-space:nowrap}
  .photo-selection-actions .photo-tool{width:36px;height:36px}
  .photo-selection-actions .danger{color:var(--err)}
  .photo-search-trigger{display:none}
  .backup-chip{min-height:28px;padding:3px 8px;font-size:11px}
  .backup-note.verified,.backup-chip.verified{color:var(--green-deep)}
  .photo-tool-panel{position:absolute;top:calc(100% + 5px);right:0;z-index:25;width:min(320px,100%);padding:10px;color:var(--text);background:var(--panel);border:1px solid var(--border);border-radius:12px;box-shadow:0 12px 30px #10142620}
  .photo-search{max-width:360px;min-width:180px;flex:1;margin-top:0;padding:4px 8px 4px 14px}
  .photo-search .round{border:0;background:transparent;flex-shrink:0}
  .backup-note small{display:block;margin-top:4px;color:var(--muted)}
  .album-strip{display:flex;flex-direction:column;gap:6px;max-height:min(330px,55dvh);overflow:auto}
  .album-chip{display:flex;align-items:center;gap:10px;min-height:44px;padding:8px 10px;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--text);cursor:pointer;text-align:left;flex-shrink:0}
  .album-chip.active{border-color:var(--accent);background:var(--rowsel)}
  .album-chip img{width:32px;height:32px;object-fit:cover;border-radius:7px}
  .album-chip span{min-width:0}.album-chip strong{display:block;font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:550}.album-chip small{display:block;font-size:11px;color:var(--muted);margin-top:3px}
  .new-album{font-size:13px;color:var(--accent-deep)}
  .photo-memories{min-width:0;flex-shrink:0;margin-bottom:8px}
  .photo-memories h2{font-size:15px;margin:0 0 7px}
  .memory-strip{display:flex;gap:8px;overflow-x:auto;padding:2px 2px 7px}
  .memory-card{position:relative;flex:0 0 116px;height:88px;padding:0;overflow:hidden;border:0;border-radius:10px;background:var(--row);cursor:pointer}
  .memory-card img{width:100%;height:100%;object-fit:cover}
  .memory-card span{position:absolute;bottom:5px;left:5px;padding:2px 6px;border-radius:6px;background:#101426c7;color:#fff;font-size:11px}
  .album-modal{z-index:110}.album-modal h2{font-size:20px;margin:0}.album-create{display:flex;flex-direction:column;gap:16px;padding:24px}.album-create label{font-size:13px;display:flex;flex-direction:column;gap:8px}.album-create input{min-height:48px;padding:12px;border:1px solid var(--border);border-radius:10px;background:var(--panel);color:var(--text)}.album-choices{padding:12px 24px;max-height:240px;overflow:auto}
  @media(max-width:760px){
    .photos-wrap{padding:16px}
    .phead{gap:6px;margin-bottom:14px;flex-wrap:nowrap}
    .photo-heading h1{font-size:clamp(20px,6.5vw,26px)}
    .photo-search-trigger{display:inline-flex}
    .backup-chip{min-width:32px;min-height:32px;padding:5px}
    .backup-chip span{display:none}
    .photo-search{display:none;position:absolute;top:calc(100% + 5px);inset-inline:0;max-width:none;z-index:25}
    .photo-search.expanded{display:flex;min-width:0}
    .photo-search input,.album-create input{font-size:16px}
  }
  @media(max-width:360px){
    .phead .photo-tool{width:36px}
    .photo-actions{gap:4px}
  }

</style>
