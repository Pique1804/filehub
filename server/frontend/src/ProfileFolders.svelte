<script>
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';

  let { t, profile = null, invitation = false, onback, onsaved } = $props();
  const url = $derived(invitation ? '/api/admin/invitations' : '/api/admin/user/folders?username=' + encodeURIComponent(profile.username));
  let inviteRole = $state('user');
  let inviteLabel = $state('');
  let restricted = $state(true);
  let folders = $state([]);
  let personal = $state(null);
  const bytesPerGiB = 1024 ** 3;
  let quotaGiB = $state(undefined);
  let sources = $state([]);
  let source = $state('');
  let path = $state('');
  let children = $state([]);
  let picking = $state('');
  let loading = $state(true);
  let listing = $state(false);
  let saving = $state(false);
  let ready = $state(false);
  let error = $state('');
  let newName = $state('');
  let creating = $state(false);
  const lifetime = new AbortController();
  let navigation;

  async function fetchJSON(address, options = {}) {
    const response = await fetch(address, { signal: lifetime.signal, ...options });
    if (!response.ok) throw new Error((await response.text()).trim());
    return response.json();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const initial = invitation ? { editable: true, restricted: true, folders: [], personal: null } : fetchJSON(url);
      const [policy, disks] = await Promise.all([initial, fetchJSON('/api/sources')]);
      if (!policy.editable) return;
      restricted = policy.restricted;
      folders = policy.folders;
      personal = policy.personal;
      quotaGiB = policy.quotaBytes ? policy.quotaBytes / bytesPerGiB : undefined;
      sources = disks.sources;
      ready = true;
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('profileFoldersLoadError');
    } finally { loading = false; }
  }

  async function browse(disk, directory) {
    navigation?.abort();
    const request = new AbortController();
    navigation = request;
    source = disk;
    path = directory;
    children = [];
    listing = true;
    error = '';
    try {
      const query = new URLSearchParams({ source, path });
      const result = await fetchJSON('/api/list?' + query, { signal: request.signal });
      if (!request.signal.aborted) children = result.entries.filter(entry => entry.isDir);
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('profileFoldersLoadError');
    } finally {
      if (navigation === request) listing = false;
    }
  }

  function choose(kind) {
    const current = kind === 'personal' ? personal : null;
    picking = kind;
    creating = false;
    void browse(current?.source || sources[0]?.name || '', current?.path || '');
  }

  function selectFolder() {
    const folder = { source, path };
    if (picking === 'personal') personal = folder;
    else if (!folders.some(item => item.source === source && item.path === path)) folders = [...folders, folder];
    picking = '';
  }

  async function createFolder(event) {
    event.preventDefault();
    const name = newName.trim();
    if (!name) return;
    const query = new URLSearchParams({ source, path, name });
    saving = true;
    error = '';
    try {
      await fetchJSON('/api/mkdir?' + query, { method: 'POST' });
      creating = false;
      await browse(source, path ? path + '/' + name : name);
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('profileFolderCreateError');
    } finally { saving = false; }
  }

  async function save() {
    const quotaBytes = restricted && personal ? (quotaGiB ?? 0) * bytesPerGiB : 0;
    if (!Number.isSafeInteger(quotaBytes) || quotaBytes < 0) {
      error = t('profileQuotaInvalid');
      return;
    }
    saving = true;
    error = '';
    try {
      const response = await fetch(url, {
        method: 'POST', signal: lifetime.signal, headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(invitation ? { role: inviteRole, label: inviteLabel, folders, personal, quotaBytes }
          : { restricted, folders: restricted ? folders : [], personal: restricted ? personal : null, quotaBytes }),
      });
      if (!response.ok) {
        error = response.status === 409 ? t('profilePersonalConflict') : t('profilesSaveError');
        return;
      }
      onsaved(invitation ? await response.json() : undefined);
    } catch (failure) {
      if (failure.name !== 'AbortError') error = t('profilesSaveError');
    } finally { saving = false; }
  }

  onMount(() => {
    void load();
    return () => { lifetime.abort(); navigation?.abort(); };
  });
</script>

