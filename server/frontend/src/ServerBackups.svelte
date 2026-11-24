<script>
  import { onMount } from 'svelte';
  import BackupFolderPicker from './BackupFolderPicker.svelte';

  let { sources, close, locale, t } = $props();
  const endpoint = '/api/admin/backup';
  const days = [1,2,3,4,5,6,0];
  const weekday = $derived(new Intl.DateTimeFormat(locale, {weekday:'short',timeZone:'UTC'}));
  function scheduleDate(timestamp) {
    // Show the wall clock recorded by the server, even when the browser is in a different time zone.
    const wallClock = timestamp.slice(0, 19);
    const date = new Date(wallClock + 'Z');
    return Number.isNaN(date.getTime()) ? wallClock : date.toLocaleString(locale, {
      dateStyle:'medium', timeStyle:'short', timeZone:'UTC',
    });
  }
  let dialog, view = $state(null), form = $state(null);
  let busy = $state(false), error = $state(''), notice = $state('');
  let revision = 0;
  let dirty = $derived(form && view && JSON.stringify(form) !== JSON.stringify(view.settings));

  onMount(() => {
    const controller = new AbortController();
    dialog.showModal();
    async function refresh() {
      if (busy) return;
      const requestedAt = revision;
      try {
        const data = await request('/settings', { signal:controller.signal });
        if (busy || requestedAt !== revision || controller.signal.aborted) return;
        view = data;
        if (!form) form = structuredClone(data.settings);
      } catch (e) {
        if (!controller.signal.aborted && !form) error = e.message;
      }
    }
    void refresh();
    const timer = setInterval(refresh, 3500);
    return () => {
      clearInterval(timer);
      controller.abort();
    };
  });

  async function request(path, options) {
    const response = await fetch(endpoint + path, options);
    if (!response.ok) throw new Error((await response.text()).trim() || t('backupRequestFailed'));
    return response.json();
  }

  async function save(event) {
    event.preventDefault();
    busy = true;
    revision++;
    error = '';
    notice = '';
    try {
      const data = await request('/settings', {
        method:'POST', headers:{ 'Content-Type':'application/json' }, body:JSON.stringify(form),
      });
      view = data;
      form = structuredClone(data.settings);
      notice = 'backupSaved';
    } catch (e) { error = e.message; }
    finally { busy = false; }
  }

  async function run() {
    busy = true;
    revision++;
    error = '';
    notice = '';
    try {
      view = await request('/run', { method:'POST' });
    } catch (e) { error = e.message; }
    finally { busy = false; }
  }

  function addFolder(folder) {
    if (!form.folders.some(existing => existing.source === folder.source && existing.path === folder.path)) {
      form.folders = [...form.folders, folder];
    }
  }

  function dayChanged(day, event) {
    form.days = event.currentTarget.checked ? [...form.days, day] : form.days.filter(value => value !== day);
  }
</script>

