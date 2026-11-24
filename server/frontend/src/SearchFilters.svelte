<script>
  import { tick } from 'svelte';
  import Icon from './Icon.svelte';

  let { filters = $bindable(), active, diskList, searchTypes, locale, t, onreset } = $props();
  let open = $state('');
  let minMiB = $state(), maxMiB = $state();
  let from = $state(''), through = $state('');
  let choosingEnd = $state(false);
  let month = $state(new Date());
  let filterRow, sizeButton, dateButton;
  let panelHeading = $state();

  const numberFormat = $derived(new Intl.NumberFormat(locale));
  const shortDate = $derived(new Intl.DateTimeFormat(locale, {day: 'numeric', month: 'short', year: 'numeric'}));
  const monthFormat = $derived(new Intl.DateTimeFormat(locale, {month: 'long', year: 'numeric'}));
  const dayFormat = $derived(new Intl.DateTimeFormat(locale, {dateStyle: 'full'}));
  const weekdayFormat = $derived(new Intl.DateTimeFormat(locale, {weekday: 'short'}));
  const weekdays = $derived(Array.from({length: 7}, (_, i) => weekdayFormat.format(new Date(2024, 0, i + 1))));
  const sizeActive = $derived(filters.minMiB !== undefined || filters.maxMiB !== undefined);
  const dateActive = $derived(Boolean(filters.from || filters.through));
  const sizeLabel = $derived(!sizeActive ? t('size') : t('searchSizeUnit', {
    size: rangeLabel(filters.minMiB, filters.maxMiB, value => numberFormat.format(value)),
  }));
  const dateLabel = $derived(!dateActive ? t('searchDates') :
    rangeLabel(filters.from || undefined, filters.through || undefined, value => shortDate.format(readDate(value))));
  const invalidSize = $derived.by(() => {
    const min = minMiB === undefined ? undefined : Math.ceil(minMiB * 1024 ** 2);
    const max = maxMiB === undefined ? undefined : Math.floor(maxMiB * 1024 ** 2);
    return [min, max].some(value => value !== undefined && (!Number.isSafeInteger(value) || value < 0))
      || (min !== undefined && max !== undefined && min > max);
  });
  const invalidDate = $derived(Boolean(from && through && from > through));
  const firstDay = $derived(dayOfMonth(month, 1));
  const days = $derived.by(() => {
    const dates = [];
    for (let day = 1; day <= 31; day++) {
      const date = dayOfMonth(month, day);
      if (date.getMonth() !== month.getMonth()) break;
      dates.push({day, value: isoDate(date), label: dayFormat.format(date)});
    }
    return dates;
  });
  const today = isoDate(new Date());

  function rangeLabel(start, end, format) {
    if (start === undefined) return `≤ ${format(end)}`;
    if (end === undefined) return `≥ ${format(start)}`;
    return start === end ? format(start) : `${format(start)} – ${format(end)}`;
  }
  function readDate(value) {
    return new Date(value + 'T12:00:00');
  }
  function dayOfMonth(date, day) {
    const result = new Date(date);
    result.setDate(day);
    return result;
  }
  function isoDate(date) {
    const year = String(date.getFullYear()).padStart(4, '0');
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }
  function moveMonth(offset) {
    const date = dayOfMonth(month, 1);
    date.setMonth(date.getMonth() + offset);
    if (date.getFullYear() >= 1 && date.getFullYear() <= 9999) month = date;
  }
  function pickDay(value) {
    if (choosingEnd) {
      [from, through] = value < from ? [value, from] : [from, value];
      choosingEnd = false;
    } else {
      from = value;
      through = '';
      choosingEnd = true;
    }
  }
  function showTypedDay(event) {
    const input = event.currentTarget;
    choosingEnd = false;
    if (input.value && input.validity.valid) month = readDate(input.value);
  }
  async function toggle(section) {
    if (open === section) {
      close();
      return;
    }
    if (section === 'size') {
      minMiB = filters.minMiB;
      maxMiB = filters.maxMiB;
    } else {
      from = filters.from;
      through = filters.through;
      month = from || through ? readDate(from || through) : new Date();
      choosingEnd = false;
    }
    open = section;
    await tick();
    panelHeading?.focus({preventScroll: true});
    panelHeading?.closest('form')?.scrollIntoView({block: 'nearest'});
  }
  function close(restoreFocus = true) {
    const trigger = open === 'size' ? sizeButton : dateButton;
    open = '';
    if (restoreFocus) trigger?.focus();
  }
  function apply(event) {
    event.preventDefault();
    if (open === 'size') {
      if (invalidSize) return;
      filters = {...filters, minMiB, maxMiB};
    } else {
      if (invalidDate) return;
      filters = {...filters, from, through: choosingEnd ? from : through};
    }
    close();
  }
  function resetRange() {
    filters = open === 'size'
      ? {...filters, minMiB: undefined, maxMiB: undefined}
      : {...filters, from: '', through: ''};
    close();
  }
