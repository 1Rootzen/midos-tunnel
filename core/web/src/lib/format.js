import { i18n, locale, t } from './i18n.js';

// Number and time formatting in the panel's language (Persian or Latin digits).
const formats = {};
function nf(digits = 1) {
  const k = locale() + digits;
  if (!formats[k]) formats[k] = new Intl.NumberFormat(locale(), { maximumFractionDigits: digits });
  return formats[k];
}

// fa formats a number for display; the name predates English and is kept
// because every page uses it
export const fa = (n, digits = 1) => nf(digits).format(Number(n) || 0);

export function bytes(n) {
  n = Number(n) || 0;
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return fa(n) + ' ' + u[i];
}

export const mbit = (bps) => fa((Number(bps) || 0) * 8 / 1e6) + ' Mbit/s';
export const ms = (v) => (v || v === 0) ? fa(Math.round(v)) + ' ms' : '—';

export function dur(sec) {
  sec = Math.max(0, Math.floor(sec || 0));
  const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  if (d) return t('{d} روز {h} ساعت', { d: fa(d), h: fa(h) });
  if (h) return t('{h} ساعت {m} دقیقه', { h: fa(h), m: fa(m) });
  return t('{m} دقیقه', { m: fa(m) });
}

export const since = (unix) => unix ? dur(Date.now() / 1000 - unix) : '—';

// a date and time in the panel's language
export const when = (unix, opts = { dateStyle: 'medium', timeStyle: 'short' }) =>
  unix ? new Date(unix * 1000).toLocaleString(locale(), opts) : '—';

export function randomKey() {
  const b = new Uint8Array(24);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

// split turns "443, 8443:2053 8000-8010" (or Persian commas) into a list.
export const split = (v) => String(v || '').split(/[\s,،]+/).map((x) => x.trim()).filter(Boolean);

export { i18n };
