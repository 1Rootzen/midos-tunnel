import { store } from './store.js';
import { isFa, t } from './i18n.js';

// The panel answers under a random secret path; every call is relative to it.
export const BASE = location.pathname.replace(/\/[^/]*$/, '');

export const LOGIN_REQUIRED = 'ورود لازم است';

const MESSAGES = {
  'captcha': 'کد امنیتی اشتباه است',
  'wrong credentials': 'نام کاربری یا رمز اشتباه است',
  'too many failed attempts, try again later': 'تلاش‌های ناموفق زیاد بود؛ ۱۵ دقیقه دیگر دوباره امتحان کنید',
  'a tunnel with this name exists': 'تانلی با این نام وجود دارد',
  'the current password is wrong': 'رمز فعلی اشتباه است',
  'domain name is not valid (use a name like panel.example.com, not an IP)': 'دامنه معتبر نیست؛ یک نام دامنه مثل panel.example.com بدهید، نه IP',
  'method must be http-01 or tls-alpn-01': 'روش اعتبارسنجی باید HTTP-01 یا TLS-ALPN-01 باشد',
  'TLS-ALPN validates on port 443, so the panel must listen on 443 first': 'روش TLS-ALPN روی پورت ۴۴۳ اعتبارسنجی می‌کند؛ اول پورت پنل را ۴۴۳ کنید یا روش HTTP-01 را انتخاب کنید',
};
// Server errors arrive in English (shown in Persian through MESSAGES) or, from
// the fleet and health checks, in Persian (shown in English through the
// dictionaries, including their patterns for messages that carry values).
export const translate = (m) => (isFa() ? MESSAGES[m] || m : t(m));

// api calls the panel. A 401 ends the session (the app falls back to the
// login screen) unless the caller asked to stay quiet.
export async function api(method, path, body, opts = {}) {
  const init = { method, headers: { 'X-Midos': '1' }, credentials: 'same-origin', cache: 'no-store' };
  if (body instanceof FormData) init.body = body;
  else if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(BASE + path, init);
  if (res.status === 401 && !opts.quiet) {
    store.session = null;
    throw new Error(LOGIN_REQUIRED);
  }
  const ct = res.headers.get('content-type') || '';
  const data = ct.includes('json') ? await res.json() : await res.blob();
  if (!res.ok) {
    const err = new Error(translate((data && data.error) || res.statusText));
    err.data = data; // e.g. the per-step results of a failed provision
    throw err;
  }
  return data;
}

export async function boot() {
  try {
    store.session = await api('GET', '/api/session', undefined, { quiet: true });
  } catch (_) {
    store.session = null;
  }
  store.booted = true;
}
