<script>
  import { onMount } from 'svelte';
  import { formats } from './i18n.js';

  let { source, path, name, onclose, locale, t } = $props();
  const format = $derived(formats(locale));
  let content = $state('');
  let status = $state('loading');
  let copied = $state(false);
  const query = $derived(new URLSearchParams({source, path}));

  async function copyLink() {
    const url = new URL(location.href); url.search = '';
    url.searchParams.set('textSource', source); url.searchParams.set('textPath', path);
    url.hash = encodeURIComponent(source) + '|' + encodeURIComponent(path.split('/').slice(0,-1).join('/'));
    try { await navigator.clipboard.writeText(url.href); copied = true; }
    catch { prompt(t('documentLinkPrompt'), url.href); }
  }

  onMount(() => {
    let disposed = false;
    async function readDocument() {
      try {
        const response = await fetch('/api/text?' + query, {cache:'no-store', signal:AbortSignal.timeout(10000)});
        if (!response.ok) throw new Error();
        const data = await response.json();
        if (typeof data.content !== 'string') throw new Error();
        if (disposed) return;
        content = data.content;
        status = 'ready';
      } catch {
        if (!disposed) status = 'failed';
      }
    }
    void readDocument();
    return () => { disposed = true; };
  });
</script>

<div class="text-backdrop">
  <div class="text-document" role="dialog" aria-modal="true" aria-label={name} tabindex="-1">
    <header>
      <div class="title"><strong>{name}</strong><small>{source} / {path}</small></div>
      <button type="button" onclick={onclose} aria-label={t('closeDocument')}>✕</button>
    </header>
    <div class="actions">
      <span>{t('documentReadMode')}</span>
      <button type="button" onclick={copyLink}>{copied ? t('linkCopied') : t('documentLink')}</button>
    </div>
    {#if status === 'ready'}
      <textarea aria-label={t('documentText')} value={content} readonly placeholder={t('documentEmpty')}></textarea>
      <footer>{format.plural('charCount', content.length)}</footer>
    {:else if status === 'failed'}
      <div class="message" role="alert">{t('documentNotOpened')}</div>
    {:else}
      <div class="message" role="status">{t('documentLoading')}</div>
    {/if}
  </div>
</div>

<style>
  .text-backdrop{position:fixed;inset:0;z-index:1500;background:#11182799;display:flex;align-items:center;justify-content:center;padding:24px;box-sizing:border-box}
  .text-document{width:min(1100px,100%);height:min(860px,94dvh);background:var(--bg,#fff);color:var(--text,#202330);border-radius:18px;box-shadow:0 24px 100px #0004;display:flex;flex-direction:column;overflow:hidden}
  header{display:flex;align-items:center;gap:16px;padding:18px 22px;border-bottom:1px solid var(--border,#dfe3ec)}
  .title{min-width:0;flex:1}.title strong{display:block;font-size:19px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.title small{display:block;opacity:.65;margin-top:5px;overflow-wrap:anywhere}
  button{font:inherit;font-size:13px;cursor:pointer;padding:8px 12px;border:1px solid var(--border,#dfe3ec);border-radius:9px;color:inherit;background:var(--row,#f5f6fa)}
  .actions{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:10px;padding:12px 22px;font-size:12px;color:var(--text2)}
  textarea{flex:1;min-height:140px;resize:none;box-sizing:border-box;width:100%;border:0;border-top:1px solid var(--border,#dfe3ec);border-bottom:1px solid var(--border,#dfe3ec);padding:22px;background:var(--bg,#fff);color:inherit;font:16px/1.65 ui-monospace,SFMono-Regular,Consolas,monospace;tab-size:2}
  .message{flex:1;padding:32px;text-align:center}
  footer{padding:12px 22px;font-size:11px;opacity:.65}
  @media(max-width:650px){.text-backdrop{padding:0}.text-document{height:100dvh;border-radius:0}header,.actions{padding:12px}textarea{padding:14px}footer{padding:10px}}
</style>
