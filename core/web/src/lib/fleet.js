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
export const KERNEL_MODES = [['esp', 'ESP (IPsec)'], ['espudp', 'ESP روی UDP'], ['gre', 'MOI'], ['fou', 'FOU'], ['ipsec', 'IPsec کامل']];
export const STATE_TEXT = { draft: 'اعمال‌نشده', up: 'متصل', warn: 'فعال، بدون حامل', down: 'خطا', off: 'متوقف', missing: 'تانل پیدا نشد' };
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

// fleetTunnels lists the applied cables between two servers other than this
// one. Those tunnels live on the two servers, so the local list (/api/tunnels)
// never sees them; their health comes from the fleet's live samples instead.
export function fleetTunnels(fleet) {
  if (!fleet || !fleet.nodes) return [];
  const byId = Object.fromEntries(fleet.nodes.map((n) => [n.id, n]));
  return (fleet.links || [])
    .filter((l) => l.tunnel && byId[l.a] && byId[l.b] && byId[l.a].kind !== 'self' && byId[l.b].kind !== 'self')
    .map((l) => {
      const [iran, kharej] = byId[l.a].role === 'kharej' ? [byId[l.b], byId[l.a]] : [byId[l.a], byId[l.b]];
      return { id: l.id, name: l.kind === 'kernel' ? 'kernel' : l.tunnel, from: nodeName(iran), to: nodeName(kharej),
        kind: cableKindText(l, KERNEL_MODES), st: cableState(l, byId), rtt: cableRtt(l, byId),
        ports: portPairs(l.ports), udp: portPairs(l.udp_ports) };
    });
}

export const cableType = (l) => (l.kind === 'direct' && l.reverse ? 'reverse' : l.kind);
// transports whose carrier rides UDP; the rest open a TCP port
export const UDP_CARRIERS = ['kcp', 'quic', 'dns'];

const portTerms = (list) => String(list || '').split(/[\s,،]+/).filter(Boolean);

// portPairs reads a port field as "Iran port -> port on the other server"
// pairs for display: "443" is 443 -> 443, "37826:37825" is 37826 -> 37825,
// "443=10.0.0.5:2096" is 443 -> 10.0.0.5:2096.
export function portPairs(list) {
  return portTerms(list).map((e) => {
    const m = /^([\d-]+)[:=](.+)$/.exec(e);
    return m ? { iran: m[1], remote: m[2] } : { iran: e, remote: e };
  });
}

// remotePorts lists the single ports a field reaches on the other server
// (ranges and custom destinations are left out)
export const remotePorts = (list) => portPairs(list).map((p) => p.remote).filter((r) => /^\d+$/.test(r)).map(Number);

// localPorts lists the ports an Iran server opens for one port field
// ("443, 8443:2053, 8000-8010, 2096=10.0.0.5:2096"): the part before ":" or
// "=", with ranges expanded. Unreadable entries are skipped; the server
// rejects them with a proper message.
export function localPorts(list) {
  const out = [];
  for (const e of String(list || '').split(/[\s,،]+/).filter(Boolean)) {
    const m = /^(\d+)(?:-(\d+))?(?:[:=].*)?$/.exec(e);
    if (!m) continue;
    const a = Number(m[1]), b = Number(m[2] || m[1]);
    for (let p = a; p <= b && p <= 65535 && out.length < 70000; p++) out.push(p);
  }
  return out;
}
