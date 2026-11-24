<script>
  let { label, sources, choose, t } = $props();
  let open = $state(false);
  let source = $state('');
  let path = $state('');
  let folders = $state([]);
  let busy = $state(false);
  let error = $state('');
  let request = 0;

  async function browse(nextSource, nextPath = '') {
    const id = ++request;
    source = nextSource;
    path = nextPath;
    folders = [];
    error = '';
    busy = !!source;
    if (!source) return;
    try {
      const query = new URLSearchParams({ source, path });
      const response = await fetch('/api/list?' + query);
      if (!response.ok) throw new Error(t('folderUnavailable'));
      const data = await response.json();
      if (request === id) folders = data.entries.filter(entry => entry.isDir);
    } catch (e) {
      if (request === id) error = e.message;
    } finally {
      if (request === id) busy = false;
    }
  }

  function toggle() {
    open = !open;
    if (open) void browse(sources[0]?.name || '');
    else request++;
  }

  function selectCurrent() {
    choose({ source, path });
    open = false;
  }
</script>

<button type="button" class="ghost" onclick={toggle} aria-expanded={open}>{label}</button>
{#if open}
  <fieldset>
    <legend>{label}</legend>
    <label>{t('storage')}
      <select value={source} onchange={(event) => browse(event.currentTarget.value)}>
        <option value="" disabled>{t('pickStorage')}</option>
        {#each sources as item (item.name)}
          <option value={item.name}>{item.name}</option>
        {/each}
      </select>
    </label>
    {#if source}
      <div class="current">
        <span>{source}:/{path}</span>
        {#if path}
          <button type="button" class="ghost" onclick={() => browse(source, path.split('/').slice(0, -1).join('/'))}>{t('parentFolder')}</button>
        {/if}
      </div>
      {#if busy}<p role="status">{t('foldersLoading')}</p>{/if}
      {#if error}<p role="alert">{error}</p>{/if}
      {#if folders.length}
        <ul aria-label={t('subfolders')}>
          {#each folders as folder (folder.name)}
            <li><button type="button" onclick={() => browse(source, path ? path + '/' + folder.name : folder.name)}>{folder.name}</button></li>
          {/each}
        </ul>
      {/if}
      <button type="button" class="ghost" disabled={busy || !!error} onclick={selectCurrent}>{t('chooseFolder')}</button>
    {/if}
  </fieldset>
{/if}

<style>
  fieldset { display:grid; gap:8px; margin:4px 0; padding:12px; min-width:0; border:1px solid var(--border); border-radius:10px; }
  legend, label { color:var(--text2); font-size:13px; }
  select { margin-left:8px; padding:5px; background:var(--panel); color:var(--text); border:1px solid var(--border); border-radius:6px; max-width:100%; }
  .current { display:flex; align-items:center; flex-wrap:wrap; gap:8px; overflow-wrap:anywhere; font-size:13px; }
  ul { list-style:none; margin:0; padding:0; max-height:140px; overflow:auto; }
  li button { width:100%; text-align:left; border:0; border-radius:6px; padding:7px; background:transparent; color:var(--text); overflow-wrap:anywhere; cursor:pointer; }
  li button:hover { background:var(--row); }
  .ghost { justify-self:start; padding:6px 10px; border:1px solid var(--border); border-radius:8px; background:var(--panel); color:var(--text2); cursor:pointer; font:inherit; font-size:13px; }
  button:disabled { opacity:.55; cursor:default; }
  p { margin:0; color:var(--muted); font-size:13px; }
</style>
