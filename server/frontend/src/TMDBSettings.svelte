<script>
  import { onMount } from 'svelte';

  const endpoint = '/api/admin/video/tmdb/settings';
  let { t } = $props();
  let credential = $state('');
  let status = $state(null);
  let busy = $state(false);
  let error = $state('');

  onMount(() => {
    const controller = new AbortController();
    fetch(endpoint, {signal:controller.signal}).then(response => {
      if (!response.ok) throw new Error();
      return response.json();
    }).then(result => {
      status = result;
    }).catch(failure => {
      if (failure.name !== 'AbortError') error = 'tmdbSettingsLoadError';
    });
    return () => controller.abort();
  });

  async function update(action) {
    if (busy || (action === 'save' && !credential.trim())) return;
    busy = true;
    error = '';
    const body = new URLSearchParams({action, credential:action === 'save' ? credential.trim() : ''});
    credential = '';
    try {
      const response = await fetch(endpoint, {method:'POST', body});
      if (!response.ok) {
        if (response.status === 400 || response.status === 422) error = 'tmdbSettingsInvalid';
        else if (response.status === 502) error = 'tmdbSettingsUnavailable';
        else error = 'tmdbSettingsError';
        return;
      }
      status = await response.json();
    } catch {
      error = 'tmdbSettingsError';
    } finally {
      busy = false;
    }
  }
</script>

<div class="tmdb-settings">
  <h2>{t('tmdbSettingsTitle')}</h2>
  {#if status}<p class="state" role="status">{t(status.configured ? 'tmdbSettingsConnected' : 'tmdbSettingsDisconnected')}</p>{/if}
  <form onsubmit={event => {event.preventDefault(); void update('save');}}>
    <label>
      <span>{t('tmdbSettingsCredential')}</span>
      <input type="password" autocomplete="new-password" spellcheck="false" autocapitalize="none"
        maxlength="2048" required bind:value={credential} disabled={busy}>
    </label>
    <button type="submit" class="save" disabled={busy || !credential.trim()}>{t(busy ? 'loading' : 'save')}</button>
  </form>
  {#if error}<p class="error" role="alert">{t(error)}</p>{/if}
  <div class="actions">
    <a href="https://developer.themoviedb.org/docs/authentication-application" target="_blank" rel="noreferrer">{t('tmdbSettingsGetKey')}</a>
    {#if status?.configured}
      <button type="button" disabled={busy} onclick={() => update('disable')}>{t('tmdbSettingsDisconnect')}</button>
    {/if}
  </div>
</div>

<style>
  .tmdb-settings{padding:16px 20px}
  h2{margin:0;font-size:15px;font-weight:700}
  .state,.error{margin:7px 0 10px;font-size:13px}
  .state{color:var(--muted)}
  .error{color:var(--err)}
  form,.actions{display:flex;align-items:flex-end;flex-wrap:wrap;gap:8px;margin-top:12px}
  label{display:grid;flex:1;min-width:min(200px,100%);gap:5px;font-size:13px;color:var(--text2)}
  input,button{min-height:44px;padding:7px 10px;border:1px solid var(--border);border-radius:9px;background:var(--panel2);color:var(--text);font:inherit}
  input{width:100%;min-width:0}
  button{cursor:pointer}
  button:disabled{cursor:default;opacity:.55}
  .save{background:var(--accent);border-color:var(--accent);color:#fff;font-weight:600}
  .actions{align-items:center;justify-content:space-between}
  .actions a{color:var(--accent-deep);font-size:13px}
  .actions button{background:var(--panel);font-size:13px}
</style>
