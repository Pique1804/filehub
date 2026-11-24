import { messages } from './i18n-messages.js';
import { preferences } from './preferences.js';

export const languageStorageKey = 'fh_language';

export function resolveLocale(preference, browserLanguages = []) {
  if (preference === 'ru' || preference === 'en') return preference;
  for (const tag of browserLanguages) {
    const language = tag.toLowerCase().split('-')[0];
    if (language === 'ru' || language === 'en') return language;
  }
  return 'en';
}

export function readLanguage() {
  const saved = preferences.getItem(languageStorageKey);
  return saved === 'ru' || saved === 'en' ? saved : 'system';
}

export function saveLanguage(preference) {
  preferences.setItem(languageStorageKey, preference);
}

export function translator(locale) {
  const position = locale === 'ru' ? 0 : 1;
  return (key, values = {}) => {
    const message = messages[key]?.[position] ?? key;
    return message.replace(/\{(\w+)\}/g, (placeholder, name) => Object.hasOwn(values, name) ? values[name] : placeholder);
  };
}

export function formats(locale) {
  const t = translator(locale);
  const numeric = new Intl.NumberFormat(locale, { maximumFractionDigits:1 });
  const percentage = new Intl.NumberFormat(locale, { style:'percent', maximumFractionDigits:0 });
  const day = new Intl.DateTimeFormat(locale);
  const plurals = new Intl.PluralRules(locale);
  const units = locale === 'ru' ? ['Б','КиБ','МиБ','ГиБ','ТиБ'] : ['B','KiB','MiB','GiB','TiB'];
  return {
    number: n => numeric.format(n),
    percent: n => percentage.format(n),
    date: seconds => seconds ? day.format(seconds * 1000) : '',
    plural: (key, n) => t(key + '.' + plurals.select(n), { n:numeric.format(n) }),
    size(n) {
      let unit = 0;
      while (n >= 1024 && unit < units.length - 1) { n /= 1024; unit++; }
      return numeric.format(n) + '\u00A0' + units[unit];
    },
  };
}
