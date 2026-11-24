<script>
  import { onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';
  import ProfileFolders from './ProfileFolders.svelte';

  let { t, onback } = $props();
  let editing = $state(false);
  let loading = $state(true);
  let error = $state('');
  let items = $state([]);
  let created = $state(null);
  let linkInput = $state();
  let copied = $state(false);
  let revoking = $state('');
  const lifetime = new AbortController();
  const endpoint = '/api/admin/invitations';

  async function load() {
    loading = true;
    try {
      const response = await fetch(endpoint, { signal: lifetime.signal });
      if (!response.ok) throw new Error();
      items = (await response.json()).invitations;
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('inviteLoadError');
    } finally { loading = false; }
  }

  async function showInvitation(invite) {
    const url = new URL(location.pathname, location.origin);
    url.hash = new URLSearchParams({ invite: invite.token }).toString();
    created = { url: url.href, expires: invite.expires };
    editing = false;
    copied = false;
    await tick();
    linkInput?.focus();
    linkInput?.select();
    void load();
  }

  async function copy() {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.url);
      copied = true;
    } catch {
      linkInput?.focus();
      linkInput?.select();
      copied = document.execCommand('copy');
    }
  }

  async function revoke(item) {
    if (revoking || !confirm(t('inviteRevokeConfirm'))) return;
    revoking = item.id;
    error = '';
    try {
      const response = await fetch(endpoint + '/revoke?' + new URLSearchParams({ id: item.id }),
        { method: 'POST', signal: lifetime.signal });
      if (!response.ok) throw new Error();
      created = null;
      await load();
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('inviteRevokeFailed');
    } finally { revoking = ''; }
  }

  function expiresAt(expires) {
    return new Date(expires * 1000).toLocaleString(document.documentElement.lang, { dateStyle: 'short', timeStyle: 'short' });
  }

  onMount(() => {
    void load();
    return () => lifetime.abort();
  });
</script>

{#if editing}
  <ProfileFolders {t} invitation onback={() => editing = false} onsaved={showInvitation} />
{:else}
  <section aria-label={t('invitationsTitle')}>
    <div class="actions">
      <button type="button" onclick={onback}><Icon name="back" size={17}/>{t('back')}</button>
      <button class="primary" type="button" onclick={() => { editing = true; created = null; error = ''; }}>
        <Icon name="add" size={17}/>{t('inviteNew')}
      </button>
    </div>
    {#if created}
      <div class="link-block">
        <label for="fh-invitation-link">{t('inviteLink')}</label>
        <input id="fh-invitation-link" bind:this={linkInput} readonly value={created.url} />
        <div class="actions">
          <button type="button" onclick={copy}>{t('copyLink')}</button>
          {#if copied}<span role="status">{t('copied')}</span>{/if}
        </div>
        <p>{t('inviteExpiryHint', { date: expiresAt(created.expires) })}</p>
      </div>
    {/if}
    <h3>{t('invitationsTitle')}</h3>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if loading}<p role="status">{t('loading')}</p>
    {:else if !items.length && !error}<p>{t('noInvitations')}</p>
    {/if}
    <ul>
      {#each items as item (item.id)}
        <li>
          <div>
            <strong>{item.label || t(item.role === 'viewer' ? 'readOnly' : 'user')}</strong>
            <p>{t('inviteExpiryHint', { date: expiresAt(item.expires) })}</p>
          </div>
          <button class="danger" type="button" disabled={!!revoking} onclick={() => revoke(item)}>{t('inviteRevoke')}</button>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  section, .link-block { display: grid; gap: 12px; }
  section { margin-block: 12px; }
  h3, p, ul { margin: 0; }
  h3 { font-size: 16px; }
  p { color: var(--muted); }
  .actions, li { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .link-block, li { padding: 12px; border: 1px solid var(--border); border-radius: 10px; }
  .link-block { background: var(--panel2); }
  ul { display: grid; gap: 8px; padding: 0; list-style: none; }
  li { justify-content: space-between; overflow-wrap: anywhere; }
  button, input { min-height: 44px; padding: 8px 11px; border: 1px solid var(--border); border-radius: 8px; color: var(--text); background: var(--panel); min-width: 0; }
  button { cursor: pointer; display: inline-flex; align-items: center; gap: 6px; }
  .primary { color: #fff; background: var(--accent); border-color: var(--accent); }
  .danger, .error { color: var(--err); }
</style>
