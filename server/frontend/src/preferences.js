// Settings still work for the current page when the browser blocks storage.
const sessionPreferences = new Map();

export const preferences = {
  getItem(key) {
    if (sessionPreferences.has(key)) return sessionPreferences.get(key);
    try { return globalThis.localStorage?.getItem(key) ?? null; }
    catch { return null; }
  },
  setItem(key, value) {
    try {
      if (globalThis.localStorage) {
        globalThis.localStorage.setItem(key, value);
        sessionPreferences.delete(key);
        return;
      }
    } catch {}
    sessionPreferences.set(key, String(value));
  },
  storageChanged(key) {
    if (key === null) sessionPreferences.clear();
    else sessionPreferences.delete(key);
  },
};
