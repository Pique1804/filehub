<script>
  import { untrack } from 'svelte';
  import Icon from './Icon.svelte';

  let { source, path, position = 0, t, onposition = null, onended = null, onnext = null,
    title = '', subtitle = '', onclose = null, onepisodes = null, episodesOpen = false, episodePanel } = $props();
  const directExtensions = new Set(['mp4', 'webm', 'mov', 'm4v', 'ogv']);
  const initial = untrack(() => position);
  let compatible = $state(untrack(() => !directExtensions.has(path.split('.').at(-1).toLowerCase())));
  let resumeAt = $state(initial);
  let video = $state(null);
  let wrapper = $state(null);
  let failed = $state(false);
  let loading = $state(true);
  let playing = $state(false);
  let duration = $state(0);
  let time = $state(initial);
  let dragging = $state(null);
  let attempt = $state(0);
  let muted = $state(false);
  let chrome = $state(true);
  let interaction = $state(0);
  let menu = $state(false);
  let rate = $state(1);
  let fill = $state(false);
  let cue = $state('');
  let jump = $state(0);
  let atEnd = $state(false);
  const rates = [.75, 1, 1.25, 1.5, 2];

  $effect(() => {
    if (!chrome || !playing || loading || failed || dragging !== null || menu || episodesOpen || interaction < 0) return;
    const timer = setTimeout(() => {
      if (!wrapper?.contains(document.activeElement) || !document.activeElement?.matches(':focus-visible')) chrome = false;
    }, 3200);
    return () => clearTimeout(timer);
  });

  $effect(() => {
    if (!cue || !jump) return;
    const timer = setTimeout(() => cue = '', 720);
    return () => clearTimeout(timer);
  });
  let completed = false;
  let lastSaved = initial;

  const address = $derived((compatible ? '/api/video/hls/index.m3u8?' : '/api/download?') +
    new URLSearchParams({source, path, ...(compatible ? {} : {inline:'1'})}));

  $effect(() => {
    const element = video;
    const url = address;
    const useHls = compatible;
    const start = resumeAt;
    // Re-run on explicit retry of the same URL.
    if (!element || attempt < 0) return;
    let disposed = false;
    let hls;
    untrack(() => { loading = true; failed = false; duration = 0; time = start; });
    if (!useHls || element.canPlayType('application/vnd.apple.mpegurl')) {
      element.src = url;
    } else {
      import('hls.js').then(({default:Hls}) => {
        if (disposed) return;
        if (!Hls.isSupported()) { failed = true; loading = false; return; }
        hls = new Hls({startPosition:start, maxBufferLength:24, maxMaxBufferLength:40, backBufferLength:30});
        hls.on(Hls.Events.ERROR, (_, detail) => {
          if (detail.fatal) { failed = true; loading = false; hls.stopLoad(); }
        });
        hls.loadSource(url);
        hls.attachMedia(element);
      }).catch(() => { if (!disposed) { failed = true; loading = false; } });
    }
    return () => {
      disposed = true;
      remember(true, element);
      hls?.destroy();
      element.pause();
      element.removeAttribute('src');
      element.load();
    };
  });

  function metadata() {
    const total = video.duration;
    if (!Number.isFinite(total) || total <= 0) return;
    duration = total;
    video.playbackRate = rate;
    if (resumeAt > 0 && resumeAt < total - 0.25 && video.currentTime < 0.25) video.currentTime = resumeAt;
  }

  function error() {
    if (!compatible && video.error) {
      resumeAt = Math.max(lastSaved, video.currentTime || 0);
      compatible = true;
      return;
    }
    if (video.error) { failed = true; loading = false; }
  }

  function remember(force = false, element = video) {
    if (!onposition || !element || completed || element.readyState < 2) return;
    const next = Math.floor(element.currentTime);
    if (next !== lastSaved && (force || Math.abs(next - lastSaved) >= 10)) {
      lastSaved = next;
      onposition(next);
    }
  }

  function updateTime() {
    time = video.currentTime;
    remember();
  }

  function ended() {
    completed = true;
    atEnd = true;
    chrome = true;
    playing = false;
    lastSaved = 0;
    onposition?.(0);
    onended?.();
  }

  function seek(next) {
    if (!duration) return;
    completed = false;
    atEnd = false;
    video.currentTime = Math.max(0, Math.min(duration, next));
    time = video.currentTime;
  }

  function toggle() {
    reveal();
    if (!video) return;
    if (video.paused) {
      if (video.ended) seek(0);
      void video.play().catch(() => {});
    } else video.pause();
  }

  function retry() {
    resumeAt = Math.max(0, time || lastSaved);
    completed = false;
    atEnd = false;
    compatible = true;
    attempt++;
  }

  function fullscreen() {
    if (document.fullscreenElement) void document.exitFullscreen?.();
    else {
      const surface = wrapper.closest('.watch-shell') ?? wrapper;
      if (surface.requestFullscreen && document.fullscreenEnabled) void surface.requestFullscreen().catch(() => {});
      else video.webkitEnterFullscreen?.();
    }
  }

  function reveal() {
    chrome = true;
    interaction++;
  }

  function toggleChrome() {
    chrome = !chrome;
    interaction++;
  }

  function fastSeek(direction) {
    seek(video.currentTime + 10 * direction);
    cue = direction < 0 ? '−10' : '+10';
    jump++;
    reveal();
  }

  function fastTap(event) {
    const bounds = wrapper.getBoundingClientRect();
    const point = (event.clientX - bounds.left) / bounds.width;
    if (point < .38) fastSeek(-1);
    else if (point > .62) fastSeek(1);
    else toggle();
  }

  function keys(event) {
    if (event.key === 'Escape' && (menu || episodesOpen)) {
      menu = false;
      if (episodesOpen) onepisodes?.();
      event.preventDefault();
      return;
    }
    if (event.target !== wrapper && !event.target.classList?.contains('stage')) return;
    switch (event.key) {
      case ' ': case 'k': toggle(); break;
      case 'ArrowLeft': fastSeek(-1); break;
      case 'ArrowRight': fastSeek(1); break;
      case 'f': fullscreen(); break;
      default: return;
    }
    event.preventDefault();
  }

  function clock(value) {
    const seconds = Math.max(0, Math.floor(value));
    const minutes = Math.floor(seconds / 60);
    const stamp = String(minutes % 60).padStart(minutes >= 60 ? 2 : 1, '0') + ':' + String(seconds % 60).padStart(2,'0');
    return minutes < 60 ? stamp : Math.floor(minutes / 60) + ':' + stamp;
  }