<section class="folder-policy" aria-labelledby="folder-policy-title">
  <button type="button" onclick={onback} disabled={saving}><Icon name="back" size={17}/>{t('back')}</button>
  <h3 id="folder-policy-title">{invitation ? t('inviteNew') : t('profileFoldersTitle', { name: profile.username })}</h3>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if loading}<p role="status">{t('loading')}</p>
  {:else if !ready}<button type="button" onclick={load}>{t('retry')}</button>
  {:else}
    {#if invitation}
      <label for="invite-label">{t('inviteLabel')}</label>
      <input id="invite-label" type="text" maxlength="80" bind:value={inviteLabel} disabled={saving}/>
      <label for="invite-role">{t('role')}</label>
      <select id="invite-role" bind:value={inviteRole} disabled={saving}>
        <option value="user">{t('user')}</option>
        <option value="viewer">{t('readOnly')}</option>
      </select>
    {:else}
    <label class="mode">
      <input type="checkbox" bind:checked={restricted} onchange={() => picking = ''} />
      <span>{t('profileRestricted')}</span>
    </label>
    {/if}
    {#if restricted}
      <div class="group">
        <h4>{t('profilePersonalFolder')}</h4>
        <p>{t((invitation ? inviteRole : profile.role) === 'viewer' ? 'profilePersonalViewerHint' : 'profilePersonalHint')}</p>
        {#if personal}
          <div class="selected"><strong>{personal.source} / {personal.path}</strong><button type="button" disabled={saving} onclick={() => { personal = null; quotaGiB = undefined; }}>{t('remove')}</button></div>
          <label for="profile-quota">{t('profileQuotaLimit')}</label>
          <input id="profile-quota" type="number" min="0" step="any" inputmode="decimal" bind:value={quotaGiB}
            aria-describedby="profile-quota-hint" disabled={saving}/>
          <p id="profile-quota-hint">{t('profileQuotaHint')}</p>
        {/if}
        <button type="button" disabled={!sources.length || saving} onclick={() => choose('personal')}>{t('profileChoosePersonal')}</button>
      </div>
      <div class="group">
        <h4>{t('profileReadFolders')}</h4>
        <p>{t('profileReadFoldersHint')}</p>
        {#each folders as folder (JSON.stringify(folder))}
          <div class="selected"><strong>{folder.source} / {folder.path || t('rootLabel')}</strong>
            <button type="button" disabled={saving} aria-label={t('profileRemoveFolder', { path: folder.source + ' / ' + folder.path })}
              onclick={() => folders = folders.filter(item => item !== folder)}>{t('remove')}</button>
          </div>
        {/each}
        <button type="button" disabled={!sources.length || folders.length >= 64 || saving} onclick={() => choose('read')}>
          <Icon name="add" size={17}/>{t('profileAddReadFolder')}
        </button>
      </div>
      {#if picking}
        <section class="picker" aria-labelledby="profile-folder-picker-title">
          <h4 id="profile-folder-picker-title">{t(picking === 'personal' ? 'profileChoosePersonal' : 'profileAddReadFolder')}</h4>
          <label for="profile-pick-source">{t('storage')}</label>
          <select id="profile-pick-source" value={source} disabled={saving} onchange={event => browse(event.currentTarget.value, '')}>
            {#each sources as disk (disk.name)}<option value={disk.name}>{disk.name}</option>{/each}
          </select>
          <p class="location">{source} / {path}</p>
          <div class="actions">
            {#if path}<button type="button" disabled={saving} onclick={() => browse(source, path.split('/').slice(0, -1).join('/'))}>{t('profileParentFolder')}</button>{/if}
            {#if picking === 'personal' && !creating}
              <button type="button" disabled={listing || saving} onclick={() => { creating = true; newName = profile?.username || ''; }}>{t('newFolder')}</button>
            {/if}
          </div>
          {#if creating}
            <form class="actions" onsubmit={createFolder}>
              <label for="profile-new-folder">{t('newFolderName')}</label>
              <input id="profile-new-folder" type="text" maxlength="120" required bind:value={newName} disabled={saving}/>
              <button disabled={saving || !newName.trim()}>{t('create')}</button>
            </form>
          {/if}
          {#if listing}<p role="status">{t('loading')}</p>{/if}
          <ul class="children">
            {#each children as child (child.name)}
              <li><button type="button" disabled={saving} onclick={() => browse(source, path ? path + '/' + child.name : child.name)}>
                <Icon name="folder" size={18}/>{child.name}
              </button></li>
            {/each}
          </ul>
          <div class="actions">
            <button type="button" class="primary" disabled={listing || saving || !source || (picking === 'personal' && !path)}
              onclick={selectFolder}>{t('profileSelectCurrentFolder')}</button>
            <button type="button" disabled={saving} onclick={() => picking = ''}>{t('cancel')}</button>
          </div>
        </section>
      {/if}
    {:else}
      <p>{t('profilesSharedAccess')}</p>
    {/if}
    <div class="actions">
      <button class="primary" type="button" disabled={saving || !!picking || (invitation && !personal && !folders.length)} onclick={save}>{t(invitation ? 'inviteCreateLink' : 'save')}</button>
      <button type="button" disabled={saving} onclick={onback}>{t('cancel')}</button>
    </div>
  {/if}
</section>

<style>
  .folder-policy, .group, .picker { display: grid; gap: 10px; justify-items: start; }
  .folder-policy { padding-block: 12px 4px; }
  .group, .picker { width: 100%; border: 1px solid var(--border); border-radius: 12px; padding: 12px; }
  .picker { background: var(--panel2); }
  h3, h4, p { margin: 0; overflow-wrap: anywhere; }
  h3 { font-size: 18px; }
  p, .location { color: var(--muted); }
  .error { color: var(--err); }
  .mode, .actions, .selected { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
  .mode { min-height: 44px; padding: 6px 0; }
  .mode input { width: 18px; height: 18px; accent-color: var(--accent); }
  .selected { width: 100%; justify-content: space-between; overflow-wrap: anywhere; }
  button, select, input[type="text"] { min-height: 44px; padding: 8px 11px; border: 1px solid var(--border); border-radius: 8px; background: var(--panel); color: var(--text); max-width: 100%; }
  button { display: inline-flex; align-items: center; gap: 7px; cursor: pointer; text-align: start; overflow-wrap: anywhere; }
  button:hover { background: var(--row); }
  .primary { color: white; background: var(--accent); border-color: var(--accent); }
  .primary:hover { background: var(--accent); }
  .children { width: 100%; max-height: 240px; overflow: auto; list-style: none; padding: 4px; margin: 0; }
  .children button { width: 100%; margin-block: 2px; }
</style>