<dialog bind:this={dialog} onclose={close} aria-labelledby="server-backups-title">
  <header>
    <h2 id="server-backups-title">{t('backup')}</h2>
    <button type="button" aria-label={t('close')} onclick={() => dialog.close()}>×</button>
  </header>
  {#if form}
    <form onsubmit={save}>
      <label class="enabled"><input type="checkbox" bind:checked={form.enabled}> {t('backupScheduled')}</label>
      {#if view.legacyDatabaseSnapshots}
        <p>{t('backupLegacy')}</p>
      {/if}
      <section aria-label={t('backupContents')}>
        <h3>{t('backupFolders')}</h3>
        {#if form.folders.length}
          <ul>
            {#each form.folders as folder, index (folder.source + ':' + folder.path)}
              <li><span>{folder.source}:/{folder.path}</span>
                <button type="button" onclick={() => form.folders = form.folders.filter((_, i) => i !== index)}>{t('remove')}</button>
              </li>
            {/each}
          </ul>
        {/if}
        <BackupFolderPicker label={t('backupAddFolder')} {sources} {t} choose={addFolder}/>
        <p>{t('backupExtras')}</p>
      </section>
      <section aria-label={t('backupDestination')}>
        <h3>{t('backupWhere')}</h3>
        {#if form.destination.source}
          <p>{form.destination.source}:/{form.destination.path}</p>
        {/if}
        <BackupFolderPicker label={t('backupChooseDestination')} {sources} {t} choose={(folder) => form.destination = folder}/>
        {#if view.sameVolume && !dirty}<p role="status">{t('backupSameVolume')}</p>{/if}
      </section>
      <section aria-label={t('backupSchedule')}>
        <h3>{t('backupWhen')}</h3>
        <div class="days">
          {#each days as value, index}
            <label><input type="checkbox" checked={form.days.includes(value)} onchange={(event) => dayChanged(value, event)}> {weekday.format(Date.UTC(2024,0,1 + index))}</label>
          {/each}
        </div>
        <label class="clock">{t('backupTime')} <input type="time" bind:value={form.time}> <span>{view.timeZone}</span></label>
        {#if view.nextRun && !dirty}
          <p>{t('backupNextRun', {date:scheduleDate(view.nextRun),zone:view.timeZone})}</p>
        {/if}
      </section>
      {#if view.lastRun}
        {#if view.lastRun.state !== 'idle' && !view.lastRunMatchesSettings}
          <p>{t('backupPreviousSettings')}</p>
        {/if}
        <p role="status">{t('backupState.' + view.lastRun.state)}{#if view.lastRun.completed} · {new Date(view.lastRun.completed * 1000).toLocaleString(locale)}{/if}</p>
        {#if view.lastRun.message}<p>{view.lastRun.message}</p>{/if}
      {/if}
      {#if notice}<p role="status">{t(notice)}</p>{/if}
      <footer>
        <button type="submit" class="primary" disabled={busy || (!dirty && !view.legacyDatabaseSnapshots)}>{t('save')}</button>
        {#if view.settings.enabled}
          <button type="button" disabled={busy || dirty || view.lastRun.state === 'running'} onclick={run}>{t('backupRunNow')}</button>
        {/if}
      </footer>
    </form>
  {:else if !error}<p role="status">{t('backupLoading')}</p>{/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</dialog>

<style>
  dialog { box-sizing:border-box; width:min(590px,calc(100vw - 28px)); max-height:calc(100dvh - 32px); overflow:auto;
    padding:20px; border:1px solid var(--border); border-radius:18px; background:var(--panel); color:var(--text); }
  dialog::backdrop { background:rgba(10,16,36,.4); }
  header, footer { display:flex; align-items:center; flex-wrap:wrap; gap:10px; }
  header { justify-content:space-between; margin-bottom:12px; }
  h2, h3, p { margin:0; }
  h2 { font-size:19px; }
  h3 { font-size:14px; }
  form, section { display:grid; gap:9px; }
  section { padding-top:10px; border-top:1px solid var(--divider); }
  p { color:var(--muted); font-size:13px; }
  .enabled, .days, .clock { font-size:14px; }
  .days, .clock { display:flex; gap:10px; flex-wrap:wrap; align-items:center; }
  input[type="time"] { padding:5px; background:var(--panel2); color:var(--text); border:1px solid var(--border); border-radius:6px; }
  ul { display:grid; gap:4px; padding:0; margin:0; list-style:none; }
  li { display:flex; align-items:center; justify-content:space-between; gap:8px; padding:4px 7px; background:var(--panel2); border-radius:7px; font-size:13px; }
  li span { overflow-wrap:anywhere; min-width:0; }
  button { background:var(--panel2); color:var(--text); border:1px solid var(--border); border-radius:8px; padding:6px 10px; cursor:pointer; }
  .primary { background:var(--accent); color:white; border-color:var(--accent); }
  button:disabled { opacity:.55; cursor:default; }
  .error { color:var(--err); margin-top:9px; }
</style>