</script>

<div class="player" bind:this={wrapper} role="group" aria-label={t('videoPlayer')} tabindex="0"
    class:quiet={!chrome && !episodesOpen && !menu}
    onkeydown={keys} onpointermove={event => { if (event.pointerType === 'mouse') reveal(); }}
    onfocusin={event => { if (!event.target.classList?.contains('stage')) reveal(); }}
    onfocusout={event => { if (!wrapper?.contains(event.relatedTarget)) interaction++; }}>
  <!-- svelte-ignore a11y_media_has_caption -->
  <video bind:this={video} class:fill playsinline autoplay
    onloadedmetadata={metadata} ondurationchange={metadata} onerror={error} ontimeupdate={updateTime}
    onwaiting={() => loading = true} oncanplay={() => loading = false}
    onplaying={() => { playing = true; loading = false; }}
    onpause={() => { playing = false; reveal(); remember(true); }} onended={ended}></video>
  <button class="stage" type="button" aria-label={t('videoControls')} onclick={toggleChrome} ondblclick={fastTap}></button>

  {#if chrome || failed || atEnd}
    <div class="scrim" aria-hidden="true"></div>
    {#if title}
      <header class="watch-title">
        {#if onclose}<button type="button" class="action" aria-label={t('close')} onclick={onclose}><Icon name="back"/></button>{/if}
        <div class="player-title">
          <strong id="playing-video">{title}</strong>
          {#if subtitle}<small>{subtitle}</small>{/if}
        </div>
        {#if onepisodes}
          <button class="action episode-button" type="button" aria-label={t('videoEpisodes')} aria-expanded={episodesOpen}
            onclick={() => { menu = false; onepisodes(); }}><Icon name="list"/><span>{t('videoEpisodes')}</span></button>
        {/if}
        <button class="action" type="button" aria-label={t('videoSettings')} aria-expanded={menu}
          onclick={() => { if (episodesOpen) onepisodes?.(); menu = !menu; }}><Icon name="settings"/></button>
      </header>
    {/if}

    {#if !loading && !failed}
      <div class="transport">
        <button class="circle skip" type="button" disabled={!duration} onclick={() => fastSeek(-1)} aria-label={t('videoBackTen')}>−10</button>
        <button class="circle play" type="button" onclick={toggle} aria-label={t(playing ? 'pause' : 'videoPlay')}>
          {#if playing}<span aria-hidden="true" class="pause-icon"></span>{:else}<Icon name="play" size={30}/>{/if}
        </button>
        <button class="circle skip" type="button" disabled={!duration} onclick={() => fastSeek(1)} aria-label={t('videoForwardTen')}>+10</button>
      </div>
    {/if}

    <div class="controls">
      <input type="range" min="0" max={duration || 1} step="0.1" value={dragging ?? time}
        style:--played={duration ? 100 * Math.min(duration, dragging ?? time) / duration + '%' : '0%'}
        aria-label={t('videoSeek')} aria-valuetext={clock(dragging ?? time) + ' / ' + clock(duration)} disabled={!duration}
        oninput={event => dragging = Number(event.currentTarget.value)}
        onchange={event => { seek(Number(event.currentTarget.value)); dragging = null; reveal(); }}
        onpointercancel={() => dragging = null} onblur={() => dragging = null} />
      <div class="control-row">
        <span class="clock"><strong>{clock(dragging ?? time)}</strong> <span>/</span> {duration ? clock(duration) : '–:––'}</span>
        <span class="spacer"></span>
        {#if onnext}<button class="action next" type="button" onclick={() => { remember(true); onnext(); }}>
          <span>{t('videoNext')}</span><Icon name="next" size={18}/>
        </button>{/if}
        <button class="action" type="button" onclick={() => { muted = !muted; video.muted = muted; reveal(); }}
          aria-pressed={muted} aria-label={t(muted ? 'videoUnmute' : 'videoMute')}><Icon name={muted ? 'volume_off' : 'volume_up'}/></button>
        {#if !title}
          <button class="action" type="button" aria-label={t('videoSettings')} aria-expanded={menu}
            onclick={() => menu = !menu}><Icon name="settings"/></button>
        {/if}
        <button class="action" type="button" onclick={fullscreen} aria-label={t('videoFullscreen')}><Icon name="fullscreen"/></button>
      </div>
    </div>
  {/if}

  {#if loading || failed}
    <div class="message" role={failed ? 'alert' : 'status'}>
      {#if failed}<p>{t('videoPlaybackFailed')}</p><button class="action" type="button" onclick={retry}>{t('retry')}</button>
      {:else}<span class="spinner"></span><span>{t('videoBuffering')}</span>{/if}
    </div>
  {/if}
  {#if cue}<span class="jump-cue" class:right={cue.startsWith('+')} aria-hidden="true">{cue}</span>{/if}

  {#if episodesOpen || menu}
    <button class="panel-shade" type="button" aria-label={t('close')} onclick={() => {
      if (menu) menu = false;
      else onepisodes?.();
      reveal();
    }}></button>
    {#if episodesOpen}
      {@render episodePanel?.()}
    {:else}
      <section class="settings" aria-label={t('videoSettings')}>
        <header><strong>{t('videoSettings')}</strong><button class="action" type="button"
          aria-label={t('close')} onclick={() => { menu = false; reveal(); }}><Icon name="close"/></button></header>
        <fieldset>
          <legend>{t('videoSpeed')}</legend>
          {#each rates as choice}
            <button class="option" type="button" aria-pressed={rate === choice}
              onclick={() => { rate = choice; video.playbackRate = choice; }}>{choice}×</button>
          {/each}
        </fieldset>
        <fieldset>
          <legend>{t('videoFrame')}</legend>
          <button class="option" type="button" aria-pressed={!fill} onclick={() => fill = false}>{t('videoFit')}</button>
          <button class="option" type="button" aria-pressed={fill} onclick={() => fill = true}>{t('videoFill')}</button>
        </fieldset>
      </section>
    {/if}
  {/if}
</div>

<style>
  .player{position:relative;isolation:isolate;width:100%;height:100%;min-height:180px;background:#000;color:#f7faff;overflow:hidden;container-type:size}
  .player:fullscreen{width:100%;height:100%}
  video,.stage,.scrim,.panel-shade{position:absolute;inset:0;width:100%;height:100%}
  video{object-fit:contain}
  video.fill{object-fit:cover}
  .stage,.panel-shade{border:0;background:none;touch-action:manipulation;cursor:pointer}
  .quiet .stage{cursor:none}
  .stage:focus-visible{outline:2px solid #a9d0ff;outline-offset:-3px}
  .scrim{pointer-events:none;background:linear-gradient(180deg,#000c,transparent 26%,transparent 66%,#000e)}
  .watch-title,.controls,.transport,.message,.settings,.jump-cue{position:absolute}
  .watch-title,.controls{left:0;right:0;padding-inline:max(14px,env(safe-area-inset-left)) max(14px,env(safe-area-inset-right))}
  .watch-title{top:0;display:flex;align-items:center;gap:6px;padding-top:max(7px,env(safe-area-inset-top));text-shadow:0 1px 4px #000;pointer-events:none}
  .watch-title button,.controls button,.controls input{pointer-events:auto}
  .player-title{flex:1;min-width:0;overflow:hidden;padding-inline:5px}
  .player-title strong,.player-title small{display:block;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}
  .player-title strong{font-size:clamp(15px,2.3vw,23px);font-weight:680}
  .player-title small{font-size:12px;color:#d4deef;margin-top:3px}
  .transport{left:50%;top:50%;transform:translate(-50%,-50%);display:flex;align-items:center;gap:clamp(12px,4vw,30px)}
  .circle,.action,.option{display:inline-grid;place-items:center;min-width:44px;min-height:44px;padding:4px 9px;border:0;color:inherit;cursor:pointer;font:inherit}
  .circle{border-radius:50%;width:48px;height:48px;background:#000a;font-size:14px;font-weight:650}
  .circle.play{width:64px;height:64px;background:#f2f6ff;color:#0a111e}
  .pause-icon{width:16px;height:18px;border-inline:5px solid currentColor}
  .action{border-radius:10px;background:none;font-size:13px}
  .action:hover,.action:focus-visible,.circle:focus-visible,.circle.skip:hover{background:#27364f;color:white}
  .action:disabled,.circle:disabled{opacity:.4}
  .episode-button,.next{display:flex;align-items:center;gap:6px;white-space:nowrap;font-weight:620}
  .next{background:#27364fe6;min-height:36px}
  .controls{bottom:0;padding-bottom:max(7px,env(safe-area-inset-bottom));pointer-events:none}
  input{appearance:none;display:block;width:100%;height:28px;margin:0;background:transparent;cursor:pointer;touch-action:none}
  input::-webkit-slider-runnable-track{height:4px;border-radius:999px;background:linear-gradient(90deg,#a5cbff var(--played),#ffffff57 var(--played))}
  input::-webkit-slider-thumb{appearance:none;width:13px;height:13px;margin-top:-4.5px;border-radius:50%;background:#c5ddff}
  input::-moz-range-track{height:4px;border-radius:999px;background:#ffffff57}
  input::-moz-range-progress{height:4px;border-radius:999px;background:#a5cbff}
  input::-moz-range-thumb{width:13px;height:13px;border:0;background:#c5ddff}
  .control-row{display:flex;align-items:center;gap:2px}
  .clock{font-size:13px;white-space:nowrap;color:#d4deef;font-variant-numeric:tabular-nums}
  .clock strong{color:white}
  .spacer{flex:1}
  .message{left:50%;top:50%;transform:translate(-50%,-50%);display:grid;place-items:center;gap:8px;padding:14px 16px;border-radius:14px;background:#101522e8;text-align:center;font-size:14px;max-width:85%}
  .message p{margin:0}
  .spinner{width:23px;height:23px;border-radius:50%;border:2px solid #fff3;border-top-color:#9ccaff;animation:spin 1s linear infinite}
  @keyframes spin{to{transform:rotate(1turn)}}
  .jump-cue{top:50%;left:12%;transform:translateY(-50%);padding:9px 13px;border-radius:24px;background:#000a;font-size:14px;font-weight:650;pointer-events:none}
  .jump-cue.right{left:auto;right:12%}
  .panel-shade{background:#0008}
  .settings{right:12px;top:10px;width:min(330px,calc(100% - 24px));max-height:calc(100% - 20px);overflow:auto;background:#131b2a;border:1px solid #384861;border-radius:16px;padding:8px 12px 14px}
  .settings header{display:flex;align-items:center;justify-content:space-between}
  fieldset{margin:6px 0 0;padding:4px 0 0;border:0}
  legend{font-size:13px;color:#c3cde1}
  .option{min-height:38px;border-radius:8px;background:#242e40;margin:4px 3px 0 0;font-size:13px}
  .option[aria-pressed="true"]{background:#a5cbff;color:#0f1a2c;font-weight:650}
  @container (max-width:440px){.episode-button span{display:none}.control-row .action{padding-inline:5px}.next span{font-size:11px}}
  @container (max-height:420px){.watch-title{padding-top:0}.controls{padding-bottom:3px}.circle.play{width:52px;height:52px}.control-row .action{min-height:36px}}
  @media(prefers-reduced-motion:reduce){.spinner{animation:none}}
</style>
