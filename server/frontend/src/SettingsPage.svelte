<script>
  import Icon from './Icon.svelte';
  import TMDBSettings from './TMDBSettings.svelte';
  import VisualSearchSettings from './VisualSearchSettings.svelte';

  let {
    t, currentUser, currentRole, language, onlanguagechange, onclose, section, sections, onsection,
    accountActions, serverActions,
    theme = $bindable(), view = $bindable(), clickMode = $bindable(), selectionMode = $bindable(),
  } = $props();

  const sectionDetails = {
    interface: { icon: 'settings', hint: 'settingsInterfaceHint' },
    files: { icon: 'folder', hint: 'settingsFilesHint' },
    account: { hint: 'settingsAccountHint' },
    server: { icon: 'storage', hint: 'settingsServerHint' },
  };
  const themeOptions = [
    ['system', 'system'],
    ['light', 'lightTheme'],
    ['dark', 'darkTheme'],
  ];
  const initial = $derived(Array.from(currentUser)[0]?.toUpperCase() || '');
  const roleLabel = $derived(currentRole === 'admin' ? 'administrator' : currentRole === 'viewer' ? 'readOnly' : 'user');
</script>

{#snippet actionList(actions)}
  <div class="actions">
    {#each actions as action (action.label)}
      <button type="button" class:danger={action.danger} onclick={action.run}>
        <span>{t(action.label)}</span>
        {#if !action.danger}<Icon name={action.icon || 'next'} size={17} />{/if}
      </button>
    {/each}
  </div>
{/snippet}

<div class="settings-page">
  <div class="settings-content">
    <header class="heading">
      <button class="back" type="button" aria-label={t('back')} onclick={onclose}><Icon name="back" size={20} /></button>
      <h1>{t('settings')}</h1>
    </header>

    <nav class="sections" aria-label={t('settings')}>
      {#each Object.entries(sections) as [item, label] (item)}
        {@const detail = sectionDetails[item]}
        <button type="button" aria-label={t(label)} aria-current={item === section ? 'page' : undefined} onclick={() => onsection(item)}>
          <span class="section-icon" aria-hidden="true">
            {#if detail.icon}<Icon name={detail.icon} size={20} />{:else}{initial}{/if}
          </span>
          <span class="section-copy">
            <strong>{t(label)}</strong>
            <small>{t(detail.hint)}</small>
          </span>
        </button>
      {/each}
    </nav>

    {#key section}
      <section class="settings-section" aria-label={t(sections[section])}>
        {#if section === 'interface'}
          <div class="surface">
            <fieldset class="theme-picker">
              <legend>{t('theme')}</legend>
              <div class="theme-options">
                {#each themeOptions as [value, label] (value)}
                  <label class="theme-option" class:selected={theme === value}>
                    <input type="radio" name="filehub-theme" {value} bind:group={theme} />
                    <span class="theme-preview" class:system={value === 'system'} class:dark={value === 'dark'} aria-hidden="true">
                      <span class="preview-sidebar"><i></i><i></i><i></i></span>
                      <span class="preview-content"><i></i><i></i><i></i></span>
                    </span>
                    <span class="theme-label"><span>{t(label)}</span><span class="radio-mark" aria-hidden="true"></span></span>
                  </label>
                {/each}
              </div>
            </fieldset>
            <div class="preference">
              <label for="settings-language">{t('language')}</label>
              <select id="settings-language" value={language} onchange={onlanguagechange}>
                <option value="system">{t('system')}</option>
                <option value="ru" lang="ru">Русский</option>
                <option value="en" lang="en">English</option>
              </select>
            </div>
          </div>
        {:else if section === 'files'}
          <div class="surface">
            <div class="preference">
              <label for="settings-file-view">{t('settingsFileView')}</label>
              <select id="settings-file-view" bind:value={view}>
                <option value="grid">{t('viewGrid')}</option>
                <option value="list">{t('viewList')}</option>
              </select>
            </div>
            <label class="preference toggle">
              <span>{t('openDoubleClick')}</span>
              <input type="checkbox" role="switch" checked={clickMode === 'double'}
                onchange={(event) => clickMode = event.currentTarget.checked ? 'double' : 'single'} />
            </label>
            <label class="preference toggle">
              <span>{t('selectionMode')}</span>
              <input type="checkbox" role="switch" bind:checked={selectionMode} />
            </label>
          </div>
        {:else if section === 'account'}
          <div class="surface">
            <div class="profile">
              <span class="avatar" aria-hidden="true">{initial}</span>
              <div><strong>{currentUser}</strong><span>{t(roleLabel)}</span></div>
            </div>
            {@render actionList(accountActions)}
          </div>
        {:else if section === 'server'}
          {#each serverActions as group (group.label)}
            <div class="surface">
              <h2>{t(group.label)}</h2>
              {@render actionList(group.actions)}
            </div>
          {/each}
          {#if currentRole === 'admin'}
            <div class="surface"><VisualSearchSettings {t}/></div>
            <div class="surface"><TMDBSettings {t}/></div>
          {/if}
        {/if}
      </section>
    {/key}
  </div>
</div>

<style>
  .settings-page {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 28px 32px 48px;
    scrollbar-width: thin;
  }
  .settings-content { max-width: 1120px; }
  .heading { display: flex; align-items: center; gap: 14px; margin-bottom: 24px; }
  h1 { margin: 0; font-size: 30px; font-weight: 750; line-height: 1.2; letter-spacing: -.9px; }
  h2 { margin: 0; padding: 16px 20px 12px; font-size: 15px; font-weight: 700; }
  button { cursor: pointer; }
  .back {
    display: grid;
    flex: none;
    width: 44px;
    height: 44px;
    place-items: center;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--panel);
    color: var(--text2);
  }
  .back:hover { background: var(--row); color: var(--text); }
  .sections { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; margin-bottom: 24px; }
  .sections button {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    min-width: 0;
    min-height: 84px;
    padding: 14px 12px;
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--panel);
    color: var(--text);
    text-align: start;
  }
  .sections button:hover { border-color: var(--accent); }
  .sections button[aria-current] {
    border-color: color-mix(in srgb, var(--accent) 45%, var(--border));
    background: var(--accent-pill);
    color: var(--accent-deep);
  }
  .section-icon, .avatar {
    display: grid;
    flex: none;
    place-items: center;
    background: var(--panel2);
    color: var(--text2);
    font-weight: 750;
  }
  .section-icon { width: 36px; height: 36px; border-radius: 10px; }
  .sections button[aria-current] .section-icon, .avatar { background: var(--accent-soft); color: var(--accent-deep); }
  .section-copy { min-width: 0; overflow-wrap: anywhere; }
  .section-copy strong { display: block; font-size: 14px; font-weight: 700; }
  .section-copy small { display: block; margin-top: 3px; font-size: 12px; line-height: 1.35; color: var(--muted); }
  .settings-section { display: grid; gap: 14px; }
  .surface {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 16px;
    background: var(--panel);
    box-shadow: var(--shadow);
  }
  .theme-picker { min-width: 0; margin: 0; padding: 0 20px 20px; border: 0; }
  .theme-picker legend { padding: 16px 0 12px; font-size: 15px; font-weight: 700; }
  .theme-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; max-width: 580px; }
  .theme-option {
    position: relative;
    display: grid;
    gap: 6px;
    min-width: 0;
    padding: 6px;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--panel);
    color: var(--text2);
    cursor: pointer;
  }
  .theme-option.selected { border-color: var(--accent); background: var(--accent-pill); color: var(--accent-deep); }
  .theme-option:has(input:focus-visible) { outline: 3px solid var(--accent); outline-offset: 2px; }
  .theme-option input { position: absolute; width: 1px; height: 1px; opacity: 0; }
  .theme-preview {
    --sample-page: #fcfcfd;
    --sample-nav: #e6edf7;
    --sample-row: #dce4ee;
    --sample-focus: #87b8f2;
    display: grid;
    grid-template-columns: 29% minmax(0, 1fr);
    gap: 8%;
    align-items: stretch;
    height: 58px;
    padding: 7px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--sample-page);
  }
  .theme-preview.dark, .theme-preview.system {
    --sample-nav: #2c3545;
    --sample-row: #536478;
    --sample-focus: #639de2;
    --sample-page: #191f29;
  }
  .theme-preview.system { background: linear-gradient(122deg, #eff4fb 50%, var(--sample-page) 50%); }
  .preview-sidebar, .preview-content { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .preview-sidebar { padding: 5px; border-radius: 4px; background: var(--sample-nav); }
  .preview-content { padding: 5px 5px 0 0; }
  .theme-preview i { display: block; height: 5px; border-radius: 3px; background: var(--sample-row); }
  .theme-preview i:first-child { width: 75%; background: var(--sample-focus); }
  .theme-preview i:last-child { width: 60%; }
  .theme-label { display: flex; align-items: center; justify-content: space-between; gap: 4px; padding: 1px 3px 2px; font-size: 13px; font-weight: 650; }
  .theme-label > span:first-child { min-width: 0; overflow-wrap: anywhere; }
  .radio-mark { width: 14px; height: 14px; flex: none; border: 1px solid var(--muted); border-radius: 50%; }
  .selected .radio-mark { border: 4px solid var(--accent); background: var(--panel); }
  .preference, .actions button {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    min-width: 0;
    min-height: 60px;
    padding: 10px 20px;
    font-weight: 550;
  }
  .preference + .preference, .theme-picker + .preference, .actions button + button, .profile + .actions {
    border-top: 1px solid var(--divider);
  }
  .preference > label, .preference > span, .actions span { min-width: 0; overflow-wrap: anywhere; }
  select {
    flex: 0 1 196px;
    min-width: 0;
    min-height: 44px;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: var(--panel2);
    color: var(--text);
    cursor: pointer;
  }
  .toggle { cursor: pointer; }
  .toggle input {
    appearance: none;
    display: inline-flex;
    align-items: center;
    flex: none;
    width: 42px;
    height: 24px;
    margin: 0;
    padding: 2px;
    border: 1px solid var(--muted);
    border-radius: 15px;
    background: var(--panel2);
    color: var(--muted);
    cursor: pointer;
  }
  .toggle input::before { content: ''; width: 18px; height: 18px; border-radius: 50%; background: currentColor; }
  .toggle input:checked { justify-content: flex-end; border-color: var(--accent); background: var(--accent); color: white; }
  .profile { display: flex; align-items: center; gap: 14px; padding: 18px 20px; overflow-wrap: anywhere; }
  .avatar { width: 52px; height: 52px; border-radius: 16px; font-size: 21px; }
  .profile strong, .profile div span { display: block; }
  .profile strong { font-size: 17px; }
  .profile div span { color: var(--muted); font-size: 13px; }
  .actions button {
    width: 100%;
    border: 0;
    background: transparent;
    color: var(--text);
    text-align: start;
  }
  .actions button:hover { background: var(--panel2); }
  .actions button:last-child { border-radius: 0 0 15px 15px; }
  .actions button:focus-visible { outline-offset: -3px; }
  .actions button.danger { color: var(--err); }
  .actions button :global(svg) { color: var(--muted); }

  @media (min-width: 1400px) {
    .settings-page { padding-inline: 40px; }
  }
  @media (max-width: 1060px) {
    .sections { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .sections button { min-height: 64px; align-items: center; }
  }
  @media (max-width: 760px) {
    .settings-page { padding: 18px 16px 28px; }
    .heading { margin-bottom: 16px; }
    h1 { font-size: 27px; }
    .sections { gap: 8px; margin-bottom: 16px; }
    .sections button { gap: 8px; min-height: 54px; padding: 8px; }
    .section-icon { width: 32px; height: 32px; }
    .section-copy small { display: none; }
    .theme-picker { padding-inline: 14px; padding-bottom: 14px; }
    .theme-options { gap: 6px; }
    .theme-label { font-size: 12px; }
    .theme-preview { height: 46px; }
    .preference, .actions button, .profile, h2 { padding-inline: 14px; }
    select { flex-basis: 160px; }
  }
  @media (forced-colors: active) {
    .sections button[aria-current], .theme-option.selected { outline: 2px solid Highlight; outline-offset: -2px; }
    .toggle input { appearance: auto; width: 24px; }
    .toggle input::before { content: none; }
  }
</style>
