/* Same preference and tokens as the Filehub web app. */
(()=>{let preference='system';try{preference=localStorage.getItem('fh_theme')||'system';}catch{}const media=matchMedia('(prefers-color-scheme: dark)');const apply=()=>{document.documentElement.dataset.theme=preference==='system'?(media.matches?'dark':'light'):preference;};apply();media.addEventListener('change',apply);})();
