<script>
  import Icon from './Icon.svelte';

  let {t, format, locale, sources, initialSource, revision, close, onrestore} = $props();
  let selectedSource = $state('');
  const source = $derived(
    sources.find(disk => disk.name === selectedSource)?.name ??
    sources.find(disk => disk.name === initialSource)?.name ?? sources[0]?.name ?? ''
  );
  const date = $derived(new Intl.DateTimeFormat(locale, {dateStyle:'medium', timeStyle:'short'}));
  let loaded = $state(null), loading = $state(false), busy = $state(false);
  let loadError = $state(false), operation = $state(null), reload = $state(0), confirmation;
  const items = $derived(loaded?.source === source ? loaded.items : []);
  let selectedIds = $state(new Set()), rowErrors = $state({}), pending = $state(null);
  let progress = $state(null);
  const selection = $derived(items.filter(item => selectedIds.has(item.id)));

  $effect(() => {
    revision;
    reload;
    const storage = source;
    if (!storage) return;
    const controller = new AbortController();
    void load(storage, controller.signal);
    return () => controller.abort();
  });

  async function load(storage, signal) {
    loading = true;
    loadError = false;
    try {
      const response = await fetch('/api/trash/list?' + new URLSearchParams({source:storage}), {signal});
      if (!response.ok) throw new Error();
      const listing = await response.json();
      if (!signal.aborted) {
        loaded = {source:storage, ...listing};
        selectedIds = new Set(listing.items.filter(item => selectedIds.has(item.id)).map(item => item.id));
      }
    } catch {
      if (!signal.aborted) loadError = true;
    } finally {
      if (!signal.aborted) loading = false;
    }
  }

  function changeSource(event) {
    selectedSource = event.currentTarget.value;
    selectedIds = new Set();
    rowErrors = {};
    operation = null;
  }

  function toggleItem(id, checked) {
    const next = new Set(selectedIds);
    if (checked) next.add(id);
    else next.delete(id);
    selectedIds = next;
  }

  function askBeforeDeleting(action) {
    if (action === 'delete' && !selection.length) return;
    pending = {action, storage:source, targets:[...selection]};
    confirmation.showModal();
  }

  function confirmDeletion() {
    const batch = pending;
    confirmation.close();
    if (batch) void updateTrash(batch.action, batch.targets, batch.storage);
  }

  async function send(action, storage, item) {
    const query = new URLSearchParams({source:storage});
    if (item) query.set('id', item.id);
    const failure = action === 'restore' ? 'trashRestoreFailed' : action === 'delete' ? 'trashDeleteFailed' : 'trashEmptyFailed';
    try {
      const response = await fetch('/api/trash/' + action + '?' + query, {method:'POST'});
      if (!response.ok) {
        return {error:action === 'restore' && response.status === 409 ? 'trashOriginalFolderUnavailable' : failure};
      }
      return action === 'restore' ? await response.json() : {};
    } catch {
      return {error:failure};
    }
  }

  async function updateTrash(action, targets = [], storage = source) {
    if (busy || !storage || storage !== source || (action !== 'empty' && !targets.length)) return;
    busy = true;
    operation = null;
    try {
      if (action === 'empty') {
        const {error} = await send(action, storage);
        operation = error ? {error:true, label:error} : {label:'trashCleared'};
        return;
      }

      // A restored folder must be in place before restoring its separately
      // deleted children.
      const ordered = action === 'restore'
        ? [...targets].sort((a, b) => a.origPath.split('/').length - b.origPath.split('/').length)
        : targets;
      const failures = {...rowErrors};
      const completed = new Set();
      let renamedPath = '';
      if (targets.length > 1) progress = {done:0, total:targets.length};
      for (const [index, item] of ordered.entries()) {
        const result = await send(action, storage, item);
        if (result.error) {
          failures[item.id] = result.error;
        } else {
          delete failures[item.id];
          completed.add(item.id);
          if (result.path && result.path !== item.origPath) renamedPath = result.path;
        }
        if (progress) progress.done = index + 1;
      }
      rowErrors = failures;
      selectedIds = new Set([...selectedIds].filter(id => !completed.has(id)));
      if (action === 'restore' && completed.size) onrestore();
      if (targets.length === 1) {
        operation = completed.size
          ? {label:action === 'delete' ? 'trashDeleted' : renamedPath ? 'trashRestoredAs' : 'trashRestored',
            values:{path:renamedPath}}
          : null;
      } else {
        operation = {error:completed.size !== targets.length,
          label:action === 'restore' ? 'trashRestoreResult' : 'trashDeleteResult',
          values:{done:format.number(completed.size), total:format.number(targets.length)}};
      }
    } finally {
      progress = null;
      reload++;
      busy = false;
    }
  }
