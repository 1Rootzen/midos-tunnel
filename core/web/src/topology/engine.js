// The network designer's isometric 3D floor, drawn in SVG with no library so
// it works on a server that cannot reach a CDN. The page owns the model and
// every panel around the floor; this engine only draws the floor, devices and
// cables, and turns pointer input into select / move / cable / pan actions.

import { t } from '../lib/i18n.js';

const NS = 'http://www.w3.org/2000/svg';
const C30 = Math.cos(Math.PI / 6), S30 = Math.sin(Math.PI / 6);
const BOX = 70; // device footprint, world units

// names and labels stay Persian here and go through t() where they are drawn
export const KIND = {
  self: { name: 'سرور مادر', color: '#3d45aa', h: 92 },
  iran: { name: 'سرور ایران', color: '#3ee0a1', h: 68 },
  kharej: { name: 'سرور خارج', color: '#4cc3ff', h: 68 },
};
export const CABLE = {
  direct: { name: 'تانل مستقیم', color: '#6c75e6' },
  reverse: { name: 'تانل معکوس', color: '#f5a524' },
  kernel: { name: 'تانل کرنلی', color: '#f472b6' },
};
// a reverse tunnel is stored as a direct cable that dials the other way
export const cableType = (l) => (l.kind === 'direct' && l.reverse ? 'reverse' : l.kind);
export { KERNEL_MODES } from '../lib/fleet.js';
export const snap = (v) => Math.round(v / 20) * 20;

function el(tag, attrs, parent) {
  const n = document.createElementNS(NS, tag);
  for (const k in attrs) if (attrs[k] !== undefined && attrs[k] !== null) n.setAttribute(k, attrs[k]);
  if (parent) parent.appendChild(n);
  return n;
}
const clear = (g) => { while (g.firstChild) g.removeChild(g.firstChild); };

