<script>
  let {t} = $props();
  let value = $state(null), busy = $state(false), error = $state(false);
  const endpoint = '/api/admin/photos/visual-search';

  async function refresh(signal) {
    try {
      const response = await fetch(endpoint, {signal});
      if (!response.ok) throw new Error();
      const next = await response.json();
      if (!signal?.aborted) {
        value = next;
        error = false;
      }
    } catch (failure) {
      if (failure.name !== 'AbortError') error = true;
    }
  }

  $effect(() => {
    const controller = new AbortController();
    void refresh(controller.signal);
    const timer = setInterval(() => { if (!busy) void refresh(controller.signal); }, 15000);
    return () => {controller.abort();clearInterval(timer);};
  });

  async function toggle() {
    if (!value?.configured || busy) return;
    busy = true;
    try {
      const response = await fetch(endpoint, {method:'POST', headers:{'Content-Type':'application/json'},
        body:JSON.stringify({enabled:!value.enabled})});
      if (!response.ok) throw new Error();
      await refresh();
    } catch {
      error = true;
    } finally {
      busy = false;
    }
  }
</script>

<div class="visual-settings">
  <h2>{t('visualSearchTitle')}</h2>
  {#if value}
    <label>
      <span>{t('visualSearchEnable')}</span>
      <input type="checkbox" checked={value.enabled} disabled={busy || !value.configured} onchange={toggle}>
    </label>
    {#if !value.configured}
      <p>{t('visualSearchConfigure')} <code>FILEHUB_CLIP_SIDECAR</code></p>
    {:else if value.enabled}
      <p role="status">{t(!value.available ? 'visualSearchOffline' : value.indexing ? 'visualSearchIndexing' : 'visualSearchReady')}</p>
      <p aria-live="polite">{t('visualSearchProgress', {n:value.indexed, total:value.total})}</p>
      {#if value.total > 0}<progress value={value.indexed} max={value.total} aria-label={t('visualSearchIndexing')}></progress>{/if}
    {/if}
  {/if}
  {#if error}<p role="alert">{t('visualSearchSettingsError')}</p>{/if}
</div>

<style>
  .visual-settings{padding:16px 20px}.visual-settings label{display:flex;gap:12px;align-items:center;justify-content:space-between}.visual-settings p{color:var(--muted);margin:8px 0}.visual-settings progress{width:min(100%,24rem);accent-color:var(--accent)}
</style>