</script>

<section class="trash-page" aria-labelledby="trash-title">
  <header>
    <button class="back" type="button" aria-label={t('back')} onclick={close}><Icon name="back" size={20}/></button>
    <h1 id="trash-title">{t('trash')}</h1>
    <div class="tools">
      {#if sources.length > 1}
        <select aria-label={t('storages')} value={source} disabled={busy} onchange={changeSource}>
          {#each sources as disk (disk.name)}
            <option value={disk.name}>{disk.name}</option>
          {/each}
        </select>
      {:else if source}
        <span class="source"><Icon name="storage" size={17}/>{source}</span>
      {/if}
      {#if source}
        <button type="button" class="danger" disabled={busy || loading || loadError || !items.length || !!selection.length}
          onclick={() => askBeforeDeleting('empty')}>
          {t('emptyTrash')}
        </button>
      {/if}
    </div>
  </header>

  {#if operation}
    <p class:err={operation.error} role={operation.error ? 'alert' : 'status'}>{t(operation.label, operation.values)}</p>
  {/if}
  {#if loadError}
    <p class="err" role="alert">{t('trashLoadFailed')} <button type="button" onclick={() => reload++}>{t('retry')}</button></p>
  {:else if !source}
    <p>{t('trashNoStorage')}</p>
  {:else if loading && !items.length}
    <p role="status">{t('loading')}</p>
  {:else if !items.length}
    <p>{t('trashEmpty')}</p>
  {/if}

  {#if items.length}
    {#if selection.length}
      <div class="selection-bar" role="group" aria-label={t('trashSelectionActions')}>
        <strong aria-live="polite">{t('trashSelected', {n:format.number(selection.length)})}</strong>
        <button type="button" disabled={busy || loading || loadError} onclick={() => updateTrash('restore', selection)}>
          {t('restoreFromTrash')}
        </button>
        <button type="button" class="danger" disabled={busy || loading || loadError} onclick={() => askBeforeDeleting('delete')}>
          {t('deleteTrashSelection')}
        </button>
        <button type="button" class="selection-close" aria-label={t('clearSelection')} disabled={busy} onclick={() => selectedIds = new Set()}>
          <Icon name="close" size={17}/>
        </button>
      </div>
    {/if}
    {#if progress}
      <p role="status">{t('trashBatchProgress', {done:format.number(progress.done), total:format.number(progress.total)})}</p>
    {/if}
    <ul aria-label={t('trash')}>
      {#each items as item (item.id)}
        <li class:selected={selectedIds.has(item.id)}>
          <label class="file-check">
            <input type="checkbox" checked={selectedIds.has(item.id)} disabled={busy || loading || loadError}
              aria-label={t('selectTrashItem', {name:item.name})} onchange={(event) => toggleItem(item.id, event.currentTarget.checked)}/>
          </label>
          <span class="file-icon"><Icon name={item.isDir ? 'folder' : 'list'} size={20}/></span>
          <span class="file">
            <strong title={item.name}>{item.name}</strong>
            <small title={item.origPath}>{item.origPath}</small>
            {#if rowErrors[item.id]}
              <small class="row-error" role="alert">{t(rowErrors[item.id])}</small>
            {/if}
          </span>
          <span class="details">
            {date.format(item.deletedAt * 1000)}
            {#if !item.isDir}<small>{format.size(item.size)}</small>{/if}
          </span>
          <button type="button" disabled={busy || loading || loadError} aria-label={t('restoreTrashItem', {name:item.name})}
            onclick={() => updateTrash('restore', [item])}>
            {t('restoreFromTrash')}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
  {#if loaded?.source === source && loaded?.retentionDays}
    <p class="retention">{t('trashRetention', {days:format.number(loaded.retentionDays)})}</p>
  {/if}

  <dialog bind:this={confirmation} onclose={() => pending = null} aria-labelledby="empty-trash-title" aria-describedby="empty-trash-description">
    <h2 id="empty-trash-title">
      {pending?.action === 'delete'
        ? t('deleteTrashSelectionTitle', {n:format.number(pending.targets.length), source:pending.storage})
        : t('emptyTrashTitle', {source:pending?.storage ?? source})}
    </h2>
    <p id="empty-trash-description">{t(pending?.action === 'delete' ? 'deleteTrashSelectionDescription' : 'emptyTrashDescription')}</p>
    <div class="confirm-actions">
      <button type="button" onclick={() => confirmation.close()}>{t('cancel')}</button>
      <button type="button" class="danger filled" onclick={confirmDeletion}>
        {t(pending?.action === 'delete' ? 'deleteTrashSelection' : 'emptyTrash')}
      </button>
    </div>
  </dialog>
</section>

<style>
  .trash-page{flex:1;min-height:0;overflow:auto;padding:clamp(16px,3vw,32px)}
  header{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:20px}
  h1{margin:0;font-size:clamp(22px,3vw,30px);letter-spacing:-.02em}
  .tools{margin-inline-start:auto;display:flex;align-items:center;gap:8px}
  .source{display:flex;align-items:center;gap:7px;color:var(--muted);font-size:13px}
  button,select{font:inherit;color:var(--text);min-height:38px;background:var(--panel);border:1px solid var(--border);border-radius:9px;padding:7px 11px}
  button{cursor:pointer;font-size:13px;font-weight:600}
  button:hover:not(:disabled){background:var(--row)}
  button:disabled{opacity:.55;cursor:default}
  .back{display:grid;place-items:center;padding:0;width:40px}
  .danger,.err{color:var(--err)}
  .filled,.filled:hover:not(:disabled){background:var(--err);color:#fff;border-color:var(--err)}
  p{font-size:14px;color:var(--muted);margin:12px 0}
  .retention{font-size:12px}
  .selection-bar{position:sticky;top:0;z-index:1;display:flex;align-items:center;gap:6px 10px;flex-wrap:wrap;margin-bottom:8px;padding:8px 10px;border:1px solid var(--border);border-radius:10px;background:var(--panel)}
  .selection-bar strong{margin-inline-end:auto;color:var(--muted)}
  .selection-close,.file-check{display:grid;place-items:center;width:36px;padding:0}
  .file-check{height:38px;cursor:pointer}
  .file-check input{width:17px;height:17px;margin:0;accent-color:var(--accent);cursor:pointer}
  ul{padding:0;margin:0;border:1px solid var(--border);border-radius:12px;background:var(--panel);list-style:none}
  li{display:grid;grid-template-columns:36px 36px minmax(0,1fr) auto auto;align-items:center;gap:8px 12px;padding:10px 12px}
  li + li{border-top:1px solid var(--divider)}
  li.selected{background:var(--accent-pill)}
  li:first-child{border-start-start-radius:inherit;border-start-end-radius:inherit}
  li:last-child{border-end-start-radius:inherit;border-end-end-radius:inherit}
  .file-icon{display:grid;place-items:center;height:36px;border-radius:9px;background:var(--accent-pill);color:var(--accent-deep)}
  .file,.details{min-width:0}
  strong,small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  strong{font-size:14px}
  small,.details{font-size:12px;color:var(--muted)}
  .row-error{white-space:normal;color:var(--err)}
  .details{text-align:right}
  dialog{max-width:min(420px,calc(100% - 32px));padding:20px;border:1px solid var(--border);border-radius:14px;background:var(--panel);color:var(--text)}
  dialog::backdrop{background:rgba(0,0,0,.48)}
  h2{margin:0;font-size:18px}
  .confirm-actions{display:flex;gap:8px;justify-content:flex-end;flex-wrap:wrap;margin-top:18px}
  @media(max-width:600px){
    .tools{flex-basis:100%;margin-inline-start:0;justify-content:space-between}
    li{grid-template-columns:30px 28px minmax(0,1fr) auto;gap:3px 6px;padding-inline:8px}
    .file-check{width:30px;grid-row:span 2}
    .file-icon{grid-row:span 2;height:32px}
    .details{grid-column:3;grid-row:2;text-align:left}
    .details small{display:inline;margin-inline-start:5px}
    li button{grid-column:4;grid-row:1 / span 2}
  }
</style>
