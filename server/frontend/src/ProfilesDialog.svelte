<script>
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import ProfileFolders from './ProfileFolders.svelte';
  import ProfileInvitations from './ProfileInvitations.svelte';

  let { t, currentUser, formatDate, post, onclose } = $props();

  const roles = [['user', 'user'], ['viewer', 'readOnly'], ['admin', 'administrator']];
  let dialog = $state();
  let profiles = $state([]);
  const adminCount = $derived(profiles.filter(profile => profile.role === 'admin').length);
  let loading = $state(true);
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let created = $state({ username: '', password: '', role: 'user', restricted: true });
  let passwordProfile = $state('');
  let folderProfile = $state(null);
  let invitationsOpen = $state(false);
  let password = $state('');
  let controller;

  async function load() {
    controller?.abort();
    const request = new AbortController();
    controller = request;
    loading = true;
    try {
      const response = await fetch('/api/admin/users', { signal: request.signal });
      if (!response.ok) throw new Error();
      profiles = (await response.json()).users;
      loaded = true;
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('profilesLoadError');
    } finally {
      if (controller === request) loading = false;
    }
  }

  function retry() {
    error = '';
    void load();
  }

  async function mutate(operation, success) {
    if (busy) return;
    busy = true;
    error = '';
    notice = '';
    try {
      await operation();
      success();
    } catch (failure) {
      error = failure.message || t('profilesSaveError');
    } finally {
      busy = false;
    }
  }

  function create(event) {
    event.preventDefault();
    const values = { ...created, username: created.username.trim(), restricted: created.restricted ? '1' : '' };
    if (!/^[A-Za-z0-9._-]{1,32}$/.test(values.username)) {
      error = t('profileUsernameHint');
      return;
    }
    void mutate(() => post('/api/admin/user/create', values), () => {
      created = { username: '', password: '', role: 'user', restricted: true };
      notice = t('profileCreated', { name: values.username });
      void load();
      if (values.restricted && values.role !== 'admin') folderProfile = { username: values.username, role: values.role };
    });
  }

  function changeRole(event, profile) {
    const role = event.currentTarget.value;
    event.currentTarget.value = profile.role;
    const query = new URLSearchParams({ username: profile.username, role });
    void mutate(() => post('/api/admin/user/role?' + query), () => {
      profiles = profiles.map(row => row.username === profile.username ? { ...row, role } : row);
      if (profile.username === currentUser) onclose(true);
    });
  }

  function remove(name) {
    if (!confirm(t('deleteUser', { name }))) return;
    const query = new URLSearchParams({ username: name });
    void mutate(() => post('/api/admin/user/delete?' + query), () => {
      profiles = profiles.filter(row => row.username !== name);
      if (passwordProfile === name) cancelPassword();
      if (name === currentUser) onclose(true);
    });
  }

  function choosePassword(name) {
    passwordProfile = name;
    password = '';
    error = '';
    notice = '';
  }

  function cancelPassword() {
    passwordProfile = '';
    password = '';
  }

  function savePassword(event) {
    event.preventDefault();
    const username = passwordProfile;
    void mutate(() => post('/api/admin/user/password', { username, password }), () => {
      cancelPassword();
      if (username === currentUser) onclose(true);
      else notice = t('passwordUpdated');
    });
  }

  onMount(() => {
    dialog.showModal();
    void load();
    return () => controller?.abort();
  });
</script>

