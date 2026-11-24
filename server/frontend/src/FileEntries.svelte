<script>
  import { tick } from 'svelte';
  import Icon from './Icon.svelte';

  let {results, view, label, t, fmtSize, fmtDate, resultKey, parentDir, extInfo,
    thumbnailKind, onopen, actionsFor, ondoubleclick, onselect, isSelected, sort, onSort,
    dragging, canDrag, onDragStart, canDrop, onDrop, directory = false} = $props();
  let hoveredFolder = $state(null);
  let menu = $state(null);
  let menuElement = $state();
  let trigger;
  const menuId = $props.id();
  const tileGroups = $derived(directory ? [
    {label: 'folders', compact: true, items: results.filter(result => result.isDir)},
    {label: 'files', compact: false, items: results.filter(result => !result.isDir)},
  ] : [{label: '', compact: false, items: results}]);

  function closeMenu(restoreFocus = false) {
    menu = null;
    if (restoreFocus) trigger?.focus({preventScroll: true});
    trigger = null;
  }

  async function showMenu(button, result) {
    if (trigger === button && menu) {
      closeMenu(true);
      return;
    }
    trigger = button;
    const anchor = trigger.getBoundingClientRect();
    const width = Math.min(216, innerWidth - 16);
    menu = {result, left: Math.max(8, Math.min(anchor.right - width, innerWidth - width - 8)),
      top: anchor.bottom + 5, width, maxHeight: innerHeight - 16};
    await tick();
    if (!menuElement) return;
    positionMenu();
    menuElement.querySelector('button')?.focus({preventScroll: true});
  }

  function positionMenu() {
    if (!menu || !menuElement || !trigger?.isConnected) return;
    const anchor = trigger.getBoundingClientRect();
    const bounds = trigger.closest('.content')?.getBoundingClientRect();
    const top = Math.max(8, bounds?.top ?? 0);
    const bottom = Math.min(innerHeight - 8, bounds?.bottom ?? innerHeight);
    if (anchor.bottom < top || anchor.top > bottom) {
      closeMenu();
      return;
    }
    menu.width = Math.min(216, innerWidth - 16);
    menu.left = Math.max(8, Math.min(anchor.right - menu.width, innerWidth - menu.width - 8));
    menu.maxHeight = Math.max(40, bottom - top - 8);
    const height = Math.min(menuElement.offsetHeight, menu.maxHeight);
    menu.top = anchor.bottom + 5 + height <= bottom
      ? anchor.bottom + 5
      : Math.max(top, anchor.top - height - 5);
  }

  $effect(() => {
    results;
    view;
    closeMenu();
  });

  $effect(() => {
    if (!menu) return;
    window.addEventListener('scroll', positionMenu, true);
    window.addEventListener('resize', positionMenu);
    return () => {
      window.removeEventListener('scroll', positionMenu, true);
      window.removeEventListener('resize', positionMenu);
    };
  });

  function run(action) {
    closeMenu(true);
    action();
  }

  function rightClick(event, result) {
    event.preventDefault();
    const button = event.currentTarget.closest('tr, article')?.querySelector('.more');
    if (button) void showMenu(button, result);
  }

  $effect(() => { if (!dragging) hoveredFolder = null; });

  function dragOverFolder(event, result) {
    if (!canDrop(event, result)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    hoveredFolder = resultKey(result);
  }

  function leaveFolder(event, result) {
    if (hoveredFolder === resultKey(result) && !event.currentTarget.contains(event.relatedTarget)) {
      hoveredFolder = null;
    }
  }

  function dropOnFolder(event, result) {
    hoveredFolder = null;
    if (!canDrop(event, result)) return;
    event.preventDefault();
    event.stopPropagation();
    onDrop(result);
  }

  function location(result) {
    const folder = parentDir(result.path);
    return folder ? result.source + ' / ' + folder : result.source;
  }

  function mobileDetails(result) {
    let detail = '';
    if (sort.key === 'mtime' && result.modTime) detail = fmtDate(result.modTime);
    else if (!result.isDir && (sort.key === 'size' || directory)) detail = fmtSize(result.size);
    return [detail, !directory && location(result)].filter(Boolean).join(' · ');
  }

  function thumbUrl(result, kind) {
    return '/api/thumb?' + new URLSearchParams({
      source: result.source, path: result.path, w: kind === 'document' ? 384 : 320,
      v: `${result.modTime}-${result.size}`,
      ...(kind === 'document' ? {kind} : {}),
    });
  }
</script>

<svelte:window
  onpointerdown={(event) => {
    if (menu && !menuElement?.contains(event.target) && !trigger?.contains(event.target)) closeMenu();
  }}
  onkeydown={(event) => {
    if (menu && event.key === 'Escape') {
      event.preventDefault();
      closeMenu(true);
    }
  }}
/>

{#snippet typeBadge(result)}
  {@const badge = extInfo(result.name)}
  <span class="type-badge" style:background={result.isDir ? 'var(--accent-pill)' : badge.tint}
    style:color={result.isDir ? 'var(--accent-deep)' : badge.fg}>
    {#if result.isDir}<Icon name="folder" size={22}/>{:else}{badge.badge}{/if}
  </span>
{/snippet}

{#snippet thumbnail(result, large)}
  {@const kind = thumbnailKind(result)}
  <span class="thumbnail" class:large class:document={kind === 'document'} aria-hidden="true">
    {@render typeBadge(result)}
    {#if kind && (large || kind !== 'document')}
      {@const url = thumbUrl(result, kind)}
      {#key url}
        <img draggable="false" loading="lazy" decoding="async" src={url} alt="" onerror={(event) => event.currentTarget.hidden = true}/>
      {/key}
    {/if}
    {#if large && kind === 'video'}<span class="play"><Icon name="play" size={22}/></span>{/if}
  </span>
{/snippet}

{#snippet actionButton(result)}
  {@const open = menu && resultKey(menu.result) === resultKey(result)}
  <button type="button" class="more" aria-label={t('searchFileActions', {name:result.name})}
    aria-expanded={Boolean(open)} aria-controls={open ? menuId : undefined} onclick={(event) => showMenu(event.currentTarget, result)}>
    <Icon name="more" size={20}/>
  </button>
{/snippet}

{#snippet selectBox(result)}
  {#if onselect}
    <input type="checkbox" class="select-box" checked={isSelected(result)}
      aria-label={t('fileSelectNamed', {name:result.name})} onchange={() => onselect(result)}/>
  {/if}
{/snippet}

{#snippet sortButton(key, label, compact = false)}
  {@const active = sort.key === key}
  <button type="button" class="sort-heading" class:active aria-pressed={compact ? active : undefined}
    title={active ? t(sort.order === 'asc' ? 'ascending' : 'descending') : undefined} onclick={() => onSort(key)}>
    {t(label)}
    <span class="sort-direction" aria-hidden="true">{active ? (sort.order === 'asc' ? '↑' : '↓') : ''}</span>
  </button>
{/snippet}

<div class="results">
  {#if view === 'list'}
    <table class="file-table" aria-label={label}>
      <colgroup><col class:col-name={!directory}/>{#if !directory}<col/>{/if}<col class="col-size"/><col class="col-date"/><col class="col-actions"/></colgroup>
      <thead><tr>
        <th scope="col" aria-sort={sort.key === 'name' ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}>
          <div class="desktop-heading">{@render sortButton('name', 'name')}</div>
          <div class="compact-headings" role="group" aria-label={t('sort')}>
            {@render sortButton('name', 'name', true)}
            {@render sortButton('size', 'size', true)}
            {@render sortButton('mtime', 'modified', true)}
          </div>
        </th>
        {#if !directory}<th scope="col" class="desktop">{t('searchFolder')}</th>{/if}
        <th scope="col" class="desktop size" aria-sort={sort.key === 'size' ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}>
          {@render sortButton('size', 'size')}
        </th>
        <th scope="col" class="desktop" aria-sort={sort.key === 'mtime' ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}>
          {@render sortButton('mtime', 'modified')}
        </th>
        <th scope="col" class="actions" aria-label={t('searchActions')}></th>
      </tr></thead>
      <tbody>
        {#each results as result (resultKey(result))}
          {@const detail = mobileDetails(result)}
          <tr class:selected={onselect && isSelected(result)} class:drop-target={hoveredFolder === resultKey(result)}
            ondragover={event => dragOverFolder(event, result)} ondragleave={event => leaveFolder(event, result)}
            ondrop={event => dropOnFolder(event, result)}>
            <td><div class="name-cell">
              {@render selectBox(result)}
              <button type="button" class="file-name row-name" title={result.name}
                data-file-key={resultKey(result)} draggable={canDrag(result)} ondragstart={event => onDragStart(event, result)}
                onclick={(event) => onopen(result, event)}
                ondblclick={() => ondoubleclick?.(result)} oncontextmenu={(event) => rightClick(event, result)}>
                {@render thumbnail(result, false)}
                <span class="file-label">
                  <span class="name">{result.name}</span>
                  {#if result.owner}<span class="owner" title={t('fileOwnerLabel',{owner:result.owner})}>{t('fileOwnerLabel',{owner:result.owner})}</span>{/if}
                  {#if detail}<span class="mobile-folder">{detail}</span>{/if}
                </span>
              </button>
            </div></td>
            {#if !directory}
              <td class="desktop"><span class="folder" title={location(result)}><Icon name="folder" size={16}/><span>{location(result)}</span></span></td>
            {/if}
            <td class="desktop size">{result.isDir ? '—' : fmtSize(result.size)}</td>
            <td class="desktop date">{result.modTime ? fmtDate(result.modTime) : '—'}</td>
            <td class="actions">{@render actionButton(result)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else}
    {#each tileGroups as group (group.label)}
      {#if group.items.length}
        {#if group.label}<h2 class="group-heading">{t(group.label)}</h2>{/if}
        <div class="file-tiles" role="group" aria-label={group.label ? t(group.label) : label}>
          {#each group.items as result (resultKey(result))}
            <article class="tile" class:compact={group.compact} class:selected={onselect && isSelected(result)}
              class:drop-target={hoveredFolder === resultKey(result)}
              ondragover={event => dragOverFolder(event, result)} ondragleave={event => leaveFolder(event, result)}
              ondrop={event => dropOnFolder(event, result)}>
              <button type="button" class="file-name tile-open" title={result.name} data-file-key={resultKey(result)}
                draggable={canDrag(result)} ondragstart={event => onDragStart(event, result)}
                onclick={(event) => onopen(result, event)}
                ondblclick={() => ondoubleclick?.(result)} oncontextmenu={(event) => rightClick(event, result)}>
                {@render thumbnail(result, !group.compact)}
                <span class="tile-label">
                  <span class="name">{result.name}</span>
                  {#if !directory}
                    <span class="folder" title={location(result)}><Icon name="folder" size={15}/><span>{location(result)}</span></span>
                  {:else if !result.isDir}<span class="folder">{fmtSize(result.size)}</span>{/if}
                  {#if result.owner}<span class="owner" title={t('fileOwnerLabel',{owner:result.owner})}>{t('fileOwnerLabel',{owner:result.owner})}</span>{/if}
                </span>
              </button>
              {@render actionButton(result)}
              {@render selectBox(result)}
            </article>
          {/each}
        </div>
      {/if}
    {/each}
  {/if}
</div>

{#if menu}
  <div class="result-menu" id={menuId} role="group" aria-label={t('searchFileActions', {name:menu.result.name})}
    bind:this={menuElement} style:top="{menu.top}px" style:left="{menu.left}px" style:width="{menu.width}px" style:max-height="{menu.maxHeight}px">
    {#each actionsFor(menu.result) as action (action.label)}
      {#if action.danger}<div class="menu-separator"></div>{/if}
      <button type="button" class:danger={action.danger} onclick={() => run(action.run)}><Icon name={action.icon} size={18}/>{t(action.label)}</button>
    {/each}
  </div>
{/if}

<style>
  .results{container-type:inline-size;min-width:0}
  .file-table{width:100%;table-layout:fixed;border-spacing:0}
  .owner{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--muted);font-size:.75rem;font-weight:400}
  .col-name{width:45%}
  .col-size{width:clamp(88px,11cqw,136px)}
  .col-date{width:clamp(100px,13cqw,164px)}
  .col-actions{width:54px}
  th,td{text-align:start;padding:4px 14px;border-bottom:1px solid var(--divider)}
  th{height:32px;color:var(--muted);font-size:12px;font-weight:560}
  .sort-heading{
    display:inline-flex;align-items:center;gap:4px;min-height:28px;
    border:0;border-radius:4px;padding:0;color:inherit;background:none;
    font:inherit;white-space:nowrap;cursor:pointer;
  }
  .sort-heading:hover,.sort-heading.active{color:var(--accent-deep)}
  .sort-heading:focus-visible{outline:2px solid var(--accent);outline-offset:3px}
  .sort-direction{width:1ch;text-align:center}
  .compact-headings{display:none}
  tr{position:relative}
  tr:last-child td{border-bottom:0}
  td{height:44px;font-size:13px}
  .file-table tbody tr:hover{background:var(--row)}
  .file-table tbody tr.selected{background:var(--rowsel)}
  .actions{padding:3px 9px;text-align:end}
  .size{text-align:end;white-space:nowrap;font-variant-numeric:tabular-nums}
  .date{white-space:nowrap;color:var(--text2)}
  .file-name{
    display:flex;align-items:center;width:100%;min-width:0;border:0;border-radius:6px;
    background:none;color:var(--text);font:inherit;text-align:start;cursor:pointer;
  }
  .row-name{gap:10px;padding:4px 0}
  td:first-child{position:relative}
  .row-name::after{content:'';position:absolute;inset-block:0;inset-inline-start:0;width:100cqw}
  .name-cell{display:flex;align-items:center;gap:9px;min-width:0}
  .select-box{
    position:relative;z-index:1;flex:none;width:18px;height:18px;margin:0;
    accent-color:var(--accent);cursor:pointer;
  }
  .file-name:hover .name{color:var(--accent-deep)}
  .file-label,.tile-label{display:block;min-width:0}
  .name,.folder>span,.mobile-folder{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  .name{font-weight:570}
  .folder{display:flex;min-width:0;gap:6px;align-items:center;color:var(--muted)}
  .mobile-folder{display:none;color:var(--muted);font-size:12px}
  .thumbnail{
    position:relative;display:grid;place-items:center;flex:none;width:36px;height:32px;
    overflow:hidden;border-radius:6px;background:var(--panel2);
  }
  .type-badge{
    display:grid;place-items:center;min-width:29px;min-height:25px;padding:0 3px;
    border-radius:5px;font-size:9px;font-weight:800;letter-spacing:.03em;
  }
  .thumbnail img{position:absolute;inset:0;width:100%;height:100%;object-fit:cover}
  .thumbnail.document img{object-fit:contain;background:var(--panel2)}
  .thumbnail.large{width:100%;height:auto;aspect-ratio:1.62;border-radius:0;border-bottom:1px solid var(--border)}
  .large .type-badge{min-width:54px;min-height:42px;border-radius:9px;font-size:13px}
  .play{
    position:absolute;display:grid;place-items:center;width:32px;height:32px;
    border-radius:50%;background:#151923d9;color:#fff;
  }
  .file-tiles{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,214px),1fr));gap:12px}
  .group-heading{font-size:13px;font-weight:620;color:var(--text2);margin:0 0 10px}
  .file-tiles + .group-heading{margin-top:20px}
  .tile{
    position:relative;display:grid;grid-template-columns:minmax(0,1fr) 38px;min-width:0;
    border:1px solid var(--border);border-radius:12px;background:var(--panel);
  }
  .tile:hover{border-color:var(--chev)}
  .tile.selected{border-color:var(--accent);background:var(--rowsel)}
  .file-table tbody tr.drop-target,.tile.drop-target{
    background:var(--accent-pill);
    outline:2px solid var(--accent);
    outline-offset:-2px;
  }
  .tile .select-box{position:absolute;top:8px;left:8px}
  .tile-open{display:block;grid-column:1/-1;grid-row:1;padding:0;border-radius:11px;overflow:hidden;font-size:13px}
  .tile.compact .tile-open{display:flex;gap:8px;min-height:58px;padding:6px 38px 6px 10px}
  .tile.compact:has(.select-box) .tile-open{padding-inline-start:34px}
  .tile.compact .select-box{top:50%;transform:translateY(-50%)}
  .tile.compact .tile-label{padding:0}
  .tile-label{padding:8px 42px 9px 11px}
  .tile-label .folder{font-size:12px;margin-top:2px}
  .more{
    position:relative;z-index:1;display:inline-grid;place-items:center;width:34px;height:34px;padding:0;
    border:0;border-radius:7px;background:none;color:var(--text2);cursor:pointer;
  }
  .more:hover,.more[aria-expanded="true"]{background:var(--accent-pill);color:var(--accent-deep)}
  .tile .more{position:relative;grid-column:2;grid-row:1;align-self:end;margin-bottom:5px}
  .tile.compact .more{align-self:center;margin-bottom:0}
  .result-menu{
    position:fixed;z-index:50;padding:5px;border:1px solid var(--border);
    border-radius:10px;background:var(--panel);box-shadow:0 8px 24px #0003;overflow-y:auto;
  }
  .result-menu button{
    display:flex;align-items:center;gap:9px;width:100%;min-height:38px;padding:6px 9px;text-align:start;
    border:0;border-radius:6px;background:none;color:var(--text);font-size:13px;cursor:pointer;
  }
  .result-menu button:hover,.result-menu button:focus-visible{background:var(--accent-pill);color:var(--accent-deep)}
  .result-menu button.danger{color:var(--err)}
  .menu-separator{height:1px;background:var(--divider);margin:4px}
  @container(max-width:630px){
    .desktop,.file-table colgroup{display:none}
    .desktop-heading{display:none}
    .compact-headings{display:flex;gap:12px;flex-wrap:wrap}
    .compact-headings .sort-heading{min-height:36px}
    .mobile-folder{display:block}
    .file-table th,.file-table td{padding-inline:9px}
    .file-table .actions{width:47px;padding-inline:5px}
    .file-tiles{grid-template-columns:repeat(auto-fill,minmax(min(100%,144px),1fr))}
    .tile-label{padding-inline-start:8px}
  }
</style>
