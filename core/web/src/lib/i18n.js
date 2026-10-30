import { reactive } from 'vue';

// The panel speaks English by default and Persian on request. Strings are
// written in Persian in the source (the panel was built in Persian) and looked
// up in the English dictionaries under src/i18n/en/. A string with no English
// entry shows its Persian text, so a missed translation is visible rather than
// broken. Messages from the server that carry values (an IP, an error) are
// matched by the regular expressions the dictionaries export as `patterns`.
//
// Use $t('…') in templates and t('…') in scripts. Placeholders are {name}:
// t('نسخهٔ {v} نصب شد', { v }) -> "Version 2.6.0 installed". Translate where a
// string is shown, not where it is defined, so a switch re-renders it.

const dicts = import.meta.glob('../i18n/en/*.js', { eager: true });
const EN = {};
const PATTERNS = [];
for (const m of Object.values(dicts)) {
  Object.assign(EN, m.default || {});
  if (m.patterns) PATTERNS.push(...m.patterns);
}

const KEY = 'midos-lang';
function initial() {
  try {
    const v = localStorage.getItem(KEY);
    if (v === 'fa' || v === 'en') return v;
  } catch (_) { /* private mode: use the default */ }
  return 'en';
}

export const i18n = reactive({ lang: initial() });
export const isFa = () => i18n.lang === 'fa';

function patterned(s) {
  for (const [re, rep] of PATTERNS) {
    if (re.test(s)) return s.replace(re, rep);
  }
  return null;
}

export function t(s, vars) {
  if (s === null || s === undefined) return '';
  let out = String(s);
  if (i18n.lang === 'en' && /[؀-ۿ]/.test(out)) {
    const key = out.trim();
    out = EN[key] ?? patterned(key) ?? out;
  }
  if (vars) out = out.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));
  return out;
}

export function apply() {
  const h = document.documentElement;
  h.lang = i18n.lang;
  h.dir = i18n.lang === 'fa' ? 'rtl' : 'ltr';
}

export function setLang(l) {
  i18n.lang = l === 'fa' ? 'fa' : 'en';
  try { localStorage.setItem(KEY, i18n.lang); } catch (_) { /* not remembered */ }
  apply();
}

// the arrow that points forward in the reading direction ("go on" buttons)
export const fwd = () => (i18n.lang === 'fa' ? '←' : '→');
export const back = () => (i18n.lang === 'fa' ? '→' : '←');
// the percent sign of the current language
export const pct = () => (i18n.lang === 'fa' ? '٪' : '%');
// the locale for numbers and dates
export const locale = () => (i18n.lang === 'fa' ? 'fa-IR' : 'en-US');
