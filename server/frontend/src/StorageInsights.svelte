<script>
  import { onMount } from 'svelte';

  let { sources, initial, t, format, close, openFolder } = $props();
  let dialog, duplicateRequest;
  let source = $state(''), folder = $state('');
  let usage = $state(null), usageBusy = $state(false), usageError = $state('');
  let matches = $state(null), matchBusy = $state(false), matchError = $state('');
  let acrossSources = $state(false), revision = $state(0);
  const parent = path => path.slice(0, Math.max(0, path.lastIndexOf('/')));

  onMount(() => {
    dialog.showModal();
    return () => duplicateRequest?.abort();
  });

  $effect(() => {
    if (!source && sources.length) {
      source = initial.source || sources[0].name;
      folder = initial.path || '';
    }
  });

  $effect(() => {
    revision;
    if (!source) return;
    const params = new URLSearchParams({source, path:folder, foldersOnly:'1'});
    const controller = new AbortController();
    usage = null; usageBusy = true; usageError = '';
    async function refresh() {
      try {
        const response = await fetch('/api/admin/storage/usage?' + params, {signal:controller.signal});
        if (response.status === 429) {
          if (!controller.signal.aborted) usageError = 'storageScanBusy';
          return;
        }
        if (!response.ok) throw new Error();
        const data = await response.json();
        if (!controller.signal.aborted) usage = data;
      } catch {
        if (!controller.signal.aborted) usageError = 'storageUsageError';
      } finally {
        if (!controller.signal.aborted) usageBusy = false;
      }
    }
    void refresh();
    return () => controller.abort();
  });

  function locationChanged(nextSource, nextFolder) {
    source = nextSource;
    folder = nextFolder;
    matches = null;
    matchError = '';
  }

  async function scanDuplicates() {
    const controller = new AbortController();
    duplicateRequest = controller;
    const params = acrossSources ? new URLSearchParams() : new URLSearchParams({source, path:folder});
    matches = null; matchBusy = true; matchError = '';
    try {
      const response = await fetch('/api/admin/storage/duplicates?' + params, {signal:controller.signal});
      if (response.status === 429) {
        if (!controller.signal.aborted) matchError = 'storageScanBusy';
        return;
      }
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (!controller.signal.aborted) matches = data;
    } catch {
      if (!controller.signal.aborted) matchError = 'storageDuplicateError';
    } finally {
      if (duplicateRequest === controller) {
        duplicateRequest = null;
        matchBusy = false;
      }
    }
  }
</script>