// ctx supplies the model and receives the user's actions:
//   topo(), selected(), tool(), cableKind(), selfIP(),
//   linkState(l), linkLabel(l), benchSummary(n),
//   select(sel), addLink(aId, bId), moved()
export function createEngine(canvas, ctx) {
  const cam = { x: 0, y: 0, z: 1, r: 0 };
  let drag = null;
  let mouse = null;
  let pending = null;
  let raf = 0;
  let floorKey = '';
  let size = '';

  const rot = (x, y) => [[x, y], [-y, x], [-x, -y], [y, -x]][cam.r & 3];
  const unrot = (x, y) => [[x, y], [y, -x], [-x, -y], [-y, x]][cam.r & 3];
  const Pr = (x, y, z) => ({ x: cam.x + (x - y) * C30 * cam.z, y: cam.y + ((x + y) * S30 - (z || 0)) * cam.z });
  const P = (wx, wy, z) => { const [x, y] = rot(wx, wy); return Pr(x, y, z); };
  const pts = (list) => list.map((p) => p.x.toFixed(1) + ',' + p.y.toFixed(1)).join(' ');
  function toWorld(sx, sy) {
    const u = (sx - cam.x) / (C30 * cam.z), v = (sy - cam.y) / (S30 * cam.z);
    const [x, y] = unrot((u + v) / 2, (v - u) / 2);
    return { x, y };
  }
  const nodeById = (id) => ctx.topo().nodes.find((n) => n.id === id);

  // ------------------------------------------------------------ layers
  const defs = el('defs', null, canvas);
  const lin = (id, a, b, x2, y2) => {
    const g = el('linearGradient', { id, x1: '0', y1: '0', x2: x2 || '1', y2: y2 || '1' }, defs);
    el('stop', { offset: '0', 'stop-color': a }, g);
    el('stop', { offset: '1', 'stop-color': b }, g);
  };
  lin('tpTop', '#323b44', '#232a31');
  lin('tpLeft', '#1d2329', '#14191e', '0', '1');
  lin('tpRight', '#13171c', '#0c0f12', '0', '1');
  const rg = el('radialGradient', { id: 'tpFloor', cx: '0.5', cy: '0.5', r: '0.5' }, defs);
  el('stop', { offset: '0', 'stop-color': 'rgba(84,94,210,0.12)' }, rg);
  el('stop', { offset: '0.6', 'stop-color': 'rgba(84,94,210,0.03)' }, rg);
  el('stop', { offset: '1', 'stop-color': 'rgba(0,0,0,0)' }, rg);
  const glow = el('filter', { id: 'tpGlow', x: '-50%', y: '-50%', width: '200%', height: '200%' }, defs);
  el('feGaussianBlur', { stdDeviation: '3', result: 'b' }, glow);
  const mg = el('feMerge', null, glow);
  el('feMergeNode', { in: 'b' }, mg);
  el('feMergeNode', { in: 'SourceGraphic' }, mg);
  const soft = el('filter', { id: 'tpSoft', x: '-50%', y: '-50%', width: '200%', height: '200%' }, defs);
  el('feGaussianBlur', { stdDeviation: '7' }, soft);

  const root = el('g', null, canvas);
  const floorG = el('g', { class: 'tp-floor-g' }, root);
  const cablesG = el('g', null, root);
  const devsG = el('g', null, root);
  const overlayG = el('g', null, root);

  // ------------------------------------------------------------ drawing
  function floorEllipse(cx, cy, r) {
    const out = [];
    for (let i = 0; i < 28; i++) {
      const a = (2 * Math.PI * i) / 28;
      out.push(Pr(cx + Math.cos(a) * r, cy + Math.sin(a) * r, 0));
    }
    return pts(out);
  }

  function extent() {
    let ext = 360;
    for (const n of ctx.topo().nodes) ext = Math.max(ext, Math.abs(n.x) + 240, Math.abs(n.y) + 240);
    return Math.ceil(ext / 40) * 40;
  }

  function drawFloor(ext) {
    clear(floorG);
    const corners = pts([Pr(-ext, -ext), Pr(ext, -ext), Pr(ext, ext), Pr(-ext, ext)]);
    el('polygon', { points: corners, class: 'tp-floor' }, floorG);
    el('polygon', { points: corners, fill: 'url(#tpFloor)' }, floorG);
    // every grid line in one path: one DOM node instead of hundreds
    let minor = '', major = '';
    for (let v = -ext; v <= ext; v += 40) {
      const a = Pr(v, -ext), b = Pr(v, ext), c = Pr(-ext, v), e = Pr(ext, v);
      const seg = `M${a.x.toFixed(1)},${a.y.toFixed(1)}L${b.x.toFixed(1)},${b.y.toFixed(1)}M${c.x.toFixed(1)},${c.y.toFixed(1)}L${e.x.toFixed(1)},${e.y.toFixed(1)}`;
      if ((v / 40) % 5 === 0) major += seg; else minor += seg;
    }
    el('path', { d: minor, class: 'tp-grid' }, floorG);
    el('path', { d: major, class: 'tp-grid major' }, floorG);
    el('polygon', { points: corners, class: 'tp-floor-edge' }, floorG);
  }

  function chip(parent, x, y, text, cls, color) {
    const g = el('g', { class: 'tp-chip ' + (cls || '') }, parent);
    const fs = Math.max(9, Math.min(14, 11 * cam.z));
    const t = el('text', { x, y, 'text-anchor': 'middle', 'dominant-baseline': 'middle', 'font-size': fs.toFixed(1) }, g);
    t.textContent = text;
    let bb;
    try { bb = t.getBBox(); } catch (_) { bb = { x: x - text.length * 3.5, y: y - 8, width: text.length * 7, height: 16 }; }
    const r = el('rect', { x: bb.x - 7, y: bb.y - 3, width: bb.width + 14, height: bb.height + 6, rx: '4' }, null);
    g.insertBefore(r, t);
    if (color) el('rect', { x: bb.x - 7, y: bb.y - 3, width: '2.5', height: bb.height + 6, fill: color }, g);
  }

  function drawCable(l) {
    const a = nodeById(l.a), b = nodeById(l.b);
    if (!a || !b) return;
    const dist = Math.hypot(a.x - b.x, a.y - b.y);
    const s = P(a.x, a.y, 6), e = P(b.x, b.y, 6);
    const c = P((a.x + b.x) / 2, (a.y + b.y) / 2, 34 + dist * 0.2);
    const mid = { x: 0.25 * s.x + 0.5 * c.x + 0.25 * e.x, y: 0.25 * s.y + 0.5 * c.y + 0.25 * e.y };
    const sh0 = P(a.x, a.y, 0), sh1 = P(b.x, b.y, 0);
    const d = `M${s.x.toFixed(1)},${s.y.toFixed(1)} Q${c.x.toFixed(1)},${c.y.toFixed(1)} ${e.x.toFixed(1)},${e.y.toFixed(1)}`;
    const st = ctx.linkState(l);
    const col = CABLE[cableType(l)].color;
    const sel = ctx.selected();
    const isSel = sel && sel.type === 'link' && sel.id === l.id;
    const grp = el('g', { class: 'tp-cable st-' + st + (isSel ? ' sel' : ''), 'data-link': l.id }, cablesG);
    el('line', { x1: sh0.x, y1: sh0.y, x2: sh1.x, y2: sh1.y, class: 'tp-cable-shadow', filter: 'url(#tpSoft)' }, grp);
    el('path', { d, class: 'tp-cable-hit' }, grp);
    el('path', { d, class: 'tp-cable-under' }, grp);
    el('path', { d, class: 'tp-cable-core', stroke: st === 'down' ? '#ff4d5e' : col, filter: st === 'draft' ? null : 'url(#tpGlow)' }, grp);
    if (st === 'up' || st === 'warn') el('path', { d, class: 'tp-cable-flow' + (l.reverse ? ' rev' : '') }, grp);
    for (const p of [s, e]) el('circle', { cx: p.x, cy: p.y, r: String(4 * cam.z), fill: col, class: 'tp-plug' }, grp);
    chip(grp, mid.x, mid.y - 6 * cam.z, ctx.linkLabel(l), 'tp-chip-cable st-' + st, col);
  }

  function drawDevice(n) {
    const k = KIND[n.kind];
    const [cx, cy] = rot(n.x, n.y);
    const w = BOX / 2, h = k.h;
    const sel = ctx.selected();
    const isSel = sel && sel.type === 'node' && sel.id === n.id;
    const isPending = pending === n.id;
    const grp = el('g', { class: 'tp-dev kind-' + n.kind + ' st-' + (n.kind === 'self' ? 'ready' : n.state || 'draft') + (isSel ? ' sel' : '') + (isPending ? ' pending' : ''), 'data-id': n.id }, devsG);

    el('polygon', { points: floorEllipse(cx, cy, 58), class: 'tp-dev-shadow', filter: 'url(#tpSoft)' }, grp);
    el('polygon', { points: floorEllipse(cx, cy, 52), class: 'tp-dev-ring', stroke: k.color }, grp);
    if (isSel || isPending) el('polygon', { points: floorEllipse(cx, cy, 64), class: 'tp-dev-select', stroke: k.color }, grp);

    const body = el('g', { class: 'tp-dev-body' }, grp);
    const c = (dx, dy, z) => Pr(cx + dx, cy + dy, z);
    el('polygon', { points: pts([c(-w, w, 0), c(w, w, 0), c(w, w, h), c(-w, w, h)]), fill: 'url(#tpLeft)', class: 'tp-face' }, body);
    el('polygon', { points: pts([c(w, -w, 0), c(w, w, 0), c(w, w, h), c(w, -w, h)]), fill: 'url(#tpRight)', class: 'tp-face' }, body);
    el('polygon', { points: pts([c(-w, -w, h), c(w, -w, h), c(w, w, h), c(-w, w, h)]), fill: 'url(#tpTop)', class: 'tp-face' }, body);
    // rack units with status LEDs on the front wall
    const units = n.kind === 'self' ? 5 : 4;
    for (let i = 0; i < units; i++) {
      const z = h * (0.16 + (0.72 * i) / units);
      const a = c(-w + 6, w, z), b = c(w - 6, w, z);
      el('line', { x1: a.x, y1: a.y, x2: b.x, y2: b.y, class: 'tp-slot' }, body);
      const led = c(w - 12, w, z + 6);
      el('circle', { cx: led.x, cy: led.y, r: String(2.1 * cam.z), fill: k.color, class: 'tp-led l' + (i % 3) }, body);
      const led2 = c(w - 20, w, z + 6);
      el('circle', { cx: led2.x, cy: led2.y, r: String(1.6 * cam.z), fill: i % 2 ? '#c7cdd3' : k.color, class: 'tp-led l' + ((i + 1) % 3) }, body);
    }
    for (let dy = -w + 14; dy <= w - 14; dy += 9) {
      const a = c(w, dy, h * 0.3), b = c(w, dy, h * 0.66);
      el('line', { x1: a.x, y1: a.y, x2: b.x, y2: b.y, class: 'tp-vent' }, body);
    }
    const e0 = c(w, w, 0), e1 = c(w, w, h);
    el('line', { x1: e0.x, y1: e0.y, x2: e1.x, y2: e1.y, stroke: k.color, class: 'tp-edge', filter: 'url(#tpGlow)' }, body);
    const i = w - 10;
    el('polygon', { points: pts([c(-i, -i, h), c(i, -i, h), c(i, i, h), c(-i, i, h)]), stroke: k.color, class: 'tp-lid' }, body);
    if (n.kind === 'self') {
      const m = c(0, 0, h);
      el('circle', { cx: m.x, cy: m.y, r: String(6 * cam.z), fill: k.color, filter: 'url(#tpGlow)', class: 'tp-core' }, body);
    }

    const top = c(0, 0, h + 30);
    chip(grp, top.x, top.y, n.kind === 'self' ? (n.label || t('سرور مادر')) + ' ★' : (n.label || t(k.name)), 'tp-chip-dev', k.color);
    const fsz = Math.max(8, Math.min(12, 9.5 * cam.z)).toFixed(1);
    const ipAt = c(0, 0, h + 14);
    const ipt = el('text', { x: ipAt.x, y: ipAt.y, 'text-anchor': 'middle', class: 'tp-ip', 'font-size': fsz }, grp);
    ipt.textContent = n.ip || (n.kind === 'self' ? ctx.selfIP() : t('IP؟'));
    const bs = ctx.benchSummary(n);
    if (bs) {
      const at = c(0, 0, h + 50);
      const bt = el('text', { x: at.x, y: at.y, 'text-anchor': 'middle', direction: 'ltr', class: 'tp-bench-sum', 'font-size': fsz }, grp);
      bt.textContent = bs;
    }
  }

  function render() {
    const W = canvas.clientWidth || 800, H = canvas.clientHeight || 520;
    const sz = W + 'x' + H;
    if (sz !== size) { size = sz; canvas.setAttribute('viewBox', `0 0 ${W} ${H}`); }
    const topo = ctx.topo();
    const ext = extent();
    const key = [cam.x, cam.y, cam.z, cam.r, ext, sz].join('|');
    if (key !== floorKey) { floorKey = key; drawFloor(ext); }
    clear(cablesG);
    for (const l of topo.links) drawCable(l);
    clear(devsG);
    // painter's order: far devices first
    const sorted = topo.nodes.slice().sort((a, b) => { const [ax, ay] = rot(a.x, a.y), [bx, by] = rot(b.x, b.y); return (ax + ay) - (bx + by); });
    for (const n of sorted) drawDevice(n);
    clear(overlayG);
    if (ctx.tool() === 'cable' && pending && mouse) {
      const n = nodeById(pending);
      if (n) {
        const s = P(n.x, n.y, KIND[n.kind].h / 2);
        el('line', { x1: s.x, y1: s.y, x2: mouse.x, y2: mouse.y, class: 'tp-rubber', stroke: CABLE[ctx.cableKind()].color }, overlayG);
      }
    }
  }
  const redraw = () => { if (!raf) raf = requestAnimationFrame(() => { raf = 0; render(); }); };

  // ------------------------------------------------------------ camera
  function zoomAt(f, sx, sy) {
    if (sx === undefined) { sx = canvas.clientWidth / 2; sy = canvas.clientHeight / 2; }
    const w = toWorld(sx, sy);
    cam.z = Math.max(0.35, Math.min(2.6, cam.z * f));
    const p = P(w.x, w.y, 0);
    cam.x += sx - p.x; cam.y += sy - p.y;
    redraw();
  }
  function fit() {
    const W = canvas.clientWidth || 800, H = canvas.clientHeight || 520;
    const nodes = ctx.topo().nodes;
    if (!nodes.length) { cam.x = W / 2; cam.y = H / 2; render(); return; }
    cam.z = 1; cam.x = 0; cam.y = 0;
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const n of nodes) {
      for (const z of [0, KIND[n.kind].h + 40]) {
        for (const [dx, dy] of [[-60, -60], [60, 60], [-60, 60], [60, -60]]) {
          const p = P(n.x + dx, n.y + dy, z);
          x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y); x1 = Math.max(x1, p.x); y1 = Math.max(y1, p.y);
        }
      }
    }
    const z = Math.max(0.45, Math.min(1.5, Math.min((W - 80) / (x1 - x0 || 1), (H - 80) / (y1 - y0 || 1))));
    cam.z = z;
    cam.x = W / 2 - ((x0 + x1) / 2) * z;
    cam.y = H / 2 - ((y0 + y1) / 2) * z;
    render();
  }
  function rotate() { cam.r = (cam.r + 1) & 3; fit(); }

  // ------------------------------------------------------------ pointer
  const local = (e) => { const r = canvas.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
  const capture = (e) => { try { canvas.setPointerCapture(e.pointerId); } catch (_) { /* a pointer the browser no longer tracks */ } };

  function onDown(e) {
    const p = local(e);
    const devEl = e.target.closest('[data-id]');
    const linkEl = e.target.closest('[data-link]');
    if (ctx.tool() === 'cable') {
      if (!devEl) { pending = null; redraw(); return; }
      const id = devEl.getAttribute('data-id');
      if (!pending) { pending = id; redraw(); return; }
      const first = pending;
      pending = null;
      ctx.addLink(first, id);
      redraw();
      return;
    }
    if (devEl) {
      const n = nodeById(devEl.getAttribute('data-id'));
      const w = toWorld(p.x, p.y);
      drag = { kind: 'node', node: n, dx: n.x - w.x, dy: n.y - w.y, moved: false };
      const sel = ctx.selected();
      if (!sel || sel.id !== n.id) ctx.select({ type: 'node', id: n.id });
      capture(e);
      return;
    }
    if (linkEl) { ctx.select({ type: 'link', id: linkEl.getAttribute('data-link') }); return; }
    drag = { kind: 'pan', sx: p.x, sy: p.y, cx: cam.x, cy: cam.y, moved: false };
    capture(e);
  }
  function onMove(e) {
    const p = local(e);
    mouse = p;
    if (!drag) { if (ctx.tool() === 'cable' && pending) redraw(); return; }
    if (drag.kind === 'node') {
      const w = toWorld(p.x, p.y);
      drag.node.x = w.x + drag.dx; drag.node.y = w.y + drag.dy;
      drag.moved = true;
    } else {
      cam.x = drag.cx + (p.x - drag.sx); cam.y = drag.cy + (p.y - drag.sy);
      if (Math.abs(p.x - drag.sx) + Math.abs(p.y - drag.sy) > 3) drag.moved = true;
    }
    redraw();
  }
  function onUp() {
    if (!drag) return;
    if (drag.kind === 'node' && drag.moved) {
      drag.node.x = snap(drag.node.x); drag.node.y = snap(drag.node.y);
      ctx.moved();
    } else if (drag.kind === 'pan' && !drag.moved) {
      ctx.select(null);
    }
    drag = null;
    redraw();
  }
  function onWheel(e) {
    e.preventDefault();
    const p = local(e);
    zoomAt(e.deltaY < 0 ? 1.12 : 1 / 1.12, p.x, p.y);
  }

  canvas.addEventListener('pointerdown', onDown);
  canvas.addEventListener('pointermove', onMove);
  canvas.addEventListener('pointerup', onUp);
  canvas.addEventListener('pointercancel', onUp);
  canvas.addEventListener('wheel', onWheel, { passive: false });
  const ro = new ResizeObserver(redraw);
  ro.observe(canvas);

  return {
    redraw, render, fit, zoomAt, rotate, toWorld,
    cancelCable() { pending = null; redraw(); },
    center: () => toWorld(canvas.clientWidth / 2, canvas.clientHeight / 2),
    destroy() {
      cancelAnimationFrame(raf);
      ro.disconnect();
      canvas.removeEventListener('pointerdown', onDown);
      canvas.removeEventListener('pointermove', onMove);
      canvas.removeEventListener('pointerup', onUp);
      canvas.removeEventListener('pointercancel', onUp);
      canvas.removeEventListener('wheel', onWheel);
    },
  };
}
