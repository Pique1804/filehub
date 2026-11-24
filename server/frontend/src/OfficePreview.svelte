<script>
  import { onMount } from 'svelte';

  let { src, name, t, download } = $props();
  let status = $state('loading');
  let pages = $state(0);

  onMount(() => {
    const request = new AbortController();
    async function checkPreview() {
      try {
        const response = await fetch(src, {method: 'HEAD', signal: request.signal});
        if (request.signal.aborted) return;
        if (response.ok && response.headers.get('content-type')?.startsWith('application/pdf')) {
          pages = Number(response.headers.get('X-FileHub-Preview-Pages'));
          status = 'ready';
        } else if (response.headers.get('X-FileHub-Preview') === 'empty') {
          status = 'empty';
        } else {
          status = response.status === 413 ? 'tooLarge' : 'failed';
        }
      } catch {
        if (!request.signal.aborted) status = 'failed';
      }
    }
    void checkPreview();
    return () => request.abort();
  });
</script>

{#if status === 'ready'}
  <div class="preview">
    <div class="toolbar">
      {#if pages}<span>{t('officePreviewPages', {n: pages})}</span>{/if}
      <a href={src} target="_blank" rel="noopener">{t('officeOpenPDF')}</a>
    </div>
    <iframe {src} title={name}></iframe>
  </div>
{:else}
  <div class="message" role="status">
    {#if status === 'loading'}
      <p>{t('officePreviewLoading')}</p>
    {:else}
      <p>{t(status === 'empty' ? 'officePreviewEmpty' : status === 'tooLarge' ? 'officePreviewTooLarge' : 'officePreviewFailed')}</p>
      <button type="button" onclick={download}>{t('download')}</button>
    {/if}
  </div>
{/if}

<style>
  .preview { width: 100%; height: 100%; min-width: 0; display: flex; flex-direction: column; gap: 8px; }
  .toolbar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; font-size: 13px; }
  .toolbar span { color: #c8cdd6; }
  .toolbar a { margin-inline-start: auto; color: #e6edf7; padding: 6px 10px; border-radius: 8px; background: #ffffff15; }
  iframe { width: 100%; flex: 1; min-height: 0; border: 0; border-radius: 12px; background: white; }
  .message { text-align: center; color: #e6edf7; padding: 20px 12px; }
  .message p { margin: 0 0 14px; }
  .message button { min-height: 44px; padding: 6px 16px; border: 1px solid #ffffff24; border-radius: 10px; background: #ffffff15; color: inherit; cursor: pointer; }
</style>