<dialog bind:this={dialog} aria-labelledby="profiles-title"
  oncancel={event => { event.preventDefault(); onclose(); }}>
  <header>
    <h2 id="profiles-title">{t('profilesAndAccess')}</h2>
    <button class="close" type="button" aria-label={t('close')} onclick={() => onclose()}><Icon name="close"/></button>
  </header>

  {#if invitationsOpen}
    <ProfileInvitations {t} onback={() => { invitationsOpen = false; void load(); }} />
  {:else if folderProfile}
    <ProfileFolders {t} profile={folderProfile} onback={() => folderProfile = null} onsaved={() => {
      folderProfile = null;
      notice = t('profileFoldersSaved');
      void load();
    }} />
  {:else}
  <p class="description">{t('profilesAccessSummary')}</p>

  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="notice" role="status">{notice}</p>{/if}
  {#if loading}<p role="status">{t('loading')}</p>{/if}
  {#if !loaded && !loading}<button type="button" onclick={retry}>{t('retry')}</button>{/if}

  {#if loaded}
    <button type="button" class="primary invite-button" disabled={busy}
      onclick={() => { invitationsOpen = true; error = ''; notice = ''; }}>
      <Icon name="add" size={17}/>{t('inviteProfiles')}
    </button>
    {#if passwordProfile}
      <form class="profile-form" onsubmit={savePassword}>
        <h3>{t('profilePasswordTitle', { name: passwordProfile })}</h3>
        <label>
          {t('password')}
          <input type="password" required minlength="12" autocomplete="new-password" bind:value={password} />
        </label>
        <div class="form-actions">
          <button class="primary" disabled={busy}>{t('save')}</button>
          <button type="button" disabled={busy} onclick={cancelPassword}>{t('cancel')}</button>
        </div>
      </form>
    {:else}
      <form class="profile-form" onsubmit={create}>
        <h3>{t('profileCreate')}</h3>
        <div class="fields">
          <label>{t('username')}<input required maxlength="32" autocapitalize="none" spellcheck="false"
              autocomplete="off" bind:value={created.username} /></label>
          <label>{t('password')}<input type="password" required minlength="12" autocomplete="new-password"
              bind:value={created.password} /></label>
          <div class="field">
            <label for="profile-create-role">{t('role')}</label>
            <select id="profile-create-role" bind:value={created.role}>
              {#each roles as [value, label]}<option {value}>{t(label)}</option>{/each}
            </select>
          </div>
        </div>
        <p class="description">{t('profileUsernameHint')}</p>
        {#if created.role !== 'admin'}
          <label class="profile-mode"><input type="checkbox" bind:checked={created.restricted}/> {t('profileRestricted')}</label>
        {/if}
        <button class="primary" disabled={busy || loading}><Icon name="add" size={17}/>{t('profileCreate')}</button>
      </form>
    {/if}

    <ul class="profiles" aria-label={t('users')}>
      {#each profiles as profile (profile.username)}
        {@const lastAdmin = profile.role === 'admin' && adminCount === 1}
        <li>
          <div class="identity">
            <strong>{profile.username}</strong>
            <span>{lastAdmin ? t('profileLastAdmin') : t('userCreated', { date: formatDate(profile.created) })}{profile.username === currentUser ? ' ' + t('isYou') : ''}</span>
            {#if profile.role !== 'admin'}<span>{t(profile.restricted ? 'profileRestrictedStatus' : 'profileSharedStatus')}</span>{/if}
          </div>
          <div class="row-actions">
            <select value={profile.role} disabled={busy || loading || lastAdmin}
              aria-label={t('profileRole', { name: profile.username })} onchange={event => changeRole(event, profile)}>
              {#each roles as [value, label]}<option {value}>{t(label)}</option>{/each}
            </select>
            {#if profile.role !== 'admin'}
              <button type="button" disabled={busy} aria-label={t('profileFoldersTitle', { name: profile.username })}
                onclick={() => { folderProfile = profile; error = ''; notice = ''; }}>{t('profileFolders')}</button>
            {/if}
            <button type="button" disabled={busy} onclick={() => choosePassword(profile.username)}>{t('changePassword')}</button>
            <button class="danger" type="button" disabled={busy || lastAdmin}
              aria-label={t('profileDelete', { name: profile.username })} onclick={() => remove(profile.username)}>{t('delete')}</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
  {/if}
</dialog>

<style>
  dialog{width:min(720px,calc(100vw - 24px));max-height:calc(100dvh - 24px);padding:clamp(13px,3vw,22px);border:1px solid var(--border);border-radius:16px;color:var(--text);background:var(--panel);box-sizing:border-box}
  dialog::backdrop{background:#0c142689}
  header,.row-actions,.form-actions{display:flex;align-items:center;gap:8px}
  header{justify-content:space-between;margin-bottom:6px}
  h2,h3,p{margin:0}
  h2{font-size:clamp(19px,4vw,24px);line-height:1.2}
  h3{font-size:16px}
  button,input,select{min-height:42px;min-width:0;box-sizing:border-box;padding:6px 10px;border-radius:8px;border:1px solid var(--border);color:var(--text);background:var(--panel2);font:inherit}
  button{cursor:pointer}
  button:disabled{opacity:.55;cursor:default}
  button.close{display:grid;place-items:center;width:42px;flex:none}
  .description, li span{color:var(--muted);font-size:13px;line-height:1.5}
  .error,.notice{padding:8px 10px;margin-top:10px;border-radius:8px;background:var(--panel2)}
  .error,.danger{color:var(--err)}
  .notice{color:var(--accent-deep)}
  .profiles{padding:0;margin:14px 0;list-style:none;display:grid;gap:6px}
  li{display:flex;flex-wrap:wrap;gap:7px 12px;align-items:center;justify-content:space-between;padding:9px 10px;border:1px solid var(--border);border-radius:9px}
  .identity{display:grid;gap:2px;min-width:0;flex:1 1 125px;overflow-wrap:anywhere}
  .row-actions{flex-wrap:wrap;font-size:13px}
  .profile-form{display:grid;gap:8px;justify-items:start;margin-top:12px;padding-top:12px;border-top:1px solid var(--border)}
  .fields{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(140px,100%),1fr));gap:8px;width:100%}
  label,.field{display:grid;gap:4px;min-width:0;font-size:13px}
  .profile-mode{display:flex;align-items:center;gap:8px;min-height:44px;cursor:pointer}
  .profile-mode input{width:18px;min-height:18px;accent-color:var(--accent)}
  .invite-button{margin-top:12px}
  .primary{display:inline-flex;align-items:center;gap:5px;background:var(--accent);color:#fff;border-color:transparent;font-weight:650}
</style>
