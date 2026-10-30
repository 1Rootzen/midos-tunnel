import { trName } from './constants.js';
import { t } from './i18n.js';

// The fleet: the mother server and the Iran / Kharej servers it manages. These
// helpers are shared by the servers page and the network designer. The Persian
// labels below are keys: render them through $t()/t() so a language switch
// re-renders them.

export const ROLE_FA = { iran: 'ایران', kharej: 'خارج', none: 'فقط مدیر' };
export const NODE_STATE = {
  installing: ['warn', 'در حال نصب'],
  ready: ['ok', 'آماده'],
  error: ['bad', 'خطا در نصب'],
  '': ['', 'اضافه نشده'],
};
export const STEP_ICON = { run: '◌', ok: '✓', bad: '✕', warn: '!', skip: '–' };

export const roleOf = (n, selfRole) => (n.kind === 'self' ? selfRole : n.kind);
export const nodeName = (n) => n.label || n.ip || n.host || n.id;

// cableState reads a cable's health from the live samples of its two servers.
export function cableState(l, byId) {
  if (!l.tunnel) return 'draft';
  const ends = [byId[l.a], byId[l.b]].filter(Boolean);
  if (ends.length < 2) return 'missing';
  if (ends.some((n) => n.stats && n.stats.error)) return 'down';
  if (ends.some((n) => !n.stats)) return 'warn';
  if (l.kind === 'kernel') {
    const st = ends.map((n) => n.stats.kernel);
    if (st.every((s) => s === 'active')) return 'up';
    return st.some((s) => s === 'failed' || !s) ? 'down' : 'off';
  }
  const ts = ends.map((n) => (n.stats.tunnels || []).find((t) => t.name === l.tunnel));
  if (ts.some((t) => !t)) return 'missing';
  if (ts.some((t) => t.active === 'failed')) return 'down';
  if (ts.some((t) => t.active !== 'active')) return 'off';
  return ts.some((t) => t.carriers > 0) ? 'up' : 'warn';
}

export function cableRtt(l, byId) {
  if (l.kind !== 'direct' || !l.tunnel) return null;
  let best = null;
  for (const id of [l.a, l.b]) {
    const n = byId[id];
    const t = n && n.stats && (n.stats.tunnels || []).find((x) => x.name === l.tunnel);
    if (t && t.rtt_ms && (best == null || t.rtt_ms < best)) best = t.rtt_ms;
  }
  return best;
}

export function cableKindText(l, kernelModes) {
  if (l.kind === 'kernel') return t('کرنلی · {mode}', { mode: t((kernelModes.find((m) => m[0] === l.mode) || [null, l.mode || 'ESP'])[1]) });
  const tr = trName(l.transport || 'tls') + (l.transport === 'icmp' ? '' : ' :' + (l.port || ''));
  return l.reverse ? t('معکوس · {tr}', { tr }) : t('مستقیم · {tr}', { tr });
}

export const pct = (used, total) => (total ? (100 * used) / total : 0);
