import { mount } from 'svelte';
import App from './App.svelte';
import InviteAcceptance from './InviteAcceptance.svelte';
import { preferences } from './preferences.js';
import './app.css';

const savedTheme = preferences.getItem('fh_theme') || 'system';
document.documentElement.dataset.theme = savedTheme === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : savedTheme;

const invite = location.hash.startsWith('#invite=') ? new URLSearchParams(location.hash.slice(1)).get('invite') : null;
const app = mount(invite !== null ? InviteAcceptance : App, {
  target: document.getElementById('app'),
  props: invite !== null ? { token: invite } : {},
});

export default app;
