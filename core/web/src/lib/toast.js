import { reactive } from 'vue';
import { LOGIN_REQUIRED } from './api.js';
import { t } from './i18n.js';

export const toasts = reactive([]);
let seq = 0;

export function toast(text, kind = '') {
  const id = ++seq;
  toasts.push({ id, text, kind });
  if (toasts.length > 4) toasts.shift();
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id);
    if (i >= 0) toasts.splice(i, 1);
  }, kind === 'err' ? 5200 : 3600);
}

// fail reports an error, except the "login required" one: the app is already
// on its way back to the login screen. Errors from the server are in Persian
// and shown in the panel's language.
export function fail(e) {
  const msg = (e && e.message) || String(e);
  if (msg !== LOGIN_REQUIRED) toast(t(msg), 'err');
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t('کپی شد'), 'ok');
  } catch (_) {
    toast(t('کپی نشد؛ دستی انتخاب کنید'), 'err');
  }
}