<dialog bind:this={dialog} onclose={close} aria-labelledby="storage-insights-title">
  <header>
    <h2 id="storage-insights-title">{t('storageUsage')}</h2>
    <button type="button" aria-label={t('close')} onclick={() => dialog.close()}>×</button>
  </header>

  <section aria-labelledby="storage-folders">
    <h3 id="storage-folders">{t('storageLargestFolders')}</h3>
    <div class="controls">
      <label>{t('storage')}
        <select value={source} disabled={matchBusy} onchange={event => locationChanged(event.currentTarget.value, '')}>
          {#each sources as disk (disk.name)}<option value={disk.name}>{disk.name}</option>{/each}
        </select>
      </label>
      {#if folder}<button type="button" disabled={matchBusy} onclick={() => locationChanged(source, parent(folder))}>{t('storageParent')}</button>{/if}
    </div>
    {#if folder}<p>{source} / {folder}</p>{/if}
    {#if usageBusy}<p role="status">{t('loading')}</p>{/if}
    {#if usageError}
      <p role="alert">{t(usageError)}</p>
      <button type="button" disabled={matchBusy} onclick={() => revision++}>{t('retry')}</button>
    {/if}
    {#if usage}
      {#if usage.folders.length}
        <ul class="folders">
          {#each usage.folders as item (item.path)}
            <li>
              <button type="button" disabled={matchBusy} onclick={() => locationChanged(source, item.path)}>{item.path.split('/').at(-1)}</button>
              <span>{format.plural('fileCount', item.files)} · <strong>{format.size(item.bytes)}</strong></span>
            </li>
          {/each}
        </ul>
      {:else}<p>{t('storageNoFolders')}</p>{/if}
      <p class="note">{t('storageIndexedSizes')}</p>
      {#if usage.folderPartial}<p role="status">{t('storageUsagePartial')}</p>{/if}
    {/if}
  </section>

  <section aria-labelledby="storage-duplicates">
    <h3 id="storage-duplicates">{t('storageDuplicates')}</h3>
    <div class="controls">
      <label class="checkbox"><input type="checkbox" bind:checked={acrossSources} disabled={matchBusy} onchange={() => {matches = null; matchError = '';}}> {t('storageAcrossSources')}</label>
      {#if matchBusy}
        <button type="button" onclick={() => duplicateRequest?.abort()}>{t('storageStopScan')}</button>
      {:else}
        <button type="button" class="accent" disabled={!source || usageBusy} onclick={scanDuplicates}>{t('storageStartScan')}</button>
      {/if}
    </div>
    {#if matchBusy}<p role="status">{t('storageScanningDuplicates')}</p>{/if}
    {#if matchError}<p role="alert">{t(matchError)}</p>{/if}
    {#if matches}
      <p role="status">{t('storageDuplicateSummary', {groups:format.number(matches.groupCount), savings:format.size(matches.savings)})}</p>
      {#if matches.partial}<p role="status">{t('storageDuplicatePartial')}</p>{/if}
      {#if matches.groupCount > matches.groups.length}<p>{t('storageEntriesShown', {shown:format.number(matches.groups.length), total:format.number(matches.groupCount)})}</p>{/if}
      <div class="duplicates">
        {#each matches.groups as group (group.sha256)}
          <article>
            <h4>{t('storageDuplicateGroup', {copies:format.number(group.copies), bytes:format.size(group.size), savings:format.size(group.savings)})}</h4>
            <ul>
              {#each group.paths as item (JSON.stringify([item.source,item.path]))}
                <li><button type="button" class="path" title={t('storageOpenFolder')} onclick={() => openFolder(item.source,parent(item.path))}>{item.source} / {item.path}</button></li>
              {/each}
            </ul>
            {#if group.pathCount > group.paths.length}<p>{t('storageEntriesShown', {shown:format.number(group.paths.length), total:format.number(group.pathCount)})}</p>{/if}
          </article>
        {/each}
      </div>
      {#if matches.groups.length}<p class="note">{t('storageSavingsNote')}</p>{/if}
    {/if}
  </section>
</dialog>

<style>
  dialog{width:min(760px,calc(100% - 24px));max-height:85vh;overflow:auto;padding:20px;border:1px solid var(--divider);border-radius:14px;background:var(--panel);color:var(--text)}
  dialog::backdrop{background:rgba(0,0,0,.55)}
  header,.controls,.folders li{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
  section{border-top:1px solid var(--divider);margin-top:16px;padding-top:12px}
  h2,h3,h4,p{margin:0;overflow-wrap:anywhere}
  h3{margin-bottom:12px}
  p,.duplicates{margin-top:8px}
  button,select{min-height:36px;padding:5px 10px;border:1px solid var(--divider);border-radius:7px;background:var(--panel2);color:var(--text);font:inherit}
  button{cursor:pointer}
  button:disabled{cursor:default;opacity:.65}
  label{display:flex;align-items:center;flex-wrap:wrap;gap:8px;min-width:0}
  select{max-width:100%}
  .checkbox{flex-wrap:nowrap}
  .accent{background:var(--accent);border-color:var(--accent);color:white}
  .note{color:var(--muted);font-size:13px}
  .duplicates{display:grid;gap:8px}
  article{border:1px solid var(--divider);border-radius:8px;padding:10px;min-width:0}
  ul{padding:0;list-style:none}
  .folders{display:grid;gap:6px}
  .folders button,.path{text-align:left;overflow-wrap:anywhere;max-width:100%}
  .path{width:100%;margin:2px 0}
</style>
