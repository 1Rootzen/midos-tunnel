import { trName, LISTENERS } from './constants.js';
import { t as tx } from './i18n.js';

export const dialsOf = (c) => (c.mode === 'client') !== !!c.reverse;

// summary reduces a tunnel's live data to what a row, a card or the maps show.
export function summary(t) {
  const c = t.config || {};
  const s = t.stats;
  const dials = dialsOf(c);
  let transport = '—';
  if (s && s.endpoint) transport = trName(s.endpoint.transport);
  else if (dials && c.endpoints && c.endpoints.length) transport = c.endpoints.map((e) => trName(e.transport)).join(' / ');
  else if (dials && c.server) transport = trName(c.transport || 'tls');
  else if (!dials) transport = LISTENERS.filter(([key]) => c[key]).map(([, tr]) => trName(tr)).join(' / ') || '—';
  const carriers = s ? s.carriers.length : null;
  const rtts = s ? s.carriers.filter((x) => x.measured).map((x) => x.rtt_ms) : [];
  const rtt = rtts.length ? rtts.reduce((a, b) => a + b, 0) / rtts.length : null;
  let up = 0, down = 0;
  if (s) for (const p of s.ports) { up += p.bytes_up; down += p.bytes_down; }
  const active = t.state.active === 'active';
  const healthy = active && (carriers === null || carriers > 0);
  return { c, s, dials, transport, carriers, rtt, up, down, active, healthy };
}

export function roleText(c) {
  return tx(c.mode === 'client' ? 'ایران' : 'خارج') + ' · ' + tx(c.reverse ? 'معکوس' : 'مستقیم');
}

// health: ok, warn (running without a carrier), bad (failed) or off
export function health(t) {
  const sm = summary(t);
  if (t.state.active === 'failed') return 'bad';
  if (!sm.active) return 'off';
  return sm.healthy ? 'ok' : 'warn';
}
