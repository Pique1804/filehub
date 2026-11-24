<script>
  import { tick, untrack } from 'svelte';
  import { createVirtualizer } from '@tanstack/svelte-virtual';
  import Icon from './Icon.svelte';
  import { createPhotoThumbnailLoader } from './photo-thumbnail-loader.js';
  import { photoGridLayout, timelineRows, thumbUrl, photoKey } from './media-utils.js';
  let { photos = [], onopen, actionsFor, onselect, selectionMode, selected,
    hasMore = false, loadingMore = false, onloadmore = () => {}, pageError = '', onretry = () => {},
    size, desktop, locale, t, groupByDate = true } = $props();
  const loadThumbnail = createPhotoThumbnailLoader();
  let scroller = $state(), width = $state(800);
  let menu = $state(null), menuElement = $state();
  let menuTrigger;
  const menuId = $props.id();
  const {columns, cell, gap} = $derived(photoGridLayout(width, size, desktop));
  const rows = $derived(timelineRows(photos, columns, locale, groupByDate));
  const virtualizer = createVirtualizer({count:0, getScrollElement:() => scroller, estimateSize:() => 240, overscan:3});
  let previousRows, previousColumns, previousPhotos;
  $effect(() => {
    const list = rows, count = columns, currentPhotos = photos, node = scroller;
    const rowHeight = cell + (desktop ? 26 + gap : 44);
    untrack(() => {
      const v = $virtualizer;
      let anchor = -1;
      if (node?.scrollTop && previousPhotos === currentPhotos && previousColumns !== count) {
        const firstVisible = v.getVirtualItems().find(item => item.end > node.scrollTop);
        const oldRow = previousRows[firstVisible?.index];
        if (oldRow?.type === 'date') anchor = list.findIndex(row => row.key === oldRow.key);
        else if (oldRow?.photos.length) {
          const photo = oldRow.photos[0];
          anchor = list.findIndex(row => row.photos?.includes(photo));
        }
      }
      previousRows = list;
      previousColumns = count;
      previousPhotos = currentPhotos;
      v.setOptions({count:list.length + 1, getScrollElement:() => node,
        getItemKey:i => list[i]?.key || 'footer', estimateSize:i => list[i]?.type === 'photos' ? rowHeight : 64});
      v.measure();
      if (anchor >= 0) void tick().then(() => {
        if (node === scroller && list === previousRows) v.scrollToIndex(anchor, {align:'start'});
      });
    });
  });
  $effect(() => {
    const items = $virtualizer.getVirtualItems();
    if (hasMore && !loadingMore && !pageError && (items.at(-1)?.index ?? 0) >= rows.length - 6) untrack(onloadmore);
  });
  function observe(node) {
    const observer = new ResizeObserver(entries => {width = entries[0].contentRect.width;});
    observer.observe(node); return {destroy:() => observer.disconnect()};
  }

  function closeMenu(restoreFocus = false) {
    menu = null;
    if (restoreFocus) menuTrigger?.focus({preventScroll:true});
    menuTrigger = null;
  }

  async function showMenu(event, photo) {
    event.preventDefault();
    const button = event.currentTarget;
    if (menu && menuTrigger === button && event.type === 'click') {
      closeMenu(true);
      return;
    }
    menuTrigger = button;
    const anchor = button.getBoundingClientRect();
    const atPointer = event.type === 'contextmenu' && (event.clientX !== 0 || event.clientY !== 0);
    const offset = 8;
    const menuWidth = Math.min(232, innerWidth - offset * 2);
    const left = atPointer ? event.clientX : anchor.right - menuWidth;
    const top = atPointer ? event.clientY : anchor.bottom + 4;
    menu = {photo, left:Math.max(offset, Math.min(left, innerWidth - menuWidth - offset)),
      top, width:menuWidth};
    await tick();
    if (!menuElement || menuTrigger !== button) return;
    menu.top = Math.max(offset, Math.min(top, innerHeight - menuElement.offsetHeight - offset));
    menuElement.querySelector('button')?.focus({preventScroll:true});
  }

  function run(action) {
    closeMenu(true);
    action();
  }

  function onMenuKey(event) {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    closeMenu(true);
  }
</script>

<svelte:window onresize={() => closeMenu()} onpointerdown={(event) => {
  if (menu && !menuElement?.contains(event.target) && !menuTrigger?.contains(event.target)) closeMenu();
}} />