</script>

<svelte:window
  onpointerdown={(event) => { if (open && !filterRow?.contains(event.target)) close(false); }}
  onkeydown={(event) => {
    if (open && event.key === 'Escape' && filterRow?.contains(document.activeElement)) {
      event.preventDefault();
      close();
    }
  }}
/>

<div class="file-filters" role="group" aria-label={t('searchFilters')} bind:this={filterRow}>
  <select aria-label={t('storage')} class:active={filters.source} bind:value={filters.source}>
    <option value="">{t('searchAllStorages')}</option>
    {#each diskList as disk (disk.name)}<option value={disk.name}>{disk.name}</option>{/each}
  </select>
  <select aria-label={t('type')} class:active={filters.type} bind:value={filters.type}>
    <option value="">{t('searchAllTypes')}</option>
    {#each searchTypes as [value, label] (value)}<option {value}>{t(label)}</option>{/each}
  </select>

  <div class="range-control">
    <button class="filter" class:active={sizeActive} type="button" bind:this={sizeButton}
      aria-label={sizeActive ? t('size') + ' ' + sizeLabel : sizeLabel} aria-expanded={open === 'size'}
      aria-controls={open === 'size' ? 'file-size-filter' : undefined} onclick={() => toggle('size')}>
      <span>{sizeLabel}</span><span class="chevron" aria-hidden="true"></span>
    </button>
    {#if open === 'size'}
      <form class="range-panel size-panel" id="file-size-filter" aria-labelledby="file-size-heading" onsubmit={apply}>
        <h2 id="file-size-heading" tabindex="-1" bind:this={panelHeading}>{t('size')}</h2>
        <div class="bound-fields">
          <label>{t('searchMinSize')}<input type="number" min="0" step="any" bind:value={minMiB}/></label>
          <label>{t('searchMaxSize')}<input type="number" min="0" step="any" bind:value={maxMiB}/></label>
        </div>
        {#if invalidSize}<p class="range-error" role="alert">{t('searchInvalidSize')}</p>{/if}
        <div class="range-actions">
          {#if sizeActive}<button type="button" class="reset" onclick={resetRange}>{t('reset')}</button>{/if}
          <button type="submit" class="apply" disabled={invalidSize}>{t('searchApply')}</button>
        </div>
      </form>
    {/if}
  </div>

  <div class="range-control">
    <button class="filter" class:active={dateActive} type="button" bind:this={dateButton}
      aria-label={dateActive ? t('searchDates') + ' ' + dateLabel : dateLabel} aria-expanded={open === 'dates'}
      aria-controls={open === 'dates' ? 'file-date-filter' : undefined} onclick={() => toggle('dates')}>
      <span class="calendar-icon" aria-hidden="true"></span><span>{dateLabel}</span>
    </button>
    {#if open === 'dates'}
      <form class="range-panel date-panel" id="file-date-filter" aria-labelledby="file-date-heading" onsubmit={apply}>
        <h2 id="file-date-heading" tabindex="-1" bind:this={panelHeading}>{t('searchModifiedDate')}</h2>
        <div class="bound-fields">
          <label>{t('dateFrom')}<input type="date" min="0001-01-01" max="9999-12-31" bind:value={from} onchange={showTypedDay}/></label>
          <label>{t('dateTo')}<input type="date" min="0001-01-01" max="9999-12-31" bind:value={through} onchange={showTypedDay}/></label>
        </div>
        <div class="month">
          <button type="button" class="month-arrow" aria-label={t('searchPreviousMonth')} onclick={() => moveMonth(-1)}><Icon name="back" size={18}/></button>
          <span aria-live="polite">{monthFormat.format(month)}</span>
          <button type="button" class="month-arrow" aria-label={t('searchNextMonth')} onclick={() => moveMonth(1)}><Icon name="next" size={18}/></button>
        </div>
        <div class="calendar">
          {#each weekdays as weekday, i (i)}<span class="weekday">{weekday}</span>{/each}
          {#each days as {day, value, label} (value)}
            <button type="button" class="day" class:range={from && through && value > from && value < through}
              class:start={value === from} class:end={value === through}
              style:grid-column={day === 1 ? ((firstDay.getDay() + 6) % 7) + 1 : undefined}
              aria-label={label} aria-current={value === today ? 'date' : undefined}
              aria-pressed={value === from || value === through || Boolean(from && through && value > from && value < through)}
              onclick={() => pickDay(value)}>{day}</button>
          {/each}
        </div>
        {#if invalidDate}<p class="range-error" role="alert">{t('searchInvalidDate')}</p>{/if}
        <div class="range-actions">
          {#if dateActive}<button type="button" class="reset" onclick={resetRange}>{t('reset')}</button>{/if}
          <button type="submit" class="apply" disabled={invalidDate}>{t('searchApply')}</button>
        </div>
      </form>
    {/if}
  </div>

  {#if active}
    <button type="button" class="reset" onclick={() => { close(false); onreset(); }}>{t('reset')}</button>
  {/if}
</div>

<style>
  .file-filters{position:relative;display:flex;flex-wrap:wrap;align-items:center;gap:7px;margin:0 0 14px}
  .file-filters select,.filter{
    max-width:100%;min-height:36px;padding:5px 12px;border:1px solid var(--border);border-radius:8px;
    background-color:var(--panel);color:var(--text2);font-size:13px;font-weight:550;cursor:pointer;
  }
  .file-filters select{max-width:min(100%,220px)}
  .range-control{max-width:100%;min-width:0}
  .filter{display:flex;align-items:center;gap:8px;text-align:start}
  .filter span{min-width:0}
  .filter:hover,.filter[aria-expanded="true"],.file-filters select:hover{border-color:var(--accent)}
  .filter.active,.file-filters select.active{background-color:var(--accent-pill);color:var(--accent-deep)}
  .chevron{width:6px;height:6px;margin-inline-start:2px;border:solid currentColor;border-width:0 1.5px 1.5px 0;transform:translateY(-1px) rotate(45deg)}
  .calendar-icon{
    position:relative;flex:none;width:15px;height:14px;margin-block-start:2px;border:1.5px solid currentColor;
    border-radius:3px;background:linear-gradient(currentColor,currentColor) 0 3px / 100% 1px no-repeat;
  }
  .calendar-icon::before,.calendar-icon::after{
    content:'';position:absolute;top:-4px;width:1.5px;height:4px;border-radius:1px;background:currentColor;
  }
  .calendar-icon::before{left:2px}
  .calendar-icon::after{right:2px}
  .range-panel{
    position:absolute;z-index:4;inset-block-start:calc(100% + 8px);inset-inline-start:0;scroll-margin-block:8px;
    width:min(350px,100%);padding:14px;border:1px solid var(--border);border-radius:12px;background:var(--panel);
    box-shadow:0 14px 36px rgba(0,0,0,.19);
  }
  .size-panel{width:min(300px,100%)}
  .range-panel h2{margin:0 0 12px;font-size:14px;line-height:1.3}
  .range-panel h2:focus{outline:0}
  .bound-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}
  .bound-fields label{display:grid;gap:4px;color:var(--text2);font-size:12px}
  .bound-fields input{
    width:100%;min-width:0;min-height:36px;padding:5px 8px;border:1px solid var(--border);
    border-radius:7px;background:var(--panel2);color:var(--text);font-size:13px;
  }
  .month{display:flex;align-items:center;justify-content:space-between;margin-block:10px 4px;font-size:13px;font-weight:650}
  .month span::first-letter{text-transform:uppercase}
  .calendar{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));row-gap:2px;text-align:center}
  .weekday{padding-block:4px;font-size:11px;color:var(--muted)}
  .month-arrow,.day{
    display:grid;place-items:center;min-height:31px;padding:0;border:0;background:transparent;color:var(--text);cursor:pointer;
  }
  .month-arrow{width:34px;border-radius:6px;color:var(--text2)}
  .day{border-radius:6px;font-size:13px}
  .month-arrow:hover,.day:hover{background:var(--row)}
  .day[aria-current="date"]{font-weight:750;text-decoration:underline;text-underline-offset:3px}
  .day.range{border-radius:0;background:var(--accent-soft);color:var(--accent-deep)}
  .day.start,.day.end{background:#0065D1;color:white;font-weight:650}
  .day.start:not(.end):has(+.range){border-radius:6px 0 0 6px}
  .day.range+.end:not(.start){border-radius:0 6px 6px 0}
  .range-actions{display:flex;align-items:center;justify-content:flex-end;gap:6px;margin-top:12px}
  .reset,.apply{min-height:34px;padding:4px 10px;border:0;border-radius:7px;font-size:13px;font-weight:600;cursor:pointer}
  .reset{background:none;color:var(--accent-deep)}
  .reset:hover{background:var(--accent-pill)}
  .apply{background:#0065D1;color:#fff}
  .apply:hover:enabled{background:#0059BB}
  .range-error{margin:8px 0 0;color:var(--err);font-size:12px}
  @media(min-width:1080px){
    .range-control{position:relative}
    .range-panel{width:350px}
    .size-panel{width:300px}
  }
  @media(max-width:760px){
    .file-filters select,.filter,.reset{min-height:40px}
    .bound-fields input{padding-inline:5px;font-size:16px}
    .day{min-height:35px}
  }
</style>
