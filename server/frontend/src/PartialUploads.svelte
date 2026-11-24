<script>
  import { onMount } from 'svelte';

  let { t, format, locale, close } = $props();
  let dialog;
  let list = $state(null), selected = $state(''), busy = $state(false), error = $state('');
  const endpoint = '/api/admin/uploads';
  const relative = $derived(new Intl.RelativeTimeFormat(locale, {numeric:'always'}));

  function age(updated) {
    const hours = Math.floor(Math.max(0, Date.now() - Date.parse(updated)) / 3600000);
    if (hours === 0) return t('partialUploadRecent');
    return hours < 24 ? relative.format(-hours, 'hour') : relative.format(-Math.floor(hours / 24), 'day');
  }

  async function refresh(signal) {
    busy = true;
    error = '';
    try {
      const response = await fetch(endpoint, {signal});
      if (!response.ok) throw new Error();
      list = await response.json();
    } catch {
      if (!signal?.aborted) error = 'partialUploadLoadError';
    } finally {
      busy = false;
    }
  }

  async function cancel(id) {
    busy = true;
    error = '';
    try {
      const response = await fetch(endpoint + '/cancel?' + new URLSearchParams({id}), {method:'POST'});
      if (response.status === 409) {
        error = 'partialUploadBusy';
        return;
      }
      if (!response.ok && response.status !== 404) throw new Error();
      selected = '';
      await refresh();
    } catch {
      error = 'partialUploadCancelError';
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    const controller = new AbortController();
    dialog.showModal();
    void refresh(controller.signal);
    return () => controller.abort();
  });
</script>

<dialog bind:this={dialog} onclose={close} aria-labelledby="partial-uploads-title">
  <header>
    <h2 id="partial-uploads-title">{t('partialUploads')}</h2>
    <button type="button" aria-label={t('close')} onclick={() => dialog.close()}>×</button>
  </header>
  {#if list}
    <p>{t('partialUploadRetention', {days:list.retentionDays})}</p>
    <p>{t('partialUploadTotals', {count:format.number(list.count), bytes:format.size(list.bytes)})}</p>
    {#if list.items.length < list.count}
      <p>{t('partialUploadLimited', {count:format.number(list.items.length)})}</p>
    {/if}
    <ul>
      {#each list.items as item (item.id)}
        <li>
          <div><strong>{item.name}</strong>
            <small>{item.source}:/{item.folder} · {format.size(item.received)} · {age(item.updated)}</small>
          </div>
          {#if selected === item.id}
            <button type="button" disabled={busy} onclick={() => cancel(item.id)}>{t('partialUploadConfirm')}</button>
            <button type="button" disabled={busy} onclick={() => selected = ''}>{t('back')}</button>
          {:else}
            <button type="button" disabled={busy} onclick={() => selected = item.id}>{t('partialUploadCancel')}</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if busy}<p role="status">{t('loading')}</p>{/if}
  {#if error}<p role="alert">{t(error)}</p>{/if}
  <button type="button" disabled={busy} onclick={() => refresh()}>{t('partialUploadRefresh')}</button>
</dialog>

<style>
  dialog{width:min(680px,calc(100% - 24px));max-height:85vh;overflow:auto;padding:20px;border:1px solid var(--divider);border-radius:14px;background:var(--panel);color:var(--text)}
  dialog::backdrop{background:rgba(0,0,0,.55)}
  header,li{display:flex;align-items:center;justify-content:space-between;gap:8px}
  h2{margin:0}
  ul{display:grid;gap:8px;padding:0;list-style:none}
  li{border:1px solid var(--divider);border-radius:8px;padding:8px;flex-wrap:wrap}
  li>div{flex:1;min-width:160px;overflow-wrap:anywhere}
  small{display:block;color:var(--muted)}
  button{font:inherit;padding:6px 10px;border:1px solid var(--divider);border-radius:7px;background:var(--panel2);color:var(--text);cursor:pointer}
</style>