<div class="timeline" bind:this={scroller} use:observe role="region" aria-label={t('timeline')} tabindex="0" onscroll={() => closeMenu()}>
  <div class="space" style:height={$virtualizer.getTotalSize() + 'px'}>
    {#each $virtualizer.getVirtualItems() as item (item.key)}
      {@const row = rows[item.index]}
      <div class="row" style:height={item.size + 'px'} style:transform={'translateY(' + item.start + 'px)'}>
        {#if row?.type === 'date'}<h2>{row.date}</h2>
        {:else if row}
          <div class="grid" class:compact={desktop && size === 'compact'}
            style:gap={gap + 'px'} style:grid-template-columns={'repeat(' + columns + ',minmax(0,1fr))'}>
            {#each row.photos as p (photoKey(p))}
              {@const checked = selected.has(photoKey(p))}
              {@const expanded = menu && photoKey(menu.photo) === photoKey(p)}
              <div class="photo-card">
                <button type="button" class="photo" aria-pressed={selectionMode ? checked : undefined}
                  aria-label={t(selectionMode ? 'fileSelectNamed' : 'openNamed', {name:p.name})} title={p.name}
                  onclick={(event) => selectionMode || event.ctrlKey || event.metaKey ? onselect(p) : onopen(p)}
                  oncontextmenu={(event) => showMenu(event,p)}>
                  <span class="image">
                    <img use:loadThumbnail={{url:thumbUrl(p), video:p.isVideo}} alt="" decoding="async"/>
                    {#if p.isVideo}<span class="video" aria-label={t('video')}>▶</span>{/if}
                    {#if selectionMode}
                      <span class="selection-mark" class:checked aria-hidden="true">{#if checked}<Icon name="check" size={16}/>{/if}</span>
                    {/if}
                  </span>
                  <span class="name">{p.name}</span>
                </button>
                <button type="button" class="photo-more" aria-label={t('searchFileActions', {name:p.name})}
                  aria-expanded={Boolean(expanded)} aria-controls={expanded ? menuId : undefined}
                  onclick={(event) => showMenu(event,p)}><Icon name="more" size={20}/></button>
              </div>
            {/each}
          </div>
        {:else}
          <div class="footer" role="status">{#if pageError}<span>{pageError}</span><button onclick={onretry}>{t('retry')}</button>{:else if loadingMore}{t('photosLoading')}{:else if hasMore}<button onclick={onloadmore}>{t('loadMore')}</button>{:else if photos.length}{t('collectionLoaded')}{/if}</div>
        {/if}
      </div>
    {/each}
  </div>
</div>

{#if menu}
  <div class="photo-menu" id={menuId} role="group" aria-label={t('searchFileActions', {name:menu.photo.name})}
    bind:this={menuElement} style:top="{menu.top}px" style:left="{menu.left}px" style:width="{menu.width}px">
    {#each actionsFor(menu.photo) as action (action.label)}
      <button type="button" class:danger={action.danger} onkeydown={onMenuKey} onclick={() => run(action.run)}>
        <Icon name={action.icon} size={18}/>{t(action.label)}
      </button>
    {/each}
  </div>
{/if}

<style>
  .timeline{flex:1;min-height:180px;overflow:auto;position:relative;scrollbar-width:thin;outline-offset:-3px;padding-right:3px}
  .space{width:100%;position:relative}.row{position:absolute;top:0;left:0;width:100%}h2{font-size:18px;font-weight:650;letter-spacing:-.3px;margin:22px 0 14px;text-transform:capitalize;color:var(--text)}
  .grid{display:grid}.photo{min-width:0;padding:0;border:0;background:transparent;text-align:left;color:var(--text);cursor:pointer}.image{display:block;aspect-ratio:1;position:relative;overflow:hidden;background:var(--row);border-radius:10px}.image img{width:100%;height:100%;object-fit:cover;display:block}.name{display:block;padding:8px 2px 0;font-size:12px;font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.video{position:absolute;bottom:8px;right:8px;border-radius:20px;padding:2px 7px;color:white;background:#0008}.footer{min-height:52px;display:flex;align-items:center;justify-content:center;gap:12px;color:var(--muted);font-size:12px}.footer button{border:1px solid var(--border);border-radius:8px;padding:8px 12px;background:var(--panel);color:var(--text);cursor:pointer}
  .photo-card{position:relative;min-width:0}
  .photo{display:block;width:100%;border-radius:10px}
  .photo[aria-pressed="true"] .image{outline:2px solid var(--accent);outline-offset:-2px}
  .selection-mark,.photo-more{position:absolute;top:7px;display:grid;place-items:center}
  .selection-mark{left:7px;width:24px;height:24px;background:var(--panel);border:1px solid var(--muted);border-radius:50%}
  .selection-mark.checked{color:#fff;background:var(--accent);border-color:var(--accent)}
  .photo-more{right:7px;width:36px;height:36px;padding:0;border:1px solid var(--border);border-radius:9px;background:var(--panel);color:var(--text);cursor:pointer}
  .compact .image,.compact .photo{border-radius:6px}
  .compact .selection-mark{top:4px;left:4px;width:20px;height:20px}
  .compact .photo-more{top:4px;right:4px;width:30px;height:30px;border-radius:6px}
  .compact .video{right:4px;bottom:4px;padding:1px 4px;font-size:11px}
  @media(hover:hover){
    .photo-more{opacity:0}
    .photo-card:hover .photo-more,.photo-card:focus-within .photo-more,.photo-more[aria-expanded="true"]{opacity:1}
  }
  .photo-menu{position:fixed;z-index:45;max-height:calc(100dvh - 16px);overflow:auto;padding:5px;background:var(--panel);border:1px solid var(--border);border-radius:12px;box-shadow:0 12px 30px #10142630}
  .photo-menu button{display:flex;align-items:center;gap:9px;width:100%;min-height:40px;padding:6px 9px;background:none;border:0;border-radius:7px;color:var(--text);font:inherit;text-align:start;cursor:pointer}
  .photo-menu button:hover,.photo-menu button:focus-visible{background:var(--accent-pill)}
  .photo-menu button.danger{color:var(--err);border-top:1px solid var(--divider);border-radius:0}
  @media(max-width:760px){h2{font-size:16px}.name{font-size:11px}}
</style>
