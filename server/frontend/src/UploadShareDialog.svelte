<script>
  import { onMount } from 'svelte';
  import BackupFolderPicker from './BackupFolderPicker.svelte';

  let { initialFolder, sources, t, close, created, copy } = $props();
  let dialog;
  let destination = $state(null);
  let days = $state(7), maxMiB = $state(64), maxFiles = $state(10);
  let conflict = $state('rename');
  let error = $state(''), busy = $state(false), url = $state('');

  onMount(() => {
    destination = initialFolder?.source ? initialFolder : null;
    dialog.showModal();
  });

  async function createLink(event) {
    event.preventDefault();
    if (!destination || busy) return;
    error = '';
    busy = true;
    try {
      const response = await fetch('/api/share/create-upload', {
        method: 'POST',
        body: new URLSearchParams({...destination, days, maxMiB, maxFiles, conflict}),
      });
      if (!response.ok) {
        error = response.status === 413 ? 'uploadShareServerLimit' : response.status === 404 ? 'folderUnavailable' : 'uploadShareCreateError';
        return;
      }
      url = location.origin + (await response.json()).url;
      created();
    } catch {
      error = 'uploadShareCreateError';
    } finally {
      busy = false;
    }
  }
</script>

<dialog bind:this={dialog} onclose={close} aria-labelledby="upload-share-title" oncancel={event => { if (busy) event.preventDefault(); }}>
  <header>
    <h2 id="upload-share-title">{t('uploadShareTitle')}</h2>
    <button type="button" class="ghost" aria-label={t('close')} disabled={busy} onclick={() => dialog.close()}>×</button>
  </header>
  {#if url}
    <p role="status">{t('uploadShareCreated')}</p>
    <label>{t('copyLink')} <input type="url" value={url} readonly onclick={(event) => event.currentTarget.select()} /></label>
    <footer><button class="primary" type="button" onclick={() => copy(url)}>{t('copyLink')}</button></footer>
  {:else}
    <form onsubmit={createLink}>
      <p>{t('uploadShareGuestRights')}</p>
      <fieldset disabled={busy}>
        <legend>{t('uploadShareDestination')}</legend>
        {#if destination}<p class="destination">{destination.source} / {destination.path || t('rootLabel')}</p>{/if}
        <BackupFolderPicker label={t('chooseFolder')} {sources} {t} choose={(folder) => destination = folder} />
      </fieldset>
      <fieldset disabled={busy} class="limits">
        <legend>{t('uploadShareRestrictions')}</legend>
        <label>{t('uploadShareDays')} <input type="number" min="1" max="365" step="1" required bind:value={days} /></label>
        <label>{t('uploadShareMaxMiB')} <input type="number" min="1" step="1" required bind:value={maxMiB} /></label>
        <label>{t('uploadShareMaxFiles')} <input type="number" min="1" max="100" step="1" required bind:value={maxFiles} /></label>
        <label>{t('uploadShareConflict')}
          <select bind:value={conflict}>
            <option value="rename">{t('uploadShareKeepBoth')}</option>
            <option value="reject">{t('uploadShareReject')}</option>
          </select>
        </label>
      </fieldset>
      {#if error}<p role="alert">{t(error)}</p>{/if}
      <footer><button class="primary" type="submit" disabled={busy || !destination}>{t(busy ? 'uploadShareCreating' : 'uploadShareCreate')}</button></footer>
    </form>
  {/if}
</dialog>

<style>
  dialog { box-sizing:border-box; width:min(540px,calc(100vw - 28px)); max-height:calc(100dvh - 32px); overflow:auto;
    padding:20px; border:1px solid var(--border); border-radius:18px; background:var(--panel); color:var(--text); }
  dialog::backdrop { background:rgba(10,16,36,.4); }
  header, footer { display:flex; align-items:center; flex-wrap:wrap; gap:10px; }
  header { justify-content:space-between; margin-bottom:12px; }
  footer { margin-top:12px; }
  h2 { margin:0; font-size:19px; }
  p { margin:0 0 6px; color:var(--muted); font-size:14px; }
  p.destination { color:var(--text); overflow-wrap:anywhere; }
  form { display:grid; gap:10px; }
  fieldset { min-width:0; margin:0; padding:10px; border:1px solid var(--border); border-radius:10px; }
  legend, label { font-size:14px; }
  .limits { display:grid; grid-template-columns:repeat(auto-fit,minmax(174px,1fr)); gap:10px; }
  label { display:grid; gap:4px; }
  input, select { width:100%; min-width:0; padding:6px 8px; border:1px solid var(--border); border-radius:8px; background:var(--panel2); color:var(--text); font:inherit; }
  [role=alert] { color:var(--red); }
</style>
