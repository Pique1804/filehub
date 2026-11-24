<script>
  import { readLanguage, resolveLocale, translator } from './i18n.js';

  let { token } = $props();
  const locale = resolveLocale(readLanguage(), navigator.languages);
  const t = translator(locale);
  document.documentElement.lang = locale;
  let showPassword = $state(false);
  let busy = $state(false);
  let error = $state('');

  async function accept(event) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    const body = new URLSearchParams(new FormData(event.currentTarget));
    body.set('token', token);
    try {
      const response = await fetch('/api/invite/accept', { method: 'POST', body });
      if (response.ok) {
        location.replace(location.pathname);
        return;
      }
      const reason = (await response.text()).trim();
      error = ({
        invitation_inactive: 'inviteInactive',
        username_taken: 'inviteUsernameTaken',
        invalid_credentials: 'inviteCredentialsInvalid',
        invitation_folders_unavailable: 'inviteFoldersUnavailable',
        too_many_attempts: 'loginRateLimited',
      })[reason] || (response.status === 403 ? 'loginRequestRejected' : 'loginUnavailable');
    } catch {
      error = 'loginUnavailable';
    } finally { busy = false; }
  }
</script>

<main>
  <form onsubmit={accept}>
    <h1>{t('inviteAcceptTitle')}</h1>
    <p>{t('inviteAcceptDescription')}</p>
    {#if error}<p class="error" role="alert">{t(error)}</p>{/if}
    <label for="invite-username">{t('username')}</label>
    <input id="invite-username" name="username" type="text" autocomplete="username"
      autocapitalize="none" autocorrect="off" spellcheck="false" minlength="1" maxlength="32" pattern="[A-Za-z0-9._\-]+" required />
    <label for="invite-password">{t('password')}</label>
    <div class="password">
      <input id="invite-password" name="password" type={showPassword ? 'text' : 'password'} autocomplete="new-password"
        minlength="12" autocapitalize="none" spellcheck="false" required />
      <button type="button" onclick={() => showPassword = !showPassword}>{t(showPassword ? 'hidePassword' : 'showPassword')}</button>
    </div>
    <p class="hint">{t('profileUsernameHint')}</p>
    <button class="primary" type="submit" disabled={busy}>{t('inviteAcceptSubmit')}</button>
  </form>
</main>

<style>
  main { display: grid; height: 100%; overflow: auto; padding: 20px; place-items: center; }
  form { display: grid; gap: 10px; width: min(100%, 410px); padding: 24px; background: var(--panel); border: 1px solid var(--border); border-radius: 18px; }
  h1 { font-size: 25px; line-height: 1.2; }
  h1, p { margin: 0; }
  p { color: var(--muted); }
  .hint { font-size: 13px; }
  .error { color: var(--err); }
  label { margin-top: 6px; }
  input, button { min-height: 44px; min-width: 0; padding: 8px 11px; border: 1px solid var(--border); border-radius: 9px; background: var(--panel2); color: var(--text); }
  input { width: 100%; font-size: 16px; }
  button { cursor: pointer; }
  .password { display: flex; gap: 6px; }
  .primary { margin-top: 4px; background: var(--accent); border-color: var(--accent); color: #fff; font-weight: 650; }
</style>
