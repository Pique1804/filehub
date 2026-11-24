<script>
  // Material Symbols, Apache-2.0, bundled from @iconify-json/material-symbols.
  import icons from './material-icons.json';
  let {name, size = 22} = $props();
</script>
<svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html icons[name] || ''}</svg>
<style>svg{flex-shrink:0;display:inline-block;vertical-align:middle}</style>
